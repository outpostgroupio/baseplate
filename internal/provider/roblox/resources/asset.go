package resources

import (
	"context"
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
		Summary: "Uploads a user- or group-owned image, decal or audio asset.",
		Attributes: map[string]string{
			"file":             "Path to the file to upload: `.png`/`.jpg`/`.bmp`/`.tga` for images and decals, `.mp3`/`.ogg`/`.wav`/`.flac` for audio.",
			"display_name":     "Asset name.",
			"description":      "Asset description.",
			"asset_type":       "Asset type: `Image`, `Decal` or `Audio`. Defaults to `Audio` for audio files and `Image` otherwise. Use `Image` for anything the game shows in an `ImageLabel` or `Decal`. Roblox can't change an existing asset's type.",
			"creator_user_id":  "Owning user id (set exactly one of creator_user_id/creator_group_id).",
			"creator_group_id": "Owning group id (set exactly one of creator_user_id/creator_group_id).",
		},
		Notes:  "The `id` output is the `assetId` (a numeric string). For an `Image` asset (the default for image files) that id is the texture id: use it directly as `rbxassetid://<id>` in `ImageLabel.Image` or `Decal.Texture`. A `Decal` asset's id is not a texture id. Each Decal wraps a separate Image asset that Roblox creates alongside it, owned by the same creator. To adopt assets from a Decal-based uploader, import those underlying image ids, not the decal ids (see the many-assets how-to). Changing `file` doesn't re-upload: Open Cloud only accepts new content for Model assets, so a changed image or sound needs a new resource (and gets a new id). Audio is limited to 7 minutes and 20 MB, and Roblox caps audio uploads per month (10, or 100 for ID-verified accounts). Image/decal assets are not archivable, so `destroy` unmanages the asset in place (drops it from state, leaves it uploaded).",
		Scopes: []string{"asset:read", "asset:write"},
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

// Computed lists attributes excluded from the desired-vs-actual diff: the state
// id, plus the create-only inputs the API never echoes back (the local file path
// and the creator). Changing them would require re-creating the asset, which is
// out of scope for now; display_name/description/asset_type remain diffable.
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

func (a *asset) Update(ctx context.Context, id string, desired provider.Attributes) (provider.Attributes, error) {
	if _, err := a.ac.Update(ctx, opencloud.Asset{
		ID:          id,
		DisplayName: desired["display_name"].AsString(),
		Description: desired["description"].AsString(),
	}); err != nil {
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
// audio files, Image for everything else.
func defaultAssetType(path string) string {
	if strings.HasPrefix(contentType(path), "audio/") {
		return "Audio"
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
	default:
		return "application/octet-stream"
	}
}
