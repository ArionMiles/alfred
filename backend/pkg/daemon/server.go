package daemon

import (
	"context"
	"net"
	"net/http"

	"github.com/ArionMiles/alfred/pkg/x/errors"
)

type readiness interface {
	MarkReady()
	MarkStopping()
}

// Server serves Alfred's local RPC endpoint for one ownership lifetime.
type Server struct {
	config    Config
	owner     *Owner
	readiness readiness
	listener  net.Listener
	http      *http.Server
	endpoint  string
}

// NewServer binds the configured address without publishing it to clients.
// The caller retains responsibility for closing owner when construction fails.
func NewServer(
	ctx context.Context,
	config Config,
	owner *Owner,
	handler http.Handler,
	readiness readiness,
) (*Server, error) {
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
	return &Server{
		config:    config,
		owner:     owner,
		readiness: readiness,
		listener:  listener,
		http: &http.Server{
			Handler:           handler,
			ReadHeaderTimeout: config.ReadHeaderTimeout,
		},
		endpoint: "http://" + net.JoinHostPort(host, port),
	}, nil
}

// Endpoint returns the bound endpoint. It is not discoverable until Serve runs.
func (server *Server) Endpoint() string {
	return server.endpoint
}

// Serve publishes readiness, serves until ctx ends, and then shuts down.
func (server *Server) Serve(ctx context.Context) (serveErr error) {
	const op = "Serve"

	defer func() {
		if err := server.owner.Close(); err != nil {
			serveErr = errors.B.
				Op(op).
				Text("close daemon ownership").
				Err(serveErr).
				Err(err).
				Build()
		}
	}()
	if err := ctx.Err(); err != nil {
		_ = server.listener.Close()
		return errors.B.Op(op).Text("start daemon RPC server").Err(err).Build()
	}

	server.http.BaseContext = func(net.Listener) context.Context { return ctx }
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- server.http.Serve(server.listener)
	}()

	server.readiness.MarkReady()
	if _, err := server.owner.Publish(server.endpoint); err != nil {
		server.readiness.MarkStopping()
		_ = server.http.Close()
		stoppedErr := <-serverErrors
		builder := errors.B.Op(op).Text("publish ready daemon").Err(err)
		if !errors.Is(stoppedErr, http.ErrServerClosed) {
			builder = builder.Err(stoppedErr)
		}
		return builder.Build()
	}

	select {
	case err := <-serverErrors:
		server.readiness.MarkStopping()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return errors.B.Op(op).Text("serve daemon RPC").Err(err).Build()
	case <-ctx.Done():
		server.readiness.MarkStopping()
		shutdownContext, cancel := context.WithTimeout(
			context.WithoutCancel(ctx),
			server.config.ShutdownTimeout,
		)
		defer cancel()
		if err := server.http.Shutdown(shutdownContext); err != nil {
			_ = server.http.Close()
			stoppedErr := <-serverErrors
			builder := errors.B.Op(op).Text("shut down daemon RPC").Err(err)
			if !errors.Is(stoppedErr, http.ErrServerClosed) {
				builder = builder.Err(stoppedErr)
			}
			return builder.Build()
		}
		if err := <-serverErrors; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return errors.B.Op(op).Text("finish daemon RPC serving").Err(err).Build()
		}
		return nil
	}
}
