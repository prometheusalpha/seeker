// Package app owns seeker's state: the saved profiles and the sessions they
// open, the sidebar tree, the tabs and the dialogs. The view reads it and
// calls it; nothing here builds elements.
//
// Everything runs on the window's thread, except the work that talks to a
// database or the keychain: those goroutines publish their result through
// Update, and drop it when the generation or the attempt they started under
// has moved on.
package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/egoist/mygo/ui/editor"

	"seeker/internal/grid"
	"seeker/internal/model"
	"seeker/internal/pool"
	"seeker/internal/secret"
	"seeker/internal/store"
	"seeker/internal/theme"
)

// Keys of the settings table: recall kept these in localStorage, which the
// store cannot import, so a fresh install starts from the defaults.
const (
	settingTheme  = "theme"
	settingFont   = "font"
	settingLayout = "layout"
	settingTabs   = "tabs"
	// saveDelay is how long typing holds off a write; recall used 300 ms for
	// tabs.
	saveDelay = 300 * time.Millisecond
)

// Update runs fn on the window's thread and redraws it.
type Update func(fn func())

// UI is the settings that describe the window rather than the data.
type UI struct {
	Theme       theme.Settings
	Font        theme.Font
	SidebarW    float32
	EditorPct   float32
	HideSidebar bool
	// CharW is the advance of the table font as the view measured it, 0
	// before it has.
	CharW float64
}

type layout struct {
	SidebarW    float32 `json:"sidebarW"`
	EditorPct   float32 `json:"editorPct"`
	HideSidebar bool    `json:"hideSidebar"`
}

// CharWidth is the monospace advance of the grid's font in px: what the view
// measured, or until it has, the 8px the widths were tuned to at 12pt
// scaled by the size (a monospace advance scales with it, for the 0.6em
// families).
func (u UI) CharWidth() float64 {
	if u.CharW > 0 {
		return u.CharW
	}
	return grid.GridCharWidth * u.Font.Size / 12
}

func defaultUI() UI {
	return UI{Theme: theme.DefaultSettings(), Font: theme.DefaultFont(), SidebarW: 260, EditorPct: 48}
}

// Toast is a message the view shows once. Action, with Run, adds a button.
type Toast struct {
	Message string
	Action  string
	Run     func()
}

type App struct {
	ctx context.Context
	db  *store.Store
	pl  *pool.Pool
	upd Update

	profiles []model.Profile
	conns    map[string]*connState
	pins     []model.PinnedTable
	pinAt    map[model.PinnedTable]int

	tree Tree
	tabs Tabs
	dlg  Dialogs
	ui   UI

	savedUI     UI
	savedTabs   []Tab
	savedActive string
	saves       *saver

	toasts  []Toast
	testGen int // counts connection tests, across dialogs

	// The editors of query tabs, by tab id, and the theme they wear.
	editors   map[string]*tabEditor
	codeTheme *editor.Theme
	cat       *catalog
	warmed    warmStamp

	// File tabs: the version of each file as last read or written, the
	// autosaves waiting out their delay, the tabs whose last write was
	// refused, and the datasource each file is bound to, newest first.
	fileVer     map[string]string
	autosave    map[string]*time.Timer
	saveBlocked map[string]bool
	bindings    []binding
	dbLists     map[string][]string
	dbLoading   map[string]bool

	bookmarks []model.Bookmark
	jump      *jump

	// The rows of table tabs, by tab id.
	tables map[string]*TableView
}

