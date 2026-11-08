// Package rpc adapts Alfred's application services to its ConnectRPC contract.
package rpc

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ArionMiles/alfred"
	alfredv1 "github.com/ArionMiles/alfred/gen/alfred/v1"
	"github.com/ArionMiles/alfred/gen/alfred/v1/alfredv1connect"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

// InvocationHandler exposes invocation operations over RPC.
type InvocationHandler struct {
	invocations alfred.InvocationService
}

// NewInvocationHandler creates an invocation RPC handler.
func NewInvocationHandler(invocations alfred.InvocationService) *InvocationHandler {
	return &InvocationHandler{invocations: invocations}
}

// Invoke creates and dispatches a durable run.
func (handler *InvocationHandler) Invoke(
	ctx context.Context,
	request *alfredv1.InvokeRequest,
) (*alfredv1.InvokeResponse, error) {
	domainRequest, err := decodeInvocationRequest(request)
	if err != nil {
		return nil, connectError(err)
	}
	run, err := handler.invocations.Invoke(ctx, domainRequest)
	if err != nil {
		return nil, connectError(err)
	}
	encodedRun, err := encodeRun(run)
	if err != nil {
		return nil, connectError(err)
	}
	return &alfredv1.InvokeResponse{Run: encodedRun}, nil
}

func decodeInvocationRequest(request *alfredv1.InvokeRequest) (alfred.InvocationRequest, error) {
	const op = "decodeInvocationRequest"

	if request == nil {
		return alfred.InvocationRequest{}, errors.B.
			Op(op).
			KindInvalidArgument().
			Text("request is nil").
			Build()
	}
	source, err := decodeInvocationSource(request.GetSource())
	if err != nil {
		return alfred.InvocationRequest{}, errors.B.Op(op).Err(err).Build()
	}
	input, err := decodeContentParts(request.GetInput())
	if err != nil {
		return alfred.InvocationRequest{}, errors.B.Op(op).Err(err).Build()
	}
	return alfred.InvocationRequest{
		ProjectID: alfred.ProjectID(request.GetProjectId()),
		SessionID: alfred.SessionID(request.GetSessionId()),
		Source:    source,
		Input:     input,
	}, nil
}

func decodeInvocationSource(source alfredv1.InvocationSource) (alfred.InvocationSource, error) {
	const op = "decodeInvocationSource"

	switch source {
	case alfredv1.InvocationSource_INVOCATION_SOURCE_USER:
		return alfred.InvocationSourceUser, nil
	case alfredv1.InvocationSource_INVOCATION_SOURCE_SCHEDULE:
		return alfred.InvocationSourceSchedule, nil
	case alfredv1.InvocationSource_INVOCATION_SOURCE_WEBHOOK:
		return alfred.InvocationSourceWebhook, nil
	case alfredv1.InvocationSource_INVOCATION_SOURCE_MONITOR:
		return alfred.InvocationSourceMonitor, nil
	case alfredv1.InvocationSource_INVOCATION_SOURCE_INTERNAL:
		return "", errors.B.
			Op(op).
			KindInvalidArgument().
			Text("internal invocation source is not accepted over RPC").
			UserMsg("internal is not a valid RPC invocation source").
			Build()
	default:
		return "", errors.B.
			Op(op).
			KindInvalidArgument().
			Textf("unknown invocation source %d", source).
			Build()
	}
}

func decodeContentParts(parts []*alfredv1.ContentPart) ([]alfred.ContentPart, error) {
	const op = "decodeContentParts"

	decoded := make([]alfred.ContentPart, 0, len(parts))
	for index, part := range parts {
		if part == nil {
			return nil, errors.B.
				Op(op).
				KindInvalidArgument().
				Textf("content part %d is nil", index).
				Build()
		}
		switch value := part.GetValue().(type) {
		case *alfredv1.ContentPart_Text:
			decoded = append(decoded, alfred.TextContent{Text: value.Text})
		case *alfredv1.ContentPart_Resource:
			resource, err := decodeResourceContent(value.Resource)
			if err != nil {
				return nil, errors.B.Op(op).Textf("decode content part %d", index).Err(err).Build()
			}
			decoded = append(decoded, resource)
		default:
			return nil, errors.B.
				Op(op).
				KindInvalidArgument().
				Textf("content part %d has no value", index).
				Build()
		}
	}
	return decoded, nil
}

