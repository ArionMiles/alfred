package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectinprocess"
	"go.uber.org/mock/gomock"

	"github.com/ArionMiles/alfred"
	alfredv1 "github.com/ArionMiles/alfred/gen/alfred/v1"
	"github.com/ArionMiles/alfred/gen/alfred/v1/alfredv1connect"
	"github.com/ArionMiles/alfred/mocks"
	"github.com/ArionMiles/alfred/pkg/transport/rpc"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

func TestModelResolvesProjectThroughRPC(t *testing.T) {
	t.Parallel()

	controller := gomock.NewController(t)
	project := alfred.Project{
		ID:         "project-1",
		Name:       "alfred",
		Root:       "/work/alfred",
		SkillTrust: alfred.ProjectSkillTrustUnknown,
	}
	resolver := mocks.NewMockProjectResolver(controller)
	resolver.EXPECT().ResolveProject(gomock.Any(), project.Root).Return(project, nil)
	inspector := mocks.NewMockProjectSkillInspector(controller)
	inspector.EXPECT().InspectProjectSkills(gomock.Any(), project.ID).Return(
		alfred.ProjectSkillTrustState{ProjectID: project.ID, Trust: project.SkillTrust}, nil,
	)
	connectServer := connect.NewServer()
	alfredv1connect.RegisterProjectServiceHandler(
		connectServer,
		rpc.NewProjectHandler(
			resolver,
			inspector,
			mocks.NewMockProjectStore(controller),
		),
	)
	clients := rpc.NewClients(connect.NewClient(connectinprocess.New(connectServer)))
	model := NewModel(t.Context(), DefaultConfig(), clients, project.Root)

	message := model.Init()()
	updated, command := model.Update(message)
	if command == nil {
		t.Fatal("Update() did not inspect project skills")
	}
	updated, _ = updated.(Model).Update(command())
	ready := updated.(Model)
	view := ready.View().Content
	if !strings.Contains(view, "Project: alfred\nRoot: /work/alfred") || !ready.composer.Focused() {
		t.Fatalf("View() = %q", view)
	}
}

func TestModelPersistsProjectSkillTrustFromGate(t *testing.T) {
	t.Parallel()

	controller := gomock.NewController(t)
	project := alfred.Project{
		ID: "project-1", Name: "alfred", Root: "/work/alfred",
		SkillTrust: alfred.ProjectSkillTrustUnknown,
	}
	resolver := mocks.NewMockProjectResolver(controller)
	resolver.EXPECT().ResolveProject(gomock.Any(), project.Root).Return(project, nil)
	inspector := mocks.NewMockProjectSkillInspector(controller)
	inspector.EXPECT().InspectProjectSkills(gomock.Any(), project.ID).Return(
		alfred.ProjectSkillTrustState{
			ProjectID: project.ID,
			Trust:     project.SkillTrust,
			Roots:     []string{"/work/alfred/.agents/skills"},
		},
		nil,
	)
	projects := mocks.NewMockProjectStore(controller)
	projects.EXPECT().SetProjectSkillTrust(
		gomock.Any(), project.ID, alfred.ProjectSkillTrustTrusted,
	).Return(nil)
	model := newTestModel(t, resolver, inspector, projects, project.Root)

	updated, inspect := model.Update(model.Init()())
	updated, _ = updated.(Model).Update(inspect())
	if view := updated.(Model).View().Content; !strings.Contains(view, "Allow this project to load them?") {
		t.Fatalf("trust gate view = %q", view)
	}
	updated, persist := updated.(Model).Update(tea.KeyPressMsg{Text: "t", Code: 't'})
	if persist == nil {
		t.Fatal("trust key did not persist a decision")
	}
	updated, _ = updated.(Model).Update(persist())
	if view := updated.(Model).View().Content; !strings.Contains(view, "Project skills: trusted") {
		t.Fatalf("trusted view = %q", view)
	}
}

