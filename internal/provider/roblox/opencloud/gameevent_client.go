package opencloud

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
)

// GameEvent is the domain view of an experience (virtual) event on the
// virtual-events/v3 API. Ids are decimal strings. Categories and
// ThumbnailMediaIDs are ordered by the API's rank (lowest rank first).
type GameEvent struct {
	ID                string
	UniverseID        string
	PlaceID           string
	Title             string
	Subtitle          string
	Description       string
	Tagline           string
	StartTime         string // as returned by the API (RFC 3339)
	EndTime           string
	Visibility        string // "public" | "private" | "moderated"
	FeaturingStatus   string // "enabled" | "disabled"
	Categories        []string
	ThumbnailMediaIDs []string
	HostType          string
	HostID            string
}

// GameEventWrite is the input to Create and Update. A nil field is omitted from
// the request body (on PATCH, omitted = "no change"). GroupID is create-only and
// ignored by Update. Categories and ThumbnailMediaIDs are sent in order, with
// rank = position + 1; a non-nil empty slice is sent as [] (clears the list).
type GameEventWrite struct {
	Title             *string
	Subtitle          *string
	Description       *string
	Tagline           *string
	StartTime         *string
	EndTime           *string
	Visibility        *string
	FeaturingStatus   *string
	PlaceID           *string
	GroupID           *string
	Categories        []string
	ThumbnailMediaIDs []string
}

// GameEventClient manages experience events (virtual-events/v3).
type GameEventClient interface {
	Create(ctx context.Context, universeID string, w GameEventWrite) (GameEvent, error)
	Get(ctx context.Context, eventID string) (GameEvent, error)
	Update(ctx context.Context, eventID string, w GameEventWrite) (GameEvent, error)
	Delete(ctx context.Context, eventID string) error
}

func NewGameEventClient(c *Client) GameEventClient { return &gameEventClient{c: c} }

type gameEventClient struct{ c *Client }

// flexID decodes an id that the API may serialize as a JSON number or a JSON
// string (the spec declares event ids as strings because they exceed 2^53, and
// the other ids as int64 numbers; Roblox has been inconsistent elsewhere — cf.
// assetId — so accept both).
type flexID string

func (f *flexID) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if bytes.Equal(b, []byte("null")) {
		*f = ""
		return nil
	}
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*f = flexID(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*f = flexID(n.String())
	return nil
}

type geRanked struct {
	Category string `json:"category,omitempty"`
	MediaID  flexID `json:"mediaId,omitempty"`
	Rank     int32  `json:"rank"`
}

type geHost struct {
	HostType string `json:"hostType"`
	HostID   flexID `json:"hostId"`
}

type geWire struct {
	ID              flexID     `json:"id"`
	Title           string     `json:"title"`
	Subtitle        string     `json:"subtitle"`
	Description     string     `json:"description"`
	Tagline         string     `json:"tagline"`
	StartTime       string     `json:"startTime"`
	EndTime         string     `json:"endTime"`
	UniverseID      flexID     `json:"universeId"`
	PlaceID         flexID     `json:"placeId"`
	Visibility      string     `json:"visibility"`
	FeaturingStatus string     `json:"featuringStatus"`
	Host            *geHost    `json:"host"`
	Categories      []geRanked `json:"categories"`
	Thumbnails      []geRanked `json:"thumbnails"`
}

func (w geWire) toDomain() GameEvent {
	ev := GameEvent{
		ID: string(w.ID), UniverseID: string(w.UniverseID), PlaceID: string(w.PlaceID),
		Title: w.Title, Subtitle: w.Subtitle, Description: w.Description, Tagline: w.Tagline,
		StartTime: w.StartTime, EndTime: w.EndTime,
		Visibility: w.Visibility, FeaturingStatus: w.FeaturingStatus,
	}
	if w.Host != nil {
		ev.HostType, ev.HostID = w.Host.HostType, string(w.Host.HostID)
	}
	cats := append([]geRanked(nil), w.Categories...)
	sort.SliceStable(cats, func(i, j int) bool { return cats[i].Rank < cats[j].Rank })
	for _, c := range cats {
		ev.Categories = append(ev.Categories, c.Category)
	}
	thumbs := append([]geRanked(nil), w.Thumbnails...)
	sort.SliceStable(thumbs, func(i, j int) bool { return thumbs[i].Rank < thumbs[j].Rank })
	for _, t := range thumbs {
		ev.ThumbnailMediaIDs = append(ev.ThumbnailMediaIDs, string(t.MediaID))
	}
	return ev
}

