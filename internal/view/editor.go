package view

import (
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"seeker/internal/app"
	"seeker/internal/icons"
	"seeker/internal/model"
)

// editorPane is the run bar over the active tab's editor.
func (s *App) editorPane(c *ui.Context) {
	tab, ok := s.core.ActiveTab()
	if !ok {
		s.emptyEditor(c, "Open a table, or press + for a new query tab.")
		return
	}
	st, ok := s.core.Editor(tab.ID)
	if !ok {
		s.emptyEditor(c, "This tab has no editor.")
		return
	}
	s.runBar(c, tab)
	ui.Divider(c)
	editor.View(c, st).Grow(1).FillWidth().DrawOver(func(p *ui.Painter, r ui.Rect) {
		s.statementFrame(c.Theme(), p, r, tab.ID, st)
	})
}

func (s *App) emptyEditor(c *ui.Context, msg string) {
	ui.Box(c).Grow(1).FillWidth().Center().Children(func() {
		ui.Text(c, msg).FontSize(12).TextColor(c.Theme().TextMuted)
	})
}

// maxFrameLines bounds how many lines the box around a statement measures:
// past it the box is not worth a frame's work.
const maxFrameLines = 400

// statementFrame draws the box around the statement a run would take from
// the caret: from its first character's column to the end of its widest
// line (recall's outline). Measured with the editor's own geometry, so it
// follows scrolling, folds and wrapped rows; a wrapped line fills to the
// editor's edge, as its rows do.
func (s *App) statementFrame(t *ui.Theme, p *ui.Painter, r ui.Rect, id string, st *editor.State) {
	f := s.core.StatementFrame(id)
	if f == nil || len(f.Lines) > maxFrameLines {
		return
	}
	var left, right, top, bottom float32
	have, wrapped := false, false
	for _, ln := range f.Lines {
		a, ok1 := st.Rect(ln[0])
		b, ok2 := st.Rect(ln[1])
		if !ok1 || !ok2 {
			continue
		}
		if !have {
			left, top, have = a.X, a.Y, true
		}
		bottom = b.Y + b.H
		right = max(right, b.X)
		wrapped = wrapped || b.Y != a.Y
	}
	if !have {
		return
	}
	if wrapped {
		right = r.W - 14
	}
	// Room between the text and the line, or the border shaves a column
	// off the first and last glyphs.
	const pad = 4
	box := ui.Rect{X: r.X + left - pad, Y: r.Y + top - 1, W: right - left + 2*pad, H: bottom - top + 2}
	p.Stroke(box, t.Accent.Alpha(0.5), 3, 1)
}

// runBar names the tab's target and takes the run, format and explain
// buttons, with the keys each shows.
func (s *App) runBar(c *ui.Context, tab app.Tab) {
	t := c.Theme()
	running := s.core.Running(tab.ID)
	ui.Row(c).Height(headerH).FillWidth().AlignItems(ui.Center).Padding(0, 12).Gap(8).Children(func() {
		if running {
			if ui.Button(c.Key("stop"), "Stop").Height(26).Tooltip("Stop the running query (⌘↵)").Clicked() {
				s.core.Stop(tab.ID)
			}
		} else if ui.Button(c.Key("run"), "Run").Height(26).Tooltip("Run the selection, the statement at the caret, or the file (⌘↵)").Clicked() {
			s.core.RunSmart(tab.ID)
		}
		if iconButton(c, icons.WandSparkles, "Format SQL (⌥⇧F)").Clicked() {
			s.core.Format(tab.ID)
		}
		if iconButton(c, icons.Zap, "Explain the statement (⌥⇧E)").Clicked() {
			s.core.ExplainSmart(tab.ID)
		}
		s.datasourcePicker(c, tab)
		if s.core.AwaitingMnemonic(tab.ID) {
			ui.Text(c, "Press a letter or digit for the bookmark…").FontSize(11).TextColor(t.Accent)
		}
		ui.Spacer(c)
		state := "Saved"
		switch {
		case s.core.TabDirty(tab) && tab.Path != "":
			state = "Unsaved · autosaves"
		case s.core.TabDirty(tab):
			state = "Unsaved"
		case tab.Path == "":
			state = "Scratch"
		}
		ui.Text(c, state).FontSize(11).TextColor(t.TextMuted)
		if iconButton(c, icons.Download, "Save (⌘S)").Clicked() {
			s.core.SaveTab(tab.ID)
		}
	})
}

