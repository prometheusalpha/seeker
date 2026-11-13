package explain

import (
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
)

// Category groups operators by what they do; the viewer colours by it.
type Category int

const (
	Other Category = iota
	Result
	Sort
	Join
	TableScan
	IndexScan
	Lookup
	Materialize
	Aggregate
	Exchange
	Modify
)

var categoryNames = [...]string{"other", "result", "sort", "join", "tscan", "iscan", "lookup", "mat", "agg", "xchg", "mod"}

// String is recall's id for the category.
func (c Category) String() string { return categoryNames[c] }

const (
	NodeW = 212
	NodeH = 76
	GapX  = 84
	GapY  = 26
	Pad   = 36
)

// categoryRules are ordered because operator names overlap across engines:
// "Merge Join" is a join while a bare "Merge" is DML, "Clustered Index Scan"
// reads a whole table while "Index Scan" does not, and "SORT AGGREGATE" is an
// aggregate, not a sort. Never sort them. Most of the vocabulary is from
// engines recall also read (SQL Server, Oracle); it costs nothing to keep.
var categoryRules = []struct {
	re  *regexp.Regexp
	cat Category
}{
	// MySQL access types are single ambiguous words, so they only match exactly.
	{regexp.MustCompile(`^(?:eq_ref|const|system)$`), Lookup},
	{regexp.MustCompile(`^(?:ref|range|index|index_merge|ref_or_null|fulltext)$`), IndexScan},
	{regexp.MustCompile(`^all$`), TableScan},

	{regexp.MustCompile(`(?:key|rid|row|rowid|heap)\s+lookup|table access by .*rowid`), Lookup},
	{regexp.MustCompile(`hash join|hash match|merge join|nested loops?|nest loop|hash2|cartesian`), Join},
	{regexp.MustCompile(`aggregate|group by|grouping_operation|hagr\d*|sagr\d*|\bagg\b`), Aggregate},
	{regexp.MustCompile(`sort|ordering_operation`), Sort},
	{regexp.MustCompile(`gather|parallelism|px send|px receive|px block|exchange|repartition|distribute`), Exchange},
	{regexp.MustCompile(`^(?:insert|update|delete|replace)\b|^merge$|modifytable|table (?:insert|update|delete|merge)|clustered index (?:insert|update|delete|merge)|load table|\bdml\b`), Modify},
	{regexp.MustCompile(`materiali[sz]|spool|\bhash\b|\bcte\b|temp table transformation|worktable`), Materialize},
	{regexp.MustCompile(`clustered index scan|seq scan|sequential scan|table scan|table access full|table access storage full|cscn\d*`), TableScan},
	{regexp.MustCompile(`index.*(?:scan|seek)|bitmap heap scan|ssek\d*|csek\d*|bmsk\d*`), IndexScan},
	{regexp.MustCompile(`result|^select\b|select statement|limit|query_block|nset\d*|prjt\d*|union|\btop\b`), Result},
}

// Categorize places an operator name; Other when no rule does.
func Categorize(nodeType string) Category {
	n := strings.ToLower(strings.TrimSpace(nodeType))
	if n == "" {
		return Other
	}
	for _, r := range categoryRules {
		if r.re.MatchString(n) {
			return r.cat
		}
	}
	return Other
}

// Num is an optional number: plans leave out what an operator does not report.
type Num struct {
	V  float64
	OK bool
}

var numRe = regexp.MustCompile(`(?i)^([+-]?[\d,]*\.?\d+)\s*(?:([kmgt])\b)?`)

// ParseNumber reads the numeric part of an engine-native cost or rows string:
// "0.29..1830.12", "1,234", "96M".
func ParseNumber(s string) Num {
	s = strings.TrimSpace(s)
	// Postgres reports "startup..total"; the total is what matters.
	if i := strings.LastIndex(s, ".."); i >= 0 {
		s = strings.TrimSpace(s[i+2:])
	}
	m := numRe.FindStringSubmatch(s)
	if m == nil {
		return Num{}
	}
	v, err := strconv.ParseFloat(strings.ReplaceAll(m[1], ",", ""), 64)
	if err != nil || math.IsInf(v, 0) {
		return Num{}
	}
	switch strings.ToLower(m[2]) {
	case "k":
		v *= 1e3
	case "m":
		v *= 1e6
	case "g":
		v *= 1e9
	case "t":
		v *= 1e12
	}
	return Num{v, true}
}

