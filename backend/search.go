package alfred

import "context"

// SearchProviderSettings configures one canonical search backend independently of model providers.
type SearchProviderSettings struct {
	Backend       string
	Enabled       bool
	Settings      map[string]string
	CredentialRef CredentialRef
}

// SearchSettingsStore persists search-specific metadata, excluding secret values.
type SearchSettingsStore interface {
	GetSearchSettings(context.Context, string) (SearchProviderSettings, error)
	PutSearchSettings(context.Context, SearchProviderSettings) error
}

// SearchBackendDefinition declares a search backend's setup inputs.
type SearchBackendDefinition struct {
	ID     string
	Name   string
	Fields []SetupField
}

// SearchSetupForm contains only non-secret saved settings.
type SearchSetupForm struct {
	Definition SearchBackendDefinition
	Values     map[string]string
}
