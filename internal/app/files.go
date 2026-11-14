package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"

	"seeker/internal/driver"
	"seeker/internal/model"
)

const (
	// autosaveDelay is how long a file tab holds still before it is
	// written (recall: 1.5 s).
	autosaveDelay = 1500 * time.Millisecond
	// maxBindings is how many file→datasource bindings are remembered;
	// the oldest go first.
	maxBindings = 200

	settingBindings = "fileBindings"
)

// FileVersion is the token a write is guarded with: the sha256 of the bytes
// as last read or written. Nothing interprets it but equality.
func FileVersion(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// writeResult is a write's outcome: refusals are data, not errors, because
// each one is something the user has to hear about and the tab stays dirty
// either way.
type writeResult int

const (
	written writeResult = iota
	conflict
	missing
)

// writeGuarded writes content only when the file on disk is still what the
// caller last saw. expected nil means "create, and refuse if anything is
// there". It reports the version on disk when it refuses.
func writeGuarded(path, content string, expected *string) (writeResult, string, error) {
	current, exists, err := diskVersion(path)
	if err != nil {
		return 0, "", err
	}
	switch {
	case expected == nil && exists:
		return conflict, current, nil
	case expected != nil && !exists:
		return missing, "", nil
	case expected != nil && current != *expected:
		return conflict, current, nil
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return 0, "", err
	}
	return written, FileVersion(content), nil
}

func diskVersion(path string) (version string, exists bool, err error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return FileVersion(string(b)), true, nil
}

// ReadFile reads a .sql file and remembers its version, which the guarded
// writes after it compare against.
func (a *App) ReadFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	a.fileVer[path] = FileVersion(string(b))
	return string(b), nil
}

// expectedVersion is the token a tab's next write is guarded by: what was
// last read or written, else the version of the text the tab was last
// saved with. A tab restored from the store has read nothing this session,
// and its snapshot is what it believes is on disk.
func (a *App) expectedVersion(t Tab) string {
	if v, ok := a.fileVer[t.Path]; ok {
		return v
	}
	return FileVersion(t.SavedSQL)
}

// scheduleSave arms the tab's autosave, restarting its delay. A table tab
// has no file, a scratch tab has no path, and a tab whose last write was
// refused stays off until the user saves it by hand: the file changed under
// us, and the next pause must not write over it.
func (a *App) scheduleSave(id string) {
	t, ok := a.TabByID(id)
	if !ok || t.Path == "" || t.Mode != ModeQuery || a.saveBlocked[id] || a.upd == nil {
		return
	}
	if old := a.autosave[id]; old != nil {
		old.Stop()
	}
	upd := a.upd
	a.autosave[id] = time.AfterFunc(autosaveDelay, func() {
		upd(func() {
			delete(a.autosave, id)
			a.saveFile(id, false)
		})
	})
}

func (a *App) cancelAutosave(id string) {
	if t := a.autosave[id]; t != nil {
		t.Stop()
		delete(a.autosave, id)
	}
}

// SaveTab is Cmd+S: it writes a file tab now, and asks where a scratch tab
// should live.
func (a *App) SaveTab(id string) {
	t, ok := a.TabByID(id)
	if !ok || t.Mode != ModeQuery {
		return
	}
	a.cancelAutosave(id)
	delete(a.saveBlocked, id)
	if t.Path == "" {
		a.SaveAs(id)
		return
	}
	a.saveFile(id, true)
}

// saveFile writes a file tab's text, off the window's thread. Only the text
// that was written becomes the tab's saved snapshot: what was typed since
// stays dirty.
func (a *App) saveFile(id string, explicit bool) {
	t, ok := a.TabByID(id)
	if !ok || t.Path == "" || !a.TabDirty(t) && !explicit {
		return
	}
	path, content, expected := t.Path, t.SQL, a.expectedVersion(t)
	go func() {
		res, disk, err := writeGuarded(path, content, &expected)
		a.upd(func() { a.finishSave(id, path, content, res, disk, err) })
	}()
}

