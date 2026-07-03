package opencloud

import (
	"context"
	"net/http"
	"strconv"
)

// DeveloperProduct is the domain view of a developer product.
type DeveloperProduct struct {
	ID           string
	Name         string
	Description  string
	PriceInRobux int64
	UniverseID   string
	IconAssetID  string
	IsForSale    bool
}

// DeveloperProductCreate is the input for creating a product. The icon, if any,
// is a directly-uploaded image file (the API assigns the icon asset id).
type DeveloperProductCreate struct {
	Name        string
	Description string
	Price       int64
	IsForSale   bool
	IconData    []byte
	IconFile    string
	IconType    string
}

type DeveloperProductUpdate struct {
	Name        string
	Description string
	Price       int64
	IsForSale   bool
}

type DeveloperProductClient interface {
	Create(ctx context.Context, universeID string, p DeveloperProductCreate) (DeveloperProduct, error)
	Get(ctx context.Context, universeID, productID string) (DeveloperProduct, error)
	Update(ctx context.Context, universeID, productID string, p DeveloperProductUpdate) (DeveloperProduct, error)
	Delete(ctx context.Context, universeID, productID string) error
}

func NewDeveloperProductClient(c *Client) DeveloperProductClient { return &productClient{c: c} }

type productClient struct{ c *Client }

type dpPriceInfo struct {
	DefaultPriceInRobux *int64 `json:"defaultPriceInRobux"`
}

type dpConfigWire struct {
	ProductId        int64        `json:"productId"`
	Name             string       `json:"name"`
	Description      string       `json:"description"`
	UniverseId       int64        `json:"universeId"`
	IsForSale        bool         `json:"isForSale"`
	IconImageAssetId *int64       `json:"iconImageAssetId"`
	PriceInformation *dpPriceInfo `json:"priceInformation"`
}

func (w dpConfigWire) toDomain() DeveloperProduct {
	dp := DeveloperProduct{
		ID:          strconv.FormatInt(w.ProductId, 10),
		Name:        w.Name,
		Description: w.Description,
		IsForSale:   w.IsForSale,
		UniverseID:  strconv.FormatInt(w.UniverseId, 10),
	}
	if w.PriceInformation != nil && w.PriceInformation.DefaultPriceInRobux != nil {
		dp.PriceInRobux = *w.PriceInformation.DefaultPriceInRobux
	}
	if w.IconImageAssetId != nil {
		dp.IconAssetID = strconv.FormatInt(*w.IconImageAssetId, 10)
	}
	return dp
}

func productsBase(universeID string) string {
	return "/developer-products/v2/universes/" + universeID + "/developer-products"
}

func (pc *productClient) Create(ctx context.Context, universeID string, p DeveloperProductCreate) (DeveloperProduct, error) {
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
	var w dpConfigWire
	if _, err := pc.c.DoMultipart(ctx, http.MethodPost, productsBase(universeID), parts, &w); err != nil {
		return DeveloperProduct{}, err
	}
	return w.toDomain(), nil
}

func (pc *productClient) Get(ctx context.Context, universeID, productID string) (DeveloperProduct, error) {
	var w dpConfigWire
	path := productsBase(universeID) + "/" + productID + "/creator"
	if _, err := pc.c.Do(ctx, http.MethodGet, path, nil, &w); err != nil {
		return DeveloperProduct{}, err
	}
	return w.toDomain(), nil
}

func (pc *productClient) Update(ctx context.Context, universeID, productID string, p DeveloperProductUpdate) (DeveloperProduct, error) {
	parts := []Part{
		{Name: "name", Data: []byte(p.Name)},
		{Name: "price", Data: []byte(strconv.FormatInt(p.Price, 10))},
		{Name: "isForSale", Data: []byte(strconv.FormatBool(p.IsForSale))},
	}
	if p.Description != "" {
		parts = append(parts, Part{Name: "description", Data: []byte(p.Description)})
	}
	var w dpConfigWire
	path := productsBase(universeID) + "/" + productID
	if _, err := pc.c.DoMultipart(ctx, http.MethodPatch, path, parts, &w); err != nil {
		return DeveloperProduct{}, err
	}
	return w.toDomain(), nil
}

// Delete deactivates the product (developer products cannot be hard-deleted) by
// taking it off sale.
func (pc *productClient) Delete(ctx context.Context, universeID, productID string) error {
	parts := []Part{{Name: "isForSale", Data: []byte("false")}}
	path := productsBase(universeID) + "/" + productID
	_, err := pc.c.DoMultipart(ctx, http.MethodPatch, path, parts, nil)
	return err
}
