package view

import (
	"reflect"
	"testing"
)

func TestRuneRanges(t *testing.T) {
	// "é" is two bytes: offsets go in as runes and come out as bytes, and
	// neighbours join into one range.
	got := runeRanges("café_users", []int{0, 1, 3, 5, 6})
	want := [][2]int{{0, 2}, {3, 5}, {6, 8}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("runeRanges = %v, want %v", got, want)
	}
}
