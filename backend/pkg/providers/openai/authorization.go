package openai

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"golang.org/x/oauth2"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/identity"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

const (
	chatGPTSubscriptionMethod = "chatgpt-subscription"
	openAIProviderType        = "openai"
	agentHostIDKey            = "providers.openai.agent_host_id"
	dynamicAgentClientID      = "dynamic_agent_client"
	requiredDirectScope       = "chatgpt.tokens.use.direct"
)

// SubscriptionInstanceName is the display identity of an OpenAI connection
// backed by ChatGPT-plan authorization.
const SubscriptionInstanceName = "OpenAI (ChatGPT plan)"

var authorizationScopes = []string{
	"openid",
	"profile",
	"email",
	"offline_access",
	"resource.invoke",
	requiredDirectScope,
}

// AuthorizationConfig contains OpenAI onboarding endpoints and lifetime settings.
type AuthorizationConfig struct {
	Issuer         string        `koanf:"-" default:"https://auth.openai.com"`
	AuthorizeURL   string        `koanf:"-" default:"https://auth.openai.com/api/accounts/authorize"`
	TokenURL       string        `koanf:"-" default:"https://auth.openai.com/api/accounts/oauth/token"`
	Resource       string        `koanf:"-" default:"https://api.openai.com/v1"`
	CallbackPath   string        `koanf:"-" default:"/auth/callback"`
	RequestTimeout time.Duration `koanf:"-" default:"30s"`
	AttemptTimeout time.Duration `koanf:"attempt_timeout" default:"10m"`
}

type authorizationStore interface {
	GetKeyValue(context.Context, string) (json.RawMessage, error)
	PutKeyValue(context.Context, string, json.RawMessage) error
	GetProviderInstance(context.Context, alfred.ProviderID) (alfred.ProviderInstance, error)
	PutProviderInstance(context.Context, alfred.ProviderInstance) error
}

// AuthorizationDependencies are runtime collaborators for browser authorization.
type AuthorizationDependencies struct {
	Store       authorizationStore
	Credentials alfred.CredentialProvider
}

// Authorizer owns pending local callbacks and the resulting credentials.
type Authorizer struct {
	config      AuthorizationConfig
	store       authorizationStore
	credentials alfred.CredentialProvider
	client      *http.Client
	agentName   string
	successHTML string
}

type authorizationAttempt struct {
	result      alfred.ProviderAuthorization
	state       string
	nonce       string
	verifier    string
	redirectURL string
	existing    *oauthCredentialRecord
}

type agentHostIDRecord struct {
	Version uint32 `json:"version"`
	ID      string `json:"id"`
}

type oauthCredentialRecord struct {
	Version           uint32              `json:"version"`
	Provider          alfred.ProviderType `json:"provider"`
	Method            string              `json:"method"`
	Issuer            string              `json:"issuer"`
	Subject           string              `json:"subject"`
	Email             string              `json:"email,omitempty"`
	ClientID          string              `json:"client_id"`
	IDToken           string              `json:"id_token"`
	AccessToken       string              `json:"access_token"`
	RefreshToken      string              `json:"refresh_token"`
	TokenType         string              `json:"token_type"`
	Scopes            []string            `json:"scopes"`
	SavedAt           int64               `json:"saved_at_unix"`
	ExpiresAt         int64               `json:"expires_at_unix"`
	EarliestRefreshAt int64               `json:"earliest_refresh_at_unix,omitempty"`
}

type tokenResponse struct {
	AccessToken       string `json:"access_token"`
	RefreshToken      string `json:"refresh_token"`
	IDToken           string `json:"id_token"`
	TokenType         string `json:"token_type"`
	Scope             string `json:"scope"`
	ExpiresIn         int64  `json:"expires_in"`
	EarliestRefreshAt int64  `json:"earliest_refresh_at"`
}

// NewAuthorizer constructs OpenAI onboarding without starting background work.
func NewAuthorizer(config AuthorizationConfig, dependencies AuthorizationDependencies, options ...AuthorizationOption) *Authorizer {
	service := &Authorizer{
		config: config, store: dependencies.Store, credentials: dependencies.Credentials,
		client: &http.Client{Timeout: config.RequestTimeout}, agentName: "Alfred",
	}
	for _, option := range options {
		option(service)
	}
	return service
}

