package main

import (
	"context"
	"log"
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"seeker/internal/app"
	"seeker/internal/store"
	"seeker/internal/view"
)

func main() {
	mygo.App.WhenReady(func() {
		core, err := openApp()
		if err != nil {
			log.Fatal(err)
		}
		a := view.New(core)
		w := mygo.NewWindow(mygo.WindowOptions{
			Title:         "Seeker",
			Width:         1280,
			Height:        820,
			MinWidth:      900,
			MinHeight:     560,
			StateKey:      "main",
			TitleBarStyle: mygo.TitleBarHidden,
			Content:       ui.View(a.View),
		})
		mygo.App.SetMenu(appMenu(core, a, w))
		core.OnShortcutsChange(func() { mygo.App.SetMenu(appMenu(core, a, w)) })
		core.Attach(w.Update)
		w.OnClosed(core.Close)
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// appMenu is the menu bar. Its Go menu holds the alt chords of stepping through
// the tabs (⌃Tab, ⌃⇧Tab; the main ones, ⌘⌥←/→, reach the window): on macOS a
// key such as ⌃Tab reaches the app only as a menu's key equivalent, so the
// menu is built again
// as one is rebound (or held back while the settings wait for a key).
func appMenu(core *app.App, a *view.App, w *mygo.Window) *mygo.Menu {
	step := func(id app.Command, label string, delta int) *mygo.MenuItem {
		return &mygo.MenuItem{Label: label, Accelerator: core.Accelerator(id),
			Click: func(*mygo.MenuItem, *mygo.Window) { w.Update(func() { a.StepTab(delta) }) }}
	}
	return mygo.NewMenu([]*mygo.MenuItem{
		{Role: mygo.RoleAppMenu},
		{Role: mygo.RoleEditMenu},
		{Label: "Go", Submenu: []*mygo.MenuItem{
			step(app.TabNextAlt, "Show Next Tab", 1),
			step(app.TabPrevAlt, "Show Previous Tab", -1),
		}},
		{Role: mygo.RoleWindowMenu},
	})
}

// openApp opens the app's database in the user data folder and imports
// recall's file once: its connections, pins and bookmarks.
func openApp() (*app.App, error) {
	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		return nil, err
	}
	recall, err := store.RecallDB()
	if err != nil {
		return nil, err
	}
	return app.New(context.Background(), filepath.Join(dir, "seeker.db"), recall)
}
