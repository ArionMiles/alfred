// Package identity generates opaque identities for Alfred domain types.
package identity

import "crypto/rand"

// New returns a cryptographically random, filesystem-safe identifier.
func New[T ~string]() T {
	return T(rand.Text())
}