// Authorize owns one bounded attempt and returns after all callback handlers stop.
func (service *Authorizer) Authorize(
	ctx context.Context,
	request alfred.ProviderAuthorizationRequest,
	publish func(string),
) (alfred.ProviderAuthorization, error) {
	const op = "Authorize"
	ctx, cancel := context.WithTimeout(ctx, service.config.AttemptTimeout)
	defer cancel()
	if request.Type != openAIProviderType || request.MethodID != chatGPTSubscriptionMethod {
		return alfred.ProviderAuthorization{}, errors.B.Op(op).KindInvalidArgument().
			Text("unsupported provider authorization method").UserMsg("This OpenAI authorization method is unavailable.").Build()
	}
	instance, existing, err := service.loadExisting(ctx, request)
	if err != nil {
		return alfred.ProviderAuthorization{}, errors.B.Op(op).Err(err).Build()
	}
	hostID, err := service.agentHostID(ctx)
	if err != nil {
		return alfred.ProviderAuthorization{}, errors.B.Op(op).Text("load OpenAI agent host ID").Err(err).Build()
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return alfred.ProviderAuthorization{}, errors.B.Op(op).Text("listen for authorization callback").Err(err).Build()
	}
	defer func() { _ = listener.Close() }()
	attempt := &authorizationAttempt{
		state: rand.Text(), nonce: rand.Text(), verifier: oauth2.GenerateVerifier(),
		redirectURL: "http://" + listener.Addr().String() + service.config.CallbackPath, existing: existing,
		result: alfred.ProviderAuthorization{State: alfred.ProviderAuthorizationPending},
	}
	clientID := dynamicAgentClientID
	if existing != nil {
		clientID = existing.ClientID
	}
	authorizationURL, err := service.authorizationURL(authorizationURLParams{
		clientID: clientID, redirectURL: attempt.redirectURL,
		state: attempt.state, nonce: attempt.nonce, verifier: attempt.verifier, hostID: hostID, existing: existing,
	})
	if err != nil {
		return alfred.ProviderAuthorization{}, errors.B.Op(op).Err(err).Build()
	}
	completed := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc(service.config.CallbackPath, service.callbackHandler(ctx, attempt, instance, completed))
	server := &http.Server{
		Handler: mux, ReadHeaderTimeout: 5 * time.Second,
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	publish(authorizationURL)
	var serveErr error
	select {
	case <-completed:
	case <-ctx.Done():
	case serveErr = <-served:
	}
	// Cancel outstanding requests, then wait even if a collaborator is slow to
	// honor cancellation. The setup service bounds shutdown waiting without
	// closing storage underneath a callback that is still writing.
	stopCause := ctx.Err()
	cancel()
	if err := server.Shutdown(context.Background()); err != nil {
		return alfred.ProviderAuthorization{}, errors.B.Op(op).Text("stop authorization callbacks").Err(err).Build()
	}
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) && attempt.result.State == alfred.ProviderAuthorizationPending {
		attempt.result = alfred.ProviderAuthorization{State: alfred.ProviderAuthorizationFailed, Message: "The local authorization callback stopped unexpectedly."}
	}
	if attempt.result.State == alfred.ProviderAuthorizationPending ||
		(stopCause != nil && attempt.result.State != alfred.ProviderAuthorizationCompleted) {
		attempt.result.State = alfred.ProviderAuthorizationCanceled
		attempt.result.Message = "Authorization was canceled."
	}
	if errors.Is(stopCause, context.DeadlineExceeded) && attempt.result.State != alfred.ProviderAuthorizationCompleted {
		attempt.result.State = alfred.ProviderAuthorizationFailed
		attempt.result.Message = "Authorization expired. Start again to reconnect OpenAI."
	}
	return attempt.result, nil
}

