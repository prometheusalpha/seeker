// Package redis is the Redis half of the backend: a client per logical
// database, the operations the key browser and the console need, and the
// rules for what the console may run without asking.
package redis

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Bytes is a key, field or value as the UI shows it. Redis is binary-safe
// and a Go string shown as text is not: written back, a lossy decode would
// corrupt the key. Data is always the exact bytes; Text is them as text when
// they are valid UTF-8, else an escaped label, and Binary is then set, which
// makes the UI show them read-only.
type Bytes struct {
	Data   []byte
	Text   string
	Binary bool
}

// NewBytes reads b. Valid UTF-8 is the text itself; otherwise printable ASCII
// stays, everything else becomes `\xNN`, and the backslash is escaped so the
// label is unambiguous.
func NewBytes(b []byte) Bytes {
	if utf8.Valid(b) {
		return Bytes{Data: b, Text: string(b)}
	}
	var sb strings.Builder
	for _, c := range b {
		switch {
		case c == '\\':
			sb.WriteString(`\\`)
		case c >= 0x20 && c < 0x7f:
			sb.WriteByte(c)
		default:
			fmt.Fprintf(&sb, `\x%02x`, c)
		}
	}
	return Bytes{Data: b, Text: sb.String(), Binary: true}
}

// TextBytes is the Bytes of text the user typed.
func TextBytes(s string) Bytes { return Bytes{Data: []byte(s), Text: s} }

// ID names the bytes for a list to follow a choice by.
func (b Bytes) ID() string { return string(b.Data) }
