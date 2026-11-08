package rpc

import (
	"connectrpc.com/connect/v2"

	"github.com/ArionMiles/alfred/gen/alfred/v1/alfredv1connect"
)

// Clients contains Alfred's generated RPC service clients over one transport.
type Clients struct {
	SearchSetup   alfredv1connect.SearchSetupServiceClient
	Models        alfredv1connect.ModelServiceClient
	Daemon        alfredv1connect.DaemonServiceClient
	Project       alfredv1connect.ProjectServiceClient
	Invocation    alfredv1connect.InvocationServiceClient
	Session       alfredv1connect.SessionServiceClient
	Run           alfredv1connect.RunServiceClient
	ProviderSetup alfredv1connect.ProviderSetupServiceClient
}

// NewClients constructs Alfred's service clients over a shared Connect client.
func NewClients(client *connect.Client) Clients {
	return Clients{
		SearchSetup:   alfredv1connect.NewSearchSetupServiceClient(client),
		Models:        alfredv1connect.NewModelServiceClient(client),
		Daemon:        alfredv1connect.NewDaemonServiceClient(client),
		Project:       alfredv1connect.NewProjectServiceClient(client),
		Invocation:    alfredv1connect.NewInvocationServiceClient(client),
		Session:       alfredv1connect.NewSessionServiceClient(client),
		Run:           alfredv1connect.NewRunServiceClient(client),
		ProviderSetup: alfredv1connect.NewProviderSetupServiceClient(client),
	}
}
