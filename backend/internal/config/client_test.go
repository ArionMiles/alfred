package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ArionMiles/alfred/internal/config"
	"github.com/ArionMiles/alfred/pkg/tui"
)

func TestLoadClientDefaultsAndOverrides(t *testing.T) {
	root := configEnvironment(t)
	got, err := config.Load()
	want := tui.KeybindingsConfig{Submit: []string{"enter"}, Interrupt: []string{"ctrl+c"}, Palette: []string{"ctrl+p"}}
	if err != nil || !reflect.DeepEqual(got.TUI.Keybindings, want) {
		t.Fatalf("missing client config = %+v, %v", got, err)
	}
	writeConfig(t, filepath.Join(root, "config", "alfred", "cli.toml"), "[keybindings]\ninterrupt = ['ctrl+x']\npalette = ['ctrl+o']\n")
	// Selecting another server file does not relocate client settings.
	serverPath := filepath.Join(root, "custom", "config.toml")
	writeConfig(t, serverPath, "")
	t.Setenv("ALFRED_CONFIG_FILE", serverPath)
	got, err = config.Load()
	want.Interrupt = []string{"ctrl+x"}
	want.Palette = []string{"ctrl+o"}
	if err != nil || !reflect.DeepEqual(got.TUI.Keybindings, want) {
		t.Fatalf("client config = %+v, %v; want %+v", got, err, want)
	}
}

func TestLoadClientRejectsInvalidFiles(t *testing.T) {
	for name, source := range map[string]string{
		"malformed":       "[keybindings",
		"unknown":         "[keybindings]\nsubmitt = ['enter']\n",
		"wrong type":      "[keybindings]\nsubmit = 'enter'\n",
		"wrong array":     "[keybindings]\nsubmit = [1, 2]\n",
		"empty":           "[keybindings]\nsubmit = []\n",
		"blank":           "[keybindings]\nsubmit = [' ']\n",
		"overlap":         "[keybindings]\nsubmit = ['ctrl+c']\n",
		"server settings": "data_dir = '/unexpected'\n",
		"nested client":   "[tui.keybindings]\nsubmit = ['enter']\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := configEnvironment(t)
			writeConfig(t, filepath.Join(root, "config", "alfred", "cli.toml"), source)
			if _, err := config.Load(); err == nil {
				t.Fatal("invalid client file was accepted")
			}
		})
	}
}

func TestLoadClientDoesNotIgnoreReadErrors(t *testing.T) {
	root := configEnvironment(t)
	path := filepath.Join(root, "config", "alfred", "cli.toml")
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(); err == nil {
		t.Fatal("unreadable client file was ignored")
	}
}
