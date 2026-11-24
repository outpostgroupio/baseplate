package resources

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/roblox/opencloud"
)

type asset struct {
	filteredText
	ac opencloud.AssetClient
	// tc resolves a Decal's texture id (texture_id). Optional: nil leaves
	// texture_id unset for Decals.
	tc opencloud.TextureClient
	// noTextureScope is set once the texture lookup is refused for lack of the
	// API-key scope, so the remaining Decals in this run skip the lookup (the
	// warning is already recorded).
	noTextureScope atomic.Bool
}

// Asset returns the roblox_asset resource backed by an AssetClient, and a
// TextureClient for Decal texture ids (nil: Decals have no texture_id).
func Asset(ac opencloud.AssetClient, tc opencloud.TextureClient) provider.Resource {
	return &asset{ac: ac, tc: tc}
}

func (a *asset) Type() string { return "roblox_asset" }

func (a *asset) Doc() provider.ResourceDoc {
	return provider.ResourceDoc{
		Summary: "Uploads a user- or group-owned image, decal, audio or model asset.",
		Attributes: map[string]string{
			"file":             "Path to the file to upload: `.png`/`.jpg`/`.bmp`/`.tga` for images and decals, `.mp3`/`.ogg`/`.wav`/`.flac` for audio, `.fbx`/`.gltf`/`.glb`/`.rbxm`/`.rbxmx` for models. A change to the file's content is detected by hash: an Image or Model takes the new content in place (same id); a Decal, Audio or Video asset is replaced (a new upload with a new id; the old asset is left in place).",
			"source_hash":      "Optional. A value that changes when the asset's source changes, used instead of the hash of `file` to detect a content change, e.g. `filesha256(\"art/logo.svg\")` for a PNG generated from an SVG. Then regenerating `file` with different bytes from the same source (a new converter version, say) doesn't upload it again, and a plan doesn't need `file` to exist. Adding it to an existing asset doesn't upload anything.",
			"display_name":     "Asset name.",
			"description":      "Asset description.",
			"asset_type":       "Asset type: `Image`, `Decal`, `Audio` or `Model`. Defaults to `Audio` for audio files, `Model` for model files and `Image` otherwise. Changing it (including the default, by changing the file's extension) replaces the asset, because Roblox can't change an existing asset's type. Use `Image` for anything the game shows in an `ImageLabel` or `Decal`.",
			"creator_user_id":  "Owning user id (set exactly one of creator_user_id/creator_group_id).",
			"creator_group_id": "Owning group id (set exactly one of creator_user_id/creator_group_id).",
		},
		Outputs: map[string]string{
			"texture_id": "The texture id, for `rbxassetid://<texture_id>` in `ImageLabel.Image`, `ImageButton.Image` or `Decal.Texture`. For an `Image` asset it equals `id`. For a `Decal` it is the id of the separate Image asset Roblox creates behind the decal, looked up through toolbox-service, which needs the optional `creator-store-product:read` scope. Unset (absent from state and `show --json`, and an error if referenced) for `Model`, `Audio` and `Video` assets, and for a Decal whose lookup failed (see the notes).",
		},
		Notes:        "The `id` output is the `assetId` (a numeric string). For an `Image` asset (the default for image files) that id is the texture id, and `texture_id` equals it. A `Decal` asset's id is not a texture id: each Decal wraps a separate Image asset that Roblox creates alongside it, owned by the same creator, and `texture_id` is that image's id. Use `texture_id` and you don't need to know which kind an asset is. Resolving a Decal's `texture_id` costs one extra request per Decal on every `plan` (Images cost nothing extra). If the lookup fails, plan and apply still succeed: `texture_id` stays unset (or keeps the value already recorded) and Baseplate prints a warning with the reason. The usual reasons: the API key lacks `creator-store-product:read` (one warning, and the remaining Decals skip the lookup); the decal was just uploaded (toolbox-service returns 404 for a new decal for about a minute, so a later `apply` records it even when nothing else changed); or Roblox doesn't show the key the decal's details (group-owned decals are unverified). To adopt assets from a Decal-based uploader, import those underlying image ids, not the decal ids (see the many-assets how-to). When the content of `file` changes (its `content_hash` trigger), an `Image` or `Model` asset is updated in place: apply uploads the file as a new version of the same asset, so the id doesn't change (Roblox documents content updates for Models only, but Images accept them too; a metadata-only edit doesn't re-send the content). A `Decal`, `Audio` or `Video` asset can't take new content (Roblox rejects a Decal content update as not supported yet), so plan shows `-/+ (replace)`: apply uploads a new asset, which gets a new id, and drops the old one from state. The old asset is left in place (never archived, even for Audio), because live servers keep using its id until the place is republished. A change to `asset_type` always replaces. Forcing a replace for a type that updates in place is a planned general feature (`lifecycle { replace_triggered_by = [...] }`), not a per-resource setting. If `file` is missing on the machine running `plan` (e.g. CI without the source images), an asset already in state keeps its recorded hash and plan warns that content changes can't be detected; creating an asset still needs the file. Renaming or moving the file without changing its bytes does nothing. References to the asset's `id` and `baseplate show --json` pick up the new id in the same apply. An imported asset records the current file's hash as its baseline, so adopting existing assets never re-uploads them; the same goes for assets created by Baseplate versions without the hash, on their next apply. `lifecycle { prevent_destroy = true }` blocks a replace. Audio is limited to 7 minutes and 20 MB, and Roblox caps audio uploads per month (10, or 100 for ID-verified accounts). Image and Model assets are not archivable, so `destroy` unmanages the asset in place (drops it from state, leaves it uploaded); a Decal is archived, and can be restored.",
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
		"source_hash":      &hcldec.AttrSpec{Name: "source_hash", Type: cty.String, Required: false},
		"creator_user_id":  &hcldec.AttrSpec{Name: "creator_user_id", Type: cty.Number, Required: false},
		"creator_group_id": &hcldec.AttrSpec{Name: "creator_group_id", Type: cty.Number, Required: false},
	}
}

