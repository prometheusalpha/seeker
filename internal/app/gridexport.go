package app

import (
	"fmt"
	"os"
	"strings"

	"github.com/egoist/mygo"
)

// pickExport asks where to write an export; "" is cancel. The native panel
// waits for the user, so it runs off the window's thread.
var pickExport = func(name, ext string) (string, error) {
	return mygo.Dialog.Save(mygo.SaveDialogOptions{
		Title:             "Export result",
		DefaultPath:       name,
		ButtonLabel:       "Export",
		CreateDirectories: true,
		Filters:           []mygo.FileFilter{{Name: strings.ToUpper(ext), Extensions: []string{ext}}},
	})
}

// ExportGrid writes the whole result, not only the rows in view, as csv or
// json, to a file the user names. The rows never change after they arrive,
// so the text is made off the window's thread.
func (a *App) ExportGrid(g *ResultGrid, title, format string) {
	if g == nil || len(g.Rows) == 0 || (format != "csv" && format != "json") {
		return
	}
	name := strings.TrimSuffix(title, ".sql") + "." + format
	go func() {
		path, err := pickExport(name, format)
		n := len(g.Rows)
		if err == nil && path != "" {
			if !strings.HasSuffix(strings.ToLower(path), "."+format) {
				path += "." + format
			}
			text := g.CSV()
			if format == "json" {
				text = g.JSON()
			}
			err = os.WriteFile(path, []byte(text), 0o644)
		}
		a.upd(func() {
			switch {
			case err != nil:
				a.toast("Could not export: " + err.Error())
			case path != "":
				a.toast(fmt.Sprintf("Exported %d rows to %s", n, path))
			}
		})
	}()
}
