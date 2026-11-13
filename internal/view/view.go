// Package view builds seeker's window: a title bar over islands, rounded
// panels floating on the window's canvas, for the sidebar, the editor and
// the results.
package view

import (
	"slices"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"seeker/internal/app"
	"seeker/internal/icons"
	"seeker/internal/theme"
)

// Islands round only a little, so the panels read as panels, not cards.
const (
	islandRadius = 8
	islandGap    = 6
)

type palette = theme.Islands

type App struct {
	core *app.App

	// One buffer until M3 gives each tab its own.
	code    *editor.State
	codeTab string
	codeVer uint64

	appearance   int
	settingsOpen bool
	systemDark   bool

	resultView int
}

func New(core *app.App) *App {
	buf := editor.NewBuffer("")
	buf.SetLanguage(editor.LanguageFor("query.sql"))
	// The saved appearance is the segmented control's starting choice;
	// otherwise the first frame writes System over it.
	return &App{core: core, code: &editor.State{Buffer: buf},
		appearance: max(0, slices.Index(appearances, core.UI().Theme.Appearance))}
}

var appearances = []theme.Appearance{theme.System, theme.Light, theme.Dark}

func (s *App) View(c *ui.Context) {
	// Before SetTheme, the frame's theme is the system's.
	s.systemDark = c.Theme().Dark
	u := s.core.UI()
	u.Theme.Appearance = appearances[s.appearance]
	res := theme.Resolve(u.Theme, c.Theme())
	c.SetTheme(&res.UI)
	s.code.Theme = res.Code
	pal := res.Islands
	ui.Column(c).Fill().Background(pal.Canvas).Children(func() {
		s.titleBar(c)
		ui.Row(c).Grow(1).FillWidth().Padding(0, islandGap, islandGap, islandGap).Children(func() {
			s.body(c, pal)
		})
	})
	s.settingsDialog(c)
	s.dialogs(c, pal)
	s.toasts(c)
	s.syncEditor()
	s.core.Sync()
	s.core.ConnSync()
}

// ink is what icons and secondary text on a row take: on a chosen row,
// filled with the accent (or its gray while the list is not focused), they
// take AccentText as the row's own text does.
func ink(t *ui.Theme, chosen bool) (icon, muted ui.Color) {
	if chosen {
		return t.AccentText, t.AccentText.Alpha(0.75)
	}
	return t.TextMuted, t.TextMuted
}

func (s *App) body(c *ui.Context, pal palette) {
	center := func() { s.workspace(c, pal) }
	if s.core.UI().HideSidebar {
		center()
		return
	}
	gapSplit(c, pal, false, func(first, second func(func())) {
		ui.Split(c, &s.core.UI().SidebarW, func() { first(func() { s.sidebar(c, pal) }) }, func() { second(center) }).SplitReset(260).Fill()
	})
}

// gapSplit lets a split's divider stand for the gap between two islands:
// its line takes the canvas color, so it shows only as the accent while
// hovered or dragged, and each pane keeps half the gap. Split draws the
// line in the theme's Border, hence the theme around it; the panes get the
// window's theme back.
func gapSplit(c *ui.Context, pal palette, vertical bool, build func(first, second func(func()))) {
	base := c.Theme()
	hidden := *base
	hidden.Border = pal.Canvas
	half := float32(islandGap-1) / 2
	pane := func(pad []float32) func(func()) {
		return func(content func()) {
			ui.Themed(c, base).Background(ui.Transparent).Fill().Padding(pad...).Children(content)
		}
	}
	first, second := pane([]float32{0, 0, 0, half}), pane([]float32{0, half, 0, 0})
	if vertical {
		first, second = pane([]float32{0, 0, half, 0}), pane([]float32{half, 0, 0, 0})
	}
	ui.Themed(c, &hidden).Background(ui.Transparent).Fill().Children(func() { build(first, second) })
}

func island(c *ui.Context, pal palette) ui.Element {
	return ui.Column(c).Background(pal.Island).Radius(islandRadius).Border(1, pal.Edge).Clip()
}

func iconButton(c *ui.Context, icon *ui.SVG, label string) ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c.Key(label)).Size(28, 28).Radius(6).Label(label).Tooltip(label)
	if b.Hovered() {
		b.Background(t.SurfaceHover)
	}
	b.Children(func() { ui.Icon(c, icon).FontSize(15).TextColor(t.TextMuted) })
	return b
}

func (s *App) titleBar(c *ui.Context) {
	t := c.Theme()
	bar := c.TitleBar()
	u := s.core.UI()
	ui.Row(c).Height(max(bar.Height, 40)).FillWidth().AlignItems(ui.Center).Gap(2).
		Padding(0, bar.Right+10, 0, bar.Left+10).DragWindow().Children(func() {
		if iconButton(c, icons.PanelLeft, "Toggle connections").Clicked() {
			u.HideSidebar = !u.HideSidebar
		}
		ui.Spacer(c)
		// The quick open field arrives with search (M5); it shows the keys
		// it will take but takes none until then.
		ui.Row(c).Width(360).Height(26).Radius(6).Padding(0, 10).Gap(8).
			Background(t.Text.Alpha(0.06)).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons.Search).FontSize(13).TextColor(t.TextMuted)
			ui.Text(c, "Search tables, files, connections").FontSize(12).TextColor(t.TextMuted).SingleLine()
			ui.Spacer(c)
			ui.Text(c, "⌘P").FontSize(11).TextColor(t.TextMuted)
		})
		ui.Spacer(c)
		if iconButton(c, icons.Plus, "New connection").Clicked() {
			s.core.OpenConnectionDialog("")
		}
		if iconButton(c, icons.Moon, "Toggle light and dark").Clicked() {
			if u.Theme.IsDark(s.systemDark) {
				s.appearance = 1
			} else {
				s.appearance = 2
			}
		}
		if iconButton(c, icons.Settings, "Settings").Clicked() {
			s.settingsOpen = true
		}
	})
}

