package view

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"seeker/internal/app"
	"seeker/internal/icons"
	"seeker/internal/model"
)

// sidebar is the left island: the saved connections and, below the rows
// opened, what their sessions list. Every level loads on the first expand.
func (s *App) sidebar(c *ui.Context, pal palette) {
	t := c.Theme()
	out := s.core.Outline()
	out.List.Selected = s.core.TreeRow()
	roots := s.core.Roots()

	island(c, pal).Fill().Children(func() {
		ui.Row(c).Height(headerH).FillWidth().AlignItems(ui.Center).Padding(0, 10, 0, 16).Gap(2).Children(func() {
			ui.Text(c, "CONNECTIONS").FontSize(11).FontWeight(600).TextColor(t.TextMuted)
			ui.Spacer(c)
			if iconButton(c, icons.Plus, "New connection").Clicked() {
				s.core.OpenConnectionDialog("")
			}
			if iconButton(c, icons.ChevronsUp, "Collapse all").Clicked() {
				s.core.CollapseAll()
			}
		})
		ui.Box(c).Padding(0, 12, 8, 12).Children(func() {
			if ui.SearchField(c, s.core.TreeFilter()).Placeholder("Search tables").Label("Search tables").FillWidth().Changed() {
				s.core.FilterChanged()
			}
		})
		if len(roots) == 0 {
			ui.Box(c).Grow(1).FillWidth().Center().Children(func() {
				ui.Column(c).Gap(10).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "No connections yet").FontSize(12).TextColor(t.TextMuted)
					if ui.Button(c, "New connection").Clicked() {
						s.core.OpenConnectionDialog("")
					}
				})
			})
			return
		}
		tree := ui.Outline(c, out, roots, s.treeChildren, func(n app.Node) {
			s.treeRow(c, out, n)
		}).Grow(1).FillWidth()
		// A click, Enter or an arrow key chooses a row; the app says what
		// that row does, opening its tab or its children.
		if tree.Changed() {
			if r := *s.core.TreeRow(); r >= 0 && r < out.Rows() {
				s.core.ActivateNode(out.Item(r))
			}
		}
	})
}

// treeChildren lists what sits under n. The outline asks a row for its
// children before it opens, only to learn whether the row has an arrow,
// and the app starts a load for whatever it is asked: answering a closed
// container with an empty slice keeps a saved profile from connecting
// until its row opens, as recall does.
func (s *App) treeChildren(n app.Node) []app.Node {
	switch n.Kind {
	case app.KindConnection, app.KindDatabase, app.KindSchema, app.KindGroup:
		if !s.core.Outline().Open.Has(n) {
			return []app.Node{}
		}
	}
	return s.core.Children(n)
}

func (s *App) treeRow(c *ui.Context, out *ui.OutlineState[app.Node], n app.Node) {
	t := c.Theme()
	sel := *s.core.TreeRow()
	chosen := sel >= 0 && sel < out.Rows() && out.Item(sel) == n
	icon, muted := ink(t, chosen)

	row := ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).PaddingEnd(6).Children(func() {
		switch n.Kind {
		case app.KindConnection:
			s.connectionRow(c, n, chosen, muted)
		case app.KindDatabase:
			ui.Icon(c, icons.Database).FontSize(14).TextColor(icon)
			ui.Text(c, n.DB).FontSize(13).FontWeight(430).SingleLine()
		case app.KindSchema:
			ui.Icon(c, icons.Folder).FontSize(14).TextColor(icon)
			ui.Text(c, n.Schema).FontSize(13).FontWeight(430).SingleLine()
		case app.KindTable:
			glyph := icons.Table
			if n.Type == "view" || n.Type == "materialized view" {
				glyph = icons.Eye
			}
			ui.Icon(c, glyph).FontSize(14).TextColor(icon)
			ui.Text(c, n.Table).FontSize(13).FontWeight(430).SingleLine()
			if s.core.IsPinned(n) {
				ui.Spacer(c)
				ui.Icon(c, icons.Pin).FontSize(11).TextColor(muted).Label("Pinned")
			}
		case app.KindGroup:
			ui.Icon(c, groupIcon(n.Group)).FontSize(14).TextColor(icon)
			ui.Text(c, groupLabel(n.Group)).FontSize(13).FontWeight(430).SingleLine()
		case app.KindLeaf:
			label := ui.Text(c, n.Label).FontSize(13).SingleLine()
			if n.Title != "" {
				label.Tooltip(n.Title)
			}
			if n.Badge != "" {
				ui.Spacer(c)
				ui.Text(c, n.Badge).FontSize(11).TextColor(muted).SingleLine()
			}
		case app.KindLoading:
			ui.Text(c, "Loading…").FontSize(12).TextColor(muted).SingleLine()
		case app.KindError:
			ui.Text(c, n.Message).FontSize(12).TextColor(muted).SingleLine().Tooltip(n.Message)
		}
	})
	if hasTreeMenu(n) {
		row.ContextMenu(func(m *ui.Menu) { s.treeMenu(c, n, m) })
	}
}

