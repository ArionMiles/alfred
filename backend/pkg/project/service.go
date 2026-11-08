// Package project resolves local workspaces to durable Alfred projects.
package project

import (
	"context"
	"path/filepath"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

type projectStore interface {
	CreateProject(context.Context, alfred.Project) error
	GetProjectByRoot(context.Context, string) (alfred.Project, error)
}

// Service resolves local directories to durable projects.
type Service struct {
	projects  projectStore
	projectID alfred.IDGenerator[alfred.ProjectID]
	clock     alfred.Clock
}

// NewService creates a project resolver from its runtime dependencies.
func NewService(
	projects projectStore,
	projectID alfred.IDGenerator[alfred.ProjectID],
	clock alfred.Clock,
) *Service {
	return &Service{
		projects:  projects,
		projectID: projectID,
		clock:     clock,
	}
}

// ResolveProject returns the project for root, creating it when necessary.
func (service *Service) ResolveProject(ctx context.Context, root string) (alfred.Project, error) {
	const op = "ResolveProject"

	canonicalRoot, err := filepath.Abs(root)
	if err != nil {
		return alfred.Project{}, errors.B.
			Op(op).
			KindInvalidArgument().
			Textf("make project root %q absolute", root).
			UserMsg("the project directory is invalid").
			Err(err).
			Build()
	}
	canonicalRoot, err = filepath.EvalSymlinks(canonicalRoot)
	if err != nil {
		return alfred.Project{}, errors.B.
			Op(op).
			KindInvalidArgument().
			Textf("resolve project root %q", canonicalRoot).
			UserMsg("the project directory does not exist or cannot be accessed").
			Err(err).
			Build()
	}

	project, err := service.projects.GetProjectByRoot(ctx, canonicalRoot)
	if err == nil {
		return project, nil
	}
	if errors.WhatKind(err) != errors.NotFound {
		return alfred.Project{}, errors.B.
			Op(op).
			Textf("find project for root %q", canonicalRoot).
			Err(err).
			Build()
	}

	id, err := service.projectID.New()
	if err != nil {
		return alfred.Project{}, errors.B.Op(op).Text("generate project ID").Err(err).Build()
	}
	project = alfred.Project{
		ID:         id,
		Name:       filepath.Base(canonicalRoot),
		Root:       canonicalRoot,
		SkillTrust: alfred.ProjectSkillTrustUnknown,
		CreatedAt:  service.clock.Now(),
	}
	if err := service.projects.CreateProject(ctx, project); err != nil {
		if errors.WhatKind(err) == errors.Conflict {
			existing, getErr := service.projects.GetProjectByRoot(ctx, canonicalRoot)
			if getErr == nil {
				return existing, nil
			}
		}
		return alfred.Project{}, errors.B.
			Op(op).
			Textf("create project %q", project.ID).
			Err(err).
			Build()
	}
	return project, nil
}

var _ alfred.ProjectResolver = (*Service)(nil)
