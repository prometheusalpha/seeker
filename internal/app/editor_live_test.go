package app

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui/editor"

	"seeker/internal/model"
	"seeker/internal/testenv"
)

// liveApp connects the profile and returns the app with its pump.
func liveApp(t *testing.T, profile model.Profile, password string) (*App, *pump) {
	t.Helper()
	testenv.SQLite(t)
	dir := t.TempDir()
	core, err := New(context.Background(), filepath.Join(dir, "seeker.db"), filepath.Join(dir, "recall.db"))
	if err != nil {
		t.Fatal(err)
	}
	p := newPump()
	core.Attach(p.update)
	t.Cleanup(core.Close)
	core.profiles = []model.Profile{profile}
	core.conn(profile.ID).setPassword(password)
	core.Connect(profile.ID)
	p.waitFor(t, "connection", func() bool { return core.Status(profile.ID) == Connected })
	return core, p
}

func livePG(t *testing.T) (model.Profile, string) {
	t.Helper()
	profile, password := pgProfile(t)
	if profile.Database == "" {
		profile.Database = "recall_test"
	}
	return profile, password
}

func liveTab(a *App, p model.Profile, sql string) *tabEditor {
	schema := "public"
	if p.DBType == model.MySQL {
		schema = p.Database
	}
	a.OpenQueryTab(QueryTab{Conn: p.ID, Database: p.Database, Schema: schema, Title: "live", SQL: sql, ForceNew: true})
	t, _ := a.ActiveTab()
	return a.tabEditor(t.ID)
}

func (te *tabEditor) waitRun(t *testing.T, p *pump) *runState {
	t.Helper()
	p.waitFor(t, "the run to end", func() bool { return !te.run.Running })
	return &te.run
}

// Run narrows to the statement under the caret, keeps the session of the
// tab between runs, and puts a server's error in the text it came from.
func TestLiveRunPostgres(t *testing.T) {
	profile, password := livePG(t)
	a, p := liveApp(t, profile, password)
	te := liveTab(a, profile, "select 1 as one;\nselect 2 as two;\nselect * from seeker_no_such_table;\n")

	// The caret in the second statement runs that statement alone.
	te.st.Select(20, 20)
	a.RunSmart(te.id)
	run := te.waitRun(t, p)
	if len(run.Results) != 1 || run.Results[0].Columns[0].Name != "two" || run.Base != 17 {
		t.Fatalf("caret run: base %d, %+v", run.Base, run.Results)
	}

	// The whole file when the caret is in the blank: the last one fails and
	// its error sits on the table's name.
	te.st.Select(len(te.st.Buffer.String()), len(te.st.Buffer.String()))
	a.RunSmart(te.id)
	run = te.waitRun(t, p)
	if len(run.Results) != 3 || run.Results[2].Err == nil || run.Results[0].Err != nil {
		t.Fatalf("file run: %+v", run.Results)
	}
	diags := te.st.Diagnostics()
	want := strings.Index(te.st.Buffer.String(), "seeker_no_such_table")
	if len(diags) != 1 || diags[0].Start != want || diags[0].End != want+len("seeker_no_such_table") {
		t.Fatalf("diagnostics %+v, want a range at %d", diags, want)
	}
	if place := a.ErrorPlace(te.id, run.Results[2]); place != "line 3, column 15" {
		t.Errorf("place %q", place)
	}

	// A selection of two statements runs just those; the old error clears.
	te.st.Select(0, 33)
	a.RunSmart(te.id)
	run = te.waitRun(t, p)
	if len(run.Results) != 2 || len(te.st.Diagnostics()) != 0 {
		t.Fatalf("selection run: %d results, diags %+v", len(run.Results), te.st.Diagnostics())
	}

	// The tab's session is its own: SET survives to the next run.
	te.st.Buffer.Set("set application_name = 'seeker_m3'")
	a.RunAll(te.id)
	te.waitRun(t, p)
	te.st.Buffer.Set("show application_name")
	a.RunAll(te.id)
	run = te.waitRun(t, p)
	if len(run.Results) != 1 || run.Results[0].Rows[0][0] != "seeker_m3" {
		t.Fatalf("session state lost: %+v", run.Results)
	}

	// Cmd+Enter while running stops it, and the tab runs again after.
	te.st.Buffer.Set("select pg_sleep(30)")
	a.RunSmart(te.id)
	if !te.run.Running {
		t.Fatal("not running")
	}
	time.Sleep(200 * time.Millisecond)
	start := time.Now()
	a.RunSmart(te.id)
	run = te.waitRun(t, p)
	if !run.Stopped || time.Since(start) > 8*time.Second {
		t.Fatalf("stop: %+v after %v", run, time.Since(start))
	}
	te.st.Buffer.Set("select 5 as five")
	a.RunAll(te.id)
	run = te.waitRun(t, p)
	if len(run.Results) != 1 || run.Results[0].Err != nil {
		t.Fatalf("after a stop: %+v", run.Results[0].Err)
	}

	// Explain never executes: a DELETE's plan leaves the rows.
	te.st.Buffer.Set("select 1 as one;\nselect * from pg_class where false;")
	te.st.Select(20, 20)
	a.ExplainSmart(te.id)
	p.waitFor(t, "the plan", func() bool { return !te.run.Explaining })
	if te.run.Plan == nil || !strings.Contains(te.run.Plan.Raw, "Plan") || te.run.PlanErr != "" {
		t.Fatalf("plan %+v, err %q", te.run.Plan, te.run.PlanErr)
	}
}

