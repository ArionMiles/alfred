// Package config decodes TOML files into caller-owned configuration types.
package config

import (
	"os"

	"github.com/go-viper/mapstructure/v2"
	"github.com/knadh/koanf/parsers/toml/v2"
	"github.com/knadh/koanf/providers/rawbytes"
	"github.com/knadh/koanf/v2"

	"github.com/ArionMiles/alfred/pkg/x/errors"
)

// DecodeFile applies file values to target, a pointer initialized with defaults.
// Callers own missing-file handling and validation, and must discard target on error.
func DecodeFile(path string, target any) error {
	const op = "DecodeFile"

	// #nosec G703 -- Configuration paths are chosen by the local user, not restricted to a directory.
	data, err := os.ReadFile(path)
	if err != nil {
		return errors.B.Op(op).Textf("read configuration file %q", path).Err(err).Build()
	}
	k := koanf.New(".")
	if err := k.Load(rawbytes.Provider(data), toml.Parser()); err != nil {
		return errors.B.Op(op).Textf("parse configuration file %q", path).Err(err).Build()
	}
	if err := k.UnmarshalWithConf("", target, koanf.UnmarshalConf{
		Tag: "koanf",
		DecoderConfig: &mapstructure.DecoderConfig{
			ErrorUnused:      true,
			WeaklyTypedInput: false,
			ZeroFields:       false,
		},
	}); err != nil {
		return errors.B.Op(op).Textf("decode configuration file %q", path).Err(err).Build()
	}
	return nil
}
