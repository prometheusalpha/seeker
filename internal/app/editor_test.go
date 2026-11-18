package app

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"seeker/internal/driver"
	"seeker/internal/model"
	"seeker/internal/testenv"
)

// pumpApp is an app whose worker results wait for the test to apply them,
// as the window would on its next frame.
func pumpApp(t *testing.T) (*App, *pump) {
	t.Helper()
	testenv.SQLite(t)
	dir := t.TempDir()
	a, err := New(context.Background(), filepath.Join(dir, "seeker.db"), filepath.Join(dir, "absent.db"))
	if err != nil {
		t.Fatal(err)
	}
	p := newPump()
	a.Attach(p.update)
	t.Cleanup(a.Close)
	return a, p
}

func queryTab(a *App, id, sql string) *tabEditor {
	a.tabs.Items = append(a.tabs.Items, Tab{ID: id, Title: id, Conn: "c", Mode: ModeQuery, SQL: sql, SavedSQL: sql})
	a.tabs.Active = len(a.tabs.Items) - 1
	return a.tabEditor(id)
}

func TestEachTabHasItsOwnBuffer(t *testing.T) {
	a := newTestApp(t)
	one, two := queryTab(a, "1", "select 1"), queryTab(a, "2", "select 2")
	if one.st.Buffer == two.st.Buffer {
		t.Fatal("tabs share a buffer")
	}
	one.st.Buffer.Edit(editor.Edit{Start: 8, End: 8, Text: "0"})
	two.st.Buffer.Edit(editor.Edit{Start: 8, End: 8, Text: "9"})
	a.SyncEditors()
	if a.tabs.Items[0].SQL != "select 10" || a.tabs.Items[1].SQL != "select 29" {
		t.Fatalf("tab SQL: %q, %q", a.tabs.Items[0].SQL, a.tabs.Items[1].SQL)
	}
	if !a.TabDirty(a.tabs.Items[0]) {
		t.Error("an edited tab is not dirty")
	}
	// Undo is the tab's own: tab 1's undo leaves tab 2's text alone.
	one.st.Buffer.Undo()
	if one.st.Buffer.String() != "select 1" || two.st.Buffer.String() != "select 29" {
		t.Fatalf("undo crossed tabs: %q, %q", one.st.Buffer.String(), two.st.Buffer.String())
	}
	a.SyncEditors()
	if a.TabDirty(a.tabs.Items[0]) {
		t.Error("undoing back to the saved text left the tab dirty")
	}
	// Closing a tab drops its editor.
	a.forceClose("2")
	a.SyncEditors()
	if _, ok := a.editors["2"]; ok {
		t.Error("the closed tab kept its editor")
	}
}

// Cmd+Enter narrows as the user narrowed: selection, statement, file.
func TestSmartRunTarget(t *testing.T) {
	a := newTestApp(t)
	const sql = "select 1;\n\nselect 2;\nselect 3;\n  \n"
	te := queryTab(a, "t", sql)
	check := func(what, wantScript string, wantBase int, wantOK bool) {
		t.Helper()
		script, base, ok := te.smartTarget()
		if script != wantScript || base != wantBase || ok != wantOK {
			t.Errorf("%s: got %q at %d (%v), want %q at %d (%v)", what, script, base, ok, wantScript, wantBase, wantOK)
		}
	}
	te.st.Select(12, 12)
	check("caret in the second statement", "select 2;", 11, true)
	te.st.Select(0, 0)
	check("caret at the start", "select 1;", 0, true)
	te.st.Select(0, 20)
	check("a selection beats the statement", sql[0:20], 0, true)
	te.st.Select(10, 10)
	if _, _, ok := te.smartTarget(); ok {
		t.Error("a caret in the blank between statements picked a statement")
	}
	te.st.Select(len(sql)-2, len(sql)-1)
	if _, _, ok := te.smartTarget(); ok {
		t.Error("a whitespace-only selection ran")
	}
}

