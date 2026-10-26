package resources

import (
	"context"
	"errors"
	"fmt"
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
	creates   int
	store     map[string]opencloud.Asset
	getErr    error
	deleteErr error
}

func (f *fakeAsset) CreateAndAwait(_ context.Context, req opencloud.AssetCreate, _, _ string, data []byte) (opencloud.Asset, error) {
	f.created = req
	f.gotFile = data
	id := fmt.Sprint(555 + f.creates)
	f.creates++
	a := opencloud.Asset{ID: id, DisplayName: req.DisplayName, Description: req.Description, AssetType: req.AssetType}
	if f.store == nil {
		f.store = map[string]opencloud.Asset{}
	}
	f.store[id] = a
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
	img := provider.Attributes{"asset_type": cty.StringVal("Image")}
	model := provider.Attributes{"asset_type": cty.StringVal("Model")}
	cases := []struct {
		name            string
		desired, actual provider.Attributes
		content         bool // content_hash forces replacement
	}{
		{"image", provider.Attributes{"file": cty.StringVal("a.png")}, img, true},
		{"decal", provider.Attributes{"file": cty.StringVal("a.png"), "asset_type": cty.StringVal("Decal")}, provider.Attributes{"asset_type": cty.StringVal("Decal")}, true},
		{"audio", provider.Attributes{"file": cty.StringVal("a.ogg")}, provider.Attributes{"asset_type": cty.StringVal("Audio")}, true},
		{"model", provider.Attributes{"file": cty.StringVal("a.fbx"), "asset_type": cty.StringVal("Model")}, model, false},
		{"model by extension", provider.Attributes{"file": cty.StringVal("a.rbxmx")}, model, false},
		// The CURRENT type decides: an Image whose config now says Model is an
		// Image remotely, which can't take content.
		{"image becoming model", provider.Attributes{"file": cty.StringVal("a.fbx")}, img, true},
		{"model becoming image", provider.Attributes{"file": cty.StringVal("a.png")}, model, false},
		{"no actual type", provider.Attributes{"file": cty.StringVal("a.png")}, provider.Attributes{}, true},
	}
	for _, c := range cases {
		got := map[string]bool{}
		for _, k := range r.RequiresReplace(c.desired, c.actual) {
			got[k] = true
		}
		if !got["asset_type"] {
			t.Errorf("%s: asset_type must always force replacement (Roblox can't change a type)", c.name)
		}
		if got["content_hash"] != c.content {
			t.Errorf("%s: content_hash forcing = %v, want %v", c.name, got["content_hash"], c.content)
		}
	}
	if !Asset(&fakeAsset{}).(provider.Replacer).ReplaceAbandons() {
		t.Fatal("a replaced asset must be left in place, not archived")
	}
}

func TestAssetNormalizeFillsDefaultType(t *testing.T) {
	n := Asset(&fakeAsset{}).(provider.Normalizer)
	for file, want := range map[string]string{"a.png": "Image", "a.fbx": "Model", "a.ogg": "Audio"} {
		in := provider.Attributes{"file": cty.StringVal(file)}
		out, err := n.Normalize(in)
		if err != nil {
			t.Fatal(err)
		}
		if out["asset_type"].AsString() != want {
			t.Errorf("%s: asset_type = %v, want %s", file, out["asset_type"], want)
		}
		if _, mutated := in["asset_type"]; mutated {
			t.Fatal("Normalize must not mutate its input")
		}
	}
	out, _ := n.Normalize(provider.Attributes{"file": cty.StringVal("a.png"), "asset_type": cty.StringVal("Decal")})
	if out["asset_type"].AsString() != "Decal" {
		t.Fatal("a declared asset_type must be kept")
	}
}

func TestAssetTriggersMissingFileIsUnavailable(t *testing.T) {
	_, err := Asset(&fakeAsset{}).(provider.Triggerable).Triggers(provider.Attributes{
		"file": cty.StringVal(filepath.Join(t.TempDir(), "gone.png"))})
	var unavailable *provider.TriggerUnavailableError
	if !errors.As(err, &unavailable) {
		t.Fatalf("a missing file must be a TriggerUnavailableError, got %v", err)
	}
}

func modelAttrs(fp string) provider.Attributes {
	return provider.Attributes{
		"file": cty.StringVal(fp), "display_name": cty.StringVal("Tree"),
		"description": cty.StringVal("a tree"), "asset_type": cty.StringVal("Model"),
	}
}

func TestAssetModelMetadataEditDoesNotReupload(t *testing.T) {
	fp := writeFile(t, "tree.fbx", "FBXDATA")
	sum, _ := fileHash(fp)
	f := &fakeAsset{store: map[string]opencloud.Asset{"9": {ID: "9"}}}
	u := Asset(f).(provider.TriggerAwareUpdater)
	if _, err := u.UpdateWithTriggers(context.Background(), "9", modelAttrs(fp),
		map[string]cty.Value{"content_hash": cty.StringVal(sum)}); err != nil {
		t.Fatal(err)
	}
	if len(f.content) != 0 || f.updates != 1 {
		t.Fatalf("unchanged content must not be re-uploaded (content=%v updates=%d)", f.content, f.updates)
	}
	if _, err := u.UpdateWithTriggers(context.Background(), "9", modelAttrs(fp),
		map[string]cty.Value{"content_hash": cty.StringVal("old")}); err != nil {
		t.Fatal(err)
	}
	if string(f.content["9"]) != "FBXDATA" {
		t.Fatal("changed content must be uploaded in place")
	}
}

func TestAssetModelUpdateWithMissingFileIsMetadataOnly(t *testing.T) {
	f := &fakeAsset{store: map[string]opencloud.Asset{"9": {ID: "9"}}}
	if _, err := Asset(f).(provider.TriggerAwareUpdater).UpdateWithTriggers(context.Background(), "9",
		modelAttrs(filepath.Join(t.TempDir(), "gone.fbx")), map[string]cty.Value{"content_hash": cty.StringVal("x")}); err != nil {
		t.Fatal(err)
	}
	if f.updates != 1 || len(f.content) != 0 {
		t.Fatalf("missing file: want a metadata-only update (updates=%d content=%v)", f.updates, f.content)
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
