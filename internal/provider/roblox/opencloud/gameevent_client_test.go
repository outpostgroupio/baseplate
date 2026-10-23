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

// eventBody mirrors GameEventResponse: the event id is a JSON STRING (it exceeds
// 2^53), the other ids are JSON numbers, and categories/thumbnails come back
// out of rank order to prove the client sorts them.
const eventBody = `{"id":"9007199254740993123","title":"Double XP","subtitle":"Weekend",` +
	`"description":"d","tagline":"t","startTime":"2026-12-01T18:00:00Z","endTime":"2026-12-01T20:00:00Z",` +
	`"universeId":10438196190,"placeId":123456789012,"visibility":"private","featuringStatus":"disabled",` +
	`"host":{"hostType":"user","hostId":5822334358},` +
	`"categories":[{"category":"festival","rank":2},{"category":"newContent","rank":1}],` +
	`"thumbnails":[{"mediaId":222,"rank":2},{"mediaId":111,"rank":1}]}`

func TestGameEventCreateGetUpdateDelete(t *testing.T) {
	const evPath = "/virtual-events/v3/game-events/9007199254740993123"
	var createBody, patchBody map[string]any
	var deleted bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "k" {
			http.Error(w, "no key", 401)
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/virtual-events/v3/universes/10438196190/game-events":
			if ct := r.Header.Get("Content-Type"); ct != "application/json" {
				http.Error(w, "content-type "+ct, 400)
				return
			}
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &createBody)
			w.Write([]byte(eventBody))
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

	got, err := gc.Create(ctx, "10438196190", GameEventWrite{
		Title: s("Double XP"), Subtitle: s("Weekend"),
		StartTime: s("2026-12-01T18:00:00Z"), EndTime: s("2026-12-01T20:00:00Z"),
		Visibility: s("private"), PlaceID: s("123456789012"), GroupID: s("77"),
		Categories:        []string{"newContent", "festival"},
		ThumbnailMediaIDs: []string{"111", "222"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Request shape: ids as JSON numbers, rank = position+1, nil fields omitted.
	if createBody["placeId"] != float64(123456789012) || createBody["groupId"] != float64(77) {
		t.Fatalf("ids must be sent as numbers, got %#v", createBody)
	}
	if _, ok := createBody["description"]; ok {
		t.Fatalf("nil description must be omitted, got %#v", createBody)
	}
	cats := createBody["categories"].([]any)
	if c0 := cats[0].(map[string]any); c0["category"] != "newContent" || c0["rank"] != float64(1) {
		t.Fatalf("categories = %#v", cats)
	}
	th := createBody["thumbnails"].([]any)
	if t1 := th[1].(map[string]any); t1["mediaId"] != float64(222) || t1["rank"] != float64(2) {
		t.Fatalf("thumbnails = %#v", th)
	}
	// Response decoding: string event id preserved exactly; numeric ids as strings.
	if got.ID != "9007199254740993123" || got.UniverseID != "10438196190" || got.PlaceID != "123456789012" ||
		got.HostID != "5822334358" || got.HostType != "user" {
		t.Fatalf("create got %+v", got)
	}
	if strings.Join(got.Categories, ",") != "newContent,festival" || strings.Join(got.ThumbnailMediaIDs, ",") != "111,222" {
		t.Fatalf("lists must be rank-ordered, got %v / %v", got.Categories, got.ThumbnailMediaIDs)
	}

	g2, err := gc.Get(ctx, "9007199254740993123")
	if err != nil || g2.Title != "Double XP" || g2.Visibility != "private" || g2.StartTime != "2026-12-01T18:00:00Z" {
		t.Fatalf("get = %+v, %v", g2, err)
	}

	if _, err := gc.Update(ctx, "9007199254740993123", GameEventWrite{Title: s("Triple XP"), GroupID: s("77"), Categories: []string{}}); err != nil {
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
	if c, ok := patchBody["categories"].([]any); !ok || len(c) != 0 {
		t.Fatalf("an empty (non-nil) categories list must be sent as [], got %#v", patchBody["categories"])
	}

	if err := gc.Delete(ctx, "9007199254740993123"); err != nil || !deleted {
		t.Fatalf("delete: err=%v hit=%v", err, deleted)
	}
}

func TestGameEventGetNotFoundAndProblemDetails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		if strings.HasSuffix(r.URL.Path, "/404") {
			w.WriteHeader(404)
			w.Write([]byte(`{"type":"x","title":"Not Found","status":404,"detail":"Event does not exist."}`))
			return
		}
		w.WriteHeader(400)
		w.Write([]byte(`{"title":"Bad Request","status":400,"detail":"End time must be after start time."}`))
	}))
	defer srv.Close()
	gc := NewGameEventClient(New("k", srv.URL, WithSleeper(func(time.Duration) {})))

	_, err := gc.Get(context.Background(), "404")
	if !IsNotFound(err) {
		t.Fatalf("want IsNotFound, got %v", err)
	}
	_, err = gc.Update(context.Background(), "1", GameEventWrite{})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 400 || !strings.Contains(apiErr.Message, "End time must be after start time.") {
		t.Fatalf("ProblemDetails detail must surface, got %v", err)
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