func TestTrustCommandTogglesProjectSkillTrust(t *testing.T) {
	t.Parallel()

	controller := gomock.NewController(t)
	project := alfred.Project{
		ID: "project-1", Name: "alfred", Root: "/work/alfred",
		SkillTrust: alfred.ProjectSkillTrustTrusted,
	}
	resolver := mocks.NewMockProjectResolver(controller)
	resolver.EXPECT().ResolveProject(gomock.Any(), project.Root).Return(project, nil)
	inspector := mocks.NewMockProjectSkillInspector(controller)
	inspector.EXPECT().InspectProjectSkills(gomock.Any(), project.ID).Return(
		alfred.ProjectSkillTrustState{
			ProjectID: project.ID,
			Trust:     project.SkillTrust,
			Roots:     []string{"/work/alfred/.agents/skills"},
		},
		nil,
	)
	projects := mocks.NewMockProjectStore(controller)
	projects.EXPECT().SetProjectSkillTrust(
		gomock.Any(), project.ID, alfred.ProjectSkillTrustDenied,
	).Return(nil)
	model := newTestModel(t, resolver, inspector, projects, project.Root)

	updated, inspect := model.Update(model.Init()())
	updated, _ = updated.(Model).Update(inspect())
	ready := updated.(Model)
	ready.composer.SetValue("/tru")
	updated, persist := ready.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if persist == nil {
		t.Fatal("single /trust suggestion did not execute")
	}
	updated, _ = updated.(Model).Update(persist())
	if view := updated.(Model).View().Content; !strings.Contains(view, "Project skills: denied") {
		t.Fatalf("denied view = %q", view)
	}
}

func TestMessageSubmissionCreatesAndReusesSession(t *testing.T) {
	t.Parallel()

	controller := gomock.NewController(t)
	invocations := mocks.NewMockInvocationService(controller)
	firstRun := alfred.Run{ID: "run-1", SessionID: "session-1", Status: alfred.RunStatusQueued}
	secondRun := alfred.Run{ID: "run-2", SessionID: firstRun.SessionID, Status: alfred.RunStatusQueued}
	thirdRun := alfred.Run{ID: "run-3", SessionID: "session-2", Status: alfred.RunStatusQueued}
	gomock.InOrder(
		invocations.EXPECT().Invoke(gomock.Any(), alfred.InvocationRequest{
			ProjectID: "project-1",
			Source:    alfred.InvocationSourceUser,
			Input:     []alfred.ContentPart{alfred.TextContent{Text: "hello"}},
		}).Return(firstRun, nil),
		invocations.EXPECT().Invoke(gomock.Any(), alfred.InvocationRequest{
			ProjectID: "project-1",
			SessionID: firstRun.SessionID,
			Source:    alfred.InvocationSourceUser,
			Input:     []alfred.ContentPart{alfred.TextContent{Text: "continue"}},
		}).Return(secondRun, nil),
		invocations.EXPECT().Invoke(gomock.Any(), alfred.InvocationRequest{
			ProjectID: "project-1",
			Source:    alfred.InvocationSourceUser,
			Input:     []alfred.ContentPart{alfred.TextContent{Text: "fresh message"}},
		}).Return(thirdRun, nil),
	)
	server := connect.NewServer()
	alfredv1connect.RegisterInvocationServiceHandler(server, rpc.NewInvocationHandler(invocations))
	config := DefaultConfig()
	config.Keybindings.Submit = []string{"ctrl+s"}
	model := NewModel(
		t.Context(), config,
		rpc.NewClients(connect.NewClient(connectinprocess.New(server))),
		"",
	)
	model.project = &alfredv1.Project{Id: "project-1"}

	model.composer.SetValue("hello")
	updated, invoke := model.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if invoke == nil || !updated.(Model).submitting || updated.(Model).composer.Value() != "hello" {
		t.Fatal("first submission did not retain input while awaiting acceptance")
	}
	updated, _ = updated.(Model).Update(batchMessage[invocationCompletedMsg](t, invoke))
	accepted := updated.(Model)
	if accepted.sessionID != "session-1" || accepted.activeRun.GetId() != "run-1" {
		t.Fatalf("accepted run = %v, session ID = %q", accepted.activeRun, accepted.sessionID)
	}
	if accepted.composer.Value() != "" {
		t.Fatalf("accepted composer value = %q, want empty", accepted.composer.Value())
	}
	accepted.applySessionActivity(&alfredv1.WatchSessionResponse{
		Item: &alfredv1.WatchSessionResponse_CaughtUp{CaughtUp: &alfredv1.WatchSessionCaughtUp{}},
	})

	accepted.activeRun.Status = alfredv1.RunStatus_RUN_STATUS_COMPLETED
	accepted.composer.SetValue("continue")
	updated, invoke = accepted.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	updated, _ = updated.(Model).Update(batchMessage[invocationCompletedMsg](t, invoke))
	if runID := updated.(Model).activeRun.GetId(); runID != "run-2" {
		t.Fatalf("active run ID = %q, want run-2", runID)
	}
	reset, _ := updated.(Model).newSession()
	model = reset.(Model)
	model.composer.SetValue("fresh message")
	updated, invoke = model.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	updated, _ = updated.(Model).Update(batchMessage[invocationCompletedMsg](t, invoke))
	if updated.(Model).sessionID != "session-2" || updated.(Model).activeRun.GetId() != "run-3" {
		t.Fatal("first message after New session did not create a separate session")
	}
}

