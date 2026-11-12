package openai_test

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	josejwt "github.com/go-jose/go-jose/v4/jwt"
	"github.com/google/uuid"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/providers/openai"
	"github.com/ArionMiles/alfred/pkg/storage/sqlite"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

func TestAuthorizationSavesValidatedConnection(t *testing.T) {
	t.Parallel()
	fixture := newOAuthFixture(t, true)
	store := newAuthorizationStore(t)
	service := newAuthorizationService(t, fixture, store)

	authorization, err := service.Start(t.Context(), alfred.ProviderAuthorizationRequest{
		Type: "openai", MethodID: "chatgpt-subscription",
	})
	if err != nil {
		t.Fatal(err)
	}
	authorizeURL, err := url.Parse(authorization.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	query := authorizeURL.Query()
	fixture.setAuthorization(query.Get("nonce"), query.Get("code_challenge"))
	if query.Get("client_id") != "dynamic_agent_client" || query.Get("ext_agent_host_id") == "" ||
		query.Get("code_challenge_method") != "S256" {
		t.Fatalf("authorization parameters = %v", query)
	}
	callback := query.Get("redirect_uri") + "?code=accepted-code&client_id=issued-client&state=" + url.QueryEscape(query.Get("state"))
	response, err := getAuthorizationURL(t, callback)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("callback status = %d", response.StatusCode)
	}
	result, err := service.Get(t.Context(), authorization.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != alfred.ProviderAuthorizationCompleted || result.AuthorizationURL != "" ||
		result.AccountLabel != "person@example.test" || result.Instance == nil {
		t.Fatalf("authorization result = %#v", result)
	}
	instance, err := store.GetProviderInstance(t.Context(), "openai")
	if err != nil {
		t.Fatal(err)
	}
	if instance.CredentialRef == "" || instance.Name != "OpenAI (ChatGPT plan)" || !instance.Enabled || len(instance.Settings) != 0 {
		t.Fatalf("saved instance = %#v", instance)
	}
	credential, err := store.GetCredential(t.Context(), instance.CredentialRef)
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	if err := json.Unmarshal(credential, &saved); err != nil {
		t.Fatal(err)
	}
	if saved["method"] != "chatgpt-subscription" || saved["client_id"] != "issued-client" ||
		saved["subject"] != "account-subject" || saved["refresh_token"] != "refresh-secret" {
		t.Fatalf("saved credential metadata = %#v", saved)
	}
	hostID, err := store.GetKeyValue(t.Context(), "providers.openai.agent_host_id")
	if err != nil || !json.Valid(hostID) || !strings.Contains(string(hostID), query.Get("ext_agent_host_id")) {
		t.Fatalf("saved host ID = %s, error = %v", hostID, err)
	}
}

func TestAuthorizationPreservesExistingDisabledState(t *testing.T) {
	t.Parallel()
	fixture := newOAuthFixture(t, true)
	store := newAuthorizationStore(t)
	if err := store.PutProviderInstance(t.Context(), alfred.ProviderInstance{
		ID: "openai", Type: "openai", Name: "Old client-supplied name", Enabled: false,
	}); err != nil {
		t.Fatal(err)
	}
	service := newAuthorizationService(t, fixture, store)
	authorization, err := service.Start(t.Context(), alfred.ProviderAuthorizationRequest{
		Type: "openai", MethodID: "chatgpt-subscription",
	})
	if err != nil {
		t.Fatal(err)
	}
	authorizeURL, err := url.Parse(authorization.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	query := authorizeURL.Query()
	fixture.setAuthorization(query.Get("nonce"), query.Get("code_challenge"))
	response, err := getAuthorizationURL(
		t,
		query.Get("redirect_uri")+"?code=accepted-code&client_id=issued-client&state="+url.QueryEscape(query.Get("state")),
	)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	instance, err := store.GetProviderInstance(t.Context(), "openai")
	if err != nil || instance.Enabled || instance.Name != openai.SubscriptionInstanceName {
		t.Fatalf("saved instance = %#v, %v", instance, err)
	}
}

func TestAuthorizationRejectsInvalidStateWithoutStoppingAttempt(t *testing.T) {
	t.Parallel()
	fixture := newOAuthFixture(t, true)
	store := newAuthorizationStore(t)
	service := newAuthorizationService(t, fixture, store)

	authorization, err := service.Start(t.Context(), alfred.ProviderAuthorizationRequest{
		Type: "openai", MethodID: "chatgpt-subscription",
	})
	if err != nil {
		t.Fatal(err)
	}
	authorizeURL, err := url.Parse(authorization.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	callback := authorizeURL.Query().Get("redirect_uri") + "?code=ignored&client_id=issued-client&state=wrong"
	response, err := getAuthorizationURL(t, callback)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("callback status = %d", response.StatusCode)
	}
	result, err := service.Get(t.Context(), authorization.ID)
	if err != nil || result.State != alfred.ProviderAuthorizationPending {
		t.Fatalf("result = %#v, error = %v", result, err)
	}
	result, err = service.Cancel(t.Context(), authorization.ID)
	if err != nil || result.State != alfred.ProviderAuthorizationCanceled {
		t.Fatalf("cancel result = %#v, error = %v", result, err)
	}
	if _, err := store.GetProviderInstance(t.Context(), "openai"); err == nil {
		t.Fatal("invalid callback saved a provider instance")
	}
}

func TestAuthorizationRetainsIdentityWithoutDirectTokenScope(t *testing.T) {
	t.Parallel()
	fixture := newOAuthFixture(t, false)
	store := newAuthorizationStore(t)
	service := newAuthorizationService(t, fixture, store)
	authorization, err := service.Start(t.Context(), alfred.ProviderAuthorizationRequest{
		Type: "openai", MethodID: "chatgpt-subscription",
	})
	if err != nil {
		t.Fatal(err)
	}
	authorizeURL, _ := url.Parse(authorization.AuthorizationURL)
	query := authorizeURL.Query()
	fixture.setAuthorization(query.Get("nonce"), query.Get("code_challenge"))
	response, err := getAuthorizationURL(
		t,
		query.Get("redirect_uri")+"?code=accepted-code&client_id=issued-client&state="+url.QueryEscape(query.Get("state")),
	)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	result, err := service.Get(t.Context(), authorization.ID)
	if err != nil || result.State != alfred.ProviderAuthorizationCompleted {
		t.Fatalf("result = %#v, error = %v", result, err)
	}
	if result.Instance == nil || result.Instance.CredentialRef == "" {
		t.Fatalf("completed result = %#v", result)
	}
	config := withDefaults[openai.AuthorizationConfig]()
	config.Issuer = fixture.server.URL
	config.TokenURL = fixture.server.URL + "/token"
	manager := openai.NewSubscriptionCredentials(store, config, openai.WithHTTPClient(fixture.server.Client()))
	if _, err := manager.AccessToken(t.Context(), result.Instance.CredentialRef); errors.WhatKind(err) != errors.FailedPrecondition {
		t.Fatalf("AccessToken() error = %v", err)
	}
}

func TestAuthorizationHostIDSurvivesServiceRestart(t *testing.T) {
	t.Parallel()
	fixture := newOAuthFixture(t, true)
	store := newAuthorizationStore(t)
	var previous string
	for range 2 {
		service := newAuthorizationService(t, fixture, store)
		authorization, err := service.Start(t.Context(), alfred.ProviderAuthorizationRequest{
			Type: "openai", MethodID: "chatgpt-subscription",
		})
		if err != nil {
			t.Fatal(err)
		}
		endpoint, err := url.Parse(authorization.AuthorizationURL)
		if err != nil {
			t.Fatal(err)
		}
		hostID := endpoint.Query().Get("ext_agent_host_id")
		parsed, err := uuid.Parse(hostID)
		if err != nil || parsed.Version() != 4 || parsed.URN() != hostID {
			t.Fatalf("host ID = %q, error = %v", hostID, err)
		}
		if previous != "" && hostID != previous {
			t.Fatalf("host ID changed from %q to %q", previous, hostID)
		}
		previous = hostID
		service.Close()
	}
}

func TestAuthorizationCallbackCoordinatesPollingCancellationAndDuplicate(t *testing.T) {
	t.Parallel()
	for _, cancel := range []bool{false, true} {
		name := "complete"
		if cancel {
			name = "cancel"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fixture := newOAuthFixture(t, true)
			fixture.tokenStarted = make(chan struct{}, 1)
			fixture.tokenRelease = make(chan struct{}, 1)
			defer close(fixture.tokenRelease)
			store := newAuthorizationStore(t)
			service := newAuthorizationService(t, fixture, store)
			authorization, err := service.Start(t.Context(), alfred.ProviderAuthorizationRequest{
				Type: "openai", MethodID: "chatgpt-subscription",
			})
			if err != nil {
				t.Fatal(err)
			}
			endpoint, err := url.Parse(authorization.AuthorizationURL)
			if err != nil {
				t.Fatal(err)
			}
			query := endpoint.Query()
			fixture.setAuthorization(query.Get("nonce"), query.Get("code_challenge"))
			callback := query.Get("redirect_uri") + "?code=accepted-code&client_id=issued-client&state=" + url.QueryEscape(query.Get("state"))
			finished := make(chan int, 1)
			go func() {
				response, err := getAuthorizationURL(t, callback)
				if err != nil {
					t.Error(err)
					finished <- 0
					return
				}
				_ = response.Body.Close()
				finished <- response.StatusCode
			}()
			select {
			case <-fixture.tokenStarted:
			case <-time.After(5 * time.Second):
				t.Fatal("callback did not reach token exchange")
			}
			result, err := service.Get(t.Context(), authorization.ID)
			if err != nil || result.State != alfred.ProviderAuthorizationPending {
				t.Fatalf("pending result = %#v, error = %v", result, err)
			}
			duplicate, err := getAuthorizationURL(t, callback)
			if err != nil {
				t.Fatal(err)
			}
			_ = duplicate.Body.Close()
			if duplicate.StatusCode != http.StatusBadRequest {
				t.Fatalf("duplicate callback status = %d", duplicate.StatusCode)
			}
			if cancel {
				if _, err := service.Cancel(t.Context(), authorization.ID); err != nil {
					t.Fatal(err)
				}
			}
			fixture.tokenRelease <- struct{}{}
			wantStatus := http.StatusOK
			wantState := alfred.ProviderAuthorizationCompleted
			if cancel {
				wantStatus = http.StatusConflict
				wantState = alfred.ProviderAuthorizationCanceled
			}
			select {
			case status := <-finished:
				if status != wantStatus {
					t.Fatalf("callback status = %d, want %d", status, wantStatus)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("callback did not finish")
			}
			result, err = service.Get(t.Context(), authorization.ID)
			if err != nil || result.State != wantState || fixture.tokenRequests.Load() != 1 {
				t.Fatalf("result = %#v, error = %v, token requests = %d", result, err, fixture.tokenRequests.Load())
			}
			if cancel {
				if _, err := store.GetProviderInstance(t.Context(), "openai"); errors.WhatKind(err) != errors.NotFound {
					t.Fatalf("canceled callback saved a provider: %v", err)
				}
			}
		})
	}
}

func TestReauthorizationReusesRegistrationAndPreservesConnectionOnFailure(t *testing.T) {
	t.Parallel()
	for _, outcome := range []string{"complete", "revoked", "denied", "canceled", "exchange_failure", "account_mismatch", "client_mismatch", "enable_plan"} {
		t.Run(outcome, func(t *testing.T) {
			t.Parallel()
			fixture := newOAuthFixture(t, outcome != "enable_plan")
			store := newAuthorizationStore(t)
			service := newAuthorizationService(t, fixture, store)
			request := alfred.ProviderAuthorizationRequest{Type: "openai", MethodID: "chatgpt-subscription"}
			first, err := service.Start(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			response := completeFixtureAuthorization(t, fixture, first)
			_ = response.Body.Close()
			before, err := store.GetProviderInstance(t.Context(), "openai")
			if err != nil {
				t.Fatal(err)
			}
			previous, err := store.GetCredential(t.Context(), before.CredentialRef)
			if err != nil {
				t.Fatal(err)
			}
			if outcome == "revoked" {
				var record map[string]any
				if err := json.Unmarshal(previous, &record); err != nil {
					t.Fatal(err)
				}
				record["access_token"], record["refresh_token"] = "", ""
				previous, err = json.Marshal(record)
				if err != nil {
					t.Fatal(err)
				}
				if err := store.PutCredential(t.Context(), before.CredentialRef, previous); err != nil {
					t.Fatal(err)
				}
			}
			service.Close()
			service = newAuthorizationService(t, fixture, store, openai.WithSuccessHTML("CUSTOM SUCCESS"))
			next, err := service.Start(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			endpoint, err := url.Parse(next.AuthorizationURL)
			if err != nil {
				t.Fatal(err)
			}
			query := endpoint.Query()
			if query.Get("client_id") != "issued-client" || query.Get("id_token_hint") == "" ||
				query.Get("login_hint") != "person@example.test" || query.Has("agent_name_hint") {
				t.Fatal("reauthorization did not reuse the saved registration")
			}
			firstURL, _ := url.Parse(first.AuthorizationURL)
			if query.Get("ext_agent_host_id") != firstURL.Query().Get("ext_agent_host_id") {
				t.Fatal("reauthorization changed the host ID")
			}
			if outcome == "enable_plan" {
				if query.Get("prompt") != "consent" || !strings.Contains(query.Get("scope"), "chatgpt.tokens.use.direct") {
					t.Fatal("enabling plan usage did not request consent and direct permission")
				}
			} else if query.Has("prompt") {
				t.Fatal("ordinary reauthorization forced consent")
			}
			fixture.mu.Lock()
			if outcome == "account_mismatch" {
				fixture.subject = "another-account"
			}
			if outcome == "exchange_failure" {
				fixture.tokenStatus = http.StatusServiceUnavailable
			}
			if outcome == "enable_plan" {
				fixture.directScope = true
			}
			fixture.mu.Unlock()
			wantState := alfred.ProviderAuthorizationCompleted
			switch outcome {
			case "canceled":
				_, err = service.Cancel(t.Context(), next.ID)
				if err != nil {
					t.Fatal(err)
				}
				wantState = alfred.ProviderAuthorizationCanceled
			case "denied", "client_mismatch":
				fixture.setAuthorization(query.Get("nonce"), query.Get("code_challenge"))
				callback := query.Get("redirect_uri") + "?state=" + url.QueryEscape(query.Get("state"))
				if outcome == "denied" {
					callback += "&error=access_denied"
					wantState = alfred.ProviderAuthorizationDenied
				} else {
					callback += "&code=accepted-code&client_id=wrong-client"
					wantState = alfred.ProviderAuthorizationFailed
				}
				response, err = getAuthorizationURL(t, callback)
				if err != nil {
					t.Fatal(err)
				}
				body, _ := io.ReadAll(response.Body)
				_ = response.Body.Close()
				if bytes.Contains(body, []byte("CUSTOM SUCCESS")) {
					t.Fatal("failed authorization served custom success HTML")
				}
			default:
				response = completeFixtureAuthorization(t, fixture, next)
				body, _ := io.ReadAll(response.Body)
				_ = response.Body.Close()
				if outcome == "exchange_failure" || outcome == "account_mismatch" {
					wantState = alfred.ProviderAuthorizationFailed
					if bytes.Contains(body, []byte("CUSTOM SUCCESS")) {
						t.Fatal("failed authorization served custom success HTML")
					}
				} else if string(body) != "CUSTOM SUCCESS" {
					t.Fatalf("success body = %s", body)
				}
			}
			result, err := service.Get(t.Context(), next.ID)
			if err != nil || result.State != wantState {
				t.Fatalf("result = %#v, error = %v", result, err)
			}
			after, err := store.GetProviderInstance(t.Context(), "openai")
			if err != nil {
				t.Fatal(err)
			}
			if wantState == alfred.ProviderAuthorizationCompleted {
				if after.CredentialRef == before.CredentialRef {
					t.Fatal("reauthorization did not save fresh credentials")
				}
				manager := openai.NewSubscriptionCredentials(store, withDefaults[openai.AuthorizationConfig](), openai.WithHTTPClient(fixture.server.Client()))
				if _, err := manager.AccessToken(t.Context(), after.CredentialRef); err != nil {
					t.Fatal(err)
				}
			} else if after.CredentialRef != before.CredentialRef || after.Name != before.Name || after.Enabled != before.Enabled {
				t.Fatal("failed reauthorization replaced saved connection")
			}
			retained, err := store.GetCredential(t.Context(), before.CredentialRef)
			if err != nil || !bytes.Equal(retained, previous) {
				t.Fatal("prior credentials changed during reauthorization")
			}
		})
	}
}

func TestAuthorizationPresentationOptions(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, agentName, html, want string
		direct                      bool
	}{
		{"default", "Alfred", "", "Authorization complete", true},
		{"custom", "My Agent", "<!doctype html><h1>Ready</h1>", "<!doctype html><h1>Ready</h1>", true},
		{"permission_missing", "My Agent", "CUSTOM SUCCESS", "ChatGPT plan usage was not enabled", false},
		{"escaped_name", "<script>bad()</script>", "", "&lt;script&gt;bad()&lt;/script&gt;", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newOAuthFixture(t, test.direct)
			store := newAuthorizationStore(t)
			service := newAuthorizationService(t, fixture, store, openai.WithSuccessHTML(test.html), openai.WithAgentName(test.agentName))
			authorization, err := service.Start(t.Context(), alfred.ProviderAuthorizationRequest{Type: "openai", MethodID: "chatgpt-subscription"})
			if err != nil {
				t.Fatal(err)
			}
			endpoint, _ := url.Parse(authorization.AuthorizationURL)
			if endpoint.Query().Get("agent_name_hint") != test.agentName {
				t.Fatal("application name override was not sent")
			}
			response := completeFixtureAuthorization(t, fixture, authorization)
			body, err := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if err != nil || response.StatusCode != http.StatusOK || !strings.Contains(string(body), test.want) ||
				response.Header.Get("Content-Type") != "text/html; charset=utf-8" || response.Header.Get("Cache-Control") != "no-store" {
				t.Fatalf("page status = %d, body = %s, error = %v", response.StatusCode, body, err)
			}
			if test.direct && test.html != "" && string(body) != test.html {
				t.Fatal("custom HTML was not served verbatim")
			}
			if bytes.Contains(body, []byte("<script>bad()")) || bytes.Contains(body, []byte("access-secret")) {
				t.Fatal("page exposed unescaped input or token")
			}
		})
	}
}

func completeFixtureAuthorization(t *testing.T, fixture *oauthFixture, authorization alfred.ProviderAuthorization) *http.Response {
	t.Helper()
	endpoint, err := url.Parse(authorization.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	query := endpoint.Query()
	fixture.setAuthorization(query.Get("nonce"), query.Get("code_challenge"))
	response, err := getAuthorizationURL(t, query.Get("redirect_uri")+"?code=accepted-code&client_id=issued-client&state="+url.QueryEscape(query.Get("state")))
	if err != nil {
		t.Fatal(err)
	}
	return response
}

type oauthFixture struct {
	server        *httptest.Server
	privateKey    *rsa.PrivateKey
	directScope   bool
	mu            sync.Mutex
	nonce         string
	challenge     string
	tokenStarted  chan struct{}
	tokenRelease  chan struct{}
	tokenRequests atomic.Int32
	subject       string
	tokenStatus   int
}

func newOAuthFixture(t *testing.T, directScope bool) *oauthFixture {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &oauthFixture{privateKey: privateKey, directScope: directScope, subject: "account-subject"}
	fixture.server = httptest.NewTLSServer(http.HandlerFunc(fixture.serveHTTP))
	t.Cleanup(fixture.server.Close)
	return fixture
}

func (fixture *oauthFixture) setAuthorization(nonce, challenge string) {
	fixture.mu.Lock()
	fixture.nonce = nonce
	fixture.challenge = challenge
	fixture.mu.Unlock()
}

func (fixture *oauthFixture) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	switch request.URL.Path {
	case "/.well-known/openid-configuration":
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"issuer": fixture.server.URL, "authorization_endpoint": fixture.server.URL + "/authorize",
			"token_endpoint": fixture.server.URL + "/token", "jwks_uri": fixture.server.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	case "/jwks":
		_ = json.NewEncoder(writer).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
			Key: &fixture.privateKey.PublicKey, KeyID: "test-key", Algorithm: string(jose.RS256), Use: "sig",
		}}})
	case "/token":
		fixture.tokenRequests.Add(1)
		if fixture.tokenStarted != nil {
			fixture.tokenStarted <- struct{}{}
			<-fixture.tokenRelease
		}
		if err := request.ParseForm(); err != nil || request.Form.Get("code_verifier") == "" || request.Form.Get("client_id") != "issued-client" {
			http.Error(writer, "invalid token request", http.StatusBadRequest)
			return
		}
		fixture.mu.Lock()
		nonce := fixture.nonce
		challenge := fixture.challenge
		subject := fixture.subject
		directScope := fixture.directScope
		tokenStatus := fixture.tokenStatus
		fixture.mu.Unlock()
		if tokenStatus != 0 {
			writer.WriteHeader(tokenStatus)
			return
		}
		verifier := request.Form.Get("code_verifier")
		digest := sha256.Sum256([]byte(verifier))
		if len(verifier) < 43 || len(verifier) > 128 || base64.RawURLEncoding.EncodeToString(digest[:]) != challenge {
			http.Error(writer, "PKCE challenge mismatch", http.StatusBadRequest)
			return
		}
		signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: fixture.privateKey},
			(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "test-key"))
		if err != nil {
			http.Error(writer, "signer failed", http.StatusInternalServerError)
			return
		}
		now := time.Now()
		idToken, err := josejwt.Signed(signer).Claims(josejwt.Claims{
			Issuer: fixture.server.URL, Subject: subject, Audience: josejwt.Audience{"issued-client"},
			Expiry: josejwt.NewNumericDate(now.Add(time.Hour)), IssuedAt: josejwt.NewNumericDate(now),
		}).Claims(map[string]any{"nonce": nonce, "email": "person@example.test"}).Serialize()
		if err != nil {
			http.Error(writer, "token failed", http.StatusInternalServerError)
			return
		}
		scope := "openid offline_access"
		if directScope {
			scope += " chatgpt.tokens.use.direct"
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"access_token": "access-secret", "refresh_token": "refresh-secret", "id_token": idToken,
			"token_type": "Bearer", "scope": scope, "expires_in": 3600,
		})
	default:
		http.NotFound(writer, request)
	}
}

func newAuthorizationStore(t *testing.T) *sqlite.Store {
	t.Helper()
	store, err := sqlite.Open(t.Context(), sqlite.Config{Path: filepath.Join(t.TempDir(), "alfred.db"), BusyTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func getAuthorizationURL(t *testing.T, address string) (*http.Response, error) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	return http.DefaultClient.Do(request)
}

func newAuthorizationService(t *testing.T, fixture *oauthFixture, store *sqlite.Store, options ...openai.AuthorizationOption) *openai.AuthorizationService {
	t.Helper()
	config := withDefaults[openai.AuthorizationConfig]()
	config.Issuer = fixture.server.URL
	config.AuthorizeURL = fixture.server.URL + "/authorize"
	config.TokenURL = fixture.server.URL + "/token"
	options = append([]openai.AuthorizationOption{openai.WithAuthorizationHTTPClient(fixture.server.Client())}, options...)
	service := openai.NewAuthorizationService(t.Context(), config, openai.AuthorizationDependencies{
		Store: store, Credentials: store,
	}, options...)
	t.Cleanup(service.Close)
	return service
}
