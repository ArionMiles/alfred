package tools_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/mocks"
	"github.com/ArionMiles/alfred/pkg/storage/sqlite"
	"github.com/ArionMiles/alfred/pkg/tools"
)

func TestWebSearchRoutesToDefaultAndExplicitProviders(t *testing.T) {
	t.Parallel()
	controller := gomock.NewController(t)
	exa, other := mocks.NewMockSearchProvider(controller), mocks.NewMockSearchProvider(controller)
	exa.EXPECT().Name().Return("exa").AnyTimes()
	other.EXPECT().Name().Return("other").AnyTimes()
	exa.EXPECT().Search(gomock.Any(), "query", 5).Return([]tools.SearchResult{{Title: "Default", URL: "https://example.org"}}, nil)
	other.EXPECT().Search(gomock.Any(), "query", 2).Return([]tools.SearchResult{{Title: "Alternative"}}, nil)
	tool, err := tools.NewWebSearch(tools.WebSearchConfig{Provider: "exa"}, exa, other)
	if err != nil {
		t.Fatal(err)
	}
	if text := webInvoke(t, tool, map[string]any{"query": "query"}); !strings.Contains(text, "Default") {
		t.Fatal(text)
	}
	if text := webInvoke(t, tool, map[string]any{"query": "query", "provider": "other", "limit": 2}); !strings.Contains(text, "Alternative") {
		t.Fatal(text)
	}
	if _, err := tools.NewWebSearch(tools.WebSearchConfig{Provider: "missing"}, exa); err == nil {
		t.Fatal("unknown configured provider accepted")
	}
}

func TestExaUsesSavedCredentialsAndDoesNotExposeRemoteErrors(t *testing.T) {
	t.Parallel()
	store, err := sqlite.Open(t.Context(), sqlite.Config{Path: filepath.Join(t.TempDir(), "alfred.db"), BusyTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	keys := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		keys <- request.Header.Get("x-api-key")
		var body struct {
			Query string `json:"query"`
			Limit int    `json:"numResults"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil || body.Query != "query" || body.Limit != 3 {
			t.Errorf("request=%v error=%v", body, err)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"results":[{"title":"Result","url":"https://example.org","text":"Excerpt"}]}`))
	}))
	t.Cleanup(server.Close)
	provider := tools.NewExa(server.URL, store, store, server.Client())
	if _, err := provider.Search(t.Context(), "query", 3); err == nil {
		t.Fatal("search without setup succeeded")
	}
	if err := store.PutSearchSettings(t.Context(), alfred.SearchProviderSettings{Backend: "exa", Enabled: true, CredentialRef: "exa-key"}); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"first-key", "rotated-key", "moved-key"} {
		backendField := "backend"
		if key == "moved-key" {
			backendField = "provider"
		}
		data, _ := json.Marshal(map[string]any{"version": 1, backendField: "exa", "method": "api-key", "fields": map[string]string{"api_key": key}})
		if err := store.PutCredential(t.Context(), "exa-key", data); err != nil {
			t.Fatal(err)
		}
		results, err := provider.Search(t.Context(), "query", 3)
		if err != nil || len(results) != 1 || results[0].Text != "Excerpt" || <-keys != key {
			t.Fatalf("results=%v error=%v", results, err)
		}
	}
	failed := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = writer.Write([]byte("rotated-key"))
	}))
	t.Cleanup(failed.Close)
	_, err = tools.NewExa(failed.URL, store, store, failed.Client()).Search(t.Context(), "query", 3)
	if err == nil || strings.Contains(err.Error(), "rotated-key") {
		t.Fatalf("unsafe error=%v", err)
	}
	redirect := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, server.URL, http.StatusFound)
	}))
	t.Cleanup(redirect.Close)
	if _, err := tools.NewExa(redirect.URL, store, store, redirect.Client()).Search(t.Context(), "query", 3); err == nil {
		t.Fatal("credential-bearing request followed a redirect")
	}
}

func TestWebFetchReturnsBoundedContentAndRejectsNonHTTPURLs(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(strings.Repeat("content ", 20000)))
	}))
	t.Cleanup(server.Close)
	tool := tools.NewWebFetch(server.Client())
	text := webInvoke(t, tool, map[string]any{"url": server.URL})
	if !strings.Contains(text, "Output truncated") || len(text) > 66000 {
		t.Fatal("fetch response was not bounded")
	}
	text = webInvoke(t, tool, map[string]any{"url": "file:///etc/passwd"})
	if !strings.Contains(text, "HTTP(S)") {
		t.Fatal("file URL accepted")
	}
}

func webInvoke(t *testing.T, tool alfred.Tool, args any) string {
	t.Helper()
	data, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	result, err := tool.Invoke(context.Background(), alfred.ToolInvocation{Call: alfred.ToolCall{ID: "call", Arguments: data}})
	if err != nil {
		t.Fatal(err)
	}
	return result.Result.Output[0].(alfred.TextContent).Text
}
