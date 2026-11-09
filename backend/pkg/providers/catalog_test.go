package providers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

const catalogFixture = `{
 "openai":{"models":{
  "test-z":{"name":"Test Z","reasoning":true,"tool_call":true,"modalities":{"input":["text","image","pdf"],"output":["text"]},"limit":{"context":128000,"output":16000},"cost":{"input":1.25,"output":10,"cache_read":0.125,"future_tier":{"input":2.5}}},
  "test-a":{"name":"Test A","modalities":{"input":["text"],"output":["text"]},"limit":{"context":64000,"output":8000}}
 }},
 "other":{"models":{"other-model":{"name":"Other","modalities":{"input":["text"],"output":["text"]}}}}
}`

func TestLoadCatalogFetchesCachesAndMapsPublishedMetadata(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.Method != http.MethodGet {
			t.Error("catalog request was not GET")
		}
		_, _ = writer.Write([]byte(catalogFixture))
	}))
	t.Cleanup(server.Close)
	config := DefaultCatalogConfig()
	config.URL, config.CacheFile = server.URL, filepath.Join(t.TempDir(), "catalog", "models.dev.json")
	catalog, err := LoadCatalog(t.Context(), config, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	models := catalog.Models("openai")
	if len(models) != 2 || models[0].Ref.ModelID != "test-a" || models[1].Name != "Test Z" || models[1].Ref.ProviderID != "" {
		t.Fatalf("models=%#v", models)
	}
	want := alfred.ModelCapabilities{InputModalities: []alfred.ContentKind{alfred.ContentKindText, alfred.ContentKindImage, alfred.ContentKindFile}, OutputModalities: []alfred.ContentKind{alfred.ContentKindText}, ContextWindow: 128000, MaxOutputTokens: 16000, ToolCalling: true, Reasoning: true}
	if !reflect.DeepEqual(models[1].Capabilities, want) {
		t.Fatalf("capabilities=%#v", models[1].Capabilities)
	}
	models[1].Capabilities.InputModalities[0] = alfred.ContentKindAudio
	if got := catalog.Models("openai"); got[1].Capabilities.InputModalities[0] != alfred.ContentKindText {
		t.Fatal("caller mutated catalog metadata")
	}
	if len(catalog.Models("unknown")) != 0 || len(catalog.Models("other")) != 1 {
		t.Fatal("provider catalog lookup was not scoped")
	}
	data, err := os.ReadFile(config.CacheFile)
	if err != nil || string(data) != catalogFixture {
		t.Fatalf("source document not retained, error=%v", err)
	}
	cached, err := LoadCatalog(t.Context(), config, server.Client())
	if err != nil || calls.Load() != 1 || !reflect.DeepEqual(cached.Models("openai"), catalog.Models("openai")) {
		t.Fatalf("cache error=%v requests=%d", err, calls.Load())
	}
}

func TestCatalogRefreshRetainsUsableCacheOnFailure(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		status int
		body   string
	}{
		{"unavailable", http.StatusServiceUnavailable, `upstream unavailable`},
		{"malformed", http.StatusOK, `not json`},
		{"empty", http.StatusOK, `{}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				writer.WriteHeader(test.status)
				_, _ = writer.Write([]byte(test.body))
			}))
			t.Cleanup(server.Close)
			config := DefaultCatalogConfig()
			config.URL, config.CacheFile = server.URL, filepath.Join(t.TempDir(), "models.dev.json")
			if err := os.WriteFile(config.CacheFile, []byte(catalogFixture), 0o600); err != nil {
				t.Fatal(err)
			}
			old := time.Now().Add(-48 * time.Hour)
			if err := os.Chtimes(config.CacheFile, old, old); err != nil {
				t.Fatal(err)
			}
			catalog, err := LoadCatalog(t.Context(), config, server.Client())
			if err != nil || len(catalog.Models("openai")) != 2 || calls.Load() != 1 {
				t.Fatalf("stale fallback error=%v requests=%d", err, calls.Load())
			}
			data, err := os.ReadFile(config.CacheFile)
			if err != nil || string(data) != catalogFixture {
				t.Fatal("failed refresh replaced usable cache")
			}
		})
	}
}

func TestCatalogSourceAcceptsCatalogWithoutOpenAI(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"other":{"models":{"other-model":{"name":"Other"}}}}`))
	}))
	t.Cleanup(server.Close)
	config := DefaultCatalogConfig()
	config.URL, config.CacheFile = server.URL, filepath.Join(t.TempDir(), "models.dev.json")
	source := NewCatalogSource(config, server.Client())
	models, err := source.Models(t.Context(), "other")
	if err != nil || len(models) != 1 || models[0].Ref.ModelID != "other-model" {
		t.Fatalf("Models(other) = %#v, %v", models, err)
	}
	models, err = source.Models(t.Context(), "openai")
	if err != nil || len(models) != 0 {
		t.Fatalf("Models(openai) = %#v, %v", models, err)
	}
}

func TestCatalogRefreshReplacesStaleCache(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { _, _ = writer.Write([]byte(catalogFixture)) }))
	t.Cleanup(server.Close)
	config := DefaultCatalogConfig()
	config.URL, config.CacheFile = server.URL, filepath.Join(t.TempDir(), "models.dev.json")
	if err := os.WriteFile(config.CacheFile, []byte(`{"openai":{"models":{"old":{"name":"Old"}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(config.CacheFile, old, old); err != nil {
		t.Fatal(err)
	}
	catalog, err := LoadCatalog(t.Context(), config, server.Client())
	if err != nil || len(catalog.Models("openai")) != 2 {
		t.Fatalf("refresh error=%v", err)
	}
	data, err := os.ReadFile(config.CacheFile)
	if err != nil || string(data) != catalogFixture {
		t.Fatal("successful refresh did not replace stale cache")
	}
}

func TestCatalogWithoutCacheReportsDownloadAndDecodeErrors(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		status int
		body   string
		kind   errors.Kind
	}{
		{"http", http.StatusServiceUnavailable, `unavailable`, errors.Unavailable},
		{"json", http.StatusOK, `invalid json`, errors.BadGateway},
		{"empty", http.StatusOK, `{}`, errors.BadGateway},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(test.status)
				_, _ = writer.Write([]byte(test.body))
			}))
			t.Cleanup(server.Close)
			config := DefaultCatalogConfig()
			config.URL, config.CacheFile = server.URL, filepath.Join(t.TempDir(), "models.dev.json")
			catalog, err := LoadCatalog(t.Context(), config, server.Client())
			if catalog != nil || errors.WhatKind(err) != test.kind {
				t.Fatalf("catalog=%v error=%v", catalog, err)
			}
			if _, err := os.Stat(config.CacheFile); !os.IsNotExist(err) {
				t.Fatal("invalid download was cached")
			}
		})
	}
}

func TestCatalogCancellationDoesNotFallBackToStaleCache(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		close(started)
		<-request.Context().Done()
	}))
	t.Cleanup(server.Close)
	config := DefaultCatalogConfig()
	config.URL, config.CacheFile, config.MaxAge = server.URL, filepath.Join(t.TempDir(), "models.dev.json"), 0
	if err := os.WriteFile(config.CacheFile, []byte(catalogFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := LoadCatalog(ctx, config, server.Client())
		done <- err
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("catalog request did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation error=%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("catalog request did not stop")
	}
}
