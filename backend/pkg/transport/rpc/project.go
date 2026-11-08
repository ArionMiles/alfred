package rpc

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ArionMiles/alfred"
	alfredv1 "github.com/ArionMiles/alfred/gen/alfred/v1"
	"github.com/ArionMiles/alfred/gen/alfred/v1/alfredv1connect"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

type projectSkillTrustSetter interface {
	SetProjectSkillTrust(context.Context, alfred.ProjectID, alfred.ProjectSkillTrust) error
}

// ProjectHandler exposes project operations over RPC.
type ProjectHandler struct {
	resolver    alfred.ProjectResolver
	inspector   alfred.ProjectSkillInspector
	trustSetter projectSkillTrustSetter
}

// NewProjectHandler creates a project RPC handler.
func NewProjectHandler(
	resolver alfred.ProjectResolver,
	inspector alfred.ProjectSkillInspector,
	trustSetter projectSkillTrustSetter,
) *ProjectHandler {
	return &ProjectHandler{
		resolver:    resolver,
		inspector:   inspector,
		trustSetter: trustSetter,
	}
}

// InspectProjectSkills reports existing project skill roots without loading them.
func (handler *ProjectHandler) InspectProjectSkills(
	ctx context.Context,
	request *alfredv1.InspectProjectSkillsRequest,
) (*alfredv1.InspectProjectSkillsResponse, error) {
	const op = "InspectProjectSkills"

	if request == nil {
		return nil, connectError(errors.B.
			Op(op).
			KindInvalidArgument().
			Text("request is nil").
			Build())
	}
	state, err := handler.inspector.InspectProjectSkills(ctx, alfred.ProjectID(request.GetProjectId()))
	if err != nil {
		return nil, connectError(err)
	}
	trust, err := encodeProjectSkillTrust(state.Trust)
	if err != nil {
		return nil, connectError(errors.B.Op(op).Err(err).Build())
	}
	return &alfredv1.InspectProjectSkillsResponse{
		ProjectId: string(state.ProjectID),
		Trust:     trust,
		Roots:     append([]string(nil), state.Roots...),
	}, nil
}

// ResolveProject resolves a service-local directory to its durable project.
func (handler *ProjectHandler) ResolveProject(
	ctx context.Context,
	request *alfredv1.ResolveProjectRequest,
) (*alfredv1.ResolveProjectResponse, error) {
	if request == nil {
		return nil, connectError(errors.B.
			Op("ResolveProject").
			KindInvalidArgument().
			Text("request is nil").
			Build())
	}
	project, err := handler.resolver.ResolveProject(ctx, request.GetRoot())
	if err != nil {
		return nil, connectError(err)
	}
	encoded, err := encodeProject(project)
	if err != nil {
		return nil, connectError(err)
	}
	return &alfredv1.ResolveProjectResponse{Project: encoded}, nil
}

// SetProjectSkillTrust records whether project-provided skills may be loaded.
func (handler *ProjectHandler) SetProjectSkillTrust(
	ctx context.Context,
	request *alfredv1.SetProjectSkillTrustRequest,
) (*alfredv1.SetProjectSkillTrustResponse, error) {
	const op = "SetProjectSkillTrust"

	if request == nil {
		return nil, connectError(errors.B.
			Op(op).
			KindInvalidArgument().
			Text("request is nil").
			Build())
	}
	trust, err := decodeProjectSkillTrust(request.GetTrust())
	if err != nil {
		return nil, connectError(errors.B.Op(op).Err(err).Build())
	}
	if err := handler.trustSetter.SetProjectSkillTrust(
		ctx,
		alfred.ProjectID(request.GetProjectId()),
		trust,
	); err != nil {
		return nil, connectError(err)
	}
	return &alfredv1.SetProjectSkillTrustResponse{}, nil
}

func decodeProjectSkillTrust(trust alfredv1.ProjectSkillTrust) (alfred.ProjectSkillTrust, error) {
	const op = "decodeProjectSkillTrust"

	switch trust {
	case alfredv1.ProjectSkillTrust_PROJECT_SKILL_TRUST_UNKNOWN:
		return alfred.ProjectSkillTrustUnknown, nil
	case alfredv1.ProjectSkillTrust_PROJECT_SKILL_TRUST_TRUSTED:
		return alfred.ProjectSkillTrustTrusted, nil
	case alfredv1.ProjectSkillTrust_PROJECT_SKILL_TRUST_DENIED:
		return alfred.ProjectSkillTrustDenied, nil
	default:
		return "", errors.B.
			Op(op).
			KindInvalidArgument().
			Textf("unknown project skill trust %d", trust).
			Build()
	}
}

func encodeProject(project alfred.Project) (*alfredv1.Project, error) {
	const op = "encodeProject"

	trust, err := encodeProjectSkillTrust(project.SkillTrust)
	if err != nil {
		return nil, errors.B.Op(op).Err(err).Build()
	}
	return &alfredv1.Project{
		Id:         string(project.ID),
		Name:       project.Name,
		Root:       project.Root,
		SkillTrust: trust,
		CreatedAt:  timestamppb.New(project.CreatedAt),
	}, nil
}

func encodeProjectSkillTrust(trust alfred.ProjectSkillTrust) (alfredv1.ProjectSkillTrust, error) {
	const op = "encodeProjectSkillTrust"

	switch trust {
	case alfred.ProjectSkillTrustUnknown:
		return alfredv1.ProjectSkillTrust_PROJECT_SKILL_TRUST_UNKNOWN, nil
	case alfred.ProjectSkillTrustTrusted:
		return alfredv1.ProjectSkillTrust_PROJECT_SKILL_TRUST_TRUSTED, nil
	case alfred.ProjectSkillTrustDenied:
		return alfredv1.ProjectSkillTrust_PROJECT_SKILL_TRUST_DENIED, nil
	default:
		return alfredv1.ProjectSkillTrust_PROJECT_SKILL_TRUST_UNSPECIFIED, errors.B.
			Op(op).
			KindInternal().
			Textf("unknown project skill trust %q", trust).
			Build()
	}
}

var _ alfredv1connect.ProjectServiceHandler = (*ProjectHandler)(nil)
