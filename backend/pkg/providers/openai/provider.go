// Package openai implements Alfred's native OpenAI Responses adapter.
package openai

import (
	"context"
	"encoding/json"
	"io"
	"iter"
	"net/http"
	"slices"
	"sort"
	"strings"

	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

// nativeKey namespaces complete native items retained for Responses replay.
func nativeKey(kind string) string { return "openai.responses." + kind + ".v1" }

// Config contains constructor settings, not a public file configuration schema.
type Config struct {
	APIKey  string
	BaseURL string `default:"https://api.openai.com/v1"`
}

// Provider translates Alfred model requests to the Responses API.
type Provider struct {
	id           alfred.ProviderID
	models       map[alfred.ModelID]alfred.ModelInfo
	service      responses.ResponseService
	subscription *subscriptionRuntime
}

type subscriptionRuntime struct {
	credentials *SubscriptionCredentials
	ref         alfred.CredentialRef
	client      *http.Client
	baseURL     string
}

// SubscriptionDependencies are runtime collaborators for ChatGPT-plan access.
type SubscriptionDependencies struct {
	Credentials   *SubscriptionCredentials
	CredentialRef alfred.CredentialRef
	HTTPClient    *http.Client
}

// New constructs an adapter using caller-supplied model metadata and transport.
// The service constructor avoids the SDK client's environment-derived defaults.
func New(id alfred.ProviderID, config Config, models []alfred.ModelInfo, client *http.Client) *Provider {
	catalog := make(map[alfred.ModelID]alfred.ModelInfo, len(models))
	for _, model := range models {
		model.Ref.ProviderID = id
		catalog[model.Ref.ModelID] = cloneModel(model)
	}
	return &Provider{
		id: id, models: catalog,
		service: responses.NewResponseService(
			option.WithAPIKey(config.APIKey), option.WithBaseURL(config.BaseURL),
			option.WithHTTPClient(client), option.WithMaxRetries(0),
		),
	}
}

// NewSubscription constructs an adapter backed by a renewable ChatGPT-plan token.
func NewSubscription(
	id alfred.ProviderID,
	config Config,
	models []alfred.ModelInfo,
	dependencies SubscriptionDependencies,
) *Provider {
	provider := New(id, Config{BaseURL: config.BaseURL}, models, dependencies.HTTPClient)
	provider.subscription = &subscriptionRuntime{
		credentials: dependencies.Credentials, ref: dependencies.CredentialRef,
		client: dependencies.HTTPClient, baseURL: strings.TrimRight(config.BaseURL, "/"),
	}
	return provider
}

// ID returns the configured provider identity.
func (provider *Provider) ID() alfred.ProviderID { return provider.id }

// Models returns caller-supplied model metadata in model-ID order.
func (provider *Provider) Models(ctx context.Context) ([]alfred.ModelInfo, error) {
	const op = "Models"

	if err := ctx.Err(); err != nil {
		return nil, errors.B.Op(op).Err(err).Build()
	}
	if provider.subscription != nil {
		return provider.subscriptionModels(ctx)
	}
	models := make([]alfred.ModelInfo, 0, len(provider.models))
	for _, model := range provider.models {
		models = append(models, cloneModel(model))
	}
	sort.Slice(models, func(left, right int) bool { return models[left].Ref.ModelID < models[right].Ref.ModelID })
	return models, nil
}

// ResolveModel resolves metadata without inventing capabilities or fetching a catalog.
func (provider *Provider) ResolveModel(ctx context.Context, id alfred.ModelID) (alfred.ModelInfo, error) {
	const op = "ResolveModel"

	if err := ctx.Err(); err != nil {
		return alfred.ModelInfo{}, errors.B.Op(op).Err(err).Build()
	}
	models := provider.models
	if provider.subscription != nil {
		listed, err := provider.subscriptionModels(ctx)
		if err != nil {
			return alfred.ModelInfo{}, errors.B.Op(op).Err(err).Build()
		}
		models = make(map[alfred.ModelID]alfred.ModelInfo, len(listed))
		for _, model := range listed {
			models[model.Ref.ModelID] = model
		}
	}
	model, ok := models[id]
	if !ok {
		return alfred.ModelInfo{}, errors.B.Op(op).KindNotFound().UserMsg("the requested OpenAI model is not available").Build()
	}
	return cloneModel(model), nil
}

// Stream sends one stateless request. Completed items come from the terminal
// snapshot in output order, after all streamed deltas have been consumed.
func (provider *Provider) Stream(ctx context.Context, request alfred.ModelRequest) iter.Seq2[alfred.ModelEvent, error] {
	return func(yield func(alfred.ModelEvent, error) bool) {
		const op = "Stream"

		prepared, err := provider.buildRequest(ctx, request)
		if err != nil {
			yield(alfred.ModelEvent{}, err)
			return
		}
		var requestOptions []option.RequestOption
		if provider.subscription != nil {
			token, err := provider.subscription.credentials.AccessToken(ctx, provider.subscription.ref)
			if err != nil {
				yield(alfred.ModelEvent{}, errors.B.Op(op).Err(err).Build())
				return
			}
			requestOptions = append(requestOptions, option.WithAPIKey(token))
		}
		stream := provider.service.NewStreaming(ctx, prepared.params, requestOptions...)
		defer func() { _ = stream.Close() }()
		indices := make(map[[2]int64]uint32)
		reasoningIndices := make(map[[3]int64]uint32)
		var nextIndex uint32
		var nextReasoningIndex uint32
		for stream.Next() {
			event := stream.Current()
			switch event.Type {
			case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
				if !yield(alfred.ModelEvent{CallID: request.ID, Payload: reasoningDelta(event, reasoningIndices, &nextReasoningIndex)}, nil) {
					return
				}
			case "response.output_text.delta":
				if !yield(alfred.ModelEvent{CallID: request.ID, Payload: textDelta(event, indices, &nextIndex)}, nil) {
					return
				}
			case "response.completed":
				yieldResponse(event.Response, request, prepared.tools, yield)
				return
			case "response.failed":
				yield(alfred.ModelEvent{}, responseError(string(event.Response.Error.Code)))
				return
			case "response.incomplete":
				yield(alfred.ModelEvent{}, errors.B.Op(op).KindBadGateway().UserMsg("OpenAI did not complete the response.").Build())
				return
			case "error":
				yield(alfred.ModelEvent{}, responseError(event.Code))
				return
			}
		}
		if err := stream.Err(); err != nil {
			yield(alfred.ModelEvent{}, requestError(ctx, err))
			return
		}
		yield(alfred.ModelEvent{}, errors.B.Op(op).KindBadGateway().Retryable(true).
			UserMsg("The OpenAI stream ended before the response completed.").Build())
	}
}

func (provider *Provider) subscriptionModels(ctx context.Context) ([]alfred.ModelInfo, error) {
	const op = "subscriptionModels"

	runtime := provider.subscription
	token, err := runtime.credentials.AccessToken(ctx, runtime.ref)
	if err != nil {
		return nil, errors.B.Op(op).Err(err).Build()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, runtime.baseURL+"/models", nil)
	if err != nil {
		return nil, errors.B.Op(op).Text("create OpenAI subscription model request").Err(err).Build()
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := runtime.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, errors.B.Op(op).Err(ctx.Err()).Build()
		}
		return nil, errors.B.Op(op).KindUnavailable().Retryable(true).
			UserMsg("OpenAI models could not be loaded. Try again.").Build()
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, errors.B.Op(op).Err(subscriptionHTTPError(response)).Build()
	}
	var catalog struct {
		Models []struct {
			Slug        string `json:"slug"`
			DisplayName string `json:"display_name"`
			Visibility  string `json:"visibility"`
		} `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&catalog); err != nil {
		return nil, errors.B.Op(op).KindBadGateway().UserMsg("OpenAI returned an invalid model catalog.").Build()
	}
	models := make([]alfred.ModelInfo, 0, len(catalog.Models))
	for _, listed := range catalog.Models {
		if listed.Visibility != "list" {
			continue
		}
		model, exists := provider.models[alfred.ModelID(listed.Slug)]
		if !exists {
			continue
		}
		if listed.DisplayName != "" {
			model.Name = listed.DisplayName
		}
		models = append(models, cloneModel(model))
	}
	return models, nil
}

func reasoningDelta(event responses.ResponseStreamEventUnion, indices map[[3]int64]uint32, nextIndex *uint32) alfred.ModelReasoningDelta {
	kind := alfred.ReasoningTextSummary
	key := [3]int64{event.OutputIndex, event.SummaryIndex, 0}
	if event.Type == "response.reasoning_text.delta" {
		kind = alfred.ReasoningTextReasoning
		key = [3]int64{event.OutputIndex, event.ContentIndex, 1}
	}
	index, ok := indices[key]
	if !ok {
		index = *nextIndex
		indices[key] = index
		*nextIndex++
	}
	return alfred.ModelReasoningDelta{Index: index, Text: event.Delta, TextKind: kind}
}

func textDelta(event responses.ResponseStreamEventUnion, indices map[[2]int64]uint32, nextIndex *uint32) alfred.ModelContentDelta {
	key := [2]int64{event.OutputIndex, event.ContentIndex}
	index, ok := indices[key]
	if !ok {
		index = *nextIndex
		indices[key] = index
		*nextIndex++
	}
	return alfred.ModelContentDelta{Index: index, Delta: alfred.TextContent{Text: event.Delta}}
}

func yieldResponse(response responses.Response, request alfred.ModelRequest, tools map[string]alfred.ToolID, yield func(alfred.ModelEvent, error) bool) {
	events, err := decodeResponse(response, tools, request.RunID)
	if err != nil {
		yield(alfred.ModelEvent{}, err)
		return
	}
	for _, event := range events {
		event.CallID = request.ID
		if !yield(event, nil) {
			return
		}
	}
}

func cloneModel(model alfred.ModelInfo) alfred.ModelInfo {
	model.Capabilities.InputModalities = slices.Clone(model.Capabilities.InputModalities)
	model.Capabilities.OutputModalities = slices.Clone(model.Capabilities.OutputModalities)
	return model
}

var _ alfred.ModelProvider = (*Provider)(nil)
