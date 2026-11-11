package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectinprocess"
	"go.uber.org/mock/gomock"

	"github.com/ArionMiles/alfred"
	alfredv1 "github.com/ArionMiles/alfred/gen/alfred/v1"
	"github.com/ArionMiles/alfred/gen/alfred/v1/alfredv1connect"
	"github.com/ArionMiles/alfred/mocks"
	"github.com/ArionMiles/alfred/pkg/transport/rpc"
)

func TestPalettePreservesBothDraftsAndRestoresFocus(t *testing.T) {
	t.Parallel()
	for _, requestedInput := range []bool{false, true} {
		model := readyModel(t)
		model.composer.SetValue("message draft")
		if requestedInput {
			model.gates.apply(inputRequestedEvent(1, "run-1", "input-1", "Which value?"))
		}
		model.focusInput()
		model.gateInput.SetValue("response draft")
		opened, _ := model.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
		model = opened.(Model)
		if model.palette == nil || model.composer.Focused() || model.gateInput.Focused() {
			t.Fatal("palette did not take focus")
		}
		ignored, _ := model.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
		closed, _ := ignored.(Model).Update(tea.KeyPressMsg{Code: tea.KeyEscape})
		model = closed.(Model)
		if model.palette != nil || model.composer.Value() != "message draft" || model.gateInput.Value() != "response draft" {
			t.Fatal("palette changed a draft or failed to dismiss")
		}
		if model.gateInput.Focused() != requestedInput || model.composer.Focused() == requestedInput {
			t.Fatal("palette restored focus to the wrong input")
		}
	}
}

func TestPaletteNavigatesAndExecutesQuit(t *testing.T) {
	t.Parallel()
	model := readyModel(t)
	model.composer.SetValue("keep my draft")
	opened, _ := model.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	model = opened.(Model)
	if len(model.palette.Items()) != len(commands) {
		t.Fatal("palette does not use the command registry")
	}
	for _, step := range []struct {
		key  tea.KeyPressMsg
		want commandID
	}{
		{tea.KeyPressMsg{Code: tea.KeyTab}, quitCommand},
		{tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}, trustCommand},
		{tea.KeyPressMsg{Code: tea.KeyDown}, quitCommand},
		{tea.KeyPressMsg{Code: tea.KeyUp}, trustCommand},
		{tea.KeyPressMsg{Code: tea.KeyTab}, quitCommand},
	} {
		navigated, _ := model.Update(step.key)
		model = navigated.(Model)
		selected, ok := model.palette.SelectedItem().(command)
		if !ok || selected.id != step.want || model.composer.Value() != "keep my draft" {
			t.Fatalf("%s selected %+v, want %v; draft %q", step.key.String(), selected, step.want, model.composer.Value())
		}
	}
	exited, quit := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if quit == nil {
		t.Fatal("palette did not execute Quit")
	}
	if _, ok := quit().(tea.QuitMsg); !ok || exited.(Model).composer.Value() != "keep my draft" {
		t.Fatal("palette quit differs from the slash command or cleared the draft")
	}
}

func TestPaletteTrustActionUsesExistingRPC(t *testing.T) {
	t.Parallel()
	projects := mocks.NewMockProjectStore(gomock.NewController(t))
	projects.EXPECT().SetProjectSkillTrust(gomock.Any(), alfred.ProjectID("project-1"), alfred.ProjectSkillTrustTrusted).Return(nil)
	server := connect.NewServer()
	alfredv1connect.RegisterProjectServiceHandler(server, rpc.NewProjectHandler(nil, nil, projects))
	model := readyModel(t)
	model.clients = rpc.NewClients(connect.NewClient(connectinprocess.New(server)))
	model.composer.SetValue("preserved draft")
	opened, _ := model.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	selected, save := opened.(Model).Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if save == nil || !selected.(Model).settingTrust || selected.(Model).submitting {
		t.Fatal("palette did not start the trust operation")
	}
	updated, _ := selected.(Model).Update(save())
	model = updated.(Model)
	if model.palette != nil || model.project.GetSkillTrust() != alfredv1.ProjectSkillTrust_PROJECT_SKILL_TRUST_TRUSTED ||
		model.composer.Value() != "preserved draft" || !model.composer.Focused() {
		t.Fatal("trust operation did not preserve the draft and restore focus")
	}
}

