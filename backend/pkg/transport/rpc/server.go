package rpc

import (
	"net/http"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/ArionMiles/alfred/gen/alfred/v1/alfredv1connect"
)

// Handlers contains the complete RPC surface served by Alfred.
type Handlers struct {
	SearchSetup   alfredv1connect.SearchSetupServiceHandler
	Models        alfredv1connect.ModelServiceHandler
	Daemon        alfredv1connect.DaemonServiceHandler
	Project       alfredv1connect.ProjectServiceHandler
	Invocation    alfredv1connect.InvocationServiceHandler
	Session       alfredv1connect.SessionServiceHandler
	Run           alfredv1connect.RunServiceHandler
	ProviderSetup alfredv1connect.ProviderSetupServiceHandler
}

// NewServer registers Alfred's services on one Connect HTTP handler.
func NewServer(handlers Handlers) http.Handler {
	server := connect.NewServer()
	alfredv1connect.RegisterSearchSetupServiceHandler(server, handlers.SearchSetup)
	alfredv1connect.RegisterModelServiceHandler(server, handlers.Models)
	alfredv1connect.RegisterDaemonServiceHandler(server, handlers.Daemon)
	alfredv1connect.RegisterProjectServiceHandler(server, handlers.Project)
	alfredv1connect.RegisterInvocationServiceHandler(server, handlers.Invocation)
	alfredv1connect.RegisterSessionServiceHandler(server, handlers.Session)
	alfredv1connect.RegisterRunServiceHandler(server, handlers.Run)
	alfredv1connect.RegisterProviderSetupServiceHandler(server, handlers.ProviderSetup)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	return mux
}
