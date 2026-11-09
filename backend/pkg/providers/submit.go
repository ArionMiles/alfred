package providers

import (
	"context"
	"encoding/json"
	"maps"
	"strings"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

// setupTarget persists provider metadata. Failed writes must leave saved
// metadata unchanged.
type setupTarget interface {
	GetProviderInstance(context.Context, alfred.ProviderID) (alfred.ProviderInstance, error)
	PutProviderInstance(context.Context, alfred.ProviderInstance) error
}

// Submitter saves provider form submissions independently of credential backend.
// The application supplies the provider metadata store.
type Submitter struct {
	setup       *SetupService
	credentials alfred.CredentialWriter
	references  alfred.IDGenerator[alfred.CredentialRef]
}

// NewSubmitter composes metadata and credential persistence for onboarding.
func NewSubmitter(
	setup *SetupService,
	credentials alfred.CredentialWriter,
	references alfred.IDGenerator[alfred.CredentialRef],
) *Submitter {
	return &Submitter{setup: setup, credentials: credentials, references: references}
}

// setupCredentialRecord is the setup consumer's credential format, independent
// of the CredentialProvider implementation and ordinary instance settings.
type setupCredentialRecord struct {
	Version  uint32              `json:"version"`
	Provider alfred.ProviderType `json:"provider"`
	Method   string              `json:"method"`
	Fields   map[string]string   `json:"fields"`
}

// Submit validates a full form, saves secrets separately, and replaces metadata.
// It preserves settings outside the selected method. Concurrent successful
// submissions follow the target's replacement semantics; no multi-store
// transaction or remote authentication is implied.
func (submitter *Submitter) Submit(
	ctx context.Context,
	target setupTarget,
	request alfred.ProviderSetupSubmission,
) (alfred.ProviderSetupResult, error) {
	const op = "Submit"

	if strings.TrimSpace(string(request.Type)) == "" {
		return alfred.ProviderSetupResult{}, errors.B.Op(op).KindInvalidArgument().Text("provider type is required").Build()
	}
	instanceID := alfred.ProviderID(request.Type)
	instance, err := target.GetProviderInstance(ctx, instanceID)
	if err != nil && errors.WhatKind(err) != errors.NotFound {
		return alfred.ProviderSetupResult{}, errors.B.Op(op).Text("load setup target").Err(err).Build()
	}
	var existing *alfred.ProviderInstance
	if err == nil {
		existing = &instance
	} else {
		instance = alfred.ProviderInstance{ID: instanceID, Type: request.Type, Enabled: true}
	}
	form, err := submitter.setup.PrepareForm(request.Type, request.MethodID, existing)
	if err != nil {
		return alfred.ProviderSetupResult{}, errors.B.Op(op).Err(err).Build()
	}
	if problems := alfred.ValidateSetupFields(form.Method.Fields, request.Values); len(problems) != 0 {
		return alfred.ProviderSetupResult{FieldErrors: problems}, nil
	}
	instance.Name = form.Method.InstanceName
	settings, secrets := setupSettings(instance.Settings, form.Method, request.Values)
	instance.Settings = settings
	ref, err := submitter.saveCredential(ctx, request, instance.CredentialRef, secrets)
	if err != nil {
		return alfred.ProviderSetupResult{}, errors.B.Op(op).Text("save setup credential").Err(err).Build()
	}
	if ref != "" {
		instance.CredentialRef = ref
	}
	if err := submitter.persistInstance(ctx, target, instance, ref); err != nil {
		return alfred.ProviderSetupResult{}, errors.B.Op(op).Err(err).Build()
	}
	return alfred.ProviderSetupResult{Instance: &instance}, nil
}

func setupSettings(
	previous map[string]string,
	method alfred.ProviderSetupMethod,
	values map[string]string,
) (map[string]string, map[string]string) {
	settings := maps.Clone(previous)
	if settings == nil {
		settings = make(map[string]string)
	}
	secrets := make(map[string]string)
	for _, field := range method.Fields {
		value, supplied := values[field.ID]
		if field.Kind == alfred.SetupFieldSecret {
			delete(settings, field.ID)
			if supplied && value != "" {
				secrets[field.ID] = value
			}
		} else if supplied {
			settings[field.ID] = value
		}
	}
	return settings, secrets
}

func (submitter *Submitter) persistInstance(
	ctx context.Context,
	target setupTarget,
	instance alfred.ProviderInstance,
	ref alfred.CredentialRef,
) error {
	const op = "persistInstance"

	if err := target.PutProviderInstance(ctx, instance); err != nil {
		builder := errors.B.Op(op).Text("save provider settings").Err(err)
		if ref != "" {
			if cleanupErr := submitter.credentials.DeleteCredential(context.WithoutCancel(ctx), ref); cleanupErr != nil {
				builder = builder.Text("new credential cleanup also failed").Err(cleanupErr)
			}
		}
		return builder.Build()
	}
	return nil
}

func (submitter *Submitter) saveCredential(
	ctx context.Context,
	request alfred.ProviderSetupSubmission,
	previous alfred.CredentialRef,
	fields map[string]string,
) (alfred.CredentialRef, error) {
	const op = "saveCredential"

	if len(fields) == 0 {
		return "", nil
	}
	data, err := json.Marshal(setupCredentialRecord{Version: 1, Provider: request.Type, Method: request.MethodID, Fields: fields})
	if err != nil {
		return "", errors.B.Op(op).Text("encode setup credential").Err(err).Build()
	}
	ref, err := submitter.references.New()
	if err != nil {
		return "", errors.B.Op(op).Text("generate credential reference").Err(err).Build()
	}
	if ref == "" || ref == previous {
		return "", errors.B.Op(op).KindConflict().Text("credential generator did not return a fresh reference").Build()
	}
	if err := submitter.credentials.PutCredential(ctx, ref, data); err != nil {
		return "", errors.B.Op(op).Text("write setup credential").Err(err).Build()
	}
	return ref, nil
}
