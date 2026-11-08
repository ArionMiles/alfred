package config

import (
	"os"
	"path/filepath"

	"github.com/ArionMiles/alfred/pkg/x/errors"
)

// LoadClient applies cli.toml from the resolved configuration directory to a
// caller-owned value initialized with defaults.
// An absent file preserves defaults. The caller owns validation and must discard
// the target on error.
func LoadClient(directory string, target any) error {
	const op = "LoadClient"

	path := filepath.Join(directory, "cli.toml")
	if err := DecodeFile(path, target); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.B.Op(op).Err(err).Build()
	}
	return nil
}
