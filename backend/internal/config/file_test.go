package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ArionMiles/alfred/internal/config"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

type settings struct {
	Name    string   `koanf:"name"`
	Enabled bool     `koanf:"enabled"`
	Keys    []string `koanf:"keys"`
}

func TestDecodeFileAppliesExplicitValuesAndPreservesOmittedDefaults(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "settings.toml")
	if err := os.WriteFile(path, []byte("enabled = false\nkeys = ['ctrl+x']\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := settings{Name: "default", Enabled: true, Keys: []string{"ctrl+c", "esc"}}
	if err := config.DecodeFile(path, &got); err != nil {
		t.Fatal(err)
	}
	want := settings{Name: "default", Enabled: false, Keys: []string{"ctrl+x"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("decoded settings = %+v, want %+v", got, want)
	}
}

func TestDecodeFileRejectsInvalidFiles(t *testing.T) {
	t.Parallel()
	for name, source := range map[string]string{
		"syntax":       "[broken",
		"unknown key":  "enabled = true\ntypo = 'value'\n",
		"wrong type":   "enabled = 'true'\n",
		"wrong array":  "keys = [1, 2]\n",
		"scalar array": "keys = 'enter'\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "settings.toml")
			if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
				t.Fatal(err)
			}
			var target settings
			if err := config.DecodeFile(path, &target); err == nil {
				t.Fatal("invalid file was accepted")
			}
		})
	}
}

func TestDecodeFilePreservesMissingFileError(t *testing.T) {
	t.Parallel()
	var target settings
	err := config.DecodeFile(filepath.Join(t.TempDir(), "missing.toml"), &target)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing-file error = %v", err)
	}
}
