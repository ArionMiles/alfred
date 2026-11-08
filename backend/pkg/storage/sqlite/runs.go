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

// CreateRun persists a run's initial lifecycle state.
func (store *Store) CreateRun(ctx context.Context, run alfred.Run) error {
	const op = "CreateRun"

	_, err := store.db.ExecContext(ctx, `
		INSERT INTO runs (
			id, invocation_id, session_id, default_provider_id, default_model_id, status,
			created_at_unix_nano
		)
		VALUES (
			@id, @invocation_id, @session_id, @default_provider_id, @default_model_id, @status,
			@created_at_unix_nano
		)
	`,
		sql.Named("id", run.ID),
		sql.Named("invocation_id", run.InvocationID),
		sql.Named("session_id", run.SessionID),
		sql.Named("default_provider_id", run.DefaultModel.ProviderID),
		sql.Named("default_model_id", run.DefaultModel.ModelID),
		sql.Named("status", run.Status),
		sql.Named("created_at_unix_nano", run.CreatedAt.UnixNano()),
	)
	if err == nil {
		return nil
	}

	builder := errors.B.Op(op).Textf("create run %q", run.ID).Err(err)
	var sqliteErr *modernsqlite.Error
	if errors.As(err, &sqliteErr) {
		switch sqliteErr.Code() {
		case sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY, sqlite3.SQLITE_CONSTRAINT_UNIQUE:
			builder = builder.KindConflict().UserMsg("the run already exists")
		case sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY:
			builder = builder.KindNotFound().UserMsg("the invocation or session does not exist")
		}
	}
	return builder.Build()
}

// GetRun returns run metadata and its current lifecycle state.
func (store *Store) GetRun(ctx context.Context, runID alfred.RunID) (alfred.Run, error) {
	const op = "GetRun"

	var run alfred.Run
	var createdAt int64
	err := store.db.QueryRowContext(ctx, `
		SELECT
			id, invocation_id, session_id, default_provider_id, default_model_id, status,
			created_at_unix_nano
		FROM runs
		WHERE id = @id
	`, sql.Named("id", runID)).Scan(
		&run.ID,
		&run.InvocationID,
		&run.SessionID,
		&run.DefaultModel.ProviderID,
		&run.DefaultModel.ModelID,
		&run.Status,
		&createdAt,
	)
	if err != nil {
		builder := errors.B.Op(op).Textf("get run %q", runID).Err(err)
		if errors.Is(err, sql.ErrNoRows) {
			builder = builder.KindNotFound().UserMsg("the run does not exist")
		}
		return alfred.Run{}, builder.Build()
	}
	run.CreatedAt = time.Unix(0, createdAt).UTC()
	return run, nil
}

// ListRuns returns one page of a session's runs in creation order.
func (store *Store) ListRuns(
	ctx context.Context,
	sessionID alfred.SessionID,
	options alfred.RunListOptions,
) (alfred.RunPage, error) {
	const op = "ListRuns"

	query, err := renderQuery(store.templates.listRuns, queryTemplateVars{
		Flags: map[string]bool{queryFlagHasAfter: options.After != nil},
	})
	if err != nil {
		return alfred.RunPage{}, errors.B.Op(op).Text("render list_runs query").Err(err).Build()
	}
	parameters := NewParameters()
	parameters.addParameter("session_id", sessionID)
	if options.After != nil {
		parameters.addParameter("after_created_at", options.After.CreatedAt.UnixNano())
		parameters.addParameter("after_id", options.After.ID)
	}
	parameters.addLimit(int64(options.Limit) + 1)
	rows, err := store.db.QueryContext(ctx, query, parameters.Pars()...)
	if err != nil {
		return alfred.RunPage{}, errors.B.
			Op(op).
			Textf("query session %q runs", sessionID).
			Err(err).
			Build()
	}
	defer func() { _ = rows.Close() }()

	runs := make([]alfred.Run, 0)
	for rows.Next() {
		var run alfred.Run
		var createdAt int64
		if err := rows.Scan(
			&run.ID,
			&run.InvocationID,
			&run.SessionID,
			&run.DefaultModel.ProviderID,
			&run.DefaultModel.ModelID,
			&run.Status,
			&createdAt,
		); err != nil {
			return alfred.RunPage{}, errors.B.Op(op).Text("scan run").Err(err).Build()
		}
		run.CreatedAt = time.Unix(0, createdAt).UTC()
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return alfred.RunPage{}, errors.B.Op(op).Text("iterate runs").Err(err).Build()
	}

	page := alfred.RunPage{Runs: runs}
	if options.Limit > 0 && len(page.Runs) > int(options.Limit) {
		page.Runs = page.Runs[:options.Limit]
		last := page.Runs[len(page.Runs)-1]
		page.Next = &alfred.RunCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return page, nil
}

// TransitionRun changes a run's status only when its current status matches expected.
func (store *Store) TransitionRun(
	ctx context.Context,
	runID alfred.RunID,
	expected alfred.RunStatus,
	next alfred.RunStatus,
) error {
	const op = "TransitionRun"

	result, err := store.db.ExecContext(ctx, `
		UPDATE runs
		SET status = @next
		WHERE id = @id AND status = @expected
	`,
		sql.Named("next", next),
		sql.Named("id", runID),
		sql.Named("expected", expected),
	)
	if err != nil {
		return errors.B.Op(op).Textf("transition run %q", runID).Err(err).Build()
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return errors.B.Op(op).Textf("count transitioned run %q rows", runID).Err(err).Build()
	}
	if rows != 0 {
		return nil
	}

	var current alfred.RunStatus
	err = store.db.QueryRowContext(ctx, `
		SELECT status FROM runs WHERE id = @id
	`, sql.Named("id", runID)).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.B.
			Op(op).
			KindNotFound().
			Textf("run %q does not exist", runID).
			UserMsg("the run does not exist").
			Err(err).
			Build()
	}
	if err != nil {
		return errors.B.Op(op).Textf("get run %q status", runID).Err(err).Build()
	}
	return errors.B.
		Op(op).
		KindConflict().
		Textf("run %q status is %q, expected %q", runID, current, expected).
		UserMsg("the run state changed before the operation completed").
		Build()
}

var _ alfred.RunStore = (*Store)(nil)
