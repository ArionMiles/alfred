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

// CreateProject persists project metadata.
func (store *Store) CreateProject(ctx context.Context, project alfred.Project) error {
	const op = "CreateProject"

	_, err := store.db.ExecContext(ctx, `
		INSERT INTO projects (id, name, root, skill_trust, created_at_unix_nano)
		VALUES (@id, @name, @root, @skill_trust, @created_at_unix_nano)
	`,
		sql.Named("id", project.ID),
		sql.Named("name", project.Name),
		sql.Named("root", project.Root),
		sql.Named("skill_trust", project.SkillTrust),
		sql.Named("created_at_unix_nano", project.CreatedAt.UnixNano()),
	)
	if err == nil {
		return nil
	}

	builder := errors.B.Op(op).Textf("create project %q", project.ID).Err(err)
	var sqliteErr *modernsqlite.Error
	if errors.As(err, &sqliteErr) {
		switch sqliteErr.Code() {
		case sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY, sqlite3.SQLITE_CONSTRAINT_UNIQUE:
			builder = builder.KindConflict().UserMsg("the project already exists")
		}
	}
	return builder.Build()
}

// GetProject returns project metadata by ID.
func (store *Store) GetProject(ctx context.Context, projectID alfred.ProjectID) (alfred.Project, error) {
	const op = "GetProject"

	var project alfred.Project
	var createdAt int64
	err := store.db.QueryRowContext(ctx, `
		SELECT id, name, root, skill_trust, created_at_unix_nano
		FROM projects
		WHERE id = @id
	`, sql.Named("id", projectID)).Scan(
		&project.ID,
		&project.Name,
		&project.Root,
		&project.SkillTrust,
		&createdAt,
	)
	if err != nil {
		builder := errors.B.Op(op).Textf("get project %q", projectID).Err(err)
		if errors.Is(err, sql.ErrNoRows) {
			builder = builder.KindNotFound().UserMsg("the project does not exist")
		}
		return alfred.Project{}, builder.Build()
	}
	project.CreatedAt = time.Unix(0, createdAt).UTC()
	return project, nil
}

// GetProjectByRoot returns project metadata by canonical filesystem root.
func (store *Store) GetProjectByRoot(ctx context.Context, root string) (alfred.Project, error) {
	const op = "GetProjectByRoot"

	var project alfred.Project
	var createdAt int64
	err := store.db.QueryRowContext(ctx, `
		SELECT id, name, root, skill_trust, created_at_unix_nano
		FROM projects
		WHERE root = @root
	`, sql.Named("root", root)).Scan(
		&project.ID,
		&project.Name,
		&project.Root,
		&project.SkillTrust,
		&createdAt,
	)
	if err != nil {
		builder := errors.B.Op(op).Textf("get project with root %q", root).Err(err)
		if errors.Is(err, sql.ErrNoRows) {
			builder = builder.KindNotFound().UserMsg("the project does not exist")
		}
		return alfred.Project{}, builder.Build()
	}
	project.CreatedAt = time.Unix(0, createdAt).UTC()
	return project, nil
}

// ListProjects returns one page of projects in creation order.
func (store *Store) ListProjects(
	ctx context.Context,
	options alfred.ProjectListOptions,
) (alfred.ProjectPage, error) {
	const op = "ListProjects"

	query, err := renderQuery(store.templates.listProjects, queryTemplateVars{
		Flags: map[string]bool{queryFlagHasAfter: options.After != nil},
	})
	if err != nil {
		return alfred.ProjectPage{}, errors.B.Op(op).Text("render list_projects query").Err(err).Build()
	}
	parameters := NewParameters()
	if options.After != nil {
		parameters.addParameter("after_created_at", options.After.CreatedAt.UnixNano())
		parameters.addParameter("after_id", options.After.ID)
	}
	parameters.addLimit(int64(options.Limit) + 1)
	rows, err := store.db.QueryContext(ctx, query, parameters.Pars()...)
	if err != nil {
		return alfred.ProjectPage{}, errors.B.Op(op).Text("query projects").Err(err).Build()
	}
	defer func() { _ = rows.Close() }()

	projects := make([]alfred.Project, 0)
	for rows.Next() {
		var project alfred.Project
		var createdAt int64
		if err := rows.Scan(
			&project.ID,
			&project.Name,
			&project.Root,
			&project.SkillTrust,
			&createdAt,
		); err != nil {
			return alfred.ProjectPage{}, errors.B.Op(op).Text("scan project").Err(err).Build()
		}
		project.CreatedAt = time.Unix(0, createdAt).UTC()
		projects = append(projects, project)
	}
	if err := rows.Err(); err != nil {
		return alfred.ProjectPage{}, errors.B.Op(op).Text("iterate projects").Err(err).Build()
	}

	page := alfred.ProjectPage{Projects: projects}
	if options.Limit > 0 && len(page.Projects) > int(options.Limit) {
		page.Projects = page.Projects[:options.Limit]
		last := page.Projects[len(page.Projects)-1]
		page.Next = &alfred.ProjectCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return page, nil
}

// RenameProject changes a project's display name.
func (store *Store) RenameProject(ctx context.Context, projectID alfred.ProjectID, name string) error {
	const op = "RenameProject"

	result, err := store.db.ExecContext(ctx, `
		UPDATE projects
		SET name = @name
		WHERE id = @id
	`, sql.Named("name", name), sql.Named("id", projectID))
	if err != nil {
		return errors.B.Op(op).Textf("rename project %q", projectID).Err(err).Build()
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return errors.B.Op(op).Textf("count renamed project %q rows", projectID).Err(err).Build()
	}
	if rows == 0 {
		return errors.B.
			Op(op).
			KindNotFound().
			Textf("project %q does not exist", projectID).
			UserMsg("the project does not exist").
			Build()
	}
	return nil
}

// SetProjectSkillTrust changes whether project-provided skills may be loaded.
func (store *Store) SetProjectSkillTrust(
	ctx context.Context,
	projectID alfred.ProjectID,
	trust alfred.ProjectSkillTrust,
) error {
	const op = "SetProjectSkillTrust"

	result, err := store.db.ExecContext(ctx, `
		UPDATE projects
		SET skill_trust = @skill_trust
		WHERE id = @id
	`, sql.Named("skill_trust", trust), sql.Named("id", projectID))
	if err != nil {
		return errors.B.Op(op).Textf("set project %q skill trust", projectID).Err(err).Build()
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return errors.B.Op(op).Textf("count updated project %q rows", projectID).Err(err).Build()
	}
	if rows == 0 {
		return errors.B.
			Op(op).
			KindNotFound().
			Textf("project %q does not exist", projectID).
			UserMsg("the project does not exist").
			Build()
	}
	return nil
}

var _ alfred.ProjectStore = (*Store)(nil)
