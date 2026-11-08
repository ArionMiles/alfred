// Package context assembles bounded input for model calls.
package context

import (
	stdcontext "context"
	_ "embed"
	"iter"
	"strings"
	"text/template"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/internal/history"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

type sessionEventReader interface {
	ReadSessionEvents(stdcontext.Context, alfred.SessionID) iter.Seq2[alfred.RunEvent, error]
}

// Builder assembles model context from run-scoped and harness-owned sources.
type Builder struct {
	tools                alfred.ToolCatalog
	history              sessionEventReader
	projects             projectReader
	config               Config
	skillCatalogTemplate *template.Template
}

// Config supplies the already-resolved user configuration directory.
type Config struct{ ConfigDir string }

type projectReader interface {
	GetProject(stdcontext.Context, alfred.ProjectID) (alfred.Project, error)
}

//go:embed templates/skill_catalog.tmpl
var skillCatalogTemplateSource string

// New creates a context builder using registered tools and durable session history.
func New(config Config, tools alfred.ToolCatalog, events sessionEventReader, projects projectReader) *Builder {
	return &Builder{
		tools:    tools,
		history:  events,
		projects: projects, config: config,
		skillCatalogTemplate: template.Must(
			template.New("skill_catalog").Parse(skillCatalogTemplateSource),
		),
	}
}

// Build assembles the initial model context for a run.
func (builder *Builder) Build(
	ctx stdcontext.Context,
	request alfred.ContextRequest,
) (alfred.ModelContext, error) {
	const op = "Build"

	tools, err := builder.tools.ListTools(ctx)
	if err != nil {
		return alfred.ModelContext{}, errors.B.
			Op(op).
			Textf("list tools for run %q", request.RunID).
			Err(err).
			Build()
	}

	instructions, err := builder.instructions(ctx, request)
	if err != nil {
		return alfred.ModelContext{}, errors.B.
			Op(op).
			Textf("render skill catalog for run %q", request.RunID).
			Err(err).
			Build()
	}
	input, err := builder.priorInput(ctx, request.SessionID, request.RunID)
	if err != nil {
		return alfred.ModelContext{}, errors.B.Op(op).Err(err).Build()
	}

	modelContext := alfred.ModelContext{
		Instructions: instructions,
		Tools:        tools,
		Input:        input,
	}
	if len(request.Invocation.Input) > 0 {
		modelContext.Input = append(modelContext.Input, alfred.ModelInput{Payload: alfred.Message{
			Role:    alfred.MessageRoleUser,
			Content: request.Invocation.Input,
		}})
	}

	return modelContext, nil
}

func (builder *Builder) priorInput(
	ctx stdcontext.Context,
	sessionID alfred.SessionID,
	runID alfred.RunID,
) ([]alfred.ModelInput, error) {
	const op = "priorInput"

	projection := history.New(nil)
	for event, err := range builder.history.ReadSessionEvents(ctx, sessionID) {
		if err != nil {
			return nil, errors.B.Op(op).Textf("read earlier context in session %q", sessionID).Err(err).Build()
		}
		// The invocation records its input before dispatch. This boundary stays
		// fixed on resume; the runner owns replay of the current run's events.
		if event.RunID == runID {
			break
		}
		projection.Apply(event)
	}
	return projection.ResolvedInput(), nil
}

func (builder *Builder) skillCatalogInstructions(catalog alfred.SkillCatalog) (string, error) {
	if len(catalog.Skills) == 0 {
		return "", nil
	}

	var instructions strings.Builder
	if err := builder.skillCatalogTemplate.Execute(&instructions, catalog); err != nil {
		return "", err
	}

	return strings.TrimSpace(instructions.String()), nil
}

var _ alfred.ContextBuilder = (*Builder)(nil)