func (a *App) finishSave(id, path, content string, res writeResult, disk string, err error) {
	i := a.indexOf(id)
	if i < 0 || a.tabs.Items[i].Path != path {
		return
	}
	switch {
	case err != nil:
		a.saveBlocked[id] = true
		a.toast("Could not save " + filepath.Base(path) + ": " + err.Error())
	case res == written:
		delete(a.saveBlocked, id)
		a.fileVer[path] = FileVersion(content)
		a.tabs.Items[i].SavedSQL = content
	case res == missing:
		a.saveBlocked[id] = true
		a.toast("The file is no longer on disk: it was deleted or moved.")
	default:
		a.saveBlocked[id] = true
		a.toastAction("That file changed on disk: your edits were not written.", "Overwrite", func() {
			// The file on disk is the one the user is looking at now; its
			// version is the token that lets this one write.
			a.fileVer[path] = disk
			delete(a.saveBlocked, id)
			a.saveFile(id, true)
		})
	}
}

// pickSave asks where to save; "" is cancel. The native panel waits for the
// user, so it runs off the window's thread.
var pickSave = func(name string) (string, error) {
	return mygo.Dialog.Save(mygo.SaveDialogOptions{
		Title:             "Save SQL file",
		DefaultPath:       name,
		ButtonLabel:       "Save",
		CreateDirectories: true,
		Filters:           []mygo.FileFilter{{Name: "SQL", Extensions: []string{"sql"}}},
	})
}

// SaveAs asks where the tab's text should live, then adopts that file: the
// tab takes its path and name and is saved. The panel asks before replacing
// a file, so the write here is a plain one.
func (a *App) SaveAs(id string) {
	t, ok := a.TabByID(id)
	if !ok || t.Mode != ModeQuery {
		return
	}
	name := t.Title
	if !strings.HasSuffix(name, ".sql") {
		name += ".sql"
	}
	content := t.SQL
	go func() {
		path, err := pickSave(name)
		if err == nil && path != "" {
			if !strings.HasSuffix(strings.ToLower(path), ".sql") {
				path += ".sql"
			}
			err = os.WriteFile(path, []byte(content), 0o644)
		}
		a.upd(func() {
			if err != nil {
				a.toast("Could not save: " + err.Error())
				return
			}
			if path != "" {
				a.adoptFile(id, path, content)
			}
		})
	}()
}

// adoptFile makes path the file of tab id, whose text content is on disk.
func (a *App) adoptFile(id, path, content string) {
	i := a.indexOf(id)
	if i < 0 {
		return
	}
	a.cancelAutosave(id)
	delete(a.saveBlocked, id)
	t := &a.tabs.Items[i]
	t.Path, t.Title, t.SavedSQL = path, filepath.Base(path), content
	a.fileVer[path] = FileVersion(content)
	a.SetFileBinding(path, model.Datasource{Conn: t.Conn, Database: t.Database, Schema: t.Schema})
}

// Binding is a file's datasource.
type binding struct {
	Path string `json:"path"`
	model.Datasource
}

// BindingFor is the datasource a file was last bound to.
func (a *App) BindingFor(path string) (model.Datasource, bool) {
	for _, b := range a.bindings {
		if b.Path == path {
			return b.Datasource, true
		}
	}
	return model.Datasource{}, false
}

// SetFileBinding binds a file to a datasource, evicting the oldest past the
// cap. Bindings outlive tabs: a file reopens on what it last ran against.
func (a *App) SetFileBinding(path string, ds model.Datasource) {
	if path == "" {
		return
	}
	a.bindings = slices.DeleteFunc(a.bindings, func(b binding) bool { return b.Path == path })
	a.bindings = slices.Insert(a.bindings, 0, binding{path, ds})
	if len(a.bindings) > maxBindings {
		a.bindings = a.bindings[:maxBindings]
	}
	saved := slices.Clone(a.bindings)
	a.saves.later(settingBindings, func() {
		if err := a.db.SetSetting(a.ctx, settingBindings, saved); err != nil {
			a.upd(func() { a.toast("Could not save the file bindings: " + err.Error()) })
		}
	})
}

