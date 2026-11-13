package view

import (
	"context"
	"path/filepath"
	"testing"

	"seeker/internal/app"
	"seeker/internal/testenv"
)

// The segmented control starts at the saved appearance; starting at System
// made the first frame overwrite a saved Dark.
func TestSavedAppearanceStartsTheControl(t *testing.T) {
	testenv.SQLite(t)
	dir := t.TempDir()
	core, err := app.New(context.Background(), filepath.Join(dir, "s.db"), filepath.Join(dir, "none.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()
	core.UI().Theme.Appearance = "Dark"
	if got := New(core).appearance; got != 2 {
		t.Errorf("appearance index %d, want 2 (Dark)", got)
	}
}
