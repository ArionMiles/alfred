package config_test

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/ArionMiles/alfred/internal/config"
	"github.com/ArionMiles/alfred/pkg/daemon"
	"github.com/ArionMiles/alfred/pkg/tui"
)

func TestLoadResolvesSettings(t *testing.T) {
	root := configEnvironment(t)
	path := filepath.Join(root, "custom", "config.toml")
	writeConfig(t, path, "data_dir = '../state'\nlisten_address = '127.0.0.1:9000'\n")
	t.Setenv("ALFRED_CONFIG_FILE", path)
	got, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(root, "state")
	if got.DataDir != dataDir || got.ConfigDir != filepath.Join(root, "config", "alfred") {
		t.Fatalf("resolved configuration = %+v", got)
	}
	if got.Daemon.ReadHeaderTimeout != 5*time.Second || got.Daemon.ShutdownTimeout != 10*time.Second ||
		got.Daemon.StartupTimeout != 10*time.Second || got.SQLite.BusyTimeout != 5*time.Second ||
		got.History.LockShardCount != 64 || got.Catalog.MaxAge != 24*time.Hour ||
		got.Catalog.URL != "https://models.dev/api.json" || got.Skills.SystemRoot != "/etc/alfred/skills" ||
		got.Catalog.CacheFileName != "models.dev.json" || got.Catalog.RequestTimeout != 15*time.Second ||
		got.Tools.WebSearch.Exa.Endpoint != "https://api.exa.ai/search" || got.Tools.WebSearch.Exa.RequestTimeout != 30*time.Second ||
		got.Tools.WebFetch.RequestTimeout != 30*time.Second || got.Providers.OpenAI.Authorization.RequestTimeout != 30*time.Second ||
		got.Providers.OpenAI.Authorization.CallbackPath != "/auth/callback" || got.Providers.OpenAI.Authorization.TokenURL == "" {
		t.Fatalf("dependency defaults = %+v", got)
	}
}

func TestLoadEnvironmentOverridesFiles(t *testing.T) {
	root := configEnvironment(t)
	writeConfig(t, filepath.Join(root, "config", "alfred", "config.toml"),
		"data_dir = 'file-state'\nlisten_address = '127.0.0.1:9000'\n[tools.shell]\nrc_file = 'file.zsh'\n")
	writeConfig(t, filepath.Join(root, "config", "alfred", "cli.toml"), "[keybindings]\nsubmit = ['ctrl+e']\n")
	t.Setenv("ALFRED_DATA_DIR", "env-state")
	t.Setenv("ALFRED_LISTEN_ADDRESS", "0.0.0.0:9001")
	t.Setenv("ALFRED_TOOLS_SHELL_RC_FILE", "env.zsh")
	t.Setenv("ALFRED_TOOLS_WEB_SEARCH_PROVIDER", "custom-search")
	t.Setenv("ALFRED_TUI_KEYBINDINGS_SUBMIT", "enter,ctrl+s")
	got, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.DataDir != filepath.Join(root, "config", "alfred", "env-state") ||
		got.Daemon.ListenAddress != "0.0.0.0:9001" ||
		got.Tools.Shell.RCFile != filepath.Join(root, "config", "alfred", "env.zsh") ||
		got.Tools.WebSearch.Provider != "custom-search" ||
		!reflect.DeepEqual(got.TUI.Keybindings.Submit, []string{"enter", "ctrl+s"}) ||
		!reflect.DeepEqual(got.TUI.Keybindings.Interrupt, []string{"ctrl+c"}) {
		t.Fatalf("environment configuration = %+v", got)
	}
}

