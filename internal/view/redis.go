package view

import (
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"seeker/internal/app"
	"seeker/internal/icons"
	"seeker/internal/redis"
)

// redisUI is what the view keeps of a Redis tab: the lists' scroll and
// choice, and where its splits sit. Like the splits of the editor, it lives
// for the session.
type redisUI struct {
	keys ui.ListState
	hash ui.ListState
	sel  int
	// keysPct is the key list's share of the top, topPct the top's share of
	// the tab, the console taking the rest.
	keysPct, topPct float32
}

func (s *App) redisState(id string) *redisUI {
	if s.redis == nil {
		s.redis = map[string]*redisUI{}
	}
	st := s.redis[id]
	if st == nil {
		st = &redisUI{sel: -1, keysPct: 34, topPct: 68}
		s.redis[id] = st
	}
	return st
}

// redisWorkspace is one Redis tab: keys on the left, the chosen key's value
// on the right, the console under both.
func (s *App) redisWorkspace(c *ui.Context, tab app.Tab) {
	rt := s.core.RedisOf(tab.ID)
	if rt == nil {
		s.resultsNote(c, "This tab has no Redis database.")
		return
	}
	st := s.redisState(tab.ID)
	ui.SplitVerticalPercent(c, &st.topPct, func() {
		ui.SplitPercent(c, &st.keysPct, func() {
			ui.Column(c).Fill().Children(func() { s.redisKeys(c, tab.ID, rt, st) })
		}, func() {
			ui.Column(c).Fill().Children(func() { s.redisValue(c, tab.ID, rt, st) })
		}).Fill()
	}, func() {
		ui.Column(c).Fill().Children(func() { s.redisConsole(c, tab.ID, rt) })
	}).Fill()
	s.redisNewKey(c, tab.ID, rt)
}

// redisBadge is the small uppercase label of a key's type.
func redisBadge(c *ui.Context, typ string) {
	t := c.Theme()
	ui.Box(c).Padding(1, 5).Radius(4).Background(t.Text.Alpha(0.07)).Shrink(0).Children(func() {
		ui.Text(c, strings.ToUpper(typ)).FontSize(9.5).FontWeight(650).TextColor(t.TextMuted).SingleLine()
	})
}

func (s *App) redisKeys(c *ui.Context, id string, rt *app.RedisTab, st *redisUI) {
	t := c.Theme()
	ui.Row(c).Height(headerH).FillWidth().AlignItems(ui.Center).Padding(0, 10).Gap(6).Shrink(0).Children(func() {
		in := ui.SearchField(c.Key("rpat-"+id), &rt.Pattern).Placeholder("Pattern, e.g. user:*").Label("Key pattern").Grow(1)
		if in.Submitted() {
			s.core.RedisSearch(id)
		}
		if iconButton(c, icons.Refresh, "Scan again").Clicked() {
			s.core.RedisRefresh(id)
		}
		if iconButton(c, icons.Plus, "New key").Clicked() {
			s.core.OpenRedisNewKey(id)
		}
	})
	ui.Divider(c)

	switch {
	case rt.Err != "":
		s.resultsNote(c, "Could not read keys: "+rt.Err)
	case len(rt.Keys) == 0 && (rt.Scanning || !rt.Done()):
		s.resultsNote(c, "Scanning…")
	case len(rt.Keys) == 0:
		s.resultsNote(c, "No keys match.")
	default:
		// The chosen key's row follows the app's choice, as after a create.
		st.sel = -1
		if rt.Sel != nil {
			for i, k := range rt.Keys {
				if k.Key.ID() == rt.Sel.Key.ID() {
					st.sel = i
					break
				}
			}
		}
		st.keys.Selected = &st.sel
		l := ui.List(c, &st.keys, len(rt.Keys), func(i int) {
			s.redisKeyRow(c, rt.Keys[i], i == st.sel)
		}).Grow(1).FillWidth()
		if l.Changed() && st.sel >= 0 && st.sel < len(rt.Keys) {
			s.core.RedisSelect(id, rt.Keys[st.sel])
		}
	}

	ui.Divider(c)
	ui.Row(c).Height(30).FillWidth().AlignItems(ui.Center).Padding(0, 10).Gap(8).Shrink(0).Children(func() {
		status := strconv.Itoa(len(rt.Keys)) + " keys"
		if rt.Total >= 0 && rt.Pattern == "" {
			status = strconv.Itoa(len(rt.Keys)) + " of " + strconv.FormatInt(rt.Total, 10) + " keys"
		}
		ui.Text(c, status).FontSize(11).TextColor(t.TextMuted).SingleLine()
		ui.Spacer(c)
		switch {
		case rt.Scanning:
			ui.Spinner(c).FontSize(12).Label("Scanning")
		case rt.HasMore():
			if ui.Button(c.Key("rmore-"+id), "Load more").Height(22).Clicked() {
				s.core.RedisMore(id)
			}
		}
	})
}

