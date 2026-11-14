package app

import (
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"seeker/internal/driver"
	"seeker/internal/model"
	"seeker/internal/sqltext"
)

// tabEditor is a query tab's editor: its own Buffer, so its undo is its
// own, and what the app derives from the text.
type tabEditor struct {
	a  *App
	id string
	st *editor.State

	// synced is the buffer version last written to the tab's SQL.
	synced uint64

	// The statement under the caret is cached by buffer version and caret.
	stmtVer   uint64
	stmtPos   int
	stmtValid bool
	stmt      sqltext.Statement
	stmtOK    bool

	// pending is a completion request that found a list cold; the list
	// landing asks again, unless the text moved on.
	pending *editor.CompletionRequest

	// awaiting: Ctrl+F11 was pressed and the next character names the
	// mnemonic. swallow is when that key went by, so the text it types is
	// not inserted.
	awaiting bool
	swallow  time.Time

	run runState
}

// Editor returns the editor of a query tab, made when it is first asked
// for from the tab's SQL; false for a tab that has none.
func (a *App) Editor(id string) (*editor.State, bool) {
	te := a.tabEditor(id)
	if te == nil {
		return nil, false
	}
	return te.st, true
}

func (a *App) tabEditor(id string) *tabEditor {
	if te := a.editors[id]; te != nil {
		return te
	}
	t, ok := a.TabByID(id)
	if !ok || t.Mode != ModeQuery {
		return nil
	}
	buf := editor.NewBuffer(t.SQL)
	buf.SetLanguage(editor.LanguageFor("query.sql"))
	te := &tabEditor{a: a, id: id, synced: buf.Version()}
	te.st = &editor.State{
		Buffer:       buf,
		Theme:        a.codeTheme,
		OnInput:      te.onInput,
		OnCompletion: te.complete,
	}
	if a.editors == nil {
		a.editors = map[string]*tabEditor{}
	}
	a.editors[id] = te
	return te
}

// SetEditorTheme is the theme every editor wears; the view hands it over
// each frame because the theme follows the system's appearance.
func (a *App) SetEditorTheme(t *editor.Theme) {
	a.codeTheme = t
	for _, te := range a.editors {
		te.st.Theme = t
	}
}

// SyncEditors writes each editor's text to its tab, so a tab is dirty the
// moment it differs from what it was saved with, and keeps the gutter's
// bookmark marks and the files' autosave current. Editors of closed tabs
// go.
func (a *App) SyncEditors() {
	for id, te := range a.editors {
		i := a.indexOf(id)
		if i < 0 {
			delete(a.editors, id)
			continue
		}
		if v := te.st.Buffer.Version(); v != te.synced {
			te.synced = v
			a.tabs.Items[i].SQL = te.st.Buffer.String()
			te.stmtValid = false
			a.scheduleSave(id)
		}
		te.st.GutterMarks = a.gutterMarks(a.tabs.Items[i].Path)
	}
	a.applyJump()
	if t, ok := a.ActiveTab(); ok && t.Mode == ModeQuery {
		a.warmTab(t)
	}
}

func (a *App) dialect(t Tab) sqltext.Dialect {
	if p, ok := a.Profile(t.Conn); ok && p.DBType == model.MySQL {
		return sqltext.MySQL
	}
	return sqltext.Postgres
}

// caret is the offset of the newest caret, which is where "under the
// caret" is read: a selection is an instruction to run a range, not an
// outline.
func (te *tabEditor) caret() int { return te.st.Selection().Head }

// StatementAtCaret is the statement the caret is in, for the frame drawn
// around it and for Run; false between statements and in an empty file.
func (a *App) StatementAtCaret(id string) (sqltext.Statement, bool) {
	te := a.editors[id]
	if te == nil {
		return sqltext.Statement{}, false
	}
	return te.statementAtCaret()
}

