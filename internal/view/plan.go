package view

import (
	"math"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"seeker/internal/app"
	"seeker/internal/explain"
	"seeker/internal/icons"
)

// The plan viewer's three ways to read a plan, as recall's: the node canvas,
// the operator tree and the server's own answer.
const (
	planCanvas = iota
	planTree
	planRaw
)

const (
	planMinZoom = 0.5
	planMaxZoom = 1.6
	planFitZoom = 1.3
	planStep    = 0.15
)

// planView is what the viewer keeps of one tab's plan: the way it is read,
// the node chosen, the canvas's zoom and pan. It is dropped with the plan, so
// a new Explain starts at the canvas, fitted, with nothing chosen.
type planView struct {
	plan   *explain.Plan
	layout *explain.Layout
	mode   int
	sel    *explain.CanvasNode
	zoom   float32
	// pan is the canvas's offset in the viewport, before the zoom.
	panX, panY float32
	// vw, vh are the viewport's size as it was last painted, 0 before; fit
	// waits for them.
	vw, vh float32
	fit    bool
}

func (s *App) planViewOf(tabID string, p *explain.Plan) *planView {
	if s.plans == nil {
		s.plans = map[string]*planView{}
	}
	pv := s.plans[tabID]
	if pv == nil || pv.plan != p {
		pv = &planView{plan: p, layout: explain.BuildCanvas(p.Nodes), zoom: 1, fit: true}
		// A plan the parser did not read has no nodes to draw: open raw
		// rather than an empty canvas.
		if len(p.Nodes) == 0 {
			pv.mode = planRaw
		}
		s.plans[tabID] = pv
	}
	return pv
}

// planPane shows an Explain's answer: the viewer for a plan, else why there
// is none yet.
func (s *App) planPane(c *ui.Context, tabID string, r *app.RunView) {
	t := c.Theme()
	switch {
	case r.Explaining:
		s.resultsNote(c, "Explaining…")
	case r.PlanErr != "":
		ui.Scroll(c).Grow(1).FillWidth().Children(func() {
			ui.Column(c).FillWidth().Padding(12).Gap(8).Children(func() {
				ui.Text(c, "Explain failed: "+r.PlanErr).FontSize(12).TextColor(t.Danger).Selectable().Wrap()
				if r.PlanSQL != "" {
					s.tableFont(ui.Text(c, r.PlanSQL), -1).TextColor(t.TextMuted).Selectable()
				}
			})
		})
	case r.Plan != nil && r.Parsed != nil:
		s.planViewer(c, s.planViewOf(tabID, r.Parsed), r)
	default:
		s.resultsNote(c, "Explain the statement under the caret ("+s.chordText(app.EditorExplain)+").")
	}
}