func (s *App) redisKeyRow(c *ui.Context, k redis.KeyInfo, chosen bool) {
	_, muted := ink(c.Theme(), chosen)
	ui.Row(c).FillWidth().Height(26).AlignItems(ui.Center).Gap(8).Padding(0, 10).Children(func() {
		s.tableFont(ui.Text(c, k.Key.Text), -0.5).SingleLine().Grow(1).Tooltip(k.Key.Text)
		if k.TTL >= 0 {
			ui.Text(c, redis.FormatTTL(k.TTL)).FontSize(10.5).TextColor(muted).SingleLine().Shrink(0)
		}
		redisBadge(c, k.Type)
	})
}

func (s *App) redisValue(c *ui.Context, id string, rt *app.RedisTab, st *redisUI) {
	t := c.Theme()
	if rt.Sel == nil {
		s.resultsNote(c, "Select a key")
		return
	}
	v := &rt.Value
	typ := rt.Sel.Type
	if v.V != nil {
		typ = string(v.V.Kind)
		if v.V.Kind == redis.KindUnsupported {
			typ = v.V.RedisType
		}
	}
	ui.Row(c).Height(headerH).FillWidth().AlignItems(ui.Center).Padding(0, 10).Gap(6).Shrink(0).Children(func() {
		redisBadge(c, typ)
		s.tableFont(ui.Text(c, rt.Sel.Key.Text), -0.5).SingleLine().Shrink(1).Tooltip(rt.Sel.Key.Text)
		if iconButton(c, icons.Copy, "Copy key").Clicked() {
			c.WriteClipboard(rt.Sel.Key.Text)
			s.core.Toast("Key copied")
		}
		ui.Spacer(c)
		if v.V != nil {
			s.redisTTL(c, id, v)
		}
		if iconButton(c, icons.Refresh, "Reload value").Clicked() {
			s.core.RedisReload(id)
		}
		if rt.ConfirmDelete {
			if ui.Button(c.Key("rdel-yes"), "Delete key").Disabled(v.Busy).Height(24).Clicked() {
				s.core.RedisDeleteSelected(id)
			}
			if ui.Button(c.Key("rdel-no"), "Cancel").Height(24).Clicked() {
				rt.ConfirmDelete = false
			}
		} else if iconButton(c, icons.Trash, "Delete key").Clicked() {
			rt.ConfirmDelete = true
		}
	})
	ui.Divider(c)

	switch {
	case v.Missing:
		s.resultsNote(c, "This key no longer exists. It may have expired or been deleted elsewhere.")
	case v.V == nil:
		s.resultsNote(c, "Reading…")
	case v.V.Kind == redis.KindString:
		s.redisString(c, id, v)
	case v.V.Kind == redis.KindHash:
		s.redisHash(c, id, v, st)
	default:
		s.resultsNote(c, v.V.RedisType+" values are not shown here yet. The console below can read and change them.")
	}
	_ = t
}