func (s *App) workspace(c *ui.Context, pal palette) {
	// A table's rows take the whole workspace, without the editor above
	// them, as recall shows a table tab.
	if tab, ok := s.core.ActiveTab(); ok && tab.Mode == app.ModeTable {
		island(c, pal).Fill().Children(func() {
			s.tabStrip(c, pal)
			ui.Divider(c)
			s.results(c)
		})
		return
	}
	gapSplit(c, pal, true, func(first, second func(func())) {
		ui.SplitVerticalPercent(c, &s.core.UI().EditorPct, func() {
			first(func() {
				island(c, pal).Fill().Children(func() {
					s.tabStrip(c, pal)
					s.runBar(c)
					ui.Divider(c)
					editor.View(c, s.code).Grow(1).FillWidth()
				})
			})
		}, func() {
			second(func() {
				island(c, pal).Fill().Children(func() { s.results(c) })
			})
		}).Fill()
	})
}

// runBar names the tab's target and the keys SQL will take; running, explain
// and format arrive with the editor (M3) and the plan viewer (M6).
func (s *App) runBar(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Height(36).FillWidth().AlignItems(ui.Center).Padding(0, 8).Gap(6).Children(func() {
		ui.Button(c.Key("run"), "Run").Height(26).Disabled(true).Tooltip("Running SQL arrives with the editor (M3)")
		ui.Row(c).Height(26).Radius(6).Padding(0, 8).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons.Database).FontSize(12).TextColor(t.Accent)
			ui.Text(c, s.datasourceLabel()).FontSize(12).SingleLine()
			ui.Icon(c, icons.ChevronDown).FontSize(12).TextColor(t.TextMuted)
		})
		ui.Spacer(c)
		if tab, ok := s.core.ActiveTab(); ok {
			state := "Saved"
			if tab.Mode == app.ModeQuery && s.core.TabDirty(tab) {
				state = "Unsaved"
			}
			ui.Text(c, state).FontSize(11).TextColor(t.TextMuted)
		}
	})
}

// datasourceLabel is the active tab's connection and database, "" for no
// tab, as the editor's datasource picker shows it.
func (s *App) datasourceLabel() string {
	tab, ok := s.core.ActiveTab()
	if !ok {
		return "No datasource"
	}
	name := tab.Conn
	if p, ok := s.core.Profile(tab.Conn); ok {
		name = p.Name
	}
	if tab.Database != "" {
		return name + " · " + tab.Database
	}
	return name
}

func (s *App) results(c *ui.Context) {
	t := c.Theme()
	title, empty := "RESULTS", "Run a query to see its rows."
	if tab, ok := s.core.ActiveTab(); ok && tab.Mode == app.ModeTable {
		// The grid that lists the rows lands in M4; until then the tab
		// names its table and says what it holds.
		title, empty = tab.Table, "No rows loaded."
	}
	ui.Row(c).Height(36).FillWidth().AlignItems(ui.Center).Padding(0, 12).Gap(8).Children(func() {
		ui.Text(c, title).FontSize(11).FontWeight(600).TextColor(t.TextMuted).SingleLine()
		ui.Spacer(c)
	})
	ui.Divider(c)
	ui.Box(c).Grow(1).FillWidth().Center().Children(func() {
		ui.Text(c, empty).FontSize(12).TextColor(t.TextMuted)
	})
}

// toasts shows what the app queued; a message with an action stays until
// the user takes it or closes it.
func (s *App) toasts(c *ui.Context) {
	for _, m := range s.core.TakeToasts() {
		if m.Action != "" && m.Run != nil {
			c.ToastAction(m.Message, m.Action, m.Run)
			continue
		}
		c.Toast(m.Message)
	}
}

// syncEditor moves the editor onto the active tab and writes its text back,
// so a tab is dirty the moment it differs from what it was saved with.
func (s *App) syncEditor() {
	tab, ok := s.core.ActiveTab()
	switch {
	case !ok:
		if s.codeTab != "" {
			s.code.Buffer.Set("")
			s.codeTab, s.codeVer = "", s.code.Buffer.Version()
		}
	case tab.ID != s.codeTab:
		s.code.Buffer.Set(tab.SQL)
		s.codeTab, s.codeVer = tab.ID, s.code.Buffer.Version()
	case s.code.Buffer.Version() != s.codeVer:
		s.codeVer = s.code.Buffer.Version()
		s.core.SetTabSQL(tab.ID, s.code.Buffer.String())
	}
}