// New opens the app's database at dbPath, imports recall's file at
// recallPath once, and reads the saved settings, profiles, pins and tabs.
// A failed import is a toast, not a failure to start.
func New(ctx context.Context, dbPath, recallPath string) (*App, error) {
	db, err := store.Open(ctx, dbPath)
	if err != nil {
		return nil, err
	}
	a := &App{
		ctx:   ctx,
		db:    db,
		pl:    pool.New(),
		conns: map[string]*connState{},
		pinAt: map[model.PinnedTable]int{},
		ui:    defaultUI(),
		saves: newSaver(),

		editors:     map[string]*tabEditor{},
		cat:         newCatalog(),
		fileVer:     map[string]string{},
		autosave:    map[string]*time.Timer{},
		saveBlocked: map[string]bool{},
		dbLists:     map[string][]string{},
		dbLoading:   map[string]bool{},
	}
	a.tabs.Active = -1
	a.tree.init()
	a.pl.OnLost = a.connectionLost

	if n, err := db.ImportRecall(ctx, recallPath); err != nil {
		a.toast("Could not import recall's connections: " + err.Error())
	} else if n > 0 {
		a.toast(fmt.Sprintf("Imported %d connections from Recall", n))
	}
	if a.profiles, err = db.Connections(ctx); err != nil {
		db.Close()
		return nil, err
	}
	if a.pins, err = db.PinnedTables(ctx); err != nil {
		db.Close()
		return nil, err
	}
	a.indexPins()
	if err := db.Setting(ctx, settingTheme, &a.ui.Theme); err != nil && !errors.Is(err, store.ErrNoSetting) {
		a.toast("Could not read the theme: " + err.Error())
	}
	if err := db.Setting(ctx, settingFont, &a.ui.Font); err != nil && !errors.Is(err, store.ErrNoSetting) {
		a.toast("Could not read the fonts: " + err.Error())
	}
	var l layout
	if err := db.Setting(ctx, settingLayout, &l); err != nil && !errors.Is(err, store.ErrNoSetting) {
		a.toast("Could not read the window layout: " + err.Error())
	} else if err == nil {
		a.ui.SidebarW, a.ui.EditorPct, a.ui.HideSidebar = l.SidebarW, l.EditorPct, l.HideSidebar
	}
	var saved savedTabs
	if err := db.Setting(ctx, settingTabs, &saved); err != nil && !errors.Is(err, store.ErrNoSetting) {
		a.toast("Could not read the tabs: " + err.Error())
	} else if err == nil {
		a.restoreTabs(saved)
	}
	if err := db.Setting(ctx, settingBindings, &a.bindings); err != nil && !errors.Is(err, store.ErrNoSetting) {
		a.toast("Could not read the file bindings: " + err.Error())
	}
	a.bindings = a.bindings[:min(len(a.bindings), maxBindings)]
	a.loadBookmarks()
	a.savedUI = a.ui
	a.snapshotTabs()
	return a, nil
}

// Attach is the window's Update, which publishes work done off its thread.
// The tab the store restored opens its connection now, so it is ready when
// the workspace needs it.
func (a *App) Attach(upd Update) {
	a.upd = upd
	if t, ok := a.ActiveTab(); ok {
		a.ensureTabConnected(t)
	}
}

// Close ends every session and closes the app's database, on quit.
func (a *App) Close() {
	if a.tree.filterTimer != nil {
		a.tree.filterTimer.Stop()
	}
	a.flushAutosaves()
	// Writes still waiting out their delay happen now, before the store
	// closes, and later ones are dropped.
	a.saves.flush()
	a.pl.CloseAll()
	a.db.Close()
}

// Toast queues a message for the view's next frame.
func (a *App) toast(msg string) { a.toasts = append(a.toasts, Toast{Message: msg}) }

// Toast queues a message from the view, which shows it next frame.
func (a *App) Toast(msg string) { a.toast(msg) }

func (a *App) toastAction(msg, action string, run func()) {
	a.toasts = append(a.toasts, Toast{Message: msg, Action: action, Run: run})
}

// TakeToasts hands the queued messages to the view, which shows them once.
func (a *App) TakeToasts() []Toast {
	t := a.toasts
	a.toasts = nil
	return t
}

// Sync saves what the view changed since the last frame. Values bound to a
// control change after the frame that built it, so the app compares rather
// than hooking every control.
func (a *App) Sync() {
	if a.ui != a.savedUI {
		a.savedUI = a.ui
		ui := a.ui
		a.saves.later(settingTheme, func() {
			if err := a.db.SetSetting(a.ctx, settingTheme, ui.Theme); err != nil {
				a.upd(func() { a.toast("Could not save the theme: " + err.Error()) })
			}
		})
		a.saves.later(settingFont, func() {
			if err := a.db.SetSetting(a.ctx, settingFont, ui.Font); err != nil {
				a.upd(func() { a.toast("Could not save the fonts: " + err.Error()) })
			}
		})
		l := layout{a.ui.SidebarW, a.ui.EditorPct, a.ui.HideSidebar}
		a.saves.later(settingLayout, func() {
			if err := a.db.SetSetting(a.ctx, settingLayout, l); err != nil {
				a.upd(func() { a.toast("Could not save the window layout: " + err.Error()) })
			}
		})
	}
	if active := a.tabs.activeID(); !slices.Equal(a.tabs.Items, a.savedTabs) || active != a.savedActive {
		a.snapshotTabs()
		saved := savedTabs{Tabs: slices.Clone(a.tabs.Items), ActiveTabID: active}
		a.saves.later(settingTabs, func() {
			if err := a.db.SetSetting(a.ctx, settingTabs, saved); err != nil {
				a.upd(func() { a.toast("Could not save the tabs: " + err.Error()) })
			}
		})
	}
}

