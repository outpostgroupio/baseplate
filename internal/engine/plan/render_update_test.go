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
