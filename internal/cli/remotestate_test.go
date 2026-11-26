package cli

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/auth"
	"github.com/outpostgroupio/baseplate/internal/config"
	"github.com/outpostgroupio/baseplate/internal/state"
)

// One config's outputs feed another's resources through
// baseplate_remote_state, read from the first config's state file.
func TestRemoteStateFromLocalState(t *testing.T) {
	sharedMock(t)
	dir := t.TempDir()
	assets, assetsState := filepath.Join(dir, "assets.bp.hcl"), filepath.Join(dir, "assets.bpstate")
	prod, prodState := filepath.Join(dir, "prod.bp.hcl"), filepath.Join(dir, "prod.bpstate")
	os.WriteFile(assets, []byte(`
resource "mock_thing" "icon" { name = "icon" }
output "icon_id" { value = mock_thing.icon.id }
`), 0o644)
	os.WriteFile(prod, []byte(`
data "baseplate_remote_state" "assets" {
  backend = "local"
  config  = { path = "`+filepath.ToSlash(assetsState)+`" }
}
resource "mock_ref" "store" {
  name   = "store"
  target = data.baseplate_remote_state.assets.outputs.icon_id
}
`), 0o644)

	// Before the assets config is applied its state has no outputs.
	if out := mustRun(t, 1, "plan", "--config", prod, "--state", prodState); !strings.Contains(out, `has no output "icon_id" (it has none)`) {
		t.Fatalf("plan before the source is applied:\n%s", out)
	}
	mustRun(t, 0, "apply", "--auto-approve", "--config", assets, "--state", assetsState)
	if out := mustRun(t, 0, "validate", "--config", prod); !strings.Contains(out, "valid") {
		t.Fatalf("validate:\n%s", out)
	}
	out := mustRun(t, 2, "plan", "--config", prod, "--state", prodState)
	if !strings.Contains(out, "target = mock_thing/") {
		t.Fatalf("plan should show the id read from the other state:\n%s", out)
	}
}

// fakeStateServer serves one state over the http backend protocol, and
// outputs.v1 when outputsV1 is set. It records the paths requested.
func fakeStateServer(t *testing.T, st *state.State, outputsV1 bool) (*httptest.Server, *[]string) {
	t.Helper()
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.RequestURI())
		if r.Header.Get("Authorization") != "Bearer tok" && r.URL.Path != auth.DiscoveryPath {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.URL.Path == auth.DiscoveryPath:
			if outputsV1 {
				w.Write([]byte(`{"outputs.v1": {}}`))
			} else {
				w.Write([]byte(`{}`))
			}
		case r.URL.Path == "/states/assets" && r.URL.Query().Has("outputs") && outputsV1:
			w.Write([]byte(`{"outputs": {"icon_id": {"value": "42", "type": "string"}}}`))
		case r.URL.Path == "/states/assets":
			b, _ := st.Marshal()
			w.Write(b)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &paths
}

// Over http, a server that advertises outputs.v1 is asked for the outputs
// only; any other serves the whole state, whose outputs are used.
func TestRemoteStateOverHTTP(t *testing.T) {
	t.Setenv(auth.TokenEnv, "tok")
	st := state.New()
	o, _ := state.NewOutput(cty.StringVal("42"), false)
	st.SetOutputs(map[string]state.Output{"icon_id": o})
	for _, outputsV1 := range []bool{true, false} {
		srv, paths := fakeStateServer(t, st, outputsV1)
		got, err := remoteStateReader()(config.RemoteStateSource{
			Name: "assets", Backend: "http", Config: map[string]cty.Value{"address": cty.StringVal(srv.URL + "/states/assets")},
		})
		if err != nil {
			t.Fatalf("outputs.v1=%v: %v", outputsV1, err)
		}
		if got["icon_id"].AsString() != "42" {
			t.Fatalf("outputs.v1=%v: outputs = %#v", outputsV1, got)
		}
		wantPath := "/states/assets"
		if outputsV1 {
			wantPath += "?outputs="
		}
		if last := (*paths)[len(*paths)-1]; last != wantPath {
			t.Fatalf("outputs.v1=%v: read %s, want %s (all: %v)", outputsV1, last, wantPath, *paths)
		}
	}
}

func TestRemoteStateBadBackend(t *testing.T) {
	read := remoteStateReader()
	if _, err := read(config.RemoteStateSource{Backend: "s3"}); err == nil || !strings.Contains(err.Error(), `unsupported backend "s3"`) {
		t.Fatalf("err = %v", err)
	}
	if _, err := read(config.RemoteStateSource{Backend: "local", Config: map[string]cty.Value{"file": cty.StringVal("x")}}); err == nil || !strings.Contains(err.Error(), "path") {
		t.Fatalf("err = %v", err)
	}
	if _, err := read(config.RemoteStateSource{Backend: "http", Config: map[string]cty.Value{"address": cty.StringVal("http://example.com/s")}}); err == nil || !strings.Contains(err.Error(), "must use https") {
		t.Fatalf("err = %v", err)
	}
}
