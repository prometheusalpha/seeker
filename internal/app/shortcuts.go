package app

import (
	"errors"
	"runtime"
	"strings"

	"github.com/egoist/mygo/ui"

	"seeker/internal/store"
)

const settingShortcuts = "shortcuts"

// Command is something a chord runs. The ids are recall's (useShortcuts.ts)
// plus the three seeker gave a chord of their own: reopening a tab, saving
// and copying.
type Command string

const (
	TabClose       Command = "tab.close"
	TabCloseOthers Command = "tab.closeOthers"
	TabCloseAll    Command = "tab.closeAll"
	TabReopen      Command = "tab.reopen"
	TabNext        Command = "tab.next"
	TabPrev        Command = "tab.prev"
	TabNextAlt     Command = "tab.nextAlt"
	TabPrevAlt     Command = "tab.prevAlt"
	QuickOpenKey   Command = "quickOpen"
	FindInFilesKey Command = "findInFiles"
	SettingsOpen   Command = "settings.open"
	EditorFormat   Command = "editor.formatSql"
	EditorExplain  Command = "editor.explainPlan"
	EditorSave     Command = "editor.save"
	SortAsc        Command = "result.sortAsc"
	SortDesc       Command = "result.sortDesc"
	ResultRerun    Command = "result.rerun"
	ResultCopy     Command = "result.copy"
	FocusWhere     Command = "result.focusWhere"
)

// Chord is a key and the modifiers held with it. The zero Chord is
// unbound. The key is the physical one, as the editor reads it.
type Chord struct {
	Mods ui.Modifiers
	Key  ui.Key
}

// Bound reports whether the chord names a key.
func (ch Chord) Bound() bool { return ch.Key != ui.KeyUnknown }

// Matches reports whether a key press is the chord.
func (ch Chord) Matches(mods ui.Modifiers, key ui.Key) bool {
	return ch.Bound() && ch.Mods == mods && ch.Key == key
}

// CommandDef is one row of the table: what the settings list and what a
// fresh install binds.
type CommandDef struct {
	ID      Command
	Label   string
	Group   string
	Default Chord
}

// Commands is the table, in the order the settings show it.
var Commands = []CommandDef{
	{TabClose, "Close tab", "Tabs", Chord{ui.Cmd, ui.KeyW}},
	{TabCloseOthers, "Close other tabs", "Tabs", Chord{ui.Cmd | ui.Shift, ui.KeyW}},
	{TabCloseAll, "Close all tabs", "Tabs", Chord{ui.Cmd | ui.Alt, ui.KeyW}},
	{TabReopen, "Reopen closed tab", "Tabs", Chord{ui.Cmd | ui.Shift, ui.KeyT}},
	{TabNext, "Next tab", "Tabs", Chord{ui.Cmd | ui.Alt, ui.KeyRight}},
	{TabPrev, "Previous tab", "Tabs", Chord{ui.Cmd | ui.Alt, ui.KeyLeft}},
	// Real Control on every platform: ⌘Tab is the app switcher's.
	{TabNextAlt, "Next tab (alt)", "Tabs", Chord{ui.Ctrl, ui.KeyTab}},
	{TabPrevAlt, "Previous tab (alt)", "Tabs", Chord{ui.Ctrl | ui.Shift, ui.KeyTab}},
	{QuickOpenKey, "Quick open", "View", Chord{ui.Cmd, ui.KeyP}},
	{FindInFilesKey, "Find in files", "View", Chord{ui.Cmd | ui.Shift, ui.KeyF}},
	{SettingsOpen, "Open settings", "View", Chord{ui.Cmd, ui.KeyComma}},
	{EditorFormat, "Format SQL", "Editor", Chord{ui.Alt | ui.Shift, ui.KeyF}},
	{EditorExplain, "Explain statement", "Editor", Chord{ui.Alt | ui.Shift, ui.KeyE}},
	{EditorSave, "Save file / write edits", "Editor", Chord{ui.Cmd, ui.KeyS}},
	{SortAsc, "Sort column ascending", "Result grid", Chord{ui.Ctrl | ui.Shift, ui.KeyUp}},
	{SortDesc, "Sort column descending", "Result grid", Chord{ui.Ctrl | ui.Shift, ui.KeyDown}},
	{ResultRerun, "Refresh result", "Result grid", Chord{ui.Cmd, ui.KeyR}},
	{ResultCopy, "Copy selection", "Result grid", Chord{ui.Cmd, ui.KeyC}},
	{FocusWhere, "Focus WHERE filter", "Result grid", Chord{ui.Cmd, ui.KeyL}},
}

