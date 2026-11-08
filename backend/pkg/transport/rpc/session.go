package rpc

import (
	"context"
	"iter"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ArionMiles/alfred"
	alfredv1 "github.com/ArionMiles/alfred/gen/alfred/v1"
	"github.com/ArionMiles/alfred/gen/alfred/v1/alfredv1connect"
	"github.com/ArionMiles/alfred/pkg/events"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

type sessionReader interface {
	GetSession(context.Context, alfred.SessionID) (alfred.Session, error)
	ListSessions(context.Context, alfred.ProjectID, alfred.SessionListOptions) (alfred.SessionPage, error)
}

type sessionEventReader interface {
	ReadSessionEvents(context.Context, alfred.SessionID) iter.Seq2[alfred.RunEvent, error]
}

// SessionHandler exposes session queries over RPC.
type SessionHandler struct {
	sessions    sessionReader
	eventReader sessionEventReader
	broker      *events.Broker
}

// NewSessionHandler creates a session RPC handler.
func NewSessionHandler(
	sessions sessionReader,
	eventReader sessionEventReader,
	broker *events.Broker,
) *SessionHandler {
	return &SessionHandler{sessions: sessions, eventReader: eventReader, broker: broker}
}

// WatchSession replays durable events and then follows live session activity.
func (handler *SessionHandler) WatchSession(
	ctx context.Context,
	request *alfredv1.WatchSessionRequest,
	stream alfredv1connect.SessionServiceWatchSessionServerStream,
) error {
	const op = "WatchSession"

	if request == nil {
		return connectError(errors.B.
			Op(op).
			KindInvalidArgument().
			Text("request is nil").
			Build())
	}
	sessionID := alfred.SessionID(request.GetSessionId())
	subscription := handler.broker.SubscribeSession(sessionID)
	defer subscription.Close()

	session, err := handler.sessions.GetSession(ctx, sessionID)
	if err != nil {
		return connectError(err)
	}
	if err := stream.Send(&alfredv1.WatchSessionResponse{
		Item: &alfredv1.WatchSessionResponse_Snapshot{
			Snapshot: &alfredv1.WatchSessionSnapshot{Session: encodeSession(session)},
		},
	}); err != nil {
		return errors.B.Op(op).Textf("send session %q snapshot", sessionID).Err(err).Build()
	}

	sequence, err := handler.sendSessionEvents(ctx, stream, sessionID, request.GetAfterSequence())
	if err != nil {
		return connectError(errors.B.Op(op).Textf("replay session %q events", sessionID).Err(err).Build())
	}
	if err := stream.Send(&alfredv1.WatchSessionResponse{
		Item: &alfredv1.WatchSessionResponse_CaughtUp{
			CaughtUp: &alfredv1.WatchSessionCaughtUp{ThroughSequence: sequence},
		},
	}); err != nil {
		return errors.B.Op(op).Textf("send session %q caught-up marker", sessionID).Err(err).Build()
	}
	if err := handler.followSession(ctx, stream, subscription, sessionID, sequence); err != nil {
		return connectError(errors.B.Op(op).Textf("follow session %q", sessionID).Err(err).Build())
	}
	return nil
}

func (handler *SessionHandler) followSession(
	ctx context.Context,
	stream alfredv1connect.SessionServiceWatchSessionServerStream,
	subscription *events.Subscription,
	sessionID alfred.SessionID,
	sequence uint64,
) error {
	const op = "followSession"

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-subscription.Changes():
			next, err := handler.sendSessionEvents(ctx, stream, sessionID, sequence)
			if err != nil {
				return errors.B.Op(op).Text("replay durable events").Err(err).Build()
			}
			sequence = next
		case activity := <-subscription.Updates():
			update, gap, err := encodeUpdateActivity(activity)
			if err != nil {
				return errors.B.Op(op).Text("encode transient activity").Err(err).Build()
			}
			response := &alfredv1.WatchSessionResponse{}
			switch {
			case update != nil:
				response.Item = &alfredv1.WatchSessionResponse_Update{Update: update}
			case gap:
				response.Item = &alfredv1.WatchSessionResponse_UpdateGap{UpdateGap: &alfredv1.RunUpdateGap{}}
			default:
				continue
			}
			if err := stream.Send(response); err != nil {
				return errors.B.Op(op).Text("send transient activity").Err(err).Build()
			}
		}
	}
}

