package grid

import "testing"

func TestTSVFlattensTabsAndNewlines(t *testing.T) {
	got := TSV([][]string{{"a\tb", "c\nd"}, {"", "e"}})
	if want := "a b\tc d\n\te"; got != want {
		t.Errorf("TSV = %q, want %q", got, want)
	}
}

func TestCSVQuotesPerRFC4180(t *testing.T) {
	got := CSV([]string{"id", "note,1"}, [][]any{
		{"1", `say "hi"`},
		{nil, "two\nlines"},
		{[]byte{0xff, 0x00}, "plain"},
	})
	want := "id,\"note,1\"\n1,\"say \"\"hi\"\"\"\n,\"two\nlines\"\n/wA=,plain"
	if got != want {
		t.Errorf("CSV =\n%q\nwant\n%q", got, want)
	}
}

func TestJSONMatchesStringifyLayout(t *testing.T) {
	got := JSON([]string{"id", "name", "blob"}, [][]any{
		{"1", "a\"b", []byte{1, 2}},
		{nil, "x", nil},
	})
	want := `[
  {
    "id": "1",
    "name": "a\"b",
    "blob": "AQI="
  },
  {
    "id": null,
    "name": "x",
    "blob": null
  }
]`
	if got != want {
		t.Errorf("JSON =\n%s\nwant\n%s", got, want)
	}
	if JSON([]string{"a"}, nil) != "[]" {
		t.Error("an empty result is not []")
	}
}
