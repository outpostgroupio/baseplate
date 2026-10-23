package addr

import (
	"strings"
	"testing"
)

func TestFormatSingularAndKeyed(t *testing.T) {
	if got := Format("roblox_asset", "icon", ""); got != "roblox_asset.icon" {
		t.Fatalf("singular = %q", got)
	}
	if got := Format("roblox_asset", "sprites", "ui/gem.png"); got != `roblox_asset.sprites["ui/gem.png"]` {
		t.Fatalf("keyed = %q", got)
	}
	if got := Format("t", "n", `a"b\c`); got != `t.n["a\"b\\c"]` {
		t.Fatalf("escaped = %q", got)
	}
}

func TestParseRoundTripsFormat(t *testing.T) {
	for _, key := range []string{"", "ui/gem.png", `quo"te`, `back\slash`, "${not_a_template}", "%{x}", "tab\there", "ünï cødé"} {
		s := Format("roblox_asset", "sprites", key)
		typ, name, k, hasKey, err := Parse(s)
		if err != nil {
			t.Fatalf("Parse(%q): %v", s, err)
		}
		if typ != "roblox_asset" || name != "sprites" || k != key || hasKey != (key != "") {
			t.Fatalf("Parse(%q) = %q %q %q %v", s, typ, name, k, hasKey)
		}
	}
}

func TestParseRejectsMalformed(t *testing.T) {
	cases := map[string]string{
		"roblox_asset.sprites[ui/gem.png]": "key must be a quoted string",
		"roblox_asset.sprites[0]":          "key must be a quoted string",
		`roblox_asset.sprites[""]`:         "must not be empty",
		"roblox_asset":                     "expected type.name",
		"roblox_asset.sprites.id":          "expected type.name",
		"":                                 "invalid address",
		"roblox_asset.sprites[\"a\"].id":   "expected type.name",
	}
	for in, want := range cases {
		_, _, _, _, err := Parse(in)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Parse(%q) error = %v, want containing %q", in, err, want)
		}
	}
}

// Format's fast path (keys that need no escaping) must render exactly what the
// HCL writer renders, so addresses stay canonical either way.
func TestFormatFastPathMatchesHCLQuoting(t *testing.T) {
	keys := []string{"a", "ui/gem.png", "units/warship-2.png", "with space", "UPPER_lower.123",
		"~!@#^&*()_+-=[]{}|;':,./<>?", `quo"te`, `back\slash`, "${x}", "$plain", "%{x}", "100%",
		"tab\there", "new\nline", "ünï", "日本", "\x7f"}
	for _, k := range keys {
		want := typAndName + "[" + hclQuote(k) + "]"
		if got := Format("t", "n", k); got != want {
			t.Errorf("Format(%q) = %s, want %s", k, got, want)
		}
	}
}

const typAndName = "t.n"

func TestFormatSimpleKeyIsCheap(t *testing.T) {
	allocs := testing.AllocsPerRun(100, func() { _ = Format("roblox_asset", "sprites", "ui/gem.png") })
	if allocs > 1 {
		t.Fatalf("Format of a plain key allocated %v times, want at most 1", allocs)
	}
}

func TestMatches(t *testing.T) {
	cases := []struct {
		typ, name, key, address string
		want                    bool
	}{
		{"t", "n", "", "t.n", true},
		{"t", "n", "", `t.n["a"]`, false},
		{"t", "n", "a", `t.n["a"]`, true},
		{"t", "n", "a", `t.n["b"]`, false},
		{"t", "n", "a", "t.n", false},
		{"t", "n", `q"`, `t.n["q\""]`, true},
		{"t", "n", "a", `t.nn["a"]`, false},
		{"t", "nn", "", "t.n", false},
		{"tt", "n", "", "t.n", false},
	}
	for _, c := range cases {
		if got := Matches(c.typ, c.name, c.key, c.address); got != c.want {
			t.Errorf("Matches(%q,%q,%q,%s) = %v, want %v", c.typ, c.name, c.key, c.address, got, c.want)
		}
	}
	allocs := testing.AllocsPerRun(100, func() { _ = Matches("roblox_asset", "sprites", "ui/gem.png", `roblox_asset.sprites["ui/gem.png"]`) })
	if allocs != 0 {
		t.Fatalf("Matches of a plain key allocated %v times, want 0", allocs)
	}
}
