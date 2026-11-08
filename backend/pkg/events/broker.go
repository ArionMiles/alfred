package events

import (
	"context"
	"sync"

	"github.com/ArionMiles/alfred"
)

// updateBufferSize absorbs short transient-output bursts without making the
// broker an unbounded store. Overflow becomes an explicit UpdateGap.
const updateBufferSize = 64

// UpdateActivity is one transient update or a marker that updates were lost.
type UpdateActivity interface {
	isUpdateActivity()
}

// TransientUpdate contains one non-durable live run update.
type TransientUpdate struct {
	Update alfred.RunUpdate
}

func (TransientUpdate) isUpdateActivity() {}

// UpdateGap tells a client to discard provisional output and wait for the next
// complete durable message.
type UpdateGap struct{}

func (UpdateGap) isUpdateActivity() {}

// Subscription receives stored-history notifications and transient updates for
// one session. Its owner must call Close.
type Subscription struct {
	broker    *Broker
	sessionID alfred.SessionID
	changes   chan struct{}
	updates   chan UpdateActivity
}

// Changes is notified when the subscribed session may have new durable history.
// Notifications are coalesced; consumers read JSONL from their last durable
// sequence.
func (subscription *Subscription) Changes() <-chan struct{} {
	return subscription.changes
}

// Updates returns transient activity for incremental rendering.
func (subscription *Subscription) Updates() <-chan UpdateActivity {
	return subscription.updates
}

// Close removes the subscription. Repeated calls are safe.
func (subscription *Subscription) Close() {
	subscription.broker.closeSubscription(subscription)
}

// Broker coordinates process-local session subscriptions. JSONL, rather than
// the broker, remains the source of durable event content.
type Broker struct {
	mu                 sync.Mutex
	sessionSubscribers map[alfred.SessionID]map[*Subscription]struct{}
}

// NewBroker creates a live session-activity broker.
func NewBroker() *Broker {
	return &Broker{
		sessionSubscribers: make(map[alfred.SessionID]map[*Subscription]struct{}),
	}
}

// SubscribeSession registers one session subscription.
func (broker *Broker) SubscribeSession(sessionID alfred.SessionID) *Subscription {
	broker.mu.Lock()
	defer broker.mu.Unlock()

	subscription := &Subscription{
		broker:    broker,
		sessionID: sessionID,
		changes:   make(chan struct{}, 1),
		updates:   make(chan UpdateActivity, updateBufferSize),
	}
	sessionSubscribers := broker.sessionSubscribers[sessionID]
	if sessionSubscribers == nil {
		sessionSubscribers = make(map[*Subscription]struct{})
		broker.sessionSubscribers[sessionID] = sessionSubscribers
	}
	sessionSubscribers[subscription] = struct{}{}
	return subscription
}

// NotifyStoredEvent wakes subscribers after a run event is durably stored.
func (broker *Broker) NotifyStoredEvent(event alfred.RunEvent) {
	broker.mu.Lock()
	defer broker.mu.Unlock()

	notifyChanges(broker.sessionSubscribers[event.SessionID])
}

// Publish delivers a transient update without blocking run execution.
func (broker *Broker) Publish(_ context.Context, update alfred.RunUpdate) {
	broker.mu.Lock()
	defer broker.mu.Unlock()

	publishUpdate(broker.sessionSubscribers[update.SessionID], update)
}

func notifyChanges(subscribers map[*Subscription]struct{}) {
	for subscription := range subscribers {
		select {
		case subscription.changes <- struct{}{}:
		default:
		}
	}
}

func publishUpdate(subscribers map[*Subscription]struct{}, update alfred.RunUpdate) {
	for subscription := range subscribers {
		select {
		case subscription.updates <- TransientUpdate{Update: update}:
		default:
		drainUpdates:
			for {
				select {
				case <-subscription.updates:
				default:
					break drainUpdates
				}
			}
			subscription.updates <- UpdateGap{}
		}
	}
}

func (broker *Broker) closeSubscription(subscription *Subscription) {
	broker.mu.Lock()
	defer broker.mu.Unlock()

	subscribers := broker.sessionSubscribers[subscription.sessionID]
	if _, exists := subscribers[subscription]; !exists {
		return
	}
	delete(subscribers, subscription)
	if len(subscribers) == 0 {
		delete(broker.sessionSubscribers, subscription.sessionID)
	}
	close(subscription.changes)
	close(subscription.updates)
}

var _ alfred.RunUpdateBroadcaster = (*Broker)(nil)
