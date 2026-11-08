package daemon

import (
	"context"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	alfredv1 "github.com/ArionMiles/alfred/gen/alfred/v1"
	"github.com/ArionMiles/alfred/gen/alfred/v1/alfredv1connect"
	"github.com/ArionMiles/alfred/pkg/transport/rpc"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

func TestServerPublishesReadyRPCAndCleansUpOnShutdown(t *testing.T) {
	t.Parallel()

	config := Config{
		ListenAddress:     "127.0.0.1:0",
		DataDir:           t.TempDir(),
		ReadHeaderTimeout: time.Second,
		ShutdownTimeout:   time.Second,
		StartupTimeout:    time.Second,
	}
	owner, err := Acquire(config)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	status := rpc.NewDaemonHandler(owner.InstanceID(), owner.StartedAt())
	handler := newTestRPCHandler(status)
	server, err := NewServer(t.Context(), config, owner, handler, status)
	if err != nil {
		_ = owner.Close()
		t.Fatalf("NewServer() error = %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	served := make(chan error, 1)
	go func() { served <- server.Serve(ctx) }()

	discovery := waitForDiscovery(t, config)
	if discovery.Endpoint != server.Endpoint() || discovery.InstanceID != owner.InstanceID() {
		t.Fatalf("ReadDiscovery() = %#v", discovery)
	}
	client := alfredv1connect.NewDaemonServiceClient(connect.NewClient(
		connecthttp.NewTransport(http.DefaultClient, discovery.Endpoint),
	))
	response, err := client.GetStatus(t.Context(), &alfredv1.GetStatusRequest{})
	if err != nil {
		t.Fatalf("GetStatus() error = %v", err)
	}
	if response.GetInstanceId() != discovery.InstanceID ||
		response.GetState() != alfredv1.DaemonState_DAEMON_STATE_READY {
		t.Fatalf("GetStatus() = %v", response)
	}
	located, err := Ensure(t.Context(), config, func() error {
		t.Fatal("Ensure() attempted to start an already-ready daemon")
		return nil
	})
	if err != nil || located != discovery {
		t.Fatalf("Ensure() = %#v, %v; want %#v", located, err, discovery)
	}

	cancel()
	if err := <-served; err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if _, err := ReadDiscovery(config); errors.WhatKind(err) != errors.NotFound {
		t.Fatalf("ReadDiscovery() after shutdown error = %v, want not found", err)
	}
	response, err = status.GetStatus(t.Context(), &alfredv1.GetStatusRequest{})
	if err != nil || response.GetState() != alfredv1.DaemonState_DAEMON_STATE_STOPPING {
		t.Fatalf("GetStatus() after shutdown = %v, %v", response, err)
	}
}

func TestWildcardListenerPublishesReachableLocalDiscovery(t *testing.T) {
	t.Parallel()
	config := Config{DataDir: t.TempDir(), ListenAddress: "0.0.0.0:0", ShutdownTimeout: time.Second}
	owner, err := Acquire(config)
	if err != nil {
		t.Fatal(err)
	}
	status := rpc.NewDaemonHandler(owner.InstanceID(), owner.StartedAt())
	server, err := NewServer(t.Context(), config, owner, newTestRPCHandler(status), status)
	if err != nil {
		_ = owner.Close()
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	served := make(chan error, 1)
	go func() { served <- server.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-served; err != nil {
			t.Error(err)
		}
	})
	waitForDiscovery(t, config)
	if _, err := Discover(t.Context(), config); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureStartsAndWaitsForDaemon(t *testing.T) {
	t.Parallel()

	config := Config{
		ListenAddress:     "127.0.0.1:0",
		DataDir:           t.TempDir(),
		ReadHeaderTimeout: time.Second,
		ShutdownTimeout:   time.Second,
		StartupTimeout:    time.Second,
	}
	var cancel context.CancelFunc
	var served chan error
	starts := 0
	discovery, err := Ensure(t.Context(), config, func() error {
		starts++
		owner, err := Acquire(config)
		if err != nil {
			return err
		}
		status := rpc.NewDaemonHandler(owner.InstanceID(), owner.StartedAt())
		server, err := NewServer(t.Context(), config, owner, newTestRPCHandler(status), status)
		if err != nil {
			_ = owner.Close()
			return err
		}
		var serveContext context.Context
		serveContext, cancel = context.WithCancel(t.Context())
		served = make(chan error, 1)
		go func() { served <- server.Serve(serveContext) }()
		return nil
	})
	if err != nil {
		if cancel != nil {
			cancel()
			<-served
		}
		t.Fatalf("Ensure() error = %v", err)
	}
	if starts != 1 || discovery.InstanceID == "" {
		t.Fatalf("Ensure() starts = %d, discovery = %#v", starts, discovery)
	}
	cancel()
	if err := <-served; err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
}

func newTestRPCHandler(status *rpc.DaemonHandler) http.Handler {
	return rpc.NewServer(rpc.Handlers{
		Models:        alfredv1connect.UnimplementedModelServiceHandler{},
		Daemon:        status,
		Project:       alfredv1connect.UnimplementedProjectServiceHandler{},
		Invocation:    alfredv1connect.UnimplementedInvocationServiceHandler{},
		Session:       alfredv1connect.UnimplementedSessionServiceHandler{},
		Run:           alfredv1connect.UnimplementedRunServiceHandler{},
		ProviderSetup: alfredv1connect.UnimplementedProviderSetupServiceHandler{},
	})
}

func waitForDiscovery(t *testing.T, config Config) Discovery {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		discovery, err := ReadDiscovery(config)
		if err == nil {
			return discovery
		}
		if errors.WhatKind(err) != errors.NotFound {
			t.Fatalf("ReadDiscovery() error = %v", err)
		}
		select {
		case <-deadline.C:
			t.Fatal("daemon did not publish discovery")
		case <-ticker.C:
		}
	}
}
