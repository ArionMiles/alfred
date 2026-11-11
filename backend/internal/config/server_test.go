package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ArionMiles/alfred/internal/config"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

func TestLoadServerDefaultsWithoutAFile(t *testing.T) {
	root := configEnvironment(t)
	got, err := config.Load()
	if err != nil || got.DataDir != filepath.Join(root, "data", "alfred") ||
		got.ConfigDir != filepath.Join(root, "config", "alfred") || got.Daemon.ListenAddress != "127.0.0.1:0" {
		t.Fatalf("server defaults = %+v, %v", got, err)
	}
}

func TestLoadServerListener(t *testing.T) {
	root := configEnvironment(t)
	path := filepath.Join(root, "config", "alfred", "config.toml")
	writeConfig(t, path, "listen_address = '0.0.0.0:8080'\n")
	settings, err := config.Load()
	if err != nil || settings.Daemon.ListenAddress != "0.0.0.0:8080" {
		t.Fatalf("settings=%+v error=%v", settings, err)
	}
	writeConfig(t, path, "listen_address = 'not-an-address'\n")
	if _, err := config.Load(); err == nil {
		t.Fatal("invalid listen_address accepted")
	}
}

func TestLoadServerToolSettings(t *testing.T) {
	root := configEnvironment(t)
	path := filepath.Join(root, "custom", "config.toml")
	t.Setenv("ALFRED_CONFIG_FILE", path)
	writeConfig(t, path, "[tools.shell]\nrc_file = 'agent.zsh'\n[tools.web_search]\nprovider = 'exa'\n")
	settings, err := config.Load()
	if err != nil || settings.Tools.Shell.RCFile != filepath.Join(root, "custom", "agent.zsh") || settings.Tools.WebSearch.Provider != "exa" {
		t.Fatalf("settings=%+v error=%v", settings, err)
	}
	absolute := filepath.Join(root, "absolute.zsh")
	writeConfig(t, path, "[tools.shell]\nrc_file = '"+absolute+"'\n")
	settings, err = config.Load()
	if err != nil || settings.Tools.Shell.RCFile != absolute || settings.Tools.WebSearch.Provider != "exa" {
		t.Fatalf("settings=%+v error=%v", settings, err)
	}
}

func TestLoadServerReadsDefaultAndExplicitFiles(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		name := "default"
		if explicit {
			name = "explicit"
		}
		t.Run(name, func(t *testing.T) {
			root := configEnvironment(t)
			path := filepath.Join(root, "config", "alfred", "config.toml")
			if explicit {
				path = filepath.Join(root, "custom", "settings.toml")
				t.Setenv("ALFRED_CONFIG_FILE", path)
				writeConfig(t, filepath.Join(root, "config", "alfred", "config.toml"), "[broken")
			}
			writeConfig(t, path, "data_dir = '../state'\n")
			got, err := config.Load()
			want := filepath.Clean(filepath.Join(filepath.Dir(path), "..", "state"))
			if err != nil || got.DataDir != want || got.ConfigDir != filepath.Join(root, "config", "alfred") {
				t.Fatalf("server config = %+v, %v; want %q", got, err, want)
			}
		})
	}
}

func TestLoadServerAcceptsAbsoluteDataDirectory(t *testing.T) {
	root := configEnvironment(t)
	want := filepath.Join(root, "state")
	writeConfig(t, filepath.Join(root, "config", "alfred", "config.toml"), "data_dir = '"+want+"'\n")
	got, err := config.Load()
	if err != nil || got.DataDir != want {
		t.Fatalf("server config = %+v, %v", got, err)
	}
}

func TestLoadServerRejectsMissingExplicitFile(t *testing.T) {
	root := configEnvironment(t)
	t.Setenv("ALFRED_CONFIG_FILE", filepath.Join(root, "missing.toml"))
	got, err := config.Load()
	if !errors.Is(err, os.ErrNotExist) || !reflect.DeepEqual(got, config.C{}) {
		t.Fatalf("missing explicit config = %+v, %v", got, err)
	}
}

func TestLoadServerRejectsInvalidDefaultFiles(t *testing.T) {
	for name, source := range map[string]string{
		"malformed":            "[broken",
		"unknown":              "data_directory = 'somewhere'\n",
		"config dir":           "config_dir = '/another/directory'\n",
		"wrong type":           "data_dir = 42\n",
		"blank":                "data_dir = ' '\n",
		"client settings":      "[tui.keybindings]\nsubmit = ['enter']\n",
		"empty client section": "[tui]\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := configEnvironment(t)
			writeConfig(t, filepath.Join(root, "config", "alfred", "config.toml"), source)
			got, err := config.Load()
			if err == nil || !reflect.DeepEqual(got, config.C{}) {
				t.Fatalf("invalid server config = %+v, %v", got, err)
			}
		})
	}
}

func TestXDGHomeFallbacks(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	defaults, err := config.Load()
	if err != nil || defaults.DataDir != filepath.Join(home, ".local", "share", "alfred") ||
		defaults.ConfigDir != filepath.Join(home, ".config", "alfred") {
		t.Fatalf("default directories = %+v, %v", defaults, err)
	}
}

func configEnvironment(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	t.Setenv("ALFRED_CONFIG_FILE", "")
	for _, name := range []string{"ALFRED_DATA_DIR", "ALFRED_LISTEN_ADDRESS", "ALFRED_TOOLS_SHELL_RC_FILE", "ALFRED_TOOLS_WEB_SEARCH_PROVIDER", "ALFRED_TUI_KEYBINDINGS_SUBMIT", "ALFRED_TUI_KEYBINDINGS_INTERRUPT", "ALFRED_TUI_KEYBINDINGS_PALETTE"} {
		t.Setenv(name, "unused")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func writeConfig(t *testing.T, path, source string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
}
