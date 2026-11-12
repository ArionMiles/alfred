package daemon

import (
	"context"
	"io"
	"net"
	"net/http"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

type providerSetup interface{ Shutdown(context.Context) error }

type readiness interface {
	MarkReady()
	MarkStopping()
}

// Server serves Alfred's local RPC endpoint for one ownership lifetime.
type Server struct {
	config       Config
	owner        *Owner
	dispatcher   alfred.RunDispatcher
	setup        providerSetup
	store        io.Closer
	stopRequests context.CancelFunc
	readiness    readiness
	listener     net.Listener
	http         *http.Server
	endpoint     string
}

// Dependencies contains the collaborators retained by a daemon server.
type Dependencies struct {
	// Publishes discovery and releases the ownership lock after storage closes.
	Owner *Owner
	// Serves the shared RPC contract.
	Handler http.Handler
	// Publishes readiness and stopping state.
	Readiness readiness
	// Owns active execution and its shutdown.
	Dispatcher alfred.RunDispatcher
	// Owns active provider onboarding workflows.
	ProviderSetup providerSetup
	// Closes shared persistence after all writers stop.
	Store io.Closer
}

// NewServer binds the configured address without publishing it to clients.
// Ownership transfers to Server on success; the caller cleans up on failure.
func NewServer(ctx context.Context, config Config, dependencies Dependencies) (*Server, error) {
	const op = "NewServer"

	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", config.ListenAddress)
	if err != nil {
		return nil, errors.B.Op(op).Text("bind daemon RPC listener").Err(err).Build()
	}
	// A wildcard listener accepts local connections too. Discovery publishes a
	// connectable loopback address, never 0.0.0.0 or ::.
	host, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		return nil, errors.B.Op(op).Text("resolve bound daemon address").Err(err).Err(listener.Close()).Build()
	}
	address := net.ParseIP(host)
	if address.IsUnspecified() {
		host = "127.0.0.1"
		if address.To4() == nil {
			host = "::1"
		}
	}
	requestContext, stopRequests := context.WithCancel(context.Background())
	return &Server{
		config:       config,
		owner:        dependencies.Owner,
		dispatcher:   dependencies.Dispatcher,
		setup:        dependencies.ProviderSetup,
		store:        dependencies.Store,
		stopRequests: stopRequests,
		readiness:    dependencies.Readiness,
		listener:     listener,
		http: &http.Server{
			Handler:           dependencies.Handler,
			BaseContext:       func(net.Listener) context.Context { return requestContext },
			ReadHeaderTimeout: config.ReadHeaderTimeout,
		},
		endpoint: "http://" + net.JoinHostPort(host, port),
	}, nil
}

// Endpoint returns the bound endpoint. It is not discoverable until Serve runs.
func (server *Server) Endpoint() string {
	return server.endpoint
}

// Serve publishes readiness and waits for cancellation or an HTTP failure.
// The caller must call Close before releasing shared application resources.
func (server *Server) Serve(ctx context.Context) error {
	const op = "Serve"
	if err := ctx.Err(); err != nil {
		return errors.B.Op(op).Text("start daemon RPC server").Err(err).Build()
	}
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- server.http.Serve(server.listener) }()
	server.readiness.MarkReady()
	if _, err := server.owner.Publish(server.endpoint); err != nil {
		return errors.B.Op(op).Text("publish ready daemon").Err(err).Build()
	}
	select {
	case err := <-serverErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return errors.B.Op(op).Text("serve daemon RPC").Err(err).Build()
	case <-ctx.Done():
		return nil
	}
}

// Close drains RPC handlers before stopping executions. On failure, shared
// storage and ownership must remain open because writers may still be active.
func (server *Server) Close() error {
	const op = "Close"
	ctx, cancel := context.WithTimeout(context.Background(), server.config.ShutdownTimeout)
	defer cancel()
	server.readiness.MarkStopping()
	server.stopRequests()
	if err := server.http.Shutdown(ctx); err != nil {
		return errors.B.Op(op).Text("drain daemon RPC handlers").Err(err).Build()
	}
	// Shutdown does not close a listener that Serve has never registered.
	if err := server.listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		return errors.B.Op(op).Text("close daemon listener").Err(err).Build()
	}
	if err := server.setup.Shutdown(ctx); err != nil {
		return errors.B.Op(op).Text("shut down provider setup").Err(err).Build()
	}
	if err := server.dispatcher.Shutdown(ctx); err != nil {
		return errors.B.Op(op).Text("shut down run dispatcher").Err(err).Build()
	}
	if err := server.store.Close(); err != nil {
		return errors.B.Op(op).Text("close shared storage").Err(err).Build()
	}
	if err := server.owner.Close(); err != nil {
		return errors.B.Op(op).Text("release daemon ownership").Err(err).Build()
	}

	return nil
}
