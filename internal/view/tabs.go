package view

import (
	"fmt"
	"strings"
	"time"

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
	strip := ui.Row(c).Height(headerH).FillWidth().AlignItems(ui.Center).Padding(0, 10).Gap(6)
	ren := ui.Local(strip, "rename", func() renameState { return renameState{} })
	strip.Children(func() {
		// The pills sit in a capsule that scrolls when they outgrow the
		// strip, and the new-tab button stays out of it.
		if len(s.core.VisibleTabs()) > 0 {
			ui.ScrollHorizontal(c).Shrink(1).MinWidth(0).Children(func() {
				track(t, ui.Row(c)).Children(func() {
					s.tabPills(c, pal, ren)
				})
			})
		}
		if iconButton(c, icons.Plus, "New query tab").Clicked() {
			s.core.NewQueryTab()
		}
		s.renameDialog(c, ren)
	})
}

// tabPills draws the pills, pinned ones first, with a hairline between the
// pinned and the rest.
func (s *App) tabPills(c *ui.Context, pal palette, ren *renameState) {
	t := c.Theme()
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
			ui.Box(c).Width(1).Height(14).Background(t.Border)
		}
		s.tabPill(c, pal, i, tabs[raw], ren)
	}
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
	pill := ui.ButtonBase(c.Key("tab-"+tab.ID)).Height(tabH).Padding(0, 6, 0, 12).Gap(6).Radius(tabH / 2).MaxWidth(tabPillWidth).
		Transition(ui.ElementTransition{Colors: true, Duration: 160 * time.Millisecond})
	hovered := pill.Hovered()
	switch {
	case chosen:
		// A pill lifts off its track softly: on a dark theme the island's
		// own shadow is too heavy under something this small.
		sh := shadowOf(t)
		if t.Dark {
			sh = ui.RGBA(0, 0, 0, 0.2)
		}
		pill.Background(raised(t, pal)).Shadow(0, 1, 2, 0, sh).Shadow(0, 2, 8, 0, sh)
	case hovered:
		pill.Background(t.SurfaceHover)
	}
	// The pill is its own face, not the accent-filled row ink() is for.
	text := t.TextMuted
	if chosen {
		text = t.Text
	}
	pill.Children(func() {
		if tab.Mode == app.ModeTable {
			ui.Icon(c, icons.Table).FontSize(14).TextColor(t.TextMuted)
		} else {
			ui.Icon(c, icons.FileCode).FontSize(14).TextColor(t.TextMuted)
		}
		ui.Text(c, tab.Title).FontSize(12.5).FontWeight(500).SingleLine().TextColor(text)
		if dirty {
			ui.Box(c).Size(6, 6).Radius(3).Background(t.TextMuted).Label("Unsaved")
		}
		// The close button shows on the chosen pill and the hovered one; it
		// is only invisible elsewhere, so a dirty tab's close, which asks
		// first, stays reachable by click.
		close := ui.ButtonBase(c.Key("close-"+tab.ID)).Size(20, 20).Radius(10).
			Label("Close " + tab.Title).Tooltip("Close tab")
		if !chosen && !hovered {
			close.Opacity(0)
		}
		if close.Hovered() {
			close.Background(t.SurfaceHover)
		}
		close.Children(func() { ui.Icon(c, icons.X).FontSize(12).TextColor(t.TextMuted) })
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
	if s.pressed(c, app.TabClose) {
		s.closeActiveTab()
	}
	if s.pressed(c, app.TabCloseOthers) {
		s.closeOtherTabs()
	}
	if s.pressed(c, app.TabCloseAll) {
		s.core.CloseAllTabs()
	}
	if s.pressed(c, app.TabReopen) {
		s.core.ReopenClosedTab()
	}
	if s.pressed(c, app.TabNext) || s.pressed(c, app.TabNextAlt) {
		s.stepTab(1)
	}
	if s.pressed(c, app.TabPrev) || s.pressed(c, app.TabPrevAlt) {
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
// StepTab moves to the next tab (1) or the previous (-1), wrapping; the
// native menu's items call it, as macOS hands ⌃Tab to a menu's key
// equivalent and never to the window.
func (s *App) StepTab(delta int) { s.stepTab(delta) }

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
		ui.Column(c).Width(420).Gap(14).Children(func() {
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
