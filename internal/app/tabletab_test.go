package app

import (
	"testing"

	"seeker/internal/model"
	"seeker/internal/sqltext"
)

func TestTableSQL(t *testing.T) {
	cases := []struct {
		name          string
		db            model.DBType
		schema, table string
		where, order  string
		page, size    int
		want          string
	}{
		{"plain", model.Postgres, "public", "users", "", "", 1, 100, `SELECT * FROM "public"."users" LIMIT 100`},
		{"where and order", model.Postgres, "public", "users", " id > 5 ", "id DESC", 1, 500,
			`SELECT * FROM "public"."users" WHERE id > 5 ORDER BY id DESC LIMIT 500`},
		{"later page", model.Postgres, "app", "t", "", "", 3, 100, `SELECT * FROM "app"."t" LIMIT 100 OFFSET 200`},
		{"mysql names the database", model.MySQL, "shop", "orders", "", "`id` ASC", 2, 1000,
			"SELECT * FROM `shop`.`orders` ORDER BY `id` ASC LIMIT 1000 OFFSET 1000"},
		{"page 0 is page 1", model.Postgres, "public", "users", "", "", 0, 100, `SELECT * FROM "public"."users" LIMIT 100`},
	}
	for _, tc := range cases {
		if got := tableSQL(tc.db, tc.schema, tc.table, tc.where, tc.order, tc.page, tc.size); got != tc.want {
			t.Errorf("%s:\n got %s\nwant %s", tc.name, got, tc.want)
		}
	}
}

func TestClauseBlame(t *testing.T) {
	for _, tc := range []struct{ where, order, want string }{
		{"id = 1", "", "where"}, {"", "id", "orderBy"}, {"id = 1", "id", ""}, {"", "", ""}, {"  ", "id", "orderBy"},
	} {
		if got := clauseBlame(tc.where, tc.order); got != tc.want {
			t.Errorf("clauseBlame(%q, %q) = %q, want %q", tc.where, tc.order, got, tc.want)
		}
	}
}

func tableTab(a *App, id string) *TableView {
	a.profiles = []model.Profile{{ID: "c", Name: "c", DBType: model.Postgres, Database: "d"}}
	a.tabs.Items = append(a.tabs.Items, Tab{ID: id, Title: "users", Conn: "c", Database: "d", Schema: "public", Table: "users", Mode: ModeTable})
	a.tabs.Active = len(a.tabs.Items) - 1
	return a.TableOf(id)
}

// A WHERE that carries a second statement is refused before anything is
// sent: the field is raw SQL, but only ever one SELECT.
func TestTableFilterRefusesSecondStatement(t *testing.T) {
	a := newTestApp(t)
	tv := tableTab(a, "t")
	tv.WhereText = "1 = 1; DROP TABLE users"
	a.ApplyTableFilter("t")
	if tv.Loading || tv.Err != "expected one statement, got 2" || tv.ErrTarget != "where" {
		t.Fatalf("loading %v, err %q, blame %q", tv.Loading, tv.Err, tv.ErrTarget)
	}
	// A `;` inside a literal is not a second statement.
	sql := tableSQL(model.Postgres, "public", "users", "name = 'a;b'", "", 1, 100)
	if n := len(sqltext.Split(sql, sqltext.Postgres)); n != 1 {
		t.Errorf("a semicolon in a string split %q into %d statements", sql, n)
	}
}

func TestTableFilterResetsPageAndCount(t *testing.T) {
	a := newTestApp(t)
	tv := tableTab(a, "t")
	tv.Page, tv.HasCount, tv.Count = 4, true, 900
	tv.WhereText, tv.OrderText = " id > 3 ", " id DESC "
	a.ApplyTableFilter("t")
	if tv.Page != 1 || tv.HasCount || tv.Where != "id > 3" || tv.OrderBy != "id DESC" {
		t.Errorf("after apply: page %d, count %v, %q, %q", tv.Page, tv.HasCount, tv.Where, tv.OrderBy)
	}
}

func TestTablePageSizes(t *testing.T) {
	a := newTestApp(t)
	tv := tableTab(a, "t")
	tv.Page = 5
	a.SetTablePageSize("t", 500)
	if tv.PageSize != 500 || tv.Page != 1 {
		t.Errorf("page size %d, page %d", tv.PageSize, tv.Page)
	}
	a.SetTablePageSize("t", 123)
	if tv.PageSize != 500 {
		t.Errorf("a size outside the options was taken: %d", tv.PageSize)
	}
	tv.Grid = NewResultGrid(sampleResult(), nil)
	if tv.HasNextPage() {
		t.Error("a short page has a next page")
	}
	tv.PageSize = 3
	if !tv.HasNextPage() {
		t.Error("a full page has none")
	}
	if a.TableOf("nope") != nil {
		t.Error("TableOf made state for a tab that does not exist")
	}
}

func TestSortTableWritesOrderBy(t *testing.T) {
	a := newTestApp(t)
	tv := tableTab(a, "t")
	tv.Grid = NewResultGrid(sampleResult(), sampleTarget())
	a.SortTable("t", 1, true)
	if tv.OrderText != `"email" DESC` || tv.OrderBy != `"email" DESC` {
		t.Errorf("order: %q / %q", tv.OrderText, tv.OrderBy)
	}
	a.ClearSort("t")
	if tv.OrderBy != "" {
		t.Errorf("Clear sort left %q", tv.OrderBy)
	}
}
