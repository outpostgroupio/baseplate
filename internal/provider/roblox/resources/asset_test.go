package resources

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroup/baseplate/internal/provider"
	"github.com/outpostgroup/baseplate/internal/provider/roblox/opencloud"
)

type fakeAsset struct {
	created opencloud.AssetCreate
	gotFile []byte
	store   map[string]opencloud.Asset
}

func (f *fakeAsset) CreateAndAwait(_ context.Context, req opencloud.AssetCreate, _, _ string, data []byte) (opencloud.Asset, error) {
	f.created = req
	f.gotFile = data
	a := opencloud.Asset{ID: "555", DisplayName: req.DisplayName, Description: req.Description, AssetType: req.AssetType}
	if f.store == nil {
		f.store = map[string]opencloud.Asset{}
	}
	f.store["555"] = a
	return a, nil
}
func (f *fakeAsset) Get(_ context.Context, id string) (opencloud.Asset, error) {
	return f.store[id], nil
}
func (f *fakeAsset) Update(_ context.Context, a opencloud.Asset) (opencloud.Asset, error) {
	f.store[a.ID] = a
	return a, nil
}
func (f *fakeAsset) Delete(_ context.Context, id string) error { delete(f.store, id); return nil }

func TestAssetCreateReadsFileAndUploads(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "gem.png")
	if err := os.WriteFile(fp, []byte("PNGBYTES"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := &fakeAsset{}
	r := Asset(f)

	id, attrs, err := r.Create(context.Background(), provider.Attributes{
		"file":            cty.StringVal(fp),
		"display_name":    cty.StringVal("Gem"),
		"description":     cty.StringVal("a gem"),
		"creator_user_id": cty.NumberIntVal(42),
	})
	if err != nil {
		t.Fatal(err)
	}
	if id != "555" {
		t.Fatalf("id = %q", id)
	}
	if string(f.gotFile) != "PNGBYTES" {
		t.Fatalf("file bytes = %q", f.gotFile)
	}
	if f.created.CreatorUserID != 42 || f.created.DisplayName != "Gem" || f.created.AssetType != "Image" {
		t.Fatalf("create req = %+v", f.created)
	}
	if attrs["file"].AsString() != fp {
		t.Fatalf("state should record file path, got %+v", attrs)
	}
}

func TestAssetCreateRejectsMissingCreator(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "x.png")
	os.WriteFile(fp, []byte("x"), 0o600)
	r := Asset(&fakeAsset{})
	_, _, err := r.Create(context.Background(), provider.Attributes{
		"file":         cty.StringVal(fp),
		"display_name": cty.StringVal("x"),
		"description":  cty.StringVal("x"),
	})
	if err == nil {
		t.Fatal("expected error when neither creator id is set")
	}
}
