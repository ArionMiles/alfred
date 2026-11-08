package providers

import "github.com/ArionMiles/alfred"

// SetupDefinitions returns independent metadata for planned built-in adapters.
// Definitions describe forms only; they do not advertise runtime availability.
// OAuth is added when a concrete authorization flow is implemented.
func SetupDefinitions() []alfred.ProviderDefinition {
	return []alfred.ProviderDefinition{
		{Type: "openai", Name: "OpenAI", Methods: []alfred.ProviderSetupMethod{apiKeyMethod()}},
		{Type: "deepseek", Name: "DeepSeek", Methods: []alfred.ProviderSetupMethod{apiKeyMethod()}},
		{Type: "fireworks", Name: "Fireworks AI", Methods: []alfred.ProviderSetupMethod{apiKeyMethod()}},
		{
			Type: "aws-bedrock", Name: "Amazon Bedrock",
			Methods: []alfred.ProviderSetupMethod{{
				ID: "profile", Name: "AWS profile and region",
				Fields: []alfred.SetupField{
					{
						ID: "profile", Label: "AWS profile", Kind: alfred.SetupFieldText,
						Description: "Optional named profile; otherwise use the AWS credential chain.",
					},
					{ID: "region", Label: "AWS region", Kind: alfred.SetupFieldText, Required: true},
					{
						ID: "endpoint", Label: "Custom endpoint", Kind: alfred.SetupFieldText,
						Description: "Optional private or VPC endpoint.",
					},
				},
			}},
		},
	}
}

func apiKeyMethod() alfred.ProviderSetupMethod {
	return alfred.ProviderSetupMethod{
		ID: "api-key", Name: "API key",
		Fields: []alfred.SetupField{
			{ID: "api_key", Label: "API key", Kind: alfred.SetupFieldSecret, Required: true},
			{
				ID: "base_url", Label: "Base URL", Kind: alfred.SetupFieldText,
				Description: "Optional override of the provider's default endpoint.",
			},
		},
	}
}
