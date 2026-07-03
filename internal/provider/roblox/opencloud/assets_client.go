package opencloud

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// Asset is the domain view of a Roblox asset.
type Asset struct {
	ID          string
	DisplayName string
	Description string
	AssetType   string
}

// AssetCreate is the input for creating an asset. Exactly one of CreatorUserID
// or CreatorGroupID must be non-zero (assets are user- or group-owned).
type AssetCreate struct {
	AssetType      string
	DisplayName    string
	Description    string
	CreatorUserID  int64
	CreatorGroupID int64
}

type AssetClient interface {
	CreateAndAwait(ctx context.Context, req AssetCreate, fileName, contentType string, data []byte) (Asset, error)
	Get(ctx context.Context, assetID string) (Asset, error)
	Update(ctx context.Context, a Asset) (Asset, error)
	Delete(ctx context.Context, assetID string) error
}

func NewAssetClient(c *Client) AssetClient { return &assetClient{c: c} }

type assetClient struct{ c *Client }

type assetCreator struct {
	UserID  int64 `json:"userId,omitempty"`
	GroupID int64 `json:"groupId,omitempty"`
}

type assetCreationContext struct {
	Creator assetCreator `json:"creator"`
}

type assetRequest struct {
	AssetType       string               `json:"assetType,omitempty"`
	DisplayName     string               `json:"displayName,omitempty"`
	Description     string               `json:"description,omitempty"`
	CreationContext assetCreationContext `json:"creationContext,omitempty"`
}

type assetResponse struct {
	AssetID     int64  `json:"assetId,omitempty"`
	Path        string `json:"path,omitempty"`
	AssetType   string `json:"assetType,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
	Description string `json:"description,omitempty"`
}

func (r assetResponse) toAsset(fallbackID string) Asset {
	id := ""
	if r.AssetID != 0 {
		id = strconv.FormatInt(r.AssetID, 10)
	} else if r.Path != "" {
		id = strings.TrimPrefix(r.Path, "assets/")
	}
	if id == "" {
		id = fallbackID
	}
	return Asset{ID: id, DisplayName: r.DisplayName, Description: r.Description, AssetType: r.AssetType}
}

func (ac *assetClient) CreateAndAwait(ctx context.Context, req AssetCreate, fileName, contentType string, data []byte) (Asset, error) {
	body := assetRequest{
		AssetType:       req.AssetType,
		DisplayName:     req.DisplayName,
		Description:     req.Description,
		CreationContext: assetCreationContext{Creator: assetCreator{UserID: req.CreatorUserID, GroupID: req.CreatorGroupID}},
	}
	parts := []Part{
		{Name: "request", JSON: body},
		{Name: "fileContent", Filename: fileName, ContentType: contentType, Data: data},
	}
	var op operation
	if _, err := ac.c.DoMultipart(ctx, http.MethodPost, "/v1/assets", parts, &op); err != nil {
		return Asset{}, err
	}
	var res assetResponse
	switch {
	case op.Done:
		if len(op.Response) > 0 {
			if err := json.Unmarshal(op.Response, &res); err != nil {
				return Asset{}, err
			}
		}
	case op.Path != "":
		if err := ac.c.PollOperation(ctx, assetsPollPath(op.Path), &res); err != nil {
			return Asset{}, err
		}
	default:
		return Asset{}, fmt.Errorf("asset create: response was neither a completed operation nor a pollable operation path")
	}
	a := res.toAsset("")
	if a.ID == "" {
		return Asset{}, fmt.Errorf("asset create: operation completed without an assetId")
	}
	return a, nil
}

func (ac *assetClient) Get(ctx context.Context, assetID string) (Asset, error) {
	var res assetResponse
	if _, err := ac.c.Do(ctx, http.MethodGet, "/v1/assets/"+assetID, nil, &res); err != nil {
		return Asset{}, err
	}
	return res.toAsset(assetID), nil
}

func (ac *assetClient) Update(ctx context.Context, a Asset) (Asset, error) {
	body := assetResponse{DisplayName: a.DisplayName, Description: a.Description}
	var res assetResponse
	if _, err := ac.c.Do(ctx, http.MethodPatch, "/v1/assets/"+a.ID, body, &res); err != nil {
		return Asset{}, err
	}
	return res.toAsset(a.ID), nil
}

func (ac *assetClient) Delete(ctx context.Context, assetID string) error {
	_, err := ac.c.Do(ctx, http.MethodPost, "/v1/assets/"+assetID+":archive", struct{}{}, nil)
	return err
}

// assetsPollPath normalizes an operation path returned by the assets API into a
// path relative to the assets base URL (which already includes "/assets").
func assetsPollPath(p string) string {
	if strings.HasPrefix(p, "/") {
		return p
	}
	return "/v1/" + p
}
