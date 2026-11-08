package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectinprocess"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ArionMiles/alfred"
	alfredv1 "github.com/ArionMiles/alfred/gen/alfred/v1"
	"github.com/ArionMiles/alfred/gen/alfred/v1/alfredv1connect"
	"github.com/ArionMiles/alfred/mocks"
	"github.com/ArionMiles/alfred/pkg/events"
	"github.com/ArionMiles/alfred/pkg/transport/rpc"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

func TestSessionPickerListsProjectPagesThroughBothEntryPoints(t *testing.T) {
	t.Parallel()
	for _, entry := range []string{"slash", "palette"} {
		t.Run(entry, func(t *testing.T) {
			t.Parallel()
			old := alfred.Session{ID: "old", ProjectID: "project-1", Title: "Earlier", CreatedAt: time.Unix(1, 0).UTC()}
			newer := alfred.Session{ID: "new", ProjectID: old.ProjectID, CreatedAt: time.Unix(2, 0).UTC()}
			after := alfred.SessionCursor{ID: old.ID, CreatedAt: old.CreatedAt}
			store := mocks.NewMockSessionStore(gomock.NewController(t))
			gomock.InOrder(
				store.EXPECT().ListSessions(gomock.Any(), old.ProjectID, alfred.SessionListOptions{Limit: 100}).
					Return(alfred.SessionPage{Sessions: []alfred.Session{old}, Next: &after}, nil),
				store.EXPECT().ListSessions(gomock.Any(), old.ProjectID, alfred.SessionListOptions{Limit: 100, After: &after}).
					Return(alfred.SessionPage{Sessions: []alfred.Session{newer}}, nil),
			)
			server := connect.NewServer()
			alfredv1connect.RegisterSessionServiceHandler(server, rpc.NewSessionHandler(store, nil, nil))
			model := readyModel(t)
			model.clients = rpc.NewClients(connect.NewClient(connectinprocess.New(server)))
			model.sessionID = "old"
			if entry == "slash" {
				model.composer.SetValue("/sessions")
			} else {
				model.composer.SetValue("preserved draft")
				opened, _ := model.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
				model = opened.(Model)
				model.palette.Select(2)
			}
			opened, load := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			model = opened.(Model)
			if model.sessions == nil || !strings.Contains(model.View().Content, "Loading sessions") {
				t.Fatal("session picker did not show its loading state")
			}
			loaded, _ := model.Update(load())
			model = loaded.(Model)
			items := model.palette.Items()
			if len(items) != 2 || items[0].(sessionItem).session.GetId() != "new" ||
				items[0].(sessionItem).Title() != "Untitled session" || !strings.Contains(items[1].(sessionItem).Title(), "current") {
				t.Fatalf("session rows = %+v", items)
			}
			if entry == "palette" && model.composer.Value() != "preserved draft" {
				t.Fatal("palette action cleared the draft")
			}
		})
	}
}

func TestSessionPickerShowsEmptyAndErrorStatesWithoutSwitching(t *testing.T) {
	const op = "TestSessionPickerShowsEmptyAndErrorStatesWithoutSwitching"

	t.Parallel()
	for name, loadErr := range map[string]error{
		"empty": nil,
		"error": errors.B.Op(op).KindUnavailable().UserMsg("session listing unavailable").Build(),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			store := mocks.NewMockSessionStore(gomock.NewController(t))
			store.EXPECT().ListSessions(gomock.Any(), alfred.ProjectID("project-1"), alfred.SessionListOptions{Limit: 100}).
				Return(alfred.SessionPage{}, loadErr)
			server := connect.NewServer()
			alfredv1connect.RegisterSessionServiceHandler(server, rpc.NewSessionHandler(store, nil, nil))
			model := readyModel(t)
			model.clients = rpc.NewClients(connect.NewClient(connectinprocess.New(server)))
			model.sessionID = "original"
			model.composer.SetValue("draft")
			opened, load := model.openSessions()
			loaded, _ := opened.(Model).Update(load())
			model = loaded.(Model)
			want := "No sessions yet."
			if loadErr != nil {
				want = "Could not load sessions"
			}
			if !strings.Contains(model.View().Content, want) || model.sessionID != "original" || model.composer.Value() != "draft" {
				t.Fatalf("session picker state = %q", model.View().Content)
			}
		})
	}
}