func (te *tabEditor) statementAtCaret() (sqltext.Statement, bool) {
	ver, pos := te.st.Buffer.Version(), te.caret()
	if te.stmtValid && te.stmtVer == ver && te.stmtPos == pos {
		return te.stmt, te.stmtOK
	}
	t, _ := te.a.TabByID(te.id)
	te.stmt, te.stmtOK = sqltext.At(te.st.Buffer.String(), te.a.dialect(t), pos)
	te.stmtVer, te.stmtPos, te.stmtValid = ver, pos, true
	return te.stmt, te.stmtOK
}

// selectedRange is the selection as an ordered range of bytes.
func (te *tabEditor) selectedRange() (lo, hi int) {
	s := te.st.Selection()
	return min(s.Anchor, s.Head), max(s.Anchor, s.Head)
}

// mnemonicKeys are the physical keys of the 36 bookmark characters: the
// keyboard's own name for the key, since Shift turns the 1 key into "!".
var mnemonicKeys = map[ui.Key]byte{
	ui.KeyA: 'a', ui.KeyB: 'b', ui.KeyC: 'c', ui.KeyD: 'd', ui.KeyE: 'e', ui.KeyF: 'f', ui.KeyG: 'g',
	ui.KeyH: 'h', ui.KeyI: 'i', ui.KeyJ: 'j', ui.KeyK: 'k', ui.KeyL: 'l', ui.KeyM: 'm', ui.KeyN: 'n',
	ui.KeyO: 'o', ui.KeyP: 'p', ui.KeyQ: 'q', ui.KeyR: 'r', ui.KeyS: 's', ui.KeyT: 't', ui.KeyU: 'u',
	ui.KeyV: 'v', ui.KeyW: 'w', ui.KeyX: 'x', ui.KeyY: 'y', ui.KeyZ: 'z',
	ui.Key0: '0', ui.Key1: '1', ui.Key2: '2', ui.Key3: '3', ui.Key4: '4',
	ui.Key5: '5', ui.Key6: '6', ui.Key7: '7', ui.Key8: '8', ui.Key9: '9',
}

// Mnemonic is the bookmark character a physical key names.
func Mnemonic(k ui.Key) (byte, bool) {
	c, ok := mnemonicKeys[k]
	return c, ok
}

// KeyOfMnemonic is the key a bookmark character is on.
func KeyOfMnemonic(m string) (ui.Key, bool) {
	for k, c := range mnemonicKeys {
		if len(m) == 1 && m[0] == c {
			return k, true
		}
	}
	return ui.KeyUnknown, false
}

// swallowWindow is how long after a key the text it would type is dropped.
const swallowWindow = 150 * time.Millisecond

// onInput takes the editor's own chords before the editor sees them: run,
// format, explain, save, and assigning a bookmark. The keys are the
// physical ones, so a layout that types something else still works.
func (te *tabEditor) onInput(ev ui.InputEvent) bool {
	if ev.Kind == ui.InputText && !te.swallow.IsZero() && ev.Time.Sub(te.swallow) < swallowWindow {
		return true
	}
	if ev.Kind != ui.InputKeyDown {
		return false
	}
	key := ev.PhysicalKey
	if key == ui.KeyUnknown {
		key = ev.Key
	}
	// A character after Ctrl+F11 names the bookmark; anything else, Escape
	// included, ends the wait (the editor still sees the key).
	if te.awaiting {
		te.awaiting = false
		if c, ok := Mnemonic(key); ok && ev.Mods&^ui.Shift == 0 {
			te.swallow = ev.Time
			te.a.SetBookmarkHere(te.id, string(c))
			return true
		}
		if key == ui.KeyEscape {
			return true
		}
	}
	switch {
	case ev.Mods == ui.Cmd && key == ui.KeyEnter:
		if !ev.Repeat {
			te.a.RunSmart(te.id)
		}
		return true
	case ev.Mods == ui.Alt|ui.Shift && key == ui.KeyF:
		if !ev.Repeat {
			te.a.Format(te.id)
		}
		return true
	case ev.Mods == ui.Alt|ui.Shift && key == ui.KeyE:
		if !ev.Repeat {
			te.a.ExplainSmart(te.id)
		}
		return true
	case ev.Mods == ui.Cmd && key == ui.KeyS:
		if !ev.Repeat {
			te.a.SaveTab(te.id)
		}
		return true
	case ev.Mods == ui.Ctrl && key == ui.KeyF11:
		te.awaiting = true
		return true
	case ev.Mods == ui.Ctrl|ui.Shift:
		if c, ok := Mnemonic(key); ok {
			te.awaiting = false
			if !ev.Repeat {
				te.a.SetBookmarkHere(te.id, string(c))
			}
			return true
		}
	}
	return false
}

