package identity

import (
	"encoding/base32"
	"strings"
	"testing"

	"github.com/ArionMiles/alfred"
)

func TestNewCreatesOpaqueFilesystemSafeID(t *testing.T) {
	t.Parallel()

	id := New[alfred.SessionID]()
	if other := New[alfred.SessionID](); other == id {
		t.Fatalf("New() reused ID %q", id)
	}
	value := string(id)
	if value == "" || value == "." || value == ".." || strings.ContainsAny(value, `/\`) {
		t.Fatalf("New() = %q, want a filesystem-safe path component", value)
	}
	decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(value)
	if err != nil {
		t.Fatalf("New() = %q, want unpadded RFC 4648 base32: %v", value, err)
	}
	if len(decoded) < 16 {
		t.Fatalf("New() decoded bytes = %d, want at least 16", len(decoded))
	}
}
