package plan

import (
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/provider"
)

func replacePlan() *Plan {
	return &Plan{Changes: []Change{{
		Address: "roblox_asset.icon", Type: "roblox_asset", Name: "icon", ID: "111",
		Action:  provider.ActionReplace,
		Desired: provider.Attributes{"display_name": cty.StringVal("Icon"), "file": cty.StringVal("icon.png")},
		AttrDiffs: []AttrDiff{
			{Name: "content_hash", Old: cty.StringVal("aaa"), New: cty.StringVal("bbb")},
		},
		Replace: []string{"content_hash"},
	}}}
}

func TestRenderTextShowsReplace(t *testing.T) {
	out := replacePlan().RenderText(false)
	for _, want := range []string{
		"-/+ roblox_asset.icon (replace)\n",
		"    display_name = Icon\n",
		"    content_hash: aaa => bbb  # forces replacement\n",
		"Plan: 0 to import, 1 to add, 0 to change, 1 to destroy.",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestJSONShowsReplace(t *testing.T) {
	pl := replacePlan()
	jc := pl.JSONChanges()
	if jc[0].Action != "replace" || len(jc[0].ForcesReplacement) != 1 || jc[0].ForcesReplacement[0] != "content_hash" {
		t.Fatalf("json change = %+v", jc[0])
	}
	if s := pl.Summary(); s.Add != 1 || s.Destroy != 1 || s.Replace != 1 || s.Change != 0 {
		t.Fatalf("summary = %+v", s)
	}
	if !pl.HasChanges() {
		t.Fatal("a replace is a change")
	}
}

func TestPreventDestroyBlocksReplace(t *testing.T) {
	pl := replacePlan()
	pl.PreventDestroy = []string{"roblox_asset.icon"}
	err := pl.CheckPreventDestroy()
	if err == nil || !strings.Contains(err.Error(), "replaced") || !strings.Contains(err.Error(), "content_hash") {
		t.Fatalf("want a prevent_destroy error naming the forcing trigger, got %v", err)
	}
}

// A replace is neither a plain delete nor a plain create, so it must not trip
// the key-rename or empty-for_each warnings.
func TestReplaceDoesNotTriggerRenameWarning(t *testing.T) {
	pl := &Plan{Changes: []Change{
		{Address: `roblox_asset.icons["a"]`, Type: "roblox_asset", Name: "icons", Key: "a", Action: provider.ActionReplace, Replace: []string{"content_hash"}},
		{Address: `roblox_asset.icons["b"]`, Type: "roblox_asset", Name: "icons", Key: "b", Action: provider.ActionReplace, Replace: []string{"content_hash"}},
	}, EmptyForEach: []string{"roblox_asset.icons"}}
	if w := pl.Warnings(); len(w) != 0 {
		t.Fatalf("unexpected warnings: %v", w)
	}
}

func TestRenderImportThenReplace(t *testing.T) {
	pl := &Plan{Changes: []Change{{
		Address: "mock_replace.a", ImportID: "r-1", Action: provider.ActionReplace,
		AttrDiffs: []AttrDiff{{Name: "kind", Old: cty.StringVal("x"), New: cty.StringVal("y")}},
		Replace:   []string{"kind"},
	}}}
	out := pl.RenderText(false)
	if !strings.Contains(out, "mock_replace.a will be imported (id r-1), then replaced") ||
		!strings.Contains(out, "kind: x => y  # forces replacement") ||
		!strings.Contains(out, "Plan: 1 to import, 1 to add, 0 to change, 1 to destroy.") {
		t.Fatalf("got:\n%s", out)
	}
}
