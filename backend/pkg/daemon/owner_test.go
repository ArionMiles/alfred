package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ArionMiles/alfred/pkg/x/errors"
)

func TestOwnerPublishesDiscoveryAndExcludesCompetitor(t *testing.T) {
	t.Parallel()

	config := Config{DataDir: t.TempDir()}
	owner, err := Acquire(config)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	competitor, err := Acquire(config)
	if competitor != nil || errors.WhatKind(err) != errors.Conflict {
		t.Fatalf("competing Acquire() = %#v, %v; want conflict", competitor, err)
	}

	discovery, err := owner.Publish("http://127.0.0.1:43123")
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	stored, err := ReadDiscovery(config)
	if err != nil {
		t.Fatalf("ReadDiscovery() error = %v", err)
	}
	if stored != discovery || stored.InstanceID != owner.InstanceID() ||
		stored.Version != "dev" || stored.StartedAt != owner.StartedAt() {
		t.Fatalf("ReadDiscovery() = %#v, want %#v", stored, discovery)
	}
	discovery, err = owner.Publish("http://127.0.0.1:43124")
	if err != nil {
		t.Fatalf("replace discovery error = %v", err)
	}
	stored, err = ReadDiscovery(config)
	if err != nil || stored != discovery {
		t.Fatalf("replaced discovery = %#v, %v; want %#v", stored, err, discovery)
	}
	info, err := os.Stat(filepath.Join(config.DataDir, discoveryFileName))
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("discovery permissions = %o, want 600", info.Mode().Perm())
	}

	if err := owner.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := ReadDiscovery(config); errors.WhatKind(err) != errors.NotFound {
		t.Fatalf("ReadDiscovery() after Close error = %v, want not found", err)
	}
	replacement, err := Acquire(config)
	if err != nil {
		t.Fatalf("replacement Acquire() error = %v", err)
	}
	if err := replacement.Close(); err != nil {
		t.Fatalf("replacement Close() error = %v", err)
	}
}

func TestOwnerDoesNotRemoveAnotherInstanceDiscovery(t *testing.T) {
	t.Parallel()

	config := Config{DataDir: t.TempDir()}
	owner, err := Acquire(config)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	other := Discovery{
		SchemaVersion: discoverySchemaVersion,
		Endpoint:      "http://127.0.0.1:43124",
		InstanceID:    "other-instance",
		Version:       "dev",
		PID:           os.Getpid(),
		StartedAt:     time.Now().UTC(),
	}
	if err := writeDiscovery(config, other); err != nil {
		t.Fatalf("writeDiscovery() error = %v", err)
	}
	if err := owner.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	stored, err := ReadDiscovery(config)
	if err != nil {
		t.Fatalf("ReadDiscovery() error = %v", err)
	}
	if stored != other {
		t.Fatalf("ReadDiscovery() = %#v, want %#v", stored, other)
	}
}

func TestReadDiscoveryRejectsNonLocalEndpoint(t *testing.T) {
	t.Parallel()

	config := Config{DataDir: t.TempDir()}
	discovery := Discovery{
		SchemaVersion: discoverySchemaVersion,
		Endpoint:      "https://example.com:443",
		InstanceID:    "instance-1",
		Version:       "dev",
		PID:           os.Getpid(),
		StartedAt:     time.Now().UTC(),
	}
	if err := writeDiscovery(config, discovery); err != nil {
		t.Fatalf("writeDiscovery() error = %v", err)
	}
	if _, err := ReadDiscovery(config); errors.WhatKind(err) != errors.InvalidInput {
		t.Fatalf("ReadDiscovery() error = %v, want invalid input", err)
	}
}
