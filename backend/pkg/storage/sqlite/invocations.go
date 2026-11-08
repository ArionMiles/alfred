package sqlite

import (
	"context"
	"database/sql"
	"time"

	modernsqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

// CreateInvocation persists an immutable invocation.
func (store *Store) CreateInvocation(ctx context.Context, invocation alfred.Invocation) error {
	const op = "CreateInvocation"

	input, err := marshalInvocationInput(invocation.Input)
	if err != nil {
		return errors.B.Op(op).Text("encode invocation input").Err(err).Build()
	}
	_, err = store.db.ExecContext(ctx, `
		INSERT INTO invocations (
			id, project_id, session_id, source, input_json, created_at_unix_nano
		)
		VALUES (
			@id, @project_id, @session_id, @source, @input_json, @created_at_unix_nano
		)
	`,
		sql.Named("id", invocation.ID),
		sql.Named("project_id", invocation.ProjectID),
		sql.Named("session_id", invocation.SessionID),
		sql.Named("source", invocation.Source),
		sql.Named("input_json", string(input)),
		sql.Named("created_at_unix_nano", invocation.CreatedAt.UnixNano()),
	)
	if err == nil {
		return nil
	}

	builder := errors.B.Op(op).Textf("create invocation %q", invocation.ID).Err(err)
	var sqliteErr *modernsqlite.Error
	if errors.As(err, &sqliteErr) {
		switch sqliteErr.Code() {
		case sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY, sqlite3.SQLITE_CONSTRAINT_UNIQUE:
			builder = builder.KindConflict().UserMsg("the invocation already exists")
		case sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY:
			builder = builder.KindNotFound().UserMsg("the project or session does not exist")
		}
	}
	return builder.Build()
}

// GetInvocation returns an immutable invocation by ID.
func (store *Store) GetInvocation(
	ctx context.Context,
	invocationID alfred.InvocationID,
) (alfred.Invocation, error) {
	const op = "GetInvocation"

	var invocation alfred.Invocation
	var input []byte
	var createdAt int64
	err := store.db.QueryRowContext(ctx, `
		SELECT id, project_id, session_id, source, input_json, created_at_unix_nano
		FROM invocations
		WHERE id = @id
	`, sql.Named("id", invocationID)).Scan(
		&invocation.ID,
		&invocation.ProjectID,
		&invocation.SessionID,
		&invocation.Source,
		&input,
		&createdAt,
	)
	if err != nil {
		builder := errors.B.Op(op).Textf("get invocation %q", invocationID).Err(err)
		if errors.Is(err, sql.ErrNoRows) {
			builder = builder.KindNotFound().UserMsg("the invocation does not exist")
		}
		return alfred.Invocation{}, builder.Build()
	}
	invocation.Input, err = unmarshalInvocationInput(input)
	if err != nil {
		return alfred.Invocation{}, errors.B.
			Op(op).
			Textf("decode invocation %q input", invocationID).
			Err(err).
			Build()
	}
	invocation.CreatedAt = time.Unix(0, createdAt).UTC()
	return invocation, nil
}

var _ alfred.InvocationStore = (*Store)(nil)
