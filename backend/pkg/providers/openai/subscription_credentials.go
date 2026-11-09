package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

const refreshBeforeExpiry = time.Minute

// SubscriptionCredentials owns renewable OpenAI subscription credentials.
// One instance is shared by all configured OpenAI providers in a daemon.
type SubscriptionCredentials struct {
	credentials alfred.CredentialProvider
	config      AuthorizationConfig
	client      *http.Client
	clock       alfred.Clock

	mu sync.Mutex
}

func (record oauthCredentialRecord) directAccessEnabled() bool {
	return slices.Contains(record.Scopes, requiredDirectScope)
}

// NewSubscriptionCredentials constructs the renewable credential boundary.
func NewSubscriptionCredentials(
	credentials alfred.CredentialProvider,
	config AuthorizationConfig,
	client *http.Client,
	clock alfred.Clock,
) *SubscriptionCredentials {
	return &SubscriptionCredentials{credentials: credentials, config: config, client: client, clock: clock}
}

// AccessToken returns a usable bearer token, refreshing and atomically replacing
// the rotating token set when the saved access token is near expiry.
func (credentials *SubscriptionCredentials) AccessToken(
	ctx context.Context,
	ref alfred.CredentialRef,
) (string, error) {
	const op = "AccessToken"

	credentials.mu.Lock()
	defer credentials.mu.Unlock()
	record, err := credentials.read(ctx, ref)
	if err != nil {
		return "", errors.B.Op(op).Err(err).Build()
	}
	if !record.directAccessEnabled() {
		return "", errors.B.Op(op).KindFailedPrecondition().Retryable(true).
			UserMsg("Reconnect OpenAI and enable ChatGPT plan usage.").Build()
	}
	now := credentials.clock.Now()
	refreshAt := time.Unix(record.ExpiresAt, 0).Add(-refreshBeforeExpiry)
	if record.EarliestRefreshAt != 0 && time.Unix(record.EarliestRefreshAt, 0).After(refreshAt) {
		refreshAt = time.Unix(record.EarliestRefreshAt, 0)
	}
	if record.AccessToken != "" && now.Before(refreshAt) {
		return record.AccessToken, nil
	}
	if record.RefreshToken == "" {
		return "", errors.B.Op(op).KindUnauthenticated().Retryable(true).
			UserMsg("Reconnect OpenAI to renew ChatGPT plan access.").Build()
	}
	replacement, err := credentials.refresh(ctx, record)
	if err != nil {
		if errors.WhatKind(err) == errors.Unauthenticated {
			record.AccessToken = ""
			record.RefreshToken = ""
			data, encodeErr := json.Marshal(record)
			if encodeErr == nil {
				encodeErr = credentials.credentials.PutCredential(context.WithoutCancel(ctx), ref, data)
			}
			if encodeErr != nil {
				return "", errors.B.Op(op).Text("clear unusable OpenAI tokens").Err(err).Err(encodeErr).Build()
			}
		}
		return "", errors.B.Op(op).Err(err).Build()
	}
	data, err := json.Marshal(replacement)
	if err != nil {
		return "", errors.B.Op(op).Text("encode refreshed OpenAI credential").Err(err).Build()
	}
	// A completed refresh has already rotated the remote token. Persist its
	// replacement even if the model request was canceled in the meantime.
	if err := credentials.credentials.PutCredential(context.WithoutCancel(ctx), ref, data); err != nil {
		return "", errors.B.Op(op).Text("replace refreshed OpenAI credential").Err(err).Build()
	}
	if !replacement.directAccessEnabled() {
		return "", errors.B.Op(op).KindFailedPrecondition().Retryable(true).
			UserMsg("Reconnect OpenAI and enable ChatGPT plan usage.").Build()
	}
	return replacement.AccessToken, nil
}

func (credentials *SubscriptionCredentials) read(
	ctx context.Context,
	ref alfred.CredentialRef,
) (oauthCredentialRecord, error) {
	const op = "read"

	if ref == "" {
		return oauthCredentialRecord{}, errors.B.Op(op).KindInvalidArgument().Text("credential reference is required").Build()
	}
	data, err := credentials.credentials.GetCredential(ctx, ref)
	if err != nil {
		return oauthCredentialRecord{}, errors.B.Op(op).Text("read OpenAI subscription credential").Err(err).Build()
	}
	var record oauthCredentialRecord
	if err := json.Unmarshal(data, &record); err != nil || record.Version != 1 || record.Provider != openAIProviderType ||
		record.Method != chatGPTSubscriptionMethod || record.ClientID == "" || record.Subject == "" || record.ExpiresAt == 0 {
		return oauthCredentialRecord{}, errors.B.Op(op).KindFailedPrecondition().Retryable(true).
			UserMsg("The saved OpenAI connection is invalid. Reconnect it in Providers.").Build()
	}
	return record, nil
}

