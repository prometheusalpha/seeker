package explain

import "strconv"

// parsePostgres reads `EXPLAIN (FORMAT JSON)`: an array with one object per
// statement, the tree under `Plan`. `Planning Time` and `Execution Time` sit
// beside it and only exist for a plan that ran, which seeker never asks for;
// a hand-written `EXPLAIN ANALYZE` is sent through as written, so they are
// read when present.
func parsePostgres(v any) []*Node {
	entries, ok := v.([]any)
	if !ok {
		entries = []any{v}
	}
	var nodes []*Node
	for i, entry := range entries {
		root := obj(entry)
		if root == nil {
			continue
		}
		plan := obj(root["Plan"])
		if plan == nil {
			plan = root
		}
		n := postgresNode(plan, strconv.Itoa(i))
		for _, k := range []string{"Planning Time", "Execution Time"} {
			if t := text(root[k]); t != "" {
				n.Details = append(n.Details, k+": "+t+" ms")
			}
		}
		nodes = append(nodes, n)
	}
	return nodes
}

// postgresNode: everything is optional but the operator. A Seq Scan reports a
// relation, an Aggregate does not, and some operators have no cost.
func postgresNode(plan map[string]any, id string) *Node {
	nodeType := firstNonEmpty(text(plan["Node Type"]), "Plan")
	relation := text(plan["Relation Name"])
	startup, total := text(plan["Startup Cost"]), text(plan["Total Cost"])
	title := nodeType
	if relation != "" {
		title += " on " + relation
	}
	cost := total
	if startup != "" && total != "" {
		cost = startup + ".." + total
	}
	// The actuals are absent unless the plan ran. `Actual Rows` is per loop,
	// so it pairs with `Plan Rows`, not with a total.
	loops := text(plan["Actual Loops"])
	var actualTime string
	if as, at := text(plan["Actual Startup Time"]), text(plan["Actual Total Time"]); as != "" && at != "" {
		actualTime = "Actual Time: " + as + ".." + at + " ms"
	}
	var actualLoops string
	if n, err := strconv.ParseFloat(loops, 64); err == nil && n > 1 {
		actualLoops = "Actual Loops: " + loops
	}
	n := &Node{
		ID:       id,
		Title:    title,
		NodeType: nodeType,
		Relation: relation,
		Index:    text(plan["Index Name"]),
		Cost:     cost,
		Rows:     text(plan["Plan Rows"]),
		Width:    text(plan["Plan Width"]),
		Details: details(
			labelled("Join", text(plan["Join Type"])),
			labelled("Filter", text(plan["Filter"])),
			labelled("Sort", list(plan["Sort Key"])),
			labelled("Actual Rows", text(plan["Actual Rows"])),
			actualLoops,
			actualTime,
		),
	}
	children, _ := plan["Plans"].([]any)
	for i, c := range children {
		if m := obj(c); m != nil {
			n.Children = append(n.Children, postgresNode(m, id+"."+strconv.Itoa(i)))
		}
	}
	return n
}
