package opencloud

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGamePassCreateGetUpdateDelete(t *testing.T) {
	base := "/game-passes/v1/universes/42/game-passes"
	// gamePassId and iconAssetId are LARGE int64s returned as JSON NUMBERS.
	body := `{"gamePassId":1907972157,"name":"VIP","description":"d","isForSale":true,` +
		`"iconAssetId":9007199254740993,"priceInformation":{"defaultPriceInRobux":100}}`
	var createHit, getHit, updateHit, deleteOffSale bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == base:
			createHit = true
			r.ParseMultipartForm(1 << 20)
			if r.FormValue("name") != "VIP" || r.FormValue("price") != "100" {
				http.Error(w, "bad form", 400)
				return
			}
			w.Write([]byte(body))
		case r.Method == http.MethodGet && r.URL.Path == base+"/1907972157/creator":
			getHit = true
			w.Write([]byte(body))
		case r.Method == http.MethodPatch && r.URL.Path == base+"/1907972157":
			r.ParseMultipartForm(1 << 20)
			if r.FormValue("isForSale") == "false" {
				deleteOffSale = true
			} else {
				updateHit = true
			}
			// PATCH returns an empty body on this API.
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, 404)
		}
	}))
	defer srv.Close()

	gc := NewGamePassClient(New("k", srv.URL, WithSleeper(func(time.Duration) {})))
	ctx := context.Background()

	got, err := gc.Create(ctx, "42", GamePassCreate{Name: "VIP", Price: 100, IsForSale: true})
	if err != nil {
		t.Fatal(err)
	}
	if !createHit || got.ID != "1907972157" || got.PriceInRobux != 100 || got.IconAssetID != "9007199254740993" {
		t.Fatalf("create got %+v (hit=%v)", got, createHit)
	}

	got2, err := gc.Get(ctx, "42", "1907972157")
	if err != nil {
		t.Fatal(err)
	}
	if !getHit || got2.Name != "VIP" || got2.PriceInRobux != 100 {
		t.Fatalf("get got %+v (hit=%v)", got2, getHit)
	}

	if err := gc.Update(ctx, "42", "1907972157", GamePassUpdate{Name: "VIP2", Price: 100, IsForSale: true}); err != nil {
		t.Fatal(err)
	}
	if !updateHit {
		t.Fatal("update did not hit PATCH")
	}

	if err := gc.Delete(ctx, "42", "1907972157"); err != nil {
		t.Fatal(err)
	}
	if !deleteOffSale {
		t.Fatal("delete did not PATCH isForSale=false")
	}
}
