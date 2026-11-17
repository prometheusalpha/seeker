package files

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestSplitMask(t *testing.T) {
	for _, tc := range []struct {
		mask string
		want []string
	}{
		{"", nil}, {"*", nil}, {"*.sql", []string{"*.sql"}},
		{"*.{sql,md}", []string{"*.{sql,md}"}},
		{"*.sql, !tmp*,", []string{"*.sql", "!tmp*"}},
		{"a.{x,{y,z}},b", []string{"a.{x,{y,z}}", "b"}},
	} {
		if got := SplitMask(tc.mask); !slices.Equal(got, tc.want) {
			t.Errorf("SplitMask(%q) = %q, want %q", tc.mask, got, tc.want)
		}
	}
}

func rgOrSkip(t *testing.T) string {
	t.Helper()
	rg, err := exec.LookPath("rg")
	if err != nil {
		t.Skip("ripgrep is not installed")
	}
	return rg
}

func writeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFind(t *testing.T) {
	rg := rgOrSkip(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, "a.sql", "select 1;\nSELECT name FROM Users;\n")
	writeFile(t, root, "sub/b.sql", "-- héllo Users\nselect * from users where id = 1;\n")
	writeFile(t, root, "notes.txt", "users in a note\n")
	writeFile(t, root, "node_modules/x.sql", "users\n")
	writeFile(t, root, ".hidden/h.sql", "users\n")
	ctx := context.Background()
	find := func(req FindRequest) FindResult {
		t.Helper()
		req.Folders = []string{root}
		res, err := Find(ctx, rg, req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	paths := func(r FindResult) []string {
		var out []string
		for _, m := range r.Matches {
			rel, _ := filepath.Rel(root, m.Path)
			out = append(out, rel+":"+strconv.Itoa(m.Line))
		}
		return out
	}

	// Case-insensitive by default; the hidden folder is searched, the
	// ignored node_modules is not.
	got := paths(find(FindRequest{Query: "users"}))
	want := []string{".hidden/h.sql:1", "a.sql:2", "notes.txt:1", "sub/b.sql:1", "sub/b.sql:2"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := paths(find(FindRequest{Query: "Users", CaseSensitive: true})); !slices.Equal(got, []string{"a.sql:2", "sub/b.sql:1"}) {
		t.Errorf("case sensitive: %q", got)
	}
	if got := paths(find(FindRequest{Query: "user", WholeWord: true, Mask: "*.sql"})); len(got) != 0 {
		t.Errorf("whole word matched a part of a word: %q", got)
	}
	if got := paths(find(FindRequest{Query: "users", Mask: "*.sql,!sub*"})); !slices.Equal(got, []string{".hidden/h.sql:1", "a.sql:2"}) {
		t.Errorf("mask: %q", got)
	}
	if got := paths(find(FindRequest{Query: `id\s=\s\d`, Regex: true})); !slices.Equal(got, []string{"sub/b.sql:2"}) {
		t.Errorf("regex: %q", got)
	}
	// A query with regex characters is literal unless asked otherwise.
	if got := paths(find(FindRequest{Query: "id = 1;"})); !slices.Equal(got, []string{"sub/b.sql:2"}) {
		t.Errorf("literal: %q", got)
	}

	// The hit's place is in bytes, in the line, as the editor counts: `é`
	// is two bytes.
	m := find(FindRequest{Query: "users", Mask: "b.sql"}).Matches[0]
	if m.Line != 1 || m.Text != "-- héllo Users" || m.Col != len("-- héllo ") || m.Len != 5 {
		t.Errorf("hit: %+v", m)
	}

	// Nested folders do not repeat a row.
	res, err := Find(ctx, rg, FindRequest{Query: "users", Folders: []string{root, filepath.Join(root, "sub")}, Mask: "b.sql"})
	if err != nil || len(res.Matches) != 2 {
		t.Errorf("nested: %v, %+v", err, res.Matches)
	}

	// A regex that does not parse is the search's error.
	if _, err := Find(ctx, rg, FindRequest{Query: "(", Regex: true, Folders: []string{root}}); err == nil {
		t.Error("a bad regex was not an error")
	}
	// A cancelled search returns the cancellation, not rows.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := Find(cctx, rg, FindRequest{Query: "users", Folders: []string{root}}); err == nil {
		t.Error("a cancelled search succeeded")
	}
}

func TestFindCapsRowsAndClipsLongLines(t *testing.T) {
	rg := rgOrSkip(t)
	root, _ := filepath.EvalSymlinks(t.TempDir())
	var b strings.Builder
	for i := 0; i < MaxFindRows+50; i++ {
		b.WriteString("hit\n")
	}
	writeFile(t, root, "many.sql", b.String())
	res, err := Find(context.Background(), rg, FindRequest{Query: "hit", Folders: []string{root}})
	if err != nil || len(res.Matches) != MaxFindRows || !res.Truncated {
		t.Fatalf("%v, %d rows, truncated %v", err, len(res.Matches), res.Truncated)
	}

	writeFile(t, root, "long.sql", strings.Repeat("é", 1000)+"NEEDLE"+strings.Repeat("é", 1000)+"\n")
	res, err = Find(context.Background(), rg, FindRequest{Query: "needle", Folders: []string{root}, Mask: "long.sql"})
	if err != nil || len(res.Matches) != 1 {
		t.Fatalf("%v %+v", err, res.Matches)
	}
	m := res.Matches[0]
	if len(m.Text) > maxLineBytes+4 || !strings.Contains(m.Text, "NEEDLE") || !strings.HasPrefix(m.Text, "é") {
		t.Errorf("clip: %d bytes, %q…", len(m.Text), m.Text[:min(20, len(m.Text))])
	}
	if m.Col != 2000 || m.Len != 6 {
		t.Errorf("col %d len %d: the place is in the full line", m.Col, m.Len)
	}
}
