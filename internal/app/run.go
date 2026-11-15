package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo/ui/editor"

	"seeker/internal/driver"
	"seeker/internal/sqlfmt"
	"seeker/internal/sqltext"
)

// runState is what a tab's last run left. Results arrive one statement at a
// time, as recall's queryProgress pushed them. Gen drops what a run that
// was stopped, or replaced by a newer one, still publishes.
type runState struct {
	gen     int
	cancel  context.CancelFunc
	Running bool
	Stopped bool
	Started time.Time
	Elapsed time.Duration
	// Script is what ran and Base where it starts in the buffer, so an
	// error's offset in the script finds its place in the editor.
	Script  string
	Base    int
	Results []driver.Result
	// Err is a failure before any statement ran: no connection, no session.
	Err string

	Explaining bool
	Plan       *driver.Plan
	PlanErr    string
	// PlanSQL is the statement the plan is of.
	PlanSQL string
}

// RunView is what the view reads of a tab's run.
type RunView = runState

// Any reports something to show: a run, an error, a plan.
func (r *runState) Any() bool {
	return r.Running || r.Err != "" || len(r.Results) > 0 || r.Explaining || r.Plan != nil || r.PlanErr != "" || r.Stopped
}

// RunViewOf is a tab's run state, nil for a tab with no editor.
func (a *App) RunViewOf(id string) *RunView { return a.RunOf(id) }

// RunOf is a tab's run state, nil for a tab with no editor.
func (a *App) RunOf(id string) *runState {
	if te := a.editors[id]; te != nil {
		return &te.run
	}
	return nil
}

// Running reports a run in flight, which the run bar turns into Stop.
func (a *App) Running(id string) bool {
	r := a.RunOf(id)
	return r != nil && r.Running
}

// RunSmart is Cmd+Enter, narrowing the run as the user narrowed it: a run
// in flight stops; a selection runs (several statements are split); else
// the statement under the caret; else the whole file. A caret in the blank
// between statements belongs to neither, so the file runs, as before the
// outline existed. A whitespace-only selection counts as none.
func (a *App) RunSmart(id string) {
	te := a.editors[id]
	if te == nil {
		return
	}
	if te.run.Running {
		a.Stop(id)
		return
	}
	if script, base, ok := te.smartTarget(); ok {
		a.run(te, script, base)
		return
	}
	a.RunAll(id)
}

// smartTarget is what Cmd+Enter runs: the selection, else the statement
// under the caret; false for neither, which runs the file.
func (te *tabEditor) smartTarget() (script string, base int, ok bool) {
	text := te.st.Buffer.String()
	if lo, hi := te.selectedRange(); strings.TrimSpace(text[lo:hi]) != "" {
		return text[lo:hi], lo, true
	}
	if stmt, ok := te.statementAtCaret(); ok {
		return text[stmt.Start:stmt.End], stmt.Start, true
	}
	return "", 0, false
}

// RunAll runs the whole file, which the Run button does.
func (a *App) RunAll(id string) {
	te := a.editors[id]
	if te == nil || te.run.Running {
		return
	}
	text := te.st.Buffer.String()
	if strings.TrimSpace(text) == "" {
		a.toast("Nothing to run")
		return
	}
	a.run(te, text, 0)
}

