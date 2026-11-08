package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"

	alfredv1 "github.com/ArionMiles/alfred/gen/alfred/v1"
)

func newGateInput() textinput.Model {
	input := textinput.New()
	input.Prompt = "> "
	input.Placeholder = "Type a response"
	input.SetWidth(70)
	return input
}

type gateKind uint8

const (
	approvalGate gateKind = iota
	inputGate
)

type interactionGate struct {
	kind     gateKind
	runID    string
	approval *alfredv1.ApprovalRequest
	input    *alfredv1.RunInputRequest
}

func (gate interactionGate) key() string {
	switch gate.kind {
	case approvalGate:
		return "approval:" + gate.approval.GetId()
	case inputGate:
		return "input:" + gate.input.GetId()
	default:
		return ""
	}
}

type pendingGates struct {
	items []interactionGate
}

func (gates pendingGates) current() (interactionGate, bool) {
	if len(gates.items) == 0 {
		return interactionGate{}, false
	}
	return gates.items[len(gates.items)-1], true
}

func (gates pendingGates) contains(key string) bool {
	for _, gate := range gates.items {
		if gate.key() == key {
			return true
		}
	}
	return false
}

func (gates *pendingGates) apply(event *alfredv1.RunEvent) {
	if event == nil {
		return
	}
	if requested := event.GetApprovalRequested().GetRequest(); requested != nil {
		gate := interactionGate{kind: approvalGate, runID: event.GetRunId(), approval: requested}
		gates.remove(gate.key(), "")
		gates.items = append(gates.items, gate)
		return
	}
	if resolved := event.GetApprovalResolved().GetResolution(); resolved != nil {
		gates.remove("approval:"+resolved.GetApprovalId(), "")
		return
	}
	if requested := event.GetInputRequested().GetRequest(); requested != nil {
		gate := interactionGate{kind: inputGate, runID: event.GetRunId(), input: requested}
		gates.remove(gate.key(), "")
		gates.items = append(gates.items, gate)
		return
	}
	if received := event.GetInputReceived().GetResponse(); received != nil {
		gates.remove("input:"+received.GetRequestId(), "")
		return
	}
	if changed := event.GetRunStateChanged(); changed != nil && runIsTerminal(changed.GetStatus()) {
		gates.remove("", event.GetRunId())
	}
}

func (gates *pendingGates) remove(key, runID string) {
	remaining := make([]interactionGate, 0, len(gates.items))
	for _, gate := range gates.items {
		if key != "" && gate.key() == key {
			continue
		}
		if runID != "" && gate.runID == runID {
			continue
		}
		remaining = append(remaining, gate)
	}
	gates.items = remaining
}

func renderInteractionGate(gate interactionGate, input, status string, width int, interrupt string) string {
	availableWidth := max(width-2, 1)
	var title, body, controls string
	switch gate.kind {
	case approvalGate:
		title = "Approval required"
		body = gate.approval.GetReason()
		if body == "" {
			body = "Alfred wants to perform an action that requires approval."
		}
		controls = fmt.Sprintf("[y] Approve  [n] Deny  [%s] Cancel run", interrupt)
	case inputGate:
		title = "Input requested"
		body = contentText(gate.input.GetPrompt())
		if body == "" {
			body = "Alfred needs more information to continue."
		}
		controls = input
	}
	if status != "" {
		controls = status
	}
	content := fmt.Sprintf(
		"%s\n%s\n%s",
		lipgloss.NewStyle().Bold(true).Render(title),
		lipgloss.Wrap(strings.TrimSpace(body), availableWidth, ""),
		controls,
	)
	return lipgloss.NewStyle().
		Border(lipgloss.ThickBorder(), false, false, false, true).
		PaddingLeft(1).
		Render(content)
}