// body builds the JSON request. Ids go out as JSON numbers (the request schema
// declares int64). create selects whether create-only fields (groupId) are sent.
func (w GameEventWrite) body(create bool) (map[string]any, error) {
	m := map[string]any{}
	put := func(k string, v *string) {
		if v != nil {
			m[k] = *v
		}
	}
	put("title", w.Title)
	put("subtitle", w.Subtitle)
	put("description", w.Description)
	put("tagline", w.Tagline)
	put("startTime", w.StartTime)
	put("endTime", w.EndTime)
	put("visibility", w.Visibility)
	put("featuringStatus", w.FeaturingStatus)
	putID := func(k string, v *string) error {
		if v == nil {
			return nil
		}
		n, err := strconv.ParseInt(*v, 10, 64)
		if err != nil {
			return fmt.Errorf("%s %q is not a numeric id", k, *v)
		}
		m[k] = n
		return nil
	}
	if err := putID("placeId", w.PlaceID); err != nil {
		return nil, err
	}
	if create {
		if err := putID("groupId", w.GroupID); err != nil {
			return nil, err
		}
	}
	if w.Categories != nil {
		cats := make([]map[string]any, 0, len(w.Categories))
		for i, c := range w.Categories {
			cats = append(cats, map[string]any{"category": c, "rank": i + 1})
		}
		m["categories"] = cats
	}
	if w.ThumbnailMediaIDs != nil {
		thumbs := make([]map[string]any, 0, len(w.ThumbnailMediaIDs))
		for i, s := range w.ThumbnailMediaIDs {
			n, err := strconv.ParseInt(s, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("thumbnail media id %q is not a numeric id", s)
			}
			thumbs = append(thumbs, map[string]any{"mediaId": n, "rank": i + 1})
		}
		m["thumbnails"] = thumbs
	}
	return m, nil
}

func (gc *gameEventClient) Create(ctx context.Context, universeID string, w GameEventWrite) (GameEvent, error) {
	body, err := w.body(true)
	if err != nil {
		return GameEvent{}, err
	}
	var out geWire
	path := "/virtual-events/v3/universes/" + url.PathEscape(universeID) + "/game-events"
	if _, err := gc.c.Do(ctx, http.MethodPost, path, body, &out); err != nil {
		return GameEvent{}, err
	}
	return out.toDomain(), nil
}

func (gc *gameEventClient) Get(ctx context.Context, eventID string) (GameEvent, error) {
	var out geWire
	if _, err := gc.c.Do(ctx, http.MethodGet, gameEventPath(eventID), nil, &out); err != nil {
		return GameEvent{}, err
	}
	return out.toDomain(), nil
}

func (gc *gameEventClient) Update(ctx context.Context, eventID string, w GameEventWrite) (GameEvent, error) {
	body, err := w.body(false)
	if err != nil {
		return GameEvent{}, err
	}
	var out geWire
	if _, err := gc.c.Do(ctx, http.MethodPatch, gameEventPath(eventID), body, &out); err != nil {
		return GameEvent{}, err
	}
	return out.toDomain(), nil
}

// Delete permanently deletes the event (204 No Content).
func (gc *gameEventClient) Delete(ctx context.Context, eventID string) error {
	_, err := gc.c.Do(ctx, http.MethodDelete, gameEventPath(eventID), nil, nil)
	return err
}

func gameEventPath(eventID string) string {
	return "/virtual-events/v3/game-events/" + url.PathEscape(eventID)
}
