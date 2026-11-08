package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	alfredv1 "github.com/ArionMiles/alfred/gen/alfred/v1"
)

func TestNewSessionThroughSlashAliasAndPalette(t *testing.T) {
	t.Parallel()
	for _, entry := range []string{"/new", "/clear", "palette"} {
		t.Run(entry, func(t *testing.T) {
			t.Parallel()
			model := readyModel(t)
			model.sessionID = "old-session"
			model.lastSequence = 9
			model.activeRun = &alfredv1.Run{Id: "old-run", Status: alfredv1.RunStatus_RUN_STATUS_RUNNING}
			model.transcript.addMessage(textMessage(alfredv1.MessageRole_MESSAGE_ROLE_USER, "old history"))
			canceled := false
			model.watch = &sessionWatch{cancel: func() { canceled = true }}
			if entry == "palette" {
				model.composer.SetValue("message draft")
				model.gates.apply(inputRequestedEvent(1, "old-run", "old-input", "Old question"))
				model.gateInput.SetValue("response draft")
				opened, _ := model.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
				model = opened.(Model)
				model.palette.Select(3)
			} else {
				model.composer.SetValue(entry)
			}
			updated, _ := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			model = updated.(Model)
			_, hasGate := model.gates.current()
			if !canceled || model.sessionID != "" || model.lastSequence != 0 || model.activeRun != nil || model.replaying ||
				model.watch != nil || model.palette != nil || len(model.transcript.entries) != 0 || hasGate ||
				model.composer.Value() != "" || model.gateInput.Value() != "" || !model.composer.Focused() || !model.canSubmit() {
				t.Fatal("new session did not detach and clear the conversation")
			}
			stale, _ := model.Update(sessionActivityMsg{sessionID: "old-session", generation: model.watchGeneration - 1})
			if stale.(Model).sessionID != "" {
				t.Fatal("old watch changed the new conversation")
			}
		})
	}
}

func TestNewSessionWaitsForInflightAction(t *testing.T) {
	t.Parallel()
	for _, action := range []string{"submit", "cancel", "gate", "trust"} {
		t.Run(action, func(t *testing.T) {
			t.Parallel()
			model := readyModel(t)
			model.sessionID = "old-session"
			model.composer.SetValue("draft")
			switch action {
			case "submit":
				model.submitting = true
			case "cancel":
				model.canceling = true
			case "gate":
				model.resolvingGate = "input-1"
			case "trust":
				model.settingTrust = true
			}
			updated, _ := model.newSession()
			model = updated.(Model)
			if model.sessionID != "old-session" || model.composer.Value() != "draft" || model.actionErr == nil {
				t.Fatal("new session cleared state during an in-flight action")
			}
		})
	}
}
