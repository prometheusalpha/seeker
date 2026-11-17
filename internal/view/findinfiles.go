package view

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"

	"seeker/internal/files"
	"seeker/internal/icons"
)

// findKeys opens the window on ⌘⇧F, from anywhere in the window.
func (s *App) findKeys(c *ui.Context) {
	if c.Shortcut(ui.Cmd|ui.Shift, ui.KeyF) {
		s.core.OpenFind()
	}
}

// findInFiles is the search window: the query and its options over the hits
// in the opened folders. Enter opens the chosen hit with its text selected.
func (s *App) findInFiles(c *ui.Context) {
	t := c.Theme()
	f := s.core.FindState()
	modal := ui.Modal(c, &f.Open, func() {
		ui.Column(c).Width(760).Gap(10).Children(func() {
			changed := false
			in := ui.TextInput(c.Key("find-query"), &f.Query).Placeholder("Find in files").Label("Find in files").FillWidth()
			if f.Focus {
				f.Focus = false
				in.Focus()
			}
			changed = in.Changed() || changed
			if in.Submitted() {
				s.core.FindActivate()
			}
			if c.Shortcut(0, ui.KeyDown) {
				s.core.FindMove(1)
			}
			if c.Shortcut(0, ui.KeyUp) {
				s.core.FindMove(-1)
			}
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(14).Children(func() {
				changed = ui.Checkbox(c.Key("find-case"), &f.CaseSensitive, "Match case").Changed() || changed
				changed = ui.Checkbox(c.Key("find-word"), &f.WholeWord, "Whole word").Changed() || changed
				changed = ui.Checkbox(c.Key("find-regex"), &f.UseRegex, "Regex").Changed() || changed
				ui.Spacer(c)
				ui.Text(c, "File mask").FontSize(12).TextColor(t.TextMuted)
				changed = ui.TextInput(c.Key("find-mask"), &f.Mask).Placeholder("*.sql, !tmp*").Label("File mask").Width(180).Changed() || changed
			})
			if changed {
				s.core.FindChanged()
			}

			switch {
			case len(s.core.FileRoots()) == 0:
				findNote(c, "Open a folder in the Files panel to search it.")
			case f.Query == "":
				findNote(c, "Type to search the files in the opened folders.")
			case f.Err != "":
				ui.Text(c, f.Err).FontSize(12).TextColor(t.Danger).Wrap()
			case len(f.Rows) == 0 && f.Searching:
				findNote(c, "Searching…")
			case len(f.Rows) == 0:
				findNote(c, "Nothing found.")
			default:
				ui.Scroll(c.Key("find-list")).FillWidth().MaxHeight(quickRowH * 12).Children(func() {
					ui.Column(c).FillWidth().Children(func() {
						for i, m := range f.Rows {
							s.findRow(c, i, m)
						}
					})
				})
				note := fmt.Sprintf("%d lines", len(f.Rows))
				if f.Truncated {
					note = fmt.Sprintf("First %d lines; narrow the search to see the rest.", len(f.Rows))
				}
				if f.Searching {
					note += " · searching…"
				}
				ui.Text(c, note).FontSize(11).TextColor(t.TextMuted)
			}
		})
	})
	if modal.Dismissed() {
		s.core.CloseFind()
	}
}

func findNote(c *ui.Context, msg string) {
	ui.Box(c).FillWidth().Height(quickRowH * 2).Center().Children(func() {
		ui.Text(c, msg).FontSize(12).TextColor(c.Theme().TextMuted)
	})
}

func (s *App) findRow(c *ui.Context, i int, m files.FindMatch) {
	t := c.Theme()
	f := s.core.FindState()
	chosen := i == f.Row
	row := ui.ButtonBase(c.Key(fmt.Sprintf("find-%d", i))).FillWidth().Height(quickRowH).Padding(0, 10).Gap(10).Radius(8).AlignItems(ui.Center)
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
		ui.Icon(c, icons.FileCode).FontSize(14).TextColor(icon)
		ui.Text(c, strings.TrimSpace(m.Text)).FontSize(13).TextColor(text).SingleLine().Grow(1)
		ui.Text(c, fmt.Sprintf("%s:%d", s.relPath(m.Path), m.Line)).FontSize(11).TextColor(muted).SingleLine().Shrink(0)
	})
	if row.Clicked() {
		s.core.FindChoose(i)
		s.core.FindActivate()
	}
}

// relPath is a file's path under the opened folder it is in, headed by the
// folder's name, as the palette shows it.
func (s *App) relPath(path string) string {
	for _, root := range s.core.FileRoots() {
		if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.Join(filepath.Base(root), rel)
		}
	}
	return filepath.Base(path)
}
