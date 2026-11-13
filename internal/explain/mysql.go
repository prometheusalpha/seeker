package explain

import "strconv"

// parseMySQL reads `EXPLAIN FORMAT=JSON`. The server answers in one of two
// documents and the caller does not choose: the original `query_block` one,
// and the tree of `inputs` that 8.4 emits for plans it can describe that way
// (`explain_json_format_version=2`). Both are read, because which one arrives
// is the server's decision rather than a setting.
//
// Recall only recognised the tree under a `query_plan` key. A real 8.4 server
// puts the tree at the document root, so the root counts as a tree too when it
// carries `inputs` or `operation`; recall drew an empty `query_block` there.
func parseMySQL(v any) []*Node {
	root := obj(v)
	if root == nil {
		return nil
	}
	if tree := obj(root["query_plan"]); tree != nil {
		return []*Node{mysqlStep(tree, "0")}
	}
	if _, ok := root["inputs"]; ok {
		return []*Node{mysqlStep(root, "0")}
	}
	if _, ok := root["operation"]; ok {
		return []*Node{mysqlStep(root, "0")}
	}
	block := obj(root["query_block"])
	if block == nil {
		block = root
	}
	return []*Node{mysqlBlock(block, "0", "query_block")}
}

// mysqlStep is one node of the tree shape: a step that wraps `inputs`, or a
// table access, which is the same record without them. `operation` is the
// server's own sentence ("Nested loop inner join", "Sort: ..."), so it labels
// the row, while `access_type` stays the short word the category comes from.
func mysqlStep(node map[string]any, id string) *Node {
	operation := text(node["operation"])
	accessType := text(node["access_type"])
	relation := text(node["table_name"])
	title := operation
	if title == "" {
		switch {
		case relation != "":
			title = firstNonEmpty(accessType, "table") + " on " + relation
		default:
			title = firstNonEmpty(accessType, "step")
		}
	}
	n := &Node{
		ID:       id,
		Title:    title,
		NodeType: firstNonEmpty(accessType, operation, "step"),
		Relation: relation,
		Index:    text(node["index_name"]),
		Cost:     text(node["estimated_total_cost"]),
		Rows:     text(node["estimated_rows"]),
		Details: details(
			labelled("Condition", firstNonEmpty(text(node["attached_condition"]), text(node["lookup_condition"]))),
			labelled("Columns", list(node["used_columns"])),
			labelled("Sort", list(node["sort_fields"])),
		),
	}
	inputs, _ := node["inputs"].([]any)
	for i, in := range inputs {
		// A non-object input still takes its place, as a bare "step".
		n.Children = append(n.Children, mysqlStep(obj(in), id+"."+strconv.Itoa(i)))
	}
	return n
}

// mysqlBlock is a `query_block` or one of the wrappers inside it.
func mysqlBlock(block map[string]any, id, nodeType string) *Node {
	n := &Node{ID: id, Title: nodeType, NodeType: nodeType}
	child := func(c *Node) { n.Children = append(n.Children, c) }
	next := func() string { return id + "." + strconv.Itoa(len(n.Children)) }

	if t := obj(block["table"]); t != nil {
		child(mysqlTable(t, next()))
	}
	loop, _ := block["nested_loop"].([]any)
	for _, item := range loop {
		step := obj(item)
		if step == nil {
			continue
		}
		if t := obj(step["table"]); t != nil {
			child(mysqlTable(t, next()))
		} else {
			child(mysqlBlock(step, next(), "operation"))
		}
	}
	// Each of these wraps the block it describes rather than being part of it,
	// so it becomes a parent node instead of a detail on the node below.
	for _, key := range []string{
		"ordering_operation",
		"grouping_operation",
		"duplicates_removal",
		"union_result",
		"materialized_from_subquery",
	} {
		if w := obj(block[key]); w != nil {
			child(mysqlBlock(w, next(), key))
		}
	}

	n.Cost = text(obj(block["cost_info"])["query_cost"])
	// A block reports no row estimate of its own; the tables underneath it
	// do. `select_id` is an identifier, so it reads as a detail instead.
	n.Details = details(text(block["message"]), labelled("Select", text(block["select_id"])))
	return n
}

// mysqlTable is one table access: the only block-shape node with a real row
// estimate.
func mysqlTable(t map[string]any, id string) *Node {
	relation := text(t["table_name"])
	accessType := firstNonEmpty(text(t["access_type"]), "table")
	cost := obj(t["cost_info"])
	title := accessType
	if relation != "" {
		title += " on " + relation
	}
	using := ""
	if t["using_index"] == true {
		using = "Using index"
	}
	return &Node{
		ID:       id,
		Title:    title,
		NodeType: accessType,
		Relation: relation,
		Index:    text(t["key"]),
		Cost:     firstNonEmpty(text(cost["query_cost"]), text(cost["read_cost"]), text(cost["eval_cost"])),
		Rows:     firstNonEmpty(text(t["rows_examined_per_scan"]), text(t["rows_produced_per_join"])),
		Details: details(
			labelled("Condition", text(t["attached_condition"])),
			labelled("Columns", list(t["used_columns"])),
			using,
		),
	}
}
