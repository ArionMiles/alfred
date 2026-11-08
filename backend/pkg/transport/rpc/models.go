package rpc

import (
	"context"

	"github.com/ArionMiles/alfred"
	alfredv1 "github.com/ArionMiles/alfred/gen/alfred/v1"
	"github.com/ArionMiles/alfred/gen/alfred/v1/alfredv1connect"
	"github.com/ArionMiles/alfred/pkg/providers"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

// ModelHandler exposes discovery and preference selection without credentials.
type ModelHandler struct{ service *providers.ModelService }

// NewModelHandler composes the model-service RPC boundary.
func NewModelHandler(service *providers.ModelService) *ModelHandler {
	return &ModelHandler{service: service}
}

// ListModels returns configured choices with provider display names.
func (handler *ModelHandler) ListModels(ctx context.Context, request *alfredv1.ListModelsRequest) (*alfredv1.ListModelsResponse, error) {
	const op = "ListModels"
	if request == nil {
		return nil, connectError(errors.B.Op(op).KindInvalidArgument().Text("request is nil").Build())
	}
	choices, err := handler.service.ListModels(ctx)
	if err != nil {
		return nil, connectError(err)
	}
	response := &alfredv1.ListModelsResponse{}
	for _, choice := range choices {
		encoded, err := encodeModelChoice(choice)
		if err != nil {
			return nil, connectError(err)
		}
		response.Models = append(response.Models, encoded)
	}
	return response, nil
}

// GetSelectedModel returns an unset reference before the first selection.
func (handler *ModelHandler) GetSelectedModel(ctx context.Context, request *alfredv1.GetSelectedModelRequest) (*alfredv1.GetSelectedModelResponse, error) {
	const op = "GetSelectedModel"
	if request == nil {
		return nil, connectError(errors.B.Op(op).KindInvalidArgument().Text("request is nil").Build())
	}
	model, err := handler.service.GetSelectedModel(ctx)
	if errors.WhatKind(err) == errors.NotFound {
		return &alfredv1.GetSelectedModelResponse{}, nil
	}
	if err != nil {
		return nil, connectError(err)
	}
	return &alfredv1.GetSelectedModelResponse{Model: encodeModelRef(model)}, nil
}

// SelectModel saves a validated default for future runs.
func (handler *ModelHandler) SelectModel(ctx context.Context, request *alfredv1.SelectModelRequest) (*alfredv1.SelectModelResponse, error) {
	const op = "SelectModel"
	if request == nil {
		return nil, connectError(errors.B.Op(op).KindInvalidArgument().Text("request is nil").Build())
	}
	model := alfred.ModelRef{ProviderID: alfred.ProviderID(request.GetModel().GetProviderId()), ModelID: alfred.ModelID(request.GetModel().GetModelId())}
	if err := handler.service.SelectModel(ctx, model); err != nil {
		return nil, connectError(err)
	}
	return &alfredv1.SelectModelResponse{Model: encodeModelRef(model)}, nil
}

func encodeModelRef(model alfred.ModelRef) *alfredv1.ModelRef {
	return &alfredv1.ModelRef{ProviderId: string(model.ProviderID), ModelId: string(model.ModelID)}
}

func encodeModelChoice(choice providers.ModelChoice) (*alfredv1.ModelInfo, error) {
	capabilities := choice.Model.Capabilities
	encoded := &alfredv1.ModelCapabilities{
		ContextWindow: capabilities.ContextWindow, MaxOutputTokens: capabilities.MaxOutputTokens,
		ToolCalling: capabilities.ToolCalling, Reasoning: capabilities.Reasoning,
	}
	for _, kind := range capabilities.InputModalities {
		wire, err := encodeModelModality(kind)
		if err != nil {
			return nil, err
		}
		encoded.InputModalities = append(encoded.InputModalities, wire)
	}
	for _, kind := range capabilities.OutputModalities {
		wire, err := encodeModelModality(kind)
		if err != nil {
			return nil, err
		}
		encoded.OutputModalities = append(encoded.OutputModalities, wire)
	}
	return &alfredv1.ModelInfo{Ref: encodeModelRef(choice.Model.Ref), Name: choice.Model.Name, ProviderName: choice.ProviderName, Capabilities: encoded}, nil
}

var _ alfredv1connect.ModelServiceHandler = (*ModelHandler)(nil)

func encodeModelModality(kind alfred.ContentKind) (alfredv1.ContentKind, error) {
	if kind == alfred.ContentKindText {
		return alfredv1.ContentKind_CONTENT_KIND_TEXT, nil
	}
	return encodeContentKind(kind)
}
