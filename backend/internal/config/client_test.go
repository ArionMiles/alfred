package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ArionMiles/alfred/internal/config"
	"github.com/ArionMiles/alfred/pkg/tui"
)

func TestLoadClientUsesResolvedDirectoryAndPreservesDefaults(t *testing.T) {
	root := configEnvironment(t)
	directory := filepath.Join(root, "config", "alfred")
	got := tui.DefaultConfig()
	if err := config.LoadClient(directory, &got); err != nil || !reflect.DeepEqual(got, tui.DefaultConfig()) {
		t.Fatalf("missing client config = %+v, %v", got, err)
	}
	writeConfig(t, filepath.Join(root, "config", "alfred", "cli.toml"), "[keybindings]\ninterrupt = ['ctrl+x']\npalette = ['ctrl+o']\n")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := config.LoadClient(directory, &got); err != nil {
		t.Fatal(err)
	}
	want := tui.DefaultConfig()
	want.Keybindings.Interrupt = []string{"ctrl+x"}
	want.Keybindings.Palette = []string{"ctrl+o"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("client config = %+v, want %+v", got, want)
	}
}

func TestLoadClientRejectsInvalidFiles(t *testing.T) {
	for name, source := range map[string]string{
		"malformed":  "[keybindings",
		"unknown":    "[keybindings]\nsubmitt = ['enter']\n",
		"wrong type": "[keybindings]\nsubmit = 'enter'\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := configEnvironment(t)
			directory := filepath.Join(root, "config", "alfred")
			writeConfig(t, filepath.Join(root, "config", "alfred", "cli.toml"), source)
			got := tui.DefaultConfig()
			if err := config.LoadClient(directory, &got); err == nil {
				t.Fatal("invalid client file was accepted")
			}
		})
	}
}

func TestLoadClientDoesNotIgnoreReadErrors(t *testing.T) {
	root := configEnvironment(t)
	directory := filepath.Join(root, "config", "alfred")
	path := filepath.Join(root, "config", "alfred", "cli.toml")
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	got := tui.DefaultConfig()
	if err := config.LoadClient(directory, &got); err == nil {
		t.Fatal("unreadable client file was ignored")
	}
}
