package alfred

import "context"

// ProjectSkillTrustState describes project skill roots and whether Alfred may
// load skills from them. Roots contains only skill roots that currently exist.
type ProjectSkillTrustState struct {
	ProjectID ProjectID
	Trust     ProjectSkillTrust
	Roots     []string
}

// ProjectSkillInspector reports whether a project has skill roots that require
// a trust decision. It does not parse or load skills from untrusted roots.
type ProjectSkillInspector interface {
	InspectProjectSkills(context.Context, ProjectID) (ProjectSkillTrustState, error)
}

// SkillDiagnosticKind classifies a skill discovery diagnostic.
type SkillDiagnosticKind string

const (
	// SkillDiagnosticInvalid indicates that a discovered skill could not be used.
	SkillDiagnosticInvalid SkillDiagnosticKind = "invalid"
	// SkillDiagnosticShadowed indicates that a higher-precedence skill has the
	// same name.
	SkillDiagnosticShadowed SkillDiagnosticKind = "shadowed"
)

// SkillDiagnostic describes a non-fatal issue encountered during discovery.
type SkillDiagnostic struct {
	Kind     SkillDiagnosticKind
	Location string
	Message  string
}

// SkillDefinition describes a skill for discovery and selection.
type SkillDefinition struct {
	Name          SkillName
	Description   string
	Location      string
	License       string
	Compatibility string
	Metadata      map[string]string
	AllowedTools  string
}

// SkillCatalog contains the effective skills and non-fatal discovery issues
// for a project.
type SkillCatalog struct {
	Skills      []SkillDefinition
	Diagnostics []SkillDiagnostic
}

// SkillContent is the material loaded when a skill is activated.
type SkillContent struct {
	Instructions string
	Resources    []ResourceRef
}

// Skill is one installable skill implementation.
type Skill interface {
	Definition() SkillDefinition
	Load(context.Context) (SkillContent, error)
}

// SkillSet is the effective skill snapshot used for one run.
type SkillSet interface {
	Catalog() SkillCatalog
	Resolve(SkillName) (Skill, error)
}

// SkillResolver discovers the skills available to a run within a project.
type SkillResolver interface {
	DiscoverSkills(context.Context, ProjectID) (SkillSet, error)
}
