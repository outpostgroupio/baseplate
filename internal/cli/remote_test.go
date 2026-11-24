package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// stateServer is a minimal state server speaking the http backend protocol on
// one path, with LOCK/UNLOCK on the same address. It records the
// Authorization header it last saw.
type stateServer struct {
	mu   sync.Mutex
	body []byte
	lock map[string]any
	auth string
}

func (f *stateServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.auth = r.Header.Get("Authorization")
	switch r.Method {
	case "GET":
		if f.body == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Write(f.body)
	case "POST":
		if f.lock != nil && r.URL.Query().Get("ID") != f.lock["ID"] {
			w.WriteHeader(http.StatusConflict)
			return
		}
		f.body, _ = io.ReadAll(r.Body)
	case "LOCK":
		if f.lock != nil {
			w.WriteHeader(http.StatusLocked)
			json.NewEncoder(w).Encode(f.lock)
			return
		}
		json.NewDecoder(r.Body).Decode(&f.lock)
	case "UNLOCK":
		var li map[string]any
		json.NewDecoder(r.Body).Decode(&li)
		if f.lock != nil && li["ID"] != f.lock["ID"] {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(f.lock)
			return
		}
		f.lock = nil
	}
}

func remoteCfg(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	cfg := filepath.Join(t.TempDir(), "baseplate.bp.hcl")
	os.WriteFile(cfg, []byte(`
backend "http" {
  address      = "`+srv.URL+`/states/staging"
  lock_address = "`+srv.URL+`/states/staging"
}

resource "mock_thing" "a" {
  name = "root"
}
`), 0o644)
	return cfg
}

func TestPlanFailsWhileLockIsHeldAndForceUnlockReleasesIt(t *testing.T) {
	t.Setenv("BASEPLATE_STATE_TOKEN", "")
	t.Setenv("BASEPLATE_CREDENTIALS_FILE", filepath.Join(t.TempDir(), "credentials.json"))
	fs := &stateServer{lock: map[string]any{"ID": "held-1", "Who": "ci@runner", "Operation": "apply"}}
	srv := httptest.NewServer(fs)
	defer srv.Close()
	cfg := remoteCfg(t, srv)

	var out bytes.Buffer
	code := runOut([]string{"plan", "--config", cfg}, &out)
	if code != 1 || !strings.Contains(out.String(), "state locked by ci@runner") || !strings.Contains(out.String(), "force-unlock held-1") {
		t.Fatalf("plan must fail naming the holder, exit %d:\n%s", code, out.String())
	}

	out.Reset()
	if code := runOut([]string{"plan", "--lock=false", "--config", cfg}, &out); code != 2 {
		t.Fatalf("plan --lock=false should ignore the lock, exit %d:\n%s", code, out.String())
	}

	out.Reset()
	if code := runOut([]string{"force-unlock", "wrong-id", "--force", "--config", cfg}, &out); code != 1 || !strings.Contains(out.String(), "held-1") {
		t.Fatalf("force-unlock with the wrong id must fail, exit %d:\n%s", code, out.String())
	}
	out.Reset()
	if code := runOut([]string{"force-unlock", "held-1", "--force", "--config", cfg}, &out); code != 0 {
		t.Fatalf("force-unlock failed, exit %d:\n%s", code, out.String())
	}

	out.Reset()
	if code := runOut([]string{"apply", "--config", cfg}, &out); code != 0 {
		t.Fatalf("apply after force-unlock failed, exit %d:\n%s", code, out.String())
	}
	if fs.lock != nil {
		t.Fatalf("apply must release its lock, still held: %v", fs.lock)
	}
	if !bytes.Contains(fs.body, []byte(`"mock_thing"`)) {
		t.Fatalf("apply must write state to the server, got %s", fs.body)
	}
	// Each run gets a fresh mock provider, so read the state back with show.
	out.Reset()
	if code := runOut([]string{"show", "--config", cfg}, &out); code != 0 || !strings.Contains(out.String(), "mock_thing.a") {
		t.Fatalf("state must round-trip through the server, exit %d:\n%s", code, out.String())
	}
}

func TestBackendSendsTokenFromEnv(t *testing.T) {
	t.Setenv("BASEPLATE_STATE_TOKEN", "secret-token")
	fs := &stateServer{}
	srv := httptest.NewServer(fs)
	defer srv.Close()
	var out bytes.Buffer
	runOut([]string{"show", "--config", remoteCfg(t, srv)}, &out)
	if fs.auth != "Bearer secret-token" {
		t.Fatalf("Authorization = %q", fs.auth)
	}
}

// --state with a backend: apply and destroy refuse it, and other commands use
// the local file instead of the backend, without contacting it.
func TestStateFlagWithBackend(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the backend must not be contacted, got %s %s", r.Method, r.URL)
	}))
	defer srv.Close()
	cfg := remoteCfg(t, srv)
	st := filepath.Join(t.TempDir(), "local.bpstate")

	for _, cmd := range []string{"apply", "destroy"} {
		var out bytes.Buffer
		code := runOut([]string{cmd, "--auto-approve", "--config", cfg, "--state", st}, &out)
		if code != 1 || !strings.Contains(out.String(), "--state can't be used with "+cmd) {
			t.Fatalf("%s --state with a backend must be refused, exit %d:\n%s", cmd, code, out.String())
		}
	}

	var out bytes.Buffer
	if code := runOut([]string{"plan", "--config", cfg, "--state", st}, &out); code == 1 {
		t.Fatalf("plan --state: exit %d\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "note: using the local state "+st) {
		t.Fatalf("missing the override note:\n%s", out.String())
	}
	writeShowState(t, st)
	out.Reset()
	if code := runOut([]string{"show", "--config", cfg, "--state", st}, &out); code != 0 || !strings.Contains(out.String(), "mock_thing.solo") {
		t.Fatalf("show --state should read the local file: exit %d\n%s", code, out.String())
	}
}

func TestForceUnlockLocalBackendExplains(t *testing.T) {
	dir := t.TempDir()
	cfg, st := writeCfg(t, dir)
	var out bytes.Buffer
	code := runOut([]string{"force-unlock", "x", "--force", "--config", cfg, "--state", st}, &out)
	if code != 1 || !strings.Contains(out.String(), "nothing to force-unlock") {
		t.Fatalf("exit %d:\n%s", code, out.String())
	}
}
