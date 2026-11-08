package sqlite

import (
	"testing"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

func TestModelSelectionPersistsReplacementAcrossReopen(t *testing.T) {
	t.Parallel()
	config := newTestConfig(t)
	store, err := Open(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetSelectedModel(t.Context()); errors.WhatKind(err) != errors.NotFound {
		t.Fatalf("unset selection error=%v", err)
	}
	first := alfred.ModelRef{ProviderID: "personal", ModelID: "model-a"}
	second := alfred.ModelRef{ProviderID: "other", ModelID: "model-b"}
	if err := store.SetSelectedModel(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSelectedModel(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Error(err)
		}
	})
	if got, err := reopened.GetSelectedModel(t.Context()); err != nil || got != second {
		t.Fatalf("selection=%v error=%v", got, err)
	}
	for _, invalid := range []alfred.ModelRef{{}, {ProviderID: "personal"}, {ModelID: "model-c"}} {
		if err := reopened.SetSelectedModel(t.Context(), invalid); errors.WhatKind(err) != errors.InvalidArgument {
			t.Fatalf("invalid selection error=%v", err)
		}
	}
	if got, err := reopened.GetSelectedModel(t.Context()); err != nil || got != second {
		t.Fatalf("invalid replacement changed selection=%v error=%v", got, err)
	}
}
