// Package skills discovers and loads filesystem-backed Agent Skills.
package skills

import (
	"bytes"
	"strings"

	"go.yaml.in/yaml/v4"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

type frontmatter struct {
	Name          alfred.SkillName  `yaml:"name"`
	Description   string            `yaml:"description"`
	License       string            `yaml:"license"`
	Compatibility string            `yaml:"compatibility"`
	Metadata      map[string]string `yaml:"metadata"`
	AllowedTools  string            `yaml:"allowed-tools"`
}

func parseSkillFile(location string, data []byte) (alfred.SkillDefinition, string, error) {
	const op = "parseSkillFile"

	frontmatterData, body, ok := splitSkillFile(data)
	if !ok {
		return alfred.SkillDefinition{}, "", errors.B.
			Op(op).
			KindInvalidInput().
			Textf("skill file %q does not contain delimited YAML frontmatter", location).
			UserMsg("the skill file is missing valid YAML frontmatter").
			Build()
	}

	var metadata frontmatter
	if err := yaml.Unmarshal(frontmatterData, &metadata); err != nil {
		return alfred.SkillDefinition{}, "", errors.B.
			Op(op).
			KindInvalidInput().
			Textf("parse frontmatter from skill file %q", location).
			UserMsg("the skill file contains invalid YAML frontmatter").
			Err(err).
			Build()
	}
	if metadata.Name == "" {
		return alfred.SkillDefinition{}, "", errors.B.
			Op(op).
			KindInvalidInput().
			Textf("skill file %q has no name", location).
			UserMsg("the skill file is missing a name").
			Build()
	}
	if strings.TrimSpace(metadata.Description) == "" {
		return alfred.SkillDefinition{}, "", errors.B.
			Op(op).
			KindInvalidInput().
			Textf("skill file %q has no description", location).
			UserMsg("the skill file is missing a description").
			Build()
	}

	definition := alfred.SkillDefinition{
		Name:          metadata.Name,
		Description:   metadata.Description,
		Location:      location,
		License:       metadata.License,
		Compatibility: metadata.Compatibility,
		Metadata:      metadata.Metadata,
		AllowedTools:  metadata.AllowedTools,
	}

	return definition, strings.TrimSpace(string(body)), nil
}

func splitSkillFile(data []byte) ([]byte, []byte, bool) {
	line, remaining, ok := bytes.Cut(data, []byte("\n"))
	if !ok || string(bytes.TrimSuffix(line, []byte("\r"))) != "---" {
		return nil, nil, false
	}

	frontmatterStart := len(data) - len(remaining)
	for len(remaining) > 0 {
		line, next, foundNewline := bytes.Cut(remaining, []byte("\n"))
		if string(bytes.TrimSuffix(line, []byte("\r"))) == "---" {
			frontmatterEnd := len(data) - len(remaining)
			if foundNewline {
				return data[frontmatterStart:frontmatterEnd], next, true
			}
			return data[frontmatterStart:frontmatterEnd], nil, true
		}
		if !foundNewline {
			break
		}
		remaining = next
	}

	return nil, nil, false
}
