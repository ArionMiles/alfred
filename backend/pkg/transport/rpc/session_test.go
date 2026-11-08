package rpc

import (
	"context"
	"iter"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectinprocess"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ArionMiles/alfred"
	alfredv1 "github.com/ArionMiles/alfred/gen/alfred/v1"
	"github.com/ArionMiles/alfred/gen/alfred/v1/alfredv1connect"
	"github.com/ArionMiles/alfred/mocks"
	"github.com/ArionMiles/alfred/pkg/events"
)

func TestSessionHandlerGetsSession(t *testing.T) {
	t.Parallel()

	session := alfred.Session{
		ID:        "session-1",
		ProjectID: "project-1",
		Title:     "RPC design",
		CreatedAt: time.Date(2026, time.October, 26, 14, 0, 0, 0, time.UTC),
	}
	sessions := mocks.NewMockSessionStore(gomock.NewController(t))
	sessions.EXPECT().GetSession(gomock.Any(), session.ID).Return(session, nil)
	handler := NewSessionHandler(sessions, nil, nil)

	response, err := handler.GetSession(t.Context(), &alfredv1.GetSessionRequest{
		SessionId: string(session.ID),
	})
	if err != nil {
		t.Fatalf("GetSession() error = %v", err)
	}
	want := &alfredv1.GetSessionResponse{Session: &alfredv1.Session{
		Id:        string(session.ID),
		ProjectId: string(session.ProjectID),
		Title:     session.Title,
		CreatedAt: timestamppb.New(session.CreatedAt),
	}}
	if !proto.Equal(response, want) {
		t.Fatalf("GetSession() = %v, want %v", response, want)
	}
}

func TestSessionHandlerListsSessionsWithCursor(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, time.October, 26, 15, 0, 0, 0, time.UTC)
	after := alfred.SessionCursor{CreatedAt: createdAt.Add(-time.Hour), ID: "session-0"}
	next := alfred.SessionCursor{CreatedAt: createdAt, ID: "session-1"}
	session := alfred.Session{ID: next.ID, ProjectID: "project-1", CreatedAt: createdAt}
	sessions := mocks.NewMockSessionStore(gomock.NewController(t))
	sessions.EXPECT().ListSessions(
		gomock.Any(),
		session.ProjectID,
		alfred.SessionListOptions{Limit: 20, After: &after},
	).Return(alfred.SessionPage{Sessions: []alfred.Session{session}, Next: &next}, nil)
	handler := NewSessionHandler(sessions, nil, nil)

	response, err := handler.ListSessions(t.Context(), &alfredv1.ListSessionsRequest{
		ProjectId: string(session.ProjectID),
		PageSize:  20,
		After: &alfredv1.SessionCursor{
			CreatedAt: timestamppb.New(after.CreatedAt),
			Id:        string(after.ID),
		},
	})
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	want := &alfredv1.ListSessionsResponse{
		Sessions: []*alfredv1.Session{{
			Id:        string(session.ID),
			ProjectId: string(session.ProjectID),
			CreatedAt: timestamppb.New(session.CreatedAt),
		}},
		Next: &alfredv1.SessionCursor{
			CreatedAt: timestamppb.New(next.CreatedAt),
			Id:        string(next.ID),
		},
	}
	if !proto.Equal(response, want) {
		t.Fatalf("ListSessions() = %v, want %v", response, want)
	}
}

