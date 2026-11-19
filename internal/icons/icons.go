// Package icons holds the Lucide icons the app shows, converted from
// lucide-vue-next 0.470.0 so they match recall's, and the engines' logos
// from simple-icons 16.30.0, filled with currentColor so they tint as the
// rest do.
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
	ArrowDownWideNarrow = load("arrow-down-wide-narrow")
	Braces              = load("braces")
	CircleHelp          = load("circle-help")
	Copy                = load("copy")
	FileText            = load("file-text")
	GitBranch           = load("git-branch")
	GitMerge            = load("git-merge")
	Layers              = load("layers")
	Maximize            = load("maximize-2")
	MonitorCheck        = load("monitor-check")
	Pencil              = load("pencil")
	Sigma               = load("sigma")
	Split               = load("split")
	Workflow            = load("workflow")
	ZoomIn              = load("zoom-in")
	ZoomOut             = load("zoom-out")
	Trash               = load("trash-2")
	Check               = load("check")
	Clock               = load("clock")
	ChevronDown         = load("chevron-down")
	ChevronsUp          = load("chevrons-up")
	Crosshair           = load("crosshair")
	Columns             = load("columns-3")
	Database            = load("database")
	Download            = load("download")
	Eye                 = load("eye")
	FileCode            = load("file-code")
	FileDown            = load("file-down")
	Folder              = load("folder")
	FolderTree          = load("folder-tree")
	Key                 = load("key-round")
	Link                = load("link")
	ListTree            = load("list-tree")
	Moon                = load("moon")
	MySQL               = load("mysql")
	PanelLeft           = load("panel-left")
	Pin                 = load("pin")
	Play                = load("play")
	Plus                = load("plus")
	Postgres            = load("postgresql")
	Redis               = load("redis")
	Refresh             = load("refresh-cw")
	Keyboard            = load("keyboard")
	Palette             = load("palette")
	Search              = load("search")
	Settings            = load("settings")
	Square              = load("square")
	Table               = load("table-2")
	WandSparkles        = load("wand-sparkles")
	X                   = load("x")
	Zap                 = load("zap")
)