func (credentials *SubscriptionCredentials) refresh(
	ctx context.Context,
	record oauthCredentialRecord,
) (oauthCredentialRecord, error) {
	const op = "refresh"

	form := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {record.ClientID},
		"refresh_token": {record.RefreshToken},
		"resource":      {credentials.config.Resource},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, credentials.config.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return oauthCredentialRecord{}, errors.B.Op(op).Text("create OpenAI refresh request").Err(err).Build()
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := credentials.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return oauthCredentialRecord{}, errors.B.Op(op).Err(ctx.Err()).Build()
		}
		return oauthCredentialRecord{}, errors.B.Op(op).KindUnavailable().Retryable(true).
			UserMsg("OpenAI access could not be renewed. Try again.").Build()
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return oauthCredentialRecord{}, credentials.refreshError(response)
	}
	var tokens tokenResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&tokens); err != nil {
		return oauthCredentialRecord{}, errors.B.Op(op).KindBadGateway().Retryable(true).
			UserMsg("OpenAI returned an invalid renewal response.").Build()
	}
	if tokens.AccessToken == "" || tokens.RefreshToken == "" || tokens.ExpiresIn <= 0 {
		return oauthCredentialRecord{}, errors.B.Op(op).KindBadGateway().Retryable(true).
			UserMsg("OpenAI returned an incomplete renewal response.").Build()
	}
	if tokens.IDToken != "" {
		if err := credentials.validateReplacementIDToken(ctx, record, tokens.IDToken); err != nil {
			return oauthCredentialRecord{}, errors.B.Op(op).Err(err).Build()
		}
		record.IDToken = tokens.IDToken
	}
	if tokens.Scope != "" {
		record.Scopes = strings.Fields(tokens.Scope)
	}
	now := credentials.clock.Now()
	record.AccessToken = tokens.AccessToken
	record.RefreshToken = tokens.RefreshToken
	if tokens.TokenType != "" {
		record.TokenType = tokens.TokenType
	}
	record.SavedAt = now.Unix()
	record.ExpiresAt = now.Add(time.Duration(tokens.ExpiresIn) * time.Second).Unix()
	record.EarliestRefreshAt = tokens.EarliestRefreshAt
	return record, nil
}

func (credentials *SubscriptionCredentials) refreshError(response *http.Response) error {
	const op = "refreshError"

	var body struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	_ = json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&body)
	code := body.Code
	if code == "" {
		code = body.Error
	}
	switch code {
	case "invalid_grant", "invalid_refresh_token", "token_expired", "refresh_token_expired", "refresh_token_invalidated", "refresh_token_reused":
		return errors.B.Op(op).KindUnauthenticated().Retryable(true).
			UserMsg("OpenAI access expired. Reconnect it in Providers.").Build()
	case "invalid_client":
		return errors.B.Op(op).KindFailedPrecondition().
			UserMsg("The OpenAI connection is invalid. Reconnect it in Providers.").Build()
	default:
		if response.StatusCode >= http.StatusInternalServerError {
			return errors.B.Op(op).KindUnavailable().Retryable(true).
				UserMsg("OpenAI access could not be renewed. Try again.").Build()
		}
		return errors.B.Op(op).KindBadGateway().Retryable(true).
			UserMsg("OpenAI rejected the access renewal. Reconnect it in Providers.").Build()
	}
}

func (credentials *SubscriptionCredentials) validateReplacementIDToken(
	ctx context.Context,
	record oauthCredentialRecord,
	raw string,
) error {
	const op = "validateReplacementIDToken"

	discoveryContext := oidc.ClientContext(ctx, credentials.client)
	provider, err := oidc.NewProvider(discoveryContext, credentials.config.Issuer)
	if err != nil {
		return errors.B.Op(op).Text("discover OpenAI identity provider").Err(err).Build()
	}
	verified, err := provider.VerifierContext(discoveryContext, &oidc.Config{
		ClientID: record.ClientID,
		Now:      credentials.clock.Now,
	}).Verify(discoveryContext, raw)
	if err != nil {
		return errors.B.Op(op).KindBadGateway().Text("verify refreshed OpenAI ID token").Build()
	}
	var claims struct {
		Subject string `json:"sub"`
	}
	if err := verified.Claims(&claims); err != nil || claims.Subject != record.Subject {
		return errors.B.Op(op).KindConflict().Text("refreshed OpenAI identity does not match the connection").Build()
	}
	return nil
}
