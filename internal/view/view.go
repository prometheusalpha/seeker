// Package view builds seeker's window: a title bar over islands, rounded
// panels floating on the window's canvas, for the sidebar, the editor,
// the results and the SQL files.
package view

import (
	"path"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"seeker/internal/icons"
	"seeker/internal/theme"
)

// Islands round only a little, so the panels read as panels, not cards.
const (
	islandRadius = 8
	islandGap    = 6
)

type palette = theme.Islands

type tab struct {
	title string
	table bool
	dirty bool
}

type App struct {
	settings     theme.Settings
	appearance   int // index into appearances, bound to the settings' segmented control
	settingsOpen bool
	systemDark   bool

	sidebarW    float32
	filesW      float32
	editorPct   float32
	hideSidebar bool
	hideFiles   bool

	treeSearch string
	tree       ui.OutlineState[string]
	treeRow    int
	files      ui.OutlineState[string]
	fileRow    int

	tabs   []tab
	active int
	code   *editor.State

	resultView int
	grid       ui.ListState
	gridSel    ui.Selection[int]
	cols       []ui.TableColumn
	rows       [][]string
}

func New() *App {
	buf := editor.NewBuffer(mockSQL)
	buf.SetLanguage(editor.LanguageFor("query.sql"))
	a := &App{
		settings:  theme.DefaultSettings(),
		sidebarW:  260,
		filesW:    240,
		editorPct: 48,
		treeRow:   -1,
		fileRow:   -1,
		tabs: []tab{
			{title: "revenue.sql", dirty: true},
			{title: "users", table: true},
			{title: "scratch.sql"},
		},
		code: &editor.State{Buffer: buf},
		rows: mockRows(500),
	}
	for _, p := range []string{"local-pg", "local-pg/app_db", "local-pg/app_db/public"} {
		a.tree.Open.Add(p)
	}
	a.files.Open.Add("~/sql")
	a.grid.Selection, a.grid.CellSelect = &a.gridSel, true
	a.cols = []ui.TableColumn{{ID: "#", Title: "", RowHeader: true, Fixed: true, Width: 48, Align: ui.End}}
	for _, name := range mockColumns {
		a.cols = append(a.cols, ui.TableColumn{ID: name, Title: name, Width: 150})
	}
	return a
}

var appearances = []theme.Appearance{theme.System, theme.Light, theme.Dark}

