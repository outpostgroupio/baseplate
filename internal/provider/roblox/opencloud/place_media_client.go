package opencloud

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// PlaceMedia is a place's icon and detail-page thumbnails, as the Assets API
// reports them on the place asset. On the root place they are the experience's
// icon and thumbnails.
type PlaceMedia struct {
	// AssetType is the asset's type ("Place" for a place). Read-only.
	AssetType string
	// Icon is the icon's image asset id; "" means no icon.
	Icon string
	// Thumbnails is the ordered thumbnail list (the order on the game page).
	Thumbnails []PlaceThumbnail
}

// PlaceThumbnail is one detail-page thumbnail. AltText "" means none: Roblox
// stores an empty alt text as absent.
type PlaceThumbnail struct {
	AssetID string
	AltText string
}

// PlaceMediaClient reads and replaces a place's icon and thumbnails through the
// place asset (GET/PATCH /assets/v1/assets/{placeId} with readMask/updateMask
// icon and previews). It works with an API key holding asset:read/asset:write.
type PlaceMediaClient interface {
	Get(ctx context.Context, placeID string) (PlaceMedia, error)
	// Update writes the fields named in mask ("icon", "previews"). previews
	// replaces the whole list; an empty Icon removes the icon.
	Update(ctx context.Context, placeID string, m PlaceMedia, mask []string) error
}

// NewPlaceMediaClient builds a PlaceMediaClient over the assets client (base
// https://apis.roblox.com/assets).
func NewPlaceMediaClient(c *Client) PlaceMediaClient {
	return &placeMediaClient{ac: &assetClient{c: c}}
}

type placeMediaClient struct{ ac *assetClient }

type placePreviewWire struct {
	Asset   string `json:"asset"`
	AltText string `json:"altText,omitempty"`
}

type placeMediaWire struct {
	AssetID   string `json:"assetId,omitempty"`
	AssetType string `json:"assetType,omitempty"`
	// Pointers so an update can send "" (remove the icon) and [] (clear the
	// thumbnails), and leave out fields that aren't in the mask.
	Icon     *string             `json:"icon,omitempty"`
	Previews *[]placePreviewWire `json:"previews,omitempty"`
}

func (pm *placeMediaClient) Get(ctx context.Context, placeID string) (PlaceMedia, error) {
	var w placeMediaWire
	path := "/v1/assets/" + url.PathEscape(placeID) + "?readMask=assetType,icon,previews"
	if _, err := pm.ac.c.Do(ctx, http.MethodGet, path, nil, &w); err != nil {
		return PlaceMedia{}, err
	}
	m := PlaceMedia{AssetType: w.AssetType, Thumbnails: []PlaceThumbnail{}}
	if w.Icon != nil {
		m.Icon = strings.TrimPrefix(*w.Icon, "assets/")
	}
	if w.Previews != nil {
		for _, p := range *w.Previews {
			m.Thumbnails = append(m.Thumbnails, PlaceThumbnail{AssetID: strings.TrimPrefix(p.Asset, "assets/"), AltText: p.AltText})
		}
	}
	return m, nil
}

func (pm *placeMediaClient) Update(ctx context.Context, placeID string, m PlaceMedia, mask []string) error {
	body := placeMediaWire{AssetID: placeID}
	for _, f := range mask {
		switch f {
		case "icon":
			icon := ""
			if m.Icon != "" {
				icon = "assets/" + m.Icon
			}
			body.Icon = &icon
		case "previews":
			previews := make([]placePreviewWire, 0, len(m.Thumbnails))
			for _, t := range m.Thumbnails {
				previews = append(previews, placePreviewWire{Asset: "assets/" + t.AssetID, AltText: t.AltText})
			}
			body.Previews = &previews
		default:
			return fmt.Errorf("place media update: unknown field %q", f)
		}
	}
	path := "/v1/assets/" + url.PathEscape(placeID) + "?updateMask=" + strings.Join(mask, ",")
	var op operation
	if _, err := pm.ac.c.DoMultipart(ctx, http.MethodPatch, path, []Part{{Name: "request", JSON: body}}, &op); err != nil {
		return err
	}
	_, err := pm.ac.await(ctx, "place media update", op)
	return err
}
