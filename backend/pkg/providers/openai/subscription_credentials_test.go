package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/mocks"
	"github.com/ArionMiles/alfred/pkg/storage/sqlite"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

func TestSubscriptionCredentialsReturnsCurrentAccessToken(t *testing.T) {
	t.Parallel()
	store := newSubscriptionCredentialStore(t)
	now := time.Now().Truncate(time.Second)
	ref := seedSubscriptionCredential(t, store, oauthCredentialRecord{
		AccessToken: "current-access", RefreshToken: "current-refresh", Scopes: []string{requiredDirectScope},
		ExpiresAt: now.Add(time.Hour).Unix(),
	})
	manager := NewSubscriptionCredentials(store, DefaultAuthorizationConfig(), http.DefaultClient)
	token, err := manager.AccessToken(t.Context(), ref)
	if err != nil || token != "current-access" {
		t.Fatalf("AccessToken() = %q, %v", token, err)
	}
}

func TestCredentialNeedsRefresh(t *testing.T) {
	t.Parallel()
	expiresAt := time.Unix(1_700_000_000, 0)
	refreshAt := expiresAt.Add(-refreshBeforeExpiry)
	deferredRefresh := expiresAt.Add(time.Minute)
	tests := map[string]struct {
		now               time.Time
		earliestRefreshAt int64
		accessToken       string
		want              bool
	}{
		"before threshold": {now: refreshAt.Add(-time.Nanosecond), accessToken: "token"},
		"at threshold":     {now: refreshAt, accessToken: "token", want: true},
		"after threshold":  {now: refreshAt.Add(time.Nanosecond), accessToken: "token", want: true},
		"missing token":    {now: refreshAt.Add(-time.Hour), want: true},
		"deferred refresh": {now: expiresAt, earliestRefreshAt: deferredRefresh.Unix(), accessToken: "token"},
		"at deferred refresh": {
			now: deferredRefresh, earliestRefreshAt: deferredRefresh.Unix(), accessToken: "token", want: true,
		},
		"earlier hint preserves threshold": {
			now: refreshAt, earliestRefreshAt: refreshAt.Add(-time.Minute).Unix(), accessToken: "token", want: true,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			record := oauthCredentialRecord{
				AccessToken: test.accessToken, ExpiresAt: expiresAt.Unix(), EarliestRefreshAt: test.earliestRefreshAt,
			}
			if got := record.needsRefresh(test.now); got != test.want {
				t.Fatalf("needsRefresh(%v) = %v, want %v", test.now, got, test.want)
			}
		})
	}
}

func TestSubscriptionCredentialsSerializesRotatingRefresh(t *testing.T) {
	t.Parallel()
	store := newSubscriptionCredentialStore(t)
	now := time.Now().Truncate(time.Second)
	ref := seedSubscriptionCredential(t, store, oauthCredentialRecord{
		AccessToken: "expired-access", RefreshToken: "old-refresh", Scopes: []string{requiredDirectScope},
		ExpiresAt: now.Add(-time.Minute).Unix(),
	})
	var refreshes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		refreshes.Add(1)
		if err := request.ParseForm(); err != nil {
			t.Error(err)
		}
		if request.Form.Get("grant_type") != "refresh_token" || request.Form.Get("client_id") != "issued-client" ||
			request.Form.Get("refresh_token") != "old-refresh" || request.Form.Get("resource") != "https://api.openai.com/v1" ||
			request.Form.Has("scope") {
			t.Errorf("refresh form = %v", request.Form)
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"access_token": "new-access", "refresh_token": "new-refresh", "token_type": "Bearer",
			"scope": requiredDirectScope, "expires_in": 3600,
		})
	}))
	t.Cleanup(server.Close)
	config := DefaultAuthorizationConfig()
	config.TokenURL = server.URL
	manager := NewSubscriptionCredentials(store, config, server.Client())

	const callers = 8
	var wait sync.WaitGroup
	wait.Add(callers)
	errorsByCaller := make(chan error, callers)
	for range callers {
		go func() {
			defer wait.Done()
			token, err := manager.AccessToken(t.Context(), ref)
			if err == nil && token != "new-access" {
				err = errors.B.Op("TestSubscriptionCredentialsSerializesRotatingRefresh").Textf("token = %q", token).Build()
			}
			errorsByCaller <- err
		}()
	}
	wait.Wait()
	close(errorsByCaller)
	for err := range errorsByCaller {
		if err != nil {
			t.Fatal(err)
		}
	}
	if refreshes.Load() != 1 {
		t.Fatalf("refresh requests = %d", refreshes.Load())
	}
	record := readSubscriptionCredential(t, store, ref)
	if record.AccessToken != "new-access" || record.RefreshToken != "new-refresh" ||
		record.SavedAt < now.Unix() || record.SavedAt > time.Now().Unix() ||
		record.ExpiresAt != record.SavedAt+3600 {
		t.Fatalf("saved replacement = %#v", record)
	}
}

