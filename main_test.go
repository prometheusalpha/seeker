package main

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"seeker/internal/view"
)

// The shell builds without a window, and its panel toggles work.
func TestShell(t *testing.T) {
	a := view.New()
	tt := ui.NewTester(a.View, 1280, 820)
	for _, s := range []string{"CONNECTIONS", "SQL FILES", "revenue.sql", "local-pg"} {
		if !tt.HasText(s) {
			t.Fatalf("missing %q", s)
		}
	}
	if err := tt.Click("Toggle SQL files"); err != nil {
		t.Fatal(err)
	}
	if tt.HasText("SQL FILES") {
		t.Error("files panel still shown after toggling it off")
	}
}
