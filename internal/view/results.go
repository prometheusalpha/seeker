package view

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"seeker/internal/app"
	"seeker/internal/driver"
)

// maxStatementTabs is how many statements of a batch get a tab of their own
// in the strip; past it they go into one menu.
const maxStatementTabs = 8

// results is the area under the editor, or the whole of a table tab: the
// grid of the rows, with the bar and the status line that go with it.
func (s *App) results(c *ui.Context) {
	tab, ok := s.core.ActiveTab()
	switch {
	case ok && tab.Mode == app.ModeTable:
		s.tableResults(c, tab)
	case ok && tab.Mode == app.ModeQuery:
		s.queryResults(c, tab)
	default:
		// No tab: the area is there, with nothing to show yet.
		ui.Row(c).Height(headerH).FillWidth().AlignItems(ui.Center).Padding(0, 16).Children(func() {
			ui.Text(c, "RESULTS").FontSize(11).FontWeight(600).TextColor(c.Theme().TextMuted).SingleLine()
		})
		ui.Divider(c)
		s.resultsNote(c, "Run a query to see its rows.")
	}
}

func (s *App) resultsNote(c *ui.Context, msg string) {
	ui.Box(c).Grow(1).FillWidth().Center().Children(func() {
		ui.Text(c, msg).FontSize(12).TextColor(c.Theme().TextMuted)
	})
}

// queryResults shows what a run left: Result or Explain, and for a batch
// the statement to look at.
func (s *App) queryResults(c *ui.Context, tab app.Tab) {
	t := c.Theme()
	run, pane := s.core.RunViewOf(tab.ID), s.core.PaneOf(tab.ID)
	if run == nil || pane == nil {
		s.resultsNote(c, "This tab has no editor.")
		return
	}
	if n := len(run.Results); n > 0 {
		pane.Shown = min(max(pane.Shown, 0), n-1)
	}
	g := s.core.GridOf(tab.ID, pane.Shown)
	ui.Row(c).Height(headerH).FillWidth().AlignItems(ui.Center).Padding(0, 16).Gap(8).Children(func() {
		ui.Text(c, "RESULTS").FontSize(11).FontWeight(600).TextColor(t.TextMuted).SingleLine()
		ui.Segmented(c.Key("view"), &pane.View, "Result", "Explain").Height(24)
		if pane.View == app.ViewResult {
			s.statementPicker(c, run, pane)
		}
		ui.Spacer(c)
		if g != nil && pane.View == app.ViewResult {
			s.columnsButton(c, g)
			s.exportMenu(c, g, tab.Title)
		}
	})
	ui.Divider(c)
	if pane.View == app.ViewExplain {
		s.planPane(c, run)
		s.statusBar(c, run.Plan != nil, "", nil, nil)
		return
	}
	if !run.Any() {
		s.resultsNote(c, "Run a query to see its rows.")
		return
	}
	if run.Err != "" {
		ui.Text(c, run.Err).FontSize(12).TextColor(t.Danger).Selectable().Wrap().Padding(10, 12)
	}
	if len(run.Results) == 0 {
		switch {
		case run.Running:
			s.resultsNote(c, "Running…")
		case run.Err == "":
			s.resultsNote(c, "No result.")
		}
		if run.Stopped {
			s.statusBar(c, true, fmt.Sprintf("Stopped after %d", len(run.Results)), nil, nil)
		}
		return
	}
	res := run.Results[pane.Shown]
	switch {
	case res.Err != nil:
		s.errorBlock(c, tab.ID, res)
	case g == nil:
		s.resultsNote(c, fmt.Sprintf("%d rows affected", res.Affected))
	default:
		s.resultGrid(c, g, gridCtx{tabID: tab.ID})
	}
	s.statusBar(c, true, runSummary(run, res), g, nil)
}

