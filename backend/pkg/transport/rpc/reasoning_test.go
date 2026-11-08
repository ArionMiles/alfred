package rpc

import (
	"context"
	"iter"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectinprocess"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/ArionMiles/alfred"
	alfredv1 "github.com/ArionMiles/alfred/gen/alfred/v1"
	"github.com/ArionMiles/alfred/gen/alfred/v1/alfredv1connect"
	"github.com/ArionMiles/alfred/mocks"
	"github.com/ArionMiles/alfred/pkg/events"
)

func TestSessionProjectionPreservesReadableOrderWithoutNativeDataOrCopies(t *testing.T) {
	t.Parallel()
	message := alfred.Message{Role: alfred.MessageRoleAssistant, Content: []alfred.ContentPart{alfred.TextContent{Text: "Answer"}}}
	native := alfred.NativeData{"private.v1": []byte(`{"encrypted_content":"NEVER_EXPOSE"}`)}
	completion := alfred.RunEvent{
		ID: "checkpoint", RunID: "run", Sequence: 4, Visibility: alfred.RunEventVisibilityInternal,
		Payload: alfred.ModelCallCompleted{CallID: "call", Output: []alfred.ModelInput{
			{Payload: alfred.Reasoning{Text: "Plan", TextKind: alfred.ReasoningTextSummary}, Native: native},
			{Payload: message, Native: native},
			{Payload: alfred.ToolCall{ID: "tool", Arguments: []byte(`{"private":"NEVER_EXPOSE"}`)}, Native: native},
			{Payload: alfred.Reasoning{}, Native: native},
			{Payload: alfred.Reasoning{Text: "Check", TextKind: alfred.ReasoningTextReasoning}, Native: native},
		}},
	}
	committed := make(map[alfred.ModelCallID]bool)
	encoded, err := encodeSessionEvent(completion, committed)
	if err != nil {
		t.Fatal(err)
	}
	output := encoded.GetModelOutputCompleted().GetOutput()
	if encoded.GetSequence() != 4 || len(output) != 3 || output[0].GetReasoning().GetText() != "Plan" ||
		output[1].GetMessage().GetContent()[0].GetText() != "Answer" || output[2].GetReasoning().GetText() != "Check" {
		t.Fatalf("projection = %v", encoded)
	}
	data, err := protojson.Marshal(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "NEVER_EXPOSE") || strings.Contains(string(data), "private.v1") {
		t.Fatalf("native data leaked: %s", data)
	}
	copyEvent := alfred.RunEvent{Visibility: alfred.RunEventVisibilityUser, Payload: alfred.MessageAdded{ModelCallID: "call", Message: message}}
	copyOutput, err := encodeSessionEvent(copyEvent, committed)
	if err != nil || copyOutput != nil {
		t.Fatalf("duplicate message = %v, error = %v", copyOutput, err)
	}
	legacy := completion
	legacy.Payload = alfred.ModelCallCompleted{CallID: "legacy"}
	if encoded, err := encodeSessionEvent(legacy, committed); err != nil || encoded != nil {
		t.Fatalf("legacy checkpoint = %v, error = %v", encoded, err)
	}
	copyEvent.Payload = alfred.MessageAdded{ModelCallID: "legacy", Message: message}
	if encoded, err := encodeSessionEvent(copyEvent, committed); err != nil || encoded.GetMessageAdded() == nil {
		t.Fatalf("legacy message = %v, error = %v", encoded, err)
	}
}

