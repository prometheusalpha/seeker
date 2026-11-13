package explain

import (
	"errors"
	"math"
	"os"
	"strings"
	"testing"

	"seeker/internal/sqltext"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func parse(t *testing.T, d sqltext.Dialect, raw string) *Plan {
	t.Helper()
	p, err := Parse(d, raw)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPostgresJoin(t *testing.T) {
	p := parse(t, sqltext.Postgres, fixture(t, "pg_join.json"))
	if len(p.Nodes) != 1 {
		t.Fatalf("roots = %d", len(p.Nodes))
	}
	root := p.Nodes[0]
	if root.ID != "0" || root.NodeType != "Limit" || root.Title != "Limit" {
		t.Fatalf("root = %+v", root)
	}
	if root.Cost != "120.81..120.82" || root.Rows != "5" || root.Width != "72" {
		t.Fatalf("root numbers = %q %q %q", root.Cost, root.Rows, root.Width)
	}
	sort := root.Children[0]
	if sort.ID != "0.0" || sort.NodeType != "Sort" || len(sort.Details) != 1 || sort.Details[0] != "Sort: (count(*)) DESC" {
		t.Fatalf("sort = %+v", sort)
	}
	var scan *Node
	var walk func(n *Node)
	walk = func(n *Node) {
		if n.Relation == "pg_class" && scan == nil {
			scan = n
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
	if scan == nil || scan.Title != scan.NodeType+" on pg_class" {
		t.Fatalf("scan = %+v", scan)
	}
	if !strings.HasPrefix(p.Raw, "[\n  {") {
		t.Fatalf("raw not indented: %.20q", p.Raw)
	}
}

func TestPostgresAnalyzeDetails(t *testing.T) {
	// Hand-written: what a user's own EXPLAIN ANALYZE would send back.
	const raw = `[{"Plan": {"Node Type": "Hash Join", "Join Type": "Inner", "Startup Cost": 1.5, "Total Cost": 9.25,
	  "Plan Rows": 10, "Plan Width": 4, "Actual Startup Time": 0.1, "Actual Total Time": 0.9, "Actual Rows": 12, "Actual Loops": 3,
	  "Filter": "(a > 1)", "Plans": [{"Node Type": "Seq Scan", "Relation Name": "t", "Total Cost": 4, "Plan Rows": 1, "Actual Loops": 1}, "junk"]},
	  "Planning Time": 0.2, "Execution Time": 1.5}]`
	p := parse(t, sqltext.Postgres, raw)
	n := p.Nodes[0]
	want := []string{"Join: Inner", "Filter: (a > 1)", "Actual Rows: 12", "Actual Loops: 3", "Actual Time: 0.1..0.9 ms", "Planning Time: 0.2 ms", "Execution Time: 1.5 ms"}
	if strings.Join(n.Details, "|") != strings.Join(want, "|") {
		t.Fatalf("details = %q", n.Details)
	}
	if n.Cost != "1.5..9.25" {
		t.Fatalf("cost = %q", n.Cost)
	}
	if len(n.Children) != 1 || n.Children[0].Cost != "4" || len(n.Children[0].Details) != 0 {
		t.Fatalf("children = %+v", n.Children)
	}
	if got := ActualRows(n); !got.OK || got.V != 12 {
		t.Fatalf("actual = %+v", got)
	}
	if got := TreeText(p.Nodes); !strings.Contains(got, "Hash Join c:1.5..9.25 e:10 a:12\n  Seq Scan t c:4 e:1") {
		t.Fatalf("tree = %q", got)
	}
}

func TestPostgresMultipleStatements(t *testing.T) {
	p := parse(t, sqltext.Postgres, `[{"Plan":{"Node Type":"Result"}},{"Plan":{"Node Type":"Limit"}}]`)
	if len(p.Nodes) != 2 || p.Nodes[1].ID != "1" || p.Nodes[1].Cost != "" {
		t.Fatalf("nodes = %+v", p.Nodes)
	}
}

func TestMySQLBlock(t *testing.T) {
	p := parse(t, sqltext.MySQL, fixture(t, "mysql_block.json"))
	root := p.Nodes[0]
	if root.NodeType != "query_block" || root.Cost != "2.20" || root.Details[0] != "Select: 1" {
		t.Fatalf("root = %+v", root)
	}
	order := root.Children[0]
	if order.NodeType != "ordering_operation" || order.Children[0].NodeType != "grouping_operation" {
		t.Fatalf("wrappers = %+v", order)
	}
	loop := order.Children[0].Children
	if len(loop) != 2 {
		t.Fatalf("nested loop = %d tables", len(loop))
	}
	u := loop[0]
	if u.ID != "0.0.0.0" || u.Title != "index on u" || u.Index != "k_name" || u.Rows != "1" || u.Cost == "" {
		t.Fatalf("u = %+v", u)
	}
	if loop[1].Relation != "o" || loop[1].NodeType != "ref" {
		t.Fatalf("o = %+v", loop[1])
	}
}

func TestMySQLTreeShapes(t *testing.T) {
	// The wrapped shape recall read, and the unwrapped one 8.4 emits.
	for _, name := range []string{"mysql_query_plan.json", "mysql_plan.json"} {
		p := parse(t, sqltext.MySQL, fixture(t, name))
		root := p.Nodes[0]
		if len(p.Nodes) != 1 || root.Title != "Sort: s DESC" || root.NodeType != "sort" || root.Details[0] != "Sort: s DESC" {
			t.Fatalf("%s root = %+v", name, root)
		}
		if got := root.Children[0].Cost; got == "" {
			t.Fatalf("%s: step %+v has no cost", name, root.Children[0])
		}
		var types []string
		var walk func(n *Node)
		walk = func(n *Node) {
			types = append(types, n.NodeType)
			for _, c := range n.Children {
				walk(c)
			}
		}
		walk(root)
		if !strings.Contains(strings.Join(types, " "), "index") {
			t.Fatalf("%s types = %q", name, types)
		}
	}
}

func TestMySQLStepFallbacks(t *testing.T) {
	p := parse(t, sqltext.MySQL, `{"query_plan":{"inputs":[{"table_name":"t"},7,{}]}}`)
	root := p.Nodes[0]
	if root.Title != "step" || root.NodeType != "step" || len(root.Children) != 3 {
		t.Fatalf("root = %+v", root)
	}
	if root.Children[0].Title != "table on t" || root.Children[1].Title != "step" {
		t.Fatalf("children = %+v", root.Children)
	}
}

func TestNotJSONAndEmpty(t *testing.T) {
	p := parse(t, sqltext.Postgres, "Seq Scan on t  (cost=0.00..1.00)")
	if len(p.Nodes) != 0 || p.Raw != "Seq Scan on t  (cost=0.00..1.00)" {
		t.Fatalf("plan = %+v", p)
	}
	for _, raw := range []string{"", "  \n"} {
		if _, err := Parse(sqltext.MySQL, raw); !errors.Is(err, ErrEmpty) {
			t.Fatalf("Parse(%q) err = %v", raw, err)
		}
	}
	for _, raw := range []string{`42`, `null`, `[1,"a"]`, `{"a":1} trailing`} {
		if p, err := Parse(sqltext.Postgres, raw); err != nil || len(p.Nodes) != 0 {
			t.Fatalf("Parse(%q) = %+v, %v", raw, p, err)
		}
	}
}

func TestCategorize(t *testing.T) {
	for in, want := range map[string]Category{
		"Merge Join":           Join,
		"Merge":                Modify,
		"Clustered Index Scan": TableScan,
		"Index Scan":           IndexScan,
		"SORT AGGREGATE":       Aggregate,
		"Seq Scan":             TableScan,
		"eq_ref":               Lookup,
		"ALL":                  TableScan,
		"all rows":             Other,
		"ref":                  IndexScan,
		"Gather Merge":         Exchange,
		"ModifyTable":          Modify,
		"Update":               Modify,
		"Hash":                 Materialize,
		"Limit":                Result,
		"":                     Other,
		"join":                 Other,
	} {
		if got := Categorize(in); got != want {
			t.Errorf("Categorize(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestParseNumber(t *testing.T) {
	for in, want := range map[string]float64{
		"0.29..1830.12": 1830.12,
		"1,234":         1234,
		"96M":           96e6,
		"2.5k":          2500,
		"7":             7,
		"-3":            -3,
		".5":            0.5,
		" 12 rows":      12,
	} {
		if got := ParseNumber(in); !got.OK || math.Abs(got.V-want) > 1e-9 {
			t.Errorf("ParseNumber(%q) = %+v, want %v", in, got, want)
		}
	}
	for _, in := range []string{"", "abc", "..", "5..x"} {
		if got := ParseNumber(in); got.OK {
			t.Errorf("ParseNumber(%q) = %+v, want none", in, got)
		}
	}
}

func TestCanvasLayoutAndHeat(t *testing.T) {
	p := parse(t, sqltext.Postgres, fixture(t, "pg_join.json"))
	if p.Nodes[0].Children[0].Children[0].Cost != "118.97..119.66" {
		t.Fatalf("fixture drifted: %+v", p.Nodes[0].Children[0].Children[0])
	}
	l := BuildCanvas(p.Nodes)
	if len(l.Nodes) == 0 || len(l.Edges) != len(l.Nodes)-1 {
		t.Fatalf("nodes %d edges %d", len(l.Nodes), len(l.Edges))
	}
	root := l.Nodes[0]
	if root.X != Pad || root.Category != Result {
		t.Fatalf("root = %+v", root)
	}
	// Leaves stack NodeH+GapY apart; a parent sits between first and last child.
	var leaves []*CanvasNode
	for _, n := range l.Nodes {
		if len(n.Node.Children) == 0 {
			leaves = append(leaves, n)
		}
	}
	for i, n := range leaves {
		if want := Pad + float64(i)*(NodeH+GapY); n.Y != want {
			t.Errorf("leaf %d y = %v, want %v", i, n.Y, want)
		}
	}
	for _, n := range l.Nodes {
		if k := n.Node.Children; len(k) > 0 {
			var first, last *CanvasNode
			for _, c := range l.Nodes {
				if c.Node == k[0] {
					first = c
				}
				if c.Node == k[len(k)-1] {
					last = c
				}
			}
			if n.Y != (first.Y+last.Y)/2 {
				t.Errorf("%s not centred", n.Node.ID)
			}
		}
	}
	var sum float64
	for _, n := range l.Nodes {
		sum += n.CostShare.V
	}
	if math.Abs(sum-1) > 1e-9 {
		t.Fatalf("shares sum to %v", sum)
	}
	hot := l.Hottest()
	for _, n := range l.Nodes {
		if n.CostShare.V > hot.CostShare.V {
			t.Fatalf("%s is hotter than the hottest", n.Node.ID)
		}
	}
	if l.Width <= 0 || l.Height <= 0 {
		t.Fatalf("size %vx%v", l.Width, l.Height)
	}
}

func TestCanvasSelfCostHand(t *testing.T) {
	// parent 10 over children 4 and 4: self-costs 2, 4, 4.
	root := &Node{NodeType: "Hash Join", Cost: "0..10", Children: []*Node{
		{NodeType: "Seq Scan", Cost: "4"}, {NodeType: "Seq Scan", Cost: "4"},
	}}
	l := BuildCanvas([]*Node{root})
	shares := []float64{0.2, 0.4, 0.4}
	for i, n := range l.Nodes {
		if math.Abs(n.CostShare.V-shares[i]) > 1e-9 {
			t.Errorf("share %d = %v", i, n.CostShare.V)
		}
	}
	if l.Hottest() != l.Nodes[1] {
		t.Errorf("tie must keep the first hottest")
	}
	if HeatOf(l.Nodes[0].CostShare) != HeatWarm || HeatOf(l.Nodes[1].CostShare) != HeatHot {
		t.Errorf("heat")
	}
	if l.Width != Pad+(NodeW+GapX)+NodeW+Pad || l.Height != Pad+NodeH+GapY+NodeH+Pad {
		t.Errorf("size %vx%v", l.Width, l.Height)
	}
	// Children of the root: leaves at y = Pad and Pad+NodeH+GapY; root centred.
	if l.Nodes[0].Y != Pad+(NodeH+GapY)/2.0 {
		t.Errorf("root y = %v", l.Nodes[0].Y)
	}
}

func TestCanvasEmptyAndNoCost(t *testing.T) {
	l := BuildCanvas(nil)
	if l.Width != 0 || l.Height != 0 || l.Hottest() != nil {
		t.Fatalf("empty = %+v", l)
	}
	l = BuildCanvas([]*Node{{NodeType: "Result"}})
	if l.Nodes[0].CostShare.OK || HeatOf(l.Nodes[0].CostShare) != HeatNone || l.Hottest() != l.Nodes[0] {
		t.Fatalf("no cost = %+v", l.Nodes[0])
	}
}

func TestCanvasFallsBackToTitle(t *testing.T) {
	l := BuildCanvas([]*Node{{NodeType: "join", Title: "Nested loop inner join"}})
	if l.Nodes[0].Category != Join {
		t.Fatalf("category = %v", l.Nodes[0].Category)
	}
}

func TestHeatAndFormat(t *testing.T) {
	for share, want := range map[float64]Heat{0.04: HeatCool, 0.05: HeatWarm, 0.2: HeatWarm, 0.21: HeatHot} {
		if got := HeatOf(Num{share, true}); got != want {
			t.Errorf("HeatOf(%v) = %v", share, got)
		}
	}
	for in, want := range map[float64]string{
		0: "0", 4.4: "4", 4.5: "5", 999: "999", 1000: "1K", 1250: "1.3K", 1150: "1.1K", 1234: "1.2K",
		2.5e6: "2.5M", 3e6: "3M", 999999: "1000K",
	} {
		if got := FormatRows(Num{in, true}); got != want {
			t.Errorf("FormatRows(%v) = %q, want %q", in, got, want)
		}
	}
	if FormatRows(Num{}) != "—" {
		t.Error("none")
	}
	if EdgeWidth(Num{}) != 1.6 || EdgeWidth(Num{0, true}) != 1.6 || EdgeWidth(Num{1e12, true}) != 8 {
		t.Error("edge width")
	}
	if w := EdgeWidth(Num{999, true}); math.Abs(w-(1.6+3*1.05)) > 1e-9 {
		t.Errorf("edge width = %v", w)
	}
}
