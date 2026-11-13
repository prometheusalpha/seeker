package grid

import (
	"reflect"
	"testing"
)

func TestFullColumnOrder(t *testing.T) {
	cases := []struct {
		name            string
		overlay, result []string
		want            []string
	}{
		{"an empty overlay is the result's own order", nil, []string{"a", "b", "c"}, []string{"a", "b", "c"}},
		{"drops stale names and appends unseen ones", []string{"c", "gone", "a"}, []string{"a", "b", "c"}, []string{"c", "a", "b"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := FullColumnOrder(c.overlay, c.result); !reflect.DeepEqual(got, c.want) {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestMergeVisibleOrder(t *testing.T) {
	// b is hidden; the header swapped a and c.
	got := MergeVisibleOrder([]string{"a", "b", "c", "d"}, []string{"c", "a", "d"})
	if want := []string{"c", "b", "a", "d"}; !reflect.DeepEqual(got, want) {
		t.Errorf("hidden columns keep their slots: got %v, want %v", got, want)
	}
}

func TestMoveItem(t *testing.T) {
	abc := func() []string { return []string{"a", "b", "c"} }
	cases := []struct {
		name     string
		from, to int
		want     []string
	}{
		{"forwards", 0, 2, []string{"b", "c", "a"}},
		{"backwards", 2, 0, []string{"c", "a", "b"}},
		{"in place", 1, 1, []string{"a", "b", "c"}},
		{"adjacent", 0, 1, []string{"b", "a", "c"}},
	}
	for _, c := range cases {
		t.Run("moves "+c.name, func(t *testing.T) {
			if got := MoveItem(abc(), c.from, c.to); !reflect.DeepEqual(got, c.want) {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
	t.Run("out-of-range indexes leave the list alone", func(t *testing.T) {
		list := []string{"a", "b"}
		got := MoveItem(list, 0, 5)
		if &got[0] != &list[0] {
			t.Errorf("want the same list back")
		}
	})
	t.Run("a move does not write through to the input", func(t *testing.T) {
		list := abc()
		MoveItem(list, 0, 2)
		if !reflect.DeepEqual(list, abc()) {
			t.Errorf("input changed: %v", list)
		}
	})
}