// redisTTL is the key's expiry: a countdown that runs from the last read
// without asking the server, and the ways to change it.
func (s *App) redisTTL(c *ui.Context, id string, v *app.RedisValueState) {
	t := c.Theme()
	left := v.Remaining(time.Now())
	if v.V.TTL > 0 && left > 0 {
		c.After(time.Second)
	}
	if !v.TTLEditing {
		b := ui.ButtonBase(c.Key("rttl")).Height(24).Padding(0, 8).Radius(6).Gap(5).AlignItems(ui.Center).
			Disabled(v.Busy).Tooltip("Change TTL").Label("Change TTL")
		if b.Hovered() {
			b.Background(t.SurfaceHover)
		}
		b.Children(func() {
			ui.Icon(c, icons.Clock).FontSize(13).TextColor(t.TextMuted)
			ui.Text(c, redis.FormatTTL(left)).FontSize(11.5).TextColor(t.TextMuted).SingleLine()
		})
		if b.Clicked() {
			v.TTLEditing, v.TTLText = true, ""
			if left > 0 {
				v.TTLText = strconv.FormatInt(left, 10)
			}
		}
		// A countdown that reached zero means the key is gone.
		if v.V.TTL > 0 && left == 0 {
			s.core.RedisReload(id)
		}
		return
	}
	in := ui.TextInput(c.Key("rttl-in"), &v.TTLText).Placeholder("Seconds").Label("TTL in seconds").Width(90).Height(24).Padding(0, 8)
	in.Focus()
	apply := func() {
		if n, err := strconv.ParseInt(strings.TrimSpace(v.TTLText), 10, 64); err == nil && n > 0 {
			s.core.RedisSetTTL(id, redis.TTLChange{Mode: redis.TTLSeconds, Seconds: n})
			v.TTLEditing = false
		}
	}
	if in.Submitted() {
		apply()
	}
	if iconButton(c, icons.Check, "Set TTL").Clicked() {
		apply()
	}
	if ui.Button(c.Key("rttl-persist"), "No expiry").Height(24).Tooltip("Remove the TTL").Clicked() {
		s.core.RedisSetTTL(id, redis.TTLChange{Mode: redis.TTLPersist})
		v.TTLEditing = false
	}
	if iconButton(c, icons.X, "Cancel").Clicked() {
		v.TTLEditing = false
	}
}

func (s *App) redisString(c *ui.Context, id string, v *app.RedisValueState) {
	t := c.Theme()
	str := v.V
	// A binary or clipped value shows read-only: writing back what the screen
	// holds would corrupt it.
	readOnly := str.Str.Binary || str.Truncated
	ui.Column(c).Grow(1).FillWidth().Padding(10).Gap(8).Children(func() {
		if readOnly {
			ui.Scroll(c).Grow(1).FillWidth().Children(func() {
				s.tableFont(ui.Text(c, str.Str.Text), 0).Selectable().Wrap().FillWidth()
			})
		} else {
			s.tableFont(ui.TextArea(c.Key("rstr"), &v.Draft).Grow(1).FillWidth().Label("Value"), 0)
		}
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Shrink(0).Children(func() {
			note := strconv.FormatInt(str.ByteLen, 10) + " bytes"
			switch {
			case str.Truncated:
				note += " · only the first " + strconv.Itoa(redis.StringReadCap/1024) + " KiB is shown, read-only"
			case str.Str.Binary:
				note += " · binary, read-only"
			}
			ui.Text(c, note).FontSize(11).TextColor(t.TextMuted).SingleLine()
			ui.Spacer(c)
			if readOnly {
				return
			}
			dirty := v.Draft != str.Str.Text
			if ui.Button(c.Key("rstr-revert"), "Revert").Disabled(!dirty || v.Busy).Clicked() {
				v.Draft = str.Str.Text
			}
			if ui.PrimaryButton(c.Key("rstr-save"), "Save").Disabled(!dirty || v.Busy).Clicked() {
				s.core.RedisSaveString(id)
			}
		})
	})
}

func (s *App) redisHash(c *ui.Context, id string, v *app.RedisValueState, st *redisUI) {
	t := c.Theme()
	ui.Row(c).Height(34).FillWidth().AlignItems(ui.Center).Padding(0, 10).Gap(8).Shrink(0).Children(func() {
		ui.Text(c, strconv.FormatInt(v.V.HashLen, 10)+" fields").FontSize(11).TextColor(t.TextMuted).SingleLine()
		ui.Spacer(c)
		if ui.Button(c.Key("rhash-add"), "Add field").Height(24).Disabled(v.Busy).Clicked() {
			v.AddField, v.EditField, v.FieldName, v.FieldValue = true, -1, "", ""
		}
	})
	ui.Divider(c)
	if v.AddField {
		s.redisFieldEditor(c, id, v, nil)
		ui.Divider(c)
	}
	ui.List(c, &st.hash, len(v.Hash), func(i int) {
		e := v.Hash[i]
		if v.EditField == i {
			s.redisFieldEditor(c, id, v, &e)
			return
		}
		s.redisFieldRow(c, id, v, i, e)
	}).Grow(1).FillWidth()
	if v.HashCursor != "0" && v.HashCursor != "" {
		ui.Divider(c)
		ui.Row(c).Height(30).FillWidth().AlignItems(ui.Center).Padding(0, 10).Shrink(0).Children(func() {
			ui.Text(c, strconv.Itoa(len(v.Hash))+" of "+strconv.FormatInt(v.V.HashLen, 10)).FontSize(11).TextColor(t.TextMuted)
			ui.Spacer(c)
			if v.HashLoading {
				ui.Spinner(c).FontSize(12).Label("Loading fields")
			} else if ui.Button(c.Key("rhash-more"), "Load more").Height(22).Clicked() {
				s.core.RedisMoreFields(id)
			}
		})
	}
}

