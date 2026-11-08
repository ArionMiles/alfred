package sqlite

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

func TestCredentialProviderLifecycle(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	ref := alfred.CredentialRef("openai-personal")
	if _, err := store.GetCredential(t.Context(), ref); errors.WhatKind(err) != errors.NotFound {
		t.Fatalf("missing credential error = %v, want NotFound", err)
	}

	value := []byte{0, 1, 255, '\n'}
	if err := store.PutCredential(t.Context(), ref, value); err != nil {
		t.Fatalf("PutCredential() error = %v", err)
	}
	value[0] = 99
	got, err := store.GetCredential(t.Context(), ref)
	if err != nil || !bytes.Equal(got, []byte{0, 1, 255, '\n'}) {
		t.Fatalf("GetCredential() = %v, %v", got, err)
	}
	got[0] = 99
	unchanged, err := store.GetCredential(t.Context(), ref)
	if err != nil || unchanged[0] != 0 {
		t.Fatalf("read mutated stored value: %v, %v", unchanged, err)
	}

	if err := store.PutCredential(t.Context(), ref, []byte("replacement")); err != nil {
		t.Fatalf("replace credential error = %v", err)
	}
	got, err = store.GetCredential(t.Context(), ref)
	if err != nil || string(got) != "replacement" {
		t.Fatalf("replacement = %q, %v", got, err)
	}
	for range 2 {
		if err := store.DeleteCredential(t.Context(), ref); err != nil {
			t.Fatalf("DeleteCredential() error = %v", err)
		}
	}
	if _, err := store.GetCredential(t.Context(), ref); errors.WhatKind(err) != errors.NotFound {
		t.Fatalf("deleted credential error = %v, want NotFound", err)
	}
}

func TestCredentialProviderPreservesValueAfterFailedReplacement(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	ref := alfred.CredentialRef("openai-personal")
	if err := store.PutCredential(t.Context(), ref, []byte("original")); err != nil {
		t.Fatalf("PutCredential() error = %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := store.PutCredential(ctx, ref, []byte("secret-must-not-appear-in-error"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled replacement error = %v", err)
	}
	if strings.Contains(err.Error(), "secret-must-not-appear-in-error") {
		t.Fatal("replacement error exposed secret value")
	}
	got, err := store.GetCredential(t.Context(), ref)
	if err != nil || string(got) != "original" {
		t.Fatalf("value after failed replacement = %q, %v", got, err)
	}
}

func TestCredentialProviderPersistsAcrossReopen(t *testing.T) {
	t.Parallel()
	config := newTestConfig(t)
	store, err := Open(t.Context(), config)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := store.PutCredential(t.Context(), "persisted", []byte("token")); err != nil {
		_ = store.Close()
		t.Fatalf("PutCredential() error = %v", err)
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
	got, err := reopened.GetCredential(t.Context(), "persisted")
	if err != nil || string(got) != "token" {
		t.Fatalf("credential after reopen = %q, %v", got, err)
	}
}

func TestCredentialProviderStoresEmptyValue(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	if err := store.PutCredential(t.Context(), "empty", nil); err != nil {
		t.Fatalf("PutCredential() error = %v", err)
	}
	value, err := store.GetCredential(t.Context(), "empty")
	if err != nil || len(value) != 0 {
		t.Fatalf("empty credential = %v, %v", value, err)
	}
}
