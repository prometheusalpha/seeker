// Package view builds seeker's window: a title bar over islands, rounded
// panels floating on the window's canvas, for the sidebar, the editor and
// the results.
package view

import (
	"slices"
	"time"

	"github.com/egoist/mygo/ui"

	"seeker/internal/app"
	"seeker/internal/icons"
	"seeker/internal/theme"
)

// Islands are soft cards, as megafactory's: generous corners, a roomy gap
// between them, and headers tall enough to breathe.
const (
	islandRadius = 12
	islandGap    = 8
	titleH       = 44
	headerH      = 40
	tabH         = 26
)

type palette = theme.Islands

type App struct {
	core *app.App

	appearance   int
	settingsOpen bool
	systemDark   bool

	// refocusGrid hands the keyboard back to the grid after a cell editor
	// closes; focusWhere moves it to the WHERE field (⌘L).
	refocusGrid bool
	focusWhere  bool
	// tableFontKey is the table font the grid's character advance was last
	// measured for.
	tableFontKey string
}

func New(core *app.App) *App {
	// The saved appearance is the segmented control's starting choice;
	// otherwise the first frame writes System over it.
	return &App{core: core,
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
	s.core.SetEditorTheme(res.Code)
	s.measureTableFont(c)
	s.core.SyncFont()
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
	s.quickKeys(c)
	s.findKeys(c)
	s.bookmarkKeys(c)
	s.core.SyncEditors()
	s.core.TableSync()
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
	main := func() { s.workspace(c, pal) }
	if !s.core.UI().HideSidebar {
		main = func() {
			gapSplit(c, pal, false, false, func(first, second func(func())) {
				ui.Split(c, &s.core.UI().SidebarW, func() { first(func() { s.sidebar(c, pal) }) }, func() { second(func() { s.workspace(c, pal) }) }).SplitReset(260).Fill()
			})
		}
	}
	if s.core.UI().HideFiles {
		main()
		return
	}
	// The files panel's width is the first pane's, as Split sizes it, and
	// the panel sits on the right: the split runs right to left, and each
	// pane lays its own content out left to right again.
	gapSplit(c, pal, false, true, func(first, second func(func())) {
		ltr := func(content func()) func() {
			return func() { ui.Box(c).Fill().Direction(ui.LeftToRight).Children(content) }
		}
		// The direction is the box's, set before the split is built: a split
		// reads it as it builds, to turn a drag of the divider the right way.
		ui.Box(c).Fill().Direction(ui.RightToLeft).Children(func() {
			ui.Split(c, &s.core.UI().FilesW, func() { first(ltr(func() { s.filesPanel(c, pal) })) }, func() { second(ltr(main)) }).
				SplitReset(240).Fill()
		})
	})
}

// gapSplit lets a split's divider stand for the gap between two islands:
// its line takes the canvas color, so it shows only as the accent while
// hovered or dragged, and each pane keeps half the gap. Split draws the
// line in the theme's Border, hence the theme around it; the panes get the
// window's theme back.
func gapSplit(c *ui.Context, pal palette, vertical, rtl bool, build func(first, second func(func()))) {
	base := c.Theme()
	hidden := *base
	hidden.Border = pal.Canvas
	half := float32(islandGap-1) / 2
	pane := func(pad []float32) func(func()) {
		return func(content func()) {
			ui.Themed(c, base).Background(ui.Transparent).Fill().Padding(pad...).Children(content)
		}
	}
	// Padding runs top, right, bottom, left: each pane keeps its half of
	// the gap on the side that faces the other.
	first, second := pane([]float32{0, half, 0, 0}), pane([]float32{0, 0, 0, half})
	if vertical {
		first, second = pane([]float32{0, 0, half, 0}), pane([]float32{half, 0, 0, 0})
	}
	if rtl {
		first, second = pane([]float32{0, 0, 0, half}), pane([]float32{0, half, 0, 0})
	}
	ui.Themed(c, &hidden).Background(ui.Transparent).Fill().Children(func() { build(first, second) })
}

// shadowOf is the color islands and raised pills cast: faint on a light
// canvas, deeper on a dark one.
func shadowOf(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.RGBA(0, 0, 0, 0.35)
	}
	return ui.RGBA(40, 40, 60, 0.07)
}

func island(c *ui.Context, pal palette) ui.Element {
	sh := shadowOf(c.Theme())
	return ui.Column(c).Background(pal.Island).Radius(islandRadius).Border(1, pal.Edge).Clip().
		Shadow(0, 1, 2, 0, sh).Shadow(0, 6, 22, -2, sh)
}

func iconButton(c *ui.Context, icon *ui.SVG, label string) ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c.Key(label)).Size(30, 30).Radius(11).Label(label).Tooltip(label)
	if b.Hovered() {
		b.Background(t.SurfaceHover)
	}
	b.Transition(ui.ElementTransition{Colors: true, Duration: 120 * time.Millisecond})
	b.Children(func() { ui.Icon(c, icon).FontSize(15).TextColor(t.TextMuted) })
	return b
}

// track styles a row of tabs as the capsule that holds them.
func track(t *ui.Theme, row ui.Element) ui.Element {
	return row.Height(tabH+4).Padding(2).Gap(2).Radius((tabH+4)/2).Shrink(0).
		Background(t.Text.Alpha(0.055)).Border(0.5, t.Text.Alpha(0.06)).AlignItems(ui.Center)
}

// raised is the face of the chosen pill in a track: lifted off the track
// rather than sunk into it: the island's own color on a light theme, a
// veil of the text color on a dark one, which keeps the track's tint.
func raised(t *ui.Theme, pal palette) ui.Color {
	if t.Dark {
		return t.Text.Alpha(0.12)
	}
	return pal.Island
}

func (s *App) titleBar(c *ui.Context) {
	t := c.Theme()
	bar := c.TitleBar()
	u := s.core.UI()
	ui.Row(c).Height(max(bar.Height, titleH)).FillWidth().AlignItems(ui.Center).Gap(2).
		Padding(0, bar.Right+10, 0, bar.Left+10).DragWindow().Children(func() {
		if iconButton(c, icons.PanelLeft, "Toggle connections").Clicked() {
			u.HideSidebar = !u.HideSidebar
		}
		ui.Spacer(c)
		search := ui.ButtonBase(c.Key("quick-open")).Width(360).Height(30).Radius(15).Padding(0, 14).Gap(8).
			Background(t.Text.Alpha(0.06)).AlignItems(ui.Center).Label("Search tables, files, connections")
		search.Children(func() {
			ui.Icon(c, icons.Search).FontSize(13).TextColor(t.TextMuted)
			ui.Text(c, "Search tables, files, connections").FontSize(12).TextColor(t.TextMuted).SingleLine()
			ui.Spacer(c)
			ui.Text(c, "⌘P").FontSize(11).TextColor(t.TextMuted)
		})
		if search.Clicked() {
			s.core.OpenQuickOpen()
		}
		ui.Spacer(c)
		if iconButton(c, icons.Plus, "New connection").Clicked() {
			s.core.OpenConnectionDialog("")
		}
		if iconButton(c, icons.FolderTree, "Toggle files").Clicked() {
			u.HideFiles = !u.HideFiles
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
	gapSplit(c, pal, true, false, func(first, second func(func())) {
		ui.SplitVerticalPercent(c, &s.core.UI().EditorPct, func() {
			first(func() {
				island(c, pal).Fill().Children(func() {
					s.tabStrip(c, pal)
					s.editorPane(c)
				})
			})
		}, func() {
			second(func() {
				island(c, pal).Fill().Children(func() { s.results(c) })
			})
		}).Fill()
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
