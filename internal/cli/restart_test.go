package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/mock"
)

// fakeRoblox is a hermetic stand-in for the Roblox provider: it declares
// roblox_experience / roblox_place shaped resources (in memory) and records
// restarts via mock.Restarter.
type fakeRoblox struct {
	*mock.Restarter
	places int // Create/Update calls on roblox_place
	store  map[string]provider.Attributes
}

func (f *fakeRoblox) Resources() map[string]provider.Resource {
	return map[string]provider.Resource{
		"roblox_experience": &fakeRes{f: f, typ: "roblox_experience", attrs: []string{"universe_id"}},
		"roblox_place":      &fakeRes{f: f, typ: "roblox_place", attrs: []string{"universe_id", "place_id", "file"}},
	}
}

type fakeRes struct {
	f     *fakeRoblox
	typ   string
	attrs []string
}

func (r *fakeRes) Type() string { return r.typ }
func (r *fakeRes) DecodeSpec() hcldec.Spec {
	spec := hcldec.ObjectSpec{}
	for _, a := range r.attrs {
		spec[a] = &hcldec.AttrSpec{Name: a, Type: cty.String, Required: true}
	}
	return spec
}
func (r *fakeRes) Read(_ context.Context, id string) (provider.Attributes, error) {
	if a, ok := r.f.store[r.typ+":"+id]; ok {
		return a, nil
	}
	return nil, provider.ErrNotFound
}
func (r *fakeRes) Create(_ context.Context, d provider.Attributes) (string, provider.Attributes, error) {
	id := d["universe_id"].AsString()
	if r.typ == "roblox_place" {
		r.f.places++
		id += "/" + d["place_id"].AsString()
	}
	if r.f.store == nil {
		r.f.store = map[string]provider.Attributes{}
	}
	r.f.store[r.typ+":"+id] = d
	return id, d, nil
}
func (r *fakeRes) Update(_ context.Context, _ string, d provider.Attributes) (provider.Attributes, error) {
	return d, nil
}
func (r *fakeRes) Delete(context.Context, string) error            { return nil }
func (r *fakeRes) Dependencies(provider.Attributes) []provider.Ref { return nil }
func (r *fakeRes) Computed() []string                              { return []string{"file"} }

// withProvider injects p into run() for the duration of the test.
func withProvider(t *testing.T, p provider.Provider) {
	t.Helper()
	old := newProvider
	newProvider = func() provider.Provider { return p }
	t.Cleanup(func() { newProvider = old })
}

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const oneExperience = `resource "roblox_experience" "main" {
  universe_id = "100"
}
`

func TestRestartServersDerivesUniverseFromConfig(t *testing.T) {
	f := &fakeRoblox{Restarter: &mock.Restarter{}}
	withProvider(t, f)
	cfg := writeFile(t, t.TempDir(), "b.bp.hcl", oneExperience)

	var out bytes.Buffer
	code := runOut([]string{"restart-servers", "--config", cfg, "--bleed-off", "5m", "--place", "7", "--place", "8"}, &out)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
	want := []provider.RestartRequest{{UniverseID: "100", PlaceIDs: []string{"7", "8"}, BleedOff: 5 * time.Minute}}
	if got := f.Requests(); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %+v, want %+v", got, want)
	}
	for _, s := range []string{"universe 100", "only place(s) 7, 8", "5-minute bleed-off", "Restart requested for universe 100"} {
		if !strings.Contains(out.String(), s) {
			t.Fatalf("output missing %q:\n%s", s, out.String())
		}
	}
}

func TestRestartServersUniverseFlagNeedsNoConfig(t *testing.T) {
	f := &fakeRoblox{Restarter: &mock.Restarter{}}
	withProvider(t, f)

	var out bytes.Buffer
	code := runOut([]string{"restart-servers", "--universe", "42", "--all-versions", "--config", "/nonexistent.bp.hcl"}, &out)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
	want := []provider.RestartRequest{{UniverseID: "42", AllVersions: true}}
	if got := f.Requests(); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %+v, want %+v", got, want)
	}
	if !strings.Contains(out.String(), "ALL running servers") || !strings.Contains(out.String(), "IMMEDIATELY") {
		t.Fatalf("prompt text must spell out an immediate all-versions restart:\n%s", out.String())
	}
}

func TestRestartServersAmbiguousOrMissingUniverse(t *testing.T) {
	cases := map[string]string{
		"several": oneExperience + `resource "roblox_experience" "other" {
  universe_id = "200"
}
`,
		"none": `resource "roblox_place" "p" {
  universe_id = "1"
  place_id    = "2"
  file        = "x.rbxlx"
}
`,
	}
	for name, body := range cases {
		f := &fakeRoblox{Restarter: &mock.Restarter{}}
		withProvider(t, f)
		cfg := writeFile(t, t.TempDir(), "b.bp.hcl", body)
		var out bytes.Buffer
		code := runOut([]string{"restart-servers", "--config", cfg}, &out)
		if code != 1 || !strings.Contains(out.String(), "pass --universe") {
			t.Fatalf("%s: want exit 1 + --universe hint, got %d\n%s", name, code, out.String())
		}
		if len(f.Requests()) != 0 {
			t.Fatalf("%s: no restart may be sent", name)
		}
	}
}

