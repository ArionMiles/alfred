package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

// CatalogConfig contains internal loader settings, not a public TOML schema.
// The application supplies the cache directory separately from these settings.
type CatalogConfig struct {
	URL            string        `default:"https://models.dev/api.json"`
	CacheFileName  string        `default:"models.dev.json"`
	MaxAge         time.Duration `default:"24h"`
	RequestTimeout time.Duration `default:"15s"`
}

// CatalogSource loads current model metadata using the shared catalog cache.
type CatalogSource struct {
	config   CatalogConfig
	cacheDir string
	client   *http.Client
}

// CatalogOption customizes model catalog loading.
type CatalogOption func(*CatalogSource)

// WithHTTPClient replaces the catalog's default HTTP client.
func WithHTTPClient(client *http.Client) CatalogOption {
	return func(source *CatalogSource) { source.client = client }
}

// NewCatalogSource configures catalog loading independently of inference transport.
func NewCatalogSource(config CatalogConfig, cacheDir string, options ...CatalogOption) *CatalogSource {
	source := &CatalogSource{config: config, cacheDir: cacheDir, client: &http.Client{Timeout: config.RequestTimeout}}
	for _, option := range options {
		option(source)
	}
	return source
}

// Models loads the catalog and returns published metadata for the requested provider.
func (source *CatalogSource) Models(ctx context.Context, provider alfred.ProviderType) ([]alfred.ModelInfo, error) {
	const op = "Models"
	catalog, err := LoadCatalog(ctx, source.config, source.cacheDir, source.client)
	if err != nil {
		return nil, errors.B.Op(op).Err(err).Build()
	}
	return catalog.Models(provider), nil
}

// Catalog is a snapshot of provider-specific model metadata. Catalog membership
// describes published capabilities, not the account's access to a model.
type Catalog struct {
	providers map[alfred.ProviderType]catalogProvider
}

type catalogProvider struct {
	Models map[alfred.ModelID]catalogModel `json:"models"`
}

type catalogModel struct {
	Name       string `json:"name"`
	Reasoning  bool   `json:"reasoning"`
	ToolCall   bool   `json:"tool_call"`
	Modalities struct {
		Input  []string `json:"input"`
		Output []string `json:"output"`
	} `json:"modalities"`
	Limit struct {
		Context uint64 `json:"context"`
		Output  uint64 `json:"output"`
	} `json:"limit"`
}

// LoadCatalog reuses a fresh cache, otherwise fetches and atomically replaces
// it. A valid stale cache remains usable when the source cannot be reached.
// The complete source document, including pricing, is retained on disk.
func LoadCatalog(ctx context.Context, config CatalogConfig, cacheDir string, client *http.Client) (*Catalog, error) {
	const op = "LoadCatalog"
	if err := ctx.Err(); err != nil {
		return nil, errors.B.Op(op).Err(err).Build()
	}
	cacheFile := filepath.Join(cacheDir, config.CacheFileName)
	cached, cachedAt := readCatalogCache(cacheFile)
	if cached != nil && time.Since(cachedAt) < config.MaxAge {
		return cached, nil
	}
	data, err := fetchCatalog(ctx, config.URL, client)
	if err != nil {
		if ctx.Err() != nil {
			return nil, errors.B.Op(op).Err(ctx.Err()).Build()
		}
		if cached != nil {
			return cached, nil
		}
		return nil, errors.B.Op(op).Err(err).Build()
	}
	catalog, err := decodeCatalog(data)
	if err != nil {
		if cached != nil {
			return cached, nil
		}
		return nil, errors.B.Op(op).Err(err).Build()
	}
	if err := writeCatalogCache(cacheFile, data); err != nil {
		return nil, errors.B.Op(op).Err(err).Build()
	}
	return catalog, nil
}

