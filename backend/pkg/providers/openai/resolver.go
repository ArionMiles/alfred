package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/creasty/defaults"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

const apiKeyMethod = "api-key"

type providerInstanceReader interface {
	GetProviderInstance(context.Context, alfred.ProviderID) (alfred.ProviderInstance, error)
}

type modelCatalog interface {
	Models(context.Context, alfred.ProviderType) ([]alfred.ModelInfo, error)
}

// Resolver constructs OpenAI adapters from saved connections.
type Resolver struct {
	instances       providerInstanceReader
	credentials     alfred.CredentialReader
	subscription    *SubscriptionCredentials
	catalog         modelCatalog
	inferenceClient *http.Client
}

// NewResolver composes saved OpenAI connections with model metadata and transport.
// The catalog source owns its timeout independently of streamed inference.
func NewResolver(
	instances providerInstanceReader,
	credentials alfred.CredentialReader,
	subscription *SubscriptionCredentials,
	catalog modelCatalog,
	options ...HTTPOption,
) *Resolver {
	transportOptions := httpOptions{client: &http.Client{}}
	for _, option := range options {
		option(&transportOptions)
	}
	return &Resolver{
		instances: instances, credentials: credentials, subscription: subscription,
		catalog: catalog, inferenceClient: transportOptions.client,
	}
}

// ResolveProvider loads the current saved connection and selects its OpenAI authentication method.
func (resolver *Resolver) ResolveProvider(ctx context.Context, id alfred.ProviderID) (alfred.ModelProvider, error) {
	const op = "ResolveProvider"

	instance, err := resolver.instances.GetProviderInstance(ctx, id)
	if err != nil {
		return nil, errors.B.Op(op).Retryable(true).UserMsg("The provider could not be loaded. Configure it in Providers.").Err(err).Build()
	}
	if !instance.Enabled {
		return nil, errors.B.Op(op).KindFailedPrecondition().Retryable(true).UserMsg("This provider is disabled.").Build()
	}
	if instance.Type != openAIProviderType {
		return nil, errors.B.Op(op).KindUnimplemented().UserMsg("This provider's inference adapter is not available yet.").Build()
	}
	data, method, err := resolver.openAICredential(ctx, instance)
	if err != nil {
		return nil, err
	}
	switch method {
	case apiKeyMethod:
		config, err := resolver.openAPIKeyConfig(instance, data)
		if err != nil {
			return nil, err
		}
		models, err := resolver.models(ctx)
		if err != nil {
			return nil, errors.B.Op(op).Text("load provider model metadata").Err(err).Build()
		}
		return New(instance.ID, config, models, WithHTTPClient(resolver.inferenceClient)), nil
	case chatGPTSubscriptionMethod:
		if resolver.subscription == nil {
			return nil, errors.B.Op(op).KindFailedPrecondition().Retryable(true).
				UserMsg("This OpenAI connection cannot renew access. Reconnect it in Providers.").Build()
		}
		var config Config
		if err := defaults.Set(&config); err != nil {
			return nil, errors.B.Op(op).Text("initialize OpenAI configuration").Err(err).Build()
		}
		if len(instance.Settings) != 0 {
			return nil, errors.B.Op(op).KindFailedPrecondition().Retryable(true).
				UserMsg("OpenAI subscription settings are unsupported. Reconnect the provider.").Build()
		}
		models, err := resolver.models(ctx)
		if err != nil {
			return nil, errors.B.Op(op).Text("load provider model metadata").Err(err).Build()
		}
		return NewSubscription(
			instance.ID, config, models, SubscriptionDependencies{
				Credentials: resolver.subscription, CredentialRef: instance.CredentialRef,
			}, WithHTTPClient(resolver.inferenceClient),
		), nil
	default:
		return nil, errors.B.Op(op).KindFailedPrecondition().Retryable(true).
			UserMsg("The saved provider credential is unsupported. Reconfigure OpenAI.").Build()
	}
}

func (resolver *Resolver) openAICredential(
	ctx context.Context,
	instance alfred.ProviderInstance,
) ([]byte, string, error) {
	const op = "openAICredential"

	if instance.CredentialRef == "" {
		return nil, "", errors.B.Op(op).KindFailedPrecondition().Retryable(true).UserMsg("Configure this provider in Providers.").Build()
	}
	data, err := resolver.credentials.GetCredential(ctx, instance.CredentialRef)
	if err != nil {
		return nil, "", errors.B.Op(op).Retryable(true).UserMsg("The provider credential could not be loaded. Reconfigure it.").Err(err).Build()
	}
	var header struct {
		Version  uint32              `json:"version"`
		Provider alfred.ProviderType `json:"provider"`
		Method   string              `json:"method"`
	}
	if err := json.Unmarshal(data, &header); err != nil || header.Version != 1 || header.Provider != instance.Type || header.Method == "" {
		return nil, "", errors.B.Op(op).KindFailedPrecondition().Retryable(true).
			UserMsg("The saved provider credential is unsupported. Reconfigure OpenAI.").Build()
	}
	return data, header.Method, nil
}

func (resolver *Resolver) openAPIKeyConfig(instance alfred.ProviderInstance, data []byte) (Config, error) {
	const op = "openAPIKeyConfig"

	var config Config
	if err := defaults.Set(&config); err != nil {
		return Config{}, errors.B.Op(op).Text("initialize OpenAI configuration").Err(err).Build()
	}
	for key := range instance.Settings {
		if key != "base_url" {
			return Config{}, errors.B.Op(op).KindFailedPrecondition().Retryable(true).
				UserMsg("OpenAI settings are unsupported. Reconfigure the provider.").Build()
		}
	}
	if endpoint := strings.TrimSpace(instance.Settings["base_url"]); endpoint != "" {
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
			return Config{}, errors.B.Op(op).KindFailedPrecondition().Retryable(true).UserMsg("OpenAI's Base URL must be an HTTP or HTTPS URL.").Build()
		}
		config.BaseURL = endpoint
	}
	var credential struct {
		Version  uint32              `json:"version"`
		Provider alfred.ProviderType `json:"provider"`
		Method   string              `json:"method"`
		Fields   map[string]string   `json:"fields"`
	}
	// Do not wrap decoder errors or echo the stored record: it contains secrets.
	if err := json.Unmarshal(data, &credential); err != nil || credential.Version != 1 ||
		credential.Provider != instance.Type || credential.Method != apiKeyMethod || strings.TrimSpace(credential.Fields["api_key"]) == "" {
		return Config{}, errors.B.Op(op).KindFailedPrecondition().Retryable(true).
			UserMsg("The saved provider credential is unsupported. Reconfigure its API key.").Build()
	}
	config.APIKey = credential.Fields["api_key"]
	return config, nil
}

func (resolver *Resolver) models(ctx context.Context) ([]alfred.ModelInfo, error) {
	const op = "models"
	models, err := resolver.catalog.Models(ctx, openAIProviderType)
	if err != nil {
		return nil, errors.B.Op(op).Err(err).Build()
	}
	if len(models) == 0 {
		return nil, errors.B.Op(op).KindBadGateway().UserMsg("The model catalog has no OpenAI models.").Build()
	}
	return models, nil
}

var _ alfred.ProviderResolver = (*Resolver)(nil)
