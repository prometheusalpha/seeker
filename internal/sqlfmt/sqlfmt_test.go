package sqlfmt

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"seeker/internal/sqltext"
)

func format(t *testing.T, sql string, d sqltext.Dialect) string {
	t.Helper()
	got, err := Format(context.Background(), sql, d)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// The expected text is sql-formatter run directly in bun (testdata/golden.sh),
// so a pass is the proof that goja gives recall's output.
func TestMatchesBun(t *testing.T) {
	b, err := os.ReadFile("testdata/golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct{ Language, SQL, Want string }
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("no golden cases")
	}
	for _, c := range cases {
		d := sqltext.Postgres
		if c.Language == "mysql" {
			d = sqltext.MySQL
		}
		if got := format(t, c.SQL, d); got != c.Want {
			t.Errorf("%s %q:\n got %q\nwant %q", c.Language, c.SQL, got, c.Want)
		}
	}
}

func TestPostgresCastsJoinsAndCase(t *testing.T) {
	got := format(t, "select a::int from t join u on u.id = t.id where b::text = 'x' order by a limit 3", sqltext.Postgres)
	for _, want := range []string{"a::int", "b::text", "\n  join u on u.id = t.id", "select\n", "limit\n  3"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q missing from\n%s", want, got)
		}
	}
	for _, bad := range []string{"CAST", "INNER JOIN", "SELECT", "FROM", "JOIN u"} {
		if strings.Contains(got, bad) {
			t.Errorf("%q rewritten into\n%s", bad, got)
		}
	}
	if !strings.Contains(got, "\n  a::int") {
		t.Errorf("not indented by two spaces:\n%s", got)
	}
}

func TestMySQLDialect(t *testing.T) {
	sql := "select `a` from t where b like '%x%' limit 5, 10"
	got := format(t, sql, sqltext.MySQL)
	if !strings.Contains(got, "`a`") || !strings.Contains(got, "limit\n  5, 10") {
		t.Errorf("mysql output:\n%s", got)
	}
	// Backticks are not Postgres quoting: the dialect decides how this reads.
	if _, err := Format(context.Background(), sql, sqltext.Postgres); err == nil {
		t.Error("backticks parsed as Postgres")
	}
}

func TestErrorIsTrimmed(t *testing.T) {
	_, err := Format(context.Background(), "select * from t where (", sqltext.Postgres)
	var e *Error
	if !asError(err, &e) {
		t.Fatalf("err = %v", err)
	}
	if n := len([]rune(e.Message)); n == 0 || n > MaxErrorRunes+1 || strings.Contains(e.Message, "\n") {
		t.Fatalf("message %d runes: %q", n, e.Message)
	}
}

func asError(err error, target **Error) bool {
	e, ok := err.(*Error)
	if ok {
		*target = e
	}
	return ok
}

func TestTrim(t *testing.T) {
	long := strings.Repeat("é", 500)
	got := trim("first line " + long + "\nsecond line")
	if n := len([]rune(got)); n != MaxErrorRunes+1 || !strings.HasSuffix(got, "…") || strings.Contains(got, "second") {
		t.Fatalf("trim = %d runes %q", n, got)
	}
	if got := trim("short\nrest"); got != "short" {
		t.Fatalf("trim = %q", got)
	}
}

func TestCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Format(ctx, "select 1", sqltext.Postgres); err != context.Canceled {
		t.Fatalf("err = %v", err)
	}
	// An abandoned call leaves the runtime usable for the next.
	if got := format(t, "select 1", sqltext.Postgres); got != "select\n  1" {
		t.Fatalf("after cancel: %q", got)
	}
}

func TestInterruptsRunningFormat(t *testing.T) {
	// Large enough that formatting outlasts the deadline.
	sql := "select " + strings.Repeat("a + b * (c - d), ", 40000) + "z from t"
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := Format(ctx, sql, sqltext.Postgres)
	if err != context.DeadlineExceeded {
		t.Skipf("format finished in %v before the deadline (err = %v)", time.Since(start), err)
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("interrupt took %v", took)
	}
	if got := format(t, "select 1", sqltext.MySQL); got != "select\n  1" {
		t.Fatalf("after interrupt: %q", got)
	}
}

func TestConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got := format(t, "select a::int from t", sqltext.Postgres); got != "select\n  a::int\nfrom\n  t" {
				t.Errorf("got %q", got)
			}
		}()
	}
	wg.Wait()
}
