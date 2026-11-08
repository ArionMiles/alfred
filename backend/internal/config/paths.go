package config

import (
	"os"
	"path/filepath"

	"github.com/ArionMiles/alfred/pkg/x/errors"
)

func xdgHome(variable, fallback string) (string, error) {
	const op = "xdgHome"

	//nolint:forbidigo // Only the explicitly supported XDG variables are read here.
	if directory := os.Getenv(variable); directory != "" {
		if !filepath.IsAbs(directory) {
			return "", errors.B.Op(op).KindInvalidArgument().Textf("%s must be an absolute path", variable).Build()
		}
		return filepath.Clean(directory), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.B.Op(op).Text("locate user home directory").Err(err).Build()
	}
	return filepath.Join(home, fallback), nil
}