func (s *App) planViewer(c *ui.Context, pv *planView, r *app.RunView) {
	t := c.Theme()
	ui.Row(c).Height(34).FillWidth().AlignItems(ui.Center).Padding(0, 12).Gap(10).Children(func() {
		ui.Icon(c, icons.GitBranch).FontSize(14).TextColor(t.TextMuted)
		ui.Text(c, "Explain plan").FontSize(12).FontWeight(600).SingleLine()
		n := len(pv.layout.Nodes)
		ui.Text(c, strconv.Itoa(n)+" "+plural(n, "node", "nodes")).FontSize(11).TextColor(t.TextMuted).SingleLine()
		ui.Spacer(c)
		if len(pv.plan.Nodes) > 0 {
			ui.Segmented(c.Key("planmode"), &pv.mode, "Canvas", "Tree", "Raw").Height(24)
		}
	})
	ui.Divider(c)
	switch pv.mode {
	case planCanvas:
		s.planCanvasView(c, pv)
	case planTree:
		s.planTreeView(c, pv)
	default:
		s.planRawView(c, pv, r)
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// categoryColor are recall's colours for the kinds of operator.
func categoryColor(t *ui.Theme, cat explain.Category) ui.Color {
	switch cat {
	case explain.Result:
		return ui.Hex("#a78bfa")
	case explain.Sort:
		return ui.Hex("#38bdf8")
	case explain.Join:
		return ui.Hex("#f472b6")
	case explain.TableScan:
		return ui.Hex("#fb923c")
	case explain.IndexScan:
		return ui.Hex("#34d399")
	case explain.Lookup:
		return ui.Hex("#2dd4bf")
	case explain.Materialize:
		return ui.Hex("#94a3b8")
	case explain.Aggregate:
		return ui.Hex("#c084fc")
	case explain.Exchange:
		return ui.Hex("#facc15")
	case explain.Modify:
		return ui.Hex("#f87171")
	}
	return t.TextMuted
}

func categoryIcon(cat explain.Category) *ui.SVG {
	switch cat {
	case explain.Result:
		return icons.MonitorCheck
	case explain.Sort:
		return icons.ArrowDownWideNarrow
	case explain.Join:
		return icons.GitMerge
	case explain.TableScan:
		return icons.Table
	case explain.IndexScan:
		return icons.ListTree
	case explain.Lookup:
		return icons.Key
	case explain.Materialize:
		return icons.Layers
	case explain.Aggregate:
		return icons.Sigma
	case explain.Exchange:
		return icons.Split
	case explain.Modify:
		return icons.Pencil
	}
	return icons.CircleHelp
}

var categoryLabels = [...]string{"Other", "Result", "Sort", "Join", "Table scan", "Index scan", "Lookup", "Materialize", "Aggregate", "Exchange", "Modify"}

func heatColor(t *ui.Theme, share explain.Num) ui.Color {
	switch explain.HeatOf(share) {
	case explain.HeatCool:
		return ui.Hex("#22c55e")
	case explain.HeatWarm:
		return ui.Hex("#f59e0b")
	case explain.HeatHot:
		return ui.Hex("#ef4444")
	}
	return t.TextMuted
}

func costPercent(share explain.Num) string {
	if !share.OK {
		return ""
	}
	return strconv.FormatFloat(share.V*100, 'f', 1, 64) + "%"
}

// rowMismatch flags a node whose measured rows are at least twice, or at most
// half, the estimate.
func rowMismatch(n *explain.CanvasNode) string {
	if !n.ActualRows.OK || !n.Rows.OK || n.Rows.V == 0 {
		return ""
	}
	ratio := n.ActualRows.V / n.Rows.V
	if ratio >= 2 || ratio <= 0.5 {
		return "×" + strconv.FormatFloat(ratio, 'f', 1, 64)
	}
	return ""
}

func nodeName(n *explain.Node) string {
	if n.NodeType != "" {
		return n.NodeType
	}
	return n.Title
}

// planCanvasView is the plan as cards joined by edges, which pan by dragging
// and zoom by the buttons. The inspector opens beside it for a node chosen.
func (s *App) planCanvasView(c *ui.Context, pv *planView) {
	t := c.Theme()
	l := pv.layout
	ui.Row(c).Grow(1).FillWidth().Children(func() {
		ui.Column(c).Grow(1).FillHeight().Children(func() {
			ui.Row(c).Height(30).FillWidth().AlignItems(ui.Center).Padding(0, 8).Gap(2).Children(func() {
				ui.Spacer(c)
				if iconButton(c, icons.ZoomOut, "Zoom out").Clicked() {
					pv.setZoom(pv.zoom - planStep)
				}
				if iconButton(c, icons.Maximize, "Fit to view").Clicked() {
					pv.fit = true
				}
				if iconButton(c, icons.ZoomIn, "Zoom in").Clicked() {
					pv.setZoom(pv.zoom + planStep)
				}
				ui.Text(c, strconv.Itoa(int(math.Round(float64(pv.zoom)*100)))+"%").FontSize(11).TextColor(t.TextMuted).Width(40).TextAlign(ui.End)
			})
			ui.Divider(c)
			vp := ui.Box(c.Key("canvas")).Grow(1).FillWidth().Clip().Background(t.Background.Mix(t.Text, 0.02))
			// The viewport's size is read as it paints, for fit: only a
			// painter knows the box the layout gave it. A fit asked for
			// before the size is known runs when it is, in the next frame.
			vp.Draw(func(p *ui.Painter, r ui.Rect) {
				pv.vw, pv.vh = r.W, r.H
				if pv.fit {
					p.AnimationFrame()
				}
				grid := t.Border.Alpha(0.35)
				for x := float32(0); x < r.W; x += 28 {
					p.Line(r.X+x, r.Y, r.X+x, r.Y+r.H, 1, grid)
				}
				for y := float32(0); y < r.H; y += 28 {
					p.Line(r.X, r.Y+y, r.X+r.W, r.Y+y, 1, grid)
				}
			})
			if pv.fit && pv.vw > 0 {
				pv.fitToView()
			}
			if dx, dy, ok := vp.Dragged(); ok {
				pv.panX += dx / pv.zoom
				pv.panY += dy / pv.zoom
			}
			vp.Children(func() {
				ui.Box(c).Absolute().Left(pv.panX*pv.zoom).Top(pv.panY*pv.zoom).
					Size(float32(l.Width), float32(l.Height)).TransformOrigin(0, 0).Scale(pv.zoom, pv.zoom).
					Draw(func(p *ui.Painter, r ui.Rect) { s.planEdges(p, r, t, l) }).
					Children(func() {
						for i, n := range l.Nodes {
							s.planCard(c.Key("n"+strconv.Itoa(i)), pv, n)
						}
					})
			})
			ui.Divider(c)
			ui.Row(c).FillWidth().AlignItems(ui.Center).Padding(4, 12).Gap(10).Children(func() {
				ui.Text(c, "Self-cost share:").FontSize(11).TextColor(t.TextMuted)
				for _, k := range []struct {
					share explain.Num
					label string
				}{{explain.Num{V: 0, OK: true}, "< 5%"}, {explain.Num{V: 0.1, OK: true}, "5–20%"}, {explain.Num{V: 0.5, OK: true}, "> 20%"}} {
					ui.Row(c).AlignItems(ui.Center).Gap(4).Children(func() {
						ui.Box(c).Size(8, 8).Radius(2).Background(heatColor(t, k.share))
						ui.Text(c, k.label).FontSize(11).TextColor(t.TextMuted).SingleLine()
					})
				}
				ui.Text(c, "Edge width follows estimated rows.").FontSize(11).TextColor(t.TextMuted).SingleLine()
			})
		})
		if pv.sel != nil {
			ui.Divider(c).Vertical()
			s.planInspector(c, pv.sel)
		}
	})
}

func (pv *planView) setZoom(z float32) {
	pv.zoom = min(planMaxZoom, max(planMinZoom, float32(math.Round(float64(z)*100)/100)))
}

// fitToView zooms to take the whole plan, never past planFitZoom, and centres
// it.
func (pv *planView) fitToView() {
	pv.fit = false
	w, h := float32(pv.layout.Width), float32(pv.layout.Height)
	if w == 0 || h == 0 {
		return
	}
	pv.setZoom(min(pv.vw/w, pv.vh/h, planFitZoom))
	pv.panX = max(0, (pv.vw/pv.zoom-w)/2)
	pv.panY = max(0, (pv.vh/pv.zoom-h)/2)
}

// planEdges draws the elbows from each parent to its children, wider for more
// rows, with the child's estimate over the last stretch.
func (s *App) planEdges(p *ui.Painter, r ui.Rect, t *ui.Theme, l *explain.Layout) {
	col := t.TextMuted.Alpha(0.6)
	for _, e := range l.Edges {
		sx := r.X + float32(e.From.X) + explain.NodeW
		sy := r.Y + float32(e.From.Y) + explain.NodeH/2
		ex := r.X + float32(e.To.X)
		ey := r.Y + float32(e.To.Y) + explain.NodeH/2
		mx := ex - explain.GapX/2
		p.StrokePolyline([][2]float32{{sx, sy}, {mx, sy}, {mx, ey}, {ex, ey}}, float32(explain.EdgeWidth(e.Rows)), col)
		label := explain.FormatRows(e.Rows)
		p.Text(ex-8-float32(len(label))*5.6, ey-16, label, 10, t.TextMuted)
	}
}

// planCard is one node of the canvas: its heat on the left, the operator,
// the relation and index, and the estimate.
func (s *App) planCard(key *ui.Context, pv *planView, n *explain.CanvasNode) {
	t := key.Theme()
	selected := pv.sel == n
	b := ui.ButtonBase(key).Absolute().Left(float32(n.X)).Top(float32(n.Y)).Size(explain.NodeW, explain.NodeH).
		Radius(8).Clip().Background(t.Surface).Border(1, t.Border).Label(nodeName(n.Node))
	if selected {
		b.Border(2, t.Accent)
	} else if b.Hovered() {
		b.Background(t.SurfaceHover)
	}
	if b.Clicked() {
		pv.sel = n
	}
	col := categoryColor(t, n.Category)
	b.Children(func() {
		ui.Box(key).Absolute().Left(0).Top(0).Size(3, explain.NodeH).Background(heatColor(t, n.CostShare))
		ui.Column(key).Fill().Padding(7, 10, 7, 14).Gap(4).Children(func() {
			ui.Row(key).FillWidth().AlignItems(ui.Center).Gap(6).Children(func() {
				ui.Box(key).Size(20, 20).Radius(4).Background(col.Alpha(0.18)).Center().Children(func() {
					ui.Icon(key, categoryIcon(n.Category)).FontSize(12).TextColor(col)
				})
				ui.Text(key, nodeName(n.Node)).FontSize(12).FontWeight(600).SingleLine().Grow(1)
				if share := costPercent(n.CostShare); share != "" {
					ui.Text(key, share).FontSize(10).FontWeight(600).TextColor(heatColor(t, n.CostShare)).SingleLine()
				}
			})
			ui.Row(key).FillWidth().Gap(4).Children(func() {
				ui.Text(key, n.Node.Relation).FontSize(10).TextColor(t.TextMuted).SingleLine()
				if n.Node.Index != "" {
					ui.Text(key, "["+n.Node.Index+"]").FontSize(10).TextColor(categoryColor(t, explain.IndexScan)).SingleLine()
				}
			})
			ui.Row(key).FillWidth().Gap(6).Children(func() {
				ui.Text(key, "est. "+explain.FormatRows(n.Rows)).FontSize(10).TextColor(t.TextMuted).SingleLine()
				ui.Spacer(key)
				if m := rowMismatch(n); m != "" {
					ui.Text(key, m).FontSize(10).FontWeight(600).TextColor(t.Warning).SingleLine()
				}
			})
		})
	})
}

// planInspector is the side panel of the chosen node.
func (s *App) planInspector(c *ui.Context, n *explain.CanvasNode) {
	t := c.Theme()
	col := categoryColor(t, n.Category)
	ui.Scroll(c.Key("inspector")).Width(300).FillHeight().Children(func() {
		ui.Column(c).FillWidth().Children(func() {
			ui.Column(c).FillWidth().Padding(12).Gap(8).Children(func() {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
					ui.Box(c).Size(24, 24).Radius(5).Background(col.Alpha(0.18)).Center().Children(func() {
						ui.Icon(c, categoryIcon(n.Category)).FontSize(14).TextColor(col)
					})
					ui.Column(c).Grow(1).Children(func() {
						ui.Text(c, nodeName(n.Node)).FontSize(13).FontWeight(600).SingleLine()
						ui.Text(c, categoryLabels[n.Category]).FontSize(11).FontWeight(500).TextColor(col).SingleLine()
					})
				})
				if n.Node.Relation != "" || n.Node.Index != "" {
					rel := n.Node.Relation
					if n.Node.Index != "" {
						rel += " [" + n.Node.Index + "]"
					}
					s.tableFont(ui.Text(c, rel), -3).TextColor(t.TextMuted).Selectable()
				}
			})
			ui.Divider(c)
			stat := func(label, value string, color ui.Color) {
				ui.Column(c).Grow(1).Padding(8, 12).Gap(2).Children(func() {
					ui.Text(c, strings.ToUpper(label)).FontSize(10).TextColor(t.TextMuted).SingleLine()
					s.tableFont(ui.Text(c, value), -2).FontWeight(600).TextColor(color).SingleLine()
				})
			}
			or := func(s string) string {
				if s == "" {
					return "—"
				}
				return s
			}
			ui.Row(c).FillWidth().Children(func() {
				stat("Cost share", or(costPercent(n.CostShare)), heatColor(t, n.CostShare))
				stat("Cost", or(n.Node.Cost), t.Text)
			})
			ui.Divider(c)
			ui.Row(c).FillWidth().Children(func() {
				stat("Est. rows", explain.FormatRows(n.Rows), t.Text)
				actual := t.Text
				if rowMismatch(n) != "" {
					actual = t.Warning
				}
				stat("Actual rows", explain.FormatRows(n.ActualRows), actual)
			})
			ui.Divider(c)
			if len(n.Node.Details) > 0 {
				ui.Column(c).FillWidth().Padding(12).Gap(8).Children(func() {
					ui.Text(c, "DETAILS").FontSize(10).TextColor(t.TextMuted)
					for _, d := range n.Node.Details {
						label, value := "", strings.TrimSpace(d)
						if i := strings.Index(d, ":"); i >= 0 {
							label, value = strings.TrimSpace(d[:i]), strings.TrimSpace(d[i+1:])
						}
						if label == "" && value == "" {
							continue
						}
						ui.Column(c).FillWidth().Gap(2).Children(func() {
							if label != "" {
								ui.Text(c, label).FontSize(11).FontWeight(500).SingleLine()
							}
							s.tableFont(ui.Text(c, value), -3).TextColor(t.TextMuted).Selectable().Wrap()
						})
					}
				})
			}
		})
	})
}

