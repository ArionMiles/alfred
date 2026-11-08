CREATE TABLE projects (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    root TEXT NOT NULL UNIQUE,
    skill_trust TEXT NOT NULL,
    created_at_unix_nano INTEGER NOT NULL
);

CREATE INDEX projects_created_at ON projects (created_at_unix_nano, id);

CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    title TEXT NOT NULL,
    created_at_unix_nano INTEGER NOT NULL,
    UNIQUE (id, project_id)
);

CREATE INDEX sessions_project_created_at
    ON sessions (project_id, created_at_unix_nano, id);

CREATE TABLE invocations (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    source TEXT NOT NULL,
    input_json TEXT NOT NULL,
    created_at_unix_nano INTEGER NOT NULL,
    UNIQUE (id, session_id),
    FOREIGN KEY (session_id, project_id) REFERENCES sessions(id, project_id)
);

CREATE INDEX invocations_session_created_at
    ON invocations (session_id, created_at_unix_nano, id);

CREATE TABLE runs (
    id TEXT PRIMARY KEY,
    invocation_id TEXT NOT NULL UNIQUE,
    session_id TEXT NOT NULL,
    default_provider_id TEXT NOT NULL,
    default_model_id TEXT NOT NULL,
    status TEXT NOT NULL,
    created_at_unix_nano INTEGER NOT NULL,
    FOREIGN KEY (invocation_id, session_id) REFERENCES invocations(id, session_id)
);

CREATE INDEX runs_session_created_at
    ON runs (session_id, created_at_unix_nano, id);

CREATE TABLE credentials (
    ref TEXT PRIMARY KEY NOT NULL,
    value BLOB NOT NULL
);

CREATE TABLE provider_instances (
    id TEXT PRIMARY KEY NOT NULL,
    type TEXT NOT NULL,
    name TEXT NOT NULL,
    enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    credential_ref TEXT NOT NULL,
    settings_json TEXT NOT NULL
);

CREATE TABLE model_selection (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    provider_id TEXT NOT NULL,
    model_id TEXT NOT NULL
);

CREATE TABLE search_provider_settings (
    backend TEXT PRIMARY KEY NOT NULL,
    enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    credential_ref TEXT NOT NULL,
    settings_json TEXT NOT NULL
);
