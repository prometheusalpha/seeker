package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// ErrExists is a create or a rename onto a name that is taken: nothing
// replaces what is there.
var ErrExists = errors.New("already exists")

// ErrMissing is an entry that went away since the panel scanned.
var ErrMissing = errors.New("no longer exists")

// NameProblem says why name cannot be one entry's name, "" when it can.
// The name is one path segment: what stops a name typed in the panel from
// leaving the folder it was typed in.
func NameProblem(name string) string {
	switch {
	case strings.TrimSpace(name) == "":
		return "Name cannot be empty."
	case name == "." || name == "..":
		return fmt.Sprintf("%q is not a name.", name)
	case strings.ContainsAny(name, `/\`):
		return "Name cannot contain a path separator."
	case strings.ContainsRune(name, 0):
		return "Name cannot contain a NUL character."
	}
	return ""
}

// taken reports a path something is at; an error other than "not there"
// means the filesystem did not answer, which is never a green light.
func taken(path string) (bool, error) {
	_, err := os.Lstat(path)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	}
	return false, err
}

// Create makes an empty file or a folder named name under parent, and
// refuses a name in use: a plain write would truncate the user's file.
func Create(parent, name string, dir bool) (string, error) {
	if p := NameProblem(name); p != "" {
		return "", errors.New(p)
	}
	path := filepath.Join(parent, name)
	var err error
	if dir {
		err = os.Mkdir(path, 0o755)
	} else {
		var f *os.File
		if f, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644); err == nil {
			err = f.Close()
		}
	}
	if errors.Is(err, fs.ErrExist) {
		return "", fmt.Errorf("%q %w in this folder", name, ErrExists)
	}
	return path, err
}

// Rename gives an entry another name in the folder it is in. The target is
// looked at first: rename replaces what it lands on without an error, so a
// collision found only afterwards would be a destroyed file. A change of
// case alone lands on the entry itself on a case-insensitive disk, and is
// allowed.
func Rename(path, name string) (string, error) {
	if p := NameProblem(name); p != "" {
		return "", errors.New(p)
	}
	dest := filepath.Join(filepath.Dir(path), name)
	if dest == path {
		return path, nil
	}
	src, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", ErrMissing
	} else if err != nil {
		return "", err
	}
	if dst, err := os.Lstat(dest); err == nil {
		if !os.SameFile(src, dst) {
			return "", fmt.Errorf("%q %w in this folder", name, ErrExists)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("%q could not be checked: %w", name, err)
	}
	if err := os.Rename(path, dest); err != nil {
		return "", err
	}
	return dest, nil
}

// Move is an entry that landed: the path it had and the one it has.
type Move struct{ From, To string }

// Failure is an entry that did not, and why.
type Failure struct {
	Path    string
	Message string
}

// Transfer copies, or moves, each source into dest. It goes entry by entry:
// one refused entry is a failure and the rest still land. Each is checked
// before any write, so a refusal never leaves half a copy behind: not into
// itself, not onto a name in use.
func Transfer(sources []string, dest string, move bool) ([]Move, []Failure) {
	var moved []Move
	var failures []Failure
	for _, src := range sources {
		target := filepath.Join(dest, filepath.Base(src))
		if msg := transferRefusal(src, target, dest); msg != "" {
			failures = append(failures, Failure{src, msg})
			continue
		}
		var err error
		if move {
			err = moveEntry(src, target)
		} else {
			err = copyEntry(src, target)
		}
		if err != nil {
			failures = append(failures, Failure{src, err.Error()})
			continue
		}
		moved = append(moved, Move{src, target})
	}
	return moved, failures
}

func transferRefusal(src, target, dest string) string {
	if dest == src || strings.HasPrefix(dest, src+string(filepath.Separator)) {
		return "An entry cannot be moved into itself."
	}
	if _, err := os.Lstat(src); errors.Is(err, fs.ErrNotExist) {
		return "The entry no longer exists."
	} else if err != nil {
		return err.Error()
	}
	switch used, err := taken(target); {
	case err != nil:
		return err.Error()
	case used:
		return fmt.Sprintf("%q already exists in the destination.", filepath.Base(target))
	}
	return ""
}

// moveEntry renames; rename cannot cross a filesystem, so that case copies
// and then removes the original.
func moveEntry(src, target string) error {
	err := os.Rename(src, target)
	if !errors.Is(err, syscall.EXDEV) {
		return err
	}
	if err := copyEntry(src, target); err != nil {
		return err
	}
	if err := os.RemoveAll(src); err != nil {
		return fmt.Errorf("the copy succeeded but the original could not be removed: %w", err)
	}
	return nil
}

// copyEntry copies a file or a folder tree; a link is copied as a link, so
// it never reads what it points at.
func copyEntry(src, target string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		to, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(to, target)
	case info.IsDir():
		if err := os.Mkdir(target, info.Mode().Perm()|0o700); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := copyEntry(filepath.Join(src, e.Name()), filepath.Join(target, e.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// Trash puts an entry in the Trash: Finder does it, which keeps "Put Back".
// A swapped-in function lets tests leave the real Trash alone.
var Trash = func(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	// The path goes in as an argument, never into the script's text.
	script := `on run argv
	tell application "Finder" to delete (POSIX file (item 1 of argv))
end run`
	err := exec.CommandContext(ctx, "osascript", "-e", script, path).Run()
	if err == nil {
		return nil
	}
	home, herr := os.UserHomeDir()
	if herr != nil {
		return err
	}
	if ferr := TrashInto(filepath.Join(home, ".Trash"), path); ferr != nil {
		return fmt.Errorf("Finder refused (%v) and the Trash could not be written: %w", err, ferr)
	}
	return nil
}

// TrashInto moves path into the folder trash, under a name not used there.
func TrashInto(trash, path string) error {
	name := filepath.Base(path)
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	dest := filepath.Join(trash, name)
	for n := 2; ; n++ {
		used, err := taken(dest)
		if err != nil {
			return err
		}
		if !used {
			break
		}
		dest = filepath.Join(trash, fmt.Sprintf("%s %d%s", stem, n, ext))
	}
	return os.Rename(path, dest)
}

// Reveal shows an entry in Finder.
func Reveal(path string) error { return exec.Command("open", "-R", path).Run() }