// planTreeView is the operator tree, one node a row indented by depth, with
// a copy of it as text.
func (s *App) planTreeView(c *ui.Context, pv *planView) {
	t := c.Theme()
	ui.Row(c).Height(30).FillWidth().AlignItems(ui.Center).Padding(0, 12).Children(func() {
		ui.Spacer(c)
		if ui.Button(c.Key("copytree"), "Copy").Height(24).Tooltip("Copy the tree as text").Clicked() {
			c.WriteClipboard(explain.TreeText(pv.plan.Nodes))
			s.core.Toast("Tree copied")
		}
	})
	ui.Divider(c)
	ui.Scroll(c).Grow(1).FillWidth().Children(func() {
		ui.Column(c).FillWidth().Padding(8).Children(func() {
			var walk func(ns []*explain.Node, depth int)
			walk = func(ns []*explain.Node, depth int) {
				for _, n := range ns {
					cat := explain.Categorize(n.NodeType)
					if cat == explain.Other {
						cat = explain.Categorize(n.Title)
					}
					col := categoryColor(t, cat)
					ui.Row(c).FillWidth().AlignItems(ui.Center).Padding(3, 4, 3, float32(4+depth*18)).Gap(8).Children(func() {
						ui.Icon(c, categoryIcon(cat)).FontSize(13).TextColor(col)
						ui.Text(c, nodeName(n)).FontSize(12).FontWeight(600).SingleLine()
						if n.Relation != "" {
							s.tableFont(ui.Text(c, n.Relation), -3).TextColor(t.TextMuted).SingleLine()
						}
						if n.Index != "" {
							s.tableFont(ui.Text(c, "["+n.Index+"]"), -3).TextColor(categoryColor(t, explain.IndexScan)).SingleLine()
						}
						ui.Spacer(c)
						for _, f := range planFigures(n) {
							s.tableFont(ui.Text(c, f), -3).TextColor(t.TextMuted).SingleLine()
						}
					})
					walk(n.Children, depth+1)
				}
			}
			walk(pv.plan.Nodes, 0)
		})
	})
}

