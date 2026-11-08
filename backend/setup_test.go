package alfred_test

import (
	"testing"

	"github.com/ArionMiles/alfred"
)

func TestValidateSetupFieldsAcceptsProviderDefinedInputs(t *testing.T) {
	t.Parallel()
	method := alfred.ProviderSetupMethod{
		ID: "api-key",
		Fields: []alfred.SetupField{
			{ID: "api_key", Kind: alfred.SetupFieldSecret, Required: true},
			{ID: "base_url", Kind: alfred.SetupFieldText},
		},
	}
	values := map[string]string{"api_key": "secret"}
	if problems := alfred.ValidateSetupFields(method.Fields, values); len(problems) != 0 {
		t.Fatalf("valid form problems = %+v", problems)
	}
	if values["api_key"] != "secret" {
		t.Fatal("validation mutated submitted credentials")
	}
}

func TestValidateSetupFieldsReportsErrorsWithoutEchoingValues(t *testing.T) {
	t.Parallel()
	method := alfred.ProviderSetupMethod{
		ID: "profile",
		Fields: []alfred.SetupField{
			{ID: "profile", Kind: alfred.SetupFieldText, Required: true},
			{ID: "region", Kind: alfred.SetupFieldSelect, Options: []alfred.SetupFieldOption{
				{Value: "us-east-1", Label: "US East"},
			}},
		},
	}
	problems := alfred.ValidateSetupFields(method.Fields, map[string]string{
		"profile": "  ", "region": "secret-wrong-field", "unexpected": "secret",
	})
	if len(problems) != 3 || problems[0].FieldID != "profile" || problems[1].FieldID != "region" ||
		problems[2].FieldID != "unexpected" {
		t.Fatalf("field errors = %+v", problems)
	}
	if problems[1].Message != "Choose one of the available options." {
		t.Fatalf("invalid choice was echoed in message: %q", problems[1].Message)
	}
}
