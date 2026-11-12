package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/egoist/mygo/plugins/sqlite"

	"seeker/internal/model"
)

func IsMnemonic(m string) bool {
	return len(m) == 1 && (m[0] >= 'a' && m[0] <= 'z' || m[0] >= '0' && m[0] <= '9')
}

// lineHash must stay recall's (sha256 of the trimmed line, first 16 hex
// digits) for imported bookmarks to find their lines.
func lineHash(text string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(text)))
	return hex.EncodeToString(sum[:])[:16]
}

// Bookmarks lists the bookmarks by mnemonic, first dropping those whose
// file is gone.
func (s *Store) Bookmarks(ctx context.Context) ([]model.Bookmark, error) {
	rows, err := s.db.Query(ctx, "SELECT mnemonic, path, line, line_hash FROM bookmarks ORDER BY mnemonic")
	if err != nil {
		return nil, err
	}
	var out []model.Bookmark
	for _, r := range rows.Values {
		b := model.Bookmark{Mnemonic: str(r[0]), Path: str(r[1]), Line: num(r[2]), LineHash: str(r[3])}
		if _, err := os.Stat(b.Path); os.IsNotExist(err) {
			if _, err := s.db.Execute(ctx, "DELETE FROM bookmarks WHERE mnemonic = ?", b.Mnemonic); err != nil {
				return nil, err
			}
			continue
		}
		out = append(out, b)
	}
	return out, nil
}

// SetBookmark gives mnemonic to line of path, taking it from where it was
// and from any other mnemonic on that line.
func (s *Store) SetBookmark(ctx context.Context, mnemonic, path string, line int, lineText string) (model.Bookmark, error) {
	if !IsMnemonic(mnemonic) {
		return model.Bookmark{}, fmt.Errorf("not a bookmark mnemonic: %q", mnemonic)
	}
	b := model.Bookmark{Mnemonic: mnemonic, Path: path, Line: max(1, line), LineHash: lineHash(lineText)}
	_, err := s.db.Transaction(ctx, []sqlite.Statement{
		{SQL: "DELETE FROM bookmarks WHERE path = ? AND line = ? AND mnemonic <> ?", Args: []any{b.Path, b.Line, b.Mnemonic}},
		{SQL: "INSERT OR REPLACE INTO bookmarks (mnemonic, path, line, line_hash) VALUES (?, ?, ?, ?)", Args: []any{b.Mnemonic, b.Path, b.Line, b.LineHash}},
	})
	return b, err
}

// ClearBookmark drops a bookmark; one not set is not an error.
func (s *Store) ClearBookmark(ctx context.Context, mnemonic string) error {
	if !IsMnemonic(mnemonic) {
		return nil
	}
	_, err := s.db.Execute(ctx, "DELETE FROM bookmarks WHERE mnemonic = ?", mnemonic)
	return err
}

// ResolveBookmark finds the line of mnemonic in text, the file's current
// content: the recorded line when its hash still matches, else the
// matching line nearest to it, else the recorded line clamped to the file.
// The row follows what it found. ok is false for no bookmark, or one whose
// file is gone (which it drops).
func (s *Store) ResolveBookmark(ctx context.Context, mnemonic, text string) (b model.Bookmark, ok bool, err error) {
	if !IsMnemonic(mnemonic) {
		return b, false, nil
	}
	rows, err := s.db.Query(ctx, "SELECT path, line, line_hash FROM bookmarks WHERE mnemonic = ?", mnemonic)
	if err != nil || len(rows.Values) == 0 {
		return b, false, err
	}
	r := rows.Values[0]
	b = model.Bookmark{Mnemonic: mnemonic, Path: str(r[0]), Line: num(r[1]), LineHash: str(r[2])}
	if _, err := os.Stat(b.Path); os.IsNotExist(err) {
		_, err = s.db.Execute(ctx, "DELETE FROM bookmarks WHERE mnemonic = ?", mnemonic)
		return model.Bookmark{}, false, err
	}
	lines := strings.Split(text, "\n")
	at := func(n int) string { return lines[n-1] }
	recorded := min(max(b.Line, 1), len(lines))
	if lineHash(at(recorded)) == b.LineHash {
		b.Line = recorded
		return b, true, nil
	}
	best := 0
	for i := range lines {
		n := i + 1
		if lineHash(lines[i]) == b.LineHash && (best == 0 || abs(n-recorded) < abs(best-recorded)) {
			best = n
		}
	}
	line := recorded
	if best != 0 {
		line = best
	}
	hash := lineHash(at(line))
	if line != b.Line || hash != b.LineHash {
		if _, err := s.db.Execute(ctx, "UPDATE bookmarks SET line = ?, line_hash = ? WHERE mnemonic = ?", line, hash, mnemonic); err != nil {
			return b, false, err
		}
	}
	b.Line, b.LineHash = line, hash
	return b, true, nil
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