func TestLoadPreservesExplicitEmptyOverrides(t *testing.T) {
	root := configEnvironment(t)
	writeConfig(t, filepath.Join(root, "config", "alfred", "config.toml"), "[tools.shell]\nrc_file = 'file.zsh'\n")
	t.Setenv("ALFRED_TOOLS_SHELL_RC_FILE", "")
	got, err := config.Load()
	if err != nil || got.Tools.Shell.RCFile != "" {
		t.Fatalf("empty override = %+v, %v", got, err)
	}
	t.Setenv("ALFRED_DATA_DIR", "")
	if _, err := config.Load(); err == nil {
		t.Fatal("empty data directory override accepted")
	}
}

func TestLoadDoesNotExposeInternalSettings(t *testing.T) {
	root := configEnvironment(t)
	t.Setenv("ALFRED_SQLITE_BUSY_TIMEOUT", "99s")
	t.Setenv("ALFRED_OPENAI_AUTHORIZATION_TOKEN_URL", "http://unexpected.example")
	got, err := config.Load()
	if err != nil || got.SQLite.BusyTimeout != 5*time.Second || got.Providers.OpenAI.Authorization.TokenURL != "https://auth.openai.com/api/accounts/oauth/token" {
		t.Fatalf("internal environment setting exposed: %+v, %v", got, err)
	}
	for _, source := range []string{
		"[sqlite]\nbusy_timeout = '99s'\n", "[daemon]\nstartup_timeout = '99s'\n", "[openai_authorization]\ntoken_url = 'http://unexpected.example'\n",
		"[tools.web_search.exa]\nendpoint = 'http://unexpected.example'\n",
		"[tools.web_fetch]\nrequest_timeout = '1s'\n",
	} {
		writeConfig(t, filepath.Join(root, "config", "alfred", "config.toml"), source)
		if _, err := config.Load(); err == nil {
			t.Fatalf("internal file setting accepted: %s", source)
		}
	}
}

func TestConfigValid(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		dataDir   string
		listener  string
		submit    []string
		wantError bool
	}{
		{name: "valid", dataDir: "relative-state", listener: "127.0.0.1:0", submit: []string{"enter"}},
		{name: "empty data directory", listener: "127.0.0.1:0", submit: []string{"enter"}, wantError: true},
		{name: "blank data directory", dataDir: " \t", listener: "127.0.0.1:0", submit: []string{"enter"}, wantError: true},
		{name: "invalid daemon", dataDir: "state", listener: "invalid", submit: []string{"enter"}, wantError: true},
		{name: "invalid client", dataDir: "state", listener: "127.0.0.1:0", wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			settings := config.C{
				DataDir: test.dataDir,
				Daemon:  daemon.Config{ListenAddress: test.listener},
				TUI: tui.Config{Keybindings: tui.KeybindingsConfig{
					Submit: test.submit, Interrupt: []string{"ctrl+c"}, Palette: []string{"ctrl+p"},
				}},
			}
			if err := settings.Valid(); (err != nil) != test.wantError {
				t.Fatalf("Valid() = %v, wantError = %v", err, test.wantError)
			}
		})
	}
}

func TestLoadOpenAIAuthorizationTimeout(t *testing.T) {
	root := configEnvironment(t)
	path := filepath.Join(root, "config", "alfred", "config.toml")
	settings, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := settings.Providers.OpenAI.Authorization.AttemptTimeout; got != 10*time.Minute {
		t.Fatalf("default attempt timeout = %s", got)
	}
	writeConfig(t, path, "[providers.openai.authorization]\nattempt_timeout = '15m'\n")
	settings, err = config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := settings.Providers.OpenAI.Authorization.AttemptTimeout; got != 15*time.Minute {
		t.Fatalf("file attempt timeout = %s", got)
	}
	for _, contents := range []string{
		"[providers.openai.authorization]\nattempt_timeout = 'invalid'\n",
		"[providers.openai.authorization]\nattempt_timeout = true\n",
		"[providers.openai.authorization]\ntoken_url = 'http://unexpected.example'\n",
	} {
		writeConfig(t, path, contents)
		if _, err := config.Load(); err == nil {
			t.Fatalf("accepted invalid or private settings: %s", contents)
		}
	}
}
