package view

import (
	"fmt"

	"github.com/egoist/mygo/ui"

	"seeker/internal/app"
	"seeker/internal/icons"
	"seeker/internal/model"
	"seeker/internal/sqltext"
)

// tableResults is a table tab: the filter bar over the grid of a page of
// rows. Recall's keys for it are taken here: ⌘R reads the page again, ⌘L
// goes to the WHERE field, ⌘S writes the edits staged.
func (s *App) tableResults(c *ui.Context, tab app.Tab) {
	t := c.Theme()
	tv := s.core.TableOf(tab.ID)
	if tv == nil {
		return
	}
	s.tableKeys(c, tab)
	s.tableBar(c, tab, tv)
	ui.Divider(c)
	if tv.Err != "" {
		ui.Row(c).FillWidth().Padding(8, 12).Gap(8).AlignItems(ui.Center).Background(t.Danger.Alpha(0.1)).Children(func() {
			ui.Text(c, tv.Err).FontSize(12).TextColor(t.Danger).Selectable().Wrap().Grow(1)
			if ui.Button(c.Key("retry"), "Retry").Height(24).Clicked() {
				s.core.ReloadTable(tab.ID)
			}
		})
	}
	g := tv.Grid
	switch {
	case g != nil:
		s.resultGrid(c, g, gridCtx{tabID: tab.ID, tv: tv})
	case tv.Loading:
		s.resultsNote(c, "Loading…")
	case tv.Err == "":
		s.resultsNote(c, "No rows loaded.")
	default:
		ui.Spacer(c)
	}
	summary := ""
	switch {
	case tv.Loading:
		summary = "Loading…"
	case g != nil:
		summary = fmt.Sprintf("%s · page %d", round(tv.Duration), tv.Page)
	}
	s.statusBar(c, tv.Loading || g != nil, summary, g, &tab)
}

// tableKeys are the keys of a table tab that work wherever the focus is.
func (s *App) tableKeys(c *ui.Context, tab app.Tab) {
	if s.pressed(c, app.ResultRerun) {
		s.core.ReloadTable(tab.ID)
	}
	if s.pressed(c, app.FocusWhere) {
		s.focusWhere = true
	}
	if tv := s.core.TableOf(tab.ID); tv != nil && tv.Grid != nil && tv.Grid.Pending() > 0 && s.pressed(c, app.EditorSave) {
		s.core.CommitEdits(tab.ID)
	}
}

// tableBar holds the filter on the left and the actions on the right, on
// one row as in recall: WHERE and ORDER BY are raw SQL, applied on Enter.
func (s *App) tableBar(c *ui.Context, tab app.Tab, tv *app.TableView) {
	ui.Row(c).Height(40).FillWidth().AlignItems(ui.Center).Padding(0, 8).Gap(6).Children(func() {
		s.clauseField(c, tab, tv, sqltext.ClauseWhere)
		s.clauseField(c, tab, tv, sqltext.ClauseOrderBy)
		if iconButton(c, icons.Refresh, "Reload ("+s.chordText(app.ResultRerun)+")").Clicked() {
			s.core.ReloadTable(tab.ID)
		}
		if tv.Grid != nil {
			s.columnsButton(c, tv.Grid)
			s.exportMenu(c, tv.Grid, tab.Title)
		}
	})
}

// clauseField is the WHERE or the ORDER BY field. It offers the columns of
// the page for the word at the end of its text, and a connector after a
// condition; Enter with none chosen applies both fields.
func (s *App) clauseField(c *ui.Context, tab app.Tab, tv *app.TableView, kind sqltext.ClauseKind) {
	t := c.Theme()
	text, key, hint := &tv.WhereText, "where", "WHERE id = 1 AND …"
	if kind == sqltext.ClauseOrderBy {
		text, key, hint = &tv.OrderText, "orderby", "ORDER BY id DESC"
	}
	var cols []string
	if tv.Grid != nil {
		for _, col := range tv.Grid.Columns {
			cols = append(cols, col.Name)
		}
	}
	d := sqltext.Postgres
	if p, ok := s.core.Profile(tab.Conn); ok && p.DBType == model.MySQL {
		d = sqltext.MySQL
	}
	in := s.tableFont(ui.Autocomplete(c.Key(key), text, sqltext.ClauseCompletions(kind, *text, cols, d)), 0).
		Placeholder(hint).Height(26).Padding(0, 10).Grow(1)
	if kind == sqltext.ClauseWhere && s.focusWhere {
		s.focusWhere = false
		in.Focus()
	}
	if blame := tv.ErrTarget; (blame == "where") == (kind == sqltext.ClauseWhere) && blame != "" {
		in.Border(1, t.Danger)
	}
	if in.Submitted() {
		s.core.ApplyTableFilter(tab.ID)
	}
}
