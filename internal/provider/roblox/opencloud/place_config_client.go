package opencloud

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

// PlaceConfig holds a place's writable Open Cloud v2 configuration. A nil field
// is absent from the response (or, on update, left untouched).
type PlaceConfig struct {
	DisplayName *string
	Description *string
	ServerSize  *int64
}

// PlaceConfigClient reads and patches a place's configuration via
// /cloud/v2/universes/{universeId}/places/{placeId}. It configures an existing
// place; places are not created or deleted here.
type PlaceConfigClient interface {
	Get(ctx context.Context, universeID, placeID string) (PlaceConfig, error)
	Update(ctx context.Context, universeID, placeID string, p PlaceConfig, mask []string) (PlaceConfig, error)
}

// NewPlaceConfigClient builds a PlaceConfigClient over the root Open Cloud
// client (base https://apis.roblox.com).
func NewPlaceConfigClient(c *Client) PlaceConfigClient { return &placeConfigClient{c: c} }

type placeConfigClient struct{ c *Client }

type placeConfigWire struct {
	DisplayName *string `json:"displayName,omitempty"`
	Description *string `json:"description,omitempty"`
	ServerSize  *int64  `json:"serverSize,omitempty"`
}

func (w placeConfigWire) toPlaceConfig() PlaceConfig {
	return PlaceConfig{DisplayName: w.DisplayName, Description: w.Description, ServerSize: w.ServerSize}
}

func placeConfigPath(universeID, placeID string) string {
	return "/cloud/v2/universes/" + url.PathEscape(universeID) + "/places/" + url.PathEscape(placeID)
}

func (pc *placeConfigClient) Get(ctx context.Context, universeID, placeID string) (PlaceConfig, error) {
	var w placeConfigWire
	if _, err := pc.c.Do(ctx, http.MethodGet, placeConfigPath(universeID, placeID), nil, &w); err != nil {
		return PlaceConfig{}, err
	}
	return w.toPlaceConfig(), nil
}

func (pc *placeConfigClient) Update(ctx context.Context, universeID, placeID string, p PlaceConfig, mask []string) (PlaceConfig, error) {
	body := placeConfigWire{DisplayName: p.DisplayName, Description: p.Description, ServerSize: p.ServerSize}
	path := placeConfigPath(universeID, placeID) + "?updateMask=" + strings.Join(mask, ",")
	var w placeConfigWire
	if _, err := pc.c.Do(ctx, http.MethodPatch, path, body, &w); err != nil {
		return PlaceConfig{}, err
	}
	return w.toPlaceConfig(), nil
}
