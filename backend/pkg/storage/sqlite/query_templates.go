package sqlite

import (
	"database/sql"
	"embed"
	"strings"
	"text/template"

	"github.com/ArionMiles/alfred/pkg/x/errors"
)

//go:embed query_templates/*.sql.tmpl
var queryTemplateFiles embed.FS

const queryFlagHasAfter = "hasAfter"

// queryTemplateVars contains variables and boolean flags required for generating SQL queries.
// WARNING: Changes to this struct may be rejected.
type queryTemplateVars struct {
	Flags map[string]bool
}

// Parameters holds SQL query parameters.
type Parameters struct {
	m map[string]any
}

// NewParameters creates an empty named-parameter collection.
func NewParameters() *Parameters {
	return &Parameters{m: make(map[string]any)}
}

// Pars returns parameters in the form accepted by database/sql.
func (parameters *Parameters) Pars() []any {
	args := make([]any, 0, len(parameters.m))
	for key, value := range parameters.m {
		args = append(args, sql.Named(key, value))
	}
	return args
}

func (parameters *Parameters) addLimit(limit int64) {
	parameters.addParameter("limit", limit)
}

func (parameters *Parameters) addParameter(key string, value any) {
	parameters.m[key] = value
}

type queryTemplates struct {
	listProjects *template.Template
	listSessions *template.Template
	listRuns     *template.Template
}

func loadQueryTemplates() (queryTemplates, error) {
	const op = "loadQueryTemplates"

	parsed, err := template.ParseFS(queryTemplateFiles, "query_templates/*.sql.tmpl")
	if err != nil {
		return queryTemplates{}, errors.B.Op(op).Text("parse query templates").Err(err).Build()
	}
	queries := queryTemplates{}
	required := map[string]**template.Template{
		"list_projects.sql.tmpl": &queries.listProjects,
		"list_sessions.sql.tmpl": &queries.listSessions,
		"list_runs.sql.tmpl":     &queries.listRuns,
	}
	for name, destination := range required {
		query := parsed.Lookup(name)
		if query == nil {
			return queryTemplates{}, errors.B.
				Op(op).
				Textf("query template %q is missing", name).
				Build()
		}
		*destination = query
	}
	return queries, nil
}

func renderQuery(query *template.Template, variables queryTemplateVars) (string, error) {
	const op = "renderQuery"

	var rendered strings.Builder
	if err := query.Execute(&rendered, variables); err != nil {
		return "", errors.B.Op(op).Textf("render query %q", query.Name()).Err(err).Build()
	}
	return rendered.String(), nil
}
