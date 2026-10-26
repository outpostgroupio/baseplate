package resources

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/roblox/opencloud"
)

type asset struct{ ac opencloud.AssetClient }

// Asset returns the roblox_asset resource backed by an AssetClient.
func Asset(ac opencloud.AssetClient) provider.Resource { return &asset{ac: ac} }

func (a *asset) Type() string { return "roblox_asset" }

func (a *asset) Doc() provider.ResourceDoc {
	return provider.ResourceDoc{
		Summary: "Uploads a user- or group-owned image, decal, audio or model asset.",
		Attributes: map[string]string{
			"file":             "Path to the file to upload: `.png`/`.jpg`/`.bmp`/`.tga` for images and decals, `.mp3`/`.ogg`/`.wav`/`.flac` for audio, `.fbx`/`.gltf`/`.glb`/`.rbxm`/`.rbxmx` for models. A change to the file's content is detected by hash: a Model takes the new content in place (same id); any other type is replaced (a new upload with a new id).",
			"display_name":     "Asset name.",
			"description":      "Asset description.",
			"asset_type":       "Asset type: `Image`, `Decal`, `Audio` or `Model`. Defaults to `Audio` for audio files, `Model` for model files and `Image` otherwise. Use `Image` for anything the game shows in an `ImageLabel` or `Decal`. Roblox can't change an existing asset's type.",
			"creator_user_id":  "Owning user id (set exactly one of creator_user_id/creator_group_id).",
			"creator_group_id": "Owning group id (set exactly one of creator_user_id/creator_group_id).",
		},
		Notes:        "The `id` output is the `assetId` (a numeric string). For an `Image` asset (the default for image files) that id is the texture id: use it directly as `rbxassetid://<id>` in `ImageLabel.Image` or `Decal.Texture`. A `Decal` asset's id is not a texture id. Each Decal wraps a separate Image asset that Roblox creates alongside it, owned by the same creator. To adopt assets from a Decal-based uploader, import those underlying image ids, not the decal ids (see the many-assets how-to). When the content of `file` changes (its `content_hash` trigger), plan shows `-/+ (replace)` for an Image, Decal or Audio asset: apply uploads a new asset, which gets a new id, and then drops the old one (unmanaged, as on destroy). Open Cloud only takes new content for an existing Model asset, so a Model is updated in place instead and keeps its id (each content update is a new asset version; metadata edits to a Model re-send its content too). Renaming or moving the file without changing its bytes does nothing. References to the asset's `id` and `baseplate show --json` pick up the new id in the same apply. An imported asset records the current file's hash as its baseline, so adopting existing assets never re-uploads them; the same goes for assets created by Baseplate versions without the hash, on their next apply. `lifecycle { prevent_destroy = true }` blocks a replace. Audio is limited to 7 minutes and 20 MB, and Roblox caps audio uploads per month (10, or 100 for ID-verified accounts). Image, Decal and Model assets are not archivable, so `destroy` (and the old object of a replace) unmanages the asset in place (drops it from state, leaves it uploaded).",
		Scopes:       []string{"asset:read", "asset:write"},
		TriggerAttrs: []string{"file"},
	}
}

func (a *asset) DecodeSpec() hcldec.Spec {
	return hcldec.ObjectSpec{
		"file":             &hcldec.AttrSpec{Name: "file", Type: cty.String, Required: true},
		"display_name":     &hcldec.AttrSpec{Name: "display_name", Type: cty.String, Required: true},
		"description":      &hcldec.AttrSpec{Name: "description", Type: cty.String, Required: true},
		"asset_type":       &hcldec.AttrSpec{Name: "asset_type", Type: cty.String, Required: false},
		"creator_user_id":  &hcldec.AttrSpec{Name: "creator_user_id", Type: cty.Number, Required: false},
		"creator_group_id": &hcldec.AttrSpec{Name: "creator_group_id", Type: cty.Number, Required: false},
	}
}

// Triggers records a hash of the file's content, so a content change is
// detected (the API can't read content back). See RequiresReplace for what a
// change does.
func (a *asset) Triggers(desired provider.Attributes) (map[string]cty.Value, error) {
	v, ok := desired["file"]
	if !ok || v.IsNull() || !v.IsKnown() {
		return nil, nil
	}
	data, err := os.ReadFile(v.AsString())
	if err != nil {
		return nil, fmt.Errorf("roblox_asset: reading file %q: %w", v.AsString(), err)
	}
	sum := sha256.Sum256(data)
	return map[string]cty.Value{"content_hash": cty.StringVal(hex.EncodeToString(sum[:]))}, nil
}

// RequiresReplace makes a content change a replace for every asset type except
// Model: Open Cloud accepts new content (a new version under the same id) only
// for Models, so any other type needs a fresh upload with a new id.
func (a *asset) RequiresReplace(desired provider.Attributes) []string {
	if a.isModel(desired) {
		return nil
	}
	return []string{"content_hash"}
}

// isModel reports whether desired describes a Model asset (declared, or the
// default for a model file).
func (a *asset) isModel(desired provider.Attributes) bool {
	if v, ok := desired["asset_type"]; ok && !v.IsNull() {
		return v.IsKnown() && v.AsString() == "Model"
	}
	f, ok := desired["file"]
	return ok && !f.IsNull() && f.IsKnown() && defaultAssetType(f.AsString()) == "Model"
}