func TestRestartServersPromptDeclinedOnTTY(t *testing.T) {
	f := &fakeRoblox{Restarter: &mock.Restarter{}}
	withProvider(t, f)
	var out bytes.Buffer
	code := run([]string{"restart-servers", "--universe", "1"}, applyStreams("no\n", true, &out))
	if code != 1 || !strings.Contains(out.String(), "Restart cancelled.") {
		t.Fatalf("want cancellation, got %d\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "Do you want to restart these servers?") {
		t.Fatalf("expected the confirmation prompt:\n%s", out.String())
	}
	if len(f.Requests()) != 0 {
		t.Fatal("a declined prompt must not restart anything")
	}
}

func TestRestartServersPromptAcceptedOnTTY(t *testing.T) {
	f := &fakeRoblox{Restarter: &mock.Restarter{}}
	withProvider(t, f)
	var out bytes.Buffer
	code := run([]string{"restart-servers", "--universe", "1"}, applyStreams("yes\n", true, &out))
	if code != 0 || len(f.Requests()) != 1 {
		t.Fatalf("want one restart, got exit %d, %d request(s)\n%s", code, len(f.Requests()), out.String())
	}
}

func TestRestartServersJSONOnTTYRequiresYes(t *testing.T) {
	f := &fakeRoblox{Restarter: &mock.Restarter{}}
	withProvider(t, f)
	var out bytes.Buffer
	code := run([]string{"restart-servers", "--universe", "1", "--json"}, applyStreams("yes\n", true, &out))
	if code != 1 || !strings.Contains(out.String(), "requires --auto-approve") || len(f.Requests()) != 0 {
		t.Fatalf("want a refusal, got %d\n%s", code, out.String())
	}
}

func TestRestartServersJSONEnvelope(t *testing.T) {
	f := &fakeRoblox{Restarter: &mock.Restarter{}}
	withProvider(t, f)
	var out bytes.Buffer
	code := run([]string{"restart-servers", "--universe", "9", "--bleed-off", "2m", "--json", "--yes"}, applyStreams("", true, &out))
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
	var env struct {
		FormatVersion int           `json:"format_version"`
		Command       string        `json:"command"`
		Result        restartResult `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("stdout must be a single JSON envelope: %v\n%s", err, out.String())
	}
	want := restartResult{UniverseID: "9", PlaceIDs: []string{}, BleedOffMinutes: 2}
	if env.FormatVersion != 1 || env.Command != "restart-servers" || !reflect.DeepEqual(env.Result, want) {
		t.Fatalf("envelope = %+v", env)
	}
}

func TestRestartServersRejectsBadBleedOffBeforePrompt(t *testing.T) {
	f := &fakeRoblox{Restarter: &mock.Restarter{}}
	withProvider(t, f)
	var out bytes.Buffer
	code := run([]string{"restart-servers", "--universe", "1", "--bleed-off", "90s"}, applyStreams("yes\n", true, &out))
	if code != 1 || strings.Contains(out.String(), "Do you want") || len(f.Requests()) != 0 {
		t.Fatalf("want an early validation error, got %d\n%s", code, out.String())
	}
}

func TestRestartServersWithoutCapability(t *testing.T) {
	withProvider(t, mock.New())
	var out bytes.Buffer
	code := runOut([]string{"restart-servers", "--universe", "1"}, &out)
	if code != 1 || !strings.Contains(out.String(), "ROBLOX_API_KEY") {
		t.Fatalf("want a missing-capability error, got %d\n%s", code, out.String())
	}
}

func TestRestartServersSurfacesProviderError(t *testing.T) {
	f := &fakeRoblox{Restarter: &mock.Restarter{Err: errors.New("Roblox API 401: insufficient scopes")}}
	withProvider(t, f)
	var out bytes.Buffer
	code := runOut([]string{"restart-servers", "--universe", "1"}, &out)
	if code != 1 || !strings.Contains(out.String(), "insufficient scopes") {
		t.Fatalf("want the provider error, got %d\n%s", code, out.String())
	}
}

const placeConfig = oneExperience + `resource "roblox_place" "start" {
  universe_id = "100"
  place_id    = "555"
  file        = "start.rbxlx"
}
`

func TestApplyRestartServersAfterPlacePublish(t *testing.T) {
	f := &fakeRoblox{Restarter: &mock.Restarter{}}
	withProvider(t, f)
	dir := t.TempDir()
	cfg := writeFile(t, dir, "b.bp.hcl", placeConfig)
	st := filepath.Join(dir, "s.bpstate")

	var out bytes.Buffer
	code := runOut([]string{"apply", "--restart-servers", "--restart-bleed-off", "3m", "--config", cfg, "--state", st}, &out)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
	want := []provider.RestartRequest{{UniverseID: "100", PlaceIDs: []string{"555"}, BleedOff: 3 * time.Minute}}
	if got := f.Requests(); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %+v, want %+v", got, want)
	}
	for _, s := range []string{"if the apply succeeds", "roblox_place.start", "Restart requested for universe 100 (place(s) 555)"} {
		if !strings.Contains(out.String(), s) {
			t.Fatalf("output missing %q:\n%s", s, out.String())
		}
	}

	// Second apply: nothing changes, so no publish and no restart.
	out.Reset()
	if code := runOut([]string{"apply", "--restart-servers", "--config", cfg, "--state", st}, &out); code != 0 {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
	if len(f.Requests()) != 1 {
		t.Fatalf("a no-op apply must not restart; requests = %+v", f.Requests())
	}
	if !strings.Contains(out.String(), "servers were not restarted") {
		t.Fatalf("expected a no-restart note:\n%s", out.String())
	}
}

func TestApplyWithoutRestartFlagNeverRestarts(t *testing.T) {
	f := &fakeRoblox{Restarter: &mock.Restarter{}}
	withProvider(t, f)
	dir := t.TempDir()
	cfg := writeFile(t, dir, "b.bp.hcl", placeConfig)
	var out bytes.Buffer
	if code := runOut([]string{"apply", "--config", cfg, "--state", filepath.Join(dir, "s.bpstate")}, &out); code != 0 {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
	if f.places != 1 || len(f.Requests()) != 0 {
		t.Fatalf("published %d, restarts %+v", f.places, f.Requests())
	}
}

func TestApplyRestartJSONIncludesRestarts(t *testing.T) {
	f := &fakeRoblox{Restarter: &mock.Restarter{}}
	withProvider(t, f)
	dir := t.TempDir()
	cfg := writeFile(t, dir, "b.bp.hcl", placeConfig)
	var out bytes.Buffer
	code := runOut([]string{"apply", "--restart-servers", "--json", "--config", cfg, "--state", filepath.Join(dir, "s.bpstate")}, &out)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
	var env struct {
		Result applyResult `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("not a single JSON envelope: %v\n%s", err, out.String())
	}
	want := []restartResult{{UniverseID: "100", PlaceIDs: []string{"555"}}}
	if !reflect.DeepEqual(env.Result.Restarts, want) {
		t.Fatalf("restarts = %+v, want %+v", env.Result.Restarts, want)
	}
}

func TestApplyRestartFailureIsAnErrorAfterApply(t *testing.T) {
	f := &fakeRoblox{Restarter: &mock.Restarter{Err: errors.New("boom")}}
	withProvider(t, f)
	dir := t.TempDir()
	cfg := writeFile(t, dir, "b.bp.hcl", placeConfig)
	var out bytes.Buffer
	code := runOut([]string{"apply", "--restart-servers", "--config", cfg, "--state", filepath.Join(dir, "s.bpstate")}, &out)
	if code != 1 || !strings.Contains(out.String(), "apply succeeded, but restarting servers failed: universe 100: boom") {
		t.Fatalf("want exit 1 + explicit partial-success error, got %d\n%s", code, out.String())
	}
	if f.places != 1 {
		t.Fatal("the apply itself must still have happened")
	}
}

func TestApplyRestartChecksCapabilityBeforeApplying(t *testing.T) {
	withProvider(t, mock.New())
	dir := t.TempDir()
	cfg := writeFile(t, dir, "b.bp.hcl", "resource \"mock_thing\" \"a\" {\n  name = \"root\"\n}\n")
	st := filepath.Join(dir, "s.bpstate")
	var out bytes.Buffer
	code := runOut([]string{"apply", "--restart-servers", "--config", cfg, "--state", st}, &out)
	if code != 1 || !strings.Contains(out.String(), "--restart-servers") {
		t.Fatalf("want an early error, got %d\n%s", code, out.String())
	}
	if _, err := os.Stat(st); err == nil {
		t.Fatal("nothing may be applied when the restart can't happen")
	}
}

func TestApplyRestartBleedOffRequiresRestartFlag(t *testing.T) {
	withProvider(t, mock.New())
	dir := t.TempDir()
	cfg := writeFile(t, dir, "b.bp.hcl", "resource \"mock_thing\" \"a\" {\n  name = \"root\"\n}\n")
	var out bytes.Buffer
	code := runOut([]string{"apply", "--restart-bleed-off", "5m", "--config", cfg, "--state", filepath.Join(dir, "s.bpstate")}, &out)
	if code != 1 || !strings.Contains(out.String(), "requires --restart-servers") {
		t.Fatalf("got %d\n%s", code, out.String())
	}
}
