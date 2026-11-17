package view

import (
	"strconv"

	"github.com/egoist/mygo/ui"

	"seeker/internal/app"
	"seeker/internal/icons"
	"seeker/internal/theme"
)

// Sections of the settings dialog, as the sidebar lists them.
const (
	sectionAppearance = "appearance"
	sectionShortcuts  = "shortcuts"
)

func (s *App) settingsDialog(c *ui.Context) {
	if !s.settingsOpen {
		s.stopRecording()
		return
	}
	if s.settingsSection == "" {
		s.settingsSection = sectionAppearance
	}
	t := c.Theme()
	// ui.Modal pads its panel and spaces its children; the sidebar here runs
	// edge to edge, so the panel gives both up and each part pads itself.
	modal := ui.Modal(c, &s.settingsOpen, func() {
		ui.Row(c).Width(780).Height(540).Children(func() {
			ui.Column(c).Width(200).FillHeight().Padding(16, 10).Background(t.Text.Alpha(0.04)).Children(func() {
				ui.Text(c, "Settings").FontSize(13).Bold().Padding(0, 10, 10, 10)
				ui.Sidebar(c, &s.settingsSection, func() {
					ui.SidebarItem(c, sectionAppearance, icons.Palette, "Appearance")
					ui.SidebarItem(c, sectionShortcuts, icons.Keyboard, "Shortcuts")
				}).FillWidth().Background(ui.RGBA(0, 0, 0, 0)).Padding(0)
			})
			ui.Column(c).Grow(1).FillHeight().Children(func() {
				ui.Scroll(c).Grow(1).FillWidth().Children(func() {
					ui.Column(c).FillWidth().Padding(24, 28).Gap(14).Children(func() {
						switch s.settingsSection {
						case sectionShortcuts:
							s.shortcutsPanel(c)
						default:
							s.stopRecording()
							s.appearancePanel(c)
						}
					})
				})
				ui.Divider(c)
				ui.Row(c).FillWidth().Padding(12, 20).Children(func() {
					ui.Spacer(c)
					if ui.Button(c, "Done").Clicked() {
						s.settingsOpen = false
					}
				})
			})
		})
	})
	modal.Padding(0).Gap(0).Clip()
}

func (s *App) appearancePanel(c *ui.Context) {
	t := c.Theme()
	u := s.core.UI()
	ui.Text(c, "Appearance").FontSize(16).Bold()
	field := func(label string, control func()) {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Text(c, label).Width(120)
			ui.Box(c).Grow(1).Children(control)
		})
	}
	field("Appearance", func() {
		ui.Segmented(c, &s.appearance, "System", "Light", "Dark").Label("Appearance").FillEqually()
	})
	field("Light theme", func() {
		ui.Select(c, &u.Theme.Light, theme.LightThemes).FillWidth().Label("Light theme")
	})
	field("Dark theme", func() {
		ui.Select(c, &u.Theme.Dark, theme.DarkThemes).FillWidth().Label("Dark theme")
	})
	field("Accent", func() {
		ui.Select(c, &u.Theme.Accent, theme.Accents).FillWidth().Label("Accent")
	})
	if !u.Theme.HasAccent(s.systemDark) {
		ui.Text(c, "Accent applies to Catppuccin themes; the default themes follow the system's accent.").
			FontSize(11).TextColor(t.TextMuted)
	}
	field("UI size", func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			u.Scale = app.SnapScale(u.Scale)
			if ui.Button(c.Key("smaller"), "−").Width(28).Disabled(u.Scale <= app.ScaleMin).Tooltip("Smaller interface").Clicked() {
				u.Scale = app.SnapScale(u.Scale - app.ScaleStep)
			}
			ui.Text(c, strconv.Itoa(u.Scale)+"%").Width(44).TextAlign(ui.Center)
			if ui.Button(c.Key("larger"), "+").Width(28).Disabled(u.Scale >= app.ScaleMax).Tooltip("Larger interface").Clicked() {
				u.Scale = app.SnapScale(u.Scale + app.ScaleStep)
			}
		})
	})
	field("Editor font", func() {
		ui.TextInput(c, &u.Font.Editor).FillWidth().Label("Editor font").Placeholder("System monospace")
	})
	field("Table font", func() {
		ui.TextInput(c, &u.Font.Table).FillWidth().Label("Table font").Placeholder(fallbackFamily)
	})
	field("Font size", func() {
		ui.NumberInput(c, &u.Font.Size, 8, 32, 1).Width(110).Label("Font size")
	})
	field("Font features", func() {
		ui.TextInput(c, &u.Font.Features).FillWidth().Label("Font features").Placeholder("ss01, calt, -liga")
	})
	ui.Text(c, "Fonts are the family names installed on this Mac. Features follow ghostty's font-feature: comma-separated tags, -tag turns one off.").
		FontSize(11).TextColor(t.TextMuted)
}

// stopRecording drops a chord being recorded and hands the keys back.
func (s *App) stopRecording() {
	s.recordID = ""
	s.core.RecordShortcuts(false)
}

// shortcutsPanel lists the commands by group with their chords. A click on a
// chord records the next one pressed (Esc leaves it, Backspace unbinds); the
// chords are paused meanwhile so the one pressed cannot also run.
func (s *App) shortcutsPanel(c *ui.Context) {
	t := c.Theme()
	ui.Text(c, "Shortcuts").FontSize(16).Bold()
	s.core.RecordShortcuts(s.recordID != "")
	if s.recordID != "" {
		mods := c.Modifiers()
		for _, k := range c.KeysDown() {
			switch {
			case mods == 0 && k == ui.KeyEscape:
				s.stopRecording()
			case mods == 0 && (k == ui.KeyBackspace || k == ui.KeyDelete):
				s.core.SetShortcut(s.recordID, app.Chord{})
				s.stopRecording()
			case app.Recordable(mods, k):
				s.core.SetShortcut(s.recordID, app.Chord{Mods: mods, Key: k})
				s.stopRecording()
			}
			if s.recordID == "" {
				break
			}
		}
	}
	group := ""
	for _, d := range app.Commands {
		if d.Group != group {
			group = d.Group
			ui.Text(c, group).FontSize(11).FontWeight(600).TextColor(t.TextMuted).Padding(6, 0, 0, 0)
		}
		ui.Row(c.Key(string(d.ID))).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, d.Label).Grow(1)
			if s.core.ShortcutChanged(d.ID) && ui.Button(c.Key("reset"), "Reset").Height(24).Clicked() {
				s.core.ResetShortcut(d.ID)
			}
			label := app.ChordText(s.core.Shortcut(d.ID))
			if s.recordID == d.ID {
				label = "Press keys…"
			}
			if ui.Button(c.Key("chord"), label).Width(150).Height(26).Label(d.Label + " shortcut").Clicked() {
				if s.recordID == d.ID {
					s.stopRecording()
				} else {
					s.recordID = d.ID
				}
			}
		})
	}
	ui.Row(c).FillWidth().Padding(8, 0, 0, 0).Children(func() {
		if ui.Button(c, "Reset all").Clicked() {
			s.stopRecording()
			s.core.ResetShortcuts()
		}
		ui.Spacer(c)
		ui.Text(c, "Click a shortcut, then press the new keys. Esc cancels, Backspace unbinds.").
			FontSize(11).TextColor(t.TextMuted)
	})
}