func (a *App) snapshotTabs() {
	a.savedTabs = slices.Clone(a.tabs.Items)
	a.savedActive = a.tabs.activeID()
}

// UI returns the window settings, which the view binds and edits in place.
func (a *App) UI() *UI { return &a.ui }

// Profiles lists the saved connections in their order.
func (a *App) Profiles() []model.Profile { return a.profiles }

// Profile finds a saved connection.
func (a *App) Profile(id string) (model.Profile, bool) {
	i := slices.IndexFunc(a.profiles, func(p model.Profile) bool { return p.ID == id })
	if i < 0 {
		return model.Profile{}, false
	}
	return a.profiles[i], true
}

// SaveProfile adds or replaces a profile and starts connecting it, as
// recall's dialog does: the profile is saved even when the connect fails.
func (a *App) SaveProfile(p model.Profile, password string) error {
	return a.saveProfile(p, password, true)
}

// saveProfile is SaveProfile; writeSecret false leaves the keychain item as
// it is, for a form that never learned the saved password.
func (a *App) saveProfile(p model.Profile, password string, writeSecret bool) error {
	if p.Name == "" {
		p.Name = p.Host
	}
	i := slices.IndexFunc(a.profiles, func(x model.Profile) bool { return x.ID == p.ID })
	if i < 0 {
		a.profiles = append(a.profiles, p)
	} else {
		a.profiles[i] = p
	}
	if err := a.db.SaveConnections(a.ctx, a.profiles); err != nil {
		return err
	}
	c := a.conn(p.ID)
	c.setPassword(password)
	if writeSecret {
		go a.savePassword(p, password)
	}
	a.dropConn(p.ID)
	a.bump()
	a.Reconnect(p.ID)
	return nil
}

// savePassword writes or forgets the keychain item of p; the first read of
// one recall created may ask the user for permission, which the dialog
// cannot wait on.
func (a *App) savePassword(p model.Profile, password string) {
	var err error
	if p.SavePassword {
		err = secret.Save(p.ID, p.Username, password)
	} else {
		err = secret.Remove(p.ID, p.Username)
	}
	if err != nil {
		a.upd(func() { a.toast("Could not save the password: " + err.Error()) })
	}
}

// SetHiddenDatabases replaces the databases the sidebar leaves out, which
// the dialog writes through on every toggle.
func (a *App) SetHiddenDatabases(id string, hidden []string) error {
	i := slices.IndexFunc(a.profiles, func(p model.Profile) bool { return p.ID == id })
	if i < 0 {
		return nil
	}
	a.profiles[i].HiddenDatabases = hidden
	if err := a.db.SaveConnections(a.ctx, a.profiles); err != nil {
		return err
	}
	a.dropConn(id)
	a.bump()
	return nil
}

// The saver debounces writes per key, so a tab's SQL being typed writes once
// when typing pauses.
type saver struct {
	mu      sync.Mutex
	seq     map[string]int
	stop    map[string]*time.Timer
	pending map[string]func()
	closed  bool
}

func newSaver() *saver {
	return &saver{seq: map[string]int{}, stop: map[string]*time.Timer{}, pending: map[string]func(){}}
}

// flush runs the writes still waiting and refuses new ones.
func (s *saver) flush() {
	s.mu.Lock()
	s.closed = true
	var fns []func()
	for key, fn := range s.pending {
		fns = append(fns, fn)
		s.stop[key].Stop()
	}
	clear(s.pending)
	s.mu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

func (s *saver) later(key string, fn func()) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.pending[key] = fn
	s.seq[key]++
	seq := s.seq[key]
	if t := s.stop[key]; t != nil {
		t.Stop()
	}
	s.stop[key] = time.AfterFunc(saveDelay, func() {
		s.mu.Lock()
		if s.seq[key] != seq {
			s.mu.Unlock()
			return
		}
		delete(s.stop, key)
		delete(s.pending, key)
		s.mu.Unlock()
		fn()
	})
	s.mu.Unlock()
}
