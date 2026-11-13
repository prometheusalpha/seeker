// Package explain reads the JSON a server answers EXPLAIN with into one tree,
// and lays that tree out as a canvas. Plans are estimates: nothing here
// runs, or asks the server to run, a statement.
package explain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"seeker/internal/sqltext"
)

// Node is one step of a plan. Engines disagree about names and about which
// numbers they report, so this is the union of what Postgres and MySQL say:
// everything past NodeType is optional, and "" is absent.
//
// Cost and Rows stay text. Postgres reports a cost as `startup..total`, and
// a single number would round one end of that range; the canvas parses
// them when it needs to compute.
type Node struct {
	ID string
	// Title labels the node, e.g. `Seq Scan on users`.
	Title string
	// NodeType is the bare operator, e.g. `Seq Scan` or `eq_ref`; it decides
	// the node's category.
	NodeType string
	Relation string
	Index    string
	Cost     string
	Rows     string
	Width    string
	// Details are `Label: value` lines, or bare text.
	Details  []string
	Children []*Node
}

// Plan is the answer to one EXPLAIN.
type Plan struct {
	// Raw is the server's answer as indented JSON, or the text as received
	// when it is not JSON (a text-only engine): the viewer's raw pane.
	Raw string
	// Nodes are the roots: one per statement the server described. Empty when
	// the answer has a shape this package does not read; the caller opens
	// Raw instead of an empty canvas.
	Nodes []*Node
}

// ErrEmpty is Parse's only error: the server returned no plan text at all.
var ErrEmpty = errors.New("explain: empty plan")

// Parse reads the plan text a driver got back for an EXPLAIN in format JSON.
// Text that is not JSON is not an error: it comes back as a Plan with only
// Raw, as in recall.
func Parse(d sqltext.Dialect, raw string) (*Plan, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return nil, ErrEmpty
	}
	v, ok := decode(text)
	if !ok {
		return &Plan{Raw: text}, nil
	}
	var indented bytes.Buffer
	if err := json.Indent(&indented, []byte(text), "", "  "); err != nil {
		indented.Reset()
		indented.WriteString(text)
	}
	p := &Plan{Raw: indented.String()}
	if d == sqltext.MySQL {
		p.Nodes = parseMySQL(v)
	} else {
		p.Nodes = parsePostgres(v)
	}
	return p, nil
}

// decode keeps numbers as their literal: a cost the server wrote as 1830.12
// must display as that, not as whatever a float prints.
func decode(text string) (any, bool) {
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil {
		return nil, false
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, false
	}
	return v, true
}

// obj is a plan document: anything else (array, scalar, null) is not one.
func obj(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// text reads a plan field as display text. Engines spell the same field as a
// number, a string or a boolean depending on the operator.
func text(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case bool:
		return fmt.Sprint(v)
	}
	return ""
}

// list joins an array field's elements; a non-array reads as "".
func list(v any) string {
	a, _ := v.([]any)
	parts := make([]string, len(a))
	for i, e := range a {
		parts[i] = fmt.Sprint(e)
	}
	return strings.Join(parts, ", ")
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

func details(entries ...string) []string {
	var out []string
	for _, e := range entries {
		if e != "" {
			out = append(out, e)
		}
	}
	return out
}

// labelled is "Label: value", or "" when there is no value to label.
func labelled(label, value string) string {
	if value == "" {
		return ""
	}
	return label + ": " + value
}
