package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"seeker/internal/testenv"
)

func newShortcutApp(t *testing.T, dir string) *App {
	t.Helper()
	testenv.SQLite(t)
	a, err := New(context.Background(), filepath.Join(dir, "seeker.db"), filepath.Join(dir, "recall.db"))
	if err != nil {
		t.Fatal(err)
	}
	a.Attach(func(fn func()) { fn() })
	return a
}

func TestShortcutDefaultsAreDistinct(t *testing.T) {
	seen := map[Chord]Command{}
	for _, d := range Commands {
		if !d.Default.Bound() || !Recordable(d.Default.Mods, d.Default.Key) {
			t.Errorf("%s: default %+v cannot be recorded", d.ID, d.Default)
		}
		if other, ok := seen[d.Default]; ok {
			t.Errorf("%s and %s share %+v", d.ID, other, d.Default)
		}
		seen[d.Default] = d.ID
	}
}

// A chord belongs to one command; the rebinding and the unbinding it causes
// survive a restart, and Reset puts a command back.
func TestShortcutRebindConflictAndRestart(t *testing.T) {
	dir := t.TempDir()
	a := newShortcutApp(t, dir)
	closeKey := Chord{ui.Cmd, ui.KeyW}
	a.SetShortcut(QuickOpenKey, closeKey)
	if got := a.Shortcut(QuickOpenKey); got != closeKey {
		t.Fatalf("quick open %+v", got)
	}
	if a.Shortcut(TabClose).Bound() {
		t.Fatalf("Close tab kept ⌘W: %+v", a.Shortcut(TabClose))
	}
	if !a.ShortcutIs(QuickOpenKey, ui.Cmd, ui.KeyW) || a.ShortcutIs(TabClose, ui.Cmd, ui.KeyW) {
		t.Error("ShortcutIs does not follow the binding")
	}
	a.RecordShortcuts(true)
	if a.ShortcutIs(QuickOpenKey, ui.Cmd, ui.KeyW) {
		t.Error("a chord ran while recording")
	}
	a.RecordShortcuts(false)
	a.Close()

	b := newShortcutApp(t, dir)
	defer b.Close()
	if b.Shortcut(QuickOpenKey) != closeKey || b.Shortcut(TabClose).Bound() {
		t.Fatalf("after restart: %+v, %+v", b.Shortcut(QuickOpenKey), b.Shortcut(TabClose))
	}
	b.ResetShortcut(QuickOpenKey)
	if b.Shortcut(QuickOpenKey) != (Chord{ui.Cmd, ui.KeyP}) {
		t.Errorf("reset: %+v", b.Shortcut(QuickOpenKey))
	}
	// ⌘W default of Close tab is free again after the reset only if
	// Close tab is reset too.
	b.ResetShortcuts()
	if b.Shortcut(TabClose) != (Chord{ui.Cmd, ui.KeyW}) || b.ShortcutChanged(TabClose) {
		t.Errorf("reset all: %+v", b.Shortcut(TabClose))
	}
}

func TestChordText(t *testing.T) {
	for _, tc := range []struct {
		ch   Chord
		want string
	}{{Chord{}, "—"}, {Chord{ui.Alt | ui.Shift, ui.KeyF}, "⌥⇧F"}, {Chord{ui.Ctrl, ui.KeyTab}, "⌃⇥"}, {Chord{ui.Super | ui.Alt, ui.KeyRight}, "⌥⌘→"}} {
		if got := ChordText(tc.ch); got != tc.want {
			t.Errorf("%+v = %q, want %q", tc.ch, got, tc.want)
		}
	}
}

func TestKeyNamesRoundTrip(t *testing.T) {
	for k := range keyNames {
		if back, ok := keyOfName(nameOfKey(k)); !ok || back != k {
			t.Errorf("%v: %q -> %v", k, nameOfKey(k), back)
		}
	}
	if !Recordable(0, ui.KeyF5) || Recordable(0, ui.KeyP) || Recordable(ui.Cmd, ui.KeyUnknown) {
		t.Error("Recordable")
	}
}

// The native menu takes the chords the window never sees (⌃Tab on macOS),
// so it needs them spelled, and held back while one is recorded.
func TestAccelerator(t *testing.T) {
	a := &App{}
	for id, want := range map[Command]string{
		TabNextAlt: "Ctrl+Tab", TabPrevAlt: "Ctrl+Shift+Tab", TabNext: "CmdOrCtrl+Alt+Right", TabClose: "CmdOrCtrl+W",
	} {
		if got := a.Accelerator(id); got != want {
			t.Errorf("%s = %q, want %q", id, got, want)
		}
	}
	a.RecordShortcuts(true)
	if got := a.Accelerator(TabNextAlt); got != "" {
		t.Errorf("while recording: %q", got)
	}
	a.RecordShortcuts(false)
	a.setChord(TabNextAlt, Chord{})
	if got := a.Accelerator(TabNextAlt); got != "" {
		t.Errorf("unbound: %q", got)
	}
}

func TestSnapScale(t *testing.T) {
	for in, want := range map[int]int{0: 100, 103: 105, 102: 100, 10: ScaleMin, 999: ScaleMax, -5: ScaleMin, 125: 125} {
		if got := SnapScale(in); got != want {
			t.Errorf("SnapScale(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestOpenTableRevealsItInTheTree(t *testing.T) {
	a := newShortcutApp(t, t.TempDir())
	a.OpenTable("c", "db", "public", "users")
	if !a.RevealPending() {
		t.Fatal("opening a table did not ask the tree to show it")
	}
	// A reused tab is revealed too: switching back by Quick open finds it.
	a.tree.reveal = nil
	a.OpenTable("c", "db", "public", "users")
	if !a.RevealPending() {
		t.Error("a reused table tab was not revealed")
	}
}

func TestActivatingATableTabRevealsIt(t *testing.T) {
	a := newShortcutApp(t, t.TempDir())
	a.OpenTable("c", "db", "public", "users")
	a.OpenQueryTab(QueryTab{Conn: "c", Database: "db", ForceNew: true})
	a.tree.reveal = nil
	a.Activate(a.Tabs().Items[0].ID)
	if !a.RevealPending() {
		t.Error("switching to a table tab did not reveal it")
	}
	a.tree.reveal = nil
	a.Activate(a.Tabs().Items[1].ID)
	if a.RevealPending() {
		t.Error("a query tab asked the tree to reveal a table")
	}
}