func TestStatementFrame(t *testing.T) {
	a := newTestApp(t)
	te := queryTab(a, "t", "select 1;\n  select a,\n    b\n  from t;\n")
	te.st.Select(15, 15)
	f := a.StatementFrame("t")
	if f == nil {
		t.Fatal("no frame")
	}
	if f.Start != 12 || len(f.Lines) != 3 {
		t.Fatalf("frame: %+v", f)
	}
	// The first line starts at its first character, past the indent.
	if got := te.st.Buffer.Slice(f.Lines[0][0], f.Lines[0][1]); got != "select a," {
		t.Errorf("first line %q", got)
	}
	// A selection stands for what a run takes, so there is no frame.
	te.st.Select(0, 4)
	if a.StatementFrame("t") != nil {
		t.Error("a frame was drawn under a selection")
	}
}

func TestFormatIsOneUndoStep(t *testing.T) {
	a, p := pumpApp(t)
	te := queryTab(a, "t", "select a,b from t where x=1;\nselect 2;")
	te.st.Select(3, 3)
	a.Format("t")
	p.waitFor(t, "format", func() bool { return te.st.Buffer.String() != "select a,b from t where x=1;\nselect 2;" })
	got := te.st.Buffer.String()
	if !strings.HasSuffix(got, ";\nselect 2;") || !strings.Contains(got, "\n  a,\n  b\nFROM") && !strings.Contains(got, "\n  b\nfrom") {
		t.Errorf("formatted: %q", got)
	}
	if !strings.HasSuffix(got, "\nselect 2;") {
		t.Errorf("the second statement was touched: %q", got)
	}
	if !te.st.Buffer.Undo() || te.st.Buffer.String() != "select a,b from t where x=1;\nselect 2;" {
		t.Errorf("undo did not restore: %q", te.st.Buffer.String())
	}
}

// A format that finishes after the text moved on writes nothing: its range
// was measured in the old text.
func TestFormatDropsStaleResult(t *testing.T) {
	a, p := pumpApp(t)
	te := queryTab(a, "t", "select a,b from t;")
	te.st.Select(3, 3)
	a.Format("t")
	te.st.Buffer.Edit(editor.Edit{Start: 0, End: 0, Text: "-- hi\n"})
	time.Sleep(300 * time.Millisecond)
	p.drain()
	if got := te.st.Buffer.String(); got != "-- hi\nselect a,b from t;" {
		t.Errorf("a stale format wrote: %q", got)
	}
}

func TestFormatFailureIsShortened(t *testing.T) {
	a, p := pumpApp(t)
	queryTab(a, "t", "select ((( from")
	a.Format("t")
	p.waitFor(t, "a toast", func() bool { return len(a.toasts) > 0 })
	msg := a.toasts[0].Message
	if !strings.HasPrefix(msg, "Could not format: ") || len(msg) > 260 || strings.Contains(msg, "\n") {
		t.Errorf("toast %q", msg)
	}
}

// seed fills the catalog cache as a loaded session would.
func seed(a *App, key schemaKey, tables map[string][]driver.ColumnInfo) {
	var ts []driver.TableInfo
	for name, cols := range tables {
		ts = append(ts, driver.TableInfo{Name: name, Type: "table"})
		a.cat.cols[columnKey{key, name}] = &entry[[]driver.ColumnInfo]{items: cols, fetchedAt: time.Now()}
	}
	a.cat.tables[key] = &entry[[]driver.TableInfo]{items: ts, fetchedAt: time.Now()}
}

func labels(items []editor.Completion) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.Label)
	}
	return out
}

