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

// CreateSession persists session metadata.
func (store *Store) CreateSession(ctx context.Context, session alfred.Session) error {
	const op = "CreateSession"

	_, err := store.db.ExecContext(ctx, `
		INSERT INTO sessions (id, project_id, title, created_at_unix_nano)
		VALUES (@id, @project_id, @title, @created_at_unix_nano)
	`,
		sql.Named("id", session.ID),
		sql.Named("project_id", session.ProjectID),
		sql.Named("title", session.Title),
		sql.Named("created_at_unix_nano", session.CreatedAt.UnixNano()),
	)
	if err == nil {
		return nil
	}

	builder := errors.B.Op(op).Textf("create session %q", session.ID).Err(err)
	var sqliteErr *modernsqlite.Error
	if errors.As(err, &sqliteErr) {
		switch sqliteErr.Code() {
		case sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY, sqlite3.SQLITE_CONSTRAINT_UNIQUE:
			builder = builder.KindConflict().UserMsg("the session already exists")
		case sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY:
			builder = builder.KindNotFound().UserMsg("the project does not exist")
		}
	}
	return builder.Build()
}

// GetSession returns session metadata by its globally unique ID.
func (store *Store) GetSession(ctx context.Context, sessionID alfred.SessionID) (alfred.Session, error) {
	const op = "GetSession"

	var session alfred.Session
	var createdAt int64
	err := store.db.QueryRowContext(ctx, `
		SELECT id, project_id, title, created_at_unix_nano
		FROM sessions
		WHERE id = @id
	`, sql.Named("id", sessionID)).Scan(
		&session.ID,
		&session.ProjectID,
		&session.Title,
		&createdAt,
	)
	if err != nil {
		builder := errors.B.Op(op).Textf("get session %q", sessionID).Err(err)
		if errors.Is(err, sql.ErrNoRows) {
			builder = builder.KindNotFound().UserMsg("the session does not exist")
		}
		return alfred.Session{}, builder.Build()
	}
	session.CreatedAt = time.Unix(0, createdAt).UTC()
	return session, nil
}

// ListSessions returns one page of a project's sessions in creation order.
func (store *Store) ListSessions(
	ctx context.Context,
	projectID alfred.ProjectID,
	options alfred.SessionListOptions,
) (alfred.SessionPage, error) {
	const op = "ListSessions"

	query, err := renderQuery(store.templates.listSessions, queryTemplateVars{
		Flags: map[string]bool{queryFlagHasAfter: options.After != nil},
	})
	if err != nil {
		return alfred.SessionPage{}, errors.B.Op(op).Text("render list_sessions query").Err(err).Build()
	}
	parameters := NewParameters()
	parameters.addParameter("project_id", projectID)
	if options.After != nil {
		parameters.addParameter("after_created_at", options.After.CreatedAt.UnixNano())
		parameters.addParameter("after_id", options.After.ID)
	}
	parameters.addLimit(int64(options.Limit) + 1)
	rows, err := store.db.QueryContext(ctx, query, parameters.Pars()...)
	if err != nil {
		return alfred.SessionPage{}, errors.B.
			Op(op).
			Textf("query project %q sessions", projectID).
			Err(err).
			Build()
	}
	defer func() { _ = rows.Close() }()

	sessions := make([]alfred.Session, 0)
	for rows.Next() {
		var session alfred.Session
		var createdAt int64
		if err := rows.Scan(
			&session.ID,
			&session.ProjectID,
			&session.Title,
			&createdAt,
		); err != nil {
			return alfred.SessionPage{}, errors.B.Op(op).Text("scan session").Err(err).Build()
		}
		session.CreatedAt = time.Unix(0, createdAt).UTC()
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return alfred.SessionPage{}, errors.B.Op(op).Text("iterate sessions").Err(err).Build()
	}

	page := alfred.SessionPage{Sessions: sessions}
	if options.Limit > 0 && len(page.Sessions) > int(options.Limit) {
		page.Sessions = page.Sessions[:options.Limit]
		last := page.Sessions[len(page.Sessions)-1]
		page.Next = &alfred.SessionCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return page, nil
}

// SetSessionTitle changes a session's display title.
func (store *Store) SetSessionTitle(
	ctx context.Context,
	sessionID alfred.SessionID,
	title string,
) error {
	const op = "SetSessionTitle"

	result, err := store.db.ExecContext(ctx, `
		UPDATE sessions
		SET title = @title
		WHERE id = @id
	`,
		sql.Named("title", title),
		sql.Named("id", sessionID),
	)
	if err != nil {
		return errors.B.Op(op).Textf("set session %q title", sessionID).Err(err).Build()
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return errors.B.Op(op).Textf("count updated session %q rows", sessionID).Err(err).Build()
	}
	if rows == 0 {
		return errors.B.
			Op(op).
			KindNotFound().
			Textf("session %q does not exist", sessionID).
			UserMsg("the session does not exist").
			Build()
	}
	return nil
}

var _ alfred.SessionStore = (*Store)(nil)
