package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// evalFn evaluates a single HCL expression against the config function library.
func evalFn(t *testing.T, src string) (cty.Value, error) {
	t.Helper()
	expr, diags := hclsyntax.ParseExpression([]byte(src), "test.hcl", hcl.InitialPos)
	if diags.HasErrors() {
		t.Fatalf("parse %q: %v", src, diags)
	}
	v, diags := expr.Value(&hcl.EvalContext{Functions: functions()})
	if diags.HasErrors() {
		return cty.NilVal, diags
	}
	return v, nil
}

func setStrings(t *testing.T, v cty.Value) []string {
	t.Helper()
	if !v.Type().IsSetType() {
		t.Fatalf("want a set, got %s", v.Type().FriendlyName())
	}
	var out []string
	for it := v.ElementIterator(); it.Next(); {
		_, e := it.Element()
		out = append(out, e.AsString())
	}
	return out
}

func writeTree(t *testing.T, root string, files ...string) {
	t.Helper()
	for _, f := range files {
		p := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFilesetDoubleStarFilesOnlyForwardSlashesSorted(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, "units/warship.png", "ui/gem.png", "ui/coin.png", "readme.txt", "deep/a/b/c.png")
	// A directory whose name matches the pattern must NOT be returned.
	if err := os.MkdirAll(filepath.Join(dir, "folder.png"), 0o755); err != nil {
		t.Fatal(err)
	}
	v, err := evalFn(t, `fileset("`+filepath.ToSlash(dir)+`", "**/*.png")`)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(setStrings(t, v), ",")
	want := "deep/a/b/c.png,ui/coin.png,ui/gem.png,units/warship.png"
	if got != want {
		t.Fatalf("fileset = %s, want %s", got, want)
	}
}

func TestFilesetSingleStarDoesNotRecurse(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, "a.ogg", "sub/b.ogg")
	v, err := evalFn(t, `fileset("`+filepath.ToSlash(dir)+`", "*.ogg")`)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(setStrings(t, v), ","); got != "a.ogg" {
		t.Fatalf("fileset = %s", got)
	}
}

func TestFilesetNoMatchesIsEmptySet(t *testing.T) {
	v, err := evalFn(t, `fileset("`+filepath.ToSlash(t.TempDir())+`", "*.png")`)
	if err != nil {
		t.Fatal(err)
	}
	if v.LengthInt() != 0 || !v.Type().Equals(cty.Set(cty.String)) {
		t.Fatalf("want empty set(string), got %#v", v)
	}
}

func TestFilesetMissingDirIsError(t *testing.T) {
	_, err := evalFn(t, `fileset("`+filepath.ToSlash(filepath.Join(t.TempDir(), "nope"))+`", "*.png")`)
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("missing dir must error, got %v", err)
	}
}

func TestFileReadsUTF8AndRejectsBinary(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "ids.json")
	os.WriteFile(good, []byte(`{"ui/gem.png": "123"}`), 0o644)
	v, err := evalFn(t, `jsondecode(file("`+filepath.ToSlash(good)+`"))["ui/gem.png"]`)
	if err != nil {
		t.Fatal(err)
	}
	if v.AsString() != "123" {
		t.Fatalf("got %#v", v)
	}
	bad := filepath.Join(dir, "bin")
	os.WriteFile(bad, []byte{0xff, 0xfe, 0x00}, 0o644)
	if _, err := evalFn(t, `file("`+filepath.ToSlash(bad)+`")`); err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("non-UTF-8 file must error, got %v", err)
	}
}

func TestPathAndStdlibFunctions(t *testing.T) {
	cases := map[string]string{
		`basename("ui/icons/gem.png")`:             "gem.png",
		`dirname("ui/icons/gem.png")`:              "ui/icons",
		`dirname("gem.png")`:                       ".",
		`trimsuffix("gem.png", ".png")`:            "gem",
		`trimprefix("sprite/gem", "sprite/")`:      "gem",
		`join(",", tolist(toset(["b", "a"])))`:     "a,b",
		`format("%s-%d", "x", 2)`:                  "x-2",
		`lower(upper("Ab"))`:                       "ab",
		`replace("a-b", "-", "_")`:                 "a_b",
		`lookup({a = "1"}, "b", "dflt")`:           "dflt",
		`jsonencode(length(concat(["a"], ["b"])))`: "2",
	}
	for src, want := range cases {
		v, err := evalFn(t, src)
		if err != nil {
			t.Errorf("%s: %v", src, err)
			continue
		}
		if v.AsString() != want {
			t.Errorf("%s = %q, want %q", src, v.AsString(), want)
		}
	}
	if v, err := evalFn(t, `contains(toset(["a"]), "a")`); err != nil || !v.True() {
		t.Errorf("contains over a set = %#v, %v", v, err)
	}
}
