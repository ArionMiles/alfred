package sqlite

import (
	"encoding/json"
	"testing"

	"github.com/ArionMiles/alfred/pkg/x/errors"
)

func TestKeyValueStoresJSONApplicationState(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)

	if _, err := store.GetKeyValue(t.Context(), "providers.openai.agent_host_id"); errors.WhatKind(err) != errors.NotFound {
		t.Fatalf("missing value error = %v", err)
	}
	first := json.RawMessage(`{"host_id":"urn:uuid:first"}`)
	if err := store.PutKeyValue(t.Context(), "providers.openai.agent_host_id", first); err != nil {
		t.Fatal(err)
	}
	second := json.RawMessage(`{"host_id":"urn:uuid:second"}`)
	if err := store.PutKeyValue(t.Context(), "providers.openai.agent_host_id", second); err != nil {
		t.Fatal(err)
	}
	stored, err := store.GetKeyValue(t.Context(), "providers.openai.agent_host_id")
	if err != nil || string(stored) != string(second) {
		t.Fatalf("stored value = %s, error = %v", stored, err)
	}
}

func TestKeyValueRejectsInvalidInputs(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)

	if err := store.PutKeyValue(t.Context(), "", json.RawMessage(`{}`)); errors.WhatKind(err) != errors.InvalidArgument {
		t.Fatalf("empty key error = %v", err)
	}
	if err := store.PutKeyValue(t.Context(), "invalid", json.RawMessage(`{`)); errors.WhatKind(err) != errors.InvalidArgument {
		t.Fatalf("invalid JSON error = %v", err)
	}
}
