// Package sqlite persists Alfred's mutable application state in SQLite.
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/golang-migrate/migrate/v4"
	migratesqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	// Register the pure-Go SQLite database/sql driver used by the store.
	_ "modernc.org/sqlite"

	"github.com/ArionMiles/alfred/pkg/x/errors"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Config contains runtime configuration for Store.
type Config struct {
	Path        string
	BusyTimeout time.Duration `default:"5s"`
}

// Store persists Alfred's mutable state.
type Store struct {
	db        *sql.DB
	templates queryTemplates
}

// Open creates the database, applies migrations, and opens the store.
func Open(ctx context.Context, config Config) (*Store, error) {
	const op = "Open"

	templates, err := loadQueryTemplates()
	if err != nil {
		return nil, errors.B.Op(op).Text("load query templates").Err(err).Build()
	}
	if err := os.MkdirAll(filepath.Dir(config.Path), 0o700); err != nil {
		return nil, errors.B.Op(op).Textf("create database directory for %q", config.Path).Err(err).Build()
	}
	dsn := config.DSN()
	if err := applyMigrations(dsn); err != nil {
		return nil, errors.B.Op(op).Textf("migrate database %q", config.Path).Err(err).Build()
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, errors.B.Op(op).Textf("open database %q", config.Path).Err(err).Build()
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, errors.B.Op(op).Textf("ping database %q", config.Path).Err(err).Build()
	}
	return &Store{db: db, templates: templates}, nil
}

// Close releases database resources.
func (store *Store) Close() error {
	const op = "Close"

	if err := store.db.Close(); err != nil {
		return errors.B.Op(op).Text("close database").Err(err).Build()
	}
	return nil
}

// DSN returns the SQLite data source name for the configured store.
func (config Config) DSN() string {
	query := url.Values{}
	query.Add("_pragma", "foreign_keys(1)")
	query.Add("_pragma", "journal_mode(WAL)")
	query.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", config.BusyTimeout.Milliseconds()))
	return (&url.URL{Scheme: "file", Path: config.Path, RawQuery: query.Encode()}).String()
}

func applyMigrations(dsn string) error {
	const op = "applyMigrations"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return errors.B.Op(op).Text("open migration database").Err(err).Build()
	}
	driver, err := migratesqlite.WithInstance(db, &migratesqlite.Config{})
	if err != nil {
		_ = db.Close()
		return errors.B.Op(op).Text("create migration database driver").Err(err).Build()
	}
	source, err := iofs.New(migrationFiles, "migrations")
	if err != nil {
		_ = driver.Close()
		return errors.B.Op(op).Text("open embedded migrations").Err(err).Build()
	}
	migrator, err := migrate.NewWithInstance("iofs", source, "sqlite", driver)
	if err != nil {
		_ = source.Close()
		_ = driver.Close()
		return errors.B.Op(op).Text("create migrator").Err(err).Build()
	}
	migrateErr := migrator.Up()
	sourceErr, databaseErr := migrator.Close()
	if migrateErr != nil && !errors.Is(migrateErr, migrate.ErrNoChange) {
		return errors.B.Op(op).Text("apply migrations").Err(migrateErr).Err(sourceErr).Err(databaseErr).Build()
	}
	if sourceErr != nil || databaseErr != nil {
		return errors.B.Op(op).Text("close migrator").Err(sourceErr).Err(databaseErr).Build()
	}
	return nil
}
