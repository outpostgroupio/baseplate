package resources

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/provider"
)

type fakePlace struct {
	universeID, placeID, versionType, contentType string
	data                                          []byte
	version                                       int64
	calls                                         int
}

func (f *fakePlace) Publish(_ context.Context, u, p, vt, ct string, data []byte) (int64, error) {
	f.universeID, f.placeID, f.versionType, f.contentType, f.data = u, p, vt, ct, data
	f.calls++
	if f.version == 0 {
		f.version = 1
	}
	return f.version, nil
}

func TestPlaceCreatePublishes(t *testing.T) {
	fp := filepath.Join(t.TempDir(), "game.rbxlx")
	if err := os.WriteFile(fp, []byte("<roblox></roblox>"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := &fakePlace{version: 3}
	r := Place(f)
	id, attrs, err := r.Create(context.Background(), provider.Attributes{
		"universe_id": cty.StringVal("100"),
		"place_id":    cty.StringVal("200"),
		"file":        cty.StringVal(fp),
	})
	if err != nil {
		t.Fatal(err)
	}
	if id != "100/200" {
		t.Fatalf("id = %q, want 100/200", id)
	}
	if f.versionType != "Published" {
		t.Fatalf("versionType = %q, want default Published", f.versionType)
	}
	if f.contentType != "application/xml" {
		t.Fatalf("content-type = %q, want application/xml for .rbxlx", f.contentType)
	}
	if !attrs["version_number"].RawEquals(cty.NumberIntVal(3)) {
		t.Fatalf("version_number = %#v", attrs["version_number"])
	}
}

func TestPlaceTriggersHashChangesWithContent(t *testing.T) {
	fp := filepath.Join(t.TempDir(), "game.rbxlx")
	if err := os.WriteFile(fp, []byte("A"), 0o600); err != nil {
		t.Fatal(err)
	}
	tr, ok := Place(&fakePlace{}).(provider.Triggerable)
	if !ok {
		t.Fatal("roblox_place must implement provider.Triggerable")
	}
	desired := provider.Attributes{
		"universe_id": cty.StringVal("1"), "place_id": cty.StringVal("2"), "file": cty.StringVal(fp)}
	t1, err := tr.Triggers(desired)
	if err != nil {
		t.Fatal(err)
	}
	if !t1["version_type"].RawEquals(cty.StringVal("Published")) {
		t.Fatalf("version_type trigger = %#v", t1["version_type"])
	}
	if err := os.WriteFile(fp, []byte("B"), 0o600); err != nil {
		t.Fatal(err)
	}
	t2, err := tr.Triggers(desired)
	if err != nil {
		t.Fatal(err)
	}
	if t1["content_hash"].RawEquals(t2["content_hash"]) {
		t.Fatal("content_hash must change when the file content changes")
	}
}

func TestPlaceDeleteIsNoop(t *testing.T) {
	if err := Place(&fakePlace{}).Delete(context.Background(), "1/2"); err != nil {
		t.Fatalf("delete must be a no-op, got %v", err)
	}
}
