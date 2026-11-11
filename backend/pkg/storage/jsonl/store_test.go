package jsonl

import (
	"bytes"
	"context"
	"iter"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

func TestNewUsesConfig(t *testing.T) {
	t.Parallel()

	store := New(Config{DataDir: "events", LockShardCount: 3}, nil, nil, nil)
	if store.dataDir != "events" {
		t.Fatalf("data directory = %q, want events", store.dataDir)
	}
	if len(store.locks) != 3 {
		t.Fatalf("lock shard count = %d, want 3", len(store.locks))
	}
}

func TestStoreAppendsSessionSequenceAndReadsViews(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	session := alfred.Session{ID: "session-1", ProjectID: "project-1"}
	runs := runReaderStub{runs: map[alfred.RunID]alfred.Run{
		"run-1": {ID: "run-1", SessionID: session.ID},
		"run-2": {ID: "run-2", SessionID: session.ID},
	}}
	sessions := sessionReaderStub{sessions: map[alfred.SessionID]alfred.Session{session.ID: session}}
	store := newTestStore(dataDir, runs, sessions, "event-")

	before := time.Now()
	first, err := store.Append(t.Context(), alfred.NewRunEvent{
		RunID: "run-1", Visibility: alfred.RunEventVisibilityUser,
		Payload: alfred.RunStateChanged{Status: alfred.RunStatusRunning},
	})
	if err != nil {
		t.Fatalf("Append() first error = %v", err)
	}
	second, err := store.Append(t.Context(), alfred.NewRunEvent{
		RunID: "run-2", Visibility: alfred.RunEventVisibilityInternal,
		Payload: alfred.RunStateChanged{Status: alfred.RunStatusCompleted},
	})
	if err != nil {
		t.Fatalf("Append() second error = %v", err)
	}
	if first.Sequence != 1 || second.Sequence != 2 {
		t.Fatalf("sequences = %d, %d, want 1, 2", first.Sequence, second.Sequence)
	}
	if first.ID != "event-1" || second.ID != "event-2" {
		t.Fatalf("event IDs = %q, %q, want event-1, event-2", first.ID, second.ID)
	}
	if first.SessionID != session.ID || second.SessionID != session.ID {
		t.Fatalf("event session IDs = %q, %q, want %q", first.SessionID, second.SessionID, session.ID)
	}
	after := time.Now()
	for _, event := range []alfred.RunEvent{first, second} {
		if event.OccurredAt.Before(before) || event.OccurredAt.After(after) {
			t.Fatalf("event timestamp = %v, outside append interval [%v, %v]", event.OccurredAt, before, after)
		}
	}

	path := filepath.Join(dataDir, "projects", "project-1", "session-1.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	if lines := bytes.Count(data, []byte{'\n'}); lines != 2 {
		t.Fatalf("JSONL lines = %d, want 2", lines)
	}

	sessionEvents := collectEvents(t, store.ReadSessionEvents(t.Context(), session.ID))
	if len(sessionEvents) != 2 || sessionEvents[0].SessionID != session.ID ||
		sessionEvents[0].RunID != "run-1" || sessionEvents[1].RunID != "run-2" {
		t.Fatalf("ReadSessionEvents() = %#v", sessionEvents)
	}
	runEvents := collectEvents(t, store.ReadRunEvents(t.Context(), "run-2"))
	if len(runEvents) != 1 || runEvents[0].ID != second.ID {
		t.Fatalf("ReadRunEvents() = %#v, want event %q", runEvents, second.ID)
	}
}

func TestStoreContinuesSequenceAfterRestart(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	session := alfred.Session{ID: "session-1", ProjectID: "project-1"}
	runs := runReaderStub{runs: map[alfred.RunID]alfred.Run{
		"run-1": {ID: "run-1", SessionID: session.ID},
	}}
	sessions := sessionReaderStub{sessions: map[alfred.SessionID]alfred.Session{session.ID: session}}
	firstStore := newTestStore(dataDir, runs, sessions, "first-")
	if _, err := firstStore.Append(t.Context(), stateEvent("run-1")); err != nil {
		t.Fatalf("Append() before restart error = %v", err)
	}

	restarted := newTestStore(dataDir, runs, sessions, "second-")
	event, err := restarted.Append(t.Context(), stateEvent("run-1"))
	if err != nil {
		t.Fatalf("Append() after restart error = %v", err)
	}
	if event.Sequence != 2 {
		t.Fatalf("sequence after restart = %d, want 2", event.Sequence)
	}
}

func TestStoreSerializesConcurrentSessionAppends(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	session := alfred.Session{ID: "session-1", ProjectID: "project-1"}
	runs := runReaderStub{runs: map[alfred.RunID]alfred.Run{
		"run-1": {ID: "run-1", SessionID: session.ID},
	}}
	sessions := sessionReaderStub{sessions: map[alfred.SessionID]alfred.Session{session.ID: session}}
	store := newTestStore(dataDir, runs, sessions, "event-")

	const appendCount = 12
	errs := make(chan error, appendCount)
	var wait sync.WaitGroup
	for range appendCount {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := store.Append(context.Background(), stateEvent("run-1"))
			errs <- err
		}()
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}

	events := collectEvents(t, store.ReadSessionEvents(t.Context(), session.ID))
	if len(events) != appendCount {
		t.Fatalf("event count = %d, want %d", len(events), appendCount)
	}
	for index, event := range events {
		if event.Sequence != uint64(index+1) {
			t.Fatalf("event %d sequence = %d, want %d", index, event.Sequence, index+1)
		}
	}
}