func TestSubscriptionCredentialsClearsInvalidRotatingTokens(t *testing.T) {
	t.Parallel()
	store := newSubscriptionCredentialStore(t)
	now := time.Now().Truncate(time.Second)
	ref := seedSubscriptionCredential(t, store, oauthCredentialRecord{
		IDToken: "identity-token", AccessToken: "expired-access", RefreshToken: "invalid-refresh",
		Scopes: []string{requiredDirectScope}, ExpiresAt: now.Add(-time.Minute).Unix(),
	})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	t.Cleanup(server.Close)
	config := DefaultAuthorizationConfig()
	config.TokenURL = server.URL
	manager := NewSubscriptionCredentials(store, config, server.Client())
	if _, err := manager.AccessToken(t.Context(), ref); errors.WhatKind(err) != errors.Unauthenticated {
		t.Fatalf("AccessToken() error = %v", err)
	}
	record := readSubscriptionCredential(t, store, ref)
	if record.AccessToken != "" || record.RefreshToken != "" || record.IDToken != "identity-token" ||
		record.ClientID != "issued-client" || record.Subject != "account-subject" {
		t.Fatalf("saved invalidation = %#v", record)
	}
}

func TestSubscriptionCredentialsPreservesTokensAfterTransientFailure(t *testing.T) {
	t.Parallel()
	store := newSubscriptionCredentialStore(t)
	now := time.Now().Truncate(time.Second)
	ref := seedSubscriptionCredential(t, store, oauthCredentialRecord{
		AccessToken: "expired-access", RefreshToken: "retry-refresh", Scopes: []string{requiredDirectScope},
		ExpiresAt: now.Add(-time.Minute).Unix(),
	})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	config := DefaultAuthorizationConfig()
	config.TokenURL = server.URL
	manager := NewSubscriptionCredentials(store, config, server.Client())
	if _, err := manager.AccessToken(t.Context(), ref); errors.WhatKind(err) != errors.Unavailable || !errors.IsRetryable(err) {
		t.Fatalf("AccessToken() error = %v", err)
	}
	record := readSubscriptionCredential(t, store, ref)
	if record.AccessToken != "expired-access" || record.RefreshToken != "retry-refresh" {
		t.Fatalf("transient failure changed credential = %#v", record)
	}
}

