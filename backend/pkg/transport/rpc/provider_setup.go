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

// ProviderSetupHandler translates the onboarding RPC contract to the setup service.
type ProviderSetupHandler struct{ setup *providers.SetupService }

// NewProviderSetupHandler exposes provider setup without owning its workflows.
func NewProviderSetupHandler(setup *providers.SetupService) *ProviderSetupHandler {
	return &ProviderSetupHandler{setup: setup}
}

// StartProviderAuthorization begins an interactive provider setup method.
func (handler *ProviderSetupHandler) StartProviderAuthorization(
	ctx context.Context,
	request *alfredv1.StartProviderAuthorizationRequest,
) (*alfredv1.StartProviderAuthorizationResponse, error) {
	const op = "StartProviderAuthorization"

	if request == nil {
		return nil, connectError(errors.B.Op(op).KindInvalidArgument().Text("request is nil").Build())
	}
	authorization, err := handler.setup.Start(ctx, alfred.ProviderAuthorizationRequest{
		Type: alfred.ProviderType(request.GetType()), MethodID: request.GetMethodId(),
	})
	if err != nil {
		return nil, connectError(err)
	}
	return &alfredv1.StartProviderAuthorizationResponse{Authorization: encodeProviderAuthorization(authorization)}, nil
}

// GetProviderAuthorization reports progress without exposing credentials.
func (handler *ProviderSetupHandler) GetProviderAuthorization(
	ctx context.Context,
	request *alfredv1.GetProviderAuthorizationRequest,
) (*alfredv1.GetProviderAuthorizationResponse, error) {
	const op = "GetProviderAuthorization"

	if request == nil {
		return nil, connectError(errors.B.Op(op).KindInvalidArgument().Text("request is nil").Build())
	}
	authorization, err := handler.setup.Get(ctx, alfred.ProviderAuthorizationID(request.GetAuthorizationId()))
	if err != nil {
		return nil, connectError(err)
	}
	return &alfredv1.GetProviderAuthorizationResponse{Authorization: encodeProviderAuthorization(authorization)}, nil
}

// CancelProviderAuthorization stops a pending setup flow.
func (handler *ProviderSetupHandler) CancelProviderAuthorization(
	ctx context.Context,
	request *alfredv1.CancelProviderAuthorizationRequest,
) (*alfredv1.CancelProviderAuthorizationResponse, error) {
	const op = "CancelProviderAuthorization"

	if request == nil {
		return nil, connectError(errors.B.Op(op).KindInvalidArgument().Text("request is nil").Build())
	}
	authorization, err := handler.setup.Cancel(ctx, alfred.ProviderAuthorizationID(request.GetAuthorizationId()))
	if err != nil {
		return nil, connectError(err)
	}
	return &alfredv1.CancelProviderAuthorizationResponse{Authorization: encodeProviderAuthorization(authorization)}, nil
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
	form, err := handler.setup.Prepare(ctx, alfred.ProviderType(request.GetType()), request.GetMethodId())
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
	result, err := handler.setup.Submit(ctx, alfred.ProviderSetupSubmission{
		Type: alfred.ProviderType(request.GetType()), MethodID: request.GetMethodId(), Values: request.GetValues(),
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
	encoded := &alfredv1.ProviderSetupMethod{Id: method.ID, Name: method.Name, Kind: encodeProviderSetupMethodKind(method.Kind)}
	for _, field := range method.Fields {
		encoded.Fields = append(encoded.Fields, encodeSetupField(field))
	}
	return encoded
}

func encodeProviderSetupMethodKind(kind alfred.ProviderSetupMethodKind) alfredv1.ProviderSetupMethodKind {
	switch kind {
	case alfred.ProviderSetupMethodForm:
		return alfredv1.ProviderSetupMethodKind_PROVIDER_SETUP_METHOD_KIND_FORM
	case alfred.ProviderSetupMethodAuthorization:
		return alfredv1.ProviderSetupMethodKind_PROVIDER_SETUP_METHOD_KIND_AUTHORIZATION
	default:
		return alfredv1.ProviderSetupMethodKind_PROVIDER_SETUP_METHOD_KIND_UNSPECIFIED
	}
}

func encodeProviderAuthorization(value alfred.ProviderAuthorization) *alfredv1.ProviderAuthorization {
	encoded := &alfredv1.ProviderAuthorization{
		Id: string(value.ID), State: encodeProviderAuthorizationState(value.State),
		AuthorizationUrl: value.AuthorizationURL, AccountLabel: value.AccountLabel, Message: value.Message,
	}
	if value.Instance != nil {
		encoded.Instance = &alfredv1.ProviderInstance{
			Id: string(value.Instance.ID), Type: string(value.Instance.Type), Name: value.Instance.Name,
			Enabled: value.Instance.Enabled, Settings: maps.Clone(value.Instance.Settings),
		}
	}
	return encoded
}

func encodeProviderAuthorizationState(state alfred.ProviderAuthorizationState) alfredv1.ProviderAuthorizationState {
	switch state {
	case alfred.ProviderAuthorizationPending:
		return alfredv1.ProviderAuthorizationState_PROVIDER_AUTHORIZATION_STATE_PENDING
	case alfred.ProviderAuthorizationCompleted:
		return alfredv1.ProviderAuthorizationState_PROVIDER_AUTHORIZATION_STATE_COMPLETED
	case alfred.ProviderAuthorizationDenied:
		return alfredv1.ProviderAuthorizationState_PROVIDER_AUTHORIZATION_STATE_DENIED
	case alfred.ProviderAuthorizationFailed:
		return alfredv1.ProviderAuthorizationState_PROVIDER_AUTHORIZATION_STATE_FAILED
	case alfred.ProviderAuthorizationCanceled:
		return alfredv1.ProviderAuthorizationState_PROVIDER_AUTHORIZATION_STATE_CANCELED
	default:
		return alfredv1.ProviderAuthorizationState_PROVIDER_AUTHORIZATION_STATE_UNSPECIFIED
	}
}

var _ alfredv1connect.ProviderSetupServiceHandler = (*ProviderSetupHandler)(nil)