func (handler *SessionHandler) sendSessionEvents(
	ctx context.Context,
	stream alfredv1connect.SessionServiceWatchSessionServerStream,
	sessionID alfred.SessionID,
	after uint64,
) (uint64, error) {
	const op = "sendSessionEvents"

	through := after
	committed := make(map[alfred.ModelCallID]bool)
	for event, err := range handler.eventReader.ReadSessionEvents(ctx, sessionID) {
		if err != nil {
			return through, errors.B.Op(op).Text("read events").Err(err).Build()
		}
		if event.Sequence <= through {
			if completion, ok := event.Payload.(alfred.ModelCallCompleted); ok && completion.Output != nil {
				committed[completion.CallID] = true
			}
			continue
		}
		encoded, err := encodeSessionEvent(event, committed)
		if err != nil {
			return through, errors.B.Op(op).Textf("encode run event %q", event.ID).Err(err).Build()
		}
		through = event.Sequence
		if encoded == nil {
			continue
		}
		if err := stream.Send(&alfredv1.WatchSessionResponse{
			Item: &alfredv1.WatchSessionResponse_Event{Event: encoded},
		}); err != nil {
			return through, errors.B.Op(op).Textf("send run event %q", event.ID).Err(err).Build()
		}
	}
	return through, nil
}

// GetSession returns a session by identity.
func (handler *SessionHandler) GetSession(
	ctx context.Context,
	request *alfredv1.GetSessionRequest,
) (*alfredv1.GetSessionResponse, error) {
	if request == nil {
		return nil, connectError(errors.B.
			Op("GetSession").
			KindInvalidArgument().
			Text("request is nil").
			Build())
	}
	session, err := handler.sessions.GetSession(ctx, alfred.SessionID(request.GetSessionId()))
	if err != nil {
		return nil, connectError(err)
	}
	return &alfredv1.GetSessionResponse{Session: encodeSession(session)}, nil
}

// ListSessions returns one creation-ordered page for a project.
func (handler *SessionHandler) ListSessions(
	ctx context.Context,
	request *alfredv1.ListSessionsRequest,
) (*alfredv1.ListSessionsResponse, error) {
	const op = "ListSessions"

	if request == nil {
		return nil, connectError(errors.B.
			Op(op).
			KindInvalidArgument().
			Text("request is nil").
			Build())
	}
	if request.GetPageSize() == 0 {
		return nil, connectError(errors.B.
			Op(op).
			KindInvalidArgument().
			Text("page size is zero").
			UserMsg("page size must be greater than zero").
			Build())
	}
	if request.GetPageSize() > maxPageSize {
		return nil, connectError(errors.B.
			Op(op).
			KindInvalidArgument().
			Textf("page size %d exceeds maximum %d", request.GetPageSize(), maxPageSize).
			UserMsg("page size must not exceed 100").
			Build())
	}
	after, err := decodeSessionCursor(request.GetAfter())
	if err != nil {
		return nil, connectError(errors.B.Op(op).Err(err).Build())
	}
	page, err := handler.sessions.ListSessions(
		ctx,
		alfred.ProjectID(request.GetProjectId()),
		alfred.SessionListOptions{Limit: request.GetPageSize(), After: after},
	)
	if err != nil {
		return nil, connectError(err)
	}
	sessions := make([]*alfredv1.Session, 0, len(page.Sessions))
	for _, session := range page.Sessions {
		sessions = append(sessions, encodeSession(session))
	}
	return &alfredv1.ListSessionsResponse{
		Sessions: sessions,
		Next:     encodeSessionCursor(page.Next),
	}, nil
}

func decodeSessionCursor(cursor *alfredv1.SessionCursor) (*alfred.SessionCursor, error) {
	const op = "decodeSessionCursor"

	if cursor == nil {
		return nil, nil
	}
	if err := cursor.GetCreatedAt().CheckValid(); err != nil {
		return nil, errors.B.
			Op(op).
			KindInvalidArgument().
			Text("cursor has invalid creation time").
			Err(err).
			Build()
	}
	return &alfred.SessionCursor{
		CreatedAt: cursor.GetCreatedAt().AsTime(),
		ID:        alfred.SessionID(cursor.GetId()),
	}, nil
}

func encodeSession(session alfred.Session) *alfredv1.Session {
	return &alfredv1.Session{
		Id:        string(session.ID),
		ProjectId: string(session.ProjectID),
		Title:     session.Title,
		CreatedAt: timestamppb.New(session.CreatedAt),
	}
}

func encodeSessionCursor(cursor *alfred.SessionCursor) *alfredv1.SessionCursor {
	if cursor == nil {
		return nil
	}
	return &alfredv1.SessionCursor{
		CreatedAt: timestamppb.New(cursor.CreatedAt),
		Id:        string(cursor.ID),
	}
}

var _ alfredv1connect.SessionServiceHandler = (*SessionHandler)(nil)