func TestFailedMessageSubmissionRetainsInput(t *testing.T) {
	t.Parallel()

	invocations := mocks.NewMockInvocationService(gomock.NewController(t))
	invocations.EXPECT().Invoke(gomock.Any(), gomock.Any()).Return(
		alfred.Run{}, errors.B.UserMsg("the message could not be sent").Build(),
	)
	server := connect.NewServer()
	alfredv1connect.RegisterInvocationServiceHandler(server, rpc.NewInvocationHandler(invocations))
	model := NewModel(
		t.Context(), DefaultConfig(),
		rpc.NewClients(connect.NewClient(connectinprocess.New(server))),
		"",
	)
	model.project = &alfredv1.Project{Id: "project-1"}
	model.composer.SetValue("retry me")

	updated, invoke := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	updated, _ = updated.(Model).Update(batchMessage[invocationCompletedMsg](t, invoke))
	failed := updated.(Model)
	if failed.actionErr == nil || failed.composer.Value() != "retry me" {
		t.Fatalf("submission error = %v, composer value = %q", failed.actionErr, failed.composer.Value())
	}
}

func TestSessionActivityUpdatesRunAndTranscript(t *testing.T) {
	t.Parallel()

	model := Model{
		activeRun:  &alfredv1.Run{Id: "run-1", Status: alfredv1.RunStatus_RUN_STATUS_QUEUED},
		transcript: newTranscript(),
	}
	model.applySessionActivity(&alfredv1.WatchSessionResponse{
		Item: &alfredv1.WatchSessionResponse_Event{Event: &alfredv1.RunEvent{
			RunId: "run-1", Sequence: 1,
			Payload: &alfredv1.RunEvent_RunStateChanged{RunStateChanged: &alfredv1.RunStateChanged{
				Status: alfredv1.RunStatus_RUN_STATUS_FAILED,
			}},
		}},
	})
	model.applySessionActivity(&alfredv1.WatchSessionResponse{
		Item: &alfredv1.WatchSessionResponse_Event{Event: &alfredv1.RunEvent{
			RunId: "run-1", Sequence: 2,
			Payload: &alfredv1.RunEvent_MessageAdded{MessageAdded: &alfredv1.MessageAdded{
				Message: &alfredv1.Message{
					Role: alfredv1.MessageRole_MESSAGE_ROLE_USER,
					Content: []*alfredv1.ContentPart{{
						Value: &alfredv1.ContentPart_Text{Text: "hello"},
					}},
				},
			}},
		}},
	})

	if model.activeRun.GetStatus() != alfredv1.RunStatus_RUN_STATUS_FAILED {
		t.Fatalf("run status = %v, want failed", model.activeRun.GetStatus())
	}
	if view := model.transcript.View(40, 4); !strings.Contains(view, "┃") || !strings.Contains(view, "hello") {
		t.Fatalf("transcript view = %q", view)
	}
}

func TestSessionActivityTracksCursorAndIgnoresDuplicateEvents(t *testing.T) {
	t.Parallel()

	model := Model{transcript: newTranscript()}
	first := &alfredv1.WatchSessionResponse{
		Item: &alfredv1.WatchSessionResponse_Event{Event: &alfredv1.RunEvent{
			Sequence: 3,
			Payload: &alfredv1.RunEvent_MessageAdded{MessageAdded: &alfredv1.MessageAdded{
				Message: textMessage(alfredv1.MessageRole_MESSAGE_ROLE_USER, "hello"),
			}},
		}},
	}
	model.applySessionActivity(first)
	model.applySessionActivity(first)
	model.applySessionActivity(&alfredv1.WatchSessionResponse{
		Item: &alfredv1.WatchSessionResponse_CaughtUp{CaughtUp: &alfredv1.WatchSessionCaughtUp{
			ThroughSequence: 5,
		}},
	})

	if model.lastSequence != 5 {
		t.Fatalf("last sequence = %d, want 5", model.lastSequence)
	}
	if len(model.transcript.entries) != 1 {
		t.Fatalf("transcript entries = %d, want 1", len(model.transcript.entries))
	}
}