// contentInPlace lists the asset types whose content Open Cloud updates in
// place (a new version under the same id). Any other type is replaced on a
// content change (a new upload with a new id). Open Cloud documents content
// updates for Models only, but Images accepted them in live testing. Decals are rejected ("Updating Decal is not supported yet",
// verified live); Audio and Video are documented as not updatable (not probed:
// audio uploads are capped).
var contentInPlace = map[string]bool{"Image": true, "Model": true}

var (
	_ provider.Replacer            = (*asset)(nil)
	_ provider.Triggerable         = (*asset)(nil)
	_ provider.TriggerAwareUpdater = (*asset)(nil)
	_ provider.Normalizer          = (*asset)(nil)
	_ provider.Outputter           = (*asset)(nil)
)

// Outputs: texture_id, the id to use as rbxassetid://<texture_id>.
func (a *asset) Outputs() map[string]cty.Type {
	return map[string]cty.Type{"texture_id": cty.String}
}

// Triggers records a hash of the file's content, so a content change is
// detected (the API can't read content back). See RequiresReplace for what a
// change does. A missing file is reported as unavailable, so plan for an asset
// already in state warns instead of failing (e.g. CI without the images).
// When source_hash is set, it's recorded instead and the file isn't read. It's
// a separate trigger from content_hash, so adding source_hash to an asset finds
// no recorded value to compare with and records it as the baseline rather
// than uploading.
func (a *asset) Triggers(desired provider.Attributes) (map[string]cty.Value, error) {
	if src, ok := sourceHash(desired); ok {
		return map[string]cty.Value{"source_hash": cty.StringVal(src)}, nil
	}
	v, ok := desired["file"]
	if !ok || v.IsNull() || !v.IsKnown() {
		return nil, nil
	}
	sum, err := fileHash(v.AsString())
	if err != nil {
		return nil, err
	}
	return map[string]cty.Value{"content_hash": cty.StringVal(sum)}, nil
}

// sourceHash returns the declared source_hash, if any.
func sourceHash(desired provider.Attributes) (string, bool) {
	v, ok := desired["source_hash"]
	if !ok || v.IsNull() || !v.IsKnown() {
		return "", false
	}
	return v.AsString(), true
}

// fileHash hashes an asset's file (see hashFile).
func fileHash(path string) (string, error) { return hashFile("roblox_asset", "file", path) }

