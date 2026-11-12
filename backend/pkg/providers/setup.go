package providers

import (
	"context"
	"maps"
	"slices"
	"sort"
	"sync"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/identity"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

// SetupService exposes provider-defined onboarding methods independently of their clients.
// Metadata is separate from Registry, which resolves executable inference clients.
type SetupService struct {
	definitions map[alfred.ProviderType]alfred.ProviderDefinition
	store       setupTarget
	credentials alfred.CredentialWriter
	authorizers map[alfred.ProviderType]alfred.ProviderAuthorizer
	// attemptsMu serializes admission, polling, cancellation, and workflow completion.
	attemptsMu sync.Mutex
	stopping   bool
	attempts   map[alfred.ProviderAuthorizationID]*authorizationAttempt
}

// NewSetupService registers trusted backend definitions, copying their slices
// so preparing or editing one method does not change subsequent requests.
func NewSetupService(
	definitions []alfred.ProviderDefinition,
	store setupTarget,
	credentials alfred.CredentialWriter,
	options ...SetupOption,
) (*SetupService, error) {
	const op = "NewSetupService"

	registered := make(map[alfred.ProviderType]alfred.ProviderDefinition, len(definitions))
	for _, definition := range definitions {
		if _, exists := registered[definition.Type]; exists {
			return nil, errors.B.Op(op).KindConflict().Text("provider definition is registered twice").Build()
		}
		definition = cloneDefinition(definition)
		for index := range definition.Methods {
			if definition.Methods[index].Kind == "" {
				definition.Methods[index].Kind = alfred.ProviderSetupMethodForm
			}
			if definition.Methods[index].InstanceName == "" {
				definition.Methods[index].InstanceName = definition.Name
			}
		}
		registered[definition.Type] = definition
	}
	service := &SetupService{
		definitions: registered, store: store, credentials: credentials,
		authorizers: make(map[alfred.ProviderType]alfred.ProviderAuthorizer), attempts: make(map[alfred.ProviderAuthorizationID]*authorizationAttempt),
	}
	for _, option := range options {
		option(service)
	}
	return service, nil
}

// ListDefinitions returns setup metadata in stable provider-type order.
func (service *SetupService) ListDefinitions() []alfred.ProviderDefinition {
	definitions := make([]alfred.ProviderDefinition, 0, len(service.definitions))
	for _, definition := range service.definitions {
		definitions = append(definitions, cloneDefinition(definition))
	}
	sort.Slice(definitions, func(left, right int) bool { return definitions[left].Type < definitions[right].Type })
	return definitions
}

// PrepareForm returns the same method for a new or existing connection. Existing
// settings come from saved provider metadata.
// It never reads credentials or treats a reference as proof of valid auth.
func (service *SetupService) PrepareForm(
	providerType alfred.ProviderType,
	methodID string,
	instance *alfred.ProviderInstance,
) (alfred.ProviderSetupForm, error) {
	const op = "PrepareForm"

	definition, exists := service.definitions[providerType]
	if !exists {
		return alfred.ProviderSetupForm{}, errors.B.Op(op).KindNotFound().Text("provider definition does not exist").Build()
	}
	index := slices.IndexFunc(definition.Methods, func(method alfred.ProviderSetupMethod) bool { return method.ID == methodID })
	if index < 0 {
		return alfred.ProviderSetupForm{}, errors.B.Op(op).KindNotFound().Text("provider setup method does not exist").Build()
	}
	if definition.Methods[index].Kind != alfred.ProviderSetupMethodForm {
		return alfred.ProviderSetupForm{}, errors.B.Op(op).KindFailedPrecondition().Text("provider setup method is not a form").Build()
	}
	form := alfred.ProviderSetupForm{
		Type: providerType, InstanceID: alfred.ProviderID(providerType),
		Method: cloneDefinition(definition).Methods[index], Values: make(map[string]string),
	}
	if instance == nil {
		return form, nil
	}
	if instance.Type != providerType {
		return alfred.ProviderSetupForm{}, errors.B.Op(op).KindInvalidArgument().Text("provider type does not match the instance").Build()
	}
	if instance.ID != form.InstanceID {
		return alfred.ProviderSetupForm{}, errors.B.Op(op).KindInvalidArgument().Text("provider instance does not match the configured singleton").Build()
	}
	for _, field := range form.Method.Fields {
		if field.Kind != alfred.SetupFieldSecret {
			if value, exists := instance.Settings[field.ID]; exists {
				form.Values[field.ID] = value
			}
		}
	}
	return form, nil
}

func cloneDefinition(definition alfred.ProviderDefinition) alfred.ProviderDefinition {
	definition.Methods = slices.Clone(definition.Methods)
	for index := range definition.Methods {
		method := &definition.Methods[index]
		method.Fields = slices.Clone(method.Fields)
		for fieldIndex := range method.Fields {
			method.Fields[fieldIndex].Options = slices.Clone(method.Fields[fieldIndex].Options)
		}
	}
	return definition
}

// SetupOption installs an interactive setup capability.
type SetupOption func(*SetupService)

// WithAuthorizer registers the onboarding implementation for a provider.
func WithAuthorizer(provider alfred.ProviderType, authorizer alfred.ProviderAuthorizer) SetupOption {
	return func(service *SetupService) { service.authorizers[provider] = authorizer }
}

type authorizationAttempt struct {
	result alfred.ProviderAuthorization
	cancel context.CancelFunc
	done   chan struct{}
}

// Prepare loads existing public settings and prepares a provider form.
func (service *SetupService) Prepare(ctx context.Context, provider alfred.ProviderType, method string) (alfred.ProviderSetupForm, error) {
	const op = "Prepare"
	instance, err := service.store.GetProviderInstance(ctx, alfred.ProviderID(provider))
	var existing *alfred.ProviderInstance
	if err == nil {
		existing = &instance
	} else if errors.WhatKind(err) != errors.NotFound {
		return alfred.ProviderSetupForm{}, errors.B.Op(op).Err(err).Build()
	}
	return service.PrepareForm(provider, method, existing)
}

// Submit validates and saves a provider form using the configured credential store.
func (service *SetupService) Submit(ctx context.Context, request alfred.ProviderSetupSubmission) (alfred.ProviderSetupResult, error) {
	return NewSubmitter(service, service.credentials).Submit(ctx, service.store, request)
}

// Start accepts a workflow independently of the initiating request's lifetime.
func (service *SetupService) Start(ctx context.Context, request alfred.ProviderAuthorizationRequest) (alfred.ProviderAuthorization, error) {
	const op = "Start"
	if err := ctx.Err(); err != nil {
		return alfred.ProviderAuthorization{}, errors.B.Op(op).Err(err).Build()
	}
	authorizer, exists := service.authorizers[request.Type]
	if !exists {
		return alfred.ProviderAuthorization{}, errors.B.Op(op).KindFailedPrecondition().Text("provider authorization is unavailable").Build()
	}
	service.attemptsMu.Lock()
	if service.stopping {
		service.attemptsMu.Unlock()
		return alfred.ProviderAuthorization{}, errors.B.Op(op).KindFailedPrecondition().UserMsg("Alfred is shutting down.").Build()
	}
	lifetime, cancel := context.WithCancel(context.Background())
	attempt := &authorizationAttempt{
		result: alfred.ProviderAuthorization{ID: identity.New[alfred.ProviderAuthorizationID](), State: alfred.ProviderAuthorizationPending},
		cancel: cancel, done: make(chan struct{}),
	}
	service.attempts[attempt.result.ID] = attempt
	service.attemptsMu.Unlock()
	id := attempt.result.ID
	ready := make(chan struct{}, 1)
	go service.authorize(lifetime, authorizer, request, attempt, ready)
	select {
	case <-ready:
	case <-attempt.done:
	case <-ctx.Done():
		return alfred.ProviderAuthorization{}, errors.B.Op(op).Err(ctx.Err()).Build()
	}
	return service.Get(ctx, id)
}

func (service *SetupService) authorize(
	ctx context.Context,
	authorizer alfred.ProviderAuthorizer,
	request alfred.ProviderAuthorizationRequest,
	attempt *authorizationAttempt,
	ready chan<- struct{},
) {
	result, err := authorizer.Authorize(ctx, request, func(url string) {
		service.attemptsMu.Lock()
		attempt.result.AuthorizationURL = url
		service.attemptsMu.Unlock()
		ready <- struct{}{}
	})
	attempt.cancel()
	service.attemptsMu.Lock()
	defer service.attemptsMu.Unlock()
	if err != nil {
		result = alfred.ProviderAuthorization{State: alfred.ProviderAuthorizationFailed, Message: errors.UserMsg(err)}
	}
	result.ID = attempt.result.ID
	result.AuthorizationURL = ""
	attempt.result = result
	close(attempt.done)
	// Retain only the safe polling result after execution resources are released.
	attempt.cancel = nil
}

// Get returns safe progress without exposing workflow resources or credentials.
func (service *SetupService) Get(_ context.Context, id alfred.ProviderAuthorizationID) (alfred.ProviderAuthorization, error) {
	const op = "Get"
	service.attemptsMu.Lock()
	defer service.attemptsMu.Unlock()
	attempt, exists := service.attempts[id]
	if !exists {
		return alfred.ProviderAuthorization{}, errors.B.Op(op).KindNotFound().
			UserMsg("Authorization is no longer available. Reopen Providers and start again.").Build()
	}
	result := attempt.result
	if result.Instance != nil {
		instance := *result.Instance
		instance.Settings = maps.Clone(instance.Settings)
		result.Instance = &instance
	}
	return result, nil
}

// Cancel requests cancellation and waits for callback and credential work to stop.
func (service *SetupService) Cancel(ctx context.Context, id alfred.ProviderAuthorizationID) (alfred.ProviderAuthorization, error) {
	const op = "Cancel"
	service.attemptsMu.Lock()
	attempt, exists := service.attempts[id]
	if !exists {
		service.attemptsMu.Unlock()
		return alfred.ProviderAuthorization{}, errors.B.Op(op).KindNotFound().Text("provider authorization does not exist").Build()
	}
	if attempt.cancel != nil {
		attempt.cancel()
	}
	done := attempt.done
	service.attemptsMu.Unlock()
	select {
	case <-done:
		return service.Get(ctx, id)
	case <-ctx.Done():
		return alfred.ProviderAuthorization{}, errors.B.Op(op).Err(ctx.Err()).Build()
	}
}

// Shutdown stops admission, cancels active workflows, and waits for their writers.
func (service *SetupService) Shutdown(ctx context.Context) error {
	const op = "Shutdown"
	service.attemptsMu.Lock()
	service.stopping = true
	attempts := maps.Clone(service.attempts)
	for _, attempt := range attempts {
		if attempt.cancel != nil {
			attempt.cancel()
		}
	}
	service.attemptsMu.Unlock()
	for _, attempt := range attempts {
		select {
		case <-attempt.done:
		case <-ctx.Done():
			return errors.B.Op(op).Text("wait for provider authorization shutdown").Err(ctx.Err()).Build()
		}
	}
	return nil
}
