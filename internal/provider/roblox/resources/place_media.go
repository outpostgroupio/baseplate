package resources

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/roblox/opencloud"
)

// placeMediaMaxThumbnails is Roblox's limit on detail-page thumbnails ("up to
// 10 images or videos").
const placeMediaMaxThumbnails = 10

// placeMediaSettleTries/placeMediaSettleStreak/placeMediaSettleWait bound the
// read-back after a PATCH, and placeMediaAttempts the PATCHes. Live:
//   - For a minute or two after a write, about one read in ten returns the
//     previous values, so apply waits for several consecutive matching reads.
//   - Two PATCHes a few seconds apart can merge instead of the second
//     replacing the first (seen once in three tries: [A,B] then [B,A] left
//     [A,B,A,B]), and the result persists. Re-sending the PATCH fixes it.
//   - A PATCH can report done without being applied (a list with a duplicate
//     asset is stored as []).
//
// So when the reads don't settle on the written values, apply re-sends the
// PATCH, and fails if no attempt settles. Tests shorten the wait.
var (
	placeMediaAttempts     = 3
	placeMediaSettleTries  = 20
	placeMediaSettleStreak = 3
	placeMediaSettleWait   = 500 * time.Millisecond
)

// placeThumbnailType is one element of `thumbnails`. alt_text is optional in
// config; Normalize fills it with "" (what Read reports for no alt text).
var placeThumbnailType = cty.ObjectWithOptionalAttrs(map[string]cty.Type{
	"asset_id": cty.String,
	"alt_text": cty.String,
}, []string{"alt_text"})

// placeThumbnailValType is the element type Read reports (no optional marker).
var placeThumbnailValType = cty.Object(map[string]cty.Type{
	"asset_id": cty.String,
	"alt_text": cty.String,
})

type placeMedia struct{ mc opencloud.PlaceMediaClient }

// PlaceMedia returns the roblox_place_media resource: a place's icon and
// detail-page thumbnails, written through the place asset on the Assets API.
// Its state id is the place id. It adopts an existing place, like
// roblox_place_config.
func PlaceMedia(mc opencloud.PlaceMediaClient) provider.Resource { return &placeMedia{mc: mc} }

func (p *placeMedia) Type() string { return "roblox_place_media" }

func (p *placeMedia) Doc() provider.ResourceDoc {
	return provider.ResourceDoc{
		Summary: "Manages a place's icon and detail-page thumbnails. On the root place these are the experience's icon and thumbnails. Adopts the place; it does not create places.",
		Attributes: map[string]string{
			"place_id":   "The place. Use the root place for the experience's icon and thumbnails.",
			"icon":       "Image asset id of the icon (square). `\"\"` removes the icon.",
			"thumbnails": "Ordered list of `{ asset_id, alt_text }` (alt text optional), the order shown on the game page. Image asset ids, or the id of a video added in Creator Dashboard; at most 10, no duplicates. `[]` removes them all.",
		},
		Notes: "Only declared attributes are managed; omit `icon` or `thumbnails` to leave it as it is. Each apply replaces the whole thumbnail list. " +
			"`icon` and each thumbnail's `asset_id` can be a reference (`roblox_asset.icon.id`). An image still in moderation review is accepted. " +
			"Uses the Assets API's place-asset `icon` and `previews` fields (beta). Roblox's reads lag behind its writes, and an update can report done without being applied, so apply waits until the place reads back as written, re-sends the update (with a warning) if it doesn't, and fails after three tries. " +
			"For a minute or two after an apply, a plan can still read the old values and show them as a change; applying again is harmless. " +
			"A video added in Creator Dashboard reads back as an ordinary entry with alt text `\"\"`, and can be listed in `thumbnails` by its asset id: Roblox keeps it, in its position. " +
			"To keep a hand-added video, put its id in `thumbnails`, or the next apply removes it with the rest of the undeclared entries; Baseplate can't add it back. A new upload has a new id, which the config needs before the next apply. " +
			"Baseplate can't upload a video: uploading a Video asset through Open Cloud costs Robux and needs a public experience. " +
			"Homepage thumbnails (thumbnail personalization) and translated icons and thumbnails are separate APIs that this resource doesn't manage. " +
			"`destroy` unmanages the place (the icon and thumbnails are left as they are). Import by place id.",
		Scopes:      []string{"asset:read", "asset:write"},
		ExampleFile: "examples/place-media.bp.hcl",
	}
}