// connectionRow shows the profile, what its session reported once it is
// live, and whether that session is up. A failed connect keeps the reason
// on the tooltip rather than inline, as recall does: the server's message
// is usually a sentence, not a label.
func (s *App) connectionRow(c *ui.Context, n app.Node, chosen bool, muted ui.Color) {
	t := c.Theme()
	p, _ := s.core.Profile(n.Conn)
	status := s.core.Status(n.Conn)

	if status == app.Connecting {
		ui.Spinner(c).FontSize(14).TextColor(muted).Label("Connecting")
	} else {
		tint := t.Accent
		if chosen {
			tint = t.AccentText
		}
		ui.Icon(c, icons.Database).FontSize(14).TextColor(tint)
	}
	name := ui.Text(c, p.Name).FontSize(13).FontWeight(480).SingleLine()
	if status == app.Failed {
		if msg := s.core.ConnError(n.Conn); msg != "" {
			name.Tooltip(msg)
		}
	}
	ui.Text(c, s.connectionEngine(p)).FontSize(11).TextColor(muted).SingleLine()
	ui.Spacer(c)
	if status == app.Connected {
		ui.Box(c).Size(6, 6).Radius(3).Background(t.Success).Label("Connected")
	}
}

// connectionEngine names the server once it answered, and the engine the
// profile speaks until then.
func (s *App) connectionEngine(p model.Profile) string {
	if info, ok := s.core.ServerInfo(p.ID); ok {
		if detail := strings.TrimSpace(info.ProductName + " " + info.ProductVersion); detail != "" {
			return detail
		}
	}
	if p.DBType == model.MySQL {
		return app.KindMySQL
	}
	return app.KindPostgres
}

func groupIcon(g app.Group) *ui.SVG {
	switch g {
	case app.GroupColumns:
		return icons.Columns
	case app.GroupIndexes:
		return icons.Key
	case app.GroupForeignKey:
		return icons.Link
	}
	return icons.Zap
}

func groupLabel(g app.Group) string {
	switch g {
	case app.GroupColumns:
		return "Columns"
	case app.GroupIndexes:
		return "Indexes"
	case app.GroupForeignKey:
		return "Foreign keys"
	}
	return "Triggers"
}

// hasTreeMenu reports whether the row answers a right-click. A group and a
// leaf without DDL have nothing to offer, and take the platform's menu.
func hasTreeMenu(n app.Node) bool {
	switch n.Kind {
	case app.KindConnection, app.KindDatabase, app.KindSchema, app.KindTable:
		return true
	case app.KindLeaf:
		return n.DDL != ""
	}
	return false
}

func (s *App) treeMenu(c *ui.Context, n app.Node, m *ui.Menu) {
	switch n.Kind {
	case app.KindConnection:
		if s.core.Status(n.Conn) == app.Connecting {
			if m.Item("Cancel connect").Chosen() {
				s.core.CancelConnect(n.Conn)
			}
		} else if m.Item("Reconnect").Chosen() {
			s.core.Reconnect(n.Conn)
		}
		if m.Item("Edit connection").Chosen() {
			s.core.OpenConnectionDialog(n.Conn)
		}
		if p, ok := s.core.Profile(n.Conn); ok && p.DBType.IsSQL() {
			if m.Item("Databases…").Chosen() {
				s.core.OpenDatabasesDialog(n.Conn)
			}
		}
	case app.KindDatabase, app.KindSchema:
		if m.Item("Reload").Chosen() {
			s.core.Reload(n)
		}
	case app.KindTable:
		label := "Pin table"
		if s.core.IsPinned(n) {
			label = "Unpin table"
		}
		if m.Item(label).Chosen() {
			s.core.TogglePin(n)
		}
		m.Separator()
		if m.Item("Generate DDL").Chosen() {
			s.core.GenerateDDL(n)
		}
	case app.KindLeaf:
		if m.Item("Copy DDL").Chosen() {
			c.WriteClipboard(n.DDL)
			s.core.Toast("DDL copied")
		}
	}
}
