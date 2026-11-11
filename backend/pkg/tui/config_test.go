package tui

import "testing"

func TestConfigValidRejectsInvalidBindings(t *testing.T) {
	for name, keys := range map[string]KeybindingsConfig{
		"empty submit":    {Interrupt: []string{"ctrl+c"}, Palette: []string{"ctrl+p"}},
		"empty interrupt": {Submit: []string{"enter"}, Palette: []string{"ctrl+p"}},
		"empty palette":   {Submit: []string{"enter"}, Interrupt: []string{"ctrl+c"}},
		"blank submit":    {Submit: []string{" "}, Interrupt: []string{"ctrl+c"}, Palette: []string{"ctrl+p"}},
		"blank interrupt": {Submit: []string{"enter"}, Interrupt: []string{""}, Palette: []string{"ctrl+p"}},
		"blank palette":   {Submit: []string{"enter"}, Interrupt: []string{"ctrl+c"}, Palette: []string{" "}},
		"overlap":         {Submit: []string{"enter"}, Interrupt: []string{"enter"}, Palette: []string{"ctrl+p"}},
		"palette overlap": {Submit: []string{"enter"}, Interrupt: []string{"ctrl+p"}, Palette: []string{"ctrl+p"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := (Config{Keybindings: keys}).Valid(); err == nil {
				t.Fatal("invalid keybindings were accepted")
			}
		})
	}
}
