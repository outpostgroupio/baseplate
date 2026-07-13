package resources

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/roblox/opencloud"
)

type fakeBadge struct {
	created     opencloud.BadgeCreate
	updated     opencloud.BadgeUpdate
	iconUploads int
	getEnabled  bool
	notFound    bool
}

func (f *fakeBadge) Create(_ context.Context, _ string, b opencloud.BadgeCreate) (opencloud.Badge, error) {
	f.created = b
	return opencloud.Badge{ID: "555", Name: b.Name, Description: b.Description, Enabled: b.IsActive}, nil
}
func (f *fakeBadge) Get(_ context.Context, _, _ string) (opencloud.Badge, error) {
	if f.notFound {
		return opencloud.Badge{}, opencloud.SentinelNotFound()
	}
	return opencloud.Badge{ID: "555", Name: "Welcome!", Description: "d", Enabled: f.getEnabled}, nil
}
func (f *fakeBadge) Update(_ context.Context, _ string, b opencloud.BadgeUpdate) error {
	f.updated = b
	return nil
}
func (f *fakeBadge) UploadIcon(_ context.Context, _ string, _ []byte, _, _ string) error {
	f.iconUploads++
	return nil
}

func TestBadgeCreateReturnsScopedID(t *testing.T) {
	fp := filepath.Join(t.TempDir(), "icon.png")
	if err := os.WriteFile(fp, []byte("PNG"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := &fakeBadge{}
	id, attrs, err := Badge(f).Create(context.Background(), provider.Attributes{
		"universe_id": cty.StringVal("42"),
		"name":        cty.StringVal("Welcome!"),
		"description": cty.StringVal("d"),
		"icon_file":   cty.StringVal(fp),
	})
	if err != nil {
		t.Fatal(err)
	}
	if id != "42/555" {
		t.Fatalf("id = %q, want 42/555", id)
	}
	if f.created.PaymentSourceType != 1 || f.created.ExpectedCost != 0 || !f.created.IsActive {
		t.Fatalf("create defaults wrong: %+v", f.created)
	}
	if len(f.created.IconData) == 0 {
		t.Fatal("icon bytes not read")
	}
	if !attrs["name"].RawEquals(cty.StringVal("Welcome!")) {
		t.Fatalf("state name = %#v", attrs["name"])
	}
}

func TestBadgeReadMapsFields(t *testing.T) {
	attrs, err := Badge(&fakeBadge{getEnabled: true}).Read(context.Background(), "42/555")
	if err != nil {
		t.Fatal(err)
	}
	if !attrs["enabled"].RawEquals(cty.True) || !attrs["name"].RawEquals(cty.StringVal("Welcome!")) {
		t.Fatalf("read attrs = %#v", attrs)
	}
}

func TestBadgeReadNotFound(t *testing.T) {
	_, err := Badge(&fakeBadge{notFound: true}).Read(context.Background(), "42/555")
	if err != provider.ErrNotFound {
		t.Fatalf("err = %v, want provider.ErrNotFound", err)
	}
}

func TestBadgeUpdateReuploadsIcon(t *testing.T) {
	fp := filepath.Join(t.TempDir(), "icon.png")
	if err := os.WriteFile(fp, []byte("PNG"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := &fakeBadge{}
	_, err := Badge(f).Update(context.Background(), "42/555", provider.Attributes{
		"name":      cty.StringVal("Welcome!"),
		"enabled":   cty.False,
		"icon_file": cty.StringVal(fp),
	})
	if err != nil {
		t.Fatal(err)
	}
	if f.updated.Enabled != false || f.updated.Name != "Welcome!" {
		t.Fatalf("update body wrong: %+v", f.updated)
	}
	if f.iconUploads != 1 {
		t.Fatalf("icon uploads = %d, want 1", f.iconUploads)
	}
}

func TestBadgeDeleteDisables(t *testing.T) {
	f := &fakeBadge{}
	if err := Badge(f).Delete(context.Background(), "42/555"); err != nil {
		t.Fatal(err)
	}
	if f.updated.Enabled != false {
		t.Fatal("delete must PATCH enabled=false")
	}
}

func TestBadgeTriggersHashesIcon(t *testing.T) {
	fp := filepath.Join(t.TempDir(), "icon.png")
	if err := os.WriteFile(fp, []byte("A"), 0o600); err != nil {
		t.Fatal(err)
	}
	tr, ok := Badge(&fakeBadge{}).(provider.Triggerable)
	if !ok {
		t.Fatal("roblox_badge must implement provider.Triggerable")
	}
	desired := provider.Attributes{"name": cty.StringVal("x"), "icon_file": cty.StringVal(fp)}
	t1, err := tr.Triggers(desired)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fp, []byte("B"), 0o600); err != nil {
		t.Fatal(err)
	}
	t2, _ := tr.Triggers(desired)
	if t1["content_hash"].RawEquals(t2["content_hash"]) {
		t.Fatal("content_hash must change when the icon file changes")
	}
}
