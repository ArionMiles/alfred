package alfred

// ProviderType identifies the implementation used by a configured model provider.
// Several instances may use the same implementation with different settings.
type ProviderType string

// ProviderInstance describes a configured account or endpoint, not an inference
// client. Runtime adapters are resolved separately through ProviderResolver.
type ProviderInstance struct {
	ID            ProviderID
	Type          ProviderType
	Name          string
	Enabled       bool
	CredentialRef CredentialRef
	// Settings holds non-secret, provider-specific string options. Provider
	// adapters validate supported keys and values before activation.
	Settings map[string]string
}