func defOf(id Command) (CommandDef, bool) {
	for _, d := range Commands {
		if d.ID == id {
			return d, true
		}
	}
	return CommandDef{}, false
}

// Shortcut is the chord a command has now: the user's, else the default.
func (a *App) Shortcut(id Command) Chord {
	if ch, ok := a.chords[id]; ok {
		return ch
	}
	d, _ := defOf(id)
	return d.Default
}

// ShortcutIs reports whether a key press runs the command. It is false
// while the settings record a chord, so the chord being set cannot also run
// what it is bound to.
func (a *App) ShortcutIs(id Command, mods ui.Modifiers, key ui.Key) bool {
	return !a.recording && a.Shortcut(id).Matches(mods, key)
}

// OnShortcutsChange has fn run when a chord is rebound, reset, or the recorder
// starts or stops: the native menu holds some chords, and is built again.
func (a *App) OnShortcutsChange(fn func()) { a.chordsChanged = fn }

func (a *App) notifyChords() {
	if a.chordsChanged != nil {
		a.chordsChanged()
	}
}

// Recording reports whether the chords are paused for the recorder.
func (a *App) Recording() bool { return a.recording }

// RecordShortcuts pauses (or resumes) every chord, for the settings'
// recorder.
func (a *App) RecordShortcuts(on bool) {
	if a.recording != on {
		a.recording = on
		a.notifyChords()
	}
}

// SetShortcut binds a chord to a command. A chord belongs to one command: the
// one that held it becomes unbound, and the toast says so. (Recall puts it
// back to its default, which may collide again.)
func (a *App) SetShortcut(id Command, ch Chord) {
	if _, ok := defOf(id); !ok {
		return
	}
	for _, d := range Commands {
		if d.ID != id && ch.Bound() && a.Shortcut(d.ID) == ch {
			a.setChord(d.ID, Chord{})
			a.toast(d.Label + " is unbound: " + ChordText(ch) + " now runs " + labelOf(id))
		}
	}
	a.setChord(id, ch)
	a.saveShortcuts()
	a.notifyChords()
}

// ResetShortcut puts a command back on its default; a chord that default
// shares with another command's override stays that command's.
func (a *App) ResetShortcut(id Command) {
	d, ok := defOf(id)
	if !ok {
		return
	}
	delete(a.chords, id)
	for _, o := range Commands {
		if o.ID != id && d.Default.Bound() && a.Shortcut(o.ID) == d.Default {
			a.setChord(id, Chord{})
			break
		}
	}
	a.saveShortcuts()
	a.notifyChords()
}

// ResetShortcuts drops every override.
func (a *App) ResetShortcuts() {
	a.chords = nil
	a.saveShortcuts()
	a.notifyChords()
}

// ShortcutChanged reports whether a command is off its default.
func (a *App) ShortcutChanged(id Command) bool {
	_, ok := a.chords[id]
	return ok
}

func (a *App) setChord(id Command, ch Chord) {
	if d, _ := defOf(id); d.Default == ch {
		delete(a.chords, id)
		return
	}
	if a.chords == nil {
		a.chords = map[Command]Chord{}
	}
	a.chords[id] = ch
}

func labelOf(id Command) string {
	d, _ := defOf(id)
	return d.Label
}

// storedChord is a chord in the store: the modifiers as bits and the key by
// name. The blob is user-writable, so each entry is checked on the way in
// and a bad one falls back to its default.
type storedChord struct {
	Mods uint8  `json:"mods"`
	Key  string `json:"key"`
}

const allMods = ui.Shift | ui.Ctrl | ui.Alt | ui.Super

func (a *App) loadShortcuts() {
	var saved map[string]storedChord
	err := a.db.Setting(a.ctx, settingShortcuts, &saved)
	if errors.Is(err, store.ErrNoSetting) {
		return
	}
	if err != nil {
		a.toast("Could not read the shortcuts: " + err.Error())
		return
	}
	for _, d := range Commands {
		s, ok := saved[string(d.ID)]
		if !ok || ui.Modifiers(s.Mods)&^allMods != 0 {
			continue
		}
		if s.Key == "" {
			a.setChord(d.ID, Chord{})
		} else if key, ok := keyOfName(s.Key); ok {
			a.setChord(d.ID, Chord{ui.Modifiers(s.Mods), key})
		}
	}
}

func (a *App) saveShortcuts() {
	saved := map[string]storedChord{}
	for id, ch := range a.chords {
		saved[string(id)] = storedChord{uint8(ch.Mods), nameOfKey(ch.Key)}
	}
	a.saves.later(settingShortcuts, func() {
		if err := a.db.SetSetting(a.ctx, settingShortcuts, saved); err != nil {
			a.upd(func() { a.toast("Could not save the shortcuts: " + err.Error()) })
		}
	})
}

