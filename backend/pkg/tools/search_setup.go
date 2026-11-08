package tools

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"sort"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

// SearchSetup manages search configuration without model-provider metadata.
type SearchSetup struct {
	definitions map[string]alfred.SearchBackendDefinition
	store       alfred.SearchSettingsStore
	credentials alfred.CredentialWriter
	references  alfred.IDGenerator[alfred.CredentialRef]
}

// SearchSetupDefinitions returns the setup fields for installed search adapters.
func SearchSetupDefinitions() []alfred.SearchBackendDefinition {
	return []alfred.SearchBackendDefinition{{ID: exaName, Name: "Exa", Fields: []alfred.SetupField{
		{ID: "api_key", Label: "API key", Kind: alfred.SetupFieldSecret, Required: true},
	}}}
}

// NewSearchSetup composes independent search metadata and credential storage.
func NewSearchSetup(definitions []alfred.SearchBackendDefinition, store alfred.SearchSettingsStore, credentials alfred.CredentialWriter,
	references alfred.IDGenerator[alfred.CredentialRef],
) *SearchSetup {
	registered := make(map[string]alfred.SearchBackendDefinition, len(definitions))
	for _, definition := range definitions {
		definition.Fields = slices.Clone(definition.Fields)
		registered[definition.ID] = definition
	}
	return &SearchSetup{definitions: registered, store: store, credentials: credentials, references: references}
}

// List returns independent search backend definitions in canonical-name order.
func (setup *SearchSetup) List() []alfred.SearchBackendDefinition {
	definitions := make([]alfred.SearchBackendDefinition, 0, len(setup.definitions))
	for _, definition := range setup.definitions {
		definition.Fields = slices.Clone(definition.Fields)
		definitions = append(definitions, definition)
	}
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].ID < definitions[j].ID })
	return definitions
}

// Prepare returns a search form without loading or exposing stored secrets.
func (setup *SearchSetup) Prepare(ctx context.Context, backend string) (alfred.SearchSetupForm, error) {
	const op = "Prepare"
	definition, exists := setup.definitions[backend]
	if !exists {
		return alfred.SearchSetupForm{}, errors.B.Op(op).KindNotFound().UserMsg("Search backend is not installed.").Build()
	}
	definition.Fields = slices.Clone(definition.Fields)
	form := alfred.SearchSetupForm{Definition: definition, Values: make(map[string]string)}
	settings, err := setup.store.GetSearchSettings(ctx, backend)
	if errors.WhatKind(err) == errors.NotFound {
		return form, nil
	}
	if err != nil {
		return alfred.SearchSetupForm{}, errors.B.Op(op).Err(err).Build()
	}
	for _, field := range definition.Fields {
		if field.Kind != alfred.SetupFieldSecret {
			form.Values[field.ID] = settings.Settings[field.ID]
		}
	}
	return form, nil
}

type searchCredentialRecord struct {
	Version int    `json:"version"`
	Backend string `json:"backend"`
	// Provider preserves credentials moved from the earlier Exa model-provider setup.
	Provider string            `json:"provider,omitempty"`
	Method   string            `json:"method"`
	Fields   map[string]string `json:"fields"`
}

// Save validates a full submission and swaps in a freshly stored credential.
func (setup *SearchSetup) Save(ctx context.Context, backend string, values map[string]string) ([]alfred.SetupFieldError, error) {
	const op = "Save"
	form, err := setup.Prepare(ctx, backend)
	if err != nil {
		return nil, errors.B.Op(op).Err(err).Build()
	}
	if problems := alfred.ValidateSetupFields(form.Definition.Fields, values); len(problems) != 0 {
		return problems, nil
	}
	settings, err := setup.store.GetSearchSettings(ctx, backend)
	if err != nil && errors.WhatKind(err) != errors.NotFound {
		return nil, errors.B.Op(op).Err(err).Build()
	}
	settings.Backend, settings.Enabled = backend, true
	settings.Settings = maps.Clone(settings.Settings)
	if settings.Settings == nil {
		settings.Settings = make(map[string]string)
	}
	secrets := make(map[string]string)
	for _, field := range form.Definition.Fields {
		if field.Kind == alfred.SetupFieldSecret {
			secrets[field.ID] = values[field.ID]
		} else {
			settings.Settings[field.ID] = values[field.ID]
		}
	}
	ref, err := setup.references.New()
	if err != nil {
		return nil, errors.B.Op(op).Err(err).Build()
	}
	if ref == "" || ref == settings.CredentialRef {
		return nil, errors.B.Op(op).KindConflict().Text("credential reference must be fresh").Build()
	}
	data, err := json.Marshal(searchCredentialRecord{Version: 1, Backend: backend, Method: "api-key", Fields: secrets})
	if err != nil {
		return nil, errors.B.Op(op).Text("encode search credentials").Build()
	}
	if err := setup.credentials.PutCredential(ctx, ref, data); err != nil {
		return nil, errors.B.Op(op).Err(err).Build()
	}
	previous := settings.CredentialRef
	settings.CredentialRef = ref
	if err := setup.store.PutSearchSettings(ctx, settings); err != nil {
		cleanupErr := setup.credentials.DeleteCredential(context.WithoutCancel(ctx), ref)
		return nil, errors.B.Op(op).Err(err).Err(cleanupErr).Build()
	}
	if previous != "" {
		_ = setup.credentials.DeleteCredential(context.WithoutCancel(ctx), previous)
	}
	return nil, nil
}
