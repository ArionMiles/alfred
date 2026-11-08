package alfred

import (
	"slices"
	"sort"
	"strings"
)

// SetupFieldKind tells clients how to render a setup field.
type SetupFieldKind string

const (
	// SetupFieldText requests a text value.
	SetupFieldText SetupFieldKind = "text"
	// SetupFieldSecret requests masked input stored through CredentialProvider.
	SetupFieldSecret SetupFieldKind = "secret"
	// SetupFieldSelect requests one of the declared option values.
	SetupFieldSelect SetupFieldKind = "select"
)

// SetupField describes an input rather than its current value. Stored
// secrets must never be included in form definitions or prefilled client values.
// Non-secret field IDs identify provider-specific settings; secret fields are
// passed separately to the credential-handling part of onboarding.
type SetupField struct {
	ID          string
	Label       string
	Description string
	Kind        SetupFieldKind
	Required    bool
	Options     []SetupFieldOption
}

// SetupFieldOption separates a select option's label from its submitted value.
type SetupFieldOption struct {
	Value string
	Label string
}

// SetupFieldError identifies a field whose submission needs correction.
// Message is safe for clients and must not contain the submitted secret value.
type SetupFieldError struct {
	FieldID string
	Message string
}

// ValidateSetupFields validates trusted field definitions against submitted values.
func ValidateSetupFields(
	fields []SetupField,
	values map[string]string,
) []SetupFieldError {
	var problems []SetupFieldError
	known := make(map[string]bool, len(fields))
	for _, field := range fields {
		known[field.ID] = true
		value := values[field.ID]
		switch {
		case field.Kind != SetupFieldText && field.Kind != SetupFieldSecret &&
			field.Kind != SetupFieldSelect:
			problems = append(problems, SetupFieldError{
				FieldID: field.ID, Message: "This field type is not supported.",
			})
		case strings.TrimSpace(value) == "":
			if field.Required {
				problems = append(problems, SetupFieldError{
					FieldID: field.ID, Message: "This field is required.",
				})
			}
		case field.Kind == SetupFieldSelect && !slices.ContainsFunc(
			field.Options,
			func(option SetupFieldOption) bool { return option.Value == value },
		):
			problems = append(problems, SetupFieldError{
				FieldID: field.ID, Message: "Choose one of the available options.",
			})
		}
	}
	unknown := make([]string, 0)
	for id := range values {
		if !known[id] {
			unknown = append(unknown, id)
		}
	}
	sort.Strings(unknown)
	for _, id := range unknown {
		problems = append(problems, SetupFieldError{
			FieldID: id, Message: "This field is not part of the setup form.",
		})
	}
	return problems
}
