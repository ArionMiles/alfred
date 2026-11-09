package providers

import (
	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/providers/openai"
)

const apiKeyMethodID = "api-key"

// SetupDefinitions returns independent metadata for planned built-in adapters.
// Definitions describe setup methods only; they do not advertise runtime availability.
func SetupDefinitions() []alfred.ProviderDefinition {
	return []alfred.ProviderDefinition{
		{Type: "openai", Name: "OpenAI", Methods: []alfred.ProviderSetupMethod{
			apiKeyMethod(),
			{
				ID: "chatgpt-subscription", Name: "Continue with ChatGPT",
				InstanceName: openai.SubscriptionInstanceName, Kind: alfred.ProviderSetupMethodAuthorization,
			},
		}},
		{Type: "deepseek", Name: "DeepSeek", Methods: []alfred.ProviderSetupMethod{apiKeyMethod()}},
		{Type: "fireworks", Name: "Fireworks AI", Methods: []alfred.ProviderSetupMethod{apiKeyMethod()}},
		{
			Type: "aws-bedrock", Name: "Amazon Bedrock",
			Methods: []alfred.ProviderSetupMethod{{
				ID: "profile", Name: "AWS profile and region", Kind: alfred.ProviderSetupMethodForm,
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
		ID: apiKeyMethodID, Name: "API key", Kind: alfred.ProviderSetupMethodForm,
		Fields: []alfred.SetupField{
			{ID: "api_key", Label: "API key", Kind: alfred.SetupFieldSecret, Required: true},
			{
				ID: "base_url", Label: "Base URL", Kind: alfred.SetupFieldText,
				Description: "Optional override of the provider's default endpoint.",
			},
		},
	}
}
