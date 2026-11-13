package grid

// The grid's column-order overlay as plain list operations, so the header
// drag and the Columns menu drag write one order and can never disagree.
//
// The overlay holds every column, hidden ones included: a column hidden and
// shown again comes back where it was, not appended to the end.

// FullColumnOrder is every result column in display order. Overlay names the
// result no longer has are dropped; result columns the overlay never saw keep
// their own order at the end.
func FullColumnOrder(overlay, resultColumns []string) []string {
	live := make(map[string]bool, len(resultColumns))
	for _, c := range resultColumns {
		live[c] = true
	}
	out := make([]string, 0, len(resultColumns))
	placed := map[string]bool{}
	for _, name := range overlay {
		if live[name] {
			out = append(out, name)
			placed[name] = true
		}
	}
	for _, name := range resultColumns {
		if !placed[name] {
			out = append(out, name)
		}
	}
	return out
}

// MergeVisibleOrder writes a new order of the visible columns back into the
// full order. Each slot a visible column held takes the next name of
// visible; hidden columns keep their slots.
func MergeVisibleOrder(full, visible []string) []string {
	shown := make(map[string]bool, len(visible))
	for _, v := range visible {
		shown[v] = true
	}
	out := make([]string, len(full))
	cursor := 0
	for i, name := range full {
		out[i] = name
		if shown[name] && cursor < len(visible) {
			out[i] = visible[cursor]
			cursor++
		}
	}
	return out
}

// MoveItem is list with the item at from moved to to, both indexes into
// list. Out-of-range indexes return list itself; otherwise the result is a
// copy.
func MoveItem[T any](list []T, from, to int) []T {
	if from < 0 || from >= len(list) || to < 0 || to >= len(list) {
		return list
	}
	out := make([]T, 0, len(list))
	out = append(out, list[:from]...)
	out = append(out, list[from+1:]...)
	out = append(out[:to], append([]T{list[from]}, out[to:]...)...)
	return out
}
