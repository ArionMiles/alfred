package events

import (
	"context"

	"github.com/ArionMiles/alfred"
)

type storedEventNotifier interface {
	NotifyStoredEvent(alfred.RunEvent)
}

// NotifyingEventSink notifies observers only after the durable sink accepts an
// event.
type NotifyingEventSink struct {
	durable  alfred.RunEventSink
	notifier storedEventNotifier
}

// NewNotifyingEventSink creates a durable event sink with live notifications.
func NewNotifyingEventSink(
	durable alfred.RunEventSink,
	notifier storedEventNotifier,
) *NotifyingEventSink {
	return &NotifyingEventSink{
		durable:  durable,
		notifier: notifier,
	}
}

// Append persists an event before notifying live subscribers.
func (notifying *NotifyingEventSink) Append(
	ctx context.Context,
	event alfred.NewRunEvent,
) (alfred.RunEvent, error) {
	stored, err := notifying.durable.Append(ctx, event)
	if err != nil {
		return alfred.RunEvent{}, err
	}
	notifying.notifier.NotifyStoredEvent(stored)
	return stored, nil
}

var _ alfred.RunEventSink = (*NotifyingEventSink)(nil)
