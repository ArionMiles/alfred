package providers

import (
	"context"
	"slices"
	"sort"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

type providerInstanceLister interface {
	ListProviderInstances(context.Context) ([]alfred.ProviderInstance, error)
}

// ModelChoice supplies model metadata and its configured provider display name.
type ModelChoice struct {
	Model        alfred.ModelInfo
	ProviderName string
}

// ModelService owns discovery and validation of the selected model preference.
type ModelService struct {
	instances providerInstanceLister
	providers alfred.ProviderResolver
	selection alfred.ModelSelectionStore
}

// NewModelService composes model discovery with preference persistence.
func NewModelService(instances providerInstanceLister, providers alfred.ProviderResolver, selection alfred.ModelSelectionStore) *ModelService {
	return &ModelService{instances: instances, providers: providers, selection: selection}
}

// ListModels lists configured text-capable models. Unsupported adapters and
// disabled instances are omitted; setup/catalog errors remain actionable.
func (service *ModelService) ListModels(ctx context.Context) ([]ModelChoice, error) {
	const op = "ListModels"
	instances, err := service.instances.ListProviderInstances(ctx)
	if err != nil {
		return nil, errors.B.Op(op).Err(err).Build()
	}
	var choices []ModelChoice
	for _, instance := range instances {
		if !instance.Enabled {
			continue
		}
		models, err := service.providerChoices(ctx, instance)
		if err != nil {
			return nil, errors.B.Op(op).Err(err).Build()
		}
		choices = append(choices, models...)
	}
	sort.Slice(choices, func(i, j int) bool {
		a, b := choices[i], choices[j]
		if a.Model.Name != b.Model.Name {
			return a.Model.Name < b.Model.Name
		}
		if a.ProviderName != b.ProviderName {
			return a.ProviderName < b.ProviderName
		}
		if a.Model.Ref.ProviderID != b.Model.Ref.ProviderID {
			return a.Model.Ref.ProviderID < b.Model.Ref.ProviderID
		}
		return a.Model.Ref.ModelID < b.Model.Ref.ModelID
	})
	return choices, nil
}

// GetSelectedModel reads the saved reference, including when temporarily
// unavailable. NotFound means no selection has been made.
func (service *ModelService) GetSelectedModel(ctx context.Context) (alfred.ModelRef, error) {
	return service.selection.GetSelectedModel(ctx)
}

// SelectModel validates against the configured runtime before replacing the
// preference. Validation does not make a paid inference or verify account access.
func (service *ModelService) SelectModel(ctx context.Context, ref alfred.ModelRef) error {
	const op = "SelectModel"
	if ref.ProviderID == "" || ref.ModelID == "" {
		return errors.B.Op(op).KindInvalidArgument().UserMsg("Choose a provider and model.").Build()
	}
	provider, err := service.providers.ResolveProvider(ctx, ref.ProviderID)
	if err != nil {
		return errors.B.Op(op).Err(err).Build()
	}
	model, err := provider.ResolveModel(ctx, ref.ModelID)
	if err != nil {
		return errors.B.Op(op).Err(err).Build()
	}
	if !textCapable(model) {
		return errors.B.Op(op).KindUnimplemented().UserMsg("Choose a model that accepts and produces text.").Build()
	}
	if err := service.selection.SetSelectedModel(ctx, ref); err != nil {
		return errors.B.Op(op).Err(err).Build()
	}
	return nil
}

func textCapable(model alfred.ModelInfo) bool {
	return slices.Contains(model.Capabilities.InputModalities, alfred.ContentKindText) &&
		slices.Contains(model.Capabilities.OutputModalities, alfred.ContentKindText)
}

func (service *ModelService) providerChoices(ctx context.Context, instance alfred.ProviderInstance) ([]ModelChoice, error) {
	const op = "providerChoices"
	provider, err := service.providers.ResolveProvider(ctx, instance.ID)
	if errors.WhatKind(err) == errors.Unimplemented {
		return nil, nil
	}
	if err != nil {
		return nil, errors.B.Op(op).Err(err).Build()
	}
	models, err := provider.Models(ctx)
	if err != nil {
		return nil, errors.B.Op(op).Err(err).Build()
	}
	var choices []ModelChoice
	for _, model := range models {
		if textCapable(model) {
			choices = append(choices, ModelChoice{Model: model, ProviderName: instance.Name})
		}
	}
	return choices, nil
}
