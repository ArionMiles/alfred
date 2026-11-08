package daemon

import (
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"

	"github.com/ArionMiles/alfred/internal/buildinfo"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

// Owner holds exclusive daemon ownership for one Alfred data directory.
type Owner struct {
	config     Config
	lock       *flock.Flock
	instanceID string
	startedAt  time.Time
}

// InstanceID identifies this ownership lifetime in discovery and RPC status.
func (owner *Owner) InstanceID() string {
	return owner.instanceID
}

// StartedAt returns when this ownership lifetime began.
func (owner *Owner) StartedAt() time.Time {
	return owner.startedAt
}

// Acquire claims daemon ownership without waiting for another owner to exit.
func Acquire(config Config) (*Owner, error) {
	const op = "Acquire"

	if err := os.MkdirAll(config.DataDir, 0o700); err != nil {
		return nil, errors.B.Op(op).Text("create daemon data directory").Err(err).Build()
	}
	lock := flock.New(filepath.Join(config.DataDir, lockFileName))
	locked, err := lock.TryLock()
	if err != nil {
		return nil, errors.B.Op(op).Text("acquire daemon ownership lock").Err(err).Build()
	}
	if !locked {
		return nil, errors.B.
			Op(op).
			KindConflict().
			Text("another daemon owns the data directory").
			UserMsg("Alfred is already running").
			Build()
	}
	return &Owner{
		config:     config,
		lock:       lock,
		instanceID: rand.Text(),
		startedAt:  time.Now().UTC(),
	}, nil
}

// Publish atomically makes the ready daemon discoverable by local clients.
func (owner *Owner) Publish(endpoint string) (Discovery, error) {
	const op = "Publish"

	discovery := Discovery{
		SchemaVersion: discoverySchemaVersion,
		Endpoint:      endpoint,
		InstanceID:    owner.instanceID,
		Version:       buildinfo.Version,
		PID:           os.Getpid(),
		StartedAt:     owner.startedAt,
	}
	if err := validateDiscovery(discovery); err != nil {
		return Discovery{}, errors.B.Op(op).Err(err).Build()
	}
	if err := writeDiscovery(owner.config, discovery); err != nil {
		return Discovery{}, errors.B.Op(op).Err(err).Build()
	}
	return discovery, nil
}

// Close removes this owner's discovery record and releases daemon ownership.
func (owner *Owner) Close() error {
	const op = "Close"

	var cleanupErr error
	discovery, err := ReadDiscovery(owner.config)
	if err == nil && discovery.InstanceID == owner.instanceID {
		if err := os.Remove(filepath.Join(owner.config.DataDir, discoveryFileName)); err != nil &&
			!errors.Is(err, os.ErrNotExist) {
			cleanupErr = errors.B.Op(op).Text("remove daemon discovery record").Err(err).Build()
		}
	} else if err != nil && errors.WhatKind(err) != errors.NotFound {
		cleanupErr = errors.B.Op(op).Text("read daemon discovery record during cleanup").Err(err).Build()
	}
	if err := owner.lock.Unlock(); err != nil {
		return errors.B.Op(op).Text("release daemon ownership lock").Err(cleanupErr).Err(err).Build()
	}
	return cleanupErr
}

func writeDiscovery(config Config, discovery Discovery) error {
	const op = "writeDiscovery"

	temporary, err := os.CreateTemp(config.DataDir, ".service-*.json")
	if err != nil {
		return errors.B.Op(op).Text("create temporary discovery record").Err(err).Build()
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()

	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return errors.B.Op(op).Text("restrict temporary discovery record").Err(err).Build()
	}
	if err := json.NewEncoder(temporary).Encode(discovery); err != nil {
		_ = temporary.Close()
		return errors.B.Op(op).Text("encode daemon discovery record").Err(err).Build()
	}
	if err := temporary.Close(); err != nil {
		return errors.B.Op(op).Text("close temporary discovery record").Err(err).Build()
	}
	discoveryPath := filepath.Join(config.DataDir, discoveryFileName)
	if err := os.Rename(temporaryPath, discoveryPath); err != nil {
		return errors.B.Op(op).Text("publish daemon discovery record").Err(err).Build()
	}
	return nil
}
