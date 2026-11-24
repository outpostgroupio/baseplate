package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/mock"
	"github.com/outpostgroupio/baseplate/internal/state"
	"github.com/outpostgroupio/baseplate/internal/state/backend"
)

// cleanupFixture writes a state that retired three mock_abandon objects:
// "old/1" (two days ago), "old/2" (two days ago) and "new/3" (an hour ago),
// plus a managed resource with id "live/4".
func cleanupFixture(t *testing.T) (p *mock.Provider, cfg, st string) {
	t.Helper()
	p = mock.New()
	for _, id := range []string{"old/1", "old/2", "new/3"} {
		p.Seed(id, provider.Attributes{"name": cty.StringVal(id)})
	}
	old := newProvider
	newProvider = func() provider.Provider { return p }
	t.Cleanup(func() { newProvider = old })

	dir := t.TempDir()
	cfg, st = filepath.Join(dir, "none.bp.hcl"), filepath.Join(dir, "s.bpstate")
	s := state.New()
	s.Upsert(state.Resource{Type: "mock_abandon", Name: "m", Key: "b", ID: "live/4"})
	ago := func(d time.Duration) time.Time { return time.Now().Add(-d).UTC() }
	s.Retired = []state.Retired{
		{Type: "mock_abandon", ID: "old/1", Address: `mock_abandon.m["a"]`, ReplacedBy: "old/2", RetiredAt: ago(48 * time.Hour)},
		{Type: "mock_abandon", ID: "old/2", Address: `mock_abandon.m["a"]`, ReplacedBy: "x/9", RetiredAt: ago(47 * time.Hour)},
		{Type: "mock_abandon", ID: "new/3", Address: `mock_abandon.m["c"]`, ReplacedBy: "x/8", RetiredAt: ago(time.Hour)},
		{Type: "mock_abandon", ID: "live/4", Address: `mock_abandon.m["b"]`, ReplacedBy: "x/7", RetiredAt: ago(72 * time.Hour)},
	}
	if err := backend.NewLocal(st).Put(s); err != nil {
		t.Fatal(err)
	}
	return p, cfg, st
}

func retiredIDs(t *testing.T, st string) string {
	t.Helper()
	s, err := backend.NewLocal(st).Get()
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, r := range s.Retired {
		ids = append(ids, r.ID)
	}
	return strings.Join(ids, ",")
}

func TestCleanupWithoutKeepListOnlyLists(t *testing.T) {
	p, cfg, st := cleanupFixture(t)
	var out bytes.Buffer
	if code := runOut([]string{"cleanup", "--config", cfg, "--state", st}, &out); code != 0 {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
	got := out.String()
	for _, want := range []string{"Would archive 3 object(s)", "id old/1", "id new/3", "Keeping 1 still in use", "Nothing was archived"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "id live/4") {
		t.Fatalf("an id the state still manages is never a candidate:\n%s", got)
	}
	if len(p.Store()) != 3 || retiredIDs(t, st) != "old/1,old/2,new/3,live/4" {
		t.Fatal("a dry run must change nothing")
	}
}

func TestCleanupKeepsIDsFromFilesAndFlags(t *testing.T) {
	p, cfg, st := cleanupFixture(t)
	keep := filepath.Join(t.TempDir(), "MapAssets.luau")
	os.WriteFile(keep, []byte("return {\n\tmap = \"old/2\", -- rbxassetid://123\n}\n"), 0o644)
	var out bytes.Buffer
	code := runOut([]string{"cleanup", "--auto-approve", "--keep-from", keep, "--keep", "new/3", "--config", cfg, "--state", st}, &out)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "Cleanup complete: 1 archived.") {
		t.Fatalf("want one archived:\n%s", out.String())
	}
	if _, ok := p.Store()["old/1"]; ok {
		t.Fatal("old/1 must be archived")
	}
	if _, ok := p.Store()["old/2"]; !ok {
		t.Fatal("old/2 is in the keep file and must stay")
	}
	if got := retiredIDs(t, st); got != "old/2,new/3,live/4" {
		t.Fatalf("retired = %s", got)
	}
}

func TestCleanupOlderThanAndForce(t *testing.T) {
	p, cfg, st := cleanupFixture(t)
	var out bytes.Buffer
	code := runOut([]string{"cleanup", "--force", "--older-than", "24h", "--auto-approve", "--config", cfg, "--state", st}, &out)
	if code != 0 || !strings.Contains(out.String(), "Cleanup complete: 2 archived.") {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
	if _, ok := p.Store()["new/3"]; !ok {
		t.Fatal("new/3 is younger than --older-than and must stay")
	}
	if got := retiredIDs(t, st); got != "new/3,live/4" {
		t.Fatalf("retired = %s", got)
	}
}
