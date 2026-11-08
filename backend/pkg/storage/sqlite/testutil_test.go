package sqlite

import (
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(t.Context(), newTestConfig(t))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return store
}

func newTestConfig(t *testing.T) Config {
	t.Helper()
	return Config{
		Path:        filepath.Join(t.TempDir(), "alfred.db"),
		BusyTimeout: 5 * time.Second,
	}
}