// keyNames are the keys a chord can use, by the names the store keeps.
var keyNames = map[ui.Key]string{
	ui.KeyEnter: "Enter", ui.KeyTab: "Tab", ui.KeySpace: "Space", ui.KeyBackspace: "Backspace",
	ui.KeyDelete: "Delete", ui.KeyHome: "Home", ui.KeyEnd: "End", ui.KeyPageUp: "PageUp",
	ui.KeyPageDown: "PageDown", ui.KeyLeft: "Left", ui.KeyRight: "Right", ui.KeyUp: "Up", ui.KeyDown: "Down",
	ui.KeyF1: "F1", ui.KeyF2: "F2", ui.KeyF3: "F3", ui.KeyF4: "F4", ui.KeyF5: "F5", ui.KeyF6: "F6",
	ui.KeyF7: "F7", ui.KeyF8: "F8", ui.KeyF9: "F9", ui.KeyF10: "F10", ui.KeyF11: "F11", ui.KeyF12: "F12",
	ui.KeyMinus: "-", ui.KeyEqual: "=", ui.KeyComma: ",", ui.KeyPeriod: ".", ui.KeySlash: "/",
	ui.KeySemicolon: ";", ui.KeyQuote: "'", ui.KeyBracketLeft: "[", ui.KeyBracketRight: "]",
	ui.KeyBackslash: "\\", ui.KeyBackquote: "`",
}

func nameOfKey(k ui.Key) string {
	if c, ok := Mnemonic(k); ok {
		return strings.ToUpper(string(c))
	}
	return keyNames[k]
}

func keyOfName(name string) (ui.Key, bool) {
	if len(name) == 1 {
		if k, ok := KeyOfMnemonic(strings.ToLower(name)); ok {
			return k, true
		}
	}
	for k, n := range keyNames {
		if n == name {
			return k, true
		}
	}
	return ui.KeyUnknown, false
}

// Recordable reports whether a press can become a chord: it needs a key
// that is not a modifier, and a modifier unless it is a function key, as a
// bare letter would be taken from the editor.
func Recordable(mods ui.Modifiers, key ui.Key) bool {
	if nameOfKey(key) == "" {
		return false
	}
	if mods != 0 {
		return true
	}
	return key >= ui.KeyF1 && key <= ui.KeyF12
}

// ChordText writes a chord as the platform does: ⌃⌥⇧⌘K on macOS, Ctrl+Alt+Shift+K
// elsewhere. Unbound is a dash.
func ChordText(ch Chord) string {
	if !ch.Bound() {
		return "—"
	}
	key := nameOfKey(ch.Key)
	if runtime.GOOS != "darwin" {
		var parts []string
		for _, m := range []struct {
			m ui.Modifiers
			s string
		}{{ui.Ctrl, "Ctrl"}, {ui.Alt, "Alt"}, {ui.Shift, "Shift"}, {ui.Super, "Win"}} {
			if ch.Mods&m.m != 0 {
				parts = append(parts, m.s)
			}
		}
		return strings.Join(append(parts, key), "+")
	}
	arrows := map[string]string{"Left": "←", "Right": "→", "Up": "↑", "Down": "↓", "Tab": "⇥", "Enter": "↩", "Space": "␣"}
	if s, ok := arrows[key]; ok {
		key = s
	}
	var b strings.Builder
	for _, m := range []struct {
		m ui.Modifiers
		s string
	}{{ui.Ctrl, "⌃"}, {ui.Alt, "⌥"}, {ui.Shift, "⇧"}, {ui.Super, "⌘"}} {
		if ch.Mods&m.m != 0 {
			b.WriteString(m.s)
		}
	}
	return b.String() + key
}

// Accelerator writes a chord as a native menu item takes it, "CmdOrCtrl+Shift+W";
// "" for unbound, or while the recorder waits for a key, so the menu does not
// take the one pressed.
func (a *App) Accelerator(id Command) string {
	ch := a.Shortcut(id)
	if !ch.Bound() || a.recording {
		return ""
	}
	var parts []string
	mods := ch.Mods
	if mods&ui.Cmd != 0 {
		parts = append(parts, "CmdOrCtrl")
		mods &^= ui.Cmd
	}
	for _, m := range []struct {
		m ui.Modifiers
		s string
	}{{ui.Super, "Super"}, {ui.Ctrl, "Ctrl"}, {ui.Alt, "Alt"}, {ui.Shift, "Shift"}} {
		if mods&m.m != 0 {
			parts = append(parts, m.s)
		}
	}
	return strings.Join(append(parts, nameOfKey(ch.Key)), "+")
}