func TestCompletions(t *testing.T) {
	a := newTestApp(t)
	a.profiles = []model.Profile{{ID: "c", Name: "c", DBType: model.Postgres, Database: "app"}}
	key := schemaKey{"c", "app", "public"}
	seed(a, key, map[string][]driver.ColumnInfo{
		"users":  {{Name: "id", DataType: "integer", PrimaryKey: true}, {Name: "email", DataType: "text"}},
		"orders": {{Name: "id", DataType: "integer"}, {Name: "total", DataType: "numeric"}},
	})
	ask := func(sql string, explicit bool) (int, []editor.Completion, bool) {
		te := queryTab(a, "t"+sql, strings.ReplaceAll(sql, "|", ""))
		return te.candidates(editor.CompletionRequest{Offset: strings.Index(sql, "|"), Explicit: explicit, Version: te.st.Buffer.Version()})
	}
	has := func(items []editor.Completion, want ...string) {
		t.Helper()
		got := labels(items)
		for _, w := range want {
			if !slices.Contains(got, w) {
				t.Errorf("no %q in %v", w, got)
			}
		}
	}

	_, items, _ := ask("select * from us|", false)
	has(items, "users", "orders")
	for _, it := range items {
		if it.Kind == editor.CompletionField {
			t.Errorf("a column offered after FROM: %v", it)
		}
	}

	at, items, _ := ask("select u.em| from users u", false)
	has(items, "id", "email")
	if at != len("select u.") {
		t.Errorf("the word starts at %d", at)
	}
	if slices.Contains(labels(items), "total") {
		t.Error("a column of another table through the alias")
	}

	_, items, _ = ask("select * from users u join orders o on o.id = u.id where |", true)
	has(items, "id", "email", "total")

	// A snippet by its prefix, and keywords.
	_, items, _ = ask("sel|", false)
	has(items, "sel", "select")
	var snippet *editor.Completion
	for i := range items {
		if items[i].Label == "sel" {
			snippet = &items[i]
		}
	}
	if snippet == nil || snippet.Snippet == nil || snippet.Kind != editor.CompletionSnippet {
		t.Errorf("the sel snippet: %+v", snippet)
	}

	// A list not cached yet is cold, which makes the request come back.
	a.cat.cols = map[columnKey]*entry[[]driver.ColumnInfo]{}
	_, _, cold := ask("select x from users where |", true)
	if !cold {
		t.Error("an empty cache was not cold")
	}
}

