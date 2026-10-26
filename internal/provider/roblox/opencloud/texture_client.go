package opencloud

import (
	"context"
	"errors"
	"net/http"
	"net/url"
)

// TextureScope is the API-key scope DecalTextureID needs.
const TextureScope = "creator-store-product:read"

// ErrNoTextureID is returned by DecalTextureID when the asset was found but
// the response carries no texture id.
var ErrNoTextureID = errors.New("the toolbox-service response has no textureId")

// TextureClient resolves the texture behind a Decal asset. A Decal's asset id
// is not a texture id: each Decal wraps a separate Image asset, and no Assets
// API field exposes it. toolbox-service's asset details do, as asset.textureId.
type TextureClient interface {
	// DecalTextureID returns the id of the Image asset behind decal decalID.
	// The endpoint (GET /toolbox-service/v2/assets/{id}, BETA) needs the
	// creator-store-product:read scope. It works for the key owner's own
	// private decals too, not only Creator Store items (verified live); it
	// 404s for an Image id and for an asset it can't see.
	DecalTextureID(ctx context.Context, decalID string) (string, error)
}

// NewTextureClient takes a client for the apis.roblox.com root.
func NewTextureClient(c *Client) TextureClient { return &textureClient{c: c} }

type textureClient struct{ c *Client }

type toolboxAssetResponse struct {
	Asset struct {
		// TextureID is a JSON number (verified live); idString also accepts a
		// JSON string, as Roblox serializes some 64-bit ids that way.
		TextureID *idString `json:"textureId"`
	} `json:"asset"`
}

func (tc *textureClient) DecalTextureID(ctx context.Context, decalID string) (string, error) {
	var res toolboxAssetResponse
	if _, err := tc.c.Do(ctx, http.MethodGet, "/toolbox-service/v2/assets/"+url.PathEscape(decalID), nil, &res); err != nil {
		return "", err
	}
	if res.Asset.TextureID == nil {
		return "", ErrNoTextureID
	}
	switch id := string(*res.Asset.TextureID); id {
	case "", "0":
		return "", ErrNoTextureID
	default:
		return id, nil
	}
}