func (a *App) run(te *tabEditor, script string, base int) {
	t, ok := a.TabByID(te.id)
	if !ok {
		return
	}
	if _, ok := a.Profile(t.Conn); !ok {
		a.toast("Pick a connection first")
		return
	}
	a.ensureTabConnected(t)
	cs := a.conn(t.Conn)
	ctx, cancel := context.WithCancel(a.ctx)
	r := &te.run
	r.gen++
	gen := r.gen
	*r = runState{gen: gen, cancel: cancel, Running: true, Started: time.Now(), Script: script, Base: base,
		Plan: r.Plan, PlanErr: r.PlanErr, PlanSQL: r.PlanSQL}
	// The new run's rows get new grids: a selection or a hidden column of
	// the last run's must not stick to them.
	te.grids, te.pane = nil, ResultPane{}
	te.st.SetDiagnostics(te.st.Buffer.Version(), nil)
	version := te.st.Buffer.Version()
	id := te.id

	// Everything the goroutine reports goes through here, dropped when this
	// run is no longer the tab's.
	publish := func(fn func(r *runState)) {
		a.upd(func() {
			if te := a.editors[id]; te != nil && te.run.gen == gen {
				fn(&te.run)
			}
		})
	}
	go func() {
		defer cancel()
		err := waitConnected(cs)
		if err == nil {
			err = a.pl.Query(ctx, t.Conn, t.Database, id, func(ctx context.Context, c driver.Conn) error {
				c.Execute(ctx, script, driver.MaxRows, func(res driver.Result) {
					publish(func(r *runState) { r.Results = append(r.Results, res) })
				})
				return nil
			})
		}
		publish(func(r *runState) {
			r.Running, r.cancel = false, nil
			r.Elapsed = time.Since(r.Started)
			if err != nil && !errors.Is(err, context.Canceled) {
				r.Err = err.Error()
			}
			a.showErrors(te, r, version)
		})
	}()
}

// Stop cancels the run in flight. The driver interrupts the statement on
// the server (KILL QUERY for MySQL); results already in stay.
func (a *App) Stop(id string) {
	r := a.RunOf(id)
	if r == nil || !r.Running {
		return
	}
	r.Stopped = true
	if r.cancel != nil {
		r.cancel()
	}
}

// showErrors puts the first failed statement's error in the text it came
// from, where the editor underlines it. The offsets are those of the text
// as it was when the run started; the editor carries them across edits
// since.
func (a *App) showErrors(te *tabEditor, r *runState, version uint64) {
	for _, res := range r.Results {
		if res.Err == nil {
			continue
		}
		start := r.Base + res.Statement.Start
		if res.Err.Offset >= 0 {
			start = r.Base + res.Err.Offset
		}
		end := r.Base + res.Statement.End
		if res.Err.Offset >= 0 {
			// The token at the error, or the rest of its line.
			end = start + tokenLen(r.Script[min(res.Err.Offset, len(r.Script)):])
		}
		te.st.SetDiagnostics(version, []editor.Diagnostic{{
			Start: start, End: max(end, start+1), Severity: editor.SeverityError, Message: res.Err.Message, Source: "database",
		}})
		return
	}
}

// tokenLen is the length of the word at the start of s, or of one
// character when it starts on punctuation.
func tokenLen(s string) int {
	n := 0
	for _, r := range s {
		if r != '_' && r != '$' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			break
		}
		n += utf8.RuneLen(r)
	}
	if n == 0 && s != "" {
		_, n = utf8.DecodeRuneInString(s)
	}
	return n
}

// ErrorPlace is where an error shows to the user: the line and column of
// the editor's text, from 1, when the server said where.
func (a *App) ErrorPlace(id string, res driver.Result) string {
	te := a.editors[id]
	if te == nil || res.Err == nil {
		return ""
	}
	r := &te.run
	off := r.Base + res.Statement.Start
	if res.Err.Offset >= 0 {
		off = r.Base + res.Err.Offset
	}
	off = min(off, te.st.Buffer.Len())
	p := te.st.Buffer.Pos(off)
	return fmt.Sprintf("line %d, column %d", p.Line+1, p.Col+1)
}

// ExplainSmart plans the selection, else the statement under the caret.
// Unlike a run it does not fall back to the file: a batch has no single
// plan, so the file is used only when it is one statement, which is what
// makes the caret in the blank after a lone query still explain it.
func (a *App) ExplainSmart(id string) {
	te := a.editors[id]
	if te == nil {
		return
	}
	text := te.st.Buffer.String()
	sql := ""
	if lo, hi := te.selectedRange(); strings.TrimSpace(text[lo:hi]) != "" {
		sql = strings.TrimSpace(text[lo:hi])
	} else if stmt, ok := te.statementAtCaret(); ok {
		sql = strings.TrimSpace(text[stmt.Start:stmt.End])
	} else if t, ok := a.TabByID(id); ok {
		if all := sqltext.Split(text, a.dialect(t)); len(all) == 1 {
			sql = all[0].SQL
		}
	}
	if sql == "" {
		a.toast("Put the cursor inside a statement, or select one, to explain it.")
		return
	}
	a.Explain(id, sql)
}

