package tui

import (
	"fmt"
	"strings"

	"github.com/ArionMiles/alfred/pkg/x/errors"
)

// Config contains the terminal client's file settings.
type Config struct {
	Keybindings KeybindingsConfig `koanf:"keybindings"`
}

// KeybindingsConfig maps client actions to terminal key names.
type KeybindingsConfig struct {
	Submit    []string `koanf:"submit"`
	Interrupt []string `koanf:"interrupt"`
	Palette   []string `koanf:"palette"`
}

// DefaultConfig supplies client settings when no file overrides are present.
func DefaultConfig() Config {
	return Config{Keybindings: DefaultKeybindingsConfig()}
}

// DefaultKeybindingsConfig returns a fresh set of the client's default keys.
func DefaultKeybindingsConfig() KeybindingsConfig {
	return KeybindingsConfig{
		Submit: []string{enterKey}, Interrupt: []string{"ctrl+c"}, Palette: []string{"ctrl+p"},
	}
}

// ValidateConfig checks action bindings before constructing the client.
func ValidateConfig(config Config) error {
	const op = "ValidateConfig"

	assigned := make(map[string]string)
	for action, keys := range map[string][]string{
		"submit": config.Keybindings.Submit, "interrupt": config.Keybindings.Interrupt,
		"palette": config.Keybindings.Palette,
	} {
		if len(keys) == 0 {
			return errors.B.Op(op).KindInvalidArgument().
				UserMsg(fmt.Sprintf("keybindings.%s must contain at least one key", action)).Build()
		}
		for _, key := range keys {
			if strings.TrimSpace(key) == "" {
				return errors.B.Op(op).KindInvalidArgument().
					UserMsg(fmt.Sprintf("keybindings.%s must not contain blank keys", action)).Build()
			}
			if previous, exists := assigned[key]; exists && previous != action {
				return errors.B.Op(op).KindInvalidArgument().
					UserMsg(fmt.Sprintf("key %q cannot be assigned to both %s and %s", key, previous, action)).Build()
			}
			assigned[key] = action
		}
	}
	return nil
}
