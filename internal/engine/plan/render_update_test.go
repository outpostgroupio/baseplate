package plan

import (
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/provider"
)

// An update shows only what changes, old => new, and counts the rest.
func TestRenderUpdateShowsOldAndNewOfChangedAttrsOnly(t *testing.T) {
	pl := &Plan{Changes: []Change{{
		Address: "roblox_experience.main", Type: "roblox_experience", ID: "1", Action: provider.ActionUpdate,
		Desired: provider.Attributes{
			"universe_id":     cty.StringVal("1"),
			"vr_enabled":      cty.False,
			"desktop_enabled": cty.True,
		},
		AttrDiffs: []AttrDiff{{Name: "vr_enabled", Old: cty.True, New: cty.False}},
	}}}
	got := pl.RenderText(false)
	if !strings.Contains(got, "vr_enabled: true => false") {
		t.Fatalf("update must show old => new:\n%s", got)
	}
	if strings.Contains(got, "desktop_enabled") || strings.Contains(got, "universe_id") {
		t.Fatalf("unchanged attributes must be hidden:\n%s", got)
	}
	if !strings.Contains(got, "# (2 unchanged attribute(s) hidden)") {
		t.Fatalf("update must count the hidden attributes:\n%s", got)
	}
}

func TestRenderUpdateRedactsSensitive(t *testing.T) {
	pl := &Plan{Changes: []Change{{
		Address: "mock_secret.a", Type: "mock_secret", ID: "1", Action: provider.ActionUpdate,
		Desired:   provider.Attributes{"value": cty.StringVal("new-secret")},
		AttrDiffs: []AttrDiff{{Name: "value", Old: cty.StringVal("old-secret"), New: cty.StringVal("new-secret")}},
		Sensitive: []string{"value"},
	}}}
	got := pl.RenderText(false)
	if !strings.Contains(got, "value: (sensitive value) => (sensitive value)") {
		t.Fatalf("sensitive values must be redacted:\n%s", got)
	}
	if strings.Contains(got, "old-secret") || strings.Contains(got, "new-secret") {
		t.Fatalf("sensitive values leaked:\n%s", got)
	}
}

func TestCountAndAppliedLeaveOutUnchanged(t *testing.T) {
	pl := &Plan{
		Moves: []Move{{From: "mock_thing.old", To: "mock_thing.new"}},
		Changes: []Change{
			{Address: "mock_thing.a", Action: provider.ActionNoop},
			{Address: "mock_thing.b", Action: provider.ActionNoop},
			{Address: "mock_thing.c", Action: provider.ActionUpdate},
			{Address: "mock_thing.d", Action: provider.ActionCreate},
			{Address: "mock_thing.e", Action: provider.ActionNoop, ImportID: "5"},
		},
	}
	if got := pl.Count(); got != 3 {
		t.Fatalf("Count() = %d, want 3 (unchanged resources aren't changes)", got)
	}
	if got, want := pl.Summary().Applied(), "1 imported, 1 added, 1 changed, 0 destroyed, 1 moved"; got != want {
		t.Fatalf("Applied() = %q, want %q", got, want)
	}
}

// A missing trigger input is reported once per resource block, not once per
// instance (a for_each over hundreds of files CI doesn't have).
func TestUnavailableWarningsGroupByResource(t *testing.T) {
	pl := &Plan{Changes: []Change{
		{Address: `roblox_asset.maps["a"]`, Type: "roblox_asset", Name: "maps", Key: "a", Unavailable: `file "maps/a.png" not found`},
		{Address: `roblox_asset.maps["b"]`, Type: "roblox_asset", Name: "maps", Key: "b", Unavailable: `file "maps/b.png" not found`},
		{Address: `roblox_asset.maps["c"]`, Type: "roblox_asset", Name: "maps", Key: "c", Unavailable: `file "maps/c.png" not found`},
		{Address: "roblox_asset.logo", Type: "roblox_asset", Name: "logo", Unavailable: `file "logo.png" not found`},
	}}
	want := []string{
		`roblox_asset.maps: inputs are missing for 3 instances (the first, roblox_asset.maps["a"]: file "maps/a.png" not found); content changes can't be detected for them until they are available.`,
		`roblox_asset.logo: file "logo.png" not found; content changes can't be detected until it is available.`,
	}
	got := pl.Warnings()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
