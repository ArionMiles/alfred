package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/creasty/defaults"

	"github.com/ArionMiles/alfred/internal/config"
	"github.com/ArionMiles/alfred/pkg/daemon"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

func TestComposeReleasesResourcesOnFailure(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		blockDatabase    bool
		corruptDiscovery bool
		failure          string
	}{
		"store open":    {blockDatabase: true, failure: "open SQLite store"},
		"listener bind": {failure: "create daemon server"},
		"cleanup error": {blockDatabase: true, corruptDiscovery: true, failure: "open SQLite store"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var settings config.C
			defaults.MustSet(&settings)
			settings.DataDir = t.TempDir()
			settings.ConfigDir = t.TempDir()
			settings.Daemon.ListenAddress = "invalid"

			databasePath := filepath.Join(settings.DataDir, "alfred.db")
			if test.blockDatabase {
				if err := os.Mkdir(databasePath, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			discoveryPath := filepath.Join(settings.DataDir, "service.json")
			if test.corruptDiscovery {
				if err := os.WriteFile(discoveryPath, []byte("invalid JSON"), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			server, err := compose(t.Context(), settings)
			if server != nil || err == nil || !strings.Contains(err.Error(), test.failure) {
				t.Fatalf("compose() = %v, %v; want %q failure", server, err, test.failure)
			}
			if test.corruptDiscovery {
				if !strings.Contains(err.Error(), "release daemon ownership after failed composition") ||
					!strings.Contains(err.Error(), "decode daemon discovery record") {
					t.Fatalf("compose() lost cleanup error: %v", err)
				}
				if err := os.Remove(discoveryPath); err != nil {
					t.Fatal(err)
				}
			}

			owner, err := daemon.Acquire(daemon.Config{DataDir: settings.DataDir})
			if err != nil {
				t.Fatalf("failed composition retained daemon ownership: %v", err)
			}
			t.Cleanup(func() {
				if err := owner.Close(); err != nil {
					t.Error(err)
				}
			})
			if !test.blockDatabase {
				db, err := sql.Open("sqlite", databasePath)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := db.Close(); err != nil {
						t.Error(err)
					}
				})
				// Leaving WAL mode requires other database connections to be closed.
				var mode string
				if err := db.QueryRowContext(t.Context(), "PRAGMA journal_mode = DELETE").Scan(&mode); err != nil || mode != "delete" {
					t.Fatalf("failed composition retained SQLite connection: mode=%q, error=%v", mode, err)
				}
			}
		})
	}
}

func TestComposeTransfersResourcesToServer(t *testing.T) {
	t.Parallel()
	var settings config.C
	defaults.MustSet(&settings)
	settings.DataDir = t.TempDir()
	settings.ConfigDir = t.TempDir()
	settings.Daemon.ListenAddress = "127.0.0.1:0"

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	server, err := compose(ctx, settings)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		if err := server.Close(); err != nil {
			t.Error(err)
		}
	})
	owner, err := daemon.Acquire(daemon.Config{DataDir: settings.DataDir})
	if err == nil {
		if closeErr := owner.Close(); closeErr != nil {
			t.Error(closeErr)
		}
		t.Fatal("successful composition released daemon ownership")
	}
	if errors.WhatKind(err) != errors.Conflict {
		t.Fatalf("Acquire() = %v, want ownership conflict", err)
	}
}