func TestWatchSessionReplaysReasoningAndSuppressesCopiesAfterCursor(t *testing.T) {
	t.Parallel()
	for _, after := range []uint64{0, 1} {
		name := "initial"
		if after == 1 {
			name = "reconnect"
		}
		t.Run(name, func(t *testing.T) {
			controller := gomock.NewController(t)
			session := alfred.Session{ID: "session", CreatedAt: time.Now()}
			sessions := mocks.NewMockSessionStore(controller)
			sessions.EXPECT().GetSession(gomock.Any(), session.ID).Return(session, nil)
			reader := mocks.NewMockRunEventReader(controller)
			reader.EXPECT().ReadSessionEvents(gomock.Any(), session.ID).DoAndReturn(func(context.Context, alfred.SessionID) iter.Seq2[alfred.RunEvent, error] {
				return func(yield func(alfred.RunEvent, error) bool) {
					stored := []alfred.RunEvent{
						{Sequence: 1, Payload: alfred.ModelCallCompleted{CallID: "call", Output: []alfred.ModelInput{
							{Payload: alfred.Reasoning{Text: "Plan", TextKind: alfred.ReasoningTextSummary}, Native: alfred.NativeData{"private.v1": []byte(`{"signature":"NEVER_EXPOSE"}`)}},
						}}},
						{Sequence: 2, Visibility: alfred.RunEventVisibilityUser, Payload: alfred.MessageAdded{ModelCallID: "call"}},
						{Sequence: 3, Visibility: alfred.RunEventVisibilityUser, Payload: alfred.RunStateChanged{Status: alfred.RunStatusCompleted}},
					}
					for _, event := range stored {
						if !yield(event, nil) {
							return
						}
					}
				}
			}).AnyTimes()
			broker := events.NewBroker()
			server := connect.NewServer()
			alfredv1connect.RegisterSessionServiceHandler(server, NewSessionHandler(sessions, reader, broker))
			client := alfredv1connect.NewSessionServiceClient(connect.NewClient(connectinprocess.New(server)))
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			stream, err := client.WatchSession(ctx, &alfredv1.WatchSessionRequest{SessionId: string(session.ID), AfterSequence: after})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = stream.Close() }()
			if response, err := stream.Receive(); err != nil || response.GetSnapshot() == nil {
				t.Fatalf("snapshot=%v error=%v", response, err)
			}
			if after == 0 {
				response, err := stream.Receive()
				if err != nil || response.GetEvent().GetModelOutputCompleted().GetOutput()[0].GetReasoning().GetText() != "Plan" {
					t.Fatalf("checkpoint=%v error=%v", response, err)
				}
				data, err := protojson.Marshal(response)
				if err != nil || strings.Contains(string(data), "NEVER_EXPOSE") {
					t.Fatalf("unsafe response=%s error=%v", data, err)
				}
			}
			if response, err := stream.Receive(); err != nil || response.GetEvent().GetSequence() != 3 {
				t.Fatalf("expected state after skipping copy: response=%v error=%v", response, err)
			}
			if response, err := stream.Receive(); err != nil || response.GetCaughtUp().GetThroughSequence() != 3 {
				t.Fatalf("caught up=%v error=%v", response, err)
			}
			broker.Publish(ctx, alfred.RunUpdate{
				SessionID: session.ID, Visibility: alfred.RunEventVisibilityUser,
				Payload: alfred.RunReasoningDelta{CallID: "next", Delta: alfred.ModelReasoningDelta{Text: "Live plan", TextKind: alfred.ReasoningTextSummary}},
			})
			if response, err := stream.Receive(); err != nil || response.GetUpdate().GetReasoningDelta().GetText() != "Live plan" {
				t.Fatalf("live reasoning=%v error=%v", response, err)
			}
		})
	}
}

func TestReasoningUpdateEncodesOnlyReadableText(t *testing.T) {
	t.Parallel()
	encoded, err := encodeRunUpdate(alfred.RunUpdate{RunID: "run", Payload: alfred.RunReasoningDelta{
		CallID: "call", Delta: alfred.ModelReasoningDelta{Index: 2, Text: "Plan", TextKind: alfred.ReasoningTextSummary},
	}})
	if err != nil || encoded.GetReasoningDelta().GetText() != "Plan" || encoded.GetReasoningDelta().GetIndex() != 2 ||
		encoded.GetReasoningDelta().GetModelCallId() != "call" {
		t.Fatalf("update = %v, error = %v", encoded, err)
	}
}
