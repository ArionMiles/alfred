package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/providers/openai"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

type providerInstanceReader interface {
	GetProviderInstance(context.Context, alfred.ProviderID) (alfred.ProviderInstance, error)
}

type configuredProviders struct {
	instances       providerInstanceReader
	credentials     alfred.CredentialReader
	catalogConfig   CatalogConfig
	catalogClient   *http.Client
	inferenceClient *http.Client
}

// WithConfiguredProviders enables runtime resolution from saved settings.
// Catalog and inference clients are separate so a catalog timeout does not
// impose a total time limit on a streamed model response.
func WithConfiguredProviders(
	instances providerInstanceReader,
	credentials alfred.CredentialReader,
	catalogConfig CatalogConfig,
	catalogClient *http.Client,
	inferenceClient *http.Client,
) Option {
	return func(registry *Registry) {
		registry.configured = &configuredProviders{
			instances: instances, credentials: credentials, catalogConfig: catalogConfig,
			catalogClient: catalogClient, inferenceClient: inferenceClient,
		}
	}
}

func (configured *configuredProviders) resolve(ctx context.Context, id alfred.ProviderID) (alfred.ModelProvider, error) {
	const op = "resolve"

	instance, err := configured.instances.GetProviderInstance(ctx, id)
	if err != nil {
		return nil, errors.B.Op(op).Retryable(true).UserMsg("The provider could not be loaded. Configure it in Providers.").Err(err).Build()
	}
	if !instance.Enabled {
		return nil, errors.B.Op(op).KindFailedPrecondition().Retryable(true).UserMsg("This provider is disabled.").Build()
	}
	if instance.Type != "openai" {
		return nil, errors.B.Op(op).KindUnimplemented().UserMsg("This provider's inference adapter is not available yet.").Build()
	}
	config, err := configured.openAIConfig(ctx, instance)
	if err != nil {
		return nil, err
	}
	catalog, err := LoadCatalog(ctx, configured.catalogConfig, configured.catalogClient)
	if err != nil {
		return nil, errors.B.Op(op).Text("load provider model metadata").Err(err).Build()
	}
	return openai.New(instance.ID, config, catalog.Models(instance.Type), configured.inferenceClient), nil
}

func (configured *configuredProviders) openAIConfig(ctx context.Context, instance alfred.ProviderInstance) (openai.Config, error) {
	const op = "openAIConfig"

	config := openai.DefaultConfig()
	for key := range instance.Settings {
		if key != "base_url" {
			return openai.Config{}, errors.B.Op(op).KindFailedPrecondition().Retryable(true).
				UserMsg("OpenAI settings are unsupported. Reconfigure the provider.").Build()
		}
	}
	if endpoint := strings.TrimSpace(instance.Settings["base_url"]); endpoint != "" {
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
			return openai.Config{}, errors.B.Op(op).KindFailedPrecondition().Retryable(true).UserMsg("OpenAI's Base URL must be an HTTP or HTTPS URL.").Build()
		}
		config.BaseURL = endpoint
	}
	if instance.CredentialRef == "" {
		return openai.Config{}, errors.B.Op(op).KindFailedPrecondition().Retryable(true).UserMsg("Configure this provider's API key in Providers.").Build()
	}
	data, err := configured.credentials.GetCredential(ctx, instance.CredentialRef)
	if err != nil {
		return openai.Config{}, errors.B.Op(op).Retryable(true).UserMsg("The provider credential could not be loaded. Reconfigure its API key.").Err(err).Build()
	}
	var credential setupCredentialRecord
	// Do not wrap decoder errors or echo the stored record: it contains secrets.
	if err := json.Unmarshal(data, &credential); err != nil || credential.Version != 1 ||
		credential.Provider != instance.Type || credential.Method != "api-key" || strings.TrimSpace(credential.Fields["api_key"]) == "" {
		return openai.Config{}, errors.B.Op(op).KindFailedPrecondition().Retryable(true).
			UserMsg("The saved provider credential is unsupported. Reconfigure its API key.").Build()
	}
	config.APIKey = credential.Fields["api_key"]
	return config, nil
}
