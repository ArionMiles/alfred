// Package identity generates opaque identities for Alfred domain types.
package identity

import "crypto/rand"

// Generator creates cryptographically random, filesystem-safe identifiers.
type Generator[T ~string] struct{}

// New returns a new opaque identifier.
func (Generator[T]) New() (T, error) {
	return T(rand.Text()), nil
}
