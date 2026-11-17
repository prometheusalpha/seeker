package view

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"seeker/internal/app"
	"seeker/internal/model"
	"seeker/internal/testenv"
)

func filesWindow(t *testing.T) (*app.App, *pump, *ui.Tester, string) {
	t.Helper()
	testenv.SQLite(t)
	dir := t.TempDir()
	core, err := app.New(context.Background(), filepath.Join(dir, "seeker.db"), filepath.Join(dir, "recall.db"))
	if err != nil {
		t.Fatal(err)
	}
	p := &pump{}
	core.Attach(p.update)
	t.Cleanup(core.Close)
	folder := filepath.Join(dir, "queries")
	if err := os.MkdirAll(filepath.Join(folder, "reports"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"users.sql", "reports/daily.sql", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(folder, f), []byte("select 1"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tt := ui.NewTester(New(core).View, 1280, 820)
	p.frame(tt)
	return core, p, tt, folder
}

// The panel stands on the right of the workspace, shows the folder opened
// and the files under it, and its split is draggable from its left edge.
func TestFilesPanel(t *testing.T) {
	core, p, tt, folder := filesWindow(t)
	files, ok := tt.Find("FILES")
	if !ok {
		t.Fatalf("no panel: %v", tt.Texts())
	}
	conns, _ := tt.Find("CONNECTIONS")
	if files.X < 900 || conns.X > 100 {
		t.Errorf("FILES at x=%v, CONNECTIONS at x=%v", files.X, conns.X)
	}

	core.AddFolder(folder)
	p.waitUntil(t, tt, "the folder", func() bool { return tt.HasText("queries") && tt.HasText("users.sql") })
	if !tt.HasText("reports") || !tt.HasText("notes.txt") {
		t.Errorf("tree: %v", tt.Texts())
	}
}

// One click on a folder's name opens it, the next closes it, whether or not
// it was the row chosen.
func TestFilesPanelClickTogglesFolder(t *testing.T) {
	core, p, tt, folder := filesWindow(t)
	core.AddFolder(folder)
	p.waitUntil(t, tt, "the folder", func() bool { return tt.HasText("reports") })
	if tt.HasText("daily.sql") {
		t.Fatal("a closed folder shows its files")
	}
	for i, want := range []bool{true, false, true} {
		if err := tt.Click("reports"); err != nil {
			t.Fatal(err)
		}
		p.frame(tt)
		if got := tt.HasText("daily.sql"); got != want {
			t.Fatalf("click %d: daily.sql shown %v, want %v", i+1, got, want)
		}
	}
}

// One click on a file opens it in a tab, with no mark left on its row.
func TestFilesPanelClickOpensFile(t *testing.T) {
	profile := model.Profile{ID: "x", Name: "nowhere", DBType: model.Postgres, Host: "127.0.0.1", Port: 1, Username: "u", Database: "d", ConnectTimeoutSecs: 2}
	core, p, tt := editorWindow(t, profile, "", "select 0")
	folder := t.TempDir()
	file := filepath.Join(folder, "users.sql")
	if err := os.WriteFile(file, []byte("select 42"), 0o644); err != nil {
		t.Fatal(err)
	}
	core.AddFolder(folder)
	p.waitUntil(t, tt, "the file", func() bool { return tt.HasText("users.sql") })
	if err := tt.Click("users.sql"); err != nil {
		t.Fatal(err)
	}
	p.frame(tt)
	if tab, ok := core.ActiveTab(); !ok || tab.Title != "users.sql" || tab.SQL != "select 42" {
		t.Fatalf("active tab: %+v", tab)
	}
}

// A row's menu offers what the row can do, the rename dialog takes a name
// and Enter, and the file is renamed on disk and in the tree.
func TestFilesPanelRenameThroughDialog(t *testing.T) {
	core, p, tt, folder := filesWindow(t)
	core.AddFolder(folder)
	p.waitUntil(t, tt, "the file", func() bool { return tt.HasText("users.sql") })

	if err := tt.RightClick("users.sql"); err != nil {
		t.Fatal(err)
	}
	menu := tt.Menu()
	for _, want := range []string{"Open", "Cut", "Copy", "Paste", "Rename…", "Move to Trash…", "Reveal in Finder"} {
		if !slices.Contains(menu, want) {
			t.Errorf("menu lacks %q: %v", want, menu)
		}
	}
	if slices.Contains(menu, "Close folder") || slices.Contains(menu, "New file…") {
		t.Errorf("a file's menu has a folder's items: %v", menu)
	}
	if err := tt.ChooseMenuItem("Rename…"); err != nil {
		t.Fatal(err)
	}
	p.frame(tt)
	if !tt.HasText("Rename") {
		t.Fatalf("no dialog: %v", tt.Texts())
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("people.sql")
	tt.Key(0, ui.KeyEnter)
	p.waitUntil(t, tt, "the renamed file", func() bool { return tt.HasText("people.sql") && !tt.HasText("users.sql") })
	if _, err := os.Stat(filepath.Join(folder, "people.sql")); err != nil {
		t.Errorf("not renamed on disk: %v", err)
	}
}

// An opened folder's menu has the folder items and no rename or delete.
func TestFilesPanelRootMenu(t *testing.T) {
	core, p, tt, folder := filesWindow(t)
	core.AddFolder(folder)
	p.waitUntil(t, tt, "the folder", func() bool { return tt.HasText("queries") })
	if err := tt.RightClick("queries"); err != nil {
		t.Fatal(err)
	}
	menu := tt.Menu()
	for _, want := range []string{"New file…", "New folder…", "Paste", "Reveal in Finder", "Scan again", "Close folder"} {
		if !slices.Contains(menu, want) {
			t.Errorf("menu lacks %q: %v", want, menu)
		}
	}
	for _, bad := range []string{"Rename…", "Move to Trash…", "Cut", "Copy"} {
		if slices.Contains(menu, bad) {
			t.Errorf("an opened folder offers %q", bad)
		}
	}
}

// The files panel is set off from the islands beside it by the same gap the
// sidebar is.
func TestFilesPanelGap(t *testing.T) {
	_, _, tt, _ := filesWindow(t)
	img := tt.Image()
	y := 300
	// Runs of the canvas color along a row, between island edges.
	canvas := img.RGBAAt(2, y)
	var gaps []int
	run := 0
	for x := 0; x < img.Bounds().Dx(); x++ {
		if img.RGBAAt(x, y) == canvas {
			run++
			continue
		}
		if run > 3 && x > 20 {
			gaps = append(gaps, run)
		}
		run = 0
	}
	if len(gaps) != 2 || abs(gaps[0]-gaps[1]) > 2 {
		t.Errorf("gaps between islands: %v", gaps)
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// Dragging the divider to the left makes the panel wider, to the right
// narrower: the panel is on the right, so the divider follows the pointer.
func TestFilesPanelDividerFollowsPointer(t *testing.T) {
	core, p, tt, _ := filesWindow(t)
	u := core.UI()
	files, _ := tt.Find("FILES")
	for _, dx := range []float32{-100, 60} {
		before := u.FilesW
		moved := false
		// The divider is a few px wide, in the gap left of the panel.
		for off := float32(10); off < 24 && !moved; off += 2 {
			x := files.X - off
			tt.Press(x, 400)
			tt.Move(x+dx, 400)
			tt.Release(x+dx, 400)
			p.frame(tt)
			moved = u.FilesW != before
			if moved && (dx < 0) != (u.FilesW > before) {
				t.Errorf("dragged %vpx: width %v -> %v", dx, before, u.FilesW)
			}
			if !moved {
				files, _ = tt.Find("FILES")
			}
		}
		if !moved {
			t.Errorf("drag %v never found the divider", dx)
		}
	}
}

// The row clicked is the row chosen, so it is highlighted: for a folder and
// for a file.
func TestFilesPanelClickChoosesRow(t *testing.T) {
	core, p, tt, folder := filesWindow(t)
	core.AddFolder(folder)
	p.waitUntil(t, tt, "the folder", func() bool { return tt.HasText("reports") && tt.HasText("users.sql") })
	out := core.FilesOutline()
	chosen := func() string {
		if r := *core.FilesRow(); r >= 0 && r < out.Rows() {
			return filepath.Base(out.Item(r))
		}
		return ""
	}
	for _, name := range []string{"reports", "users.sql", "queries"} {
		if err := tt.Click(name); err != nil {
			t.Fatal(err)
		}
		p.frame(tt)
		if got := chosen(); got != name {
			t.Errorf("clicked %q: chosen row is %q", name, got)
		}
	}
}

// The reveal button opens the folders above the file of the active tab and
// scrolls to its row, however far down it is.
func TestFilesPanelRevealScrolls(t *testing.T) {
	core, p, tt, folder := filesWindow(t)
	if err := core.SaveProfile(model.Profile{ID: "x", Name: "nowhere", DBType: model.Postgres, Host: "127.0.0.1", Port: 1, Username: "u", Database: "d", ConnectTimeoutSecs: 2}, ""); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 150; i++ {
		name := filepath.Join(folder, "reports", fmt.Sprintf("q%03d.sql", i))
		if err := os.WriteFile(name, []byte("select 1"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	core.AddFolder(folder)
	p.waitUntil(t, tt, "the folder", func() bool { return tt.HasText("reports") })
	// The panel keeps real paths, as the files it opens carry.
	real, err := filepath.EvalSymlinks(folder)
	if err != nil {
		t.Fatal(err)
	}
	last := filepath.Join(real, "reports", "q149.sql")
	if err := core.OpenFile(last); err != nil {
		t.Fatal(err)
	}
	p.frame(tt)
	// The tab shows the name too, so the row is told by the count growing.
	shown := func() int { return strings.Count(strings.Join(tt.Texts(), "\n"), "q149.sql") }
	before := shown()
	core.RevealActiveFile()
	p.frame(tt)
	p.frame(tt)
	if shown() <= before {
		t.Errorf("the row is not in view: %v", tt.Texts())
	}
}
