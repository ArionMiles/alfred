package providers

import (
	"context"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/mocks"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

func TestSetupShutdownCancelsAndWaitsForWorkflows(t *testing.T) {
	t.Parallel()
	canceled := make(chan struct{})
	release := make(chan struct{})
	var workflowContext context.Context
	authorizer := mocks.NewMockProviderAuthorizer(gomock.NewController(t))
	authorizer.EXPECT().Authorize(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, _ alfred.ProviderAuthorizationRequest, publish func(string)) (alfred.ProviderAuthorization, error) {
		workflowContext = ctx
		publish("https://example.test/authorize")
		<-ctx.Done()
		close(canceled)
		<-release
		return alfred.ProviderAuthorization{State: alfred.ProviderAuthorizationCanceled, Message: "Canceled"}, nil
	})
	service, err := NewSetupService(nil, nil, nil, WithAuthorizer("example", authorizer))
	if err != nil {
		t.Fatal(err)
	}
	requestContext, cancelRequest := context.WithCancel(t.Context())
	authorization, err := service.Start(requestContext, alfred.ProviderAuthorizationRequest{Type: "example", MethodID: "login"})
	if err != nil || authorization.State != alfred.ProviderAuthorizationPending || authorization.AuthorizationURL == "" {
		t.Fatalf("Start = %#v, %v", authorization, err)
	}
	cancelRequest()
	if err := workflowContext.Err(); err != nil {
		t.Fatalf("request cancellation stopped accepted workflow: %v", err)
	}
	shutdownContext, cancelShutdown := context.WithCancel(t.Context())
	cancelShutdown()
	if err := service.Shutdown(shutdownContext); !errors.Is(err, context.Canceled) {
		t.Fatalf("Shutdown = %v", err)
	}
	<-canceled
	if _, err := service.Start(t.Context(), alfred.ProviderAuthorizationRequest{Type: "example"}); errors.WhatKind(err) != errors.FailedPrecondition {
		t.Fatalf("Start after shutdown = %v", err)
	}
	pending, err := service.Get(t.Context(), authorization.ID)
	if err != nil || pending.State != alfred.ProviderAuthorizationPending {
		t.Fatalf("published terminal state before cleanup: %#v, %v", pending, err)
	}
	close(release)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := service.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	result, err := service.Get(ctx, authorization.ID)
	if err != nil || result.State != alfred.ProviderAuthorizationCanceled || result.AuthorizationURL != "" {
		t.Fatalf("polling result after cleanup = %#v, %v", result, err)
	}
	if result.ID != authorization.ID {
		t.Fatal("workflow changed attempt identity")
	}
}
