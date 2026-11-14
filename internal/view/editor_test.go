package view

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"seeker/internal/app"
	"seeker/internal/model"
	"seeker/internal/store"
	"seeker/internal/testenv"
)

// editorWindow builds the window over a saved profile and an open query
// tab; the profile may point nowhere.
func editorWindow(t *testing.T, profile model.Profile, password, sql string) (*app.App, *pump, *ui.Tester) {
	t.Helper()
	testenv.SQLite(t)
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "seeker.db")
	db, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SaveConnections(context.Background(), []model.Profile{profile}); err != nil {
		t.Fatal(err)
	}
	db.Close()
	core, err := app.New(context.Background(), dbPath, filepath.Join(dir, "recall.db"))
	if err != nil {
		t.Fatal(err)
	}
	p := &pump{}
	core.Attach(p.update)
	t.Cleanup(core.Close)
	core.SetSessionPassword(profile.ID, password)
	core.OpenQueryTab(app.QueryTab{Conn: profile.ID, Database: profile.Database, Schema: "public", SQL: sql, Title: "scratch", ForceNew: true})
	tt := ui.NewTester(New(core).View, 1280, 820)
	p.frame(tt)
	return core, p, tt
}

// clickEditor puts the caret in the editor, under the run bar.
func clickEditor(t *testing.T, tt *ui.Tester, dx, dy float32) {
	t.Helper()
	run, ok := tt.Find("Run")
	if !ok {
		t.Fatalf("no Run button: %v", tt.Texts())
	}
	tt.ClickAt(run.X+dx, run.Y+run.H+dy)
}

func (p *pump) waitUntil(t *testing.T, tt *ui.Tester, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		p.frame(tt)
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s; screen: %v", what, tt.Texts())
}

// With no server, running reports why in the results rather than hanging,
// and each tab shows its own text.
func TestRunWithoutServerReportsError(t *testing.T) {
	profile := model.Profile{ID: "x", Name: "nowhere", DBType: model.Postgres, Host: "127.0.0.1", Port: 1, Username: "u", Database: "d", ConnectTimeoutSecs: 2}
	core, p, tt := editorWindow(t, profile, "", "select 1")
	clickEditor(t, tt, 100, 40)
	tt.Key(ui.Cmd, ui.KeyEnter)
	p.waitUntil(t, tt, "the error", func() bool {
		return !core.Running(activeID(core)) && tt.HasText("Stop") == false && hasAny(tt, "refused", "connect")
	})

	// Each tab shows its own buffer: the editor follows the active tab.
	first, _ := core.Editor(activeID(core))
	core.OpenQueryTab(app.QueryTab{Conn: "x", Database: "d", Schema: "public", SQL: "select 'other tab'", Title: "second", ForceNew: true})
	p.frame(tt)
	second, _ := core.Editor(activeID(core))
	if first == second || second.Buffer.String() != "select 'other tab'" || first.Buffer.String() != "select 1" {
		t.Errorf("tabs share an editor: %q, %q", first.Buffer.String(), second.Buffer.String())
	}
}

func activeID(core *app.App) string {
	tab, _ := core.ActiveTab()
	return tab.ID
}

func hasAny(tt *ui.Tester, parts ...string) bool {
	for _, s := range tt.Texts() {
		for _, p := range parts {
			if strings.Contains(strings.ToLower(s), p) {
				return true
			}
		}
	}
	return false
}

