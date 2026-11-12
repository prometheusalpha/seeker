// Package theme resolves the user's theme settings into the colors the
// window, its islands and the SQL editor draw with.
package theme

import (
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

type Appearance string

const (
	System Appearance = "System"
	Light  Appearance = "Light"
	Dark   Appearance = "Dark"
)

// Settings pick a theme for each appearance, as Zed does, so following the
// system switches between two themes the user chose.
type Settings struct {
	Appearance Appearance
	Light      string
	Dark       string
	// Accent names a Catppuccin accent; the default themes take the
	// system's accent instead.
	Accent string
}

const (
	DefaultLight = "Default Light"
	DefaultDark  = "Default Dark"
)

func DefaultSettings() Settings {
	return Settings{Appearance: System, Light: DefaultLight, Dark: DefaultDark, Accent: "Blue"}
}

var (
	LightThemes = []string{DefaultLight, "Catppuccin Latte"}
	DarkThemes  = []string{DefaultDark, "Catppuccin Frappé", "Catppuccin Macchiato", "Catppuccin Mocha"}
)

// Islands are the colors of the window's canvas and the panels on it.
type Islands struct {
	Canvas, Island, Edge, Active ui.Color
}

type Resolved struct {
	UI      ui.Theme
	Islands Islands
	// Code is nil for the editor's own theme of UI.
	Code *editor.Theme
}

// IsDark reports which of the two themes s shows while the system is dark
// or not.
func (s Settings) IsDark(systemDark bool) bool {
	switch s.Appearance {
	case Light:
		return false
	case Dark:
		return true
	}
	return systemDark
}

// HasAccent reports whether the theme shown takes s.Accent.
func (s Settings) HasAccent(systemDark bool) bool {
	name := s.Light
	if s.IsDark(systemDark) {
		name = s.Dark
	}
	_, ok := flavors[name]
	return ok
}

// Resolve returns the theme s shows, system being the window's theme
// before the app sets one: it follows the system's appearance and
// carries the desktop's accent and contrast.
func Resolve(s Settings, system *ui.Theme) Resolved {
	dark := s.IsDark(system.Dark)
	name := s.Light
	if dark {
		name = s.Dark
	}
	if f, ok := flavors[name]; ok {
		return catppuccin(f, s.Accent)
	}
	base := *system
	if base.Dark != dark {
		if dark {
			base = *ui.DarkTheme()
		} else {
			base = *ui.LightTheme()
		}
	}
	isl := Islands{
		Canvas: ui.Hex("#e7e8eb"),
		Island: ui.Hex("#ffffff"),
		Edge:   ui.RGBA(0, 0, 0, 0.07),
		Active: ui.Hex("#eceef1"),
	}
	if dark {
		isl = Islands{
			Canvas: ui.Hex("#131416"),
			Island: ui.Hex("#1e1f22"),
			Edge:   ui.RGBA(255, 255, 255, 0.06),
			Active: ui.Hex("#2b2d31"),
		}
	}
	return Resolved{UI: base, Islands: isl}
}