// Completion reads the real catalog: the first ask is cold and starts the
// load, the one after it has the columns.
func TestLiveCompletionPostgres(t *testing.T) {
	profile, password := livePG(t)
	fixtures(t, profile)
	a, p := liveApp(t, profile, password)
	profile.DefaultSchema = "seeker_m2"
	a.profiles = []model.Profile{profile}
	a.OpenQueryTab(QueryTab{Conn: profile.ID, Database: profile.Database, Schema: "seeker_m2", SQL: "select * from seeker_m2_users where ", ForceNew: true})
	tab, _ := a.ActiveTab()
	te := a.tabEditor(tab.ID)
	end := len(te.st.Buffer.String())
	te.st.Select(end, end)
	a.SyncEditors() // warms the tab: its tables, and the columns its script names

	req := func() ([]string, bool) {
		_, items, cold := te.candidates(editorRequest(te, end))
		return labels(items), cold
	}
	p.waitFor(t, "columns", func() bool {
		got, cold := req()
		return !cold && contains(got, "email") && contains(got, "created_at")
	})
	got, _ := req()
	if !contains(got, "account_id") {
		t.Errorf("columns: %v", got)
	}
	// Tables of the schema after FROM.
	te.st.Buffer.Set("select * from seeker_m2_")
	end = len(te.st.Buffer.String())
	p.waitFor(t, "tables", func() bool {
		got, _ := req()
		return contains(got, "seeker_m2_users") && contains(got, "seeker_m2_accounts")
	})
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func mysqlLive(t *testing.T) (model.Profile, string) {
	t.Helper()
	raw := os.Getenv("SEEKER_TEST_MYSQL_URL")
	if raw == "" {
		t.Skip("SEEKER_TEST_MYSQL_URL is not set; start recall/compose.yaml")
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(u.Port())
	password, _ := u.User.Password()
	return model.Profile{
		ID: "live-my", Name: "live-my", DBType: model.MySQL, Host: u.Hostname(), Port: port,
		Username: u.User.Username(), Database: strings.TrimPrefix(u.Path, "/"), ConnectTimeoutSecs: 5,
	}, password
}

// MySQL reports no error position, so the error marks its whole statement;
// Stop kills the query on the server.
func TestLiveRunMySQL(t *testing.T) {
	profile, password := mysqlLive(t)
	a, p := liveApp(t, profile, password)
	te := liveTab(a, profile, "select 1;\nselect * from seeker_no_such_table;")
	te.st.Select(len(te.st.Buffer.String()), len(te.st.Buffer.String()))
	a.RunAll(te.id)
	run := te.waitRun(t, p)
	if len(run.Results) != 2 || run.Results[1].Err == nil {
		t.Fatalf("results %+v", run.Results)
	}
	diags := te.st.Diagnostics()
	if len(diags) != 1 || diags[0].Start != 10 || diags[0].End != len(te.st.Buffer.String()) {
		t.Fatalf("diagnostics %+v", diags)
	}

	te.st.Buffer.Set("select sleep(30)")
	a.RunSmart(te.id)
	time.Sleep(300 * time.Millisecond)
	start := time.Now()
	a.Stop(te.id)
	run = te.waitRun(t, p)
	if !run.Stopped || time.Since(start) > 8*time.Second {
		t.Fatalf("stop after %v: %+v", time.Since(start), run)
	}
	te.st.Buffer.Set("select 5 as five")
	a.RunAll(te.id)
	run = te.waitRun(t, p)
	if len(run.Results) != 1 || run.Results[0].Err != nil {
		t.Fatalf("after a stop: %+v", run.Results)
	}
}

func editorRequest(te *tabEditor, offset int) editor.CompletionRequest {
	return editor.CompletionRequest{Offset: offset, Explicit: true, Version: te.st.Buffer.Version()}
}