func TestSessionReconnectUsesLastDurableSequence(t *testing.T) {
	t.Parallel()

	sessions := &recordingSessionClient{}
	model := NewModel(t.Context(), DefaultConfig(), rpc.Clients{Session: sessions}, "")
	model.sessionID = "session-1"
	model.lastSequence = 7
	model.watchGeneration = 4
	model.watchState = sessionWatchDisconnected

	updated, reconnect := model.Update(sessionReconnectMsg{
		sessionID: model.sessionID, generation: model.watchGeneration,
	})
	if reconnect == nil || updated.(Model).watchState != sessionWatchReconnecting {
		t.Fatal("reconnect did not enter reconnecting state")
	}
	message := reconnect().(sessionActivityMsg)
	if sessions.request.GetSessionId() != "session-1" || sessions.request.GetAfterSequence() != 7 {
		t.Fatalf("watch request = %v", sessions.request)
	}
	if message.generation != model.watchGeneration {
		t.Fatalf("watch generation = %d, want %d", message.generation, model.watchGeneration)
	}
}

func TestStaleSessionWatchIsCancelledAfterReplacement(t *testing.T) {
	t.Parallel()

	cancelled := false
	stale := &sessionWatch{cancel: func() { cancelled = true }, generation: 2}
	model := NewModel(t.Context(), DefaultConfig(), rpc.Clients{}, "")
	model.sessionID = "session-2"
	model.watchGeneration = 3

	updated, command := model.Update(sessionActivityMsg{
		sessionID: "session-1", generation: stale.generation, watch: stale,
	})
	if command != nil || !cancelled {
		t.Fatalf("stale watch cancellation = %v, command = %T", cancelled, command)
	}
	if updated.(Model).watch != nil {
		t.Fatal("stale watch replaced the current watch")
	}
}

func TestReplacingSessionCancelsWatchAndResetsReplayState(t *testing.T) {
	t.Parallel()

	cancelled := false
	model := NewModel(t.Context(), DefaultConfig(), rpc.Clients{}, "")
	model.sessionID = "session-1"
	model.lastSequence = 9
	model.watchGeneration = 2
	model.watch = &sessionWatch{cancel: func() { cancelled = true }, generation: 2}
	model.transcript.addMessage(textMessage(alfredv1.MessageRole_MESSAGE_ROLE_USER, "old"))

	command := model.replaceSession("session-2")
	if command == nil || !cancelled {
		t.Fatalf("replacement command = %T, cancelled = %v", command, cancelled)
	}
	if model.sessionID != "session-2" || model.lastSequence != 0 || model.watchGeneration != 3 {
		t.Fatalf(
			"replacement state = session %q, sequence %d, generation %d",
			model.sessionID,
			model.lastSequence,
			model.watchGeneration,
		)
	}
	if len(model.transcript.entries) != 0 || model.watchState != sessionWatchConnecting {
		t.Fatalf("replacement transcript/state = %d/%d", len(model.transcript.entries), model.watchState)
	}
}

func TestSessionWatchFailureDisconnectsAndSchedulesReconnect(t *testing.T) {
	t.Parallel()

	cancelled := false
	model := NewModel(t.Context(), DefaultConfig(), rpc.Clients{}, "")
	model.sessionID = "session-1"
	model.watchGeneration = 2
	model.watchState = sessionWatchConnected
	model.watch = &sessionWatch{cancel: func() { cancelled = true }, generation: 2}
	model.transcript.addUpdate(&alfredv1.RunUpdate{
		Payload: &alfredv1.RunUpdate_ContentDelta{ContentDelta: &alfredv1.RunContentDelta{
			Delta: &alfredv1.ContentPart{Value: &alfredv1.ContentPart_Text{Text: "partial"}},
		}},
	})

	updated, reconnect := model.Update(sessionActivityMsg{
		sessionID:  model.sessionID,
		generation: model.watchGeneration,
		watch:      model.watch,
		err:        connect.NewError(connect.CodeUnavailable, "service unavailable"),
	})
	disconnected := updated.(Model)
	if reconnect == nil || !cancelled || disconnected.watch != nil ||
		disconnected.watchState != sessionWatchDisconnected {
		t.Fatalf(
			"disconnect state = command %T, cancelled %v, watch %v, state %d",
			reconnect,
			cancelled,
			disconnected.watch,
			disconnected.watchState,
		)
	}
	if len(disconnected.transcript.provisional) != 0 || !disconnected.transcript.gap {
		t.Fatalf(
			"disconnect transcript = provisional %#v, gap %v",
			disconnected.transcript.provisional,
			disconnected.transcript.gap,
		)
	}
}

