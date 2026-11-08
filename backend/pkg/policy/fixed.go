// Package policy provides approval-policy implementations for Alfred.
package policy

import (
	"context"

	"github.com/ArionMiles/alfred"
)

// FixedManager returns the same policy decision for every action.
type FixedManager struct {
	decision alfred.PolicyDecision
}

// NewFixedManager creates a policy manager with a fixed decision.
func NewFixedManager(decision alfred.PolicyDecision) *FixedManager {
	return &FixedManager{decision: decision}
}

// Evaluate returns the configured policy decision.
func (manager *FixedManager) Evaluate(
	_ context.Context,
	_ alfred.AuthorizationRequest,
) (alfred.PolicyDecision, error) {
	return manager.decision, nil
}

var _ alfred.PolicyManager = (*FixedManager)(nil)