// AwaitingMnemonic reports a Ctrl+F11 waiting for its character, which the run bar
// shows.
func (a *App) AwaitingMnemonic(id string) bool {
	te := a.editors[id]
	return te != nil && te.awaiting
}

// Frame is what the box around the caret's statement is drawn from.
type Frame struct {
	Start, End int
	// Lines lists, for each line of the statement, where its text starts
	// and ends as byte offsets; blank lines have no entry.
	Lines [][2]int
}

// StatementFrame measures the statement under the caret into the lines the
// view's frame spans (recall's outline: from the first character's column
// to the end of the widest line). Nil when there is none, or a selection
// stands for what a run would take.
func (a *App) StatementFrame(id string) *Frame {
	te := a.editors[id]
	if te == nil {
		return nil
	}
	if lo, hi := te.selectedRange(); lo != hi {
		return nil
	}
	stmt, ok := te.statementAtCaret()
	if !ok {
		return nil
	}
	text := te.st.Buffer.Slice(stmt.Start, stmt.End)
	f := &Frame{Start: stmt.Start, End: stmt.End}
	off := stmt.Start
	for _, line := range strings.SplitAfter(text, "\n") {
		trim := strings.TrimRight(line, " \t\r\n")
		if trim != "" {
			lead := len(trim) - len(strings.TrimLeft(trim, " \t"))
			f.Lines = append(f.Lines, [2]int{off + lead, off + len(trim)})
		}
		off += len(line)
	}
	return f
}

// retryCompletion asks again for a completion that found a list cold.
func (te *tabEditor) retryCompletion() {
	req := te.pending
	if req == nil {
		return
	}
	te.pending = nil
	if te.st.Buffer.Version() != req.Version {
		return
	}
	te.complete(*req)
}

const (
	scanLimit      = 64_000
	maxSchemaItems = 500
)

// complete answers the editor's request: schema names for the clause the
// caret is in, snippets by prefix and keywords of the dialect. A list not
// cached yet starts loading and the request is asked again when it lands,
// where recall waited up to 250 ms for it: nothing here blocks a keystroke.
func (te *tabEditor) complete(req editor.CompletionRequest) {
	at, items, cold := te.candidates(req)
	if cold {
		r := req
		te.pending = &r
	}
	// The text may have moved while a list loaded.
	if len(items) == 0 || te.st.Buffer.Version() != req.Version {
		return
	}
	te.st.ShowCompletions(at, items)
}

