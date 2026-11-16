// Package files is the filesystem under the Files panel: scanning an opened
// folder, and keeping every path it hands out inside that folder.
package files

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// MaxDepth caps the recursion: a tree deeper than this is almost certainly
// not SQL.
const MaxDepth = 10

// ignoreDirs are never worth browsing and often so big (tens of thousands of
// entries) that scanning them would stall the panel. Matched lower-cased.
var ignoreDirs = map[string]bool{
	".git": true, "node_modules": true, ".venv": true, "dist": true,
	"build": true, ".next": true, "target": true, "__pycache__": true,
	".idea": true, ".cache": true,
}

// Node is an entry of a scanned folder. Path is the real path, symlinks
// resolved, so it is also the entry's identity.
type Node struct {
	Path     string
	Name     string
	IsDir    bool
	Children []*Node
}

// Inside resolves entry through symlinks and returns it only when it really
// lives inside root, which must already be a real path. A link dropped into
// an opened folder must not become a window onto the rest of the disk, so one
// that resolves outside, or not at all, is refused.
func Inside(root, entry string) (string, bool) {
	real, err := filepath.EvalSymlinks(entry)
	if err != nil {
		return "", false
	}
	if real == root {
		return real, true
	}
	rel, err := filepath.Rel(root, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	return real, true
}

// Scan reads folder recursively and returns the entries that resolve inside
// it, directories first, then by name ignoring case. Files must match
// filter; directories are kept even empty, so a folder just created shows.
// It fails when the folder itself cannot be read: a silently empty tree would
// read as "no SQL here".
func Scan(folder, filter string) (root string, nodes []*Node, err error) {
	root, err = filepath.EvalSymlinks(folder)
	if err != nil {
		return "", nil, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", nil, err
	}
	if !info.IsDir() {
		return "", nil, errors.New("not a folder")
	}
	nodes = scanDir(root, root, 1, filter, map[string]bool{root: true})
	return root, nodes, nil
}

func scanDir(root, dir string, depth int, filter string, visited map[string]bool) []*Node {
	if depth > MaxDepth {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		// One unreadable subfolder must not hide the rest.
		return nil
	}
	var out []*Node
	for _, e := range entries {
		name := e.Name()
		if strings.EqualFold(name, ".DS_Store") {
			continue
		}
		real, ok := Inside(root, filepath.Join(dir, name))
		if !ok {
			continue
		}
		isDir := e.IsDir()
		if e.Type()&os.ModeSymlink != 0 {
			// A link's type describes the link; the target says what it is.
			st, err := os.Stat(real)
			if err != nil {
				continue
			}
			isDir = st.IsDir()
		}
		if isDir {
			if ignoreDirs[strings.ToLower(name)] || visited[real] {
				continue
			}
			// Each real directory is scanned once, so a link back up the
			// tree cannot loop.
			visited[real] = true
			out = append(out, &Node{Path: real, Name: name, IsDir: true, Children: scanDir(root, real, depth+1, filter, visited)})
			continue
		}
		if Match(name, filter) {
			out = append(out, &Node{Path: real, Name: name})
		}
	}
	slices.SortFunc(out, func(a, b *Node) int {
		if a.IsDir != b.IsDir {
			if a.IsDir {
				return -1
			}
			return 1
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return out
}