// ResolveDatasource is where a file opens: its own binding when the
// connection still exists, else the connection the user is on, else the
// first SQL one; false when there is none, the one case a file cannot open.
func (a *App) ResolveDatasource(path string) (model.Datasource, bool) {
	if ds, ok := a.BindingFor(path); ok {
		if p, ok := a.Profile(ds.Conn); ok && p.DBType.IsSQL() {
			return ds, true
		}
	}
	p, ok := a.queryProfile()
	if !ok {
		return model.Datasource{}, false
	}
	return datasourceOf(p, p.Database), true
}

// datasourceOf is the datasource of profile p in database; MySQL's schema
// is its database, Postgres uses the profile's default (public).
func datasourceOf(p model.Profile, database string) model.Datasource {
	schema := p.DefaultSchema
	switch {
	case p.DBType == model.MySQL:
		schema = database
	case schema == "" || database != p.Database:
		schema = "public"
	}
	return model.Datasource{Conn: p.ID, Database: database, Schema: schema}
}

// OpenFile opens a .sql file in a tab on its datasource.
func (a *App) OpenFile(path string) error {
	content, err := a.ReadFile(path)
	if err != nil {
		return err
	}
	ds, ok := a.ResolveDatasource(path)
	if !ok {
		a.toast("Add a connection first")
		return nil
	}
	a.OpenQueryTab(QueryTab{Conn: ds.Conn, Database: ds.Database, Schema: ds.Schema, Path: path, Title: filepath.Base(path), SQL: content})
	return nil
}

// ChooseDatasource points a tab, and its file, at a connection and database.
func (a *App) ChooseDatasource(id, conn, database string) {
	i := a.indexOf(id)
	p, ok := a.Profile(conn)
	if i < 0 || !ok || !p.DBType.IsSQL() {
		return
	}
	ds := datasourceOf(p, database)
	t := &a.tabs.Items[i]
	t.Conn, t.Database, t.Schema = ds.Conn, ds.Database, ds.Schema
	a.warmed = warmStamp{}
	a.ensureTabConnected(*t)
	if t.Path != "" {
		a.SetFileBinding(t.Path, ds)
	}
}

// Databases lists the databases a connection offers the datasource picker:
// the server's list once it loads (asked the first time the menu opens, and
// kept for the session), until then the profile's own.
func (a *App) Databases(conn string) []string {
	p, ok := a.Profile(conn)
	if !ok {
		return nil
	}
	list, loaded := a.dbLists[conn]
	if !loaded && a.dbLoading[conn] == false && a.upd != nil {
		a.dbLoading[conn] = true
		go a.loadDatabases(p)
	}
	if len(list) == 0 && p.Database != "" {
		return []string{p.Database}
	}
	return list
}

func (a *App) loadDatabases(p model.Profile) {
	err := waitConnected(a.conn(p.ID))
	var dbs []driver.DatabaseInfo
	if err == nil {
		err = a.pl.Catalog(a.ctx, p.ID, p.Database, func(ctx context.Context, conn driver.Conn) error {
			var e error
			dbs, e = conn.ListDatabases(ctx, "")
			return e
		})
	}
	a.upd(func() {
		delete(a.dbLoading, p.ID)
		if err != nil {
			return
		}
		a.dbLists[p.ID] = databaseNames(p, dbs)
	})
}

// databaseNames keeps the profile's own database and drops hidden ones.
func databaseNames(p model.Profile, dbs []driver.DatabaseInfo) []string {
	var out []string
	for _, db := range dbs {
		if db.Name != p.Database && slices.Contains(p.HiddenDatabases, db.Name) {
			continue
		}
		out = append(out, db.Name)
	}
	return out
}

// flushAutosaves writes the files whose delay has not run out, on quit.
func (a *App) flushAutosaves() {
	for id, timer := range a.autosave {
		timer.Stop()
		t, ok := a.TabByID(id)
		if !ok || t.Path == "" || !a.TabDirty(t) {
			continue
		}
		expected := a.expectedVersion(t)
		// A refusal stays unsaved: the edits are in the tab, which the store keeps.
		writeGuarded(t.Path, t.SQL, &expected)
	}
	clear(a.autosave)
}
