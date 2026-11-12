package openai

import (
	_ "embed"
	"html/template"
	"net/http"
)

//go:embed authorization.html
var authorizationHTML string

var authorizationTemplate = template.Must(template.New("authorization").Parse(authorizationHTML))

// AuthorizationOption customizes browser authorization when composing the application.
type AuthorizationOption func(*AuthorizationService)

// WithAuthorizationHTTPClient replaces the authorization service's default HTTP client.
func WithAuthorizationHTTPClient(client *http.Client) AuthorizationOption {
	return func(service *AuthorizationService) { service.client = client }
}

// WithSuccessHTML replaces the successful authorization page with caller-supplied
// static HTML. It is served verbatim only after credentials and plan permission
// have been validated and saved. Empty HTML uses the bundled page.
func WithSuccessHTML(html string) AuthorizationOption {
	return func(service *AuthorizationService) { service.successHTML = html }
}

// WithAgentName sets the application name shown during initial OpenAI registration
// and on the bundled callback page. It does not rename an issued registration.
func WithAgentName(name string) AuthorizationOption {
	return func(service *AuthorizationService) { service.agentName = name }
}

func (service *AuthorizationService) authorizationPage(writer http.ResponseWriter, success bool, message string) {
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	title := "Authorization complete"
	if !success {
		title = "Authorization stopped"
		writer.WriteHeader(http.StatusBadRequest)
	}
	_ = authorizationTemplate.Execute(writer, struct {
		AgentName string
		Title     string
		Message   string
	}{service.agentName, title, message})
}
