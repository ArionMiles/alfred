package policy

import (
	"testing"

	"github.com/ArionMiles/alfred"
)

func TestFixedManagerReturnsConfiguredDecision(t *testing.T) {
	want := alfred.PolicyDecision{
		Outcome: alfred.PolicyOutcomeAsk,
		Reason:  "approval required",
		RuleID:  "fixed",
	}
	manager := NewFixedManager(want)

	got, err := manager.Evaluate(t.Context(), alfred.AuthorizationRequest{})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if got != want {
		t.Fatalf("Evaluate() = %#v, want %#v", got, want)
	}
}