func (s *App) redisFieldRow(c *ui.Context, id string, v *app.RedisValueState, i int, e redis.HashEntry) {
	t := c.Theme()
	ui.Row(c).FillWidth().Height(28).AlignItems(ui.Center).Gap(10).Padding(0, 10).Children(func() {
		s.tableFont(ui.Text(c, e.Field.Text), -0.5).SingleLine().Width(160).Shrink(0).Tooltip(e.Field.Text)
		s.tableFont(ui.Text(c, e.Value.Text), -0.5).SingleLine().Grow(1).TextColor(t.TextMuted).Tooltip(e.Value.Text)
		// A binary field or value cannot be typed back, so it only deletes.
		if !e.Field.Binary && !e.Value.Binary {
			if ui.ButtonBase(c.Key("rf-edit-"+strconv.Itoa(i))).Size(24, 24).Radius(6).Label("Edit field").Tooltip("Edit field").
				Disabled(v.Busy).Children(func() {
				ui.Icon(c, icons.Pencil).FontSize(13).TextColor(t.TextMuted)
			}).Clicked() {
				v.EditField, v.AddField, v.FieldName, v.FieldValue = i, false, e.Field.Text, e.Value.Text
			}
		}
		if ui.ButtonBase(c.Key("rf-del-"+strconv.Itoa(i))).Size(24, 24).Radius(6).Label("Delete field").Tooltip("Delete field").
			Disabled(v.Busy).Children(func() {
			ui.Icon(c, icons.Trash).FontSize(13).TextColor(t.TextMuted)
		}).Clicked() {
			s.core.RedisRemoveField(id, e.Field)
		}
	})
}

// redisFieldEditor edits a field in place, or adds one when e is nil.
// Changing the name renames the field, in one transaction with the write.
func (s *App) redisFieldEditor(c *ui.Context, id string, v *app.RedisValueState, e *redis.HashEntry) {
	ui.Row(c).FillWidth().Height(34).AlignItems(ui.Center).Gap(8).Padding(0, 10).Children(func() {
		name := ui.TextInput(c.Key("rf-name"), &v.FieldName).Placeholder("Field").Label("Field name").Width(160).Height(26).Padding(0, 8)
		val := ui.TextInput(c.Key("rf-value"), &v.FieldValue).Placeholder("Value").Label("Field value").Grow(1).Height(26).Padding(0, 8)
		if e == nil {
			name.Focus()
		}
		save := func() {
			if v.FieldName == "" {
				return
			}
			var prev *redis.Bytes
			if e != nil && e.Field.Text != v.FieldName {
				p := e.Field
				prev = &p
			}
			s.core.RedisSaveField(id, redis.TextBytes(v.FieldName), v.FieldValue, prev)
		}
		if val.Submitted() || name.Submitted() {
			save()
		}
		if iconButton(c, icons.Check, "Save field").Clicked() {
			save()
		}
		if iconButton(c, icons.X, "Cancel").Clicked() {
			v.EditField, v.AddField = -1, false
		}
	})
}

