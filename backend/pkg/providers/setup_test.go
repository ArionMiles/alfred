package providers

import (
	"testing"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

func TestPrepareFormPreservesSettingsAndOmitsSecrets(t *testing.T) {
	t.Parallel()
	service, err := NewSetupService(SetupDefinitions(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	instance := alfred.ProviderInstance{
		ID: "openai", Type: "openai", CredentialRef: "stored-key",
		Settings: map[string]string{
			"base_url": "https://example.com/v1", "api_key": "must-not-leak", "other": "unused",
		},
	}
	form, err := service.PrepareForm("openai", "api-key", &instance)
	if err != nil {
		t.Fatal(err)
	}
	if form.InstanceID != instance.ID || len(form.Values) != 1 ||
		form.Values["base_url"] != "https://example.com/v1" {
		t.Fatalf("prepared form = %+v", form)
	}
	if !form.Method.Fields[0].Required {
		t.Fatal("credential reference was treated as proof of valid authentication")
	}
}

func TestPreparedFormCannotMutateRegisteredDefinitions(t *testing.T) {
	t.Parallel()
	definitions := SetupDefinitions()
	service, err := NewSetupService(definitions, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	definitions[0].Methods[0].Fields[0].Label = "Changed input"
	form, err := service.PrepareForm("openai", "api-key", nil)
	if err != nil {
		t.Fatal(err)
	}
	form.Method.Fields[0].Label = "Changed form"
	again, err := service.PrepareForm("openai", "api-key", nil)
	if err != nil || again.Method.Fields[0].Label != "API key" {
		t.Fatalf("registered definition changed: %+v, %v", again, err)
	}
}

func TestPrepareFormRejectsUnknownMethodAndMismatchedInstance(t *testing.T) {
	t.Parallel()
	service, err := NewSetupService(SetupDefinitions(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.PrepareForm("openai", "oauth", nil); errors.WhatKind(err) != errors.NotFound {
		t.Fatalf("unimplemented OAuth method error = %v", err)
	}
	if _, err := service.PrepareForm("openai", "api-key", &alfred.ProviderInstance{
		ID: "work", Type: "aws-bedrock",
	}); errors.WhatKind(err) != errors.InvalidArgument {
		t.Fatalf("mismatched instance error = %v", err)
	}
	if _, err := service.PrepareForm("openai", "api-key", &alfred.ProviderInstance{
		ID: "work", Type: "openai",
	}); errors.WhatKind(err) != errors.InvalidArgument {
		t.Fatalf("non-singleton instance error = %v", err)
	}
}