func TestSessionPickerIgnoresDismissedRequestAndKeepsCurrentSession(t *testing.T) {
	t.Parallel()
	model := readyModel(t)
	model.sessionID = "original"
	model.gates.apply(inputRequestedEvent(1, "run-old", "input-old", "Old question"))
	model.gateInput.SetValue("response draft")
	opened, _ := model.openSessions()
	first := opened.(Model).sessions
	closed, _ := opened.(Model).Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	reopened, _ := closed.(Model).openSessions()
	model = reopened.(Model)
	second := model.sessions
	ignored, _ := model.Update(sessionsListedMsg{picker: first})
	model = ignored.(Model)
	if model.sessions != second || !model.sessions.loading {
		t.Fatal("old list response replaced the new picker")
	}
	loaded, _ := model.Update(sessionsListedMsg{picker: second, sessions: []*alfredv1.Session{{Id: "original"}}})
	selected, _ := loaded.(Model).Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = selected.(Model)
	if model.sessionID != "original" || model.watchGeneration != 0 || model.gateInput.Value() != "response draft" {
		t.Fatal("selecting the current session reset its attachment or response")
	}
}

func TestSessionSwitchReplaysNewHistoryAndDiscardsOldRequestedInput(t *testing.T) {
	t.Parallel()
	controller := gomock.NewController(t)
	store := mocks.NewMockSessionStore(controller)
	store.EXPECT().GetSession(gomock.Any(), alfred.SessionID("new")).Return(alfred.Session{
		ID: "new", ProjectID: "project-1", CreatedAt: time.Unix(1, 0),
	}, nil)
	reader := mocks.NewMockRunEventReader(controller)
	payloads := []alfred.RunEventPayload{
		alfred.MessageAdded{Message: alfred.Message{Role: alfred.MessageRoleUser, Content: []alfred.ContentPart{alfred.TextContent{Text: "new history"}}}},
		alfred.RunStateChanged{Status: alfred.RunStatusRunning},
	}
	reader.EXPECT().ReadSessionEvents(gomock.Any(), alfred.SessionID("new")).Return(func(yield func(alfred.RunEvent, error) bool) {
		for index, payload := range payloads {
			if !yield(alfred.RunEvent{
				ID: alfred.RunEventID(fmt.Sprintf("event-%d", index)), RunID: "new-run", Sequence: uint64(index + 1),
				OccurredAt: time.Unix(1, 0), Visibility: alfred.RunEventVisibilityUser, Payload: payload,
			}, nil) {
				return
			}
		}
	})
	server := connect.NewServer()
	alfredv1connect.RegisterSessionServiceHandler(server, rpc.NewSessionHandler(store, reader, events.NewBroker()))
	model := readyModel(t)
	defer func() { model.stopWatching() }()
	model.clients = rpc.NewClients(connect.NewClient(connectinprocess.New(server)))
	model.sessionID = "old"
	model.lastSequence = 9
	model.activeRun = &alfredv1.Run{Id: "old-run", Status: alfredv1.RunStatus_RUN_STATUS_RUNNING}
	canceled := false
	model.watch = &sessionWatch{cancel: func() { canceled = true }}
	model.composer.SetValue("message draft")
	model.gates.apply(inputRequestedEvent(1, "old-run", "old-input", "Old question"))
	model.gateInput.SetValue("silently discard this response")
	opened, _ := model.openSessions()
	model = opened.(Model)
	loaded, _ := model.Update(sessionsListedMsg{picker: model.sessions, sessions: []*alfredv1.Session{{
		Id: "new", ProjectId: "project-1", CreatedAt: timestamppb.Now(),
	}}})
	selected, attach := loaded.(Model).Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = selected.(Model)
	if !canceled || model.sessionID != "new" || model.activeRun != nil || model.lastSequence != 0 ||
		model.gateInput.Value() != "" || model.composer.Value() != "message draft" || model.canSubmit() || model.canCancelActiveRun() {
		t.Fatal("switch did not clear old attachment state and preserve the message draft")
	}
	message := batchMessage[sessionActivityMsg](t, attach)
	for {
		updated, receive := model.Update(message)
		model = updated.(Model)
		if message.response.GetCaughtUp() != nil {
			break
		}
		message = batchMessage[sessionActivityMsg](t, receive)
	}
	if model.replaying || model.activeRun.GetId() != "new-run" || model.activeRun.GetStatus() != alfredv1.RunStatus_RUN_STATUS_RUNNING ||
		!strings.Contains(model.transcript.View(40, 5), "new history") || !model.composer.Focused() {
		t.Fatal("selected session history, run state, or focus was not restored")
	}
	stale, _ := model.Update(sessionActivityMsg{sessionID: "old", response: &alfredv1.WatchSessionResponse{}})
	if stale.(Model).activeRun.GetId() != "new-run" {
		t.Fatal("old session activity changed the selected run")
	}
}
