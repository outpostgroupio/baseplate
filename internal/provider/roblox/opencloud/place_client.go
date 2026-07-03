package opencloud

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// PlaceClient publishes place files to an existing Roblox place. The universe and
// place ids identify the target; the place is not created here.
type PlaceClient interface {
	// Publish uploads data (a .rbxl/.rbxlx) as a new version of the place and
	// returns the new version number. contentType is the raw body type
	// (application/octet-stream for .rbxl, application/xml for .rbxlx).
	Publish(ctx context.Context, universeID, placeID, versionType, contentType string, data []byte) (int64, error)
}

type placeClient struct{ c *Client }

// NewPlaceClient builds a PlaceClient over the root Open Cloud client
// (base https://apis.roblox.com; the publish path is /universes/v1/...).
func NewPlaceClient(c *Client) PlaceClient { return &placeClient{c: c} }

func (pc *placeClient) Publish(ctx context.Context, universeID, placeID, versionType, contentType string, data []byte) (int64, error) {
	if versionType == "" {
		versionType = "Published"
	}
	path := fmt.Sprintf("/universes/v1/%s/places/%s/versions?versionType=%s",
		url.PathEscape(universeID), url.PathEscape(placeID), url.QueryEscape(versionType))
	var res struct {
		VersionNumber int64 `json:"versionNumber"`
	}
	if _, err := pc.c.DoRaw(ctx, http.MethodPost, path, contentType, data, &res); err != nil {
		return 0, err
	}
	return res.VersionNumber, nil
}
