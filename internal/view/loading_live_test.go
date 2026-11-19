package view

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// A slow table load shows its clock and Cancel over the rows from before;
// Cancel stops it on the server, so the session answers the next load at
// once, long before the sleep would have ended.
func TestLiveTableLoadCancel(t *testing.T) {
	profile, password, core, p, tt := livePGWindow(t, "select 1")
	seedViewTable(t, profile, password)
	core.OpenTable(profile.ID, profile.Database, "public", "seeker_m4_v")
	p.waitUntil(t, tt, "the rows", func() bool { return tt.HasText("n5") })
	tab, _ := core.ActiveTab()
	tv := core.TableOf(tab.ID)

	tt.Key(ui.Cmd, ui.KeyL)
	p.frame(tt)
	tt.Type("pg_sleep(30) is not null")
	tt.Key(0, ui.KeyEnter)
	p.waitUntil(t, tt, "the clock", func() bool { return tv.Loading && hasAny(tt, "loading… ") && tt.HasText("Cancel") })
	if !tt.HasText("n5") {
		t.Errorf("the rows from before left while loading: %v", tt.Texts())
	}
	if err := tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	p.frame(tt)
	if tv.Loading || !tv.Cancelled || !tt.HasText("n5") || !hasAny(tt, "load cancelled") {
		t.Fatalf("after cancel: loading %v cancelled %v; %v", tv.Loading, tv.Cancelled, tt.Texts())
	}

	tt.Key(ui.Cmd, ui.KeyL)
	tt.Key(ui.Cmd, ui.KeyA)
	p.frame(tt)
	tt.Type("id <= 2")
	tt.Key(0, ui.KeyEnter)
	p.waitUntil(t, tt, "the next load", func() bool { return tv.Where == "id <= 2" && !tv.Loading && !tt.HasText("n3") })
	if !tt.HasText("n2") || tv.Cancelled {
		t.Errorf("next load: cancelled %v; %v", tv.Cancelled, tt.Texts())
	}
}

// A query with no rows yet shows its clock and Cancel in the pane.
func TestLiveQueryRunningCancel(t *testing.T) {
	_, _, core, p, tt := livePGWindow(t, "select pg_sleep(30)")
	id := activeID(core)
	clickEditor(t, tt, 100, 15)
	tt.Key(ui.Cmd, ui.KeyEnter)
	p.waitUntil(t, tt, "the clock", func() bool { return core.Running(id) && hasAny(tt, "running… ") && tt.HasText("Cancel") })
	if err := tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	p.waitUntil(t, tt, "the stop", func() bool { return !core.Running(id) && hasAny(tt, "stopped") })
}