// callbackHandler admits one state-validated callback while allowing harmless
// invalid requests to leave the attempt available for the real browser redirect.
func (service *Authorizer) callbackHandler(
	ctx context.Context,
	attempt *authorizationAttempt,
	instance alfred.ProviderInstance,
	completed chan<- struct{},
) http.HandlerFunc {
	claimed := make(chan struct{}, 1)
	return func(writer http.ResponseWriter, callback *http.Request) {
		if callback.Method != http.MethodGet {
			http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if callback.URL.Query().Get("state") != attempt.state {
			http.Error(writer, "invalid authorization state", http.StatusBadRequest)
			return
		}
		select {
		case claimed <- struct{}{}:
		default:
			http.Error(writer, "invalid authorization state", http.StatusBadRequest)
			return
		}
		defer close(completed)
		service.handleCallback(ctx, attempt, instance, writer, callback)
	}
}

func (service *Authorizer) loadExisting(
	ctx context.Context,
	request alfred.ProviderAuthorizationRequest,
) (alfred.ProviderInstance, *oauthCredentialRecord, error) {
	const op = "loadExisting"

	instanceID := alfred.ProviderID(request.Type)
	instance, err := service.store.GetProviderInstance(ctx, instanceID)
	if errors.WhatKind(err) == errors.NotFound {
		return alfred.ProviderInstance{ID: instanceID, Type: request.Type, Enabled: true}, nil, nil
	}
	if err != nil {
		return alfred.ProviderInstance{}, nil, errors.B.Op(op).Text("load provider instance").Err(err).Build()
	}
	if instance.Type != request.Type {
		return alfred.ProviderInstance{}, nil, errors.B.Op(op).KindInvalidArgument().Text("provider type does not match the instance").Build()
	}
	if instance.CredentialRef == "" {
		return instance, nil, nil
	}
	data, err := service.credentials.GetCredential(ctx, instance.CredentialRef)
	if err != nil {
		return alfred.ProviderInstance{}, nil, errors.B.Op(op).Text("read provider credential").Err(err).Build()
	}
	var header struct {
		Version  uint32              `json:"version"`
		Provider alfred.ProviderType `json:"provider"`
		Method   string              `json:"method"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return alfred.ProviderInstance{}, nil, errors.B.Op(op).KindFailedPrecondition().Text("decode existing provider credential").Err(err).Build()
	}
	if header.Method != chatGPTSubscriptionMethod {
		return instance, nil, nil
	}
	var record oauthCredentialRecord
	if err := json.Unmarshal(data, &record); err != nil || record.Version != 1 ||
		record.Provider != openAIProviderType || record.ClientID == "" || record.Subject == "" {
		return alfred.ProviderInstance{}, nil, errors.B.Op(op).KindFailedPrecondition().Text("existing OpenAI authorization credential is invalid").Err(err).Build()
	}
	return instance, &record, nil
}

type authorizationURLParams struct {
	clientID, redirectURL, state, nonce, verifier, hostID string
	existing                                              *oauthCredentialRecord
}

func (service *Authorizer) authorizationURL(params authorizationURLParams) (string, error) {
	const op = "authorizationURL"

	endpoint, err := url.Parse(service.config.AuthorizeURL)
	if err != nil {
		return "", errors.B.Op(op).Text("parse authorization endpoint").Err(err).Build()
	}
	query := endpoint.Query()
	query.Set("client_id", params.clientID)
	query.Set("response_type", "code")
	query.Set("redirect_uri", params.redirectURL)
	query.Set("scope", strings.Join(authorizationScopes, " "))
	query.Set("resource", service.config.Resource)
	query.Set("state", params.state)
	query.Set("nonce", params.nonce)
	query.Set("code_challenge", oauth2.S256ChallengeFromVerifier(params.verifier))
	query.Set("code_challenge_method", "S256")
	query.Set("ext_agent_host_id", params.hostID)
	if params.existing == nil {
		query.Set("agent_name_hint", service.agentName)
	} else {
		if !params.existing.directAccessEnabled() {
			query.Set("prompt", "consent")
		}
		if params.existing.IDToken != "" {
			query.Set("id_token_hint", params.existing.IDToken)
		}
		if params.existing.Email != "" {
			query.Set("login_hint", params.existing.Email)
		}
	}
	endpoint.RawQuery = query.Encode()
	return endpoint.String(), nil
}

func (service *Authorizer) handleCallback(
	ctx context.Context,
	attempt *authorizationAttempt,
	instance alfred.ProviderInstance,
	writer http.ResponseWriter,
	request *http.Request,
) {
	query := request.URL.Query()
	switch query.Get("error") {
	case "":
	case "access_denied":
		message := "OpenAI authorization was denied."
		service.finish(attempt, alfred.ProviderAuthorizationDenied, message, "", nil)
		service.authorizationPage(writer, false, message)
		return
	default:
		message := "OpenAI authorization failed."
		service.fail(attempt, message)
		service.authorizationPage(writer, false, message)
		return
	}
	code := query.Get("code")
	if code == "" {
		service.fail(attempt, "OpenAI returned an incomplete authorization response.")
		http.Error(writer, "missing authorization code", http.StatusBadRequest)
		return
	}
	clientID := query.Get("client_id")
	if attempt.existing != nil {
		if clientID != "" && clientID != attempt.existing.ClientID {
			service.fail(attempt, "OpenAI returned an unexpected client identity.")
			http.Error(writer, "unexpected client identity", http.StatusBadRequest)
			return
		}
		clientID = attempt.existing.ClientID
	} else if clientID == "" {
		service.fail(attempt, "OpenAI did not issue a client identity.")
		http.Error(writer, "missing client identity", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(ctx, service.config.RequestTimeout)
	defer cancel()
	tokens, err := service.exchange(ctx, code, clientID, attempt)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			service.finish(attempt, alfred.ProviderAuthorizationCanceled, "Authorization was canceled.", "", nil)
			http.Error(writer, "authorization was canceled", http.StatusConflict)
			return
		}
		service.fail(attempt, "OpenAI token exchange failed.")
		http.Error(writer, "token exchange failed", http.StatusBadGateway)
		return
	}
	record, accountLabel, err := service.validateTokens(ctx, clientID, attempt, tokens)
	if err != nil {
		message := "OpenAI returned an invalid authorization result. Reconnect and try again."
		if errors.WhatKind(err) == errors.Conflict {
			message = "The selected OpenAI account does not match this connection. Reconnect using the original account."
		}
		service.fail(attempt, message)
		http.Error(writer, "invalid authorization result", http.StatusBadGateway)
		return
	}
	instance.Name = SubscriptionInstanceName
	instance.Settings = map[string]string{}
	if ctx.Err() != nil {
		service.finish(attempt, alfred.ProviderAuthorizationCanceled, "Authorization was canceled.", "", nil)
		http.Error(writer, "authorization was canceled", http.StatusConflict)
		return
	}
	if err := service.save(ctx, &instance, record); err != nil {
		service.fail(attempt, "Alfred could not save the OpenAI connection.")
		http.Error(writer, "could not save connection", http.StatusInternalServerError)
		return
	}
	message := "OpenAI account connected."
	if !record.directAccessEnabled() {
		message = "OpenAI account connected, but ChatGPT plan usage was not enabled. Reconnect it to use models."
	}
	service.finish(attempt, alfred.ProviderAuthorizationCompleted, message, accountLabel, &instance)
	if service.successHTML != "" && record.directAccessEnabled() {
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		writer.Header().Set("Cache-Control", "no-store")
		_, _ = io.WriteString(writer, service.successHTML)
		return
	}
	message = "Your OpenAI account is connected. Return to " + service.agentName + " to continue."
	if !record.directAccessEnabled() {
		message = "OpenAI account connected, but ChatGPT plan usage was not enabled. Return to " + service.agentName + " and reconnect to enable it."
	}
	service.authorizationPage(writer, true, message)
}

func (service *Authorizer) exchange(
	ctx context.Context,
	code, clientID string,
	attempt *authorizationAttempt,
) (tokenResponse, error) {
	const op = "exchange"

	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {clientID},
		"redirect_uri":  {attempt.redirectURL},
		"code_verifier": {attempt.verifier},
		"resource":      {service.config.Resource},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, service.config.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return tokenResponse{}, errors.B.Op(op).Text("create token request").Err(err).Build()
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := service.client.Do(request)
	if err != nil {
		return tokenResponse{}, errors.B.Op(op).Text("request tokens").Err(err).Build()
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return tokenResponse{}, errors.B.Op(op).Textf("token endpoint returned HTTP %d", response.StatusCode).Build()
	}
	var tokens tokenResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&tokens); err != nil {
		return tokenResponse{}, errors.B.Op(op).Text("decode token response").Err(err).Build()
	}
	if tokens.AccessToken == "" || tokens.RefreshToken == "" || tokens.IDToken == "" || tokens.ExpiresIn <= 0 {
		return tokenResponse{}, errors.B.Op(op).KindInvalidInput().Text("token response is incomplete").Build()
	}
	return tokens, nil
}

func (service *Authorizer) validateTokens(
	ctx context.Context,
	clientID string,
	attempt *authorizationAttempt,
	tokens tokenResponse,
) (oauthCredentialRecord, string, error) {
	const op = "validateTokens"

	scopes := strings.Fields(tokens.Scope)
	discoveryContext := oidc.ClientContext(ctx, service.client)
	provider, err := oidc.NewProvider(discoveryContext, service.config.Issuer)
	if err != nil {
		return oauthCredentialRecord{}, "", errors.B.Op(op).Text("discover OpenAI identity provider").Err(err).Build()
	}
	verified, err := provider.VerifierContext(discoveryContext, &oidc.Config{ClientID: clientID}).Verify(discoveryContext, tokens.IDToken)
	if err != nil {
		return oauthCredentialRecord{}, "", errors.B.Op(op).Text("verify OpenAI ID token").Err(err).Build()
	}
	var claims struct {
		Nonce   string `json:"nonce"`
		Subject string `json:"sub"`
		Email   string `json:"email"`
	}
	if err := verified.Claims(&claims); err != nil {
		return oauthCredentialRecord{}, "", errors.B.Op(op).Text("decode OpenAI identity claims").Err(err).Build()
	}
	if claims.Nonce != attempt.nonce || claims.Subject == "" {
		return oauthCredentialRecord{}, "", errors.B.Op(op).KindInvalidInput().Text("OpenAI identity claims do not match the authorization request").Build()
	}
	if attempt.existing != nil && claims.Subject != attempt.existing.Subject {
		return oauthCredentialRecord{}, "", errors.B.Op(op).KindConflict().Text("OpenAI account identity changed during reconnection").Build()
	}
	accountLabel := claims.Email
	if accountLabel == "" {
		accountLabel = claims.Subject
	}
	now := time.Now()
	return oauthCredentialRecord{
		Version: 1, Provider: openAIProviderType, Method: chatGPTSubscriptionMethod, Issuer: service.config.Issuer,
		Subject: claims.Subject, Email: claims.Email, ClientID: clientID, IDToken: tokens.IDToken,
		AccessToken: tokens.AccessToken, RefreshToken: tokens.RefreshToken, TokenType: tokens.TokenType,
		Scopes: scopes, SavedAt: now.Unix(), ExpiresAt: now.Add(time.Duration(tokens.ExpiresIn) * time.Second).Unix(),
		EarliestRefreshAt: tokens.EarliestRefreshAt,
	}, accountLabel, nil
}

func (service *Authorizer) save(
	ctx context.Context,
	instance *alfred.ProviderInstance,
	record oauthCredentialRecord,
) error {
	const op = "save"

	// #nosec G117 -- Tokens are serialized only for the credential store.
	data, err := json.Marshal(record)
	if err != nil {
		return errors.B.Op(op).Text("encode OpenAI credential").Err(err).Build()
	}
	ref := identity.New[alfred.CredentialRef]()
	if err := service.credentials.PutCredential(ctx, ref, data); err != nil {
		return errors.B.Op(op).Text("write OpenAI credential").Err(err).Build()
	}
	instance.CredentialRef = ref
	if err := service.store.PutProviderInstance(ctx, *instance); err != nil {
		cleanupErr := service.credentials.DeleteCredential(context.WithoutCancel(ctx), ref)
		return errors.B.Op(op).Text("save OpenAI provider instance").Err(err).Err(cleanupErr).Build()
	}
	return nil
}

func (service *Authorizer) agentHostID(ctx context.Context) (string, error) {
	const op = "agentHostID"

	data, err := service.store.GetKeyValue(ctx, agentHostIDKey)
	if err == nil {
		var record agentHostIDRecord
		if decodeErr := json.Unmarshal(data, &record); decodeErr != nil || record.Version != 1 || record.ID == "" {
			return "", errors.B.Op(op).KindInvalidInput().Text("saved OpenAI agent host ID is invalid").Err(decodeErr).Build()
		}
		return record.ID, nil
	}
	if errors.WhatKind(err) != errors.NotFound {
		return "", errors.B.Op(op).Text("read OpenAI agent host ID").Err(err).Build()
	}
	generated, err := uuid.NewRandom()
	if err != nil {
		return "", errors.B.Op(op).Text("generate OpenAI agent host ID").Err(err).Build()
	}
	id := generated.URN()
	data, err = json.Marshal(agentHostIDRecord{Version: 1, ID: id})
	if err != nil {
		return "", errors.B.Op(op).Text("encode OpenAI agent host ID").Err(err).Build()
	}
	if err := service.store.PutKeyValue(ctx, agentHostIDKey, data); err != nil {
		return "", errors.B.Op(op).Text("save OpenAI agent host ID").Err(err).Build()
	}
	return id, nil
}

func (service *Authorizer) finish(
	attempt *authorizationAttempt,
	state alfred.ProviderAuthorizationState,
	message, accountLabel string,
	instance *alfred.ProviderInstance,
) {
	attempt.result = alfred.ProviderAuthorization{State: state, Message: message, AccountLabel: accountLabel, Instance: instance}
}

func (service *Authorizer) fail(attempt *authorizationAttempt, message string) {
	service.finish(attempt, alfred.ProviderAuthorizationFailed, message, "", nil)
}
