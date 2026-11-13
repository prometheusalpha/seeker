package view

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"seeker/internal/app"
	"seeker/internal/icons"
)

// tabPillWidth caps a pill, so a long title cannot crowd the strip.
const tabPillWidth = 200

// renameState is the tab the rename modal edits. The state layer has no
// rename dialog, so the strip keeps the form on itself with ui.Local.
type renameState struct {
	open bool
	id   string
	name string
}

// tabStrip draws the tabs, pinned ones first, with the keys the strip
// takes. Shortcuts are registered as the strip builds, so they live as
// long as the workspace does; M6 makes them rebindable.
func (s *App) tabStrip(c *ui.Context, pal palette) {
	t := c.Theme()
	s.tabKeys(c)
	strip := ui.Row(c).Height(36).FillWidth().AlignItems(ui.Center).Padding(0, 6).Gap(2)
	ren := ui.Local(strip, "rename", func() renameState { return renameState{} })
	strip.Children(func() {
		// Actions run as their pill builds (a close, a menu choice), so the
		// loop reads a snapshot: a close mid-loop must not shift the tab
		// under the next index.
		tabs := append([]app.Tab(nil), s.core.Tabs().Items...)
		vis := s.core.VisibleTabs()
		pinned := 0
		for _, raw := range vis {
			if raw < len(tabs) && tabs[raw].Pinned {
				pinned++
			}
		}
		for i, raw := range vis {
			if raw >= len(tabs) {
				break
			}
			if pinned > 0 && pinned < len(vis) && i == pinned {
				ui.Box(c).Width(1).Height(16).Background(t.Border)
			}
			s.tabPill(c, pal, i, tabs[raw], ren)
		}
		if iconButton(c, icons.Plus, "New query tab").Clicked() {
			s.core.NewQueryTab()
		}
		s.renameDialog(c, ren)
	})
}

// tabPill is one tab: its icon, its title and the mark that it is unsaved
// or the button that closes it, draggable within the strip and with the
// tab menu.
func (s *App) tabPill(c *ui.Context, pal palette, index int, tab app.Tab, ren *renameState) {
	t := c.Theme()
	chosen := false
	if active, ok := s.core.ActiveTab(); ok && active.ID == tab.ID {
		chosen = true
	}
	dirty := s.core.TabDirty(tab)
	pill := ui.ButtonBase(c.Key("tab-"+tab.ID)).Height(26).Padding(0, 8).Gap(6).Radius(6).MaxWidth(tabPillWidth)
	switch {
	case chosen:
		pill.Background(pal.Active)
	case pill.Hovered():
		pill.Background(t.SurfaceHover)
	}
	icon, muted := ink(t, chosen)
	pill.Children(func() {
		if tab.Mode == app.ModeTable {
			ui.Icon(c, icons.Table).FontSize(13).TextColor(icon)
		} else {
			ui.Icon(c, icons.FileCode).FontSize(13).TextColor(icon)
		}
		title := tab.Title
		if dirty {
			title += " *"
		}
		ui.Text(c, title).FontSize(12).SingleLine().TextColor(t.Text)
		if dirty {
			ui.Box(c).Size(6, 6).Radius(3).Background(muted).Label("Unsaved")
		}
		// The close button stays on a dirty tab: closing one asks first, so
		// hiding the button would leave the dialog unreachable by click.
		close := ui.ButtonBase(c.Key("close-"+tab.ID)).Size(16, 16).Radius(4).
			Label("Close " + tab.Title).Tooltip("Close tab")
		if close.Hovered() {
			close.Background(t.SurfaceHover)
		}
		close.Children(func() { ui.Icon(c, icons.X).FontSize(11).TextColor(muted) })
		if close.Clicked() {
			s.core.CloseTab(tab.ID)
		}
	})
	if pill.Clicked() {
		s.core.Activate(tab.ID)
	}
	pill.ContextMenu(func(m *ui.Menu) {
		if m.Item("Rename").Chosen() {
			ren.open, ren.id, ren.name = true, tab.ID, tab.Title
		}
		label := "Pin"
		if tab.Pinned {
			label = "Unpin"
		}
		if m.Item(label).Chosen() {
			s.core.ToggleTabPin(tab.ID)
		}
		m.Separator()
		if m.Item("Close others").Disabled(len(s.core.Tabs().Items) < 2).Chosen() {
			s.core.CloseOtherTabs(tab.ID)
		}
		if m.Item("Close").Chosen() {
			s.core.CloseTab(tab.ID)
		}
		if m.Item("Close all").Chosen() {
			s.core.CloseAllTabs()
		}
	})
	pill.Drag(index)
	if from, ok := ui.Drop[int](pill); ok {
		s.core.MoveVisible(from, index)
	}
}

