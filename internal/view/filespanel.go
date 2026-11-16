package view

import (
	"path/filepath"

	"github.com/egoist/mygo/ui"

	"seeker/internal/icons"
)

// filesPanel is the right island: the folders opened, and the SQL files
// under them. A click on a file opens it in a tab on its datasource.
func (s *App) filesPanel(c *ui.Context, pal palette) {
	t := c.Theme()
	out := s.core.FilesOutline()
	roots := s.core.FileRoots()

	island(c, pal).Fill().Children(func() {
		ui.Row(c).Height(headerH).FillWidth().AlignItems(ui.Center).Padding(0, 10, 0, 16).Gap(2).Children(func() {
			ui.Text(c, "FILES").FontSize(11).FontWeight(600).TextColor(t.TextMuted)
			ui.Spacer(c)
			if iconButton(c, icons.Plus, "Open folder").Clicked() {
				s.core.ChooseFolder()
			}
			if iconButton(c, icons.Refresh, "Scan the folders again").Clicked() {
				s.core.RescanAll()
			}
		})
		if len(roots) == 0 {
			ui.Box(c).Grow(1).FillWidth().Center().Children(func() {
				ui.Column(c).Gap(10).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "No folders yet").FontSize(12).TextColor(t.TextMuted)
					if ui.Button(c, "Open folder").Clicked() {
						s.core.ChooseFolder()
					}
				})
			})
			return
		}
		ui.Box(c).Padding(0, 12, 8, 12).Children(func() {
			if ui.SearchField(c, s.core.FilesFilter()).Placeholder("File filter, e.g. *.sql").Label("File filter").FillWidth().Changed() {
				s.core.FilesFilterChanged()
			}
		})
		tree := ui.Outline(c, out, roots, s.core.FileChildren, func(path string) {
			s.fileRow(c, path)
		}).Grow(1).FillWidth()
		// Enter acts on the row chosen; a click acts on the row it lands
		// on, in fileRow, so a second click on a row chosen already does too.
		if tree.Submitted() {
			if r := *s.core.FilesRow(); r >= 0 && r < out.Rows() {
				s.core.ActivateFile(out.Item(r))
			}
		}
	})
}

func (s *App) fileRow(c *ui.Context, path string) {
	t := c.Theme()
	out := s.core.FilesOutline()
	sel := *s.core.FilesRow()
	chosen := sel >= 0 && sel < out.Rows() && out.Item(sel) == path
	icon, muted := ink(t, chosen)

	row := ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).PaddingEnd(6).Children(func() {
		switch {
		case s.core.IsFolderRoot(path):
			s.rootRow(c, path, icon, muted)
		default:
			n := s.core.FileNode(path)
			if n == nil {
				return
			}
			if n.IsDir {
				ui.Icon(c, icons.Folder).FontSize(14).TextColor(icon)
				ui.Text(c, n.Name).FontSize(13).FontWeight(430).SingleLine()
				return
			}
			ui.Icon(c, icons.FileCode).FontSize(14).TextColor(icon)
			ui.Text(c, n.Name).FontSize(13).FontWeight(430).SingleLine().Tooltip(path)
		}
	})
	if row.Clicked() {
		s.core.ActivateFile(path)
	}
	if s.core.IsFolderRoot(path) {
		row.ContextMenu(func(m *ui.Menu) {
			if m.Item("Scan again").ID("rescan").Chosen() {
				s.core.Rescan(path)
			}
			if m.Item("Close folder").ID("close").Chosen() {
				s.core.RemoveFolder(path)
			}
		})
	}
}

// rootRow is an opened folder: its name, where it lives, and whether it is
// being read or could not be.
func (s *App) rootRow(c *ui.Context, path string, icon, muted ui.Color) {
	loading, err := s.core.FolderState(path)
	if loading {
		ui.Spinner(c).FontSize(14).TextColor(muted).Label("Scanning")
	} else {
		ui.Icon(c, icons.FolderTree).FontSize(14).TextColor(icon)
	}
	name := ui.Text(c, filepath.Base(path)).FontSize(13).FontWeight(480).SingleLine().Tooltip(path)
	if err != "" {
		name.Tooltip(path + "\n" + err)
		ui.Spacer(c)
		ui.Text(c, "unreadable").FontSize(11).TextColor(muted).SingleLine()
	}
}
