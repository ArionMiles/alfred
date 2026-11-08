package events

import (
	"testing"

	"github.com/ArionMiles/alfred"
)

func TestBrokerRoutesActivityBySession(t *testing.T) {
	t.Parallel()

	broker := NewBroker()
	first := broker.SubscribeSession("session-1")
	defer first.Close()
	second := broker.SubscribeSession("session-2")
	defer second.Close()

	broker.NotifyStoredEvent(alfred.RunEvent{SessionID: "session-1", RunID: "run-1"})
	if _, open := <-first.Changes(); !open {
		t.Fatal("stored-event notification channel is closed")
	}
	update := alfred.RunUpdate{SessionID: "session-1", RunID: "run-2"}
	broker.Publish(t.Context(), update)
	if got := (<-first.Updates()).(TransientUpdate).Update; got != update {
		t.Fatalf("transient update = %#v, want %#v", got, update)
	}
	select {
	case activity := <-second.Updates():
		t.Fatalf("second session received %#v", activity)
	default:
	}
}

func TestBrokerCoalescesStoredEventNotifications(t *testing.T) {
	t.Parallel()

	broker := NewBroker()
	subscription := broker.SubscribeSession("session-1")
	defer subscription.Close()

	event := alfred.RunEvent{SessionID: "session-1", RunID: "run-1"}
	broker.NotifyStoredEvent(event)
	broker.NotifyStoredEvent(event)

	if _, open := <-subscription.Changes(); !open {
		t.Fatal("stored-event notification channel is closed")
	}
	select {
	case <-subscription.Changes():
		t.Fatal("stored-event notifications were not coalesced")
	default:
	}
}

func TestBrokerMarksTransientUpdateGapWithoutClosingSubscription(t *testing.T) {
	t.Parallel()

	broker := NewBroker()
	subscription := broker.SubscribeSession("session-1")
	defer subscription.Close()

	for range updateBufferSize + 1 {
		broker.Publish(t.Context(), alfred.RunUpdate{SessionID: "session-1", RunID: "run-1"})
	}
	if _, ok := (<-subscription.Updates()).(UpdateGap); !ok {
		t.Fatal("first activity after overflow is not an update gap")
	}

	update := alfred.RunUpdate{
		SessionID: "session-1", RunID: "run-1", Visibility: alfred.RunEventVisibilityUser,
	}
	broker.Publish(t.Context(), update)
	if got := (<-subscription.Updates()).(TransientUpdate).Update; got != update {
		t.Fatalf("update after gap = %#v, want %#v", got, update)
	}
}

func TestSubscriptionCloseIsIdempotent(t *testing.T) {
	t.Parallel()

	broker := NewBroker()
	subscription := broker.SubscribeSession("session-1")

	subscription.Close()
	subscription.Close()

	if _, open := <-subscription.Changes(); open {
		t.Fatal("stored-event notification channel remained open")
	}
	if _, open := <-subscription.Updates(); open {
		t.Fatal("transient update channel remained open")
	}
}
