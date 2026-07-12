package opencloud

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/crypto/nacl/box"
)

// newSecretTestServer serves the public-key endpoint (with a real generated
// keypair) plus create/list/delete, capturing the last POST body. The returned
// priv lets the test decrypt the sealed secret the client uploads.
func newSecretTestServer(t *testing.T) (*httptest.Server, *[32]byte, *[32]byte, *[]byte) {
	t.Helper()
	pub, priv, err := box.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var lastPost []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/cloud/v2/universes/100/secrets/public-key":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"secret": base64.StdEncoding.EncodeToString(pub[:]),
				"key_id": "key-1",
			})
		case r.Method == http.MethodPost && r.URL.Path == "/cloud/v2/universes/100/secrets":
			lastPost, _ = io.ReadAll(r.Body)
			w.WriteHeader(201)
			_, _ = io.WriteString(w, `{"id":"aws","domain":"*","key_id":"key-1"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/cloud/v2/universes/100/secrets":
			if r.URL.Query().Get("cursor") == "" {
				_, _ = io.WriteString(w, `{"secrets":[{"id":"aws","domain":"*"}],"nextPageCursor":"c2"}`)
			} else {
				_, _ = io.WriteString(w, `{"secrets":[{"id":"gcp","domain":""}],"nextPageCursor":""}`)
			}
		case r.Method == http.MethodDelete && r.URL.Path == "/cloud/v2/universes/100/secrets/aws":
			w.WriteHeader(200)
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, 404)
		}
	}))
	return srv, pub, priv, &lastPost
}

func TestSecretClientCreateSeals(t *testing.T) {
	srv, pub, priv, lastPost := newSecretTestServer(t)
	defer srv.Close()
	sc := NewSecretClient(New("k", srv.URL, WithSleeper(func(time.Duration) {})))

	got, err := sc.Create(context.Background(), "100", SecretWrite{Name: "aws", Value: []byte("hunter2"), Domain: "*"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "aws" || got.KeyID != "key-1" {
		t.Fatalf("got %+v", got)
	}
	// The uploaded body must carry a sealed secret that decrypts to the plaintext,
	// the key id, the domain, and the name — but never the plaintext itself.
	var body struct {
		ID     string `json:"id"`
		Secret string `json:"secret"`
		KeyID  string `json:"key_id"`
		Domain string `json:"domain"`
	}
	if err := json.Unmarshal(*lastPost, &body); err != nil {
		t.Fatal(err)
	}
	if body.ID != "aws" || body.KeyID != "key-1" || body.Domain != "*" {
		t.Fatalf("body fields = %+v", body)
	}
	if body.Secret == "" {
		t.Fatal("body.secret (ciphertext) must be set")
	}
	if bytes.Contains(*lastPost, []byte("hunter2")) {
		t.Fatal("plaintext leaked into the request body")
	}
	cipher, err := base64.StdEncoding.DecodeString(body.Secret)
	if err != nil {
		t.Fatalf("secret not base64: %v", err)
	}
	dec, ok := box.OpenAnonymous(nil, cipher, pub, priv)
	if !ok || string(dec) != "hunter2" {
		t.Fatalf("sealed secret did not decrypt to plaintext (ok=%v got=%q)", ok, dec)
	}
}

func TestSecretClientListPaginates(t *testing.T) {
	srv, _, _, _ := newSecretTestServer(t)
	defer srv.Close()
	sc := NewSecretClient(New("k", srv.URL, WithSleeper(func(time.Duration) {})))
	got, err := sc.List(context.Background(), "100")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "aws" || got[1].Name != "gcp" {
		t.Fatalf("list = %+v", got)
	}
}

func TestSecretClientDelete(t *testing.T) {
	srv, _, _, _ := newSecretTestServer(t)
	defer srv.Close()
	sc := NewSecretClient(New("k", srv.URL, WithSleeper(func(time.Duration) {})))
	if err := sc.Delete(context.Background(), "100", "aws"); err != nil {
		t.Fatal(err)
	}
}

// TestSecretClientListTerminatesOnEmptyPage guards against an infinite pagination
// loop: the real Roblox secrets endpoint ALWAYS returns a non-empty nextPageCursor,
// even on the final (empty) page, so List must terminate on an empty page rather
// than following the cursor forever. The handler returns 500 on any 3rd+ request,
// so a non-terminating loop fails with an error instead of hanging.
func TestSecretClientListTerminatesOnEmptyPage(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/cloud/v2/universes/100/secrets" {
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, 404)
			return
		}
		calls++
		switch r.URL.Query().Get("cursor") {
		case "":
			_, _ = io.WriteString(w, `{"secrets":[{"id":"aws","domain":"*"}],"nextPageCursor":"c1"}`)
		case "c1":
			// Final page: no items, but the cursor is STILL non-empty (real API behavior).
			_, _ = io.WriteString(w, `{"secrets":[],"nextPageCursor":"c2"}`)
		default:
			http.Error(w, "would loop forever", 500)
		}
	}))
	defer srv.Close()
	sc := NewSecretClient(New("k", srv.URL, WithSleeper(func(time.Duration) {})))
	got, err := sc.List(context.Background(), "100")
	if err != nil {
		t.Fatalf("List did not terminate on the empty page: %v (calls=%d)", err, calls)
	}
	if len(got) != 1 || got[0].Name != "aws" {
		t.Fatalf("list = %+v", got)
	}
}