// Models returns published metadata in model-ID order. The runtime adapter
// assigns its configured provider-instance identity to each returned model.
func (catalog *Catalog) Models(provider alfred.ProviderType) []alfred.ModelInfo {
	entries := catalog.providers[provider].Models
	models := make([]alfred.ModelInfo, 0, len(entries))
	for id, entry := range entries {
		models = append(models, alfred.ModelInfo{
			Ref: alfred.ModelRef{ModelID: id}, Name: entry.Name,
			Capabilities: alfred.ModelCapabilities{
				InputModalities:  catalogModalities(entry.Modalities.Input),
				OutputModalities: catalogModalities(entry.Modalities.Output),
				ContextWindow:    entry.Limit.Context, MaxOutputTokens: entry.Limit.Output,
				ToolCalling: entry.ToolCall, Reasoning: entry.Reasoning,
			},
		})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].Ref.ModelID < models[j].Ref.ModelID })
	return models
}

func catalogModalities(values []string) []alfred.ContentKind {
	var result []alfred.ContentKind
	for _, value := range values {
		kind := alfred.ContentKind(value)
		if value == "pdf" {
			kind = alfred.ContentKindFile
		}
		switch kind {
		case alfred.ContentKindText, alfred.ContentKindImage, alfred.ContentKindAudio, alfred.ContentKindVideo, alfred.ContentKindFile:
			if !slices.Contains(result, kind) {
				result = append(result, kind)
			}
		}
	}
	return result
}

func decodeCatalog(data []byte) (*Catalog, error) {
	const op = "decodeCatalog"
	var entries map[alfred.ProviderType]catalogProvider
	if err := json.Unmarshal(data, &entries); err != nil || len(entries) == 0 {
		return nil, errors.B.Op(op).KindBadGateway().UserMsg("The model catalog could not be read.").Build()
	}
	return &Catalog{providers: entries}, nil
}

func readCatalogCache(path string) (*Catalog, time.Time) {
	file, err := os.Open(path)
	if err != nil {
		return nil, time.Time{}
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, time.Time{}
	}
	data, err := io.ReadAll(io.LimitReader(file, maxCatalogBytes+1))
	if err != nil || len(data) > maxCatalogBytes {
		return nil, time.Time{}
	}
	catalog, err := decodeCatalog(data)
	if err != nil {
		return nil, time.Time{}
	}
	return catalog, info.ModTime()
}

// The complete upstream document exceeds 4 MiB; bound both disk and HTTP reads.
const maxCatalogBytes = 32 << 20

func fetchCatalog(ctx context.Context, url string, client *http.Client) ([]byte, error) {
	const op = "fetchCatalog"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, errors.B.Op(op).KindInvalidArgument().Text("invalid model catalog URL").Build()
	}
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, errors.B.Op(op).Err(ctx.Err()).Build()
		}
		return nil, errors.B.Op(op).KindUnavailable().Retryable(true).UserMsg("The model catalog could not be downloaded.").Build()
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, errors.B.Op(op).KindUnavailable().Retryable(true).
			Textf("model catalog HTTP status %d", response.StatusCode).
			UserMsg("The model catalog could not be downloaded.").Build()
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxCatalogBytes+1))
	if err != nil || len(data) > maxCatalogBytes {
		return nil, errors.B.Op(op).KindBadGateway().UserMsg("The model catalog download was incomplete or too large.").Build()
	}
	return data, nil
}

func writeCatalogCache(path string, data []byte) error {
	const op = "writeCatalogCache"
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return errors.B.Op(op).Text("create model catalog directory").Err(err).Build()
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".models-*")
	if err != nil {
		return errors.B.Op(op).Text("create model catalog cache").Err(err).Build()
	}
	defer func() { _ = os.Remove(file.Name()) }()
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		return errors.B.Op(op).Text("write model catalog cache").Err(writeErr).Err(closeErr).Build()
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return errors.B.Op(op).Text("replace model catalog cache").Err(err).Build()
	}
	return nil
}
