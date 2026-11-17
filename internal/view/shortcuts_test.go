package view

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"seeker/internal/app"
)

// Settings → Shortcuts records a chord: while it waits, the keys run
// nothing; the next chord pressed is bound, the old one stops working, and
// the new one runs the command.
func TestRecordShortcut(t *testing.T) {
	core, p, tt := quickWindow(t)

	tt.Key(ui.Cmd, ui.KeyComma)
	p.frame(tt)
	if err := tt.Click("Shortcuts"); err != nil {
		t.Fatal(err)
	}
	p.frame(tt)
	if !tt.HasText("Quick open") {
		t.Fatalf("no list: %v", tt.Texts())
	}
	if err := tt.Click("Quick open shortcut"); err != nil {
		t.Fatal(err)
	}
	p.frame(tt)
	if !core.Recording() || !tt.HasText("Press keys…") {
		t.Fatalf("not recording: %v", tt.Texts())
	}

	// ⌘P while recording is the new chord, not a Quick open.
	tt.KeyDown(ui.KeyK)
	tt.HoldModifiers(ui.Cmd | ui.Shift)
	p.frame(tt)
	tt.KeyUp(ui.KeyK)
	tt.HoldModifiers(0)
	p.frame(tt)
	want := app.Chord{Mods: ui.Cmd | ui.Shift, Key: ui.KeyK}
	if got := core.Shortcut(app.QuickOpenKey); got != want || core.Recording() {
		t.Fatalf("chord %+v, recording %v", got, core.Recording())
	}
	if core.QuickState().Open {
		t.Fatal("recording ran the command")
	}

	tt.Key(0, ui.KeyEscape)
	p.frame(tt)
	tt.Key(ui.Cmd, ui.KeyP)
	p.frame(tt)
	if core.QuickState().Open {
		t.Error("the old chord still runs it")
	}
	tt.Key(ui.Cmd|ui.Shift, ui.KeyK)
	p.frame(tt)
	if !core.QuickState().Open {
		t.Error("the new chord does not run it")
	}
}

// ⌃Tab and ⌃⇧Tab step through the tabs, wrapping at the ends.
func TestCtrlTabSteps(t *testing.T) {
	core, p, tt := quickWindow(t)
	core.OpenQueryTab(app.QueryTab{Conn: "x", Database: "d", Schema: "public", SQL: "select 2", Title: "second", ForceNew: true})
	p.frame(tt)
	first := func() string { tab, _ := core.ActiveTab(); return tab.ID }
	last := first()
	clickEditor(t, tt, 40, 20)
	p.frame(tt)

	tt.Key(ui.Ctrl, ui.KeyTab)
	p.frame(tt)
	if first() == last {
		t.Fatal("⌃Tab did not move to the next tab")
	}
	tt.Key(ui.Ctrl|ui.Shift, ui.KeyTab)
	p.frame(tt)
	if first() != last {
		t.Fatal("⌃⇧Tab did not come back")
	}
}