// Normalize fills in an omitted asset_type with the default for the file
// (Audio, Model or Image), so the type is always compared with the remote
// asset's: changing the file from .png to .fbx changes the type, which forces
// a replace.
func (a *asset) Normalize(desired provider.Attributes) (provider.Attributes, error) {
	if v, ok := desired["asset_type"]; ok && !v.IsNull() {
		return desired, nil
	}
	f, ok := desired["file"]
	if !ok || f.IsNull() || !f.IsKnown() {
		return desired, nil
	}
	out := make(provider.Attributes, len(desired)+1)
	for k, v := range desired {
		out[k] = v
	}
	out["asset_type"] = cty.StringVal(defaultAssetType(f.AsString()))
	return out, nil
}

// RequiresReplace: Roblox can't change an existing asset's type, so a type
// change always replaces. A content change replaces unless the asset's CURRENT
// type takes content in place (contentInPlace); the current type decides,
// because the content update is applied to the existing asset.
func (a *asset) RequiresReplace(desired, actual provider.Attributes) []string {
	keys := []string{"asset_type"}
	if !contentInPlace[currentType(desired, actual)] {
		keys = append(keys, "content_hash", "source_hash")
	}
	return keys
}

// ReplaceAbandons: the old asset is left in place on a replace, never archived.
// Live servers keep loading rbxassetid://<old id> until the place is
// republished with the new id, so the old asset must stay available.
func (a *asset) ReplaceAbandons() bool { return true }

// currentType is the remote asset's type as Read reports it, falling back to
// the desired type when Read didn't report one.
func currentType(desired, actual provider.Attributes) string {
	if v, ok := actual["asset_type"]; ok && !v.IsNull() && v.IsKnown() && v.Type() == cty.String {
		return v.AsString()
	}
	return desiredType(desired)
}

// desiredType is the declared asset_type, or the default for the file.
func desiredType(desired provider.Attributes) string {
	if v, ok := desired["asset_type"]; ok && !v.IsNull() {
		if v.IsKnown() {
			return v.AsString()
		}
		return ""
	}
	f, ok := desired["file"]
	if !ok || f.IsNull() || !f.IsKnown() {
		return ""
	}
	return defaultAssetType(f.AsString())
}

// Computed lists attributes excluded from the desired-vs-actual diff: the state
// id and the texture_id output, plus the create-only inputs the API never
// echoes back (the local file path and the creator). The file's content is
// tracked by the content_hash trigger instead of its path;
// display_name/description/asset_type remain diffable.
func (a *asset) Computed() []string {
	return []string{"id", "texture_id", "file", "source_hash", "creator_user_id", "creator_group_id"}
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
	out := a.stateAttrs(desired)
	a.addTextureID(ctx, out, created.ID, assetTypeOr(desired, defaultAssetType(path)))
	return created.ID, out, nil
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
	a.addTextureID(ctx, out, id, got.AssetType)
	return out, nil
}

// Update is UpdateWithTriggers without knowledge of the prior content hash,
// so an asset type that takes content in place re-sends the file.
func (a *asset) Update(ctx context.Context, id string, desired provider.Attributes) (provider.Attributes, error) {
	return a.UpdateWithTriggers(ctx, id, desired, nil)
}

// UpdateWithTriggers changes the metadata and, for a type that takes content
// in place (contentInPlace), uploads the file only when its content_hash
// differs from the one recorded before this update (prior), so a metadata-only
// edit doesn't create a new version. A type change or, for other types, a
// content change never reaches Update: plan replaces those. A missing file
// means the content can't have been checked, so only the metadata is sent.
func (a *asset) UpdateWithTriggers(ctx context.Context, id string, desired provider.Attributes, prior map[string]cty.Value) (provider.Attributes, error) {
	typ := desiredType(desired)
	meta := opencloud.Asset{
		ID:          id,
		DisplayName: desired["display_name"].AsString(),
		Description: desired["description"].AsString(),
	}
	if contentInPlace[typ] {
		path := desired["file"].AsString()
		changed, err := assetContentChanged(desired, prior)
		if err != nil {
			return nil, err
		}
		if changed {
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("roblox_asset: reading file %q: %w", path, err)
			}
			meta.AssetType = typ
			if _, err := a.ac.UpdateContent(ctx, meta, filepath.Base(path), contentType(path), data); err != nil {
				var apiErr *opencloud.APIError
				if errors.As(err, &apiErr) && (apiErr.Status == 400 || apiErr.Status == 422) {
					return nil, fmt.Errorf("roblox_asset %q (id %s): Roblox rejected the in-place content update for a %s asset (%w). Baseplate updates %s content in place because Roblox accepted it when this version was released; Roblox appears to no longer allow it, so Baseplate needs updating to replace %s assets instead. The asset is unchanged",
						meta.DisplayName, id, typ, err, typ, typ)
				}
				return nil, err
			}
			// Live (2026-10-26): a content update sent within seconds of a
			// metadata update keeps the earlier name/description, ignoring
			// the request's. Read back and re-send the metadata if so.
			got, err := a.ac.Get(ctx, id)
			if err != nil {
				return nil, err
			}
			if got.DisplayName == meta.DisplayName && got.Description == meta.Description {
				return a.updatedAttrs(ctx, id, typ, desired), nil
			}
			meta.AssetType = ""
		}
	}
	if _, err := a.ac.Update(ctx, meta); err != nil {
		return nil, err
	}
	return a.updatedAttrs(ctx, id, typ, desired), nil
}