func TestSessionWatchStatusIsVisible(t *testing.T) {
	t.Parallel()

	model := readyModel(t)
	model.sessionID = "session-1"
	model.watchState = sessionWatchDisconnected
	model.watchErr = connect.NewError(connect.CodeUnavailable, "service unavailable")
	if view := model.View().Content; !strings.Contains(view, "Session watch: disconnected") {
		t.Fatalf("disconnected view = %q", view)
	}
	model.watchState = sessionWatchReconnecting
	if view := model.View().Content; !strings.Contains(view, "Session watch: reconnecting") {
		t.Fatalf("reconnecting view = %q", view)
	}
}

func TestViewUsesAlternateScreenMouseAndProgramStatus(t *testing.T) {
	t.Parallel()

	model := readyModel(t)
	view := model.View()
	if !view.AltScreen {
		t.Fatal("alternate screen is disabled")
	}
	if view.MouseMode != tea.MouseModeCellMotion {
		t.Fatalf("mouse mode = %v, want cell motion", view.MouseMode)
	}
	if view.ProgramStatus == nil || view.ProgramStatus.State != tea.ProgramStateIdle ||
		view.ProgramStatus.App != "alfred" {
		t.Fatalf("program status = %+v", view.ProgramStatus)
	}
}

func TestMouseWheelScrollsTranscript(t *testing.T) {
	t.Parallel()

	model := readyModel(t)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	model = updated.(Model)
	for _, text := range []string{"one", "two", "three", "four", "five", "six"} {
		model.transcript.addMessage(textMessage(alfredv1.MessageRole_MESSAGE_ROLE_USER, text))
	}
	bottom := model.transcript.viewport.YOffset()
	if bottom == 0 {
		t.Fatal("test transcript did not exceed viewport height")
	}

	updated, _ = model.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	scrolled := updated.(Model)
	if offset := scrolled.transcript.viewport.YOffset(); offset >= bottom {
		t.Fatalf("mouse wheel offset = %d, want less than %d", offset, bottom)
	}
}

func TestProgramStatusFollowsRunLifecycle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		run     alfredv1.RunStatus
		state   tea.ProgramState
		kind    tea.ProgramStatusKind
		message string
	}{
		{name: "queued", run: alfredv1.RunStatus_RUN_STATUS_QUEUED, state: tea.ProgramStateWorking, message: "Run queued"},
		{name: "running", run: alfredv1.RunStatus_RUN_STATUS_RUNNING, state: tea.ProgramStateWorking, message: "Run in progress"},
		{name: "approval", run: alfredv1.RunStatus_RUN_STATUS_WAITING_FOR_APPROVAL, state: tea.ProgramStateBlocked, kind: tea.ProgramStatusKindPermission, message: "Waiting for approval"},
		{name: "input", run: alfredv1.RunStatus_RUN_STATUS_WAITING_FOR_INPUT, state: tea.ProgramStateBlocked, kind: tea.ProgramStatusKindQuestion, message: "Waiting for input"},
		{name: "interrupted", run: alfredv1.RunStatus_RUN_STATUS_INTERRUPTED, state: tea.ProgramStateBlocked, message: "Run interrupted"},
		{name: "completed", run: alfredv1.RunStatus_RUN_STATUS_COMPLETED, state: tea.ProgramStateDone, message: "Run completed"},
		{name: "failed", run: alfredv1.RunStatus_RUN_STATUS_FAILED, state: tea.ProgramStateError, message: "Run failed"},
		{name: "cancelled", run: alfredv1.RunStatus_RUN_STATUS_CANCELLED, state: tea.ProgramStateDone, message: "Run cancelled"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			model := readyModel(t)
			model.activeRun = &alfredv1.Run{Status: test.run}
			status := model.View().ProgramStatus
			if status.State != test.state || status.Kind != test.kind || status.Message != test.message {
				t.Fatalf("program status = %+v", status)
			}
		})
	}
}

func TestEnterCompletesSlashCommandBeforeExecutingIt(t *testing.T) {
	t.Parallel()

	model := NewModel(t.Context(), DefaultConfig(), rpc.Clients{}, "")
	model.composer.SetValue("/")

	updated, command := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if command != nil {
		t.Fatal("command completion unexpectedly executed the command")
	}
	if value := updated.(Model).composer.Value(); value != "/trust" {
		t.Fatalf("composer value = %q, want /trust", value)
	}
}

