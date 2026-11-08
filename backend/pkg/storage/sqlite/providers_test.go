package sqlite

import (
	"reflect"
	"testing"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

func TestProviderInstanceStoreLifecycle(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	instance := alfred.ProviderInstance{
		ID: "openai-personal", Type: "openai", Name: "Personal", Enabled: true,
		CredentialRef: "personal-key",
		Settings:      map[string]string{"base_url": "https://example.com/v1"},
	}
	if _, err := store.GetProviderInstance(t.Context(), instance.ID); errors.WhatKind(err) != errors.NotFound {
		t.Fatalf("missing provider error = %v, want NotFound", err)
	}
	if err := store.PutProviderInstance(t.Context(), instance); err != nil {
		t.Fatalf("PutProviderInstance() error = %v", err)
	}
	got, err := store.GetProviderInstance(t.Context(), instance.ID)
	if err != nil || !reflect.DeepEqual(got, instance) {
		t.Fatalf("GetProviderInstance() = %+v, %v; want %+v", got, err, instance)
	}
	instance.Name = "Renamed"
	instance.Enabled = false
	instance.Settings = map[string]string{"base_url": "https://changed.example/v1"}
	if err := store.PutProviderInstance(t.Context(), instance); err != nil {
		t.Fatalf("replace provider error = %v", err)
	}
	got, err = store.GetProviderInstance(t.Context(), instance.ID)
	if err != nil || !reflect.DeepEqual(got, instance) {
		t.Fatalf("replaced provider = %+v, %v; want %+v", got, err, instance)
	}
	// References may belong to an external credential provider, so saving
	// metadata does not require a corresponding local credentials row.
	if err := store.PutCredential(t.Context(), instance.CredentialRef, []byte("token")); err != nil {
		t.Fatalf("PutCredential() error = %v", err)
	}
	for range 2 {
		if err := store.DeleteProviderInstance(t.Context(), instance.ID); err != nil {
			t.Fatalf("DeleteProviderInstance() error = %v", err)
		}
	}
	if _, err := store.GetProviderInstance(t.Context(), instance.ID); errors.WhatKind(err) != errors.NotFound {
		t.Fatalf("deleted provider error = %v, want NotFound", err)
	}
	credential, err := store.GetCredential(t.Context(), instance.CredentialRef)
	if err != nil || string(credential) != "token" {
		t.Fatalf("deleting provider changed credential: %q, %v", credential, err)
	}
}

func TestProviderInstanceStoreListsAllAccountsInIDOrder(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	for _, instance := range []alfred.ProviderInstance{
		{ID: "work", Type: "openai", Enabled: true},
		{ID: "personal", Type: "openai", Enabled: false},
	} {
		if err := store.PutProviderInstance(t.Context(), instance); err != nil {
			t.Fatalf("PutProviderInstance() error = %v", err)
		}
	}
	instances, err := store.ListProviderInstances(t.Context())
	if err != nil || len(instances) != 2 {
		t.Fatalf("ListProviderInstances() = %+v, %v", instances, err)
	}
	if instances[0].ID != "personal" || instances[0].Enabled || instances[1].ID != "work" {
		t.Fatalf("list order or enabled state = %+v", instances)
	}
}

func TestProviderInstanceStorePersistsAcrossReopen(t *testing.T) {
	t.Parallel()
	config := newTestConfig(t)
	store, err := Open(t.Context(), config)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	instance := alfred.ProviderInstance{
		ID: "personal", Type: "openai", CredentialRef: "external-ref",
		Settings: map[string]string{"base_url": "https://example.com"},
	}
	if err := store.PutProviderInstance(t.Context(), instance); err != nil {
		_ = store.Close()
		t.Fatalf("PutProviderInstance() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	reopened, err := Open(t.Context(), config)
	if err != nil {
		t.Fatalf("reopen error = %v", err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	got, err := reopened.GetProviderInstance(t.Context(), instance.ID)
	if err != nil || !reflect.DeepEqual(got, instance) {
		t.Fatalf("reopened provider = %+v, %v; want %+v", got, err, instance)
	}
}

func TestProviderInstanceStoreRejectsUnsupportedSettingsVersion(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	if _, err := store.db.ExecContext(t.Context(), `
		INSERT INTO provider_instances (id, type, name, enabled, credential_ref, settings_json)
		VALUES ('personal', 'openai', '', 1, '', '{"version":999,"settings":{}}')
	`); err != nil {
		t.Fatalf("insert unsupported settings error = %v", err)
	}
	if _, err := store.GetProviderInstance(t.Context(), "personal"); errors.WhatKind(err) != errors.InvalidInput {
		t.Fatalf("unsupported settings error = %v, want InvalidInput", err)
	}
	if _, err := store.ListProviderInstances(t.Context()); errors.WhatKind(err) != errors.InvalidInput {
		t.Fatalf("listing unsupported settings error = %v, want InvalidInput", err)
	}
}
