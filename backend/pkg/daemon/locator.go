package daemon

import (
	"context"
	"net/http"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	alfredv1 "github.com/ArionMiles/alfred/gen/alfred/v1"
	"github.com/ArionMiles/alfred/gen/alfred/v1/alfredv1connect"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

// startupPollInterval bounds discovery latency without making polling a
// user-facing tuning surface.
const startupPollInterval = 50 * time.Millisecond

// Discover verifies that the published endpoint belongs to a ready daemon.
func Discover(ctx context.Context, config Config) (Discovery, error) {
	const op = "Discover"

	discovery, err := ReadDiscovery(config)
	if err != nil {
		return Discovery{}, errors.B.Op(op).Err(err).Build()
	}
	httpClient := &http.Client{
		Transport: http.DefaultTransport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	client := alfredv1connect.NewDaemonServiceClient(connect.NewClient(
		connecthttp.NewTransport(httpClient, discovery.Endpoint),
	))
	status, err := client.GetStatus(ctx, &alfredv1.GetStatusRequest{})
	if err != nil {
		return Discovery{}, errors.B.
			Op(op).
			KindUnavailable().
			Retryable(true).
			Textf("probe daemon endpoint %q", discovery.Endpoint).
			Err(err).
			Build()
	}
	if status.GetInstanceId() != discovery.InstanceID {
		return Discovery{}, errors.B.
			Op(op).
			KindUnavailable().
			Retryable(true).
			Textf(
				"daemon endpoint %q reported instance %q, want %q",
				discovery.Endpoint,
				status.GetInstanceId(),
				discovery.InstanceID,
			).
			Build()
	}
	if status.GetState() != alfredv1.DaemonState_DAEMON_STATE_READY {
		return Discovery{}, errors.B.
			Op(op).
			KindUnavailable().
			Retryable(true).
			Textf("daemon instance %q is %s", discovery.InstanceID, status.GetState()).
			Build()
	}
	return discovery, nil
}

// Ensure discovers a ready daemon or starts one and waits for its publication.
func Ensure(
	ctx context.Context,
	config Config,
	start func() error,
) (Discovery, error) {
	const op = "Ensure"

	discovery, err := Discover(ctx, config)
	if err == nil {
		return discovery, nil
	}
	if !canRetryDiscovery(err) {
		return Discovery{}, errors.B.Op(op).Text("discover daemon").Err(err).Build()
	}
	if err := start(); err != nil {
		return Discovery{}, errors.B.Op(op).Text("start daemon process").Err(err).Build()
	}

	startupContext, cancel := context.WithTimeout(ctx, config.StartupTimeout)
	defer cancel()
	ticker := time.NewTicker(startupPollInterval)
	defer ticker.Stop()
	var lastErr error
	for {
		discovery, err = Discover(startupContext, config)
		if err == nil {
			return discovery, nil
		}
		lastErr = err
		if !canRetryDiscovery(err) {
			return Discovery{}, errors.B.Op(op).Text("discover started daemon").Err(err).Build()
		}
		select {
		case <-startupContext.Done():
			return Discovery{}, errors.B.
				Op(op).
				KindUnavailable().
				Text("wait for daemon readiness").
				Err(lastErr).
				Err(startupContext.Err()).
				Build()
		case <-ticker.C:
		}
	}
}

func canRetryDiscovery(err error) bool {
	switch errors.WhatKind(err) {
	case errors.NotFound, errors.InvalidInput, errors.Unavailable:
		return true
	default:
		return false
	}
}
