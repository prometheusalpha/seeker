// Package sqlfmt formats SQL with sql-formatter, the library recall uses,
// run in goja so the output is the same text, not an approximation of it.
// GoSQLX was tried and dropped: it rewrote `::` casts and `JOIN`.
package sqlfmt

//go:generate ./gen.sh

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/dop251/goja"

	"seeker/internal/sqltext"
)

// sql-formatter 15.9.0 (see gen.sh), the version recall has installed.
//
//go:embed sql-formatter.js
var bundle string

// MaxErrorRunes caps an error's message. The parser's own message is a
// listing of every token the grammar would have accepted, thousands of
// characters, so only its first line is kept, cut at this many runes. Both
// numbers are recall's `formatFailureSummary`.
const MaxErrorRunes = 200

// Error is a statement the formatter rejected. Its message is already trimmed.
type Error struct{ Message string }

func (e *Error) Error() string { return e.Message }

// goja runtimes are not goroutine-safe, so there is one, behind a one-slot
// semaphore rather than a mutex so that a caller waiting for it can give up.
// Formatting is a user action on one statement: a pool would buy nothing.
var (
	program = sync.OnceValues(func() (*goja.Program, error) {
		return goja.Compile("sql-formatter.js", bundle, false)
	})
	slot = make(chan struct{}, 1)
	vm   *goja.Runtime
	call goja.Callable
)

// load builds the runtime on first use; the caller holds slot.
func load() error {
	if vm != nil {
		return nil
	}
	p, err := program()
	if err != nil {
		return err
	}
	rt := goja.New()
	if _, err := rt.RunProgram(p); err != nil {
		return err
	}
	fn, ok := goja.AssertFunction(rt.Get("formatSql"))
	if !ok {
		return errors.New("bundle defines no formatSql")
	}
	vm, call = rt, fn
	return nil
}

// Format formats sql for the dialect. Only the language is set; keyword case
// is kept (recasing the user's SQL is a preference the app has nowhere to
// ask for) and the indent is two spaces, the width Tab inserts.
//
// It is safe for concurrent use; calls run one at a time. Cancelling ctx
// interrupts a format already running and returns ctx's error. A statement
// the parser rejects comes back as *Error.
func Format(ctx context.Context, sql string, d sqltext.Dialect) (string, error) {
	select {
	case slot <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-slot }()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := load(); err != nil {
		return "", fmt.Errorf("sqlfmt: %w", err)
	}

	// When it is too late to stop the interrupter, wait for it: an Interrupt
	// landing after ClearInterrupt would abort the next call.
	fired := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(fired)
		vm.Interrupt(ctx.Err())
	})
	v, err := call(goja.Undefined(), vm.ToValue(sql), vm.ToValue(language(d)))
	if !stop() {
		<-fired
	}
	vm.ClearInterrupt()

	if err == nil {
		return v.String(), nil
	}
	var interrupted *goja.InterruptedError
	if errors.As(err, &interrupted) {
		return "", ctx.Err()
	}
	var exc *goja.Exception
	if errors.As(err, &exc) {
		return "", &Error{trim(message(exc))}
	}
	return "", fmt.Errorf("sqlfmt: %w", err)
}

func language(d sqltext.Dialect) string {
	if d == sqltext.MySQL {
		return "mysql"
	}
	return "postgresql"
}

// message is the thrown Error's own message, not goja's rendering of it with
// a stack.
func message(exc *goja.Exception) string {
	if o, ok := exc.Value().(*goja.Object); ok {
		if m := o.Get("message"); m != nil {
			return m.String()
		}
	}
	return exc.Value().String()
}

func trim(msg string) string {
	first, _, _ := strings.Cut(msg, "\n")
	if utf8.RuneCountInString(first) <= MaxErrorRunes {
		return first
	}
	return string([]rune(first)[:MaxErrorRunes]) + "…"
}