func TestPaletteReceivesConfiguredShortcut(t *testing.T) {
	t.Parallel()
	config := withDefaults[Config]()
	config.Keybindings.Palette = []string{"ctrl+o"}
	model := NewModel(t.Context(), config, rpc.Clients{}, "")
	model.project, model.projectSkills, model.inspectedSkills = readyModel(t).project, &alfredv1.InspectProjectSkillsResponse{}, true
	unchanged, _ := model.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if unchanged.(Model).palette != nil {
		t.Fatal("replaced shortcut still opens the palette")
	}
	opened, _ := model.Update(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	if opened.(Model).palette == nil {
		t.Fatal("configured shortcut did not open the palette")
	}
	closed, _ := opened.(Model).Update(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	if closed.(Model).palette != nil {
		t.Fatal("configured shortcut did not dismiss the palette")
	}
}

func TestPaletteKeepsLiveGateEventsAndRestoresCurrentFocus(t *testing.T) {
	t.Parallel()
	model := readyModel(t)
	model.composer.SetValue("preserved draft")
	opened, _ := model.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	updated, _ := opened.(Model).Update(sessionActivityMsg{response: &alfredv1.WatchSessionResponse{
		Item: &alfredv1.WatchSessionResponse_Event{Event: inputRequestedEvent(1, "run-1", "input-1", "Which value?")},
	}})
	model = updated.(Model)
	if model.palette == nil || model.currentGateKey() != "input:input-1" || model.gateInput.Focused() {
		t.Fatal("incoming request was lost or took focus from the palette")
	}
	closed, _ := model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	model = closed.(Model)
	if !model.gateInput.Focused() || model.composer.Focused() || model.composer.Value() != "preserved draft" {
		t.Fatal("dismissal did not focus the newly requested input")
	}
	opened, _ = model.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	updated, _ = opened.(Model).Update(sessionActivityMsg{response: &alfredv1.WatchSessionResponse{
		Item: &alfredv1.WatchSessionResponse_Event{Event: &alfredv1.RunEvent{
			Sequence: 2, RunId: "run-1", Payload: &alfredv1.RunEvent_InputReceived{InputReceived: &alfredv1.InputReceived{
				Response: &alfredv1.RunInputResponse{RequestId: "input-1"},
			}},
		}},
	}})
	model = updated.(Model)
	if model.palette == nil || model.currentGateKey() != "" || model.composer.Focused() {
		t.Fatal("resolved request was retained or moved focus under the palette")
	}
	closed, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if !closed.(Model).composer.Focused() {
		t.Fatal("dismissal did not restore composer focus after request resolution")
	}
}

func TestPaletteOverlayResizesWithoutChangingConversationLayout(t *testing.T) {
	t.Parallel()
	model := readyModel(t)
	model.focusInput()
	resized, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model = resized.(Model)
	before := strings.Split(model.View().Content, "\n")
	opened, _ := model.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	model = opened.(Model)
	after := strings.Split(model.View().Content, "\n")
	if len(after) != len(before) || !strings.Contains(after[len(after)-1], "> ") || !strings.Contains(model.View().Content, "Commands") {
		t.Fatalf("palette layout: rows %d/%d, last row %q", len(after), len(before), after[len(after)-1])
	}
	for _, size := range []tea.WindowSizeMsg{{Width: 30, Height: 12}, {Width: 9, Height: 4}} {
		resized, _ := model.Update(size)
		model = resized.(Model)
		if view := model.View().Content; lipgloss.Height(view) > size.Height {
			t.Fatalf("palette overflowed terminal height %d: %q", size.Height, view)
		}
	}
}

func TestPaletteKeepsMouseScrollOutOfConversation(t *testing.T) {
	t.Parallel()
	model := readyModel(t)
	resized, _ := model.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	model = resized.(Model)
	for _, text := range []string{"one", "two", "three", "four", "five", "six"} {
		model.transcript.addMessage(textMessage(alfredv1.MessageRole_MESSAGE_ROLE_USER, text))
	}
	before := model.transcript.viewport.YOffset()
	if before == 0 {
		t.Fatal("test transcript did not exceed the viewport")
	}
	opened, _ := model.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	scrolled, _ := opened.(Model).Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	model = scrolled.(Model)
	if model.transcript.viewport.YOffset() != before {
		t.Fatal("palette mouse input scrolled the conversation")
	}
}
