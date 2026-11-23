package opencloud

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// eventBody mirrors a LIVE GameEventResponse (2026-10-23): the event id is a JSON
// string, the other ids JSON numbers, times "+00:00" with milliseconds, and the
// single-event GET always reports thumbnails as [].
const eventBody = `{"id":"9007199254740993123","title":"Double XP","subtitle":"Weekend",` +
	`"description":"d","tagline":"t","startTime":"2026-12-01T18:00:00.123+00:00","endTime":"2026-12-01T20:00:00+00:00",` +
	`"universeId":1234567890,"placeId":123456789012,"visibility":"private","featuringStatus":"disabled",` +
	`"host":{"hostType":"user","hostId":1122334455,"hostName":"x","hasVerifiedBadge":false},` +
	`"categories":[],"thumbnails":[]}`

// listBody is the list endpoint, which (unlike the single GET) does return the
// thumbnails, out of rank order here to prove the client sorts them.
const listBody = `{"gameEvents":[{"id":"1","thumbnails":[]},` +
	`{"id":"9007199254740993123","thumbnails":[{"mediaId":222,"rank":1},{"mediaId":111,"rank":0}]}],"nextPageToken":null}`

func TestGameEventCreateGetUpdateDelete(t *testing.T) {
	const evPath = "/virtual-events/v3/game-events/9007199254740993123"
	const colPath = "/virtual-events/v3/universes/1234567890/game-events"
	var createBody, patchBody map[string]any
	var listQuery string
	var deleted bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "k" {
			http.Error(w, "no key", 401)
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == colPath:
			if ct := r.Header.Get("Content-Type"); ct != "application/json" {
				http.Error(w, "content-type "+ct, 400)
				return
			}
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &createBody)
			w.Write([]byte(eventBody))
		case r.Method == http.MethodGet && r.URL.Path == colPath:
			listQuery = r.URL.RawQuery
			w.Write([]byte(listBody))
		case r.Method == http.MethodGet && r.URL.Path == evPath:
			w.Write([]byte(eventBody))
		case r.Method == http.MethodPatch && r.URL.Path == evPath:
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &patchBody)
			w.Write([]byte(eventBody))
		case r.Method == http.MethodDelete && r.URL.Path == evPath:
			deleted = true
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, 404)
		}
	}))
	defer srv.Close()

	gc := NewGameEventClient(New("k", srv.URL, WithSleeper(func(time.Duration) {})))
	ctx := context.Background()
	s := func(v string) *string { return &v }

	got, err := gc.Create(ctx, "1234567890", GameEventWrite{
		Title: s("Double XP"), Subtitle: s("Weekend"),
		StartTime: s("2026-12-01T18:00:00Z"), EndTime: s("2026-12-01T20:00:00Z"),
		Visibility: s("private"), PlaceID: s("123456789012"), GroupID: s("77"),
		ThumbnailMediaIDs: []string{"111", "222"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Request shape: ids as JSON numbers, 0-based contiguous ranks, nil fields omitted.
	if createBody["placeId"] != float64(123456789012) || createBody["groupId"] != float64(77) {
		t.Fatalf("ids must be sent as numbers, got %#v", createBody)
	}
	if _, ok := createBody["description"]; ok {
		t.Fatalf("nil description must be omitted, got %#v", createBody)
	}
	if _, ok := createBody["categories"]; ok {
		t.Fatal("categories must never be sent (the live API rejects them)")
	}
	th := createBody["thumbnails"].([]any)
	if t0 := th[0].(map[string]any); t0["mediaId"] != float64(111) || t0["rank"] != float64(0) {
		t.Fatalf("thumbnails = %#v", th)
	}
	if t1 := th[1].(map[string]any); t1["mediaId"] != float64(222) || t1["rank"] != float64(1) {
		t.Fatalf("thumbnails = %#v", th)
	}
	if got.ID != "9007199254740993123" || got.UniverseID != "1234567890" || got.PlaceID != "123456789012" ||
		got.HostID != "1122334455" || got.HostType != "user" {
		t.Fatalf("create got %+v", got)
	}

	// Get: single GET + list lookup for thumbnails, bracketed on the start time.
	g2, err := gc.Get(ctx, "9007199254740993123")
	if err != nil || g2.Title != "Double XP" || g2.Visibility != "private" || g2.StartTime != "2026-12-01T18:00:00.123+00:00" {
		t.Fatalf("get = %+v, %v", g2, err)
	}
	if strings.Join(g2.ThumbnailMediaIDs, ",") != "111,222" {
		t.Fatalf("thumbnails must come from the list, rank-ordered; got %v", g2.ThumbnailMediaIDs)
	}
	for _, want := range []string{"startsAfter=2026-12-01T17%3A59%3A59Z", "startsBefore=2026-12-01T18%3A00%3A01Z", "endsAfter=2026-12-01T19%3A59%3A59Z"} {
		if !strings.Contains(listQuery, want) {
			t.Fatalf("list query %q missing %q", listQuery, want)
		}
	}

	if _, err := gc.Update(ctx, "9007199254740993123", GameEventWrite{Title: s("Triple XP"), GroupID: s("77"), ThumbnailMediaIDs: []string{}}); err != nil {
		t.Fatal(err)
	}
	if patchBody["title"] != "Triple XP" {
		t.Fatalf("patch body = %#v", patchBody)
	}
	if _, ok := patchBody["groupId"]; ok {
		t.Fatal("groupId is create-only and must not be PATCHed")
	}
	if _, ok := patchBody["subtitle"]; ok {
		t.Fatal("unset fields must be omitted from PATCH (null = no change)")
	}
	if c, ok := patchBody["thumbnails"].([]any); !ok || len(c) != 0 {
		t.Fatalf("an empty (non-nil) thumbnails list must be sent as [], got %#v", patchBody["thumbnails"])
	}

	if err := gc.Delete(ctx, "9007199254740993123"); err != nil || !deleted {
		t.Fatalf("delete: err=%v hit=%v", err, deleted)
	}
}

func TestGameEventPlaceIDZeroIsUnset(t *testing.T) {
	ev := geWire{ID: "1", PlaceID: "0"}.toDomain()
	if ev.PlaceID != "" {
		t.Fatalf("placeId 0 (live: no venue set) must map to empty, got %q", ev.PlaceID)
	}
}

func TestGameEventGetNotFoundAndProblemDetails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		if strings.HasSuffix(r.URL.Path, "/404") {
			w.WriteHeader(404)
			w.Write([]byte(`{"title":"Game event not found.","status":404,"detail":"The requested event does not exist.","code":"GameEventNotFound"}`))
			return
		}
		w.WriteHeader(400)
		w.Write([]byte(`{"title":"Invalid request.","status":400,"detail":"Event end time must be at least 15 minutes after the start time","code":"InvalidStartOrEndTime"}`))
	}))
	defer srv.Close()
	gc := NewGameEventClient(New("k", srv.URL, WithSleeper(func(time.Duration) {})))

	_, err := gc.Get(context.Background(), "404")
	if !IsNotFound(err) {
		t.Fatalf("want IsNotFound, got %v", err)
	}
	_, err = gc.Update(context.Background(), "1", GameEventWrite{})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 400 || apiErr.Code != "InvalidStartOrEndTime" ||
		!strings.Contains(apiErr.Message, "at least 15 minutes") {
		t.Fatalf("ProblemDetails code/detail must surface, got %#v", err)
	}
}

func TestGameEventRejectsNonNumericIDs(t *testing.T) {
	gc := NewGameEventClient(New("k", "http://unused.invalid"))
	s := func(v string) *string { return &v }
	if _, err := gc.Create(context.Background(), "1", GameEventWrite{PlaceID: s("abc")}); err == nil {
		t.Fatal("want error for non-numeric place id")
	}
	if _, err := gc.Create(context.Background(), "1", GameEventWrite{ThumbnailMediaIDs: []string{"x"}}); err == nil {
		t.Fatal("want error for non-numeric media id")
	}
}

func TestParseAPIErrorGatewayErrorsEnvelope(t *testing.T) {
	// The Open Cloud gateway answers a missing scope with this envelope (verified
	// live against virtual-events with a key lacking universe.event:*).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		w.Write([]byte(`{"errors":[{"code":0,"message":"Scope not authorized."}]}`))
	}))
	defer srv.Close()
	_, err := New("k", srv.URL).Do(context.Background(), http.MethodGet, "/x", nil, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 403 || apiErr.Message != "Scope not authorized." {
		t.Fatalf("got %#v", err)
	}
}
