package alfred

// ProviderDefinition describes the setup methods offered by a model provider.
// Configured accounts retain their own ProviderID separately.
type ProviderDefinition struct {
	Type    ProviderType
	Name    string
	Methods []ProviderSetupMethod
}

// ProviderSetupMethod describes one form, such as API-key authentication or AWS
// profile settings. Its ID is stable and does not depend on its display order.
// Executing the method and validating connectivity belong to the service.
type ProviderSetupMethod struct {
	ID     string
	Name   string
	Fields []SetupField
}

// ProviderSetupForm combines a method definition with safe editable settings.
// Values contain only declared non-secret fields.
type ProviderSetupForm struct {
	Type       ProviderType
	InstanceID ProviderID
	Method     ProviderSetupMethod
	Values     map[string]string
}

// ProviderSetupSubmission contains form inputs for one configured instance.
// Values may contain secrets and must not be logged or added to session events.
type ProviderSetupSubmission struct {
	ID       ProviderID
	Type     ProviderType
	MethodID string
	Name     string
	Enabled  bool
	Values   map[string]string
}

// ProviderSetupResult reports saved metadata or field errors, never secret inputs.
// A saved instance is configured; remote authentication is a separate check.
type ProviderSetupResult struct {
	Instance    *ProviderInstance
	FieldErrors []SetupFieldError
}
