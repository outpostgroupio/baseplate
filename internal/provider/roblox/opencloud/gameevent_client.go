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
	"time"
)

// GameEvent is the domain view of an experience (virtual) event on the
// virtual-events/v3 API. Ids are decimal strings (the event id is a JSON string
// on the wire, the others JSON numbers). ThumbnailMediaIDs is ordered by the
// API's 0-based rank. PlaceID is "" when the API reports 0 (no venue set).
type GameEvent struct {
	ID                string
	UniverseID        string
	PlaceID           string
	Title             string
	Subtitle          string
	Description       string
	Tagline           string
	StartTime         string // as returned by the API, e.g. 2026-12-01T18:00:00.123+00:00
	EndTime           string
	Visibility        string // "public" | "private" | "moderated"
	FeaturingStatus   string // "enabled" | "disabled"
	ThumbnailMediaIDs []string
	HostType          string
	HostID            string
}

// GameEventWrite is the input to Create and Update. A nil field is omitted from
// the request body (on PATCH, omitted = "no change"). GroupID is create-only and
// ignored by Update. ThumbnailMediaIDs is sent in order with rank = position
// (0-based and contiguous: verified live, any other rank is InvalidThumbnails);
// a non-nil empty slice is sent as [] (clears the thumbnails).
//
// Categories are deliberately absent. Live, the API rejected every categories
// payload (every enum value, any rank base, even []) with InvalidCategories,
// and a create carrying them still created the event before returning 400.
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

// flexID decodes an id that may be a JSON number or a JSON string. Live, the
// event id is a string (it exceeds 2^53) and universeId/placeId/hostId/mediaId
// are numbers; accepting both keeps us robust if Roblox changes either.
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

type geThumb struct {
	MediaID flexID `json:"mediaId"`
	Rank    int32  `json:"rank"`
}

type geHost struct {
	HostType string `json:"hostType"`
	HostID   flexID `json:"hostId"`
}

type geWire struct {
	ID              flexID    `json:"id"`
	Title           string    `json:"title"`
	Subtitle        string    `json:"subtitle"`
	Description     string    `json:"description"`
	Tagline         string    `json:"tagline"`
	StartTime       string    `json:"startTime"`
	EndTime         string    `json:"endTime"`
	UniverseID      flexID    `json:"universeId"`
	PlaceID         flexID    `json:"placeId"`
	Visibility      string    `json:"visibility"`
	FeaturingStatus string    `json:"featuringStatus"`
	Host            *geHost   `json:"host"`
	Thumbnails      []geThumb `json:"thumbnails"`
}

type geListWire struct {
	GameEvents    []geWire `json:"gameEvents"`
	NextPageToken *string  `json:"nextPageToken"`
}

func (w geWire) toDomain() GameEvent {
	ev := GameEvent{
		ID: string(w.ID), UniverseID: string(w.UniverseID), PlaceID: string(w.PlaceID),
		Title: w.Title, Subtitle: w.Subtitle, Description: w.Description, Tagline: w.Tagline,
		StartTime: w.StartTime, EndTime: w.EndTime,
		Visibility: w.Visibility, FeaturingStatus: w.FeaturingStatus,
	}
	if ev.PlaceID == "0" {
		ev.PlaceID = ""
	}
	if w.Host != nil {
		ev.HostType, ev.HostID = w.Host.HostType, string(w.Host.HostID)
	}
	thumbs := append([]geThumb(nil), w.Thumbnails...)
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
	if w.ThumbnailMediaIDs != nil {
		thumbs := make([]map[string]any, 0, len(w.ThumbnailMediaIDs))
		for i, s := range w.ThumbnailMediaIDs {
			n, err := strconv.ParseInt(s, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("thumbnail media id %q is not a numeric id", s)
			}
			thumbs = append(thumbs, map[string]any{"mediaId": n, "rank": i})
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

// Get reads one event. The single-event GET always reports "thumbnails": []
// (verified live, even with ?fields=thumbnails), while the universe LIST
// endpoint returns them. So Get fetches the event, then finds it in a list
// query narrowly bracketed on its start time to recover the thumbnails.
func (gc *gameEventClient) Get(ctx context.Context, eventID string) (GameEvent, error) {
	var out geWire
	if _, err := gc.c.Do(ctx, http.MethodGet, gameEventPath(eventID), nil, &out); err != nil {
		return GameEvent{}, err
	}
	ev := out.toDomain()
	thumbs, err := gc.listThumbnails(ctx, ev)
	if err != nil {
		return GameEvent{}, err
	}
	ev.ThumbnailMediaIDs = thumbs
	return ev, nil
}

// listThumbnails looks ev up via the list endpoint (startsAfter/startsBefore
// bracketing its start, and endsAfter just before its end so the default
// "endsAfter = now" filter doesn't hide it) and returns its rank-ordered
// thumbnail media ids. An event the list doesn't return yields nil.
func (gc *gameEventClient) listThumbnails(ctx context.Context, ev GameEvent) ([]string, error) {
	start, err := time.Parse(time.RFC3339, ev.StartTime)
	if err != nil || ev.UniverseID == "" {
		return nil, nil // can't bracket the query; report no thumbnails
	}
	q := url.Values{}
	q.Set("startsAfter", start.Add(-time.Second).UTC().Format(time.RFC3339))
	q.Set("startsBefore", start.Add(time.Second).UTC().Format(time.RFC3339))
	if end, err := time.Parse(time.RFC3339, ev.EndTime); err == nil {
		q.Set("endsAfter", end.Add(-time.Second).UTC().Format(time.RFC3339))
	}
	q.Set("pageSize", "100")
	base := "/virtual-events/v3/universes/" + url.PathEscape(ev.UniverseID) + "/game-events?"
	for page := 0; page < 50; page++ {
		var lw geListWire
		if _, err := gc.c.Do(ctx, http.MethodGet, base+q.Encode(), nil, &lw); err != nil {
			return nil, err
		}
		for _, w := range lw.GameEvents {
			if string(w.ID) == ev.ID {
				return w.toDomain().ThumbnailMediaIDs, nil
			}
		}
		if lw.NextPageToken == nil || *lw.NextPageToken == "" || len(lw.GameEvents) == 0 {
			return nil, nil
		}
		q.Set("pageToken", *lw.NextPageToken)
	}
	return nil, nil
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
