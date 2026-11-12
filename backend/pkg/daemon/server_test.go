package daemon

import (
	"context"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"go.uber.org/mock/gomock"

	"github.com/ArionMiles/alfred"
	alfredv1 "github.com/ArionMiles/alfred/gen/alfred/v1"
	"github.com/ArionMiles/alfred/gen/alfred/v1/alfredv1connect"
	"github.com/ArionMiles/alfred/mocks"
	"github.com/ArionMiles/alfred/pkg/providers"
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
	t.Cleanup(func() {
		if err := owner.Close(); err != nil {
			t.Error(err)
		}
	})
	dispatcher := mocks.NewMockRunDispatcher(gomock.NewController(t))
	dispatcher.EXPECT().Shutdown(gomock.Any()).Return(nil)
	setup, err := providers.NewSetupService(nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	store := mocks.NewMockCloser(gomock.NewController(t))
	store.EXPECT().Close().DoAndReturn(func() error {
		if _, err := Acquire(config); errors.WhatKind(err) != errors.Conflict {
			t.Errorf("storage closed after ownership released: %v", err)
		}
		return nil
	})
	status := rpc.NewDaemonHandler(owner.InstanceID(), owner.StartedAt())
	handler := newTestRPCHandler(status)
	server, err := NewServer(t.Context(), config, Dependencies{Owner: owner, Handler: handler, Readiness: status, Dispatcher: dispatcher, ProviderSetup: setup, Store: store})
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
	if _, err := ReadDiscovery(config); err != nil {
		t.Fatalf("Serve released ownership before Close: %v", err)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
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
	t.Cleanup(func() {
		if err := owner.Close(); err != nil {
			t.Error(err)
		}
	})
	dispatcher := mocks.NewMockRunDispatcher(gomock.NewController(t))
	dispatcher.EXPECT().Shutdown(gomock.Any()).Return(nil)
	setup, err := providers.NewSetupService(nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	store := mocks.NewMockCloser(gomock.NewController(t))
	store.EXPECT().Close().DoAndReturn(func() error {
		if _, err := Acquire(config); errors.WhatKind(err) != errors.Conflict {
			t.Errorf("storage closed after ownership released: %v", err)
		}
		return nil
	})
	status := rpc.NewDaemonHandler(owner.InstanceID(), owner.StartedAt())
	server, err := NewServer(t.Context(), config, Dependencies{Owner: owner, Handler: newTestRPCHandler(status), Readiness: status, Dispatcher: dispatcher, ProviderSetup: setup, Store: store})
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
		if err := server.Close(); err != nil {
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
		t.Cleanup(func() {
			if err := owner.Close(); err != nil {
				t.Error(err)
			}
		})
		dispatcher := mocks.NewMockRunDispatcher(gomock.NewController(t))
		dispatcher.EXPECT().Shutdown(gomock.Any()).Return(nil)
		setup, err := providers.NewSetupService(nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		store := mocks.NewMockCloser(gomock.NewController(t))
		store.EXPECT().Close().DoAndReturn(func() error {
			if _, err := Acquire(config); errors.WhatKind(err) != errors.Conflict {
				t.Errorf("storage closed after ownership released: %v", err)
			}
			return nil
		})
		status := rpc.NewDaemonHandler(owner.InstanceID(), owner.StartedAt())
		server, err := NewServer(t.Context(), config, Dependencies{Owner: owner, Handler: newTestRPCHandler(status), Readiness: status, Dispatcher: dispatcher, ProviderSetup: setup, Store: store})
		if err != nil {
			_ = owner.Close()
			return err
		}
		var serveContext context.Context
		serveContext, cancel = context.WithCancel(t.Context())
		served = make(chan error, 1)
		go func() { served <- server.Serve(serveContext) }()
		t.Cleanup(func() {
			if err := server.Close(); err != nil {
				t.Error(err)
			}
		})
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

func TestServerCloseDrainsRequestsBeforeDispatcher(t *testing.T) {
	t.Parallel()
	config := Config{DataDir: t.TempDir(), ListenAddress: "127.0.0.1:0", ShutdownTimeout: 20 * time.Millisecond}
	owner, err := Acquire(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := owner.Close(); err != nil {
			t.Error(err)
		}
	})
	started := make(chan struct{})
	canceled := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})
	handler := http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		close(started)
		<-request.Context().Done()
		close(canceled)
		<-release
		close(finished)
	})
	dispatcher := mocks.NewMockRunDispatcher(gomock.NewController(t))
	dispatcher.EXPECT().Shutdown(gomock.Any()).DoAndReturn(func(context.Context) error {
		select {
		case <-finished:
		default:
			t.Error("dispatcher stopped before RPC writer finished")
		}
		return nil
	})
	setup, err := providers.NewSetupService(nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	store := mocks.NewMockCloser(gomock.NewController(t))
	store.EXPECT().Close().DoAndReturn(func() error {
		if _, err := Acquire(config); errors.WhatKind(err) != errors.Conflict {
			t.Errorf("storage closed after ownership released: %v", err)
		}
		return nil
	})
	status := rpc.NewDaemonHandler(owner.InstanceID(), owner.StartedAt())
	server, err := NewServer(t.Context(), config, Dependencies{Owner: owner, Handler: handler, Readiness: status, Dispatcher: dispatcher, ProviderSetup: setup, Store: store})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	served := make(chan error, 1)
	go func() { served <- server.Serve(ctx) }()
	waitForDiscovery(t, config)
	requested := make(chan error, 1)
	go func() {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.Endpoint(), nil)
		if err != nil {
			requested <- err
			return
		}
		response, err := http.DefaultClient.Do(request)
		if err == nil {
			err = response.Body.Close()
		}
		requested <- err
	}()
	<-started
	cancel()
	if err := <-served; err != nil {
		t.Fatal(err)
	}
	if err := server.Close(); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close() = %v, want deadline exceeded", err)
	}
	<-canceled
	if _, err := Acquire(config); errors.WhatKind(err) != errors.Conflict {
		t.Fatalf("ownership while writer active: %v", err)
	}
	close(release)
	if err := <-requested; err != nil {
		t.Fatal(err)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestServerCloseRetainsStorageUntilProviderWorkflowStops(t *testing.T) {
	t.Parallel()
	config := Config{DataDir: t.TempDir(), ListenAddress: "127.0.0.1:0", ShutdownTimeout: 20 * time.Millisecond}
	owner, err := Acquire(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := owner.Close(); err != nil {
			t.Error(err)
		}
	})
	release := make(chan struct{})
	stopped := make(chan struct{})
	authorizer := mocks.NewMockProviderAuthorizer(gomock.NewController(t))
	authorizer.EXPECT().Authorize(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, _ alfred.ProviderAuthorizationRequest, publish func(string)) (alfred.ProviderAuthorization, error) {
		publish("https://example.test/authorize")
		<-ctx.Done()
		<-release
		close(stopped)
		return alfred.ProviderAuthorization{State: alfred.ProviderAuthorizationCanceled}, nil
	})
	setup, err := providers.NewSetupService(nil, nil, nil, providers.WithAuthorizer("example", authorizer))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := setup.Start(t.Context(), alfred.ProviderAuthorizationRequest{Type: "example"}); err != nil {
		t.Fatal(err)
	}
	dispatcher := mocks.NewMockRunDispatcher(gomock.NewController(t))
	store := mocks.NewMockCloser(gomock.NewController(t))
	gomock.InOrder(
		dispatcher.EXPECT().Shutdown(gomock.Any()).DoAndReturn(func(context.Context) error {
			select {
			case <-stopped:
			default:
				t.Error("dispatcher shutdown preceded provider cleanup")
			}
			return nil
		}),
		store.EXPECT().Close().DoAndReturn(func() error {
			if _, err := Acquire(config); errors.WhatKind(err) != errors.Conflict {
				t.Errorf("ownership released before storage: %v", err)
			}
			return nil
		}),
	)
	status := rpc.NewDaemonHandler(owner.InstanceID(), owner.StartedAt())
	server, err := NewServer(t.Context(), config, Dependencies{Owner: owner, Handler: newTestRPCHandler(status), Readiness: status, Dispatcher: dispatcher, ProviderSetup: setup, Store: store})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Close(); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close with active provider writer = %v", err)
	}
	if _, err := Acquire(config); errors.WhatKind(err) != errors.Conflict {
		t.Fatalf("ownership released under active workflow: %v", err)
	}
	close(release)
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	replacement, err := Acquire(config)
	if err != nil {
		t.Fatalf("Close retained ownership after writers stopped: %v", err)
	}
	if err := replacement.Close(); err != nil {
		t.Fatal(err)
	}
}
