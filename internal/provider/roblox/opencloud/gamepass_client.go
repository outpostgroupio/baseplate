package opencloud

import (
	"context"
	"net/http"
	"strconv"
)

// GamePass is the domain view of a game pass.
type GamePass struct {
	ID           string
	Name         string
	Description  string
	PriceInRobux int64
	IconAssetID  string
	IsForSale    bool
}

// GamePassCreate is the input for creating a game pass. The icon, if any, is a
// directly uploaded image file (the API assigns iconAssetId).
type GamePassCreate struct {
	Name        string
	Description string
	Price       int64
	IsForSale   bool
	IconData    []byte
	IconFile    string
	IconType    string
}

type GamePassUpdate struct {
	Name        string
	Description string
	Price       int64
	IsForSale   bool
}

type GamePassClient interface {
	Create(ctx context.Context, universeID string, p GamePassCreate) (GamePass, error)
	Get(ctx context.Context, universeID, gamePassID string) (GamePass, error)
	Update(ctx context.Context, universeID, gamePassID string, p GamePassUpdate) error
	Delete(ctx context.Context, universeID, gamePassID string) error
}

func NewGamePassClient(c *Client) GamePassClient { return &gamePassClient{c: c} }

type gamePassClient struct{ c *Client }

type gpPriceInfo struct {
	DefaultPriceInRobux *int64 `json:"defaultPriceInRobux"`
}

type gpConfigWire struct {
	GamePassId       int64        `json:"gamePassId"`
	Name             string       `json:"name"`
	Description      string       `json:"description"`
	IsForSale        bool         `json:"isForSale"`
	IconAssetId      *int64       `json:"iconAssetId"`
	PriceInformation *gpPriceInfo `json:"priceInformation"`
}

func (w gpConfigWire) toDomain() GamePass {
	gp := GamePass{
		ID:          strconv.FormatInt(w.GamePassId, 10),
		Name:        w.Name,
		Description: w.Description,
		IsForSale:   w.IsForSale,
	}
	if w.PriceInformation != nil && w.PriceInformation.DefaultPriceInRobux != nil {
		gp.PriceInRobux = *w.PriceInformation.DefaultPriceInRobux
	}
	if w.IconAssetId != nil {
		gp.IconAssetID = strconv.FormatInt(*w.IconAssetId, 10)
	}
	return gp
}

func gamePassesBase(universeID string) string {
	return "/game-passes/v1/universes/" + universeID + "/game-passes"
}

func (gc *gamePassClient) Create(ctx context.Context, universeID string, p GamePassCreate) (GamePass, error) {
	parts := []Part{
		{Name: "name", Data: []byte(p.Name)},
		{Name: "price", Data: []byte(strconv.FormatInt(p.Price, 10))},
		{Name: "isForSale", Data: []byte(strconv.FormatBool(p.IsForSale))},
	}
	if p.Description != "" {
		parts = append(parts, Part{Name: "description", Data: []byte(p.Description)})
	}
	if len(p.IconData) > 0 {
		parts = append(parts, Part{Name: "imageFile", Filename: p.IconFile, ContentType: p.IconType, Data: p.IconData})
	}
	var w gpConfigWire
	if _, err := gc.c.DoMultipart(ctx, http.MethodPost, gamePassesBase(universeID), parts, &w); err != nil {
		return GamePass{}, err
	}
	return w.toDomain(), nil
}

func (gc *gamePassClient) Get(ctx context.Context, universeID, gamePassID string) (GamePass, error) {
	var w gpConfigWire
	path := gamePassesBase(universeID) + "/" + gamePassID + "/creator"
	if _, err := gc.c.Do(ctx, http.MethodGet, path, nil, &w); err != nil {
		return GamePass{}, err
	}
	return w.toDomain(), nil
}

func (gc *gamePassClient) Update(ctx context.Context, universeID, gamePassID string, p GamePassUpdate) error {
	parts := []Part{
		{Name: "name", Data: []byte(p.Name)},
		{Name: "price", Data: []byte(strconv.FormatInt(p.Price, 10))},
		{Name: "isForSale", Data: []byte(strconv.FormatBool(p.IsForSale))},
	}
	if p.Description != "" {
		parts = append(parts, Part{Name: "description", Data: []byte(p.Description)})
	}
	path := gamePassesBase(universeID) + "/" + gamePassID
	// PATCH returns an empty body; we ignore it (the resource rebuilds state from desired).
	_, err := gc.c.DoMultipart(ctx, http.MethodPatch, path, parts, nil)
	return err
}

// Delete takes the pass off sale (game passes cannot be hard-deleted).
func (gc *gamePassClient) Delete(ctx context.Context, universeID, gamePassID string) error {
	parts := []Part{{Name: "isForSale", Data: []byte("false")}}
	path := gamePassesBase(universeID) + "/" + gamePassID
	_, err := gc.c.DoMultipart(ctx, http.MethodPatch, path, parts, nil)
	return err
}
