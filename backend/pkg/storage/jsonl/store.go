package jsonl

import (
	"bufio"
	"bytes"
	"context"
	"hash/maphash"
	"io"
	"iter"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

type runReader interface {
	GetRun(context.Context, alfred.RunID) (alfred.Run, error)
}

type sessionReader interface {
	GetSession(context.Context, alfred.SessionID) (alfred.Session, error)
}

// Config contains runtime configuration for Store.
type Config struct {
	DataDir string
	// LockShardCount bounds the in-process locks shared among sessions.
	LockShardCount int
}

// Store appends and reads the authoritative JSONL history for each session.
// It serializes sequence assignment and append when requests overlap for a session.
type Store struct {
	dataDir          string
	runs             runReader
	sessions         sessionReader
	eventIDGenerator alfred.IDGenerator[alfred.RunEventID]
	lockSeed         maphash.Seed
	locks            []sync.Mutex
}

// New creates a JSONL event store from runtime configuration and dependencies.
func New(
	config Config,
	runs runReader,
	sessions sessionReader,
	eventIDGenerator alfred.IDGenerator[alfred.RunEventID],
) *Store {
	return &Store{
		dataDir:          config.DataDir,
		runs:             runs,
		sessions:         sessions,
		eventIDGenerator: eventIDGenerator,
		lockSeed:         maphash.MakeSeed(),
		locks:            make([]sync.Mutex, config.LockShardCount),
	}
}

// Append assigns durable event metadata and appends one synced JSONL record.
func (store *Store) Append(ctx context.Context, pending alfred.NewRunEvent) (alfred.RunEvent, error) {
	const op = "Append"

	run, err := store.runs.GetRun(ctx, pending.RunID)
	if err != nil {
		return alfred.RunEvent{}, errors.B.Op(op).Textf("get run %q", pending.RunID).Err(err).Build()
	}
	session, err := store.sessions.GetSession(ctx, run.SessionID)
	if err != nil {
		return alfred.RunEvent{}, errors.B.
			Op(op).
			Textf("get session %q for run %q", run.SessionID, pending.RunID).
			Err(err).
			Build()
	}
	path := store.sessionPath(session)

	lock := store.sessionLock(session.ID)
	lock.Lock()
	defer lock.Unlock()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return alfred.RunEvent{}, errors.B.
			Op(op).
			Textf("create session history directory %q", filepath.Dir(path)).
			Err(err).
			Build()
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return alfred.RunEvent{}, errors.B.Op(op).Textf("open session history %q", path).Err(err).Build()
	}
	defer func() { _ = file.Close() }()

	sequence, err := nextSequence(file)
	if err != nil {
		return alfred.RunEvent{}, errors.B.Op(op).Textf("read sequence from %q", path).Err(err).Build()
	}
	eventID, err := store.eventIDGenerator.New()
	if err != nil {
		return alfred.RunEvent{}, errors.B.Op(op).Text("generate event ID").Err(err).Build()
	}
	event := alfred.RunEvent{
		ID:         eventID,
		SessionID:  session.ID,
		RunID:      pending.RunID,
		Sequence:   sequence,
		OccurredAt: time.Now(),
		Visibility: pending.Visibility,
		Payload:    pending.Payload,
	}
	data, err := marshalEvent(event)
	if err != nil {
		return alfred.RunEvent{}, errors.B.Op(op).Textf("encode event %q", event.ID).Err(err).Build()
	}
	data = append(data, '\n')
	written, err := file.Write(data)
	if err != nil {
		return alfred.RunEvent{}, errors.B.Op(op).Textf("append event %q", event.ID).Err(err).Build()
	}
	if written != len(data) {
		return alfred.RunEvent{}, errors.B.
			Op(op).
			Textf("append event %q wrote %d of %d bytes", event.ID, written, len(data)).
			Build()
	}
	if err := file.Sync(); err != nil {
		return alfred.RunEvent{}, errors.B.Op(op).Textf("sync event %q", event.ID).Err(err).Build()
	}
	return event, nil
}

// ReadSessionEvents reads the session's durable events in append order.
func (store *Store) ReadSessionEvents(
	ctx context.Context,
	sessionID alfred.SessionID,
) iter.Seq2[alfred.RunEvent, error] {
	return store.readEvents(ctx, sessionID)
}

// ReadRunEvents reads one run's durable events in session append order.
func (store *Store) ReadRunEvents(
	ctx context.Context,
	runID alfred.RunID,
) iter.Seq2[alfred.RunEvent, error] {
	return func(yield func(alfred.RunEvent, error) bool) {
		const op = "ReadRunEvents"

		run, err := store.runs.GetRun(ctx, runID)
		if err != nil {
			yield(alfred.RunEvent{}, errors.B.Op(op).Textf("get run %q", runID).Err(err).Build())
			return
		}
		for event, err := range store.readEvents(ctx, run.SessionID) {
			if err != nil {
				yield(alfred.RunEvent{}, err)
				return
			}
			if event.RunID == runID && !yield(event, nil) {
				return
			}
		}
	}
}