func (s *App) redisConsole(c *ui.Context, id string, rt *app.RedisTab) {
	t := c.Theme()
	con := &rt.Console
	ui.Row(c).Height(headerH).FillWidth().AlignItems(ui.Center).Padding(0, 12).Gap(6).Shrink(0).Children(func() {
		ui.Text(c, "CONSOLE").FontSize(11).FontWeight(600).TextColor(t.TextMuted).SingleLine()
		ui.Text(c, "db"+strconv.Itoa(rt.DB)).FontSize(11).TextColor(t.TextMuted).SingleLine()
		ui.Spacer(c)
		if ui.Button(c.Key("rcon-clear"), "Clear").Height(22).Disabled(len(con.Entries) == 0).Clicked() {
			s.core.ClearRedisConsole(id)
		}
	})
	ui.Divider(c)
	ui.Scroll(c.Key("rcon-out")).Grow(1).FillWidth().Children(func() {
		ui.Column(c).FillWidth().Padding(8, 12).Gap(8).Children(func() {
			if len(con.Entries) == 0 {
				ui.Text(c, "Type a command, e.g. GET key. Commands that may change data ask first.").FontSize(11.5).TextColor(t.TextMuted)
			}
			for i, e := range con.Entries {
				el := s.redisEntry(c, id, e)
				// A new answer brings the end of the output into view.
				if con.Scroll && i == len(con.Entries)-1 {
					el.ScrollIntoView()
				}
			}
			con.Scroll = false
		})
	})
	ui.Divider(c)
	ui.Row(c).FillWidth().Height(40).AlignItems(ui.Center).Padding(0, 10).Gap(8).Shrink(0).Children(func() {
		ui.Text(c, ">").FontSize(12).TextColor(t.TextMuted)
		in := s.tableFont(ui.TextInput(c.Key("rcon-in"), &con.Input).Placeholder("GET key").Label("Redis command").Grow(1).Height(28).Padding(0, 8), 0)
		if in.Submitted() {
			s.core.RedisRun(id)
			in.Focus()
		}
		if in.Focused() {
			if c.Shortcut(0, ui.KeyUp) {
				s.core.RedisHistory(id, -1)
			}
			if c.Shortcut(0, ui.KeyDown) {
				s.core.RedisHistory(id, 1)
			}
		}
		if ui.Button(c.Key("rcon-run"), "Run").Height(28).Disabled(strings.TrimSpace(con.Input) == "").Clicked() {
			s.core.RedisRun(id)
		}
	})
}

func (s *App) redisEntry(c *ui.Context, id string, e app.RedisEntry) ui.Element {
	t := c.Theme()
	out := t.Text
	if e.State == "error" {
		out = t.Danger
	}
	return ui.Column(c).FillWidth().Gap(3).Children(func() {
		s.tableFont(ui.Text(c, "> "+e.Command), -0.5).TextColor(t.TextMuted).Selectable().Wrap().FillWidth()
		switch e.State {
		case "running":
			ui.Spinner(c).FontSize(12).Label("Running")
		case "confirm":
			ui.Text(c, e.Output).FontSize(11.5).TextColor(t.TextMuted).Wrap()
			ui.Row(c).Gap(6).Children(func() {
				if ui.Button(c.Key("rcon-anyway-"+strconv.Itoa(e.ID)), "Run anyway").Height(24).Clicked() {
					s.core.RedisConfirm(id, e.ID)
				}
			})
		default:
			s.tableFont(ui.Text(c, e.Output), -0.5).TextColor(out).Selectable().Wrap().FillWidth()
			if e.Elapsed > 0 {
				ui.Text(c, e.Elapsed.String()).FontSize(10).TextColor(t.TextMuted)
			}
		}
	})
}

// redisNewKey is the form that creates a key. It never replaces one.
func (s *App) redisNewKey(c *ui.Context, id string, rt *app.RedisTab) {
	n := &rt.New
	open := n.Open
	ui.Modal(c, &open, func() {
		t := c.Theme()
		ui.Column(c).Width(460).Gap(12).Children(func() {
			ui.Text(c, "New key").FontSize(16).Bold()
			ui.Field(c, "Key", func() {
				k := ui.TextInput(c.Key("rnk-key"), &n.Key).Placeholder("user:42")
				if n.Key == "" {
					k.Focus()
				}
			})
			ui.Field(c, "Type", func() {
				ui.Select(c, &n.Type, app.RedisKeyTypes).FillWidth().Label("Type")
			})
			if n.Type == "hash" {
				ui.Field(c, "First field", func() { ui.TextInput(c.Key("rnk-field"), &n.Field) })
			}
			ui.Field(c, "Value", func() {
				ui.TextArea(c.Key("rnk-value"), &n.Value).Height(90)
			})
			ui.Field(c, "TTL", func() {
				ui.TextInput(c.Key("rnk-ttl"), &n.TTL).Placeholder("Seconds; blank for no expiry")
			})
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
				if n.Err != "" {
					ui.Text(c, n.Err).FontSize(12).TextColor(t.Danger).SingleLine()
				}
				ui.Spacer(c)
				if ui.Button(c.Key("rnk-cancel"), "Cancel").Clicked() {
					s.core.CloseRedisNewKey(id)
				}
				if ui.PrimaryButton(c.Key("rnk-create"), "Create").Disabled(n.Busy).Clicked() {
					s.core.RedisCreate(id)
				}
			})
		})
	})
	if !open && n.Open {
		s.core.CloseRedisNewKey(id)
	}
}
