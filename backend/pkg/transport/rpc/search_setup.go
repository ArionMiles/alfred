package rpc

import (
	"context"
	"maps"

	"github.com/ArionMiles/alfred"
	alfredv1 "github.com/ArionMiles/alfred/gen/alfred/v1"
	"github.com/ArionMiles/alfred/pkg/tools"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

// SearchSetupHandler exposes search configuration independently of model providers.
type SearchSetupHandler struct{ setup *tools.SearchSetup }

// NewSearchSetupHandler creates the search-only RPC handler.
func NewSearchSetupHandler(setup *tools.SearchSetup) *SearchSetupHandler {
	return &SearchSetupHandler{setup: setup}
}

// ListSearchBackends returns installed search choices, never saved secrets.
func (handler *SearchSetupHandler) ListSearchBackends(
	_ context.Context, request *alfredv1.ListSearchBackendsRequest,
) (*alfredv1.ListSearchBackendsResponse, error) {
	const op = "ListSearchBackends"
	if request == nil {
		return nil, connectError(errors.B.Op(op).KindInvalidArgument().Text("request is nil").Build())
	}
	response := &alfredv1.ListSearchBackendsResponse{}
	for _, definition := range handler.setup.List() {
		response.Backends = append(response.Backends, encodeSearchBackend(definition))
	}
	return response, nil
}

// PrepareSearchSetup loads non-secret settings; an unconfigured backend has an empty form.
func (handler *SearchSetupHandler) PrepareSearchSetup(
	ctx context.Context, request *alfredv1.PrepareSearchSetupRequest,
) (*alfredv1.PrepareSearchSetupResponse, error) {
	const op = "PrepareSearchSetup"
	if request == nil {
		return nil, connectError(errors.B.Op(op).KindInvalidArgument().Text("request is nil").Build())
	}
	form, err := handler.setup.Prepare(ctx, request.GetBackend())
	if err != nil {
		return nil, connectError(err)
	}
	return &alfredv1.PrepareSearchSetupResponse{Backend: encodeSearchBackend(form.Definition), Values: maps.Clone(form.Values)}, nil
}

// SaveSearchSetup returns field validation errors or a secret-free acknowledgement.
func (handler *SearchSetupHandler) SaveSearchSetup(ctx context.Context, request *alfredv1.SaveSearchSetupRequest) (*alfredv1.SaveSearchSetupResponse, error) {
	const op = "SaveSearchSetup"
	if request == nil {
		return nil, connectError(errors.B.Op(op).KindInvalidArgument().Text("request is nil").Build())
	}
	problems, err := handler.setup.Save(ctx, request.GetBackend(), request.GetValues())
	if err != nil {
		return nil, connectError(err)
	}
	response := &alfredv1.SaveSearchSetupResponse{Saved: len(problems) == 0}
	for _, problem := range problems {
		response.FieldErrors = append(response.FieldErrors, &alfredv1.SetupFieldError{FieldId: problem.FieldID, Message: problem.Message})
	}
	return response, nil
}

func encodeSearchBackend(definition alfred.SearchBackendDefinition) *alfredv1.SearchBackend {
	backend := &alfredv1.SearchBackend{Id: definition.ID, Name: definition.Name}
	for _, field := range definition.Fields {
		backend.Fields = append(backend.Fields, encodeSetupField(field))
	}
	return backend
}
