package opencloud

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBadgeCreateGetUpdateIcon(t *testing.T) {
	created := `{"id":2124567890,"name":"Welcome!","description":"d","enabled":true,"iconImageId":9007199254740993}`
	var createHit, updateHit, disableHit, iconHit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/legacy-badges/v1/universes/42/badges":
			createHit = true
			r.ParseMultipartForm(1 << 20)
			if r.FormValue("name") != "Welcome!" || r.FormValue("isActive") != "true" ||
				r.FormValue("paymentSourceType") != "1" || r.FormValue("expectedCost") != "0" {
				http.Error(w, "bad form", 400)
				return
			}
			if _, _, err := r.FormFile("files"); err != nil {
				http.Error(w, "missing files part", 400)
				return
			}
			w.Write([]byte(created))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/badges/2124567890":
			// single-GET read (badges.roblox.com host in prod; same server here)
			w.Write([]byte(created))
		case r.Method == http.MethodPatch && r.URL.Path == "/legacy-badges/v1/badges/2124567890":
			// JSON body: {name, description, enabled}
			buf := make([]byte, r.ContentLength)
			r.Body.Read(buf)
			if strings.Contains(string(buf), `"enabled":false`) {
				disableHit = true
			} else {
				updateHit = true
			}
			// empty response body
		case r.Method == http.MethodPost && r.URL.Path == "/legacy-publish/v1/badges/2124567890/icon":
			iconHit = true
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, 404)
		}
	}))
	defer srv.Close()

	// Both clients point at the one test server (distinct paths). In prod, write =
	// apis.roblox.com, read = badges.roblox.com.
	client := New("k", srv.URL, WithSleeper(func(time.Duration) {}))
	bc := NewBadgeClient(client, client)
	ctx := context.Background()

	got, err := bc.Create(ctx, "42", BadgeCreate{
		Name: "Welcome!", Description: "d", IsActive: true, PaymentSourceType: 1, ExpectedCost: 0,
		IconData: []byte("PNG"), IconFile: "icon.png", IconType: "image/png",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !createHit || got.ID != "2124567890" || got.IconImageID != "9007199254740993" || !got.Enabled {
		t.Fatalf("create got %+v (hit=%v)", got, createHit)
	}

	got2, err := bc.Get(ctx, "42", "2124567890")
	if err != nil {
		t.Fatal(err)
	}
	if got2.Name != "Welcome!" || !got2.Enabled {
		t.Fatalf("get got %+v", got2)
	}

	if err := bc.Update(ctx, "2124567890", BadgeUpdate{Name: "Welcome!", Description: "d", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if !updateHit {
		t.Fatal("update did not PATCH")
	}

	if err := bc.UploadIcon(ctx, "2124567890", []byte("PNG"), "icon.png", "image/png"); err != nil {
		t.Fatal(err)
	}
	if !iconHit {
		t.Fatal("icon upload did not hit the publish endpoint")
	}

	// Disable (destroy path): PATCH enabled=false.
	if err := bc.Update(ctx, "2124567890", BadgeUpdate{Name: "Welcome!", Description: "d", Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if !disableHit {
		t.Fatal("disable did not PATCH enabled=false")
	}
}

func TestBadgeGetNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"errors":[{"code":0,"message":""}]}`, http.StatusNotFound)
	}))
	defer srv.Close()
	client := New("k", srv.URL, WithSleeper(func(time.Duration) {}))
	bc := NewBadgeClient(client, client)
	_, err := bc.Get(context.Background(), "42", "999")
	if err == nil || !ErrBadgeNotFound(err) {
		t.Fatalf("expected ErrBadgeNotFound on a 404, got %v", err)
	}
}
