package files

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("select 1"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func names(ns []*Node) []string {
	var out []string
	for _, n := range ns {
		s := n.Name
		if n.IsDir {
			s += "/"
		}
		out = append(out, s)
	}
	return out
}

func TestMatch(t *testing.T) {
	cases := []struct {
		name, filter string
		want         bool
	}{
		{"a.sql", "", true},
		{"a.sql", "*", true},
		{"A.SQL", "*.sql", true},
		{"a.md", "*.sql", false},
		{"a.md", "*.{sql,md}", true},
		{"a.txt", "*.{sql,md}", false},
		{"a1.sql", "a?.sql", true},
		{"a.sql", "a.s(l", false},
		{"a{.sql", "a{.sql", true},
		{"a+b.sql", "a+b.sql", true},
	}
	for _, c := range cases {
		if got := Match(c.name, c.filter); got != c.want {
			t.Errorf("Match(%q, %q) = %v, want %v", c.name, c.filter, got, c.want)
		}
	}
}

func TestScanOrderFilterIgnore(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "b.sql"))
	write(t, filepath.Join(dir, "A.sql"))
	write(t, filepath.Join(dir, "notes.txt"))
	write(t, filepath.Join(dir, "sub", "c.sql"))
	write(t, filepath.Join(dir, "node_modules", "x.sql"))
	write(t, filepath.Join(dir, ".DS_Store"))
	if err := os.Mkdir(filepath.Join(dir, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, nodes, err := Scan(dir, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	got := names(nodes)
	want := []string{"empty/", "sub/", "A.sql", "b.sql"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	if sub := nodes[1]; len(sub.Children) != 1 || sub.Children[0].Name != "c.sql" {
		t.Errorf("sub: %+v", sub.Children)
	}
}

// A link out of the folder must not show what is behind it, and a link back
// up the tree must not loop.
func TestScanSymlinks(t *testing.T) {
	outside := t.TempDir()
	write(t, filepath.Join(outside, "secret.sql"))
	dir := t.TempDir()
	write(t, filepath.Join(dir, "ok.sql"))
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.Symlink(outside, filepath.Join(dir, "escape")))
	must(os.Symlink(filepath.Join(outside, "secret.sql"), filepath.Join(dir, "leak.sql")))
	must(os.Symlink(dir, filepath.Join(dir, "loop")))
	must(os.Symlink(filepath.Join(dir, "gone"), filepath.Join(dir, "broken.sql")))
	_, nodes, err := Scan(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := names(nodes); len(got) != 1 || got[0] != "ok.sql" {
		t.Errorf("got %v, want only ok.sql", got)
	}
}

func TestScanErrors(t *testing.T) {
	if _, _, err := Scan(filepath.Join(t.TempDir(), "nope"), ""); err == nil {
		t.Error("a missing folder scanned")
	}
	f := filepath.Join(t.TempDir(), "f.sql")
	write(t, f)
	if _, _, err := Scan(f, ""); err == nil {
		t.Error("a file scanned as a folder")
	}
}

func TestInside(t *testing.T) {
	dir := t.TempDir()
	root, _ := filepath.EvalSymlinks(dir)
	write(t, filepath.Join(root, "a.sql"))
	if _, ok := Inside(root, filepath.Join(root, "a.sql")); !ok {
		t.Error("a file inside was refused")
	}
	if _, ok := Inside(root, filepath.Join(root, "..")); ok {
		t.Error("the parent was accepted")
	}
	if _, ok := Inside(root, root); !ok {
		t.Error("the root itself was refused")
	}
}
