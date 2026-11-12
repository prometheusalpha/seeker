package view

import (
	"github.com/egoist/mygo/ui"

	"seeker/internal/theme"
)

func (a *App) settingsDialog(c *ui.Context) {
	t := c.Theme()
	ui.Modal(c, &a.settingsOpen, func() {
		ui.Column(c).Width(420).Padding(20).Gap(14).Children(func() {
			ui.Text(c, "Settings").FontSize(16).Bold()
			field := func(label string, control func()) {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
					ui.Text(c, label).Width(120)
					ui.Box(c).Grow(1).Children(control)
				})
			}
			field("Appearance", func() {
				ui.Segmented(c, &a.appearance, "System", "Light", "Dark").Label("Appearance").FillEqually()
			})
			field("Light theme", func() {
				ui.Select(c, &a.settings.Light, theme.LightThemes).FillWidth().Label("Light theme")
			})
			field("Dark theme", func() {
				ui.Select(c, &a.settings.Dark, theme.DarkThemes).FillWidth().Label("Dark theme")
			})
			field("Accent", func() {
				ui.Select(c, &a.settings.Accent, theme.Accents).FillWidth().Label("Accent")
			})
			if !a.settings.HasAccent(a.systemDark) {
				ui.Text(c, "Accent applies to Catppuccin themes; the default themes follow the system's accent.").
					FontSize(11).TextColor(t.TextMuted)
			}
			ui.Row(c).FillWidth().Children(func() {
				ui.Spacer(c)
				if ui.Button(c, "Done").Clicked() {
					a.settingsOpen = false
				}
			})
		})
	})
}
