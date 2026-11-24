package opencloud

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDeveloperProductCreateAndGet(t *testing.T) {
	var createHit, getHit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/developer-products/v2/universes/42/developer-products":
			createHit = true
			r.ParseMultipartForm(1 << 20)
			if r.FormValue("name") != "100 Gems" || r.FormValue("price") != "99" {
				http.Error(w, "bad form: name="+r.FormValue("name")+" price="+r.FormValue("price"), 400)
				return
			}
			w.Write([]byte(`{"productId":777,"name":"100 Gems","description":"d","universeId":42,"isForSale":true,"priceInformation":{"defaultPriceInRobux":99}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/developer-products/v2/universes/42/developer-products/777/creator":
			getHit = true
			w.Write([]byte(`{"productId":777,"name":"100 Gems","description":"d","universeId":42,"isForSale":true,"priceInformation":{"defaultPriceInRobux":99}}`))
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, 404)
		}
	}))
	defer srv.Close()

	pc := NewDeveloperProductClient(New("k", srv.URL, WithSleeper(func(time.Duration) {})))

	got, err := pc.Create(context.Background(), "42", DeveloperProductCreate{Name: "100 Gems", Price: 99, IsForSale: true})
	if err != nil {
		t.Fatal(err)
	}
	if !createHit || got.ID != "777" || got.PriceInRobux != 99 || got.UniverseID != "42" {
		t.Fatalf("create got %+v (hit=%v)", got, createHit)
	}

	got2, err := pc.Get(context.Background(), "42", "777")
	if err != nil {
		t.Fatal(err)
	}
	if !getHit || got2.Name != "100 Gems" || got2.PriceInRobux != 99 {
		t.Fatalf("get got %+v (hit=%v)", got2, getHit)
	}
}

func TestDeveloperProductUpdateSendsIconAndRegionalPricing(t *testing.T) {
	var form map[string]string
	var icon string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPatch && r.URL.Path == "/developer-products/v2/universes/42/developer-products/777":
			r.ParseMultipartForm(1 << 20)
			form = map[string]string{}
			for k, v := range r.MultipartForm.Value {
				form[k] = v[0]
			}
			if fh := r.MultipartForm.File["imageFile"]; len(fh) == 1 {
				icon = fh[0].Filename
			}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/developer-products/v2/universes/42/developer-products/777/creator":
			w.Write([]byte(`{"productId":777,"name":"Gems","universeId":42,"isForSale":true,"priceInformation":{"defaultPriceInRobux":25,"enabledFeatures":["RegionalPricing"]}}`))
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, 404)
		}
	}))
	defer srv.Close()

	pc := NewDeveloperProductClient(New("k", srv.URL, WithSleeper(func(time.Duration) {})))
	ctx := context.Background()

	off := false
	if _, err := pc.Update(ctx, "42", "777", DeveloperProductUpdate{
		Name: "Gems", Price: 25, IsForSale: true, RegionalPricing: &off,
		IconData: []byte("PNG"), IconFile: "icon.png", IconType: "image/png",
	}); err != nil {
		t.Fatal(err)
	}
	if form["isRegionalPricingEnabled"] != "false" || icon != "icon.png" {
		t.Fatalf("form = %v, icon = %q", form, icon)
	}

	icon = ""
	if _, err := pc.Update(ctx, "42", "777", DeveloperProductUpdate{Name: "Gems", Price: 25, IsForSale: true}); err != nil {
		t.Fatal(err)
	}
	if _, sent := form["isRegionalPricingEnabled"]; sent || icon != "" {
		t.Fatalf("unset fields must not be sent; form = %v, icon = %q", form, icon)
	}

	got, err := pc.Get(ctx, "42", "777")
	if err != nil {
		t.Fatal(err)
	}
	if !got.RegionalPricingEnabled {
		t.Fatalf("enabledFeatures RegionalPricing must map to RegionalPricingEnabled; got %+v", got)
	}
}
