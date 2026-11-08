package rpc

import (
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ArionMiles/alfred"
	alfredv1 "github.com/ArionMiles/alfred/gen/alfred/v1"
	"github.com/ArionMiles/alfred/mocks"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

func TestInvocationHandlerMapsRequestAndResponse(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, time.October, 26, 12, 0, 0, 0, time.UTC)
	domainRequest := alfred.InvocationRequest{
		ProjectID: "project-1",
		SessionID: "session-1",
		Source:    alfred.InvocationSourceUser,
		Input: []alfred.ContentPart{
			alfred.TextContent{Text: "Describe this image"},
			alfred.ResourceContent{
				Type: alfred.ContentKindImage,
				Resource: alfred.ResourceRef{
					URI:       "alfred://attachment/image-1",
					MediaType: "image/png",
					Name:      "diagram.png",
				},
			},
		},
	}
	run := alfred.Run{
		ID:           "run-1",
		InvocationID: "invocation-1",
		SessionID:    domainRequest.SessionID,
		DefaultModel: alfred.ModelRef{ProviderID: "provider-1", ModelID: "model-1"},
		Status:       alfred.RunStatusQueued,
		CreatedAt:    createdAt,
	}
	service := mocks.NewMockInvocationService(gomock.NewController(t))
	service.EXPECT().Invoke(gomock.Any(), domainRequest).Return(run, nil)
	handler := NewInvocationHandler(service)

	response, err := handler.Invoke(t.Context(), &alfredv1.InvokeRequest{
		ProjectId: string(domainRequest.ProjectID),
		SessionId: string(domainRequest.SessionID),
		Source:    alfredv1.InvocationSource_INVOCATION_SOURCE_USER,
		Input: []*alfredv1.ContentPart{
			{Value: &alfredv1.ContentPart_Text{Text: "Describe this image"}},
			{Value: &alfredv1.ContentPart_Resource{Resource: &alfredv1.ResourceContent{
				Kind: alfredv1.ContentKind_CONTENT_KIND_IMAGE,
				Resource: &alfredv1.ResourceRef{
					Uri:       "alfred://attachment/image-1",
					MediaType: "image/png",
					Name:      "diagram.png",
				},
			}}},
		},
	})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	want := &alfredv1.InvokeResponse{Run: &alfredv1.Run{
		Id:           string(run.ID),
		InvocationId: string(run.InvocationID),
		SessionId:    string(run.SessionID),
		DefaultModel: &alfredv1.ModelRef{ProviderId: "provider-1", ModelId: "model-1"},
		Status:       alfredv1.RunStatus_RUN_STATUS_QUEUED,
		CreatedAt:    timestamppb.New(createdAt),
	}}
	if !proto.Equal(response, want) {
		t.Fatalf("Invoke() = %v, want %v", response, want)
	}
	if got := response.GetRun().GetCreatedAt().AsTime(); !got.Equal(createdAt) {
		t.Fatalf("created_at = %v, want %v", got, createdAt)
	}
}

func TestInvocationHandlerRejectsUnknownWireValues(t *testing.T) {
	t.Parallel()

	service := mocks.NewMockInvocationService(gomock.NewController(t))
	handler := NewInvocationHandler(service)

	_, err := handler.Invoke(t.Context(), &alfredv1.InvokeRequest{
		Source: alfredv1.InvocationSource_INVOCATION_SOURCE_UNSPECIFIED,
	})
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("connect.CodeOf(error) = %v, want %v", connect.CodeOf(err), connect.CodeInvalidArgument)
	}
}

func TestInvocationHandlerRejectsInternalSource(t *testing.T) {
	t.Parallel()

	service := mocks.NewMockInvocationService(gomock.NewController(t))
	handler := NewInvocationHandler(service)

	_, err := handler.Invoke(t.Context(), &alfredv1.InvokeRequest{
		Source: alfredv1.InvocationSource_INVOCATION_SOURCE_INTERNAL,
	})
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("connect.CodeOf(error) = %v, want %v", connect.CodeOf(err), connect.CodeInvalidArgument)
	}
}

func TestInvocationHandlerAcceptsExternalSources(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		wire   alfredv1.InvocationSource
		domain alfred.InvocationSource
	}{
		"user":     {wire: alfredv1.InvocationSource_INVOCATION_SOURCE_USER, domain: alfred.InvocationSourceUser},
		"schedule": {wire: alfredv1.InvocationSource_INVOCATION_SOURCE_SCHEDULE, domain: alfred.InvocationSourceSchedule},
		"webhook":  {wire: alfredv1.InvocationSource_INVOCATION_SOURCE_WEBHOOK, domain: alfred.InvocationSourceWebhook},
		"monitor":  {wire: alfredv1.InvocationSource_INVOCATION_SOURCE_MONITOR, domain: alfred.InvocationSourceMonitor},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			service := mocks.NewMockInvocationService(gomock.NewController(t))
			service.EXPECT().Invoke(gomock.Any(), alfred.InvocationRequest{
				Source: test.domain,
				Input:  []alfred.ContentPart{},
			}).Return(
				alfred.Run{Status: alfred.RunStatusQueued},
				nil,
			)
			handler := NewInvocationHandler(service)

			if _, err := handler.Invoke(t.Context(), &alfredv1.InvokeRequest{Source: test.wire}); err != nil {
				t.Fatalf("Invoke() error = %v", err)
			}
		})
	}
}

func TestInvocationHandlerMapsSafeApplicationError(t *testing.T) {
	t.Parallel()

	serviceErr := errors.B.
		KindNotFound().
		Text("project lookup contained private diagnostic detail").
		UserMsg("the project could not be found").
		Build()
	service := mocks.NewMockInvocationService(gomock.NewController(t))
	service.EXPECT().Invoke(gomock.Any(), gomock.Any()).Return(alfred.Run{}, serviceErr)
	handler := NewInvocationHandler(service)

	_, err := handler.Invoke(t.Context(), &alfredv1.InvokeRequest{
		Source: alfredv1.InvocationSource_INVOCATION_SOURCE_USER,
	})
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("connect.CodeOf(error) = %v, want %v", connect.CodeOf(err), connect.CodeNotFound)
	}
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("Invoke() error = %T, want *connect.Error", err)
	}
	if connectErr.Message() != "the project could not be found" {
		t.Fatalf("error message = %q", connectErr.Message())
	}
	if !errors.Is(err, serviceErr) {
		t.Fatal("RPC error does not preserve its application cause")
	}
}

func TestInvocationHandlerHidesInternalErrorText(t *testing.T) {
	t.Parallel()

	serviceErr := errors.B.Text("private diagnostic detail").Build()
	service := mocks.NewMockInvocationService(gomock.NewController(t))
	service.EXPECT().Invoke(gomock.Any(), gomock.Any()).Return(alfred.Run{}, serviceErr)
	handler := NewInvocationHandler(service)

	_, err := handler.Invoke(t.Context(), &alfredv1.InvokeRequest{
		Source: alfredv1.InvocationSource_INVOCATION_SOURCE_USER,
	})
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("Invoke() error = %T, want *connect.Error", err)
	}
	if connectErr.Code() != connect.CodeInternal || connectErr.Message() != "the request failed" {
		t.Fatalf("RPC error = %v %q", connectErr.Code(), connectErr.Message())
	}
}
