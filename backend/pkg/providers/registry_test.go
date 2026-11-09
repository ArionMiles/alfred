package providers

import (
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/mocks"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

func TestRegistryResolvesProvider(t *testing.T) {
	want := mocks.NewMockModelProvider(gomock.NewController(t))
	want.EXPECT().ID().Return(alfred.ProviderID("provider-1"))
	registry, err := NewRegistry([]alfred.ModelProvider{want})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	got, err := registry.ResolveProvider(t.Context(), "provider-1")
	if err != nil {
		t.Fatalf("ResolveProvider() error = %v", err)
	}
	if got != want {
		t.Fatalf("ResolveProvider() = %p, want %p", got, want)
	}
}

func TestNewRegistryRejectsDuplicateProviderID(t *testing.T) {
	provider := mocks.NewMockModelProvider(gomock.NewController(t))
	provider.EXPECT().ID().Return(alfred.ProviderID("provider-1")).Times(2)
	_, err := NewRegistry([]alfred.ModelProvider{provider, provider})
	if err == nil {
		t.Fatal("NewRegistry() error = nil, want conflict")
	}
	if got := errors.WhatKind(err); got != errors.Conflict {
		t.Fatalf("errors.WhatKind() = %#v, want %#v", got, errors.Conflict)
	}
}

func TestRegistryReportsMissingProvider(t *testing.T) {
	registry, err := NewRegistry(nil)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	_, err = registry.ResolveProvider(t.Context(), "missing")
	if err == nil {
		t.Fatal("ResolveProvider() error = nil, want not found")
	}
	if got := errors.WhatKind(err); got != errors.NotFound {
		t.Fatalf("errors.WhatKind() = %#v, want %#v", got, errors.NotFound)
	}
}

func TestRegistryDelegatesAndDecoratesConfiguredProviders(t *testing.T) {
	t.Parallel()
	controller := gomock.NewController(t)
	resolver := mocks.NewMockProviderResolver(controller)
	resolved := mocks.NewMockModelProvider(controller)
	decorated := mocks.NewMockModelProvider(controller)
	resolver.EXPECT().ResolveProvider(gomock.Any(), alfred.ProviderID("custom-instance")).Return(resolved, nil)
	registry, err := NewRegistry(nil,
		WithConfiguredProviders(resolver),
		WithMiddleware(func(provider alfred.ModelProvider) alfred.ModelProvider {
			if provider != resolved {
				t.Fatal("middleware did not receive the resolved provider")
			}
			return decorated
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := registry.ResolveProvider(t.Context(), "custom-instance")
	if err != nil || provider != decorated {
		t.Fatalf("ResolveProvider() = %v, %v", provider, err)
	}
}

func TestRegistryPrefersRegisteredProviderOverConfiguredResolver(t *testing.T) {
	t.Parallel()
	controller := gomock.NewController(t)
	resolver := mocks.NewMockProviderResolver(controller)
	registered := mocks.NewMockModelProvider(controller)
	registered.EXPECT().ID().Return(alfred.ProviderID("custom-instance"))
	registry, err := NewRegistry([]alfred.ModelProvider{registered}, WithConfiguredProviders(resolver))
	if err != nil {
		t.Fatal(err)
	}
	provider, err := registry.ResolveProvider(t.Context(), "custom-instance")
	if err != nil || provider != registered {
		t.Fatalf("ResolveProvider() = %v, %v", provider, err)
	}
}