// statementPicker switches among the statements of a batch.
func (s *App) statementPicker(c *ui.Context, run *app.RunView, pane *app.ResultPane) {
	n := len(run.Results)
	if n < 2 {
		return
	}
	label := func(i int) string {
		l := fmt.Sprintf("#%d · %s", i+1, round(run.Results[i].Duration))
		if run.Results[i].Err != nil {
			l = fmt.Sprintf("#%d ✕", i+1)
		}
		return l
	}
	if n <= maxStatementTabs {
		labels := make([]string, n)
		for i := range labels {
			labels[i] = label(i)
		}
		ui.Segmented(c.Key("statements"), &pane.Shown, labels...).Height(24)
		return
	}
	ui.PopoverMenuButton(c.Key("statements"), label(pane.Shown), func(m *ui.Menu) {
		for i := range run.Results {
			if m.Item(label(i)).ID(strconv.Itoa(i)).Chosen() {
				pane.Shown = i
			}
		}
	}).Height(24)
}

// runSummary is the line under a batch: how many statements, how long.
func runSummary(r *app.RunView, res driver.Result) string {
	var b strings.Builder
	switch {
	case res.Err != nil:
		b.WriteString("Error")
	case len(res.Columns) == 0:
		fmt.Fprintf(&b, "%d rows affected · %s", res.Affected, round(res.Duration))
	default:
		fmt.Fprintf(&b, "%d rows · %s", len(res.Rows), round(res.Duration))
		if res.Truncated {
			fmt.Fprintf(&b, " · cut at %d", driver.MaxRows)
		}
	}
	switch {
	case r.Running:
		fmt.Fprintf(&b, " · running, %d done", len(r.Results))
	case r.Stopped:
		fmt.Fprintf(&b, " · stopped after %d", len(r.Results))
	case len(r.Results) > 1:
		fmt.Fprintf(&b, " · %d statements, %s", len(r.Results), round(r.Elapsed))
	}
	return b.String()
}

func round(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%d ms", d.Milliseconds())
	}
	return fmt.Sprintf("%.2f s", d.Seconds())
}

// statusBar is the line under the grid: the rows and the time on the left;
// on the right the cells chosen and their sum, the edits waiting, and a
// table's paging.
func (s *App) statusBar(c *ui.Context, show bool, text string, g *app.ResultGrid, tab *app.Tab) {
	if !show {
		return
	}
	t := c.Theme()
	ui.Divider(c)
	ui.Row(c).Height(30).FillWidth().AlignItems(ui.Center).Padding(0, 12).Gap(12).Children(func() {
		ui.Text(c, text).FontSize(11).TextColor(t.TextMuted).SingleLine()
		ui.Spacer(c)
		if g != nil {
			cells, sum := selectionTexts(g)
			if cells != "" {
				ui.Text(c, cells).FontSize(11).TextColor(t.TextMuted).SingleLine()
			}
			if sum != "" {
				ui.Text(c, sum).FontSize(11).TextColor(t.TextMuted).SingleLine()
			}
			if tab != nil {
				s.editControls(c, g, tab.ID)
			}
		}
		if tab != nil {
			if tv := s.core.TableOf(tab.ID); tv != nil {
				s.paging(c, *tab, tv)
			}
		}
	})
}