// renameDialog asks for a tab's title, the smallest native form of the
// strip's inline rename.
func (s *App) renameDialog(c *ui.Context, ren *renameState) {
	modal := ui.Modal(c, &ren.open, func() {
		ui.Column(c).Width(320).Padding(18).Gap(12).Children(func() {
			ui.Text(c, "Rename tab").FontSize(14).Bold()
			input := ui.TextInput(c, &ren.name).AutoFocus().FillWidth().Label("Tab title")
			ui.Row(c).FillWidth().Gap(8).Children(func() {
				ui.Spacer(c)
				if ui.Button(c, "Cancel").Clicked() {
					ren.open = false
				}
				if ui.PrimaryButton(c, "Rename").Clicked() || input.Submitted() {
					s.renameTab(ren)
				}
			})
		})
	})
	if modal.Dismissed() {
		ren.open = false
	}
}

func (s *App) renameTab(ren *renameState) {
	if name := strings.TrimSpace(ren.name); name != "" {
		s.core.Rename(ren.id, name)
	}
	ren.open = false
}

// tabKeys registers the strip's shortcuts: closing and reopening tabs, and
// stepping through the visible order, wrapping at its ends.
func (s *App) tabKeys(c *ui.Context) {
	if c.Shortcut(ui.Cmd, ui.KeyW) {
		s.closeActiveTab()
	}
	if c.Shortcut(ui.Cmd|ui.Shift, ui.KeyW) {
		s.closeOtherTabs()
	}
	if c.Shortcut(ui.Cmd|ui.Alt, ui.KeyW) {
		s.core.CloseAllTabs()
	}
	if c.Shortcut(ui.Cmd|ui.Shift, ui.KeyT) {
		s.core.ReopenClosedTab()
	}
	if c.Shortcut(ui.Cmd|ui.Alt, ui.KeyRight) {
		s.stepTab(1)
	}
	if c.Shortcut(ui.Cmd|ui.Alt, ui.KeyLeft) {
		s.stepTab(-1)
	}
}

func (s *App) closeActiveTab() {
	if tab, ok := s.core.ActiveTab(); ok {
		s.core.CloseTab(tab.ID)
	}
}

func (s *App) closeOtherTabs() {
	if tab, ok := s.core.ActiveTab(); ok {
		s.core.CloseOtherTabs(tab.ID)
	}
}

// stepTab shows the tab delta places away in the strip's order, from the
// last to the first.
func (s *App) stepTab(delta int) {
	vis := s.core.VisibleTabs()
	if len(vis) == 0 {
		return
	}
	active := s.core.Tabs().Active
	pos := 0
	for i, raw := range vis {
		if raw == active {
			pos = i
			break
		}
	}
	pos = (pos + delta + len(vis)) % len(vis)
	s.core.Activate(s.core.Tabs().Items[vis[pos]].ID)
}

// closeDialog is the unsaved-SQL confirmation shared by close, close others
// and close all: one dialog for every dirty tab the close queued.
func (s *App) closeDialog(c *ui.Context) {
	t := c.Theme()
	d := &s.core.DialogsState().Close
	modal := ui.Modal(c, &d.Open, func() {
		ui.Column(c).Width(420).Padding(20).Gap(14).Children(func() {
			ui.Text(c, "Unsaved changes").FontSize(16).Bold()
			ui.Text(c, closeBody(s.core.PendingTabs())).FontSize(12).TextColor(t.TextMuted)
			ui.Row(c).FillWidth().Gap(8).Children(func() {
				ui.Spacer(c)
				if ui.Button(c, "Cancel").Clicked() {
					s.core.CancelClose()
				}
				if ui.Button(c, "Discard").Clicked() {
					s.core.ConfirmClose(false)
				}
				if ui.PrimaryButton(c, "Save").Clicked() {
					s.core.ConfirmClose(true)
				}
			})
		})
	})
	// Escape or a click outside the panel is Cancel, as recall's queue
	// clearing setter makes it.
	if modal.Dismissed() {
		s.core.CancelClose()
	}
}

func closeBody(tabs []app.Tab) string {
	if len(tabs) == 1 {
		return fmt.Sprintf("%s has SQL that has not been saved. Save it, discard it, or go back to editing?", tabs[0].Title)
	}
	titles := make([]string, len(tabs))
	for i, tab := range tabs {
		titles[i] = tab.Title
	}
	return fmt.Sprintf("%d tabs have SQL that has not been saved (%s). Save them, discard them, or go back to editing?",
		len(tabs), strings.Join(titles, ", "))
}