func TestCommandMenuOverlaysWithoutMovingComposer(t *testing.T) {
	t.Parallel()

	model := Model{
		project: &alfredv1.Project{
			Name:       "alfred",
			Root:       "/work/alfred",
			SkillTrust: alfredv1.ProjectSkillTrust_PROJECT_SKILL_TRUST_DENIED,
		},
		projectSkills:   &alfredv1.InspectProjectSkillsResponse{},
		composer:        newComposer(commands),
		inspectedSkills: true,
		height:          12,
	}
	withoutMenu := strings.Split(model.View().Content, "\n")
	model.composer.SetValue("/")
	withMenu := strings.Split(model.View().Content, "\n")

	if len(withoutMenu) != len(withMenu) {
		t.Fatalf("view height changed from %d to %d", len(withoutMenu), len(withMenu))
	}
	if !strings.Contains(withMenu[len(withMenu)-1], "> ") {
		t.Fatalf("composer moved from final row: %q", withMenu[len(withMenu)-1])
	}
	if strings.Contains(model.composer.suggestionView(), "> ") {
		t.Fatalf("suggestions use the input prompt marker: %q", model.composer.suggestionView())
	}
}

func TestModelShowsProjectResolutionError(t *testing.T) {
	t.Parallel()

	model := Model{}
	updated, _ := model.Update(projectResolvedMsg{err: connect.NewError(
		connect.CodeInvalidArgument,
		"the project directory is invalid",
	)})
	view := updated.(Model).View()
	if view.Content != "Alfred\n\nUnable to open this project: invalid_argument: the project directory is invalid" {
		t.Fatalf("View() = %q", view.Content)
	}
}

func TestModelStartsInProjectResolutionView(t *testing.T) {
	t.Parallel()

	if got := (Model{}).View().Content; got != "Alfred\n\nResolving project…" {
		t.Fatalf("View() = %q", got)
	}
}

func TestQuitCommandExits(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"/quit", "/exit"} {
		model := NewModel(t.Context(), DefaultConfig(), rpc.Clients{}, "")
		model.composer.SetValue(name)
		_, command := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		if command == nil {
			t.Fatalf("%s did not return a command", name)
		}
		if _, ok := command().(tea.QuitMsg); !ok {
			t.Fatalf("%s command returned %T, want tea.QuitMsg", name, command())
		}
	}
}

func TestInterruptDoesNotExitWithoutActiveRun(t *testing.T) {
	t.Parallel()

	model := NewModel(t.Context(), DefaultConfig(), rpc.Clients{}, "")
	_, command := model.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if command != nil {
		t.Fatalf("interrupt command = %T, want no command", command())
	}
}