// candidates is what complete offers, where the word it replaces starts,
// and whether a list it wanted was still cold.
func (te *tabEditor) candidates(req editor.CompletionRequest) (at int, items []editor.Completion, cold bool) {
	tab, ok := te.a.TabByID(te.id)
	if !ok {
		return 0, nil, false
	}
	doc := te.st.Buffer.String()
	pos := min(req.Offset, len(doc))
	sStart, sEnd := sqltext.StatementBounds(doc, pos)
	before := sqltext.BlankOut(doc[max(sStart, pos-scanLimit):pos])
	scope := sqltext.ParseStatementScope(doc[sStart:min(sEnd, sStart+scanLimit)])
	key, haveKey := te.a.tabKey(tab)

	if qualifier, partial, ok := sqltext.QualifiedAt(before); ok {
		items, cold = te.qualifiedItems(key, haveKey, scope, qualifier)
		return pos - len(partial), items, cold
	}
	at = sqltext.WordBefore(doc, pos)
	prefix := doc[at:pos]
	if prefix == "" && !sqltext.OpenClauseTail.MatchString(before) && !req.Explicit {
		return at, nil, false
	}
	switch sqltext.SchemaClauseFor(before) {
	case sqltext.ClauseTables:
		for _, c := range scope.CTEs {
			items = append(items, editor.Completion{Label: c, Detail: "CTE", Kind: editor.CompletionModule})
		}
		if haveKey {
			tables := te.a.catalogTables(key)
			cold = tables == nil
			items = append(items, tableItems(tables)...)
		}
	case sqltext.ClauseColumns:
		if haveKey {
			seen := map[string]bool{}
			for _, table := range scope.Tables {
				cols := te.a.catalogColumns(key, table)
				cold = cold || cols == nil
				for _, c := range cols {
					if lc := strings.ToLower(c.Name); !seen[lc] {
						seen[lc] = true
						items = append(items, columnItem(c))
					}
				}
			}
		}
	}
	if len(items) > maxSchemaItems {
		items = items[:maxSchemaItems]
	}
	if prefix != "" {
		items = append(items, te.snippetItems(prefix)...)
		items = append(items, keywordItems(te.a.dialect(tab), prefix)...)
	}
	return at, items, cold
}

// qualifiedItems answers `alias.` with that table's columns, and `schema.`
// with that schema's tables; nil when the qualifier names neither (a word
// is not a qualifier just because a dot follows it).
func (te *tabEditor) qualifiedItems(key schemaKey, haveKey bool, scope sqltext.StatementScope, qualifier string) (items []editor.Completion, cold bool) {
	if !haveKey {
		return nil, false
	}
	if table, ok := scope.Aliases[strings.ToLower(qualifier)]; ok {
		cols := te.a.catalogColumns(key, table)
		for _, c := range cols {
			items = append(items, columnItem(c))
		}
		return items, cols == nil
	}
	other := key
	other.schema = qualifier
	tables := te.a.catalogTables(other)
	return tableItems(tables), tables == nil
}

func tableItems(tables []driver.TableInfo) []editor.Completion {
	items := make([]editor.Completion, 0, len(tables))
	for _, t := range tables {
		detail := t.Type
		if detail == "" {
			detail = "table"
		}
		items = append(items, editor.Completion{Label: t.Name, Detail: detail, Kind: editor.CompletionModule})
	}
	return items
}

func columnItem(c driver.ColumnInfo) editor.Completion {
	detail := c.DataType
	if c.PrimaryKey {
		detail += " PK"
	}
	return editor.Completion{Label: c.Name, Detail: detail, Kind: editor.CompletionField}
}

func (te *tabEditor) snippetItems(prefix string) []editor.Completion {
	key := strings.ToLower(strings.TrimSpace(prefix))
	var items []editor.Completion
	for _, s := range defaultSnippets {
		if !strings.HasPrefix(s.Prefix, key) {
			continue
		}
		sn, err := editor.ParseSnippet(s.Body)
		if err != nil {
			continue
		}
		items = append(items, editor.Completion{Label: s.Prefix, Detail: s.Label, Kind: editor.CompletionSnippet, Snippet: sn})
	}
	// A shorter prefix wins among equals: `sel` before `select_lock`.
	slices.SortStableFunc(items, func(a, b editor.Completion) int { return len(a.Label) - len(b.Label) })
	return items
}

func keywordItems(d sqltext.Dialect, prefix string) []editor.Completion {
	p := strings.ToLower(prefix)
	var items []editor.Completion
	for _, k := range sqltext.Keywords(d) {
		if strings.HasPrefix(k, p) {
			items = append(items, editor.Completion{Label: k, Kind: editor.CompletionKeyword})
		}
	}
	return items
}