// Computed lists attributes excluded from the desired-vs-actual diff: the state
// id, plus the create-only inputs the API never echoes back (the local file path
// and the creator). The file's content is tracked by the content_hash trigger
// instead of its path; display_name/description/asset_type remain diffable.
func (a *asset) Computed() []string {
	return []string{"id", "file", "creator_user_id", "creator_group_id"}
}

func (a *asset) Dependencies(provider.Attributes) []provider.Ref { return nil }

func (a *asset) Create(ctx context.Context, desired provider.Attributes) (string, provider.Attributes, error) {
	path := desired["file"].AsString()
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil, fmt.Errorf("roblox_asset: reading file %q: %w", path, err)
	}
	uid, gid := numOr(desired, "creator_user_id"), numOr(desired, "creator_group_id")
	if (uid == 0) == (gid == 0) {
		return "", nil, fmt.Errorf("roblox_asset: exactly one of creator_user_id or creator_group_id is required")
	}
	created, err := a.ac.CreateAndAwait(ctx, opencloud.AssetCreate{
		AssetType:      assetTypeOr(desired, defaultAssetType(path)),
		DisplayName:    desired["display_name"].AsString(),
		Description:    desired["description"].AsString(),
		CreatorUserID:  uid,
		CreatorGroupID: gid,
	}, filepath.Base(path), contentType(path), data)
	if err != nil {
		return "", nil, err
	}
	return created.ID, a.stateAttrs(desired), nil
}

func (a *asset) Read(ctx context.Context, id string) (provider.Attributes, error) {
	got, err := a.ac.Get(ctx, id)
	if err != nil {
		if opencloud.IsNotFound(err) {
			return nil, provider.ErrNotFound
		}
		return nil, err
	}
	out := provider.Attributes{
		"display_name": cty.StringVal(got.DisplayName),
		"description":  cty.StringVal(got.Description),
	}
	if got.AssetType != "" {
		out["asset_type"] = cty.StringVal(got.AssetType)
	}
	return out, nil
}

// Update changes the metadata. For a Model it also uploads the file, since a
// content change reaches Update (only non-Models are replaced for it) and
// Update isn't told which trigger fired; a metadata-only edit therefore
// re-sends unchanged content as a new version.
func (a *asset) Update(ctx context.Context, id string, desired provider.Attributes) (provider.Attributes, error) {
	meta := opencloud.Asset{
		ID:          id,
		DisplayName: desired["display_name"].AsString(),
		Description: desired["description"].AsString(),
	}
	if a.isModel(desired) {
		path := desired["file"].AsString()
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("roblox_asset: reading file %q: %w", path, err)
		}
		meta.AssetType = "Model"
		if _, err := a.ac.UpdateContent(ctx, meta, filepath.Base(path), contentType(path), data); err != nil {
			return nil, err
		}
		return a.stateAttrs(desired), nil
	}
	if _, err := a.ac.Update(ctx, meta); err != nil {
		return nil, err
	}
	return a.stateAttrs(desired), nil
}

// Delete archives the asset. Some asset types (e.g. Image/Decal) are not
// archivable and have no Open Cloud delete path; for those, destroy stops
// managing the asset (drops it from state) and leaves it in the account — the
// same unmanage-in-place behavior as a developer product that can't be
// hard-deleted.
func (a *asset) Delete(ctx context.Context, id string) error {
	if err := a.ac.Delete(ctx, id); err != nil && !opencloud.IsNotArchivable(err) {
		return err
	}
	return nil
}

// stateAttrs echoes the declared attributes into state so create-only inputs
// (file/creator) are recorded and diffable attributes match what Read returns.
func (a *asset) stateAttrs(desired provider.Attributes) provider.Attributes {
	out := provider.Attributes{
		"file":         desired["file"],
		"display_name": desired["display_name"],
		"description":  desired["description"],
		"asset_type":   cty.StringVal(assetTypeOr(desired, defaultAssetType(desired["file"].AsString()))),
	}
	if v, ok := desired["creator_user_id"]; ok && !v.IsNull() {
		out["creator_user_id"] = v
	}
	if v, ok := desired["creator_group_id"]; ok && !v.IsNull() {
		out["creator_group_id"] = v
	}
	return out
}

func assetTypeOr(desired provider.Attributes, def string) string {
	if v, ok := desired["asset_type"]; ok && !v.IsNull() {
		return v.AsString()
	}
	return def
}

// defaultAssetType is the asset type used when asset_type is omitted: Audio for
// audio files, Model for model files, Image for everything else.
func defaultAssetType(path string) string {
	ct := contentType(path)
	switch {
	case strings.HasPrefix(ct, "audio/"):
		return "Audio"
	case strings.HasPrefix(ct, "model/"):
		return "Model"
	}
	return "Image"
}

func numOr(desired provider.Attributes, k string) int64 {
	v, ok := desired[k]
	if !ok || v.IsNull() {
		return 0
	}
	i, _ := v.AsBigFloat().Int64()
	return i
}

func contentType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".bmp":
		return "image/bmp"
	case ".tga":
		return "image/tga"
	case ".mp3":
		return "audio/mpeg"
	case ".ogg":
		return "audio/ogg"
	case ".wav":
		return "audio/wav"
	case ".flac":
		return "audio/flac"
	// Model content types, from Open Cloud's supported asset types.
	case ".fbx":
		return "model/fbx"
	case ".gltf":
		return "model/gltf+json"
	case ".glb":
		return "model/gltf-binary"
	case ".rbxm", ".rbxmx":
		return "model/x-rbxm"
	default:
		return "application/octet-stream"
	}
}