var (
	perExecRe = regexp.MustCompile(`(?i)Actual Rows Per Execution:\s*(\S+)`)
	actualRe  = regexp.MustCompile(`(?i)Actual Rows:\s*(\S+)`)
)

// ActualRows is the measured row count, when the plan ran. Estimates are per
// execution, so the per-execution detail wins when an engine reports
// cumulative actuals (SQL Server); Postgres never emits that key.
func ActualRows(n *Node) Num {
	var total Num
	for _, d := range n.Details {
		if m := perExecRe.FindStringSubmatch(d); m != nil {
			return ParseNumber(m[1])
		}
		if total.OK {
			continue
		}
		if m := actualRe.FindStringSubmatch(d); m != nil {
			total = ParseNumber(m[1])
		}
	}
	return total
}

// CanvasNode is a Node placed on the canvas; X and Y are its top-left corner,
// NodeW by NodeH in size.
type CanvasNode struct {
	Node     *Node
	Category Category
	X, Y     float64
	// CostShare is the node's self-cost as a fraction of the plan's total.
	CostShare  Num
	Rows       Num
	ActualRows Num
}

// Edge joins a parent to a child; Rows (the child's estimate) sets its width.
type Edge struct {
	From, To *CanvasNode
	Rows     Num
}

// Layout is a plan placed on a canvas of Width by Height.
type Layout struct {
	Nodes         []*CanvasNode
	Edges         []Edge
	Width, Height float64
}

// BuildCanvas lays the plan out with one column per tree depth, leaves
// stacked down their column and a parent centred between its first and last
// child. Costs are cumulative over the subtree in every engine recall
// targets, so a node's self-cost is its cost minus its children's; the heat
// is that as a share of all self-costs, which is what makes a hot node
// visible.
func BuildCanvas(roots []*Node) *Layout {
	l := &Layout{}
	var self []struct {
		n    *CanvasNode
		cost float64
	}
	leaf := 0.0

	var place func(n *Node, depth int, parent *CanvasNode) *CanvasNode
	place = func(n *Node, depth int, parent *CanvasNode) *CanvasNode {
		// An access type the rules cannot place (MySQL reports several as a
		// bare word like `join`) falls back to the step's own sentence, which
		// names what the node does in words the rules do understand.
		cat := Categorize(n.NodeType)
		if cat == Other {
			cat = Categorize(n.Title)
		}
		p := &CanvasNode{
			Node:       n,
			Category:   cat,
			X:          Pad + float64(depth)*(NodeW+GapX),
			Rows:       ParseNumber(n.Rows),
			ActualRows: ActualRows(n),
		}
		l.Nodes = append(l.Nodes, p)
		if parent != nil {
			l.Edges = append(l.Edges, Edge{parent, p, p.Rows})
		}

		kids := make([]*CanvasNode, len(n.Children))
		for i, c := range n.Children {
			kids[i] = place(c, depth+1, p)
		}
		if len(kids) == 0 {
			p.Y = Pad + leaf
			leaf += NodeH + GapY
		} else {
			p.Y = (kids[0].Y + kids[len(kids)-1].Y) / 2
		}

		if cost := ParseNumber(n.Cost); cost.OK {
			children := 0.0
			for _, c := range n.Children {
				children += ParseNumber(c.Cost).V
			}
			self = append(self, struct {
				n    *CanvasNode
				cost float64
			}{p, math.Max(0, cost.V-children)})
		}
		return p
	}
	for _, r := range roots {
		place(r, 0, nil)
	}

	total := 0.0
	for _, s := range self {
		total += s.cost
	}
	if total > 0 {
		for _, s := range self {
			s.n.CostShare = Num{s.cost / total, true}
		}
	}

	for _, p := range l.Nodes {
		l.Width = math.Max(l.Width, p.X+NodeW)
		l.Height = math.Max(l.Height, p.Y+NodeH)
	}
	if len(l.Nodes) > 0 {
		l.Width += Pad
		l.Height += Pad
	}
	return l
}

