package tools

import "net/http"

type httpOptions struct {
	client *http.Client
}

// HTTPOption customizes the HTTP client used by a web tool.
type HTTPOption func(*httpOptions)

// WithHTTPClient replaces the tool's default client with a caller-owned client.
func WithHTTPClient(client *http.Client) HTTPOption {
	return func(options *httpOptions) { options.client = client }
}
