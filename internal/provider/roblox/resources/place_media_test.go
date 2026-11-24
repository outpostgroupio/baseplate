package resources

import (
	"context"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/roblox/opencloud"
)

func init() { placeMediaSettleWait = 0 }

type fakePlaceMedia struct {
	store map[string]opencloud.PlaceMedia
	masks [][]string
	// drop is how many PATCHes to report as done without applying, as Roblox
	// sometimes does; -1 drops them all.
	drop int
}

func (f *fakePlaceMedia) Get(_ context.Context, id string) (opencloud.PlaceMedia, error) {
	m, ok := f.store[id]
	if !ok {
		return m, &opencloud.APIError{Status: 404}
	}
	return m, nil
}

func (f *fakePlaceMedia) Update(_ context.Context, id string, m opencloud.PlaceMedia, mask []string) error {
	f.masks = append(f.masks, mask)
	if f.drop != 0 {
		f.drop--
		return nil
	}
	cur := f.store[id]
	for _, k := range mask {
		switch k {
		case "icon":
			cur.Icon = m.Icon
		case "previews":
			cur.Thumbnails = m.Thumbnails
		}
	}
	f.store[id] = cur
	return nil
}

func newFakePlaceMedia() *fakePlaceMedia {
	return &fakePlaceMedia{store: map[string]opencloud.PlaceMedia{
		"9": {AssetType: "Place", Icon: "1", Thumbnails: []opencloud.PlaceThumbnail{{AssetID: "2", AltText: "old"}}},
		"3": {AssetType: "Image", Thumbnails: []opencloud.PlaceThumbnail{}},
	}}
}

