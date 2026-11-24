package opencloud

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
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
	// UpdateContent uploads new content for an existing asset (and its
	// metadata) in one PATCH, creating a new version under the same id. Open
	// Cloud only accepts new content for Model assets.
	UpdateContent(ctx context.Context, a Asset, fileName, contentType string, data []byte) (Asset, error)
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

// assetUpdateRequest is the `request` part of an asset PATCH. displayName and
// description are sent even when empty: the updateMask names them, so an empty
// value clears the field.
type assetUpdateRequest struct {
	AssetType   string `json:"assetType,omitempty"`
	AssetID     string `json:"assetId"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
}

type assetResponse struct {
	// AssetID is a string on the wire: Roblox serializes 64-bit ids as JSON
	// strings (to avoid float precision loss) even though the OpenCloud spec
	// types it as int64. Decoding it as int64 fails against the live API.
	AssetID     string `json:"assetId,omitempty"`
	Path        string `json:"path,omitempty"`
	AssetType   string `json:"assetType,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
	Description string `json:"description,omitempty"`
}

func (r assetResponse) toAsset(fallbackID string) Asset {
	id := r.AssetID
	if id == "" && r.Path != "" {
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
	res, err := ac.await(ctx, "asset create", op)
	if err != nil {
		return Asset{}, err
	}
	a := res.toAsset("")
	if a.ID == "" {
		return Asset{}, fmt.Errorf("asset create: operation completed without an assetId")
	}
	return a, nil
}

// await resolves an asset Operation: a completed one carries its result inline,
// a pending one is polled until done.
func (ac *assetClient) await(ctx context.Context, what string, op operation) (assetResponse, error) {
	var res assetResponse
	switch {
	case op.Done:
		if err := op.failure(); err != nil {
			return res, err
		}
		if len(op.Response) > 0 {
			if err := json.Unmarshal(op.Response, &res); err != nil {
				return res, err
			}
		}
	case op.Path != "":
		if err := ac.c.PollOperation(ctx, assetsPollPath(op.Path), &res); err != nil {
			return res, err
		}
	default:
		return res, fmt.Errorf("%s: response was neither a completed operation nor a pollable operation path", what)
	}
	return res, nil
}

func (ac *assetClient) Get(ctx context.Context, assetID string) (Asset, error) {
	var res assetResponse
	if _, err := ac.c.Do(ctx, http.MethodGet, "/v1/assets/"+assetID, nil, &res); err != nil {
		return Asset{}, err
	}
	return res.toAsset(assetID), nil
}

// Update changes an asset's metadata. The endpoint is multipart (a `request`
// JSON part; content updates via `fileContent` are Model-only, so none is sent),
// takes an updateMask, and returns an Operation.
func (ac *assetClient) Update(ctx context.Context, a Asset) (Asset, error) {
	body := assetUpdateRequest{AssetID: a.ID, DisplayName: a.DisplayName, Description: a.Description}
	var op operation
	path := "/v1/assets/" + url.PathEscape(a.ID) + "?updateMask=displayName,description"
	if _, err := ac.c.DoMultipart(ctx, http.MethodPatch, path, []Part{{Name: "request", JSON: body}}, &op); err != nil {
		return Asset{}, err
	}
	res, err := ac.await(ctx, "asset update", op)
	if err != nil {
		return Asset{}, err
	}
	return res.toAsset(a.ID), nil
}

// UpdateContent is Update plus a `fileContent` part. The new content becomes a
// new version of the same asset; the spec and creator docs say only Model
// assets accept it (Image/Decal/Audio/Video can't take new content).
func (ac *assetClient) UpdateContent(ctx context.Context, a Asset, fileName, contentType string, data []byte) (Asset, error) {
	body := assetUpdateRequest{AssetType: a.AssetType, AssetID: a.ID, DisplayName: a.DisplayName, Description: a.Description}
	var op operation
	path := "/v1/assets/" + url.PathEscape(a.ID) + "?updateMask=displayName,description"
	parts := []Part{
		{Name: "request", JSON: body},
		{Name: "fileContent", Filename: fileName, ContentType: contentType, Data: data},
	}
	if _, err := ac.c.DoMultipart(ctx, http.MethodPatch, path, parts, &op); err != nil {
		return Asset{}, err
	}
	res, err := ac.await(ctx, "asset content update", op)
	if err != nil {
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