// Autosave writes after the pause, and never over a file that changed on
// disk.
func TestFileSaveGuardedByVersion(t *testing.T) {
	a, p := pumpApp(t)
	path := filepath.Join(t.TempDir(), "q.sql")
	if err := os.WriteFile(path, []byte("select 1"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.profiles = []model.Profile{{ID: "c", Name: "c", DBType: model.Postgres}}
	if _, err := a.ReadFile(path); err != nil {
		t.Fatal(err)
	}
	a.tabs.Items = []Tab{{ID: "t", Title: "q.sql", Conn: "c", Mode: ModeQuery, SQL: "select 1", SavedSQL: "select 1", Path: path}}
	a.tabs.Active = 0
	te := a.tabEditor("t")

	te.st.Buffer.Edit(editor.Edit{Start: 8, End: 8, Text: "0"})
	a.SyncEditors()
	if !a.TabDirty(a.tabs.Items[0]) {
		t.Fatal("not dirty")
	}
	p.waitFor(t, "autosave", func() bool { return !a.TabDirty(a.tabs.Items[0]) })
	if b, _ := os.ReadFile(path); string(b) != "select 10" {
		t.Fatalf("on disk: %q", b)
	}

	// Edited elsewhere: the next save is refused, and so is autosave until
	// the user decides.
	if err := os.WriteFile(path, []byte("changed outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	te.st.Buffer.Edit(editor.Edit{Start: 9, End: 9, Text: "0"})
	a.SyncEditors()
	p.waitFor(t, "a refusal", func() bool { return len(a.toasts) > 0 })
	if b, _ := os.ReadFile(path); string(b) != "changed outside" {
		t.Fatalf("the outside edit was overwritten: %q", b)
	}
	if !a.TabDirty(a.tabs.Items[0]) || !a.saveBlocked["t"] {
		t.Error("the refused tab should stay dirty and blocked")
	}
	toast := a.toasts[0]
	if toast.Action != "Overwrite" || toast.Run == nil {
		t.Fatalf("toast %+v", toast)
	}
	te.st.Buffer.Edit(editor.Edit{Start: 10, End: 10, Text: "0"})
	a.SyncEditors()
	time.Sleep(autosaveDelay + 200*time.Millisecond)
	p.drain()
	if b, _ := os.ReadFile(path); string(b) != "changed outside" {
		t.Fatalf("a blocked tab autosaved: %q", b)
	}
	toast.Run()
	p.waitFor(t, "overwrite", func() bool { return !a.TabDirty(a.tabs.Items[0]) })
	if b, _ := os.ReadFile(path); string(b) != "select 1000" {
		t.Fatalf("after overwrite: %q", b)
	}

	// A deleted file is reported, not recreated.
	os.Remove(path)
	te.st.Buffer.Edit(editor.Edit{Start: 0, End: 0, Text: " "})
	a.SyncEditors()
	a.toasts = nil
	a.SaveTab("t")
	p.waitFor(t, "missing", func() bool { return len(a.toasts) > 0 })
	if _, err := os.Stat(path); err == nil {
		t.Error("a missing file was recreated")
	}
}

// A tab restored from the store read nothing this session; its saved text
// is what it believes is on disk.
func TestRestoredFileTabGuard(t *testing.T) {
	a, p := pumpApp(t)
	path := filepath.Join(t.TempDir(), "r.sql")
	os.WriteFile(path, []byte("other"), 0o644)
	a.tabs.Items = []Tab{{ID: "t", Mode: ModeQuery, Path: path, SQL: "mine", SavedSQL: "was"}}
	a.tabs.Active = 0
	a.SaveTab("t")
	p.waitFor(t, "refusal", func() bool { return len(a.toasts) > 0 })
	if b, _ := os.ReadFile(path); string(b) != "other" {
		t.Errorf("overwrote %q", b)
	}
}

func TestSaveAsAdoptsTheFile(t *testing.T) {
	a, p := pumpApp(t)
	a.profiles = []model.Profile{{ID: "c", Name: "c", DBType: model.Postgres}}
	dest := filepath.Join(t.TempDir(), "new.sql")
	old := pickSave
	pickSave = func(name string) (string, error) { return dest, nil }
	t.Cleanup(func() { pickSave = old })
	queryTab(a, "t", "select 42")
	a.SaveTab("t")
	p.waitFor(t, "save as", func() bool { return a.tabs.Items[0].Path == dest })
	tab := a.tabs.Items[0]
	if b, _ := os.ReadFile(dest); string(b) != "select 42" || tab.Title != "new.sql" || a.TabDirty(tab) {
		t.Errorf("file %q, tab %+v", b, tab)
	}
	if ds, ok := a.BindingFor(dest); !ok || ds.Conn != "c" {
		t.Errorf("binding %+v %v", ds, ok)
	}
	// Cancel leaves the tab as it was.
	queryTab(a, "u", "select 7")
	pickSave = func(string) (string, error) { return "", nil }
	a.SaveTab("u")
	time.Sleep(50 * time.Millisecond)
	p.drain()
	if a.tabs.Items[1].Path != "" {
		t.Error("a cancelled Save As gave the tab a path")
	}
}

func TestBindingsAreBounded(t *testing.T) {
	a := newTestApp(t)
	for i := range maxBindings + 20 {
		a.SetFileBinding("/f/"+string(rune('a'+i%26))+strings.Repeat("x", i), model.Datasource{Conn: "c"})
	}
	if len(a.bindings) != maxBindings {
		t.Fatalf("%d bindings", len(a.bindings))
	}
	if _, ok := a.BindingFor("/f/a"); ok {
		t.Error("the oldest binding survived")
	}
	a.SetFileBinding("/f/new", model.Datasource{Conn: "d", Database: "x"})
	if ds, _ := a.BindingFor("/f/new"); ds.Database != "x" || a.bindings[0].Path != "/f/new" {
		t.Errorf("newest first: %+v", a.bindings[0])
	}
}

func TestResolveDatasource(t *testing.T) {
	a := newTestApp(t)
	a.profiles = []model.Profile{
		{ID: "r", DBType: model.Redis},
		{ID: "p", DBType: model.Postgres, Database: "app"},
		{ID: "m", DBType: model.MySQL, Database: "shop"},
	}
	if ds, ok := a.ResolveDatasource("/x.sql"); !ok || ds.Conn != "p" || ds.Schema != "public" {
		t.Errorf("fallback skips Redis: %+v %v", ds, ok)
	}
	a.SetFileBinding("/x.sql", model.Datasource{Conn: "m", Database: "shop", Schema: "shop"})
	if ds, _ := a.ResolveDatasource("/x.sql"); ds.Conn != "m" {
		t.Errorf("binding ignored: %+v", ds)
	}
	// A binding to a deleted connection falls through.
	a.SetFileBinding("/y.sql", model.Datasource{Conn: "gone"})
	if ds, ok := a.ResolveDatasource("/y.sql"); !ok || ds.Conn != "p" {
		t.Errorf("dead binding: %+v", ds)
	}
}

func TestBookmarkAssignAndJump(t *testing.T) {
	a, p := pumpApp(t)
	a.profiles = []model.Profile{{ID: "c", Name: "c", DBType: model.Postgres}}
	path := filepath.Join(t.TempDir(), "b.sql")
	os.WriteFile(path, []byte("select 1;\nselect 2;\nselect 3;\n"), 0o644)

	// A scratch tab has no file for a bookmark to point at.
	queryTab(a, "scratch", "select 1")
	a.SetBookmarkHere("scratch", "a")
	if len(a.toasts) != 1 {
		t.Fatalf("toasts %v", a.toasts)
	}

	if err := a.OpenFile(path); err != nil {
		t.Fatal(err)
	}
	tab, _ := a.ActiveTab()
	te := a.tabEditor(tab.ID)
	te.st.Select(12, 12) // line 2
	a.SetBookmarkHere(tab.ID, "q")
	p.waitFor(t, "bookmark", func() bool { return len(a.bookmarks) == 1 })
	if marks := a.GutterBookmarks(tab.ID); len(marks) != 1 || marks[0].Offset != 10 || marks[0].Mnemonic != "q" {
		t.Fatalf("gutter %+v", marks)
	}
	if a.BookmarkOn(path, 2) != "q" {
		t.Error("BookmarkOn")
	}
	if !slices.Equal(a.BookmarkMnemonics(), []string{"q"}) {
		t.Errorf("mnemonics %v", a.BookmarkMnemonics())
	}

	// Jump from another tab: the file's tab shows, the caret lands on line 2.
	other := queryTab(a, "other", "")
	_ = other
	a.JumpBookmark("q")
	p.waitFor(t, "jump", func() bool { return a.jump != nil })
	a.SyncEditors()
	if got, _ := a.ActiveTab(); got.ID != tab.ID {
		t.Fatalf("active tab %s", got.ID)
	}
	if line := te.st.CursorPos().Line; line != 1 {
		t.Errorf("caret on line %d", line)
	}

	// Lines inserted above: the hash finds the line again.
	te.st.Buffer.Edit(editor.Edit{Start: 0, End: 0, Text: "-- a\n-- b\n"})
	a.SyncEditors()
	a.JumpBookmark("q")
	p.waitFor(t, "jump again", func() bool { return a.jump != nil })
	a.SyncEditors()
	if line := te.st.CursorPos().Line; line != 3 {
		t.Errorf("after edits above, caret on line %d, want 3", line)
	}
}

func TestMnemonicKeys(t *testing.T) {
	if len(mnemonicKeys) != 36 {
		t.Fatalf("%d keys", len(mnemonicKeys))
	}
	for k, c := range mnemonicKeys {
		back, ok := KeyOfMnemonic(string(c))
		if !ok || back != k {
			t.Errorf("%c: %v %v", c, back, ok)
		}
	}
}

// onInput takes the chords before the editor does, and the second step of
// Ctrl+F11 takes the character and the text it types.
func TestEditorChords(t *testing.T) {
	a, _ := pumpApp(t)
	te := queryTab(a, "t", "select 1")
	press := func(mods ui.Modifiers, key ui.Key) bool {
		return te.onInput(ui.InputEvent{Kind: ui.InputKeyDown, Key: key, PhysicalKey: key, Mods: mods})
	}
	if press(0, ui.KeyA) {
		t.Error("a plain key was taken")
	}
	if !press(ui.Alt|ui.Shift, ui.KeyF) || !press(ui.Ctrl|ui.Shift, ui.Key1) {
		t.Error("format or the shifted bookmark chord not taken")
	}
	// A bookmark on a scratch tab only toasts; the point is the key is taken.
	if !press(ui.Ctrl, ui.KeyF11) || !te.awaiting {
		t.Fatal("Ctrl+F11 did not start waiting")
	}
	now := time.Now()
	if !te.onInput(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyX, PhysicalKey: ui.KeyX, Time: now}) || te.awaiting {
		t.Error("the character after Ctrl+F11 was not taken")
	}
	if !te.onInput(ui.InputEvent{Kind: ui.InputText, Text: "x", Time: now.Add(10 * time.Millisecond)}) {
		t.Error("the text of that key was typed")
	}
	if te.onInput(ui.InputEvent{Kind: ui.InputText, Text: "x", Time: now.Add(time.Second)}) {
		t.Error("later text was swallowed")
	}
	// Escape ends the wait without a bookmark.
	press(ui.Ctrl, ui.KeyF11)
	if !press(0, ui.KeyEscape) || te.awaiting {
		t.Error("Escape did not end the wait")
	}
}