func TestSubscriptionCredentialsReusesRotatedTokenAfterDatabaseRestart(t *testing.T) {
	t.Parallel()
	config := sqlite.Config{Path: filepath.Join(t.TempDir(), "alfred.db"), BusyTimeout: time.Second}
	store, err := sqlite.Open(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().Truncate(time.Second)
	ref := seedSubscriptionCredential(t, store, oauthCredentialRecord{
		AccessToken: "expired", RefreshToken: "first-refresh", IDToken: "retained-identity",
		Scopes: []string{requiredDirectScope}, ExpiresAt: now.Add(-time.Minute).Unix(),
	})
	refreshes := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := request.ParseForm(); err != nil {
			t.Error(err)
		}
		refreshes <- request.Form.Get("refresh_token")
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"access_token": "renewed-access", "refresh_token": "rotated-refresh", "expires_in": 3600,
		})
	}))
	t.Cleanup(server.Close)
	authConfig := DefaultAuthorizationConfig()
	authConfig.TokenURL = server.URL
	manager := NewSubscriptionCredentials(store, authConfig, server.Client())
	if _, err := manager.AccessToken(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	replacement := readSubscriptionCredential(t, store, ref)
	replacement.ExpiresAt = now.Add(-time.Minute).Unix()
	data, err := json.Marshal(replacement)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutCredential(t.Context(), ref, data); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = sqlite.Open(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	manager = NewSubscriptionCredentials(store, authConfig, server.Client())
	if _, err := manager.AccessToken(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	if first, second := <-refreshes, <-refreshes; first != "first-refresh" || second != "rotated-refresh" {
		t.Fatalf("refresh sequence = %q, %q", first, second)
	}
	record := readSubscriptionCredential(t, store, ref)
	if record.IDToken != "retained-identity" || !record.directAccessEnabled() || record.ClientID != "issued-client" {
		t.Fatalf("refresh lost registration or grant = %#v", record)
	}
}

func TestSubscriptionCredentialsDoesNotReturnTokenAfterScopeLoss(t *testing.T) {
	t.Parallel()
	store := newSubscriptionCredentialStore(t)
	now := time.Now().Truncate(time.Second)
	ref := seedSubscriptionCredential(t, store, oauthCredentialRecord{
		AccessToken: "expired", RefreshToken: "first-refresh", Scopes: []string{requiredDirectScope},
		ExpiresAt: now.Add(-time.Minute).Unix(),
	})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"access_token": "identity-only-access", "refresh_token": "rotated-refresh", "expires_in": 3600, "scope": "openid offline_access",
		})
	}))
	t.Cleanup(server.Close)
	config := DefaultAuthorizationConfig()
	config.TokenURL = server.URL
	manager := NewSubscriptionCredentials(store, config, server.Client())
	token, err := manager.AccessToken(t.Context(), ref)
	if token != "" || errors.WhatKind(err) != errors.FailedPrecondition {
		t.Fatalf("AccessToken() = %q, %v", token, err)
	}
	if record := readSubscriptionCredential(t, store, ref); record.RefreshToken != "rotated-refresh" || record.directAccessEnabled() {
		t.Fatalf("scope loss did not retain the replacement = %#v", record)
	}
}

func TestSubscriptionCredentialsPersistsRotationWhenRequestIsCanceled(t *testing.T) {
	t.Parallel()
	store := newSubscriptionCredentialStore(t)
	now := time.Now().Truncate(time.Second)
	ref := seedSubscriptionCredential(t, store, oauthCredentialRecord{
		AccessToken: "expired", RefreshToken: "first-refresh", Scopes: []string{requiredDirectScope},
		ExpiresAt: now.Add(-time.Minute).Unix(),
	})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"access_token": "renewed-access", "refresh_token": "rotated-refresh", "expires_in": 3600,
		})
	}))
	t.Cleanup(server.Close)
	config := DefaultAuthorizationConfig()
	config.TokenURL = server.URL
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	credentials := mocks.NewMockCredentialProvider(gomock.NewController(t))
	credentials.EXPECT().GetCredential(gomock.Any(), ref).DoAndReturn(store.GetCredential)
	credentials.EXPECT().PutCredential(gomock.Any(), ref, gomock.Any()).DoAndReturn(func(
		saveContext context.Context,
		ref alfred.CredentialRef,
		data []byte,
	) error {
		cancel()
		return store.PutCredential(saveContext, ref, data)
	})
	manager := NewSubscriptionCredentials(credentials, config, server.Client())
	if _, err := manager.AccessToken(ctx, ref); err != nil {
		t.Fatal(err)
	}
	record := readSubscriptionCredential(t, store, ref)
	if ctx.Err() == nil || record.RefreshToken != "rotated-refresh" {
		t.Fatal("cancellation discarded the rotated credential")
	}
}

func newSubscriptionCredentialStore(t *testing.T) *sqlite.Store {
	t.Helper()
	store, err := sqlite.Open(t.Context(), sqlite.Config{Path: filepath.Join(t.TempDir(), "alfred.db"), BusyTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func seedSubscriptionCredential(t *testing.T, store *sqlite.Store, record oauthCredentialRecord) alfred.CredentialRef {
	t.Helper()
	record.Version = 1
	record.Provider = "openai"
	record.Method = chatGPTSubscriptionMethod
	record.Issuer = "https://auth.openai.com"
	record.Subject = "account-subject"
	record.ClientID = "issued-client"
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	const ref alfred.CredentialRef = "openai-subscription-test"
	if err := store.PutCredential(t.Context(), ref, data); err != nil {
		t.Fatal(err)
	}
	return ref
}

func readSubscriptionCredential(t *testing.T, store *sqlite.Store, ref alfred.CredentialRef) oauthCredentialRecord {
	t.Helper()
	data, err := store.GetCredential(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	var record oauthCredentialRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	return record
}
