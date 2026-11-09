package alfred

// ProviderDefinition describes the setup methods offered by a model provider.
// Configured accounts retain their own ProviderID separately.
type ProviderDefinition struct {
	Type    ProviderType
	Name    string
	Methods []ProviderSetupMethod
}

// ProviderSetupMethodKind identifies how a setup method is completed.
type ProviderSetupMethodKind string

const (
	ProviderSetupMethodForm          ProviderSetupMethodKind = "form"
	ProviderSetupMethodAuthorization ProviderSetupMethodKind = "authorization"
)

// ProviderSetupMethod describes one form or interactive authorization flow.
// Its ID is stable and does not depend on its display order. InstanceName is
// server-owned metadata for the saved connection and is not a client input.
// Executing the method and validating connectivity belong to the service.
type ProviderSetupMethod struct {
	ID           string
	Name         string
	InstanceName string
	Kind         ProviderSetupMethodKind
	Fields       []SetupField
}

type ProviderAuthorizationID string

type ProviderAuthorizationState string

const (
	ProviderAuthorizationPending   ProviderAuthorizationState = "pending"
	ProviderAuthorizationCompleted ProviderAuthorizationState = "completed"
	ProviderAuthorizationDenied    ProviderAuthorizationState = "denied"
	ProviderAuthorizationFailed    ProviderAuthorizationState = "failed"
	ProviderAuthorizationCanceled  ProviderAuthorizationState = "canceled"
)

// ProviderAuthorizationRequest selects an interactive provider setup method.
type ProviderAuthorizationRequest struct {
	Type     ProviderType
	MethodID string
}

// ProviderAuthorization reports safe progress for an interactive setup flow.
type ProviderAuthorization struct {
	ID               ProviderAuthorizationID
	State            ProviderAuthorizationState
	AuthorizationURL string
	AccountLabel     string
	Message          string
	Instance         *ProviderInstance
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
	Type     ProviderType
	MethodID string
	Values   map[string]string
}

// ProviderSetupResult reports saved metadata or field errors, never secret inputs.
// A saved instance is configured; remote authentication is a separate check.
type ProviderSetupResult struct {
	Instance    *ProviderInstance
	FieldErrors []SetupFieldError
}
