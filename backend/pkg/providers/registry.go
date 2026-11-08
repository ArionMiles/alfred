// Package providers provides model-provider infrastructure for Alfred.
package providers

import (
	"context"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

// Middleware decorates a model provider.
type Middleware func(alfred.ModelProvider) alfred.ModelProvider

// Option configures a Registry.
type Option func(*Registry)

// WithMiddleware decorates every provider registered in the registry.
func WithMiddleware(middleware Middleware) Option {
	return func(registry *Registry) {
		registry.middlewares = append(registry.middlewares, middleware)
	}
}

// Registry resolves configured model providers.
type Registry struct {
	providers   map[alfred.ProviderID]alfred.ModelProvider
	middlewares []Middleware
	configured  *configuredProviders
}

// NewRegistry creates a registry containing providers.
func NewRegistry(
	providerList []alfred.ModelProvider,
	options ...Option,
) (*Registry, error) {
	const op = "NewRegistry"

	registry := &Registry{}
	for _, option := range options {
		option(registry)
	}

	registered := make(map[alfred.ProviderID]alfred.ModelProvider, len(providerList))
	for _, provider := range providerList {
		id := provider.ID()
		if _, exists := registered[id]; exists {
			return nil, errors.B.
				Op(op).
				KindConflict().
				Textf("provider %q is registered more than once", id).
				UserMsg("a model provider is configured more than once").
				Build()
		}
		registered[id] = registry.decorate(provider)
	}

	registry.providers = registered
	if registry.configured == nil {
		registry.middlewares = nil
	}
	return registry, nil
}

// ResolveProvider returns the provider registered with id.
func (registry *Registry) ResolveProvider(
	ctx context.Context,
	id alfred.ProviderID,
) (alfred.ModelProvider, error) {
	const op = "ResolveProvider"

	provider, ok := registry.providers[id]
	if !ok && registry.configured != nil {
		resolved, err := registry.configured.resolve(ctx, id)
		if err != nil {
			return nil, errors.B.Op(op).Err(err).Build()
		}
		return registry.decorate(resolved), nil
	}
	if !ok {
		return nil, errors.B.
			Op(op).
			KindNotFound().
			Textf("provider %q is not registered", id).
			UserMsg("the requested model provider is not available").
			Build()
	}

	return provider, nil
}

func (registry *Registry) decorate(provider alfred.ModelProvider) alfred.ModelProvider {
	for index := len(registry.middlewares) - 1; index >= 0; index-- {
		provider = registry.middlewares[index](provider)
	}
	return provider
}

var _ alfred.ProviderResolver = (*Registry)(nil)
