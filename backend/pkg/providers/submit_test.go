package providers_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/mocks"
	"github.com/ArionMiles/alfred/pkg/providers"
	"github.com/ArionMiles/alfred/pkg/storage/sqlite"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

func TestSubmitSavesCredentialSeparatelyFromDatabaseMetadata(t *testing.T) {
	t.Parallel()
	store, submitter := newSubmitter(t)
	result, err := submitter.Submit(t.Context(), store, apiKeySubmission())
	if err != nil || result.Instance == nil {
		t.Fatalf("Submit() = %+v, %v", result, err)
	}
	instance, err := store.GetProviderInstance(t.Context(), "openai")
	if err != nil || instance.CredentialRef == "" || instance.Name != "OpenAI" || !instance.Enabled ||
		instance.Settings["base_url"] != "https://example.com/v1" {
		t.Fatalf("saved instance = %+v, %v", instance, err)
	}
	if _, exists := instance.Settings["api_key"]; exists {
		t.Fatal("secret was persisted as ordinary settings")
	}
	secret, err := store.GetCredential(t.Context(), instance.CredentialRef)
	if err != nil || !strings.Contains(string(secret), `"api_key":"test-secret"`) {
		t.Fatalf("saved credential is missing: %v", err)
	}
	firstRef := instance.CredentialRef
	if _, err := submitter.Submit(t.Context(), store, apiKeySubmission()); err != nil {
		t.Fatalf("replace credential error = %v", err)
	}
	instance, err = store.GetProviderInstance(t.Context(), "openai")
	if err != nil || instance.CredentialRef == firstRef {
		t.Fatal("replacement did not use a fresh reference")
	}
	if _, err := store.GetCredential(t.Context(), firstRef); err != nil {
		t.Fatalf("previous credential was deleted: %v", err)
	}
}

func TestSubmitPreservesExistingEnabledState(t *testing.T) {
	t.Parallel()
	store, submitter := newSubmitter(t)
	if err := store.PutProviderInstance(t.Context(), alfred.ProviderInstance{
		ID: "openai", Type: "openai", Name: "Old client-supplied name", Enabled: false,
	}); err != nil {
		t.Fatal(err)
	}
	result, err := submitter.Submit(t.Context(), store, apiKeySubmission())
	if err != nil || result.Instance == nil || result.Instance.Enabled || result.Instance.Name != "OpenAI" {
		t.Fatalf("Submit() = %+v, %v", result, err)
	}
}

func TestSubmitValidationDoesNotPersistInstance(t *testing.T) {
	t.Parallel()
	store, submitter := newSubmitter(t)
	request := apiKeySubmission()
	delete(request.Values, "api_key")
	result, err := submitter.Submit(t.Context(), store, request)
	if err != nil || result.Instance != nil || len(result.FieldErrors) != 1 {
		t.Fatalf("invalid submission = %+v, %v", result, err)
	}
	if _, err := store.GetProviderInstance(t.Context(), alfred.ProviderID(request.Type)); errors.WhatKind(err) != errors.NotFound {
		t.Fatalf("invalid form created an instance: %v", err)
	}
}

func TestSubmitMetadataFailureCleansOnlyNewCredential(t *testing.T) {
	const op = "TestSubmitMetadataFailureCleansOnlyNewCredential"

	t.Parallel()
	store, submitter := newSubmitter(t)
	if err := store.PutCredential(t.Context(), "previous", []byte("old-secret")); err != nil {
		t.Fatal(err)
	}
	target := mocks.NewMockProviderInstanceStore(gomock.NewController(t))
	target.EXPECT().GetProviderInstance(gomock.Any(), alfred.ProviderID("openai")).Return(alfred.ProviderInstance{
		ID: "openai", Type: "openai", CredentialRef: "previous",
	}, nil)
	var fresh alfred.CredentialRef
	target.EXPECT().PutProviderInstance(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, instance alfred.ProviderInstance) error {
			fresh = instance.CredentialRef
			return errors.B.Op(op).KindConflict().Text("metadata changed").Build()
		},
	)
	if _, err := submitter.Submit(t.Context(), target, apiKeySubmission()); errors.WhatKind(err) != errors.Conflict {
		t.Fatalf("metadata failure = %v", err)
	}
	if fresh == "" || fresh == "previous" {
		t.Fatal("expected a fresh credential reference")
	}
	if _, err := store.GetCredential(t.Context(), fresh); errors.WhatKind(err) != errors.NotFound {
		t.Fatalf("new credential was not cleaned up: %v", err)
	}
	if _, err := store.GetCredential(t.Context(), "previous"); err != nil {
		t.Fatalf("previous credential was lost: %v", err)
	}
}

func newSubmitter(t *testing.T) (*sqlite.Store, *providers.Submitter) {
	t.Helper()
	store, err := sqlite.Open(t.Context(), sqlite.Config{
		Path: filepath.Join(t.TempDir(), "alfred.db"), BusyTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	setup, err := providers.NewSetupService(providers.SetupDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	return store, providers.NewSubmitter(setup, store)
}

func apiKeySubmission() alfred.ProviderSetupSubmission {
	return alfred.ProviderSetupSubmission{
		Type: "openai", MethodID: "api-key",
		Values: map[string]string{"api_key": "test-secret", "base_url": "https://example.com/v1"},
	}
}
