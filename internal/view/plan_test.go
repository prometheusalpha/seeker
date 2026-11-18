package view

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"seeker/internal/app"
	"seeker/internal/driver"
	"seeker/internal/explain"
	"seeker/internal/model"
	"seeker/internal/sqltext"
)

// showPlan puts a plan from the parser's fixtures in the tab's Explain pane.
func showPlan(t *testing.T, core *app.App, fixture string) *explain.Plan {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "explain", "testdata", fixture))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := explain.Parse(sqltext.Postgres, string(raw))
	if err != nil {
		t.Fatal(err)
	}
	id := activeID(core)
	run := core.RunViewOf(id)
	run.Plan, run.Parsed, run.PlanSQL = &driver.Plan{SQL: "EXPLAIN select 1", Raw: plan.Raw}, plan, "select 1"
	core.PaneOf(id).View = app.ViewExplain
	return plan
}

func TestPlanViewer(t *testing.T) {
	profile := model.Profile{ID: "x", Name: "nowhere", DBType: model.Postgres, Host: "127.0.0.1", Port: 1, Username: "u", Database: "d", ConnectTimeoutSecs: 2}
	core, p, tt := editorWindow(t, profile, "", "select 1")
	plan := showPlan(t, core, "pg_join.json")
	p.frame(tt)

	if !tt.HasText("Explain plan") || !tt.HasText("Canvas") {
		t.Fatalf("no viewer header: %v", tt.Texts())
	}
	root := plan.Nodes[0]
	if !tt.HasText(root.NodeType) {
		t.Fatalf("the canvas does not show %q: %v", root.NodeType, tt.Texts())
	}
	// The inspector opens on the costliest node without a click.
	if !tt.HasText("COST SHARE") {
		t.Errorf("no inspector on the costliest node: %v", tt.Texts())
	}
	tt.Click(root.NodeType)
	p.frame(tt)
	if !tt.HasText("COST SHARE") || !tt.HasText("DETAILS") && len(root.Details) > 0 {
		t.Errorf("no inspector after choosing a node: %v", tt.Texts())
	}

	tt.Click("Tree")
	p.frame(tt)
	if !tt.HasText("Copy") || !tt.HasText(root.NodeType) {
		t.Errorf("tree view: %v", tt.Texts())
	}
	tt.Click("Raw")
	p.frame(tt)
	if !tt.HasText("Raw plan") || !tt.HasText("SQL") {
		t.Errorf("raw view: %v", tt.Texts())
	}
}

// A plan the parser does not read opens raw, with no mode switch.
func TestPlanViewerTextPlanOpensRaw(t *testing.T) {
	profile := model.Profile{ID: "x", Name: "nowhere", DBType: model.Postgres, Host: "127.0.0.1", Port: 1, Username: "u", Database: "d", ConnectTimeoutSecs: 2}
	core, p, tt := editorWindow(t, profile, "", "select 1")
	id := activeID(core)
	run := core.RunViewOf(id)
	run.Plan, run.Parsed, run.PlanSQL = &driver.Plan{Raw: "Seq Scan on t"}, &explain.Plan{Raw: "Seq Scan on t"}, "select 1"
	core.PaneOf(id).View = app.ViewExplain
	p.frame(tt)
	if !tt.HasText("Raw plan") || tt.HasText("Canvas") {
		t.Errorf("a text plan should open raw: %v", tt.Texts())
	}
}

func TestPlanCanvasZoomSteps(t *testing.T) {
	profile := model.Profile{ID: "x", Name: "nowhere", DBType: model.Postgres, Host: "127.0.0.1", Port: 1, Username: "u", Database: "d", ConnectTimeoutSecs: 2}
	core, p, tt := editorWindow(t, profile, "", "select 1")
	showPlan(t, core, "pg_join.json")
	p.frame(tt)
	p.frame(tt) // the fit waits for the viewport's size
	before := zoomText(tt)
	tt.Click("Zoom in")
	p.frame(tt)
	if after := zoomText(tt); after == before {
		t.Errorf("zoom stayed at %s", before)
	}
	tt.Click("Fit to view")
	p.frame(tt)
	p.frame(tt)
	if got := zoomText(tt); got != before {
		t.Errorf("fit gave %s, the first fit %s", got, before)
	}
}

func zoomText(tt *ui.Tester) string {
	for _, s := range tt.Texts() {
		if strings.HasSuffix(s, "%") && !strings.Contains(s, " ") && !strings.HasPrefix(s, "<") && s != "0.0%" {
			if _, err := strconv.Atoi(strings.TrimSuffix(s, "%")); err == nil {
				return s
			}
		}
	}
	return ""
}