func (p *placeMedia) DecodeSpec() hcldec.Spec {
	return hcldec.ObjectSpec{
		"place_id":   &hcldec.AttrSpec{Name: "place_id", Type: cty.String, Required: true},
		"icon":       &hcldec.AttrSpec{Name: "icon", Type: cty.String},
		"thumbnails": &hcldec.AttrSpec{Name: "thumbnails", Type: cty.List(placeThumbnailType)},
	}
}

// Computed excludes the identity attr from the diff: it is fixed by the state id.
func (p *placeMedia) Computed() []string { return []string{"id", "place_id"} }

func (p *placeMedia) Dependencies(provider.Attributes) []provider.Ref { return nil }

// Normalize fills an omitted alt_text with "" and rejects what Roblox would
// reject or silently drop: a non-numeric id, more than 10 thumbnails, or the
// same asset twice. Unknown values (references resolved at apply) pass through.
func (p *placeMedia) Normalize(desired provider.Attributes) (provider.Attributes, error) {
	out := make(provider.Attributes, len(desired))
	for k, v := range desired {
		out[k] = v
	}
	if v, ok := desired["icon"]; ok && v.IsKnown() && !v.IsNull() {
		if s := v.AsString(); s != "" && !isAssetID(s) {
			return nil, fmt.Errorf("icon %q must be an asset id (digits only), or \"\" to remove the icon", s)
		}
	}
	v, ok := desired["thumbnails"]
	if !ok || !v.IsKnown() || v.IsNull() {
		return out, nil
	}
	if n := v.LengthInt(); n > placeMediaMaxThumbnails {
		return nil, fmt.Errorf("thumbnails has %d entries; Roblox allows at most %d", n, placeMediaMaxThumbnails)
	}
	seen := map[string]bool{}
	var elems []cty.Value
	for it := v.ElementIterator(); it.Next(); {
		_, e := it.Element()
		if !e.IsKnown() || e.IsNull() {
			elems = append(elems, e)
			continue
		}
		id, alt := e.GetAttr("asset_id"), e.GetAttr("alt_text")
		if id.IsKnown() && !id.IsNull() {
			s := id.AsString()
			if !isAssetID(s) {
				return nil, fmt.Errorf("thumbnails: asset_id %q must be an asset id (digits only)", s)
			}
			if seen[s] {
				return nil, fmt.Errorf("thumbnails: asset %s is listed twice (Roblox drops the whole list when an asset repeats)", s)
			}
			seen[s] = true
		}
		if alt.IsNull() {
			alt = cty.StringVal("")
		}
		elems = append(elems, cty.ObjectVal(map[string]cty.Value{"asset_id": id, "alt_text": alt}))
	}
	if len(elems) == 0 {
		out["thumbnails"] = cty.ListValEmpty(placeThumbnailValType)
	} else {
		out["thumbnails"] = cty.ListVal(elems)
	}
	return out, nil
}

