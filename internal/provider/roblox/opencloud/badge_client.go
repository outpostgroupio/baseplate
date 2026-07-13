package opencloud

import (
	"context"
	"errors"
	"net/http"
	"strconv"
)

// Badge is the domain view of a Roblox badge.
type Badge struct {
	ID          string
	Name        string
	Description string
	Enabled     bool
	IconImageID string
}

// BadgeCreate is the input for creating a badge. The icon, if any, is uploaded as
// the multipart "files" part. PaymentSourceType is 1 (User) or 2 (Group); it and
// ExpectedCost cover the legacy Robux-cost surface (0 in practice today).
type BadgeCreate struct {
	Name              string
	Description       string
	IsActive          bool
	PaymentSourceType int
	ExpectedCost      int64
	IconData          []byte
	IconFile          string
	IconType          string
}

// BadgeUpdate is the JSON PATCH body: name/description/enabled.
type BadgeUpdate struct {
	Name        string
	Description string
	Enabled     bool
}

type BadgeClient interface {
	Create(ctx context.Context, universeID string, b BadgeCreate) (Badge, error)
	Get(ctx context.Context, universeID, badgeID string) (Badge, error)
	Update(ctx context.Context, badgeID string, b BadgeUpdate) error
	UploadIcon(ctx context.Context, badgeID string, data []byte, filename, contentType string) error
}

// NewBadgeClient builds a BadgeClient. write serves the Open Cloud legacy-bridge
// create/update/icon paths (apis.roblox.com); read serves the public single-GET
// badge read (badges.roblox.com) — a different host, so it's a separate client.
func NewBadgeClient(write, read *Client) BadgeClient { return &badgeClient{write: write, read: read} }

type badgeClient struct {
	write *Client
	read  *Client
}

// badgeWire decodes BadgeResponseV2. id/iconImageId are JSON numbers (int64,
// Task 1-confirmed); decode into int64 and format to string (never
// interface{}/float64 — big ids lose precision).
type badgeWire struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
	IconImageID int64  `json:"iconImageId"`
}

func (w badgeWire) toDomain() Badge {
	b := Badge{
		ID:          strconv.FormatInt(w.ID, 10),
		Name:        w.Name,
		Description: w.Description,
		Enabled:     w.Enabled,
	}
	if w.IconImageID != 0 {
		b.IconImageID = strconv.FormatInt(w.IconImageID, 10)
	}
	return b
}

func (bc *badgeClient) Create(ctx context.Context, universeID string, b BadgeCreate) (Badge, error) {
	parts := []Part{
		{Name: "name", Data: []byte(b.Name)},
		{Name: "isActive", Data: []byte(strconv.FormatBool(b.IsActive))},
		{Name: "paymentSourceType", Data: []byte(strconv.Itoa(b.PaymentSourceType))},
		{Name: "expectedCost", Data: []byte(strconv.FormatInt(b.ExpectedCost, 10))},
	}
	if b.Description != "" {
		parts = append(parts, Part{Name: "description", Data: []byte(b.Description)})
	}
	if len(b.IconData) > 0 {
		parts = append(parts, Part{Name: "files", Filename: b.IconFile, ContentType: b.IconType, Data: b.IconData})
	}
	var w badgeWire
	if _, err := bc.write.DoMultipart(ctx, http.MethodPost, "/legacy-badges/v1/universes/"+universeID+"/badges", parts, &w); err != nil {
		return Badge{}, err
	}
	return w.toDomain(), nil
}

// Get reads the badge via the public single-GET on badges.roblox.com (the read
// client's host). universeID is unused (the read is by badge id) but kept in the
// signature for interface stability. A 404 maps to the not-found sentinel.
func (bc *badgeClient) Get(ctx context.Context, universeID, badgeID string) (Badge, error) {
	_ = universeID
	var w badgeWire
	if _, err := bc.read.Do(ctx, http.MethodGet, "/v1/badges/"+badgeID, nil, &w); err != nil {
		if IsNotFound(err) {
			return Badge{}, errBadgeNotFound
		}
		return Badge{}, err
	}
	return w.toDomain(), nil
}

var errBadgeNotFound = errors.New("roblox_badge: not found")

// ErrBadgeNotFound reports whether err is the not-found sentinel, so the resource
// can map it to provider.ErrNotFound.
func ErrBadgeNotFound(err error) bool { return errors.Is(err, errBadgeNotFound) }

// SentinelNotFound returns the not-found error so tests and the resource can
// return/compare it (the resource maps it to provider.ErrNotFound).
func SentinelNotFound() error { return errBadgeNotFound }

func (bc *badgeClient) Update(ctx context.Context, badgeID string, b BadgeUpdate) error {
	body := map[string]any{"name": b.Name, "description": b.Description, "enabled": b.Enabled}
	_, err := bc.write.Do(ctx, http.MethodPatch, "/legacy-badges/v1/badges/"+badgeID, body, nil)
	return err
}

// UploadIcon overwrites the badge icon. NOTE the field name here is capital
// "Files" (Task 1-confirmed), asymmetric with create's lowercase "files".
func (bc *badgeClient) UploadIcon(ctx context.Context, badgeID string, data []byte, filename, contentType string) error {
	parts := []Part{{Name: "Files", Filename: filename, ContentType: contentType, Data: data}}
	_, err := bc.write.DoMultipart(ctx, http.MethodPost, "/legacy-publish/v1/badges/"+badgeID+"/icon", parts, nil)
	return err
}