func TestStoreReportsIncompleteFinalRecord(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	session := alfred.Session{ID: "session-1", ProjectID: "project-1"}
	runs := runReaderStub{runs: map[alfred.RunID]alfred.Run{
		"run-1": {ID: "run-1", SessionID: session.ID},
	}}
	sessions := sessionReaderStub{sessions: map[alfred.SessionID]alfred.Session{session.ID: session}}
	path := filepath.Join(dataDir, "projects", "project-1", "session-1.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("os.MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	store := newTestStore(dataDir, runs, sessions, "event-")

	_, err := store.Append(t.Context(), stateEvent("run-1"))
	if got := errors.WhatKind(err); got != errors.InvalidInput {
		t.Fatalf("errors.WhatKind() = %#v, want %#v", got, errors.InvalidInput)
	}
}

func BenchmarkStoreAppend(b *testing.B) {
	dataDir := b.TempDir()
	session := alfred.Session{ID: "session-1", ProjectID: "project-1"}
	runs := runReaderStub{runs: map[alfred.RunID]alfred.Run{
		"run-1": {ID: "run-1", SessionID: session.ID},
	}}
	sessions := sessionReaderStub{sessions: map[alfred.SessionID]alfred.Session{session.ID: session}}
	store := newTestStore(dataDir, runs, sessions, "event-")
	event := stateEvent("run-1")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := store.Append(context.Background(), event); err != nil {
			b.Fatalf("Append() error = %v", err)
		}
	}
}

func newTestStore(
	dataDir string,
	runs runReader,
	sessions sessionReader,
	prefix string,
) *Store {
	return New(
		Config{DataDir: dataDir, LockShardCount: 64},
		runs,
		sessions,
		&eventIDGeneratorStub{prefix: prefix},
	)
}

func stateEvent(runID alfred.RunID) alfred.NewRunEvent {
	return alfred.NewRunEvent{
		RunID: runID, Visibility: alfred.RunEventVisibilityUser,
		Payload: alfred.RunStateChanged{Status: alfred.RunStatusRunning},
	}
}

func collectEvents(t *testing.T, sequence iter.Seq2[alfred.RunEvent, error]) []alfred.RunEvent {
	t.Helper()
	events := make([]alfred.RunEvent, 0)
	for event, err := range sequence {
		if err != nil {
			t.Fatalf("event sequence error = %v", err)
		}
		events = append(events, event)
	}
	return events
}

type runReaderStub struct {
	runs map[alfred.RunID]alfred.Run
}

func (reader runReaderStub) GetRun(_ context.Context, id alfred.RunID) (alfred.Run, error) {
	return reader.runs[id], nil
}

type sessionReaderStub struct {
	sessions map[alfred.SessionID]alfred.Session
}

func (reader sessionReaderStub) GetSession(_ context.Context, id alfred.SessionID) (alfred.Session, error) {
	return reader.sessions[id], nil
}

type eventIDGeneratorStub struct {
	mu     sync.Mutex
	prefix string
	next   int
}

func (generator *eventIDGeneratorStub) New() (alfred.RunEventID, error) {
	generator.mu.Lock()
	defer generator.mu.Unlock()
	generator.next++
	return alfred.RunEventID(generator.prefix + strconv.Itoa(generator.next)), nil
}
