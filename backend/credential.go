package alfred

import "context"

// CredentialRef identifies a secret managed by the configured credential provider.
// Callers treat the reference as opaque; adapters own its storage mapping.
type CredentialRef string

// CredentialReader reads secrets for the background service.
// Values are opaque bytes; their format belongs to the subsystem using them.
// Implementations must not include secret values in errors or diagnostics.
type CredentialReader interface {
	// GetCredential returns an owned copy of the value, or a NotFound error.
	GetCredential(context.Context, CredentialRef) ([]byte, error)
}

// CredentialWriter manages secrets without exposing read access.
// Implementations must not include secret values in errors or diagnostics.
type CredentialWriter interface {
	// PutCredential creates or replaces the entire value at ref. A failed write
	// must leave the previously stored value intact.
	PutCredential(context.Context, CredentialRef, []byte) error
	// DeleteCredential removes the value. Deleting an absent value succeeds.
	DeleteCredential(context.Context, CredentialRef) error
}

// CredentialProvider combines credential reading and management for storage implementations.
type CredentialProvider interface {
	CredentialReader
	CredentialWriter
}
