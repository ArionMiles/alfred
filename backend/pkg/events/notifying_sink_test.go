package events

import (
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/mocks"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

func TestNotifyingEventSinkNotifiesAfterStoredEvent(t *testing.T) {
	t.Parallel()

	pending := alfred.NewRunEvent{RunID: "run-1"}
	stored := alfred.RunEvent{ID: "event-1", SessionID: "session-1", RunID: pending.RunID, Sequence: 1}
	durable := mocks.NewMockRunEventSink(gomock.NewController(t))
	durable.EXPECT().Append(gomock.Any(), pending).Return(stored, nil)
	broker := NewBroker()
	subscription := broker.SubscribeSession(stored.SessionID)
	defer subscription.Close()
	sink := NewNotifyingEventSink(durable, broker)

	got, err := sink.Append(t.Context(), pending)
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if got != stored {
		t.Fatalf("Append() = %#v, want %#v", got, stored)
	}
	if _, open := <-subscription.Changes(); !open {
		t.Fatal("stored-event notification channel is closed")
	}
}

func TestNotifyingEventSinkDoesNotNotifyRejectedEvent(t *testing.T) {
	t.Parallel()

	pending := alfred.NewRunEvent{RunID: "run-1"}
	appendErr := errors.B.Text("append failed").Build()
	durable := mocks.NewMockRunEventSink(gomock.NewController(t))
	durable.EXPECT().Append(gomock.Any(), pending).Return(alfred.RunEvent{}, appendErr)
	broker := NewBroker()
	subscription := broker.SubscribeSession("session-1")
	defer subscription.Close()
	sink := NewNotifyingEventSink(durable, broker)

	if _, err := sink.Append(t.Context(), pending); !errors.Is(err, appendErr) {
		t.Fatalf("Append() error = %v, want %v", err, appendErr)
	}
	select {
	case <-subscription.Changes():
		t.Fatal("notified rejected event")
	default:
	}
}
