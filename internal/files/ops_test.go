package files

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestNameProblem(t *testing.T) {
	for _, bad := range []string{"", "  ", ".", "..", "a/b", `a\b`, "a\x00b"} {
		if NameProblem(bad) == "" {
			t.Errorf("%q was accepted", bad)
		}
	}
	for _, ok := range []string{"a.sql", "my file.sql", ".hidden", "..a"} {
		if p := NameProblem(ok); p != "" {
			t.Errorf("%q refused: %s", ok, p)
		}
	}
}

func TestCreateRefusesExisting(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.sql"))
	if err := os.WriteFile(filepath.Join(dir, "a.sql"), []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(dir, "a.sql", false); !errors.Is(err, ErrExists) {
		t.Errorf("file: %v", err)
	}
	if got := read(t, filepath.Join(dir, "a.sql")); got != "keep me" {
		t.Errorf("an existing file was truncated: %q", got)
	}
	if p, err := Create(dir, "new.sql", false); err != nil || read(t, p) != "" {
		t.Errorf("new file: %q, %v", p, err)
	}
	if p, err := Create(dir, "sub", true); err != nil {
		t.Errorf("new folder: %q, %v", p, err)
	} else if st, _ := os.Stat(p); !st.IsDir() {
		t.Error("not a folder")
	}
	if _, err := Create(dir, "sub", true); !errors.Is(err, ErrExists) {
		t.Errorf("folder twice: %v", err)
	}
	if _, err := Create(dir, "../x", false); err == nil {
		t.Error("a name with a separator was accepted")
	}
}

func TestRename(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.sql"), filepath.Join(dir, "b.sql")
	write(t, a)
	write(t, b)
	if err := os.WriteFile(b, []byte("other"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Rename(a, "b.sql"); !errors.Is(err, ErrExists) {
		t.Fatalf("onto a taken name: %v", err)
	}
	if read(t, b) != "other" {
		t.Error("the file that held the name was replaced")
	}
	got, err := Rename(a, "c.sql")
	if err != nil || got != filepath.Join(dir, "c.sql") {
		t.Fatalf("rename: %q, %v", got, err)
	}
	if _, err := Rename(a, "d.sql"); !errors.Is(err, ErrMissing) {
		t.Errorf("a gone entry: %v", err)
	}
	if got, err := Rename(b, "b.sql"); err != nil || got != b {
		t.Errorf("same name: %q, %v", got, err)
	}
	// A change of case alone: on a case-insensitive disk the target is the
	// entry itself.
	if got, err := Rename(b, "B.sql"); err != nil || got != filepath.Join(dir, "B.sql") {
		t.Errorf("case change: %q, %v", got, err)
	}
}

func TestTransfer(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dest := filepath.Join(dir, "dest")
	write(t, filepath.Join(src, "a.sql"))
	write(t, filepath.Join(src, "inner", "b.sql"))
	write(t, filepath.Join(dest, "a.sql"))
	if err := os.WriteFile(filepath.Join(dest, "a.sql"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "loose.sql"))

	// Copy: the folder lands whole, the file whose name is taken is refused
	// and the rest still go.
	moved, failures := Transfer([]string{filepath.Join(src, "a.sql"), filepath.Join(src, "inner"), filepath.Join(dir, "loose.sql")}, dest, false)
	if len(moved) != 2 || len(failures) != 1 || failures[0].Path != filepath.Join(src, "a.sql") {
		t.Fatalf("moved %v, failures %v", moved, failures)
	}
	if read(t, filepath.Join(dest, "a.sql")) != "mine" {
		t.Error("a copy replaced a file")
	}
	if read(t, filepath.Join(dest, "inner", "b.sql")) != "select 1" {
		t.Error("the folder was not copied whole")
	}
	if _, err := os.Stat(filepath.Join(dir, "loose.sql")); err != nil {
		t.Error("a copy removed its source")
	}

	// Move: the source goes.
	moved, failures = Transfer([]string{filepath.Join(src, "inner")}, filepath.Join(dir, "other"), true)
	if len(failures) != 1 {
		t.Fatalf("into a missing folder: %v %v", moved, failures)
	}
	if err := os.Mkdir(filepath.Join(dir, "other"), 0o755); err != nil {
		t.Fatal(err)
	}
	moved, failures = Transfer([]string{filepath.Join(src, "inner")}, filepath.Join(dir, "other"), true)
	if len(moved) != 1 || len(failures) != 0 {
		t.Fatalf("move: %v %v", moved, failures)
	}
	if _, err := os.Stat(filepath.Join(src, "inner")); err == nil {
		t.Error("a move left the source")
	}
}

func TestTransferRefusesIntoItself(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a", "b", "x.sql"))
	a := filepath.Join(dir, "a")
	for _, dest := range []string{a, filepath.Join(a, "b")} {
		for _, move := range []bool{false, true} {
			moved, failures := Transfer([]string{a}, dest, move)
			if len(moved) != 0 || len(failures) != 1 {
				t.Errorf("into %s (move %v): %v %v", dest, move, moved, failures)
			}
		}
	}
	// A sibling with the same name prefix is not "inside".
	write(t, filepath.Join(dir, "ab", "y.sql"))
	if moved, failures := Transfer([]string{a}, filepath.Join(dir, "ab"), false); len(moved) != 1 {
		t.Errorf("a prefix sibling was refused: %v %v", moved, failures)
	}
}

func TestCopyKeepsLinksAsLinks(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "src", "a.sql"))
	if err := os.Symlink("/etc/hosts", filepath.Join(dir, "src", "link")); err != nil {
		t.Fatal(err)
	}
	if err := copyEntry(filepath.Join(dir, "src"), filepath.Join(dir, "copy")); err != nil {
		t.Fatal(err)
	}
	if to, err := os.Readlink(filepath.Join(dir, "copy", "link")); err != nil || to != "/etc/hosts" {
		t.Errorf("link: %q, %v", to, err)
	}
}

func TestTrashInto(t *testing.T) {
	dir, trash := t.TempDir(), t.TempDir()
	for i := 0; i < 3; i++ {
		p := filepath.Join(dir, "q.sql")
		write(t, p)
		if err := TrashInto(trash, p); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(p); err == nil {
			t.Fatal("the file is still there")
		}
	}
	for _, name := range []string{"q.sql", "q 2.sql", "q 3.sql"} {
		if _, err := os.Stat(filepath.Join(trash, name)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