//nolint:gocognit // Snapshot acquisition and streaming remain together to make read consistency auditable.
func (store *Store) readEvents(
	ctx context.Context,
	sessionID alfred.SessionID,
) iter.Seq2[alfred.RunEvent, error] {
	return func(yield func(alfred.RunEvent, error) bool) {
		const op = "readEvents"

		session, err := store.sessions.GetSession(ctx, sessionID)
		if err != nil {
			yield(alfred.RunEvent{}, errors.B.Op(op).Textf("get session %q", sessionID).Err(err).Build())
			return
		}
		path := store.sessionPath(session)

		lock := store.sessionLock(session.ID)
		lock.Lock()
		file, openErr := os.Open(path)
		if os.IsNotExist(openErr) {
			lock.Unlock()
			return
		}
		if openErr != nil {
			lock.Unlock()
			yield(alfred.RunEvent{}, errors.B.Op(op).Textf("open session history %q", path).Err(openErr).Build())
			return
		}
		info, statErr := file.Stat()
		lock.Unlock()
		if statErr != nil {
			_ = file.Close()
			yield(alfred.RunEvent{}, errors.B.Op(op).Textf("stat session history %q", path).Err(statErr).Build())
			return
		}
		defer func() { _ = file.Close() }()

		reader := bufio.NewReader(io.LimitReader(file, info.Size()))
		for lineNumber := uint64(1); ; lineNumber++ {
			if err := ctx.Err(); err != nil {
				yield(alfred.RunEvent{}, err)
				return
			}
			line, readErr := reader.ReadBytes('\n')
			if readErr == io.EOF && len(line) == 0 {
				return
			}
			if readErr != nil && readErr != io.EOF {
				yield(alfred.RunEvent{}, errors.B.
					Op(op).
					Textf("read line %d from %q", lineNumber, path).
					Err(readErr).
					Build())
				return
			}
			if len(line) == 0 || line[len(line)-1] != '\n' {
				yield(alfred.RunEvent{}, errors.B.
					Op(op).
					KindInvalidInput().
					Textf("session history %q has an incomplete line %d", path, lineNumber).
					Build())
				return
			}
			event, err := unmarshalEvent(bytes.TrimSuffix(line, []byte{'\n'}))
			if err != nil {
				yield(alfred.RunEvent{}, errors.B.
					Op(op).
					Textf("decode line %d from %q", lineNumber, path).
					Err(err).
					Build())
				return
			}
			event.SessionID = session.ID
			if !yield(event, nil) {
				return
			}
		}
	}
}

// sessionPath returns the session's authoritative JSONL file.
func (store *Store) sessionPath(session alfred.Session) string {
	return filepath.Join(
		store.dataDir,
		"projects",
		string(session.ProjectID),
		string(session.ID)+".jsonl",
	)
}

// sessionLock returns the in-process lock shared by a session's appends. A
// fixed shard pool avoids retaining one mutex for every session ever observed.
func (store *Store) sessionLock(sessionID alfred.SessionID) *sync.Mutex {
	index := maphash.String(store.lockSeed, string(sessionID)) % uint64(len(store.locks))
	return &store.locks[index]
}

func nextSequence(file *os.File) (uint64, error) {
	const op = "nextSequence"

	line, err := readLastLine(file)
	if err != nil {
		return 0, errors.B.Op(op).Err(err).Build()
	}
	if len(line) == 0 {
		return 1, nil
	}
	event, err := unmarshalEvent(line)
	if err != nil {
		return 0, errors.B.Op(op).Text("decode final event").Err(err).Build()
	}
	if event.Sequence == math.MaxUint64 {
		return 0, errors.B.Op(op).KindConflict().Text("session event sequence is exhausted").Build()
	}
	return event.Sequence + 1, nil
}

// readLastLine returns the final complete JSONL record without its delimiter.
// It scans backward in bounded blocks; newlines inside JSON strings are escaped
// and therefore cannot be mistaken for the physical record delimiter.
func readLastLine(file *os.File) ([]byte, error) {
	const op = "readLastLine"

	info, err := file.Stat()
	if err != nil {
		return nil, errors.B.Op(op).Text("stat history file").Err(err).Build()
	}
	if info.Size() == 0 {
		return nil, nil
	}
	last := []byte{0}
	if _, err := file.ReadAt(last, info.Size()-1); err != nil {
		return nil, errors.B.Op(op).Text("read final byte").Err(err).Build()
	}
	if last[0] != '\n' {
		return nil, errors.B.Op(op).KindInvalidInput().Text("history file has an incomplete final line").Build()
	}

	lineEnd := info.Size() - 1
	if lineEnd == 0 {
		return nil, errors.B.Op(op).KindInvalidInput().Text("history file has an empty final line").Build()
	}
	position := lineEnd
	for position > 0 {
		blockSize := int64(4096)
		if position < blockSize {
			blockSize = position
		}
		start := position - blockSize
		block := make([]byte, blockSize)
		if _, err := file.ReadAt(block, start); err != nil {
			return nil, errors.B.Op(op).Text("scan final line").Err(err).Build()
		}
		if index := bytes.LastIndexByte(block, '\n'); index >= 0 {
			lineStart := start + int64(index) + 1
			if lineStart == lineEnd {
				return nil, errors.B.Op(op).KindInvalidInput().Text("history file has an empty final line").Build()
			}
			line := make([]byte, lineEnd-lineStart)
			if _, err := file.ReadAt(line, lineStart); err != nil {
				return nil, errors.B.Op(op).Text("read final line").Err(err).Build()
			}
			return line, nil
		}
		position = start
	}

	line := make([]byte, lineEnd)
	if _, err := file.ReadAt(line, 0); err != nil {
		return nil, errors.B.Op(op).Text("read first line").Err(err).Build()
	}
	return line, nil
}

var (
	_ alfred.RunEventSink   = (*Store)(nil)
	_ alfred.RunEventReader = (*Store)(nil)
)
