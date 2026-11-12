// Package config loads the application's startup configuration.
package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/creasty/defaults"
	"github.com/go-viper/mapstructure/v2"
	"github.com/knadh/koanf/parsers/toml/v2"
	"github.com/knadh/koanf/providers/env/v2"
	"github.com/knadh/koanf/providers/rawbytes"
	"github.com/knadh/koanf/v2"

	"github.com/ArionMiles/alfred/pkg/daemon"
	"github.com/ArionMiles/alfred/pkg/providers"
	"github.com/ArionMiles/alfred/pkg/providers/openai"
	"github.com/ArionMiles/alfred/pkg/skills"
	"github.com/ArionMiles/alfred/pkg/storage/jsonl"
	"github.com/ArionMiles/alfred/pkg/storage/sqlite"
	"github.com/ArionMiles/alfred/pkg/tools"
	"github.com/ArionMiles/alfred/pkg/tui"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

// C composes startup settings. Only fields with public koanf tags are file settings.
// Composition supplies derived component paths when constructing dependencies.
type C struct {
	ConfigDir string                  `koanf:"-"`
	DataDir   string                  `koanf:"data_dir"`
	Daemon    daemon.Config           `koanf:",squash"`
	Tools     tools.Config            `koanf:"tools"`
	TUI       tui.Config              `koanf:"tui"`
	SQLite    sqlite.Config           `koanf:"-"`
	History   jsonl.Config            `koanf:"-"`
	Skills    skills.Config           `koanf:"-"`
	Catalog   providers.CatalogConfig `koanf:"-"`
	Providers ProviderConfig          `koanf:"providers"`
}

// ProviderConfig contains installed provider startup settings, not saved accounts.
type ProviderConfig struct {
	OpenAI OpenAIConfig `koanf:"openai"`
}

// OpenAIConfig exposes the supported OpenAI onboarding settings.
type OpenAIConfig struct {
	Authorization openai.AuthorizationConfig `koanf:"authorization"`
}

// Load applies defaults, config.toml and cli.toml, then explicit environment overrides.
// Relative server paths resolve beside config.toml. Neither files nor environment
// variables can expose internal constructor settings or credentials.
//
//nolint:gocognit // Keep source precedence and loading errors together at the application boundary.
func Load() (C, error) {
	const op = "Load"
	var settings C
	if err := defaults.Set(&settings); err != nil {
		return C{}, errors.B.Op(op).Text("initialize configuration defaults").Err(err).Build()
	}
	configHome, err := xdgHome("XDG_CONFIG_HOME", ".config")
	if err != nil {
		return C{}, errors.B.Op(op).Err(err).Build()
	}
	dataHome, err := xdgHome("XDG_DATA_HOME", filepath.Join(".local", "share"))
	if err != nil {
		return C{}, errors.B.Op(op).Err(err).Build()
	}
	settings.ConfigDir = filepath.Join(configHome, "alfred")
	settings.DataDir = filepath.Join(dataHome, "alfred")
	//nolint:forbidigo // Application configuration owns this explicit path override.
	path := os.Getenv("ALFRED_CONFIG_FILE")
	explicit := path != ""
	if !explicit {
		path = filepath.Join(settings.ConfigDir, "config.toml")
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return C{}, errors.B.Op(op).Text("resolve server configuration path").Err(err).Build()
	}

	k := koanf.New(".")
	// #nosec G703 -- Configuration paths are chosen by the local user.
	serverData, err := os.ReadFile(path)
	if err != nil && (explicit || !errors.Is(err, os.ErrNotExist)) {
		return C{}, errors.B.Op(op).Textf("read server configuration %q", path).Err(err).Build()
	}
	if err := k.Load(rawbytes.Provider(serverData), toml.Parser()); err != nil {
		return C{}, errors.B.Op(op).Textf("parse server configuration %q", path).Err(err).Build()
	}
	if k.Exists("tui") {
		return C{}, errors.B.Op(op).KindInvalidArgument().Textf("tui settings belong in cli.toml, not %q", path).Build()
	}

	clientPath := filepath.Join(settings.ConfigDir, "cli.toml")
	// #nosec G703 -- Configuration paths are chosen by the local user.
	clientData, err := os.ReadFile(clientPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return C{}, errors.B.Op(op).Textf("read client configuration %q", clientPath).Err(err).Build()
	}
	clientValues, err := toml.Parser().Unmarshal(clientData)
	if err != nil {
		return C{}, errors.B.Op(op).Textf("parse client configuration %q", clientPath).Err(err).Build()
	}
	if err := k.Set("tui", clientValues); err != nil {
		return C{}, errors.B.Op(op).Text("merge client configuration").Err(err).Build()
	}

	environment := map[string]string{
		"ALFRED_DATA_DIR":                  "data_dir",
		"ALFRED_LISTEN_ADDRESS":            "listen_address",
		"ALFRED_TOOLS_SHELL_RC_FILE":       "tools.shell.rc_file",
		"ALFRED_TOOLS_WEB_SEARCH_PROVIDER": "tools.web_search.provider",
		"ALFRED_TUI_KEYBINDINGS_SUBMIT":    "tui.keybindings.submit",
		"ALFRED_TUI_KEYBINDINGS_INTERRUPT": "tui.keybindings.interrupt",
		"ALFRED_TUI_KEYBINDINGS_PALETTE":   "tui.keybindings.palette",
	}
	if err := k.Load(env.Provider(".", env.Opt{
		Prefix: "ALFRED_",
		TransformFunc: func(name, value string) (string, any) {
			key := environment[name]
			if strings.HasPrefix(key, "tui.keybindings.") {
				return key, strings.Split(value, ",")
			}
			return key, value
		},
	}), nil); err != nil {
		return C{}, errors.B.Op(op).Text("load configuration environment overrides").Err(err).Build()
	}
	if err := k.UnmarshalWithConf("", &settings, koanf.UnmarshalConf{
		Tag: "koanf",
		DecoderConfig: &mapstructure.DecoderConfig{
			ErrorUnused: true, WeaklyTypedInput: false, ZeroFields: false,
			DecodeHook: mapstructure.StringToTimeDurationHookFunc(),
		},
	}); err != nil {
		return C{}, errors.B.Op(op).Textf("decode configuration from %q and %q", path, clientPath).Err(err).Build()
	}
	// Validate before path normalization can turn an empty data directory into a valid path.
	if err := settings.Valid(); err != nil {
		return C{}, errors.B.Op(op).Err(err).Build()
	}
	if !filepath.IsAbs(settings.DataDir) {
		settings.DataDir = filepath.Join(filepath.Dir(path), settings.DataDir)
	}
	settings.DataDir = filepath.Clean(settings.DataDir)
	if settings.Tools.Shell.RCFile != "" && !filepath.IsAbs(settings.Tools.Shell.RCFile) {
		settings.Tools.Shell.RCFile = filepath.Join(filepath.Dir(path), settings.Tools.Shell.RCFile)
	}
	return settings, nil
}

// Valid checks application settings and delegates component rules to their owners.
func (settings C) Valid() error {
	const op = "Valid"
	if strings.TrimSpace(settings.DataDir) == "" {
		return errors.B.Op(op).KindInvalidArgument().UserMsg("data_dir must not be blank").Build()
	}
	if err := settings.Daemon.Valid(); err != nil {
		return errors.B.Op(op).Text("validate daemon configuration").Err(err).Build()
	}
	if err := settings.TUI.Valid(); err != nil {
		return errors.B.Op(op).Text("validate client configuration").Err(err).Build()
	}
	return nil
}

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
