package view

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"seeker/internal/app"
	"seeker/internal/driver"
	"seeker/internal/grid"
)

// previewRows and previewCell bound what the stand-in for the grid (M4)
// shows of a result: its first rows, each cell cut short.
const (
	previewRows = 12
	previewCell = 40
)

// results is the area under the editor. Until the grid arrives (M4) a
// query tab shows what each statement did: its rows as text, how many were
// affected, its error and where it is, and the plan of an Explain as the
// server answered it.
func (s *App) results(c *ui.Context) {
	t := c.Theme()
	tab, hasTab := s.core.ActiveTab()
	title, empty := "RESULTS", "Run a query to see its rows."
	if hasTab && tab.Mode == app.ModeTable {
		// The grid that lists a table's rows lands in M4; until then the tab
		// names its table and says what it holds.
		title, empty = tab.Table, "No rows loaded."
	}
	var run *app.RunView
	if hasTab && tab.Mode == app.ModeQuery {
		run = s.core.RunViewOf(tab.ID)
	}
	ui.Row(c).Height(36).FillWidth().AlignItems(ui.Center).Padding(0, 12).Gap(8).Children(func() {
		ui.Text(c, title).FontSize(11).FontWeight(600).TextColor(t.TextMuted).SingleLine()
		if run != nil {
			ui.Text(c, runSummary(run)).FontSize(11).TextColor(t.TextMuted).SingleLine()
		}
		ui.Spacer(c)
	})
	ui.Divider(c)
	if run == nil || !run.Any() {
		ui.Box(c).Grow(1).FillWidth().Center().Children(func() {
			ui.Text(c, empty).FontSize(12).TextColor(t.TextMuted)
		})
		return
	}
	ui.Scroll(c).Grow(1).FillWidth().Children(func() {
		ui.Column(c).FillWidth().Padding(10, 12).Gap(14).Children(func() {
			if run.Err != "" {
				ui.Text(c, run.Err).FontSize(12).TextColor(t.Danger).Selectable().Wrap()
			}
			for i, res := range run.Results {
				s.resultBlock(c, tab.ID, i, res)
			}
			s.planBlock(c, run)
		})
	})
}

func runSummary(r *app.RunView) string {
	switch {
	case r.Running:
		return fmt.Sprintf("Running… %d done", len(r.Results))
	case r.Stopped:
		return fmt.Sprintf("Stopped after %d", len(r.Results))
	case len(r.Results) > 0:
		return fmt.Sprintf("%d statements · %s", len(r.Results), round(r.Elapsed))
	}
	return ""
}

func round(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%d ms", d.Milliseconds())
	}
	return fmt.Sprintf("%.2f s", d.Seconds())
}

// resultBlock is one statement's outcome.
func (s *App) resultBlock(c *ui.Context, tabID string, i int, res driver.Result) {
	t := c.Theme()
	ui.Column(c.Key(fmt.Sprintf("result-%d", i))).FillWidth().Gap(4).Children(func() {
		ui.Text(c, oneLine(res.Statement.SQL, 120)).FontSize(11).TextColor(t.TextMuted).SingleLine()
		switch {
		case res.Err != nil:
			msg := res.Err.Message
			if place := s.core.ErrorPlace(tabID, res); place != "" {
				msg += "  (" + place + ")"
			}
			ui.Text(c, msg).FontSize(12).TextColor(t.Danger).Selectable().Wrap()
			if res.Err.Detail != "" {
				ui.Text(c, res.Err.Detail).FontSize(11).TextColor(t.TextMuted).Selectable().Wrap()
			}
		case len(res.Columns) == 0:
			ui.Text(c, fmt.Sprintf("%d rows affected · %s", res.Affected, round(res.Duration))).FontSize(12)
		default:
			note := fmt.Sprintf("%d rows · %s", len(res.Rows), round(res.Duration))
			if res.Truncated {
				note += fmt.Sprintf(" · cut at %d", driver.MaxRows)
			}
			ui.Text(c, note).FontSize(12)
			if len(res.Rows) > 0 {
				ui.Text(c, previewTable(res)).Font("Menlo").FontSize(11).Selectable()
			}
		}
	})
}

// previewTable lays the first rows out as aligned text.
func previewTable(res driver.Result) string {
	rows := res.Rows[:min(len(res.Rows), previewRows)]
	cells := make([][]string, 0, len(rows)+1)
	head := make([]string, len(res.Columns))
	for i, col := range res.Columns {
		head[i] = oneLine(col.Name, previewCell)
	}
	cells = append(cells, head)
	for _, r := range rows {
		line := make([]string, len(res.Columns))
		for i := range line {
			if i < len(r) {
				line[i] = oneLine(grid.FormatCell(r[i], res.Columns[i].Type), previewCell)
			}
		}
		cells = append(cells, line)
	}
	width := make([]int, len(res.Columns))
	for _, line := range cells {
		for i, cell := range line {
			width[i] = max(width[i], len([]rune(cell)))
		}
	}
	var b strings.Builder
	for _, line := range cells {
		for i, cell := range line {
			if i > 0 {
				b.WriteString("  ")
			}
			b.WriteString(cell)
			b.WriteString(strings.Repeat(" ", width[i]-len([]rune(cell))))
		}
		b.WriteByte('\n')
	}
	if len(res.Rows) > previewRows {
		fmt.Fprintf(&b, "… %d more rows", len(res.Rows)-previewRows)
	}
	return strings.TrimRight(b.String(), "\n")
}

// planBlock shows an Explain's answer; the plan viewer is M6.
func (s *App) planBlock(c *ui.Context, r *app.RunView) {
	t := c.Theme()
	switch {
	case r.Explaining:
		ui.Text(c, "Explaining…").FontSize(12).TextColor(t.TextMuted)
	case r.PlanErr != "":
		ui.Text(c, "Explain failed: "+r.PlanErr).FontSize(12).TextColor(t.Danger).Selectable().Wrap()
	case r.Plan != nil:
		raw := r.Plan.Raw
		if len(raw) > 6000 {
			raw = raw[:6000] + "\n…"
		}
		ui.Column(c).FillWidth().Gap(4).Children(func() {
			ui.Text(c, "EXPLAIN · "+oneLine(r.PlanSQL, 100)).FontSize(11).FontWeight(600).TextColor(t.TextMuted).SingleLine()
			ui.Text(c, raw).Font("Menlo").FontSize(11).Selectable()
		})
	}
}

// oneLine is s on one line, cut to n characters.
func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