// paging is a table's page controls. The page is a field: Enter goes to
// the page typed, however far; a page past the last is an empty one, as the
// total is not known until Count.
func (s *App) paging(c *ui.Context, tab app.Tab, tv *app.TableView) {
	if s.pageText == nil {
		s.pageText = map[string]string{}
	}
	if ui.Button(c.Key("prev"), "‹").Height(22).Disabled(tv.Page <= 1 || tv.Loading).Tooltip("Previous page").Clicked() {
		s.core.SetTablePage(tab.ID, tv.Page-1)
	}
	text, typing := s.pageText[tab.ID]
	if !typing {
		text = strconv.Itoa(tv.Page)
	}
	in := ui.TextInput(c.Key("page"), &text).Width(48).Height(22).Padding(0, 4).TextAlign(ui.Center).Tooltip("Page: Enter to go")
	if in.Focused() {
		s.pageText[tab.ID] = text
	} else {
		delete(s.pageText, tab.ID)
	}
	if in.Submitted() {
		if n, err := strconv.Atoi(strings.TrimSpace(text)); err == nil && n >= 1 {
			s.core.SetTablePage(tab.ID, n)
		}
		delete(s.pageText, tab.ID)
	}
	if ui.Button(c.Key("next"), "›").Height(22).Disabled(!tv.HasNextPage() || tv.Loading).Tooltip("Next page").Clicked() {
		s.core.SetTablePage(tab.ID, tv.Page+1)
	}
	ui.PopoverMenuButton(c.Key("pagesize"), strconv.Itoa(tv.PageSize)+" rows", func(m *ui.Menu) {
		for _, n := range app.PageSizes {
			if m.Item(strconv.Itoa(n)).ID(strconv.Itoa(n)).Radio(n == tv.PageSize).Chosen() {
				s.core.SetTablePageSize(tab.ID, n)
			}
		}
	}).Height(22).Tooltip("Rows per page")
}

// editControls commit or drop the edits staged on a table's grid.
func (s *App) editControls(c *ui.Context, g *app.ResultGrid, tabID string) {
	if n := g.Pending(); n > 0 {
		busy := s.core.TableOf(tabID).Committing()
		ui.Text(c, fmt.Sprintf("%d edits", n)).FontSize(11).TextColor(c.Theme().Warning).SingleLine()
		if ui.Button(c.Key("commit"), "Save").Height(20).Disabled(busy).Tooltip("Write the edits (" + s.chordText(app.EditorSave) + ")").Clicked() {
			s.core.CommitEdits(tabID)
		}
		if ui.Button(c.Key("discard"), "Discard").Height(20).Disabled(busy).Clicked() {
			s.core.DiscardEdits(tabID)
		}
	}
}

// errorBlock is a failed statement's message and where it is.
func (s *App) errorBlock(c *ui.Context, tabID string, res driver.Result) {
	t := c.Theme()
	ui.Column(c).FillWidth().Padding(10, 12).Gap(4).Children(func() {
		ui.Text(c, oneLine(res.Statement.SQL, 120)).FontSize(11).TextColor(t.TextMuted).SingleLine()
		msg := res.Err.Message
		if place := s.core.ErrorPlace(tabID, res); place != "" {
			msg += "  (" + place + ")"
		}
		ui.Text(c, msg).FontSize(12).TextColor(t.Danger).Selectable().Wrap()
		if res.Err.Detail != "" {
			ui.Text(c, res.Err.Detail).FontSize(11).TextColor(t.TextMuted).Selectable().Wrap()
		}
	})
}

// planPane shows an Explain's answer; the plan viewer is M6.
func (s *App) planPane(c *ui.Context, r *app.RunView) {
	t := c.Theme()
	var body func()
	switch {
	case r.Explaining:
		body = func() { ui.Text(c, "Explaining…").FontSize(12).TextColor(t.TextMuted) }
	case r.PlanErr != "":
		body = func() { ui.Text(c, "Explain failed: "+r.PlanErr).FontSize(12).TextColor(t.Danger).Selectable().Wrap() }
	case r.Plan != nil:
		raw := r.Plan.Raw
		if len(raw) > 6000 {
			raw = raw[:6000] + "\n…"
		}
		body = func() {
			ui.Text(c, "EXPLAIN · "+oneLine(r.PlanSQL, 100)).FontSize(11).FontWeight(600).TextColor(t.TextMuted).SingleLine()
			s.tableFont(ui.Text(c, raw), -1).Selectable()
		}
	default:
		s.resultsNote(c, "Explain the statement under the caret ("+s.chordText(app.EditorExplain)+").")
		return
	}
	ui.Scroll(c).Grow(1).FillWidth().Children(func() {
		ui.Column(c).FillWidth().Padding(10, 12).Gap(4).Children(body)
	})
}

// oneLine is s on one line, cut to n characters.
func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
