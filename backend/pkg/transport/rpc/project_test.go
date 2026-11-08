package rpc

import (
	"testing"
	"time"

	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ArionMiles/alfred"
	alfredv1 "github.com/ArionMiles/alfred/gen/alfred/v1"
	"github.com/ArionMiles/alfred/mocks"
)

func TestProjectHandlerResolvesProject(t *testing.T) {
	t.Parallel()

	project := alfred.Project{
		ID:         "project-1",
		Name:       "alfred",
		Root:       "/code/alfred",
		SkillTrust: alfred.ProjectSkillTrustTrusted,
		CreatedAt:  time.Date(2026, time.October, 26, 13, 0, 0, 0, time.UTC),
	}
	controller := gomock.NewController(t)
	resolver := mocks.NewMockProjectResolver(controller)
	resolver.EXPECT().ResolveProject(gomock.Any(), project.Root).Return(project, nil)
	handler := NewProjectHandler(
		resolver,
		mocks.NewMockProjectSkillInspector(controller),
		mocks.NewMockProjectStore(controller),
	)

	response, err := handler.ResolveProject(t.Context(), &alfredv1.ResolveProjectRequest{
		Root: project.Root,
	})
	if err != nil {
		t.Fatalf("ResolveProject() error = %v", err)
	}
	want := &alfredv1.ResolveProjectResponse{Project: &alfredv1.Project{
		Id:         string(project.ID),
		Name:       project.Name,
		Root:       project.Root,
		SkillTrust: alfredv1.ProjectSkillTrust_PROJECT_SKILL_TRUST_TRUSTED,
		CreatedAt:  timestamppb.New(project.CreatedAt),
	}}
	if !proto.Equal(response, want) {
		t.Fatalf("ResolveProject() = %v, want %v", response, want)
	}
}

func TestProjectHandlerInspectsProjectSkills(t *testing.T) {
	t.Parallel()

	controller := gomock.NewController(t)
	state := alfred.ProjectSkillTrustState{
		ProjectID: "project-1",
		Trust:     alfred.ProjectSkillTrustUnknown,
		Roots:     []string{"/work/alfred/.agents/skills"},
	}
	inspector := mocks.NewMockProjectSkillInspector(controller)
	inspector.EXPECT().InspectProjectSkills(gomock.Any(), state.ProjectID).Return(state, nil)
	handler := NewProjectHandler(
		mocks.NewMockProjectResolver(controller),
		inspector,
		mocks.NewMockProjectStore(controller),
	)

	response, err := handler.InspectProjectSkills(t.Context(), &alfredv1.InspectProjectSkillsRequest{
		ProjectId: string(state.ProjectID),
	})
	if err != nil {
		t.Fatalf("InspectProjectSkills() error = %v", err)
	}
	want := &alfredv1.InspectProjectSkillsResponse{
		ProjectId: string(state.ProjectID),
		Trust:     alfredv1.ProjectSkillTrust_PROJECT_SKILL_TRUST_UNKNOWN,
		Roots:     state.Roots,
	}
	if !proto.Equal(response, want) {
		t.Fatalf("InspectProjectSkills() = %v, want %v", response, want)
	}
}

func TestProjectHandlerSetsSkillTrust(t *testing.T) {
	t.Parallel()

	controller := gomock.NewController(t)
	projects := mocks.NewMockProjectStore(controller)
	projects.EXPECT().SetProjectSkillTrust(
		gomock.Any(),
		alfred.ProjectID("project-1"),
		alfred.ProjectSkillTrustDenied,
	).Return(nil)
	handler := NewProjectHandler(
		mocks.NewMockProjectResolver(controller),
		mocks.NewMockProjectSkillInspector(controller),
		projects,
	)

	_, err := handler.SetProjectSkillTrust(t.Context(), &alfredv1.SetProjectSkillTrustRequest{
		ProjectId: "project-1",
		Trust:     alfredv1.ProjectSkillTrust_PROJECT_SKILL_TRUST_DENIED,
	})
	if err != nil {
		t.Fatalf("SetProjectSkillTrust() error = %v", err)
	}
}