func decodeResourceContent(resource *alfredv1.ResourceContent) (alfred.ResourceContent, error) {
	const op = "decodeResourceContent"

	if resource == nil || resource.GetResource() == nil {
		return alfred.ResourceContent{}, errors.B.
			Op(op).
			KindInvalidArgument().
			Text("resource content has no reference").
			Build()
	}
	kind, err := decodeResourceKind(resource.GetKind())
	if err != nil {
		return alfred.ResourceContent{}, errors.B.Op(op).Err(err).Build()
	}
	return alfred.ResourceContent{
		Type: kind,
		Resource: alfred.ResourceRef{
			URI:       resource.GetResource().GetUri(),
			MediaType: resource.GetResource().GetMediaType(),
			Name:      resource.GetResource().GetName(),
		},
	}, nil
}

func decodeResourceKind(kind alfredv1.ContentKind) (alfred.ContentKind, error) {
	const op = "decodeResourceKind"

	switch kind {
	case alfredv1.ContentKind_CONTENT_KIND_IMAGE:
		return alfred.ContentKindImage, nil
	case alfredv1.ContentKind_CONTENT_KIND_AUDIO:
		return alfred.ContentKindAudio, nil
	case alfredv1.ContentKind_CONTENT_KIND_VIDEO:
		return alfred.ContentKindVideo, nil
	case alfredv1.ContentKind_CONTENT_KIND_FILE:
		return alfred.ContentKindFile, nil
	default:
		return "", errors.B.
			Op(op).
			KindInvalidArgument().
			Textf("unknown resource content kind %d", kind).
			Build()
	}
}

func encodeRun(run alfred.Run) (*alfredv1.Run, error) {
	const op = "encodeRun"

	status, err := encodeRunStatus(run.Status)
	if err != nil {
		return nil, errors.B.Op(op).Err(err).Build()
	}
	return &alfredv1.Run{
		Id:           string(run.ID),
		InvocationId: string(run.InvocationID),
		SessionId:    string(run.SessionID),
		DefaultModel: &alfredv1.ModelRef{
			ProviderId: string(run.DefaultModel.ProviderID),
			ModelId:    string(run.DefaultModel.ModelID),
		},
		Status:    status,
		CreatedAt: timestamppb.New(run.CreatedAt),
	}, nil
}

func encodeRunStatus(status alfred.RunStatus) (alfredv1.RunStatus, error) {
	const op = "encodeRunStatus"

	switch status {
	case alfred.RunStatusQueued:
		return alfredv1.RunStatus_RUN_STATUS_QUEUED, nil
	case alfred.RunStatusRunning:
		return alfredv1.RunStatus_RUN_STATUS_RUNNING, nil
	case alfred.RunStatusWaitingForApproval:
		return alfredv1.RunStatus_RUN_STATUS_WAITING_FOR_APPROVAL, nil
	case alfred.RunStatusWaitingForInput:
		return alfredv1.RunStatus_RUN_STATUS_WAITING_FOR_INPUT, nil
	case alfred.RunStatusInterrupted:
		return alfredv1.RunStatus_RUN_STATUS_INTERRUPTED, nil
	case alfred.RunStatusCompleted:
		return alfredv1.RunStatus_RUN_STATUS_COMPLETED, nil
	case alfred.RunStatusFailed:
		return alfredv1.RunStatus_RUN_STATUS_FAILED, nil
	case alfred.RunStatusCancelled:
		return alfredv1.RunStatus_RUN_STATUS_CANCELLED, nil
	default:
		return alfredv1.RunStatus_RUN_STATUS_UNSPECIFIED, errors.B.
			Op(op).
			KindInternal().
			Textf("unknown run status %q", status).
			Build()
	}
}

var _ alfredv1connect.InvocationServiceHandler = (*InvocationHandler)(nil)