// planFigures are a node's cost, estimated rows and measured rows as the tree
// row and the copied tree show them: c:, e:, a:.
func planFigures(n *explain.Node) []string {
	var out []string
	if n.Cost != "" {
		out = append(out, "c:"+n.Cost)
	}
	if n.Rows != "" {
		out = append(out, "e:"+n.Rows)
	}
	for _, d := range n.Details {
		if v, ok := strings.CutPrefix(d, "Actual Rows:"); ok {
			out = append(out, "a:"+strings.TrimSpace(v))
			break
		}
	}
	return out
}

// planRawView is the statement and the server's answer as it came.
func (s *App) planRawView(c *ui.Context, pv *planView, r *app.RunView) {
	t := c.Theme()
	section := func(title, body, key string) {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Text(c, title).FontSize(10).TextColor(t.TextMuted)
			ui.Spacer(c)
			if ui.Button(c.Key(key), "Copy").Height(22).Clicked() {
				c.WriteClipboard(body)
				s.core.Toast(title + " copied")
			}
		})
		ui.Column(c).FillWidth().Padding(10).Radius(6).Background(t.Text.Alpha(0.04)).Children(func() {
			s.tableFont(ui.Text(c, body), -1).Selectable()
		})
	}
	ui.Scroll(c).Grow(1).FillWidth().Children(func() {
		ui.Column(c).FillWidth().Padding(12).Gap(8).Children(func() {
			if r.PlanSQL != "" {
				section("SQL", r.PlanSQL, "copysql")
			}
			if pv.plan.Raw != "" {
				section("Raw plan", pv.plan.Raw, "copyraw")
			}
		})
	})
}