func (a *App) View(c *ui.Context) {
	// Before SetTheme, the frame's theme is the system's.
	a.systemDark = c.Theme().Dark
	a.settings.Appearance = appearances[a.appearance]
	res := theme.Resolve(a.settings, c.Theme())
	c.SetTheme(&res.UI)
	a.code.Theme = res.Code
	pal := res.Islands
	ui.Column(c).Fill().Background(pal.Canvas).Children(func() {
		a.titleBar(c)
		ui.Row(c).Grow(1).FillWidth().Padding(0, islandGap, islandGap, islandGap).Children(func() {
			a.body(c, pal)
		})
	})
	a.settingsDialog(c)
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

func chosenItem(s *ui.OutlineState[string], row int, item string) bool {
	return row >= 0 && row < s.Rows() && s.Item(row) == item
}

func (a *App) body(c *ui.Context, pal palette) {
	center := func() { a.workspace(c, pal) }
	withFiles := center
	if !a.hideFiles {
		// Split sizes its first pane, so the files panel's width is what is
		// left of the room once the workspace takes its share.
		withFiles = func() {
			ui.Row(c).Fill().Gap(islandGap).AlignItems(ui.Stretch).Children(func() {
				ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Children(center)
				a.filesPanel(c, pal)
			})
		}
	}
	if a.hideSidebar {
		withFiles()
		return
	}
	gapSplit(c, pal, false, func(first, second func(func())) {
		ui.Split(c, &a.sidebarW, func() { first(func() { a.sidebar(c, pal) }) }, func() { second(withFiles) }).SplitReset(260).Fill()
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
	first, second := pane([]float32{0, half, 0, 0}), pane([]float32{0, 0, 0, half})
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

func (a *App) titleBar(c *ui.Context) {
	t := c.Theme()
	bar := c.TitleBar()
	ui.Row(c).Height(max(bar.Height, 40)).FillWidth().AlignItems(ui.Center).Gap(2).
		Padding(0, bar.Right+10, 0, bar.Left+10).DragWindow().Children(func() {
		if iconButton(c, icons.PanelLeft, "Toggle connections").Clicked() {
			a.hideSidebar = !a.hideSidebar
		}
		ui.Spacer(c)
		search := ui.ButtonBase(c.Key("quick-open")).Width(360).Height(26).Radius(6).
			Padding(0, 10).Gap(8).Background(t.Text.Alpha(0.06)).Label("Quick open")
		search.Children(func() {
			ui.Icon(c, icons.Search).FontSize(13).TextColor(t.TextMuted)
			ui.Text(c, "Search tables, files, connections").FontSize(12).TextColor(t.TextMuted).SingleLine()
			ui.Spacer(c)
			ui.Text(c, "⌘P").FontSize(11).TextColor(t.TextMuted)
		})
		ui.Spacer(c)
		iconButton(c, icons.Plus, "New connection")
		if iconButton(c, icons.FolderTree, "Toggle SQL files").Clicked() {
			a.hideFiles = !a.hideFiles
		}
		if iconButton(c, icons.Moon, "Toggle light and dark").Clicked() {
			if a.settings.IsDark(a.systemDark) {
				a.appearance = 1
			} else {
				a.appearance = 2
			}
		}
		if iconButton(c, icons.Settings, "Settings").Clicked() {
			a.settingsOpen = true
		}
	})
}

func panelHeader(c *ui.Context, title string, actions func()) {
	t := c.Theme()
	ui.Row(c).Height(34).FillWidth().AlignItems(ui.Center).Padding(0, 6, 0, 12).Children(func() {
		ui.Text(c, title).FontSize(11).FontWeight(600).TextColor(t.TextMuted)
		ui.Spacer(c)
		actions()
	})
}

func (a *App) sidebar(c *ui.Context, pal palette) {
	t := c.Theme()
	island(c, pal).Fill().Children(func() {
		panelHeader(c, "CONNECTIONS", func() {
			iconButton(c, icons.Plus, "Add connection")
		})
		ui.Box(c).FillWidth().Padding(0, 8, 6, 8).Children(func() {
			ui.SearchField(c, &a.treeSearch).Placeholder("Filter").FillWidth().Label("Filter tables")
		})
		a.tree.List.Selected = &a.treeRow
		roots := make([]string, len(mockConns))
		for i, cn := range mockConns {
			roots[i] = cn.name
		}
		ui.Outline(c, &a.tree, roots, treeChildren, func(p string) {
			depth := strings.Count(p, "/")
			name := path.Base(p)
			chosen := chosenItem(&a.tree, a.treeRow, p)
			icon, muted := ink(t, chosen)
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				switch {
				case depth == 0:
					cn := mockConns[0]
					for _, x := range mockConns {
						if x.name == p {
							cn = x
						}
					}
					brand := t.Accent
					if chosen {
						brand = icon
					}
					ui.Icon(c, icons.Database).FontSize(14).TextColor(brand)
					ui.Text(c, name).SingleLine().FontWeight(500)
					if cn.connected {
						ui.Box(c).Size(6, 6).Radius(3).Background(t.Success)
					}
					ui.Text(c, cn.dialect).FontSize(11).TextColor(muted).SingleLine()
				case depth == 1:
					ui.Icon(c, icons.Database).FontSize(13).TextColor(icon)
					ui.Text(c, name).SingleLine()
				case depth == 2:
					ui.Icon(c, icons.ListTree).FontSize(13).TextColor(icon)
					ui.Text(c, name).SingleLine()
				case depth == 3:
					ui.Icon(c, icons.Table).FontSize(13).TextColor(icon)
					ui.Text(c, name).SingleLine()
				case depth == 4:
					ui.Text(c, name).FontSize(12).TextColor(muted).SingleLine()
				default:
					glyph := icons.Columns
					if strings.Contains(p, "/Indexes/") {
						glyph = icons.Key
					}
					ui.Icon(c, glyph).FontSize(12).TextColor(icon)
					ui.Text(c, name).FontSize(12).SingleLine()
				}
			})
		}).Grow(1).FillWidth()
	})
}

func (a *App) filesPanel(c *ui.Context, pal palette) {
	t := c.Theme()
	island(c, pal).Width(a.filesW).Shrink(0).Children(func() {
		panelHeader(c, "SQL FILES", func() {
			iconButton(c, icons.Plus, "New file")
			iconButton(c, icons.Folder, "Open folder")
		})
		a.files.List.Selected = &a.fileRow
		ui.Outline(c, &a.files, []string{"~/sql"}, func(p string) []string {
			return mockFiles[p]
		}, func(p string) {
			icon, _ := ink(t, chosenItem(&a.files, a.fileRow, p))
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				glyph := icons.Folder
				if strings.HasSuffix(p, ".sql") {
					glyph = icons.FileCode
				}
				ui.Icon(c, glyph).FontSize(13).TextColor(icon)
				ui.Text(c, path.Base(p)).SingleLine()
			})
		}).Grow(1).FillWidth()
	})
}