// datasourcePicker is the popover that points the tab, and its file, at a
// connection and database. A menu would grow as tall as the server's list of
// databases, so this one scrolls and filters. The databases of a connection
// are asked for the first time it is chosen.
func (s *App) datasourcePicker(c *ui.Context, tab app.Tab) {
	t := c.Theme()
	btn := ui.Button(c.Key("datasource"), "").Height(26).Gap(6).Tooltip("Where this runs")
	btn.Children(func() {
		ui.Text(c, s.datasourceLabel(tab)).SingleLine()
		ui.Icon(c, icons.ChevronDown).FontSize(12).TextColor(t.TextMuted)
	})
	if btn.Clicked() {
		s.dsOpen = !s.dsOpen
		if s.dsOpen {
			s.dsConn, s.dsFilter = tab.Conn, ""
		}
	}
	ui.Popover(c, btn, &s.dsOpen, func() {
		ui.Column(c).Width(280).Gap(8).Children(func() {
			var sql []model.Profile
			for _, p := range s.core.Profiles() {
				if p.DBType.IsSQL() {
					sql = append(sql, p)
				}
			}
			if !slices.ContainsFunc(sql, func(p model.Profile) bool { return p.ID == s.dsConn }) && len(sql) > 0 {
				s.dsConn = sql[0].ID
			}
			ui.Column(c).FillWidth().Children(func() {
				for _, p := range sql {
					s.datasourceRow(c, t, "ds-conn-"+p.ID, p.Name, p.ID == s.dsConn, func() { s.dsConn, s.dsFilter = p.ID, "" })
				}
			})
			if s.dsConn == "" {
				return
			}
			ui.SearchField(c.Key("ds-filter"), &s.dsFilter).Placeholder("Filter databases").Label("Filter databases").FillWidth()
			term := strings.ToLower(strings.TrimSpace(s.dsFilter))
			ui.Scroll(c.Key("ds-list")).FillWidth().MaxHeight(240).Children(func() {
				ui.Column(c).FillWidth().Children(func() {
					for _, db := range s.core.Databases(s.dsConn) {
						if term != "" && !strings.Contains(strings.ToLower(db), term) {
							continue
						}
						s.datasourceRow(c, t, "ds-db-"+db, db, s.dsConn == tab.Conn && db == tab.Database, func() {
							s.core.ChooseDatasource(tab.ID, s.dsConn, db)
							s.dsOpen = false
						})
					}
				})
			})
		})
	})
}

// datasourceRow is one choice of the picker: a connection above the list, or
// a database in it.
func (s *App) datasourceRow(c *ui.Context, t *ui.Theme, key, label string, chosen bool, pick func()) {
	row := ui.ButtonBase(c.Key(key)).FillWidth().Height(26).Padding(0, 10).Radius(6).AlignItems(ui.Center)
	text := t.Text
	if chosen {
		row.Background(t.Accent)
		text = t.AccentText
	} else if row.Hovered() {
		row.Background(t.SurfaceHover)
	}
	row.Children(func() {
		ui.Text(c, label).FontSize(12).TextColor(text).SingleLine()
		ui.Spacer(c)
	})
	if row.Clicked() {
		pick()
	}
}

// datasourceLabel is the tab's connection and database as the picker shows
// them.
func (s *App) datasourceLabel(tab app.Tab) string {
	name := tab.Conn
	if p, ok := s.core.Profile(tab.Conn); ok {
		name = p.Name
	}
	if name == "" {
		return "No datasource"
	}
	if tab.Database != "" {
		return name + " · " + tab.Database
	}
	return name
}

// bookmarkKeys takes Cmd+<character> for each bookmark that exists, after
// the shortcuts registered earlier in the frame (⌘W, ⌘P…): one of those
// already taken wins, and a character nothing is bound to is left alone.
func (s *App) bookmarkKeys(c *ui.Context) {
	for _, m := range s.core.BookmarkMnemonics() {
		key, ok := app.KeyOfMnemonic(m)
		if ok && c.Shortcut(ui.Cmd, key) {
			s.core.JumpBookmark(m)
		}
	}
}