func TestInterruptCancelsActiveRunThroughRPC(t *testing.T) {
	t.Parallel()

	controller := gomock.NewController(t)
	control := mocks.NewMockRunControl(controller)
	control.EXPECT().CancelRun(gomock.Any(), alfred.RunID("run-1")).Return(nil)
	server := connect.NewServer()
	alfredv1connect.RegisterRunServiceHandler(
		server,
		rpc.NewRunHandler(mocks.NewMockRunStore(controller), control),
	)
	config := DefaultConfig()
	config.Keybindings.Interrupt = []string{"ctrl+x"}
	model := NewModel(
		t.Context(), config,
		rpc.NewClients(connect.NewClient(connectinprocess.New(server))),
		"",
	)
	model.project = readyModel(t).project
	model.inspectedSkills = true
	model.activeRun = &alfredv1.Run{
		Id: "run-1", Status: alfredv1.RunStatus_RUN_STATUS_RUNNING,
	}
	model.transcript.addUpdate(&alfredv1.RunUpdate{
		Payload: &alfredv1.RunUpdate_ContentDelta{ContentDelta: &alfredv1.RunContentDelta{
			Delta: &alfredv1.ContentPart{Value: &alfredv1.ContentPart_Text{Text: "partial"}},
		}},
	})

	if view := model.progressView(); !strings.Contains(view, "ctrl+x to cancel") || strings.Contains(view, "ctrl+c") {
		t.Fatalf("configured progress hint = %q", view)
	}
	unchanged, _ := model.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if unchanged.(Model).canceling {
		t.Fatal("replaced default binding still cancels")
	}

	opened, _ := model.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	model = opened.(Model)
	if model.palette == nil {
		t.Fatal("palette did not open during the active run")
	}
	updated, cancel := model.Update(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
	canceling := updated.(Model)
	if cancel == nil || !canceling.canceling || canceling.palette == nil {
		t.Fatalf("cancel command = %T, canceling = %v", cancel, canceling.canceling)
	}
	updated, _ = canceling.Update(batchMessage[runCanceledMsg](t, cancel))
	cancelled := updated.(Model)
	if cancelled.canceling ||
		cancelled.activeRun.GetStatus() != alfredv1.RunStatus_RUN_STATUS_CANCELLED ||
		len(cancelled.transcript.provisional) != 0 {
		t.Fatalf(
			"cancelled state = canceling %v, status %v, provisional %#v",
			cancelled.canceling,
			cancelled.activeRun.GetStatus(),
			cancelled.transcript.provisional,
		)
	}
}

func TestTerminalRunCannotBeCancelled(t *testing.T) {
	t.Parallel()

	for _, status := range []alfredv1.RunStatus{
		alfredv1.RunStatus_RUN_STATUS_COMPLETED,
		alfredv1.RunStatus_RUN_STATUS_FAILED,
		alfredv1.RunStatus_RUN_STATUS_CANCELLED,
	} {
		model := NewModel(t.Context(), DefaultConfig(), rpc.Clients{}, "")
		model.activeRun = &alfredv1.Run{Id: "run-1", Status: status}
		updated, command := model.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
		if command != nil || updated.(Model).canceling {
			t.Fatalf("status %v produced cancel command %T", status, command)
		}
	}
}

func TestNonterminalRunPreventsConflictingSubmission(t *testing.T) {
	t.Parallel()

	model := readyModel(t)
	model.activeRun = &alfredv1.Run{
		Id: "run-1", Status: alfredv1.RunStatus_RUN_STATUS_QUEUED,
	}
	model.composer.SetValue("start another run")

	updated, command := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	blocked := updated.(Model)
	if command != nil || blocked.submitting || blocked.composer.Value() != "start another run" {
		t.Fatalf(
			"blocked submission = command %T, submitting %v, input %q",
			command,
			blocked.submitting,
			blocked.composer.Value(),
		)
	}
}

func TestProgressSpinnerAppearsAboveComposerForActiveWork(t *testing.T) {
	t.Parallel()

	model := readyModel(t)
	model.height = 12
	model.activeRun = &alfredv1.Run{
		Id: "run-1", Status: alfredv1.RunStatus_RUN_STATUS_RUNNING,
	}
	lines := strings.Split(model.View().Content, "\n")
	if !strings.Contains(lines[len(lines)-2], "Working… · ctrl+c to cancel") {
		t.Fatalf("progress line = %q", lines[len(lines)-2])
	}
	if !strings.Contains(lines[len(lines)-1], "> ") {
		t.Fatalf("composer is not on final row: %q", lines[len(lines)-1])
	}
}

func TestApprovalGateCallsResolveApproval(t *testing.T) {
	t.Parallel()

	controller := gomock.NewController(t)
	control := mocks.NewMockRunControl(controller)
	control.EXPECT().ResolveApproval(gomock.Any(), alfred.RunID("run-1"), alfred.ApprovalResolution{
		ApprovalID: "approval-1",
		Outcome:    alfred.ApprovalOutcomeApproved,
	}).Return(nil)
	server := connect.NewServer()
	alfredv1connect.RegisterRunServiceHandler(
		server,
		rpc.NewRunHandler(mocks.NewMockRunStore(controller), control),
	)
	model := readyModel(t)
	model.clients = rpc.NewClients(connect.NewClient(connectinprocess.New(server)))
	model.composer.SetValue("preserved draft")
	model.gates.apply(approvalRequestedEvent(
		1,
		"run-1",
		"approval-1",
		"Allow the action?",
	))
	model.syncGateFocus("")

	updated, resolve := model.Update(tea.KeyPressMsg{Text: "y", Code: 'y'})
	resolving := updated.(Model)
	if resolve == nil || resolving.resolvingGate != "approval:approval-1" {
		t.Fatalf("resolve command = %T, resolving gate = %q", resolve, resolving.resolvingGate)
	}
	updated, _ = resolving.Update(resolve())
	resolved := updated.(Model)
	if resolved.currentGateKey() != "" || resolved.composer.Value() != "preserved draft" ||
		!resolved.composer.Focused() {
		t.Fatalf(
			"resolved state = gate %q, draft %q, focused %v",
			resolved.currentGateKey(),
			resolved.composer.Value(),
			resolved.composer.Focused(),
		)
	}
}

func TestInputGateCallsProvideInputAndRestoresComposer(t *testing.T) {
	t.Parallel()

	controller := gomock.NewController(t)
	control := mocks.NewMockRunControl(controller)
	control.EXPECT().ProvideInput(gomock.Any(), alfred.RunID("run-1"), alfred.InputResponse{
		RequestID: "input-1",
		Content:   []alfred.ContentPart{alfred.TextContent{Text: "production"}},
	}).Return(nil)
	server := connect.NewServer()
	alfredv1connect.RegisterRunServiceHandler(
		server,
		rpc.NewRunHandler(mocks.NewMockRunStore(controller), control),
	)
	model := readyModel(t)
	model.clients = rpc.NewClients(connect.NewClient(connectinprocess.New(server)))
	model.composer.SetValue("preserved draft")
	model.gates.apply(inputRequestedEvent(1, "run-1", "input-1", "Which environment?"))
	model.syncGateFocus("")
	model.gateInput.SetValue("production")

	updated, provide := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	resolving := updated.(Model)
	if provide == nil || resolving.resolvingGate != "input:input-1" {
		t.Fatalf("provide command = %T, resolving gate = %q", provide, resolving.resolvingGate)
	}
	updated, _ = resolving.Update(provide())
	resolved := updated.(Model)
	if resolved.currentGateKey() != "" || resolved.composer.Value() != "preserved draft" ||
		!resolved.composer.Focused() {
		t.Fatalf(
			"resolved state = gate %q, draft %q, focused %v",
			resolved.currentGateKey(),
			resolved.composer.Value(),
			resolved.composer.Focused(),
		)
	}
}

func batchMessage[Message tea.Msg](t *testing.T, command tea.Cmd) Message {
	t.Helper()
	message := command()
	if typed, ok := message.(Message); ok {
		return typed
	}
	batch, ok := message.(tea.BatchMsg)
	if !ok {
		t.Fatalf("command returned %T, want requested message or tea.BatchMsg", message)
	}
	for _, batched := range batch {
		if typed, ok := batched().(Message); ok {
			return typed
		}
	}
	t.Fatalf("tea.BatchMsg did not contain requested message")
	var zero Message
	return zero
}

func newTestModel(
	t *testing.T,
	resolver alfred.ProjectResolver,
	inspector alfred.ProjectSkillInspector,
	projects alfred.ProjectStore,
	root string,
) Model {
	t.Helper()
	connectServer := connect.NewServer()
	alfredv1connect.RegisterProjectServiceHandler(
		connectServer,
		rpc.NewProjectHandler(resolver, inspector, projects),
	)
	clients := rpc.NewClients(connect.NewClient(connectinprocess.New(connectServer)))
	return NewModel(t.Context(), DefaultConfig(), clients, root)
}

func readyModel(t *testing.T) Model {
	t.Helper()
	model := NewModel(t.Context(), DefaultConfig(), rpc.Clients{}, "")
	model.project = &alfredv1.Project{
		Id: "project-1", Name: "alfred", Root: "/work/alfred",
		SkillTrust: alfredv1.ProjectSkillTrust_PROJECT_SKILL_TRUST_DENIED,
	}
	model.projectSkills = &alfredv1.InspectProjectSkillsResponse{}
	model.inspectedSkills = true
	return model
}

func textMessage(role alfredv1.MessageRole, text string) *alfredv1.Message {
	return &alfredv1.Message{
		Role: role,
		Content: []*alfredv1.ContentPart{{
			Value: &alfredv1.ContentPart_Text{Text: text},
		}},
	}
}

type recordingSessionClient struct {
	request *alfredv1.WatchSessionRequest
}

func (client *recordingSessionClient) GetSession(
	context.Context,
	*alfredv1.GetSessionRequest,
) (*alfredv1.GetSessionResponse, error) {
	return nil, connect.NewError(connect.CodeUnimplemented, "not implemented")
}

func (client *recordingSessionClient) ListSessions(
	context.Context,
	*alfredv1.ListSessionsRequest,
) (*alfredv1.ListSessionsResponse, error) {
	return nil, connect.NewError(connect.CodeUnimplemented, "not implemented")
}

func (client *recordingSessionClient) WatchSession(
	_ context.Context,
	request *alfredv1.WatchSessionRequest,
) (alfredv1connect.SessionServiceWatchSessionClientStream, error) {
	client.request = request
	return alfredv1connect.SessionServiceWatchSessionClientStream{}, connect.NewError(
		connect.CodeUnavailable,
		"service unavailable",
	)
}

func TestApprovalHintUsesConfiguredInterrupt(t *testing.T) {
	t.Parallel()
	config := DefaultConfig()
	config.Keybindings.Interrupt = []string{"ctrl+x", "esc"}
	model := NewModel(t.Context(), config, rpc.Clients{}, "")
	model.gates.apply(approvalRequestedEvent(1, "run-1", "approval-1", "Allow action?"))
	if view := model.inputView(); !strings.Contains(view, "[ctrl+x/esc] Cancel run") || strings.Contains(view, "ctrl+c") {
		t.Fatalf("configured approval hint = %q", view)
	}
}