// Explain asks the server for the estimated plan of sql, on the tab's
// session, never executing it. The viewer for it is M6; until then the
// result shows the server's answer as text.
func (a *App) Explain(id, sql string) {
	te := a.editors[id]
	t, ok := a.TabByID(id)
	if te == nil || !ok || te.run.Explaining {
		return
	}
	if _, ok := a.Profile(t.Conn); !ok {
		a.toast("Pick a connection first")
		return
	}
	a.ensureTabConnected(t)
	cs := a.conn(t.Conn)
	te.run.Explaining, te.run.Plan, te.run.PlanErr, te.run.PlanSQL = true, nil, "", sql
	te.pane.View = ViewExplain
	gen := te.run.gen
	go func() {
		var plan driver.Plan
		err := waitConnected(cs)
		if err == nil {
			err = a.pl.Query(a.ctx, t.Conn, t.Database, id, func(ctx context.Context, c driver.Conn) error {
				var e error
				plan, e = c.Explain(ctx, sql)
				return e
			})
		}
		a.upd(func() {
			te := a.editors[id]
			if te == nil || te.run.gen != gen || te.run.PlanSQL != sql {
				return
			}
			te.run.Explaining = false
			if err != nil {
				te.run.PlanErr = err.Error()
				return
			}
			te.run.Plan = &plan
		})
	}()
}

// formatFailure is the first line of a formatter's complaint, capped: the
// formatter sometimes lists every token it would have accepted.
func formatFailure(err error) string {
	first, _, _ := strings.Cut(err.Error(), "\n")
	if r := []rune(first); len(r) > 200 {
		first = string(r[:200]) + "…"
	}
	return first
}

// Format formats the selection, else the statement the caret is in, else
// the whole file (a caret in the blank between statements), as one undoable
// edit. The formatter runs off the window's thread; if the text moved on
// while it ran, nothing is written, because the range was measured in the
// old text.
func (a *App) Format(id string) {
	te := a.editors[id]
	t, ok := a.TabByID(id)
	if te == nil || !ok {
		return
	}
	buf := te.st.Buffer
	text := buf.String()
	lo, hi := te.selectedRange()
	selection := lo != hi
	if !selection {
		lo, hi = 0, len(text)
		if stmt, ok := te.statementAtCaret(); ok {
			lo, hi = stmt.Start, stmt.End
		}
	}
	target := text[lo:hi]
	if strings.TrimSpace(target) == "" {
		return
	}
	version := buf.Version()
	d := a.dialect(t)
	go func() {
		out, err := sqlfmt.Format(a.ctx, target, d)
		a.upd(func() {
			te := a.editors[id]
			if te == nil || te.st.Buffer != buf || buf.Version() != version {
				return
			}
			if err != nil {
				if !errors.Is(err, context.Canceled) {
					a.toast("Could not format: " + formatFailure(err))
				}
				return
			}
			if out == target {
				return
			}
			buf.Edit(editor.Edit{Start: lo, End: hi, Text: out})
			// A selection stays one over the new text; a caret has no
			// offset left, so it lands on the first character formatted.
			if selection {
				te.st.Select(lo, lo+len(out))
			} else {
				te.st.Select(lo, lo)
			}
		})
	}()
}

// PaneOf is what the area under a query tab's editor shows, for the view to
// bind its switches to; nil for a tab with no editor.
func (a *App) PaneOf(id string) *ResultPane {
	if te := a.editors[id]; te != nil {
		return &te.pane
	}
	return nil
}

// GridOf is the grid of the tab's i-th result, made the first time it is
// asked for; nil for a result that has no rows to show (an error, or a
// statement that returns none).
func (a *App) GridOf(id string, i int) *ResultGrid {
	te := a.editors[id]
	if te == nil || i < 0 || i >= len(te.run.Results) {
		return nil
	}
	if g := te.grids[i]; g != nil {
		return g
	}
	res := te.run.Results[i]
	if res.Err != nil || len(res.Columns) == 0 {
		return nil
	}
	if te.grids == nil {
		te.grids = map[int]*ResultGrid{}
	}
	g := NewResultGrid(res, nil)
	te.grids[i] = g
	return g
}
