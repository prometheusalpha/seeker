// Package icons holds the Lucide icons the app shows, converted from
// lucide-vue-next 0.470.0 so they match recall's.
package icons

import (
	"embed"

	"github.com/egoist/mygo/ui"
)

//go:embed svg/*.svg
var files embed.FS

func load(name string) *ui.SVG {
	data, err := files.ReadFile("svg/" + name + ".svg")
	if err != nil {
		panic(err)
	}
	return ui.MustParseSVG(data)
}

var (
	ChevronDown  = load("chevron-down")
	ChevronsUp   = load("chevrons-up")
	Crosshair    = load("crosshair")
	Columns      = load("columns-3")
	Database     = load("database")
	Download     = load("download")
	Eye          = load("eye")
	FileCode     = load("file-code")
	Folder       = load("folder")
	FolderTree   = load("folder-tree")
	Key          = load("key-round")
	Link         = load("link")
	ListTree     = load("list-tree")
	Moon         = load("moon")
	PanelLeft    = load("panel-left")
	Pin          = load("pin")
	Play         = load("play")
	Plus         = load("plus")
	Refresh      = load("refresh-cw")
	Keyboard     = load("keyboard")
	Palette      = load("palette")
	Search       = load("search")
	Settings     = load("settings")
	Square       = load("square")
	Table        = load("table-2")
	WandSparkles = load("wand-sparkles")
	X            = load("x")
	Zap          = load("zap")
)
