package driver

import (
	"testing"

	"seeker/internal/model"
)

func TestReadURLParam(t *testing.T) {
	cases := []struct {
		raw, key, want string
	}{
		{"", "sslmode", ""},
		{"sslmode=require", "sslmode", "require"},
		{"?sslmode=require&x=1", "sslmode", "require"},
		{"&&sslmode=verify-full", "sslmode", "verify-full"},
		{" x = 1 ", "x", "1"},
		{"a=hello+world", "a", "hello world"},
		{"a=%E2%9C%93", "a", "✓"},
		// A value that does not decode stays as typed, as recall keeps it.
		{"a=100%", "a", "100%"},
		{"sslmode=", "sslmode", ""},
		// The first of a duplicated key wins here; the drivers keep the last.
		{"a=1&a=2", "a", "1"},
	}
	for _, c := range cases {
		if got := ReadURLParam(c.raw, c.key); got != c.want {
			t.Errorf("ReadURLParam(%q, %q) = %q, want %q", c.raw, c.key, got, c.want)
		}
	}
}

func TestSetURLParam(t *testing.T) {
	cases := []struct {
		raw  string
		keys []string
		val  string
		want string
	}{
		{"", []string{"sslmode"}, "require", "sslmode=require"},
		{"?sslmode=prefer&x=1", []string{"sslmode"}, "require", "x=1&sslmode=require"},
		{"sslmode=prefer", []string{"sslmode"}, "", ""},
		// Every alias of the one setting goes, not just the first.
		{"ssl-mode=required&sslmode=require&ssl=1&x=2", []string{"ssl-mode", "sslmode", "ssl"}, "", "x=2"},
		{" x = 1 ", []string{"sslmode"}, "require", "x = 1&sslmode=require"},
	}
	for _, c := range cases {
		if got := SetURLParam(c.raw, c.keys, c.val); got != c.want {
			t.Errorf("SetURLParam(%q, %v, %q) = %q, want %q", c.raw, c.keys, c.val, got, c.want)
		}
	}
}

func TestTLSModeRoundTrip(t *testing.T) {
	pg := model.Profile{DBType: model.Postgres}
	for _, mode := range TLSModes(model.Postgres) {
		p := WithTLSMode(pg, mode)
		if got := TLSMode(p.DBType, p.SSL, p.URLParams); got != mode {
			t.Errorf("postgres %q: stored as %q, read back %q", mode, p.URLParams, got)
		}
	}
	my := model.Profile{DBType: model.MySQL}
	for _, mode := range TLSModes(model.MySQL) {
		p := WithTLSMode(my, mode)
		if got := TLSMode(p.DBType, p.SSL, p.URLParams); got != mode {
			t.Errorf("mysql %q: stored as %q (ssl %v), read back %q", mode, p.URLParams, p.SSL, got)
		}
	}
}

// A parameter the user typed beats the ssl switch, as it does in recall.
func TestTLSModeFromParams(t *testing.T) {
	if got := TLSMode(model.Postgres, true, "sslmode=verify-full"); got != "verify-full" {
		t.Errorf("got %q, want verify-full", got)
	}
	if got := TLSMode(model.Postgres, true, ""); got != "prefer" {
		t.Errorf("got %q, want prefer", got)
	}
	if got := TLSMode(model.MySQL, false, "ssl-mode=verify-ca"); got != "verify_ca" {
		t.Errorf("got %q, want verify_ca", got)
	}
	if got := TLSMode(model.MySQL, true, ""); got != "preferred" {
		t.Errorf("got %q, want preferred", got)
	}
}
