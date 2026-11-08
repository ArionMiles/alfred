package rpc

import (
	"context"
	"maps"

	"github.com/ArionMiles/alfred"
	alfredv1 "github.com/ArionMiles/alfred/gen/alfred/v1"
	"github.com/ArionMiles/alfred/gen/alfred/v1/alfredv1connect"
	"github.com/ArionMiles/alfred/pkg/providers"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

type providerSetupStore interface {
	GetProviderInstance(context.Context, alfred.ProviderID) (alfred.ProviderInstance, error)
	PutProviderInstance(context.Context, alfred.ProviderInstance) error
}

// ProviderSetupHandler exposes onboarding without exposing stored credentials.
type ProviderSetupHandler struct {
	setup     *providers.SetupService
	submitter *providers.Submitter
	store     providerSetupStore
}

// NewProviderSetupHandler composes provider forms and their persistence workflow.
func NewProviderSetupHandler(
	setup *providers.SetupService,
	submitter *providers.Submitter,
	store providerSetupStore,
) *ProviderSetupHandler {
	return &ProviderSetupHandler{setup: setup, submitter: submitter, store: store}
}

// ListProviderDefinitions returns the registered onboarding choices.
func (handler *ProviderSetupHandler) ListProviderDefinitions(
	_ context.Context,
	request *alfredv1.ListProviderDefinitionsRequest,
) (*alfredv1.ListProviderDefinitionsResponse, error) {
	const op = "ListProviderDefinitions"

	if request == nil {
		return nil, connectError(errors.B.Op(op).KindInvalidArgument().Text("request is nil").Build())
	}
	response := &alfredv1.ListProviderDefinitionsResponse{}
	for _, definition := range handler.setup.ListDefinitions() {
		encoded := &alfredv1.ProviderDefinition{Type: string(definition.Type), Name: definition.Name}
		for _, method := range definition.Methods {
			encoded.Methods = append(encoded.Methods, encodeProviderSetupMethod(method))
		}
		response.Definitions = append(response.Definitions, encoded)
	}
	return response, nil
}

// PrepareProviderSetup reads only non-secret metadata for an existing instance.
func (handler *ProviderSetupHandler) PrepareProviderSetup(
	ctx context.Context,
	request *alfredv1.PrepareProviderSetupRequest,
) (*alfredv1.PrepareProviderSetupResponse, error) {
	const op = "PrepareProviderSetup"

	if request == nil {
		return nil, connectError(errors.B.Op(op).KindInvalidArgument().Text("request is nil").Build())
	}
	var existing *alfred.ProviderInstance
	if request.GetInstanceId() != "" {
		instance, err := handler.store.GetProviderInstance(ctx, alfred.ProviderID(request.GetInstanceId()))
		if err != nil {
			return nil, connectError(err)
		}
		existing = &instance
	}
	form, err := handler.setup.PrepareForm(alfred.ProviderType(request.GetType()), request.GetMethodId(), existing)
	if err != nil {
		return nil, connectError(err)
	}
	return &alfredv1.PrepareProviderSetupResponse{Form: &alfredv1.ProviderSetupForm{
		Type: string(form.Type), InstanceId: string(form.InstanceID),
		Method: encodeProviderSetupMethod(form.Method), Values: maps.Clone(form.Values),
	}}, nil
}

// SubmitProviderSetup saves a complete form and returns safe validation feedback.
func (handler *ProviderSetupHandler) SubmitProviderSetup(
	ctx context.Context,
	request *alfredv1.SubmitProviderSetupRequest,
) (*alfredv1.SubmitProviderSetupResponse, error) {
	const op = "SubmitProviderSetup"

	if request == nil {
		return nil, connectError(errors.B.Op(op).KindInvalidArgument().Text("request is nil").Build())
	}
	result, err := handler.submitter.Submit(ctx, handler.store, alfred.ProviderSetupSubmission{
		ID: alfred.ProviderID(request.GetInstanceId()), Type: alfred.ProviderType(request.GetType()),
		MethodID: request.GetMethodId(), Name: request.GetName(), Enabled: request.GetEnabled(), Values: request.GetValues(),
	})
	if err != nil {
		return nil, connectError(err)
	}
	response := &alfredv1.SubmitProviderSetupResponse{}
	if instance := result.Instance; instance != nil {
		response.Instance = &alfredv1.ProviderInstance{
			Id: string(instance.ID), Type: string(instance.Type), Name: instance.Name,
			Enabled: instance.Enabled, Settings: maps.Clone(instance.Settings),
		}
	}
	for _, problem := range result.FieldErrors {
		response.FieldErrors = append(response.FieldErrors, &alfredv1.SetupFieldError{FieldId: problem.FieldID, Message: problem.Message})
	}
	return response, nil
}

func encodeProviderSetupMethod(method alfred.ProviderSetupMethod) *alfredv1.ProviderSetupMethod {
	encoded := &alfredv1.ProviderSetupMethod{Id: method.ID, Name: method.Name}
	for _, field := range method.Fields {
		encoded.Fields = append(encoded.Fields, encodeSetupField(field))
	}
	return encoded
}

var _ alfredv1connect.ProviderSetupServiceHandler = (*ProviderSetupHandler)(nil)
