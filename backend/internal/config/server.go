package config

import (
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/ArionMiles/alfred/pkg/tools"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

// Server contains settings shared by the service and its locating clients.
type Server struct {
	// ConfigDir is resolved before loading TOML and cannot be overridden by it.
	ConfigDir     string       `koanf:"-"`
	DataDir       string       `koanf:"data_dir"`
	ListenAddress string       `koanf:"listen_address"`
	Tools         tools.Config `koanf:"tools"`
}

// DefaultServer resolves the standard XDG configuration and data locations once.
func DefaultServer() (Server, error) {
	const op = "DefaultServer"

	configHome, err := xdgHome("XDG_CONFIG_HOME", ".config")
	if err != nil {
		return Server{}, errors.B.Op(op).Err(err).Build()
	}
	dataHome, err := xdgHome("XDG_DATA_HOME", filepath.Join(".local", "share"))
	if err != nil {
		return Server{}, errors.B.Op(op).Err(err).Build()
	}
	return Server{
		ListenAddress: "127.0.0.1:0",
		Tools:         tools.DefaultConfig(),
		ConfigDir:     filepath.Join(configHome, "alfred"),
		DataDir:       filepath.Join(dataHome, "alfred"),
	}, nil
}

// LoadServer loads ALFRED_CONFIG_FILE or the optional XDG config.toml.
// Relative data paths are anchored to the configuration file's directory.
func LoadServer() (Server, error) {
	const op = "LoadServer"

	settings, err := DefaultServer()
	if err != nil {
		return Server{}, errors.B.Op(op).Err(err).Build()
	}
	//nolint:forbidigo // The server configuration path is an explicitly supported override.
	path := os.Getenv("ALFRED_CONFIG_FILE")
	explicit := path != ""
	if !explicit {
		path = filepath.Join(settings.ConfigDir, "config.toml")
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return Server{}, errors.B.Op(op).Text("resolve server configuration path").Err(err).Build()
	}
	if err := DecodeFile(path, &settings); err != nil && (explicit || !errors.Is(err, os.ErrNotExist)) {
		return Server{}, errors.B.Op(op).Err(err).Build()
	}
	if strings.TrimSpace(settings.DataDir) == "" {
		return Server{}, errors.B.Op(op).KindInvalidArgument().UserMsg("data_dir must not be blank").Build()
	}
	if !filepath.IsAbs(settings.DataDir) {
		settings.DataDir = filepath.Join(filepath.Dir(path), settings.DataDir)
	}
	settings.DataDir = filepath.Clean(settings.DataDir)
	host, _, err := net.SplitHostPort(settings.ListenAddress)
	ip := net.ParseIP(host)
	if err != nil || (host != "localhost" && (ip == nil || (!ip.IsLoopback() && !ip.IsUnspecified()))) {
		return Server{}, errors.B.Op(op).KindInvalidArgument().
			UserMsg("listen_address must use a loopback or wildcard host and a port").Build()
	}
	if settings.Tools.Shell.RCFile != "" && !filepath.IsAbs(settings.Tools.Shell.RCFile) {
		settings.Tools.Shell.RCFile = filepath.Join(filepath.Dir(path), settings.Tools.Shell.RCFile)
	}
	return settings, nil
}
