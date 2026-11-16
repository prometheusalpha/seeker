package theme

import "testing"

func TestParseFeatures(t *testing.T) {
	for in, want := range map[string]string{
		"":                             "",
		"ss01, ss02, calt, cv09, dlig": "ss01,ss02,calt,cv09,dlig",
		"-liga, ss01":                  "liga=0,ss01",
		` "calt" ,, cv09=2 `:           "calt,cv09=2",
		"-":                            "",
	} {
		if got := ParseFeatures(in); got != want {
			t.Errorf("ParseFeatures(%q) = %q, want %q", in, got, want)
		}
	}
}