func (a *App) workspace(c *ui.Context, pal palette) {
	gapSplit(c, pal, true, func(first, second func(func())) {
		ui.SplitVerticalPercent(c, &a.editorPct, func() {
			first(func() {
				island(c, pal).Fill().Children(func() {
					a.tabStrip(c, pal)
					a.runBar(c)
					ui.Divider(c)
					editor.View(c, a.code).Grow(1).FillWidth()
				})
			})
		}, func() {
			second(func() {
				island(c, pal).Fill().Children(func() { a.results(c) })
			})
		}).Fill()
	})
}

func (a *App) tabStrip(c *ui.Context, pal palette) {
	t := c.Theme()
	ui.Row(c).Height(36).FillWidth().AlignItems(ui.Center).Padding(0, 6).Gap(2).Children(func() {
		for i, tb := range a.tabs {
			on := i == a.active
			b := ui.ButtonBase(c.Key("tab-"+tb.title)).Height(28).Radius(6).Padding(0, 6, 0, 10).Gap(6).Label(tb.title)
			switch {
			case on:
				b.Background(pal.Active)
			case b.Hovered():
				b.Background(t.SurfaceHover)
			}
			if b.Clicked() {
				a.active = i
			}
			b.Children(func() {
				icon := icons.FileCode
				if tb.table {
					icon = icons.Table
				}
				fg := t.TextMuted
				if on {
					fg = t.Text
				}
				ui.Icon(c, icon).FontSize(13).TextColor(fg)
				ui.Text(c, tb.title).FontSize(12).TextColor(fg).SingleLine()
				if tb.dirty {
					ui.Box(c).Size(6, 6).Radius(3).Background(t.TextMuted)
				} else {
					ui.Icon(c, icons.X).FontSize(12).TextColor(t.TextMuted)
				}
			})
		}
		iconButton(c, icons.Plus, "New tab")
	})
}

func (a *App) runBar(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Height(36).FillWidth().AlignItems(ui.Center).Padding(0, 8).Gap(6).Children(func() {
		ui.PrimaryButton(c.Key("run"), "").Height(26).Children(func() {
			ui.Icon(c, icons.Play).FontSize(12)
			ui.Text(c, "Run").FontSize(12).SingleLine()
		})
		ui.Button(c.Key("datasource"), "").Height(26).Children(func() {
			ui.Icon(c, icons.Database).FontSize(12).TextColor(t.Accent)
			ui.Text(c, "local-pg · app_db").FontSize(12).SingleLine()
			ui.Icon(c, icons.ChevronDown).FontSize(12).TextColor(t.TextMuted)
		})
		iconButton(c, icons.ListTree, "Explain plan  ⌥⇧E")
		iconButton(c, icons.WandSparkles, "Format SQL")
		ui.Spacer(c)
		ui.Text(c, "Saved").FontSize(11).TextColor(t.TextMuted)
	})
}

func (a *App) results(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Height(36).FillWidth().AlignItems(ui.Center).Padding(0, 6, 0, 8).Gap(8).Children(func() {
		ui.Segmented(c, &a.resultView, "Result", "Explain").Label("Result view")
		ui.Text(c, "500 rows · 38 ms").FontSize(11).TextColor(t.TextMuted)
		ui.Spacer(c)
		iconButton(c, icons.Refresh, "Rerun  ⌘R")
		iconButton(c, icons.Columns, "Columns")
		iconButton(c, icons.Download, "Export")
	})
	ui.Divider(c)
	// Rows of a data grid are denser than the rest of the app.
	dense := *t
	dense.Spacing = 3
	ui.Themed(c, &dense).Background(ui.Transparent).Grow(1).FillWidth().Children(func() { a.resultGrid(c, t) })
}

func (a *App) resultGrid(c *ui.Context, t *ui.Theme) {
	ui.Table(c, &a.grid, a.cols, len(a.rows), func(row, col int) {
		if col == 0 {
			_, muted := ink(t, a.grid.CellSelected(row, col))
			ui.Textf(c, "%d", row+1).FontSize(11).TextColor(muted).SingleLine()
			return
		}
		v, fg := a.rows[row][col-1], t.Text
		if v == "" {
			v, fg = "NULL", t.TextMuted.Alpha(0.6)
		}
		if a.grid.CellSelected(row, col) {
			if v == "NULL" {
				fg = t.AccentText.Alpha(0.6)
			} else {
				fg = t.AccentText
			}
		}
		ui.Text(c, v).FontSize(12).Font("monospace").TextColor(fg).SingleLine()
	}).Grow(1).FillWidth()
}
