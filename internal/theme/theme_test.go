package theme

import (
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestResolveFollowsAppearance(t *testing.T) {
	s := DefaultSettings()
	s.Dark = "Catppuccin Mocha"
	if r := Resolve(s, ui.DarkTheme()); r.UI.Background != ui.Hex("#1e1e2e") || r.Code == nil {
		t.Errorf("dark system with Mocha chosen: background %v, code theme %v", r.UI.Background, r.Code)
	}
	if r := Resolve(s, ui.LightTheme()); r.UI.Dark || r.Code != nil {
		t.Errorf("light system shows the default light theme, got dark=%v code=%v", r.UI.Dark, r.Code)
	}
	s.Appearance = Dark
	if r := Resolve(s, ui.LightTheme()); !r.UI.Dark {
		t.Error("Appearance Dark ignored on a light system")
	}
}

func TestCatppuccinAccent(t *testing.T) {
	s := Settings{Appearance: Light, Light: "Catppuccin Latte", Accent: "Mauve"}
	if r := Resolve(s, ui.LightTheme()); r.UI.Accent != ui.Hex("#8839ef") || r.UI.AccentText != ui.Hex("#dce0e8") {
		t.Errorf("Latte Mauve: accent %v, text on it %v", r.UI.Accent, r.UI.AccentText)
	}
	// An accent name from an older version of the settings falls back to Blue.
	s.Accent = "Chartreuse"
	if r := Resolve(s, ui.LightTheme()); r.UI.Accent != ui.Hex("#1e66f5") {
		t.Errorf("unknown accent: %v", r.UI.Accent)
	}
}

// Every theme gives each kind its own grid color, apart from the text.
func TestTypeColorsDistinct(t *testing.T) {
	names := append(slices.Clone(LightThemes), DarkThemes...)
	for _, name := range names {
		s := Settings{Appearance: Light, Light: name, Accent: "Blue"}
		system := ui.LightTheme()
		if slices.Contains(DarkThemes, name) {
			s = Settings{Appearance: Dark, Dark: name, Accent: "Blue"}
			system = ui.DarkTheme()
		}
		r := Resolve(s, system)
		k := r.Types
		colors := []ui.Color{k.Integer, k.Numeric, k.Boolean, k.Temporal, k.Structured, k.Identifier, k.Binary, k.Spatial, r.UI.Text}
		for i := range colors {
			for j := i + 1; j < len(colors); j++ {
				if colors[i] == colors[j] {
					t.Errorf("%s: colors %d and %d are both %v", name, i, j, colors[i])
				}
			}
		}
	}
}