// assetContentChanged reports whether the content differs from what was
// recorded before this update (prior): by source_hash when it's set and was
// recorded, otherwise by the file's hash. A missing file means the content
// can't have been checked, so it reports no change.
func assetContentChanged(desired provider.Attributes, prior map[string]cty.Value) (bool, error) {
	if src, ok := sourceHash(desired); ok {
		if was, ok := prior["source_hash"]; ok && !was.IsNull() && was.IsKnown() && was.Type() == cty.String {
			return was.AsString() != src, nil
		}
	}
	sum, err := fileHash(desired["file"].AsString())
	var unavailable *provider.TriggerUnavailableError
	if errors.As(err, &unavailable) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return contentChanged(prior, sum), nil
}

// updatedAttrs is stateAttrs plus texture_id for an updated asset of type typ.
func (a *asset) updatedAttrs(ctx context.Context, id, typ string, desired provider.Attributes) provider.Attributes {
	out := a.stateAttrs(desired)
	a.addTextureID(ctx, out, id, typ)
	return out
}

// addTextureID sets out["texture_id"] for asset id of type typ when it can be
// determined. An Image's texture id is its own id (no request). A Decal's is
// the Image behind it, looked up via toolbox-service (one request per Decal
// per Read/Create/Update). Model, Audio and Video assets have no texture id,
// so the attribute stays unset. A failed Decal lookup never fails the
// operation: texture_id stays unset (the engine keeps a recorded one) and the
// reason is reported as a warning.
func (a *asset) addTextureID(ctx context.Context, out provider.Attributes, id, typ string) {
	switch typ {
	case "Image":
		out["texture_id"] = cty.StringVal(id)
	case "Decal":
		if tex, ok := a.decalTextureID(ctx, id); ok {
			out["texture_id"] = cty.StringVal(tex)
		}
	}
}

func (a *asset) decalTextureID(ctx context.Context, id string) (string, bool) {
	if a.tc == nil || a.noTextureScope.Load() {
		return "", false
	}
	tex, err := a.tc.DecalTextureID(ctx, id)
	if err == nil {
		return tex, true
	}
	var apiErr *opencloud.APIError
	switch {
	case errors.As(err, &apiErr) && (apiErr.Status == 401 || apiErr.Status == 403) &&
		strings.Contains(strings.ToLower(apiErr.Message), "scope"):
		a.noTextureScope.Store(true)
		provider.Warn(ctx, fmt.Sprintf("roblox_asset: texture_id is not set for Decal assets: the API key lacks the %s scope (%v). The scope is optional and only needed for Decal texture ids; Image assets don't need it (their texture_id is their id).",
			opencloud.TextureScope, err))
	case opencloud.IsNotFound(err):
		provider.Warn(ctx, fmt.Sprintf("roblox_asset: texture_id is not set yet for Decal %s: toolbox-service returned 404. A newly uploaded decal takes about a minute to appear there; a later apply records texture_id. If it persists, Roblox isn't showing this API key the decal's details (e.g. a group-owned decal, which is unverified).", id))
	default:
		provider.Warn(ctx, fmt.Sprintf("roblox_asset: texture_id is not set for Decal %s: the toolbox-service lookup (scope %s) failed: %v. A later plan or apply retries.", id, opencloud.TextureScope, err))
	}
	return "", false
}

// Delete archives the asset. Some asset types (Image, Model) are not
// archivable and have no Open Cloud delete path; for those, destroy stops
// managing the asset (drops it from state) and leaves it in the account; the
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
