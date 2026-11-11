package skills

import (
	"cmp"
	"context"
	"fmt"
	"io/fs"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

// Resolver discovers filesystem-backed Agent Skills for a project.
type Resolver struct {
	projects   alfred.ProjectStore
	userHome   string
	configHome string
	systemRoot string
}

// Config contains runtime configuration for Resolver.
type Config struct {
	UserHome   string
	ConfigHome string
	SystemRoot string `default:"/etc/alfred/skills"`
}

type skillSet struct {
	projectID alfred.ProjectID
	catalog   alfred.SkillCatalog
	byName    map[alfred.SkillName]alfred.SkillDefinition
}

// NewResolver creates a filesystem-backed skill resolver.
func NewResolver(config Config, projects alfred.ProjectStore) *Resolver {
	return &Resolver{
		projects:   projects,
		userHome:   config.UserHome,
		configHome: config.ConfigHome,
		systemRoot: config.SystemRoot,
	}
}

// DiscoverSkills creates the effective skill snapshot for one run.
//
//nolint:gocognit // Discovery keeps precedence, diagnostics, and shadowing in one ordered traversal.
func (resolver *Resolver) DiscoverSkills(
	ctx context.Context,
	projectID alfred.ProjectID,
) (alfred.SkillSet, error) {
	const op = "DiscoverSkills"

	project, err := resolver.projects.GetProject(ctx, projectID)
	if err != nil {
		return nil, errors.B.
			Op(op).
			Textf("get project %q", projectID).
			Err(err).
			Build()
	}

	discovery := &skillSet{
		projectID: projectID,
		byName:    make(map[alfred.SkillName]alfred.SkillDefinition),
	}
	for _, root := range resolver.skillRoots(project) {
		entries, err := os.ReadDir(root)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, errors.B.
				Op(op).
				Textf("read skill root %q", root).
				UserMsg("Alfred could not read a skill directory").
				Err(err).
				Build()
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}

			location := filepath.Join(root, entry.Name(), "SKILL.md")
			data, err := os.ReadFile(location)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, errors.B.
					Op(op).
					Textf("read skill file %q", location).
					UserMsg("Alfred could not read a skill file").
					Err(err).
					Build()
			}

			definition, _, err := parseSkillFile(location, data)
			if err != nil {
				discovery.catalog.Diagnostics = append(
					discovery.catalog.Diagnostics,
					alfred.SkillDiagnostic{
						Kind:     alfred.SkillDiagnosticInvalid,
						Location: location,
						Message:  errors.UserMsg(err),
					},
				)
				continue
			}

			if existing, found := discovery.byName[definition.Name]; found {
				discovery.catalog.Diagnostics = append(
					discovery.catalog.Diagnostics,
					alfred.SkillDiagnostic{
						Kind:     alfred.SkillDiagnosticShadowed,
						Location: location,
						Message: fmt.Sprintf(
							"skill %q is shadowed by %q",
							definition.Name,
							existing.Location,
						),
					},
				)
				continue
			}

			discovery.byName[definition.Name] = definition
			discovery.catalog.Skills = append(discovery.catalog.Skills, definition)
		}
	}

	slices.SortFunc(discovery.catalog.Skills, func(a, b alfred.SkillDefinition) int {
		return cmp.Compare(a.Name, b.Name)
	})

	return discovery, nil
}

// Catalog returns the skill metadata and discovery diagnostics for this set.
func (set *skillSet) Catalog() alfred.SkillCatalog {
	return set.catalog
}

// Resolve resolves a skill from this set's discovery snapshot.
func (set *skillSet) Resolve(name alfred.SkillName) (alfred.Skill, error) {
	const op = "Resolve"

	if definition, found := set.byName[name]; found {
		return &fileSkill{definition: definition}, nil
	}

	return nil, errors.B.
		Op(op).
		KindNotFound().
		Textf("skill %q is not available in project %q", name, set.projectID).
		UserMsg("the requested skill is not available").
		Build()
}

func (resolver *Resolver) skillRoots(project alfred.Project) []string {
	roots := make([]string, 0, 6)
	projectSkillsTrusted := project.SkillTrust == alfred.ProjectSkillTrustTrusted
	if projectSkillsTrusted {
		roots = append(roots, filepath.Join(project.Root, ".claude", "skills"))
	}
	roots = append(roots, filepath.Join(resolver.userHome, ".claude", "skills"))
	if projectSkillsTrusted {
		roots = append(roots, filepath.Join(project.Root, ".agents", "skills"))
	}
	return append(
		roots,
		filepath.Join(resolver.userHome, ".agents", "skills"),
		filepath.Join(resolver.configHome, "alfred", "skills"),
		resolver.systemRoot,
	)
}

type fileSkill struct {
	definition alfred.SkillDefinition
}

func (skill *fileSkill) Definition() alfred.SkillDefinition {
	return skill.definition
}

func (skill *fileSkill) Load(ctx context.Context) (alfred.SkillContent, error) {
	const op = "Load"

	data, err := os.ReadFile(skill.definition.Location)
	if err != nil {
		return alfred.SkillContent{}, errors.B.
			Op(op).
			Textf("read skill file %q", skill.definition.Location).
			UserMsg("Alfred could not read the requested skill").
			Err(err).
			Build()
	}
	_, instructions, err := parseSkillFile(skill.definition.Location, data)
	if err != nil {
		return alfred.SkillContent{}, errors.B.
			Op(op).
			Textf("parse skill file %q", skill.definition.Location).
			Err(err).
			Build()
	}

	resources, err := loadResources(ctx, filepath.Dir(skill.definition.Location))
	if err != nil {
		return alfred.SkillContent{}, errors.B.
			Op(op).
			Textf("list resources for skill %q", skill.definition.Name).
			Err(err).
			Build()
	}

	return alfred.SkillContent{
		Instructions: instructions,
		Resources:    resources,
	}, nil
}

func loadResources(ctx context.Context, skillRoot string) ([]alfred.ResourceRef, error) {
	const op = "loadResources"

	resources := make([]alfred.ResourceRef, 0)
	err := filepath.WalkDir(skillRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() || !entry.Type().IsRegular() || path == filepath.Join(skillRoot, "SKILL.md") {
			return nil
		}

		name, err := filepath.Rel(skillRoot, path)
		if err != nil {
			return err
		}
		resources = append(resources, alfred.ResourceRef{
			URI:       (&url.URL{Scheme: "file", Path: path}).String(),
			MediaType: mime.TypeByExtension(strings.ToLower(filepath.Ext(path))),
			Name:      filepath.ToSlash(name),
		})
		return nil
	})
	if err != nil {
		return nil, errors.B.
			Op(op).
			Textf("walk skill root %q", skillRoot).
			UserMsg("Alfred could not inspect the requested skill's resources").
			Err(err).
			Build()
	}

	return resources, nil
}

var (
	_ alfred.SkillResolver = (*Resolver)(nil)
	_ alfred.SkillSet      = (*skillSet)(nil)
	_ alfred.Skill         = (*fileSkill)(nil)
)
