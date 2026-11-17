package view

import (
	"github.com/egoist/mygo/ui"

	"seeker/internal/app"
)

// pressed reports a frame's press of a command's chord. It is the one place
// the view reads the chords from, so a rebinding in the settings reaches
// every key at once.
func (s *App) pressed(c *ui.Context, id app.Command) bool {
	ch := s.core.Shortcut(id)
	return ch.Bound() && !s.core.Recording() && c.Shortcut(ch.Mods, ch.Key)
}

// chordText is a command's chord as the tooltips and the menus write it.
func (s *App) chordText(id app.Command) string { return app.ChordText(s.core.Shortcut(id)) }

// menuShortcut shows a command's chord on a menu item. An unbound one shows
// none.
func (s *App) menuShortcut(it *ui.MenuItem, id app.Command) *ui.MenuItem {
	if ch := s.core.Shortcut(id); ch.Bound() {
		it.Shortcut(ch.Mods, ch.Key)
	}
	return it
}
