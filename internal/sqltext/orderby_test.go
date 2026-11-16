package sqltext

import (
	"reflect"
	"testing"
)

func TestOrderTerms(t *testing.T) {
	cases := []struct {
		in   string
		want []OrderTerm
	}{
		{"", nil},
		{`"id" ASC`, []OrderTerm{{"id", false}}},
		{`"id" DESC`, []OrderTerm{{"id", true}}},
		{"name desc, `created at`", []OrderTerm{{"name", true}, {"created at", false}}},
		{`t."id" DESC NULLS LAST`, []OrderTerm{{"id", true}}},
		{"lower(name), id DESC", []OrderTerm{{"id", true}}},
		{"1, id", []OrderTerm{{"id", false}}},
		{"a + b DESC, c", []OrderTerm{{"c", false}}},
	}
	for _, c := range cases {
		if got := OrderTerms(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("OrderTerms(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