func isAssetID(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (p *placeMedia) Create(ctx context.Context, desired provider.Attributes) (string, provider.Attributes, error) {
	pid := desired["place_id"].AsString()
	attrs, err := p.apply(ctx, pid, desired)
	if err != nil {
		return "", nil, err
	}
	return pid, attrs, nil
}

func (p *placeMedia) Read(ctx context.Context, id string) (provider.Attributes, error) {
	m, err := p.get(ctx, id)
	if err != nil {
		if opencloud.IsNotFound(err) {
			return nil, provider.ErrNotFound
		}
		return nil, err
	}
	return placeMediaAttrs(id, m), nil
}

func (p *placeMedia) Update(ctx context.Context, id string, desired provider.Attributes) (provider.Attributes, error) {
	return p.apply(ctx, id, desired)
}

// Delete is a no-op: Baseplate manages the media of a place it doesn't own, so
// dropping the resource just stops managing it.
func (p *placeMedia) Delete(ctx context.Context, id string) error { return nil }

// get reads the place asset and checks that it is a place: any other asset
// reads back with an empty thumbnail list, which would look like a valid target.
func (p *placeMedia) get(ctx context.Context, pid string) (opencloud.PlaceMedia, error) {
	m, err := p.mc.Get(ctx, pid)
	if err != nil {
		return m, err
	}
	if m.AssetType != "Place" {
		return m, fmt.Errorf("roblox_place_media: asset %s is not a place (its type is %s)", pid, m.AssetType)
	}
	return m, nil
}

// apply PATCHes the declared fields (if any) and waits for the place to read
// back as written, re-sending the PATCH when it doesn't (see placeMediaAttempts).
func (p *placeMedia) apply(ctx context.Context, pid string, desired provider.Attributes) (provider.Attributes, error) {
	got, err := p.get(ctx, pid)
	if err != nil {
		return nil, err
	}
	want, mask := placeMediaFromDesired(desired)
	if len(mask) == 0 {
		return placeMediaAttrs(pid, got), nil
	}
	for attempt := 1; ; attempt++ {
		if err := p.mc.Update(ctx, pid, want, mask); err != nil {
			return nil, fmt.Errorf("roblox_place_media: update place %s: %w", pid, err)
		}
		settled, err := p.awaitSettled(ctx, pid, want, mask, &got)
		if err != nil {
			return nil, err
		}
		if settled {
			return placeMediaAttrs(pid, got), nil
		}
		if attempt == placeMediaAttempts {
			return nil, fmt.Errorf("roblox_place_media: Roblox accepted the update to place %s %d times, but it reads back as icon %q, thumbnails %v", pid, attempt, got.Icon, got.Thumbnails)
		}
		provider.Warn(ctx, fmt.Sprintf("roblox_place_media: place %s didn't read back as written (icon %q, thumbnails %v); sending the update again", pid, got.Icon, got.Thumbnails))
	}
}

// awaitSettled reads the place until it matches want on placeMediaSettleStreak
// consecutive reads, up to placeMediaSettleTries reads. got is set to the last
// read.
func (p *placeMedia) awaitSettled(ctx context.Context, pid string, want opencloud.PlaceMedia, mask []string, got *opencloud.PlaceMedia) (bool, error) {
	streak := 0
	for i := 0; i < placeMediaSettleTries; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return false, ctx.Err()
			case <-time.After(placeMediaSettleWait):
			}
		}
		m, err := p.get(ctx, pid)
		if err != nil {
			return false, err
		}
		*got = m
		if !placeMediaMatches(m, want, mask) {
			streak = 0
			continue
		}
		if streak++; streak >= placeMediaSettleStreak {
			return true, nil
		}
	}
	return false, nil
}

func placeMediaMatches(got, want opencloud.PlaceMedia, mask []string) bool {
	for _, f := range mask {
		switch f {
		case "icon":
			if got.Icon != want.Icon {
				return false
			}
		case "previews":
			if len(got.Thumbnails) != len(want.Thumbnails) || (len(want.Thumbnails) > 0 && !reflect.DeepEqual(got.Thumbnails, want.Thumbnails)) {
				return false
			}
		}
	}
	return true
}

func placeMediaFromDesired(desired provider.Attributes) (opencloud.PlaceMedia, []string) {
	var m opencloud.PlaceMedia
	var mask []string
	if v, ok := desired["icon"]; ok && !v.IsNull() {
		m.Icon = v.AsString()
		mask = append(mask, "icon")
	}
	if v, ok := desired["thumbnails"]; ok && !v.IsNull() {
		m.Thumbnails = []opencloud.PlaceThumbnail{}
		for it := v.ElementIterator(); it.Next(); {
			_, e := it.Element()
			t := opencloud.PlaceThumbnail{AssetID: e.GetAttr("asset_id").AsString()}
			if alt := e.GetAttr("alt_text"); !alt.IsNull() {
				t.AltText = alt.AsString()
			}
			m.Thumbnails = append(m.Thumbnails, t)
		}
		mask = append(mask, "previews")
	}
	sort.Strings(mask)
	return m, mask
}

func placeMediaAttrs(pid string, m opencloud.PlaceMedia) provider.Attributes {
	thumbs := cty.ListValEmpty(placeThumbnailValType)
	if len(m.Thumbnails) > 0 {
		elems := make([]cty.Value, len(m.Thumbnails))
		for i, t := range m.Thumbnails {
			elems[i] = cty.ObjectVal(map[string]cty.Value{"asset_id": cty.StringVal(t.AssetID), "alt_text": cty.StringVal(t.AltText)})
		}
		thumbs = cty.ListVal(elems)
	}
	return provider.Attributes{
		"place_id":   cty.StringVal(pid),
		"icon":       cty.StringVal(m.Icon),
		"thumbnails": thumbs,
	}
}
