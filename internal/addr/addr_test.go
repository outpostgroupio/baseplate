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
