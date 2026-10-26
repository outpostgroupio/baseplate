package resources

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/provider"
)

func badgeAttrs(icon string) provider.Attributes {
	return provider.Attributes{
		"name": cty.StringVal("Welcome!"), "enabled": cty.True, "icon_file": cty.StringVal(icon),
	}
}

// A name-only edit must not re-upload (and re-moderate) an unchanged icon.
func TestBadgeUpdateSkipsUnchangedIcon(t *testing.T) {
	fp := filepath.Join(t.TempDir(), "icon.png")
	if err := os.WriteFile(fp, []byte("PNG"), 0o600); err != nil {
		t.Fatal(err)
	}
	b := Badge(&fakeBadge{}).(provider.Triggerable)
	trig, err := b.Triggers(badgeAttrs(fp))
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeBadge{}
	u := Badge(f).(provider.TriggerAwareUpdater)
	if _, err := u.UpdateWithTriggers(context.Background(), "42/555", badgeAttrs(fp), trig); err != nil {
		t.Fatal(err)
	}
	if f.iconUploads != 0 || f.updated.Name != "Welcome!" {
		t.Fatalf("unchanged icon: uploads=%d update=%+v", f.iconUploads, f.updated)
	}
	if _, err := u.UpdateWithTriggers(context.Background(), "42/555", badgeAttrs(fp),
		map[string]cty.Value{"content_hash": cty.StringVal("old")}); err != nil {
		t.Fatal(err)
	}
	if f.iconUploads != 1 {
		t.Fatalf("changed icon must be uploaded, uploads=%d", f.iconUploads)
	}
}

func TestMissingFileTriggersAreUnavailable(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "gone")
	var unavailable *provider.TriggerUnavailableError

	if _, err := Badge(&fakeBadge{}).(provider.Triggerable).Triggers(badgeAttrs(gone)); !errors.As(err, &unavailable) {
		t.Fatalf("badge: want TriggerUnavailableError, got %v", err)
	}
	trig, err := Place(nil).(provider.Triggerable).Triggers(provider.Attributes{"file": cty.StringVal(gone + ".rbxl")})
	if !errors.As(err, &unavailable) {
		t.Fatalf("place: want TriggerUnavailableError, got %v", err)
	}
	if _, ok := trig["version_type"]; !ok {
		t.Fatal("place: the triggers that don't need the file must still be reported")
	}
	if _, ok := trig["content_hash"]; ok {
		t.Fatal("place: no content_hash without the file")
	}
	// A missing icon file skips the upload but still updates the metadata.
	f := &fakeBadge{}
	if _, err := Badge(f).(provider.TriggerAwareUpdater).UpdateWithTriggers(context.Background(), "42/555", badgeAttrs(gone), nil); err != nil {
		t.Fatal(err)
	}
	if f.iconUploads != 0 || f.updated.Name != "Welcome!" {
		t.Fatalf("missing icon: uploads=%d update=%+v", f.iconUploads, f.updated)
	}
}