func TestSessionHandlerWatchesDurableAndTransientActivity(t *testing.T) {
	t.Parallel()

	controller := gomock.NewController(t)
	session := alfred.Session{
		ID:        "session-1",
		ProjectID: "project-1",
		Title:     "RPC design",
		CreatedAt: time.Date(2026, time.October, 26, 14, 0, 0, 0, time.UTC),
	}
	sessions := mocks.NewMockSessionStore(controller)
	sessions.EXPECT().GetSession(gomock.Any(), session.ID).Return(session, nil)

	var eventMu sync.Mutex
	storedEvents := []alfred.RunEvent{
		{
			ID: "event-1", SessionID: session.ID, RunID: "run-1", Sequence: 1,
			Visibility: alfred.RunEventVisibilityInternal,
			Payload: alfred.ModelCallCompleted{CallID: "model-call-1", Output: []alfred.ModelInput{{Payload: alfred.Reasoning{
				Text: "Internal reasoning summary", TextKind: alfred.ReasoningTextSummary,
			}, Native: alfred.NativeData{"test.reasoning.v1": []byte(`{"state":"private"}`)}}}},
		},
		{
			ID: "event-2", SessionID: session.ID, RunID: "run-2", Sequence: 2,
			OccurredAt: session.CreatedAt, Visibility: alfred.RunEventVisibilityUser,
			Payload: alfred.MessageAdded{Message: alfred.Message{
				Role:    alfred.MessageRoleAssistant,
				Content: []alfred.ContentPart{alfred.TextContent{Text: "hello"}},
			}},
		},
	}
	reader := mocks.NewMockRunEventReader(controller)
	reader.EXPECT().ReadSessionEvents(gomock.Any(), session.ID).DoAndReturn(func(
		context.Context,
		alfred.SessionID,
	) iter.Seq2[alfred.RunEvent, error] {
		eventMu.Lock()
		snapshot := append([]alfred.RunEvent(nil), storedEvents...)
		eventMu.Unlock()
		return func(yield func(alfred.RunEvent, error) bool) {
			for _, event := range snapshot {
				if !yield(event, nil) {
					return
				}
			}
		}
	}).AnyTimes()
	broker := events.NewBroker()
	handler := NewSessionHandler(sessions, reader, broker)
	server := connect.NewServer()
	alfredv1connect.RegisterSessionServiceHandler(server, handler)
	client := alfredv1connect.NewSessionServiceClient(connect.NewClient(
		connectinprocess.New(server),
	))
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	stream, err := client.WatchSession(ctx, &alfredv1.WatchSessionRequest{
		SessionId:     string(session.ID),
		AfterSequence: 1,
	})
	if err != nil {
		t.Fatalf("WatchSession() error = %v", err)
	}
	defer func() { _ = stream.Close() }()

	response, err := stream.Receive()
	if err != nil || response.GetSnapshot().GetSession().GetId() != string(session.ID) {
		t.Fatalf("snapshot = %v, error = %v", response, err)
	}
	response, err = stream.Receive()
	if err != nil || response.GetEvent().GetRunId() != "run-2" || response.GetEvent().GetSequence() != 2 {
		t.Fatalf("durable event = %v, error = %v", response, err)
	}
	response, err = stream.Receive()
	if err != nil || response.GetCaughtUp().GetThroughSequence() != 2 {
		t.Fatalf("caught-up marker = %v, error = %v", response, err)
	}

	broker.Publish(ctx, alfred.RunUpdate{
		SessionID:  session.ID,
		RunID:      "run-3",
		Visibility: alfred.RunEventVisibilityUser,
		Payload: alfred.RunContentDelta{
			CallID: "model-call-2",
			Delta:  alfred.ModelContentDelta{Index: 0, Delta: alfred.TextContent{Text: " world"}},
		},
	})
	response, err = stream.Receive()
	if err != nil || response.GetUpdate().GetRunId() != "run-3" ||
		response.GetUpdate().GetContentDelta().GetDelta().GetText() != " world" {
		t.Fatalf("transient update = %v, error = %v", response, err)
	}

	stored := alfred.RunEvent{
		ID: "event-3", SessionID: session.ID, RunID: "run-3", Sequence: 3,
		OccurredAt: session.CreatedAt.Add(time.Second), Visibility: alfred.RunEventVisibilityUser,
		Payload: alfred.RunStateChanged{Status: alfred.RunStatusCompleted},
	}
	eventMu.Lock()
	storedEvents = append(storedEvents, stored)
	eventMu.Unlock()
	broker.NotifyStoredEvent(stored)
	response, err = stream.Receive()
	if err != nil || response.GetEvent().GetRunId() != "run-3" || response.GetEvent().GetSequence() != 3 {
		t.Fatalf("followed durable event = %v, error = %v", response, err)
	}
}

func TestSessionHandlerRejectsZeroPageSize(t *testing.T) {
	t.Parallel()

	handler := NewSessionHandler(mocks.NewMockSessionStore(gomock.NewController(t)), nil, nil)

	_, err := handler.ListSessions(t.Context(), &alfredv1.ListSessionsRequest{})
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("connect.CodeOf(error) = %v, want %v", connect.CodeOf(err), connect.CodeInvalidArgument)
	}
}

func TestSessionHandlerRejectsOversizedPage(t *testing.T) {
	t.Parallel()

	handler := NewSessionHandler(mocks.NewMockSessionStore(gomock.NewController(t)), nil, nil)

	_, err := handler.ListSessions(t.Context(), &alfredv1.ListSessionsRequest{
		PageSize: maxPageSize + 1,
	})
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("connect.CodeOf(error) = %v, want %v", connect.CodeOf(err), connect.CodeInvalidArgument)
	}
}
