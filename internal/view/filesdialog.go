package view

import (
	"fmt"
	"path/filepath"

	"github.com/egoist/mygo/ui"

	"seeker/internal/app"
)

// filesDialog is the window over the Files panel: a name for a new entry or
// a rename, or the question before something goes to the Trash.
func (s *App) filesDialog(c *ui.Context) {
	t := c.Theme()
	d := &s.core.DialogsState().Files
	modal := ui.Modal(c, &d.Open, func() {
		ui.Column(c).Width(420).Gap(14).Children(func() {
			ui.Text(c, filesDialogTitle(d)).FontSize(16).Bold()
			if d.Kind == app.FileDialogDelete {
				ui.Text(c, fmt.Sprintf("%q goes to the Trash, with everything in it. A tab that has it open keeps its text.", filepath.Base(d.Target))).
					FontSize(12).TextColor(t.TextMuted).Wrap()
			} else {
				in := ui.TextInput(c.Key("file-name"), &d.Name).Placeholder(filesDialogPlaceholder(d.Kind)).Label("Name").FillWidth()
				if d.Focus {
					d.Focus = false
					in.Focus()
				}
				if in.Submitted() {
					s.core.SubmitFileDialog()
				}
			}
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
				if d.Err != "" {
					ui.Text(c, d.Err).FontSize(12).TextColor(t.Danger).Wrap().Grow(1)
				}
				ui.Spacer(c)
				if ui.Button(c, "Cancel").Clicked() {
					s.core.CancelFileDialog()
				}
				if ui.PrimaryButton(c, filesDialogAction(d.Kind)).Clicked() {
					s.core.SubmitFileDialog()
				}
			})
		})
	})
	if modal.Dismissed() {
		s.core.CancelFileDialog()
	}
}

func filesDialogTitle(d *app.FileDialog) string {
	switch d.Kind {
	case app.FileDialogNewFile:
		return "New file"
	case app.FileDialogNewFolder:
		return "New folder"
	case app.FileDialogRename:
		return "Rename"
	}
	return "Move to the Trash?"
}

func filesDialogPlaceholder(k app.FileDialogKind) string {
	if k == app.FileDialogNewFile {
		return "query.sql"
	}
	return "name"
}

func filesDialogAction(k app.FileDialogKind) string {
	switch k {
	case app.FileDialogRename:
		return "Rename"
	case app.FileDialogDelete:
		return "Move to Trash"
	}
	return "Create"
}