// thumbs builds a `thumbnails` value the way HCL decodes it: alt_text is
// optional, so an omitted one is null.
func thumbs(t *testing.T, pairs ...string) cty.Value {
	t.Helper()
	var elems []cty.Value
	for i := 0; i < len(pairs); i += 2 {
		attrs := map[string]cty.Value{"asset_id": cty.StringVal(pairs[i])}
		if pairs[i+1] != "-" {
			attrs["alt_text"] = cty.StringVal(pairs[i+1])
		}
		elems = append(elems, cty.ObjectVal(attrs))
	}
	in := cty.EmptyTupleVal
	if len(elems) > 0 {
		in = cty.TupleVal(elems)
	}
	v, err := convert.Convert(in, cty.List(placeThumbnailType))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func normalizePlaceMedia(t *testing.T, r provider.Resource, a provider.Attributes) provider.Attributes {
	t.Helper()
	out, err := r.(provider.Normalizer).Normalize(a)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestPlaceMediaCreateWritesAndConverges(t *testing.T) {
	f := newFakePlaceMedia()
	r := PlaceMedia(f)
	desired := normalizePlaceMedia(t, r, provider.Attributes{
		"place_id":   cty.StringVal("9"),
		"icon":       cty.StringVal("5"),
		"thumbnails": thumbs(t, "6", "hero", "7", "-"),
	})
	id, got, err := r.Create(context.Background(), desired)
	if err != nil {
		t.Fatal(err)
	}
	if id != "9" {
		t.Fatalf("id = %q", id)
	}
	if m := f.masks[0]; len(m) != 2 || m[0] != "icon" || m[1] != "previews" {
		t.Fatalf("mask = %v", m)
	}
	want := []opencloud.PlaceThumbnail{{AssetID: "6", AltText: "hero"}, {AssetID: "7"}}
	if s := f.store["9"]; s.Icon != "5" || len(s.Thumbnails) != 2 || s.Thumbnails[0] != want[0] || s.Thumbnails[1] != want[1] {
		t.Fatalf("stored %+v", s)
	}
	// The omitted alt_text normalizes to "", which is what Read reports.
	for _, k := range []string{"icon", "thumbnails"} {
		if !got[k].RawEquals(desired[k]) {
			t.Errorf("%s: read %#v, desired %#v", k, got[k], desired[k])
		}
	}
}

func TestPlaceMediaOnlyDeclaredFields(t *testing.T) {
	f := newFakePlaceMedia()
	r := PlaceMedia(f)
	if _, err := r.Update(context.Background(), "9", provider.Attributes{
		"place_id": cty.StringVal("9"),
		"icon":     cty.StringVal(""),
	}); err != nil {
		t.Fatal(err)
	}
	if m := f.masks[0]; len(m) != 1 || m[0] != "icon" {
		t.Fatalf("mask = %v", m)
	}
	if s := f.store["9"]; s.Icon != "" || len(s.Thumbnails) != 1 {
		t.Fatalf("stored %+v", s)
	}
}

func TestPlaceMediaClearThumbnails(t *testing.T) {
	f := newFakePlaceMedia()
	r := PlaceMedia(f)
	desired := normalizePlaceMedia(t, r, provider.Attributes{"place_id": cty.StringVal("9"), "thumbnails": thumbs(t)})
	got, err := r.Update(context.Background(), "9", desired)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.store["9"].Thumbnails) != 0 {
		t.Fatalf("stored %+v", f.store["9"])
	}
	if !got["thumbnails"].RawEquals(desired["thumbnails"]) {
		t.Fatalf("read %#v, desired %#v", got["thumbnails"], desired["thumbnails"])
	}
}

func TestPlaceMediaResendsWhenTheChangeDoesNotSettle(t *testing.T) {
	f := newFakePlaceMedia()
	f.drop = 1
	r := PlaceMedia(f)
	ctx, warns := provider.WithWarnings(context.Background())
	if _, err := r.Update(ctx, "9", provider.Attributes{"place_id": cty.StringVal("9"), "icon": cty.StringVal("5")}); err != nil {
		t.Fatal(err)
	}
	if len(f.masks) != 2 || f.store["9"].Icon != "5" {
		t.Fatalf("patches %v, stored %+v", f.masks, f.store["9"])
	}
	if w := warns.List(); len(w) != 1 || !strings.Contains(w[0], "sending the update again") {
		t.Fatalf("warnings = %v", w)
	}
}

func TestPlaceMediaFailsWhenRobloxNeverAppliesTheChange(t *testing.T) {
	f := newFakePlaceMedia()
	f.drop = -1
	r := PlaceMedia(f)
	_, err := r.Update(context.Background(), "9", provider.Attributes{"place_id": cty.StringVal("9"), "icon": cty.StringVal("5")})
	if err == nil || !strings.Contains(err.Error(), "accepted the update to place 9 3 times") {
		t.Fatalf("err = %v", err)
	}
	if len(f.masks) != placeMediaAttempts {
		t.Fatalf("patches = %d", len(f.masks))
	}
}

func TestPlaceMediaNoDeclaredFieldsSendsNothing(t *testing.T) {
	f := newFakePlaceMedia()
	if _, _, err := PlaceMedia(f).Create(context.Background(), provider.Attributes{"place_id": cty.StringVal("9")}); err != nil {
		t.Fatal(err)
	}
	if len(f.masks) != 0 {
		t.Fatalf("patches = %v", f.masks)
	}
}

func TestPlaceMediaRejectsNonPlace(t *testing.T) {
	r := PlaceMedia(newFakePlaceMedia())
	if _, err := r.Read(context.Background(), "3"); err == nil || !strings.Contains(err.Error(), "is not a place (its type is Image)") {
		t.Fatalf("err = %v", err)
	}
	if _, _, err := r.Create(context.Background(), provider.Attributes{"place_id": cty.StringVal("3"), "icon": cty.StringVal("5")}); err == nil {
		t.Fatal("create on an image asset should fail")
	}
}

func TestPlaceMediaReadMissingIsNotFound(t *testing.T) {
	r := PlaceMedia(newFakePlaceMedia())
	if _, err := r.Read(context.Background(), "404"); err != provider.ErrNotFound {
		t.Fatalf("err = %v", err)
	}
}

func TestPlaceMediaNormalizeRejects(t *testing.T) {
	r := PlaceMedia(newFakePlaceMedia()).(provider.Normalizer)
	eleven := make([]string, 0, 22)
	for i := 0; i < 11; i++ {
		eleven = append(eleven, string(rune('1'+i%9))+strings.Repeat("0", i), "-")
	}
	for name, tc := range map[string]struct {
		attrs provider.Attributes
		want  string
	}{
		"bad icon":     {provider.Attributes{"icon": cty.StringVal("rbxassetid://5")}, "must be an asset id"},
		"bad thumb id": {provider.Attributes{"thumbnails": thumbs(t, "assets/5", "-")}, "must be an asset id"},
		"duplicate":    {provider.Attributes{"thumbnails": thumbs(t, "5", "a", "6", "-", "5", "b")}, "listed twice"},
		"too many":     {provider.Attributes{"thumbnails": thumbs(t, eleven...)}, "at most 10"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := r.Normalize(tc.attrs); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestPlaceMediaNormalizeLeavesUnknown(t *testing.T) {
	r := PlaceMedia(newFakePlaceMedia()).(provider.Normalizer)
	in := provider.Attributes{
		"icon":       cty.UnknownVal(cty.String),
		"thumbnails": cty.UnknownVal(cty.List(placeThumbnailValType)),
	}
	out, err := r.Normalize(in)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range in {
		if !out[k].RawEquals(v) {
			t.Errorf("%s changed to %#v", k, out[k])
		}
	}
}
