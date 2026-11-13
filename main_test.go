package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"seeker/internal/app"
	"seeker/internal/testenv"
	"seeker/internal/view"
)

// The shell builds over a real store, and its panel toggle works.
func TestShell(t *testing.T) {
	testenv.SQLite(t)
	dir := t.TempDir()
	core, err := app.New(context.Background(), filepath.Join(dir, "seeker.db"), filepath.Join(dir, "recall.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(core.Close)
	a := view.New(core)
	tt := ui.NewTester(a.View, 1280, 820)
	for _, want := range []string{"CONNECTIONS", "RESULTS"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q", want)
		}
	}
	if err := tt.Click("Toggle connections"); err != nil {
		t.Fatal(err)
	}
	if tt.HasText("CONNECTIONS") {
		t.Error("the sidebar still shows after toggling it off")
	}
}
