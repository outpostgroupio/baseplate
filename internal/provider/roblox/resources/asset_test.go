package resources

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/roblox/opencloud"
)

type fakeAsset struct {
	created opencloud.AssetCreate
	gotFile []byte
	// content records UpdateContent calls (asset id -> uploaded bytes).
	content   map[string][]byte
	updates   int
	store     map[string]opencloud.Asset
	getErr    error
	deleteErr error
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
	if f.getErr != nil {
		return opencloud.Asset{}, f.getErr
	}
	return f.store[id], nil
}
func (f *fakeAsset) Update(_ context.Context, a opencloud.Asset) (opencloud.Asset, error) {
	f.updates++
	f.store[a.ID] = a
	return a, nil
}
func (f *fakeAsset) UpdateContent(_ context.Context, a opencloud.Asset, _, _ string, data []byte) (opencloud.Asset, error) {
	if f.content == nil {
		f.content = map[string][]byte{}
	}
	f.content[a.ID] = data
	f.store[a.ID] = a
	return a, nil
}
func (f *fakeAsset) Delete(_ context.Context, id string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	delete(f.store, id)
	return nil
}

func TestAssetDeleteUnmanagesNonArchivable(t *testing.T) {
	r := Asset(&fakeAsset{deleteErr: &opencloud.APIError{
		Status: 400, Message: "Asset 1 is not an archivable asset type."}})
	if err := r.Delete(context.Background(), "1"); err != nil {
		t.Fatalf("destroy of a non-archivable asset must succeed (unmanage), got %v", err)
	}
}

func TestAssetDeletePropagatesOtherErrors(t *testing.T) {
	r := Asset(&fakeAsset{deleteErr: &opencloud.APIError{Status: 500, Message: "boom"}})
	if err := r.Delete(context.Background(), "1"); err == nil {
		t.Fatal("a delete error unrelated to archivability must propagate")
	}
}

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

func TestAssetReadMapsNotFound(t *testing.T) {
	r := Asset(&fakeAsset{getErr: &opencloud.APIError{Status: 404}})
	if _, err := r.Read(context.Background(), "555"); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("want provider.ErrNotFound, got %v", err)
	}
}

func TestAssetAudioTypeInferredFromExtension(t *testing.T) {
	path := filepath.Join(t.TempDir(), "click.ogg")
	if err := os.WriteFile(path, []byte("OggS"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := &fakeAsset{}
	_, attrs, err := Asset(f).Create(context.Background(), provider.Attributes{
		"file":            cty.StringVal(path),
		"display_name":    cty.StringVal("Click"),
		"description":     cty.StringVal("UI click"),
		"creator_user_id": cty.NumberIntVal(42),
	})
	if err != nil {
		t.Fatal(err)
	}
	if f.created.AssetType != "Audio" || attrs["asset_type"].AsString() != "Audio" {
		t.Fatalf("asset type = %q / %v, want Audio", f.created.AssetType, attrs["asset_type"])
	}
	if got := contentType(path); got != "audio/ogg" {
		t.Fatalf("content type = %q", got)
	}
}

func writeFile(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAssetContentHashTrigger(t *testing.T) {
	fp := writeFile(t, "gem.png", "v1")
	r := Asset(&fakeAsset{}).(provider.Triggerable)
	t1, err := r.Triggers(provider.Attributes{"file": cty.StringVal(fp)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fp, []byte("v2"), 0o600); err != nil {
		t.Fatal(err)
	}
	t2, err := r.Triggers(provider.Attributes{"file": cty.StringVal(fp)})
	if err != nil {
		t.Fatal(err)
	}
	if t1["content_hash"].RawEquals(t2["content_hash"]) {
		t.Fatal("content_hash must change when the file content changes")
	}
	// Same bytes at another path: same hash (a rename alone does nothing).
	other := writeFile(t, "moved.png", "v2")
	t3, _ := r.Triggers(provider.Attributes{"file": cty.StringVal(other)})
	if !t2["content_hash"].RawEquals(t3["content_hash"]) {
		t.Fatal("content_hash must depend on content only, not the path")
	}
}

func TestAssetRequiresReplace(t *testing.T) {
	r := Asset(&fakeAsset{}).(provider.Replacer)
	cases := []struct {
		name  string
		attrs provider.Attributes
		want  bool // content_hash forces replacement
	}{
		{"image default", provider.Attributes{"file": cty.StringVal("a.png")}, true},
		{"decal", provider.Attributes{"file": cty.StringVal("a.png"), "asset_type": cty.StringVal("Decal")}, true},
		{"audio default", provider.Attributes{"file": cty.StringVal("a.ogg")}, true},
		{"model declared", provider.Attributes{"file": cty.StringVal("a.fbx"), "asset_type": cty.StringVal("Model")}, false},
		{"model by extension", provider.Attributes{"file": cty.StringVal("a.rbxmx")}, false},
	}
	for _, c := range cases {
		got := r.RequiresReplace(c.attrs)
		if (len(got) == 1 && got[0] == "content_hash") != c.want {
			t.Errorf("%s: RequiresReplace = %v, want content_hash forcing = %v", c.name, got, c.want)
		}
	}
}

func TestAssetModelUpdateUploadsContentInPlace(t *testing.T) {
	fp := writeFile(t, "tree.fbx", "FBXDATA")
	f := &fakeAsset{store: map[string]opencloud.Asset{"9": {ID: "9"}}}
	_, err := Asset(f).Update(context.Background(), "9", provider.Attributes{
		"file":         cty.StringVal(fp),
		"display_name": cty.StringVal("Tree"),
		"description":  cty.StringVal("a tree"),
		"asset_type":   cty.StringVal("Model"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(f.content["9"]) != "FBXDATA" {
		t.Fatalf("a Model update must upload the file content in place, got %q", f.content["9"])
	}
	if f.updates != 0 {
		t.Fatal("a Model update must go through UpdateContent, not the metadata-only Update")
	}
	if got := f.store["9"]; got.DisplayName != "Tree" || got.AssetType != "Model" {
		t.Fatalf("metadata not sent with content: %+v", got)
	}
}

func TestAssetImageUpdateIsMetadataOnly(t *testing.T) {
	fp := writeFile(t, "gem.png", "PNG")
	f := &fakeAsset{store: map[string]opencloud.Asset{"9": {ID: "9"}}}
	if _, err := Asset(f).Update(context.Background(), "9", provider.Attributes{
		"file":         cty.StringVal(fp),
		"display_name": cty.StringVal("Gem"),
		"description":  cty.StringVal("x"),
	}); err != nil {
		t.Fatal(err)
	}
	if f.updates != 1 || len(f.content) != 0 {
		t.Fatalf("an Image update must be metadata-only (updates=%d, content=%v)", f.updates, f.content)
	}
}

func TestAssetModelContentTypes(t *testing.T) {
	for ext, want := range map[string]string{
		".fbx": "model/fbx", ".gltf": "model/gltf+json", ".glb": "model/gltf-binary",
		".rbxm": "model/x-rbxm", ".rbxmx": "model/x-rbxm",
	} {
		if got := contentType("m" + ext); got != want {
			t.Errorf("contentType(%s) = %q, want %q", ext, got, want)
		}
		if got := defaultAssetType("m" + ext); got != "Model" {
			t.Errorf("defaultAssetType(%s) = %q, want Model", ext, got)
		}
	}
}
