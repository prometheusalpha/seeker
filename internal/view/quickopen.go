package view

import (
	"github.com/egoist/mygo/ui"

	"seeker/internal/app"
	"seeker/internal/icons"
)

const quickRowH = 32

// quickKeys opens the palette on ⌘P, from anywhere in the window.
func (s *App) quickKeys(c *ui.Context) {
	if c.Shortcut(ui.Cmd, ui.KeyP) {
		s.core.OpenQuickOpen()
	}
}

// quickOpen is the palette: one field over the hits, the chosen one opened
// by Enter or a click.
func (s *App) quickOpen(c *ui.Context) {
	t := c.Theme()
	q := s.core.QuickState()
	modal := ui.Modal(c, &q.Open, func() {
		ui.Column(c).Width(580).Gap(10).Children(func() {
			in := ui.TextInput(c.Key("quick-query"), &q.Query).Placeholder("Search tables, files, connections").Label("Search").FillWidth()
			if q.Focus {
				q.Focus = false
				in.Focus()
			}
			if in.Submitted() {
				s.core.QuickActivate()
			}
			if c.Shortcut(0, ui.KeyDown) {
				s.core.QuickMove(1)
			}
			if c.Shortcut(0, ui.KeyUp) {
				s.core.QuickMove(-1)
			}

			res := s.core.QuickResults()
			loading := s.core.QuickLoading()
			switch {
			case q.Query == "":
				quickNote(c, "Type to find a table, a file or a connection.")
			case len(res) == 0 && loading:
				quickNote(c, "Looking through the tables…")
			case len(res) == 0:
				quickNote(c, "Nothing matches.")
			default:
				ui.Scroll(c.Key("quick-list")).FillWidth().MaxHeight(quickRowH * 11).Children(func() {
					ui.Column(c).FillWidth().Children(func() {
						for i, it := range res {
							s.quickRow(c, i, it)
						}
					})
				})
			}
			if loading && len(res) > 0 {
				ui.Text(c, "Still listing tables…").FontSize(11).TextColor(t.TextMuted)
			}
		})
	})
	if modal.Dismissed() {
		s.core.CloseQuickOpen()
	}
}

func quickNote(c *ui.Context, msg string) {
	ui.Box(c).FillWidth().Height(quickRowH * 2).Center().Children(func() {
		ui.Text(c, msg).FontSize(12).TextColor(c.Theme().TextMuted)
	})
}

func (s *App) quickRow(c *ui.Context, i int, it app.QuickItem) {
	t := c.Theme()
	q := s.core.QuickState()
	chosen := i == q.Row
	row := ui.ButtonBase(c.Key("quick-"+it.Key)).FillWidth().Height(quickRowH).Padding(0, 10).Gap(8).Radius(8).AlignItems(ui.Center)
	icon, muted := ink(t, chosen)
	text := t.Text
	if chosen {
		row.Background(t.Accent)
		text = t.AccentText
		row.ScrollIntoView()
	} else if row.Hovered() {
		row.Background(t.SurfaceHover)
	}
	row.Children(func() {
		ico := icons.Table
		switch it.Kind {
		case app.QuickFile:
			ico = icons.FileCode
		case app.QuickConnection:
			ico = icons.Database
		}
		ui.Icon(c, ico).FontSize(14).TextColor(icon)
		ui.Text(c, it.Label).FontSize(13).TextColor(text).SingleLine()
		ui.Spacer(c)
		detail := it.Detail
		if it.Type != "" && it.Type != "table" {
			detail += " · " + it.Type
		}
		ui.Text(c, detail).FontSize(11).TextColor(muted).SingleLine()
	})
	if row.Clicked() {
		s.core.QuickChoose(i)
		s.core.QuickActivate()
	}
}
