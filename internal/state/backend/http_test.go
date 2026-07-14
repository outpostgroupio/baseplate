package backend

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/outpostgroupio/baseplate/internal/state"
)

// fakeTFServer is an in-memory Terraform-http state host.
type fakeTFServer struct {
	mu              sync.Mutex
	body            []byte
	lockID          string
	lockWho         string
	sawLockMethod   string
	sawUnlockMethod string
	sawContentMD5   string
	requireAuth     bool
}

func (f *fakeTFServer) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f.requireAuth {
			if u, p, ok := r.BasicAuth(); !ok || u != "u" || p != "pw" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.Method == "GET":
			if f.body == nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Write(f.body)
		case r.URL.Path == "/lock":
			var li map[string]any
			b, _ := io.ReadAll(r.Body)
			json.Unmarshal(b, &li)
			if f.lockID != "" {
				w.WriteHeader(http.StatusLocked)
				json.NewEncoder(w).Encode(map[string]string{"ID": f.lockID, "Who": f.lockWho})
				return
			}
			f.sawLockMethod = r.Method
			f.lockID, _ = li["ID"].(string)
			f.lockWho, _ = li["Who"].(string)
			// assert capitalized keys arrived
			if _, ok := li["ID"]; !ok {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusOK)
		case strings.HasPrefix(r.URL.Path, "/unlock"):
			f.sawUnlockMethod = r.Method
			f.lockID = ""
			w.WriteHeader(http.StatusOK)
		default: // state update (POST/PUT on the state path)
			if f.lockID != "" && r.URL.Query().Get("ID") != f.lockID {
				w.WriteHeader(http.StatusConflict)
				return
			}
			f.sawContentMD5 = r.Header.Get("Content-MD5")
			f.body, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusOK)
		}
	})
}

func TestHTTPGetEmptyReturnsNewState(t *testing.T) {
	srv := httptest.NewServer((&fakeTFServer{}).handler())
	defer srv.Close()
	h, err := NewHTTP(HTTPConfig{Address: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	s, err := h.Get()
	if err != nil {
		t.Fatal(err)
	}
	if s == nil || len(s.Resources) != 0 || s.Lineage == "" {
		t.Fatalf("want fresh state, got %+v", s)
	}
}

func TestHTTPPutThenGetRoundTrips(t *testing.T) {
	srv := httptest.NewServer((&fakeTFServer{}).handler())
	defer srv.Close()
	h, _ := NewHTTP(HTTPConfig{Address: srv.URL})
	in := state.New()
	in.Serial = 7
	in.Resources = append(in.Resources, state.Resource{Type: "mock_thing", Name: "a", ID: "9"})
	if err := h.Put(in); err != nil {
		t.Fatal(err)
	}
	out, err := h.Get()
	if err != nil {
		t.Fatal(err)
	}
	if out.Serial != 7 || len(out.Resources) != 1 || out.Resources[0].ID != "9" {
		t.Fatalf("round-trip mismatch: %+v", out)
	}
}

func TestHTTPPutSendsContentMD5(t *testing.T) {
	fs := &fakeTFServer{}
	srv := httptest.NewServer(fs.handler())
	defer srv.Close()
	h, _ := NewHTTP(HTTPConfig{Address: srv.URL})
	s := state.New()
	if err := h.Put(s); err != nil {
		t.Fatal(err)
	}
	b, _ := s.Marshal()
	sum := md5.Sum(b)
	want := base64.StdEncoding.EncodeToString(sum[:])
	if fs.sawContentMD5 != want {
		t.Fatalf("Content-MD5 = %q, want %q", fs.sawContentMD5, want)
	}
}

func TestHTTPBasicAuth(t *testing.T) {
	fs := &fakeTFServer{requireAuth: true}
	srv := httptest.NewServer(fs.handler())
	defer srv.Close()
	// wrong creds -> error
	bad, _ := NewHTTP(HTTPConfig{Address: srv.URL, Username: "u", Password: "nope"})
	if _, err := bad.Get(); err == nil {
		t.Fatal("expected auth failure")
	}
	good, _ := NewHTTP(HTTPConfig{Address: srv.URL, Username: "u", Password: "pw"})
	if _, err := good.Get(); err != nil {
		t.Fatalf("auth should succeed: %v", err)
	}
}

func TestHTTPLockLifecycleAndConflict(t *testing.T) {
	fs := &fakeTFServer{}
	srv := httptest.NewServer(fs.handler())
	defer srv.Close()
	cfg := HTTPConfig{Address: srv.URL, LockAddress: srv.URL + "/lock", UnlockAddress: srv.URL + "/unlock",
		LockMethod: "POST", UnlockMethod: "DELETE", Version: "test"}
	h1, _ := NewHTTP(cfg)
	unlock, err := h1.Lock()
	if err != nil {
		t.Fatalf("first lock should succeed: %v", err)
	}
	if fs.sawLockMethod != "POST" {
		t.Fatalf("custom lock method not used: %q", fs.sawLockMethod)
	}
	// second client can't lock
	h2, _ := NewHTTP(cfg)
	if _, err := h2.Lock(); err == nil {
		t.Fatal("second lock should conflict")
	}
	// put carries the lock ID
	if err := h1.Put(state.New()); err != nil {
		t.Fatalf("locked put should succeed: %v", err)
	}
	if err := unlock(); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	if fs.sawUnlockMethod != "DELETE" {
		t.Fatalf("custom unlock method not used: %q", fs.sawUnlockMethod)
	}
	// now a fresh lock works again
	if _, err := (func() (func() error, error) { h3, _ := NewHTTP(cfg); return h3.Lock() })(); err != nil {
		t.Fatalf("lock after unlock should succeed: %v", err)
	}
}

func TestHTTPLockingDisabledWhenNoLockAddress(t *testing.T) {
	fs := &fakeTFServer{}
	srv := httptest.NewServer(fs.handler())
	defer srv.Close()
	h, _ := NewHTTP(HTTPConfig{Address: srv.URL}) // no LockAddress
	unlock, err := h.Lock()
	if err != nil || unlock == nil {
		t.Fatalf("lock with no address should be a no-op success: %v", err)
	}
	if fs.sawLockMethod != "" {
		t.Fatal("no lock request should have been sent")
	}
	if err := unlock(); err != nil {
		t.Fatalf("no-op unlock: %v", err)
	}
}

func TestHTTPPutRejectedUnderForeignLock(t *testing.T) {
	fs := &fakeTFServer{}
	srv := httptest.NewServer(fs.handler())
	defer srv.Close()
	cfg := HTTPConfig{Address: srv.URL, LockAddress: srv.URL + "/lock", LockMethod: "POST", Version: "test"}
	holder, _ := NewHTTP(cfg)
	if _, err := holder.Lock(); err != nil {
		t.Fatal(err)
	}
	other, _ := NewHTTP(cfg)
	if err := other.Put(state.New()); err == nil {
		t.Fatal("put without the held lock ID should be rejected")
	}
}

func TestNewHTTPRequiresAddress(t *testing.T) {
	if _, err := NewHTTP(HTTPConfig{}); err == nil {
		t.Fatal("expected error when address is empty")
	}
}
