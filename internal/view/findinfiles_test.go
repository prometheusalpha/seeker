package view

import (
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// ⌘⇧F opens the window with the keyboard in the query; the hits show, Enter
// opens the chosen one in a tab, and Escape closes it.
func TestFindInFilesByKeyboard(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep is not installed")
	}
	core, p, tt := quickWindow(t)

	tt.Key(ui.Cmd|ui.Shift, ui.KeyF)
	p.frame(tt)
	if !core.FindState().Open || !tt.HasText("Type to search the files in the opened folders.") {
		t.Fatalf("not open: %v", tt.Texts())
	}
	tt.Type("select 1")
	deadline := time.Now().Add(10 * time.Second)
	for len(core.FindState().Rows) < 3 && time.Now().Before(deadline) {
		p.frame(tt)
		time.Sleep(5 * time.Millisecond)
	}
	rows := core.FindState().Rows
	if len(rows) != 3 || !tt.HasText("3 lines") {
		t.Fatalf("rows %+v, texts %v", rows, tt.Texts())
	}

	tt.Key(0, ui.KeyDown)
	p.frame(tt)
	want := rows[1].Path
	tt.Key(0, ui.KeyEnter)
	p.frame(tt)
	if tab, ok := core.ActiveTab(); !ok || tab.Path != want || core.FindState().Open {
		t.Fatalf("tab %+v, open %v", tab, core.FindState().Open)
	}
	if filepath.Ext(want) != ".sql" {
		t.Errorf("opened %s", want)
	}

	tt.Key(ui.Cmd|ui.Shift, ui.KeyF)
	p.frame(tt)
	if !core.FindState().Open || !tt.HasText("3 lines") {
		t.Errorf("the query was not kept: %v", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	p.frame(tt)
	if core.FindState().Open {
		t.Error("Escape left it open")
	}
}
