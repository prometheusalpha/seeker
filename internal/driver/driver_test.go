package driver

import (
	"net/url"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"seeker/internal/model"
)

func TestParseURLParams(t *testing.T) {
	got := ParseURLParams(" ?&sslmode=verify-full&application_name=a+b&flag&&bad=%zz&enc=%2Fx ")
	want := [][2]string{{"sslmode", "verify-full"}, {"application_name", "a b"}, {"flag", ""}, {"bad", "%zz"}, {"enc", "/x"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	if ParseURLParams("  ") != nil {
		t.Fatal("blank params should be empty")
	}
}

func TestBuildRowUpdate(t *testing.T) {
	row := RowChange{KeyValues: []any{7, "eu"}, Changes: []CellChange{{"name", "a"}, {"age", 3}}}
	cases := []struct {
		name   string
		t      model.DBType
		schema string
		table  string
		keys   []string
		row    RowChange
		sql    string
		args   []any
	}{
		{"postgres numbers SET placeholders before the key", model.Postgres, "public", "users", []string{"id", "region"}, row,
			`UPDATE "public"."users" SET "name" = $1, "age" = $2 WHERE "id" = $3 AND "region" = $4`, []any{"a", 3, 7, "eu"}},
		{"mysql binds positionally in text order", model.MySQL, "shop", "users", []string{"id", "region"}, row,
			"UPDATE `shop`.`users` SET `name` = ?, `age` = ? WHERE `id` = ? AND `region` = ?", []any{"a", 3, 7, "eu"}},
		{"quotes break-out characters in identifiers", model.Postgres, "", `a"b`, []string{"id"},
			RowChange{KeyValues: []any{1}, Changes: []CellChange{{`c"d`, 1}}},
			`UPDATE "a""b" SET "c""d" = $1 WHERE "id" = $2`, []any{1, 1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := buildRowUpdate(c.t, c.schema, c.table, c.keys, c.row)
			if s.SQL != c.sql || !reflect.DeepEqual(s.Args, c.args) {
				t.Fatalf("got %s %v\nwant %s %v", s.SQL, s.Args, c.sql, c.args)
			}
		})
	}
	if n := len(buildRowUpdates(model.Postgres, "", "t", []string{"id"}, []RowChange{{KeyValues: []any{1}}, row})); n != 1 {
		t.Fatalf("rows without changes should be skipped, got %d statements", n)
	}
}

func TestPostgresURL(t *testing.T) {
	p := model.Profile{Host: "db.local", Port: 5432, Username: "u@x", URLParams: "application_name=seeker&search_path=app"}
	u, err := url.Parse(postgresURL(p, "my db"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.User.Username() != "u@x" || u.Path != "/my db" || q.Get("sslmode") != "disable" || q.Get("connect_timeout") != "30" ||
		q.Get("application_name") != "seeker" || q.Get("search_path") != "app" {
		t.Fatalf("unexpected url %s", u)
	}
	p.SSL = true
	if q := mustQuery(t, postgresURL(p, "")); q.Get("sslmode") != "require" {
		t.Fatalf("ssl flag should require TLS, got %q", q.Get("sslmode"))
	}
	p.URLParams = "sslmode=verify-full&connect_timeout=5"
	if q := mustQuery(t, postgresURL(p, "")); q.Get("sslmode") != "verify-full" || q.Get("connect_timeout") != "5" {
		t.Fatalf("explicit params should win: %v", q)
	}
}

func mustQuery(t *testing.T, s string) url.Values {
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query()
}

func TestMySQLConfig(t *testing.T) {
	p := model.Profile{Host: "127.0.0.1", Port: 3306, Username: "root",
		URLParams: "allowPublicKeyRetrieval=true&parseTime=true&multiStatements=true&charset=utf8mb4&sql_mode=ANSI"}
	cfg, err := mysqlConfig(p, "pw", " ")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DBName != "" {
		t.Fatalf("a blank database must connect with none, got %q", cfg.DBName)
	}
	if cfg.ParseTime || cfg.MultiStatements || cfg.InterpolateParams {
		t.Fatal("parseTime/multiStatements/interpolateParams are not the profile's to turn on")
	}
	if !reflect.DeepEqual(cfg.Params, map[string]string{"sql_mode": "ANSI"}) {
		t.Fatalf("only system variables are forwarded, got %v", cfg.Params)
	}
	if cfg.TLS != nil || cfg.TLSConfig != "" {
		t.Fatal("no TLS unless asked")
	}
	p.SSL, p.URLParams = true, ""
	if cfg, _ := mysqlConfig(p, "", ""); cfg.TLSConfig != "preferred" {
		t.Fatalf("ssl flag should prefer TLS, got %q", cfg.TLSConfig)
	}
	p.URLParams = "ssl-mode=require"
	if cfg, _ := mysqlConfig(p, "", ""); cfg.TLS == nil || !cfg.TLS.InsecureSkipVerify {
		t.Fatal("require encrypts without verifying")
	}
	p.URLParams = "allowPublicKeyRetrieval=false"
	if cfg, _ := mysqlConfig(p, "", ""); cfg.TLS == nil || cfg.TLS.InsecureSkipVerify {
		t.Fatal("refusing key retrieval means TLS that verifies the server")
	}
	p.URLParams = "allowPublicKeyRetrieval=false&ssl-mode=verify-ca"
	if cfg, _ := mysqlConfig(p, "", ""); cfg.TLS == nil || cfg.TLS.VerifyPeerCertificate == nil {
		t.Fatal("a chosen verify-ca is kept")
	}
	p.URLParams = "ssl-mode=verify-full"
	if cfg, _ := mysqlConfig(p, "", ""); cfg.TLS == nil || cfg.TLS.InsecureSkipVerify || cfg.TLS.ServerName != "127.0.0.1" {
		t.Fatal("verify-full checks chain and host")
	}
}

func TestRuneOffset(t *testing.T) {
	s := "select 'ả' frm"
	if got := runeOffset(s, utf8.RuneCountInString(s[:strings.Index(s, "frm")])); s[got:] != "frm" {
		t.Fatalf("got %q", s[got:])
	}
}
