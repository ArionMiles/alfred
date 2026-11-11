package project

import (
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/mocks"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

func TestResolveProjectReturnsExistingCanonicalRoot(t *testing.T) {
	t.Parallel()
	root := canonicalTestRoot(t)
	want := alfred.Project{ID: "project-1", Root: root}
	controller := gomock.NewController(t)
	projects := mocks.NewMockProjectStore(controller)
	projects.EXPECT().GetProjectByRoot(gomock.Any(), root).Return(want, nil)
	service := NewService(projects, nil)

	got, err := service.ResolveProject(t.Context(), root, time.Time{})
	if err != nil {
		t.Fatalf("ResolveProject() error = %v", err)
	}
	if got != want {
		t.Fatalf("ResolveProject() = %#v, want %#v", got, want)
	}
}

func TestResolveProjectCreatesUnknownRoot(t *testing.T) {
	t.Parallel()
	root := canonicalTestRoot(t)
	createdAt := time.Date(2026, time.October, 25, 12, 0, 0, 0, time.UTC)
	want := alfred.Project{
		ID:         "project-1",
		Name:       filepath.Base(root),
		Root:       root,
		SkillTrust: alfred.ProjectSkillTrustUnknown,
		CreatedAt:  createdAt,
	}
	controller := gomock.NewController(t)
	projects := mocks.NewMockProjectStore(controller)
	projects.EXPECT().GetProjectByRoot(gomock.Any(), root).Return(
		alfred.Project{},
		errors.B.KindNotFound().Build(),
	)
	projects.EXPECT().CreateProject(gomock.Any(), want).Return(nil)
	service := NewService(projects, projectIDGenerator{id: want.ID})

	got, err := service.ResolveProject(t.Context(), root, createdAt)
	if err != nil {
		t.Fatalf("ResolveProject() error = %v", err)
	}
	if got != want {
		t.Fatalf("ResolveProject() = %#v, want %#v", got, want)
	}
}

func TestResolveProjectRejectsMissingRoot(t *testing.T) {
	t.Parallel()
	service := NewService(nil, nil)

	_, err := service.ResolveProject(t.Context(), filepath.Join(t.TempDir(), "missing"), time.Time{})
	if errors.WhatKind(err) != errors.InvalidArgument {
		t.Fatalf("ResolveProject() error = %v, want invalid argument", err)
	}
}

func TestResolveProjectReturnsConcurrentCreationWinner(t *testing.T) {
	t.Parallel()
	root := canonicalTestRoot(t)
	createdAt := time.Date(2026, time.October, 25, 12, 0, 0, 0, time.UTC)
	winner := alfred.Project{ID: "project-winner", Root: root}
	controller := gomock.NewController(t)
	projects := mocks.NewMockProjectStore(controller)
	projects.EXPECT().GetProjectByRoot(gomock.Any(), root).Return(
		alfred.Project{},
		errors.B.KindNotFound().Build(),
	)
	projects.EXPECT().CreateProject(gomock.Any(), gomock.Any()).Return(
		errors.B.KindConflict().Build(),
	)
	projects.EXPECT().GetProjectByRoot(gomock.Any(), root).Return(winner, nil)
	service := NewService(projects, projectIDGenerator{id: "project-loser"})

	got, err := service.ResolveProject(t.Context(), root, createdAt)
	if err != nil {
		t.Fatalf("ResolveProject() error = %v", err)
	}
	if got != winner {
		t.Fatalf("ResolveProject() = %#v, want concurrent winner %#v", got, winner)
	}
}

func canonicalTestRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks() error = %v", err)
	}
	return root
}

type projectIDGenerator struct {
	id alfred.ProjectID
}

func (generator projectIDGenerator) New() (alfred.ProjectID, error) {
	return generator.id, nil
}
