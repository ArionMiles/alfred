package providers

import (
	"slices"
	"sort"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

// SetupService exposes provider-defined forms independently of their clients.
// Metadata is separate from Registry, which resolves executable inference clients.
type SetupService struct {
	definitions map[alfred.ProviderType]alfred.ProviderDefinition
}

// NewSetupService registers trusted backend definitions, copying their slices
// so preparing or editing one form does not change subsequent requests.
func NewSetupService(definitions []alfred.ProviderDefinition) (*SetupService, error) {
	const op = "NewSetupService"

	registered := make(map[alfred.ProviderType]alfred.ProviderDefinition, len(definitions))
	for _, definition := range definitions {
		if _, exists := registered[definition.Type]; exists {
			return nil, errors.B.Op(op).KindConflict().Text("provider definition is registered twice").Build()
		}
		registered[definition.Type] = cloneDefinition(definition)
	}
	return &SetupService{definitions: registered}, nil
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
	form := alfred.ProviderSetupForm{
		Type: providerType, Method: cloneDefinition(definition).Methods[index], Values: make(map[string]string),
	}
	if instance == nil {
		return form, nil
	}
	if instance.Type != providerType {
		return alfred.ProviderSetupForm{}, errors.B.Op(op).KindInvalidArgument().Text("provider type does not match the instance").Build()
	}
	form.InstanceID = instance.ID
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
