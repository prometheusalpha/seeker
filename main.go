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
		mygo.App.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
			{Role: mygo.RoleAppMenu},
			{Role: mygo.RoleEditMenu},
			{Role: mygo.RoleWindowMenu},
		}))
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
		core.Attach(w.Update)
		w.OnClosed(core.Close)
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
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
