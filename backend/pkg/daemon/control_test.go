package daemon

import (
	"context"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/ArionMiles/alfred/mocks"
	"github.com/ArionMiles/alfred/pkg/providers"
	"github.com/ArionMiles/alfred/pkg/transport/rpc"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

func TestStopAlreadyStopped(t *testing.T) {
	t.Parallel()
	for _, exists := range []bool{false, true} {
		config := Config{DataDir: filepath.Join(t.TempDir(), "data"), ShutdownTimeout: time.Second}
		if exists {
			if err := os.Mkdir(config.DataDir, 0o700); err != nil {
				t.Fatal(err)
			}
			// An unlocked directory is stopped even if a crashed process left bad discovery.
			if err := os.WriteFile(filepath.Join(config.DataDir, discoveryFileName), []byte("stale"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := Stop(t.Context(), config); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStopRejectsUnverifiedDiscovery(t *testing.T) {
	t.Parallel()
	config := Config{DataDir: t.TempDir(), ShutdownTimeout: time.Second}
	owner, err := Acquire(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := owner.Close(); err != nil {
			t.Error(err)
		}
	})
	// This record points at the test process, so any blind PID signaling would
	// terminate the test. The endpoint must prove the recorded instance first.
	if _, err := owner.Publish("http://127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	if err := Stop(t.Context(), config); errors.WhatKind(err) != errors.Unavailable {
		t.Fatalf("Stop = %v, want unverified endpoint failure", err)
	}
}

func TestStopWaitsForResourceCleanup(t *testing.T) {
	t.Parallel()
	config := Config{DataDir: t.TempDir(), ShutdownTimeout: 5 * time.Second}
	command := startControlProcess(t, config, false)
	waitForDiscovery(t, config)
	stopped := make(chan error, 1)
	go func() { stopped <- Stop(t.Context(), config) }()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(filepath.Join(config.DataDir, "stopping")); err == nil {
			break
		}
		select {
		case err := <-stopped:
			t.Fatalf("Stop returned before cleanup: %v", err)
		case <-deadline.C:
			t.Fatal("daemon did not begin cleanup")
		case <-ticker.C:
		}
	}
	select {
	case err := <-stopped:
		t.Fatalf("Stop returned while storage was active: %v", err)
	default:
	}
	if _, err := Acquire(config); errors.WhatKind(err) != errors.Conflict {
		t.Fatalf("ownership released before cleanup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(config.DataDir, "release"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadDiscovery(config); errors.WhatKind(err) != errors.NotFound {
		t.Fatalf("discovery retained after stop: %v", err)
	}
}

func TestStopTimesOutWithoutKillingProcess(t *testing.T) {
	t.Parallel()
	config := Config{DataDir: t.TempDir(), ShutdownTimeout: 100 * time.Millisecond}
	command := startControlProcess(t, config, true)
	waitForDiscovery(t, config)
	if err := Stop(t.Context(), config); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop = %v, want deadline", err)
	}
	if err := command.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("Stop killed daemon after timeout: %v", err)
	}
}

func startControlProcess(t *testing.T, config Config, ignore bool) *exec.Cmd {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), executable, "-test.run=^TestDaemonControlProcess$")
	command.Env = append(os.Environ(), "ALFRED_TEST_DAEMON_DIR="+config.DataDir)
	if ignore {
		command.Env = append(command.Env, "ALFRED_TEST_DAEMON_IGNORE_STOP=1")
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait() })
	return command
}

// TestDaemonControlProcess serves the production transport in an isolated test
// process, so signal tests cannot terminate the parent test runner.
func TestDaemonControlProcess(t *testing.T) {
	directory := os.Getenv("ALFRED_TEST_DAEMON_DIR")
	if directory == "" {
		return
	}
	config := Config{DataDir: directory, ListenAddress: "127.0.0.1:0", ShutdownTimeout: 5 * time.Second}
	owner, err := Acquire(config)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := mocks.NewMockRunDispatcher(gomock.NewController(t))
	dispatcher.EXPECT().Shutdown(gomock.Any()).DoAndReturn(func(ctx context.Context) error {
		if err := os.WriteFile(filepath.Join(directory, "stopping"), nil, 0o600); err != nil {
			return err
		}
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			if _, err := os.Stat(filepath.Join(directory, "release")); err == nil {
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
			}
		}
	})
	store := mocks.NewMockCloser(gomock.NewController(t))
	store.EXPECT().Close().Return(nil)
	setup, err := providers.NewSetupService(nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	status := rpc.NewDaemonHandler(owner.InstanceID(), owner.StartedAt())
	server, err := NewServer(t.Context(), config, Dependencies{Owner: owner, Handler: newTestRPCHandler(status), Readiness: status, Dispatcher: dispatcher, ProviderSetup: setup, Store: store})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := signal.NotifyContext(t.Context(), syscall.SIGTERM)
	defer cancel()
	if os.Getenv("ALFRED_TEST_DAEMON_IGNORE_STOP") != "" {
		signal.Ignore(syscall.SIGTERM)
	}
	if err := server.Serve(ctx); err != nil {
		t.Fatal(err)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
}