// Hottest is the node with the highest self-cost share, the first on a tie;
// nil for an empty plan. The inspector falls back to it until a node is
// selected. A plan with no costs has no hot node, but still returns its first.
func (l *Layout) Hottest() *CanvasNode {
	var best *CanvasNode
	share := func(n *CanvasNode) float64 {
		if n.CostShare.OK {
			return n.CostShare.V
		}
		return -1
	}
	for _, n := range l.Nodes {
		if best == nil || share(n) > share(best) {
			best = n
		}
	}
	return best
}

// Heat buckets a cost share for colouring.
type Heat int

const (
	HeatNone Heat = iota
	HeatCool
	HeatWarm
	HeatHot
)

// HeatOf: above 20% is hot, 5% up to 20% warm, the rest cool.
func HeatOf(share Num) Heat {
	switch {
	case !share.OK:
		return HeatNone
	case share.V > 0.2:
		return HeatHot
	case share.V >= 0.05:
		return HeatWarm
	}
	return HeatCool
}

// EdgeWidth is the stroke of an edge, 1.6 to 8 by log of the rows it carries.
func EdgeWidth(rows Num) float64 {
	if !rows.OK {
		return 1.6
	}
	return math.Min(8, math.Max(1.6, 1.6+math.Log10(math.Max(0, rows.V)+1)*1.05))
}

// FormatRows abbreviates an estimate: 1234 -> "1.2K", 3e6 -> "3M"; "—" when
// there is none.
func FormatRows(rows Num) string {
	if !rows.OK {
		return "—"
	}
	trim := func(v float64) string { return strings.TrimSuffix(toFixed1(v), ".0") }
	switch m := math.Abs(rows.V); {
	case m >= 1e6:
		return trim(rows.V/1e6) + "M"
	case m >= 1e3:
		return trim(rows.V/1e3) + "K"
	}
	return strconv.FormatFloat(math.Floor(rows.V+0.5), 'f', -1, 64)
}

// toFixed1 is JavaScript's toFixed(1): it rounds an exact tie up, where Go's
// formatting rounds it to even, so 1.25 is "1.3" here as in recall.
func toFixed1(v float64) string {
	a := math.Abs(v)
	exact := new(big.Float).SetFloat64(a).Text('f', 80)
	dot := strings.IndexByte(exact, '.')
	tail := exact[dot+1:]
	if tail[1] == '5' && strings.Trim(tail[2:], "0") == "" {
		a += 0.05
	}
	s := strconv.FormatFloat(a, 'f', 1, 64)
	if v < 0 {
		s = "-" + s
	}
	return s
}

// TreeText is the operator tree as text, one node per line indented by depth:
// the fields the tree rows show, so a copy reads like the screen.
func TreeText(nodes []*Node) string {
	var lines []string
	var walk func(ns []*Node, depth int)
	walk = func(ns []*Node, depth int) {
		for _, n := range ns {
			parts := []string{n.NodeType}
			if n.Relation != "" {
				parts = append(parts, n.Relation)
			}
			if n.Index != "" {
				parts = append(parts, "["+n.Index+"]")
			}
			if n.Cost != "" {
				parts = append(parts, "c:"+n.Cost)
			}
			if n.Rows != "" {
				parts = append(parts, "e:"+n.Rows)
			}
			for _, d := range n.Details {
				if m := actualRowsExact.FindStringSubmatch(d); m != nil {
					parts = append(parts, "a:"+m[1])
					break
				}
			}
			lines = append(lines, strings.Repeat("  ", depth)+strings.Join(parts, " "))
			walk(n.Children, depth+1)
		}
	}
	walk(nodes, 0)
	return strings.Join(lines, "\n")
}

// The tree view matches case-sensitively, unlike the canvas inspector's
// ActualRows; both are recall's.
var actualRowsExact = regexp.MustCompile(`Actual Rows:\s*(\S+)`)
