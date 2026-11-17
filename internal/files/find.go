package files

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	// MaxFindRows is how many rows a search returns at most. Past it the list
	// is noise; the user narrows the query.
	MaxFindRows = 500
	// maxLineBytes clips a longer line around its first hit, keeping a huge
	// minified line drawable.
	maxLineBytes = 400
	// clipLead is how much of a clipped line precedes the first hit.
	clipLead = 80
)

// FindRequest is one Find in Files search over the opened folders.
type FindRequest struct {
	// Folders are real paths. Nested ones are fine: duplicate hits drop.
	Folders []string
	Query   string
	// Mask is JetBrains-style: comma-separated globs, `!` excludes, braces
	// group (`*.{sql,md}`). Empty or `*` is every file.
	Mask          string
	CaseSensitive bool
	Regex         bool
	WholeWord     bool
}

// FindMatch is one matching line; several hits on it share the row.
type FindMatch struct {
	Path string
	// Line is from 1. Col and Len are the first hit's place in the full
	// line, in bytes, which is what the editor counts in.
	Line, Col, Len int
	// Text is the line, clipped around the first hit when it is very long.
	Text string
}

// FindResult is what a search found. Truncated is set when the row cap
// stopped it early.
type FindResult struct {
	Matches   []FindMatch
	Truncated bool
}

// SplitMask splits a mask on the commas outside braces, so `*.{sql,md}`
// stays one glob. Empty parts and a bare `*` (every file) are dropped.
func SplitMask(mask string) []string {
	var parts []string
	depth, start := 0, 0
	add := func(end int) {
		if p := strings.TrimSpace(mask[start:end]); p != "" && p != "*" {
			parts = append(parts, p)
		}
	}
	for i, r := range mask {
		switch {
		case r == '{':
			depth++
		case r == '}':
			depth = max(0, depth-1)
		case r == ',' && depth == 0:
			add(i)
			start = i + 1
		}
	}
	add(len(mask))
	return parts
}

// rgArgs searches what the Files panel shows, not what git tracks: hidden
// and ignored files are in, the directories the scan skips are out, and
// symlinks are not followed (the panel follows only those that resolve
// inside a folder, and rg has no such check).
func rgArgs(req FindRequest) []string {
	args := []string{
		"--json", "--no-config", "--no-messages", "--hidden", "--no-ignore",
		"--sort=path", "--max-filesize=8M",
		// The scan goes MaxDepth levels down; the files sit one deeper.
		"--max-depth=" + strconv.Itoa(MaxDepth+1),
	}
	if req.CaseSensitive {
		args = append(args, "--case-sensitive")
	} else {
		args = append(args, "--ignore-case")
	}
	if !req.Regex {
		args = append(args, "--fixed-strings")
	}
	if req.WholeWord {
		args = append(args, "--word-regexp")
	}
	dirs := make([]string, 0, len(ignoreDirs))
	for d := range ignoreDirs {
		dirs = append(dirs, d)
	}
	slices.Sort(dirs)
	for _, d := range dirs {
		args = append(args, "--iglob=!"+d+"/")
	}
	for _, g := range SplitMask(req.Mask) {
		args = append(args, "--iglob="+g)
	}
	args = append(args, "--regexp", req.Query, "--")
	return append(args, req.Folders...)
}

type rgText struct {
	Text  *string `json:"text"`
	Bytes *string `json:"bytes"`
}

func (t rgText) decode() (string, bool) {
	switch {
	case t.Text != nil:
		return *t.Text, true
	case t.Bytes != nil:
		b, err := base64.StdEncoding.DecodeString(*t.Bytes)
		return string(b), err == nil
	}
	return "", false
}

type rgEvent struct {
	Type string `json:"type"`
	Data struct {
		Path       rgText `json:"path"`
		Lines      rgText `json:"lines"`
		LineNumber int    `json:"line_number"`
		Submatches []struct {
			Start int `json:"start"`
			End   int `json:"end"`
		} `json:"submatches"`
	} `json:"data"`
}

// matchOf is one rg `match` event as a row.
func matchOf(ev rgEvent) (FindMatch, bool) {
	path, ok := ev.Data.Path.decode()
	if !ok {
		return FindMatch{}, false
	}
	raw, ok := ev.Data.Lines.decode()
	if !ok {
		return FindMatch{}, false
	}
	line := strings.TrimSuffix(strings.TrimSuffix(raw, "\n"), "\r")
	m := FindMatch{Path: path, Line: ev.Data.LineNumber, Text: line}
	if len(ev.Data.Submatches) > 0 {
		s := ev.Data.Submatches[0]
		m.Col, m.Len = min(s.Start, len(line)), max(0, min(s.End, len(line))-min(s.Start, len(line)))
	}
	if len(line) > maxLineBytes {
		from := clipBoundary(line, max(0, m.Col-clipLead))
		to := clipBoundary(line, min(len(line), from+maxLineBytes))
		m.Text = line[from:to]
	}
	return m, true
}

// clipBoundary moves i back to the start of a rune.
func clipBoundary(s string, i int) int {
	for i > 0 && i < len(s) && !utf8.RuneStart(s[i]) {
		i--
	}
	return i
}

// Find runs ripgrep at rg over the request's folders and returns its rows,
// at most MaxFindRows. Cancelling ctx kills the process: a search for a
// query the user has typed past is worth nothing. A search that cannot run
// (a regex that does not parse) comes back as the error, with rg's message.
func Find(ctx context.Context, rg string, req FindRequest) (FindResult, error) {
	var res FindResult
	if req.Query == "" || len(req.Folders) == 0 {
		return res, nil
	}
	cmd := exec.CommandContext(ctx, rg, rgArgs(req)...)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return res, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return res, err
	}
	seen := map[string]bool{}
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 0, 64<<10), 64<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if !bytes.HasPrefix(line, []byte(`{"type":"match"`)) {
			continue
		}
		var ev rgEvent
		if json.Unmarshal(line, &ev) != nil {
			continue
		}
		m, ok := matchOf(ev)
		if !ok {
			continue
		}
		m.Path = filepath.Clean(m.Path)
		key := m.Path + "\x00" + strconv.Itoa(m.Line)
		if seen[key] {
			continue
		}
		seen[key] = true
		res.Matches = append(res.Matches, m)
		if len(res.Matches) >= MaxFindRows {
			res.Truncated = true
			cancelKill(cmd)
			break
		}
	}
	// Draining is not needed after a kill; Wait reaps the process.
	werr := cmd.Wait()
	if ctx.Err() != nil {
		return FindResult{}, ctx.Err()
	}
	// Exit 2 also covers "some file was unreadable": with rows in hand that
	// is no reason for a banner, an empty search is.
	var ee *exec.ExitError
	if len(res.Matches) == 0 && errors.As(werr, &ee) && ee.ExitCode() == 2 {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = "ripgrep failed"
		}
		return FindResult{}, errors.New(msg)
	}
	return res, nil
}

func cancelKill(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