// Cmd+Enter, Stop, format and the frame, in the window against a real
// Postgres.
func TestLiveEditorKeys(t *testing.T) {
	raw := os.Getenv("SEEKER_TEST_PG_URL")
	if raw == "" {
		t.Skip("SEEKER_TEST_PG_URL is not set; start recall/compose.yaml")
	}
	profile, password := livePostgres(t, raw)
	core, p, tt := editorWindow(t, profile, password, "select 1 as one_col;\nselect 2 as two_col;\n")
	id := activeID(core)

	// The caret is in the first statement: only it runs.
	clickEditor(t, tt, 100, 15)
	tt.Key(ui.Cmd, ui.KeyEnter)
	p.waitUntil(t, tt, "the first statement's columns", func() bool { return tt.HasText("one_col") && !core.Running(id) })
	if tt.HasText("two_col") && hasRow(tt, "two_col") {
		t.Errorf("the whole file ran: %v", tt.Texts())
	}
	if !hasAny(tt, "1 rows") {
		t.Errorf("no row count: %v", tt.Texts())
	}

	// Format with Alt+Shift+F rewrites the statement.
	editorSt, _ := core.Editor(id)
	editorSt.Buffer.Set("select a,b from t;")
	editorSt.Select(3, 3)
	p.frame(tt)
	tt.Key(ui.Alt|ui.Shift, ui.KeyF)
	p.waitUntil(t, tt, "formatted text", func() bool { return strings.Contains(editorSt.Buffer.String(), "\n") })

	// A slow statement shows Stop, and Cmd+Enter again stops it.
	editorSt.Buffer.Set("select pg_sleep(30)")
	editorSt.Select(3, 3)
	p.frame(tt)
	tt.Key(ui.Cmd, ui.KeyEnter)
	p.waitUntil(t, tt, "Stop", func() bool { return core.Running(id) && tt.HasText("Stop") })
	tt.Key(ui.Cmd, ui.KeyEnter)
	p.waitUntil(t, tt, "the stop", func() bool { return !core.Running(id) && hasAny(tt, "stopped") })
}

// hasRow reports a result block that lists col as a header line (the
// statement's own text also holds the name).
func hasRow(tt *ui.Tester, col string) bool {
	for _, s := range tt.Texts() {
		if strings.HasPrefix(strings.TrimSpace(s), col) && !strings.Contains(s, "select") {
			return true
		}
	}
	return false
}

func livePostgres(t *testing.T, raw string) (model.Profile, string) {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(u.Port())
	password, _ := u.User.Password()
	return model.Profile{
		ID: "live", Name: "live-pg", DBType: model.Postgres, Host: u.Hostname(), Port: port,
		Username: u.User.Username(), Database: strings.TrimPrefix(u.Path, "/"), ConnectTimeoutSecs: 5,
	}, password
}

// A bookmark is set with Ctrl+Shift+<key> from the editor and jumped to with
// Cmd+<key> from anywhere; a shortcut registered earlier (Cmd+W) wins over
// a bookmark on its letter.
func TestBookmarkKeys(t *testing.T) {
	profile := model.Profile{ID: "x", Name: "nowhere", DBType: model.Postgres, Host: "127.0.0.1", Port: 1, Username: "u", Database: "d", ConnectTimeoutSecs: 2}
	core, p, tt := editorWindow(t, profile, "", "select 0")
	path := filepath.Join(t.TempDir(), "marks.sql")
	if err := os.WriteFile(path, []byte("select 1;\nselect 2;\nselect 3;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := core.OpenFile(path); err != nil {
		t.Fatal(err)
	}
	p.frame(tt)
	file := activeID(core)
	st, _ := core.Editor(file)
	st.Select(12, 12)
	clickEditor(t, tt, 400, 200)
	st.Select(12, 12)
	tt.Key(ui.Ctrl|ui.Shift, ui.Key1)
	// One mnemonic per line: a second on the line would take it over.
	st.Select(0, 0)
	tt.Key(ui.Ctrl|ui.Shift, ui.KeyW)
	p.waitUntil(t, tt, "two bookmarks", func() bool { return len(core.BookmarkMnemonics()) == 2 })

	// Jump from the scratch tab: Cmd+1 shows the file and puts the caret on line 2.
	core.Activate(core.Tabs().Items[0].ID)
	p.frame(tt)
	st.Select(0, 0)
	tt.Key(ui.Cmd, ui.Key1)
	p.waitUntil(t, tt, "the jump", func() bool { return activeID(core) == file && st.CursorPos().Line == 1 })

	// Cmd+W is the tab strip's, though a bookmark is bound to w.
	n := len(core.Tabs().Items)
	tt.Key(ui.Cmd, ui.KeyW)
	p.frame(tt)
	if len(core.Tabs().Items) != n-1 {
		t.Errorf("Cmd+W did not close the tab: %d tabs, was %d", len(core.Tabs().Items), n)
	}
}
