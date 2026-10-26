package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

const movedBefore = `
resource "mock_thing" "sprites" {
  for_each = toset(["ui/old.png", "keep.png"])
  name     = "sprite"
}
`

const movedAfter = `
resource "mock_thing" "sprites" {
  for_each = toset(["ui/new.png", "keep.png"])
  name     = "sprite"
}
moved {
  for_each = { "ui/old.png" = "ui/new.png" }
  from     = mock_thing.sprites[each.key]
  to       = mock_thing.sprites[each.value]
}
`

const movedLine = `~ mock_thing.sprites["ui/old.png"] has moved to mock_thing.sprites["ui/new.png"]`

func TestPlanShowsMovesAndDoesNotWriteState(t *testing.T) {
	cfg, st := pdSetup(t, movedBefore)
	before, err := os.ReadFile(st)
	if err != nil {
		t.Fatal(err)
	}
	writePDCfg(t, cfg, movedAfter)

	var out bytes.Buffer
	if code := runOut([]string{"plan", "--config", cfg, "--state", st}, &out); code != 2 {
		t.Fatalf("a move-only plan exits 2, got %d\n%s", code, out.String())
	}
	for _, want := range []string{movedLine, "0 to add, 0 to change, 0 to destroy, 1 to move."} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("plan output missing %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "Warning:") {
		t.Fatalf("no rename warning once the move is declared:\n%s", out.String())
	}
	after, err := os.ReadFile(st)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("plan must not write state")
	}

	out.Reset()
	if code := runOut([]string{"plan", "--json", "--config", cfg, "--state", st}, &out); code != 2 {
		t.Fatalf("json plan exit %d\n%s", code, out.String())
	}
	var env struct {
		Result struct {
			Moves   []struct{ From, To string } `json:"moves"`
			Summary struct {
				Move, Add, Destroy int
			} `json:"summary"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	r := env.Result
	if len(r.Moves) != 1 || r.Moves[0].From != `mock_thing.sprites["ui/old.png"]` || r.Moves[0].To != `mock_thing.sprites["ui/new.png"]` ||
		r.Summary.Move != 1 || r.Summary.Add != 0 || r.Summary.Destroy != 0 {
		t.Fatalf("json plan = %+v\n%s", r, out.String())
	}
}

func TestApplyPersistsMoveOnlyPlan(t *testing.T) {
	cfg, st := pdSetup(t, movedBefore)
	writePDCfg(t, cfg, movedAfter)
	var out bytes.Buffer
	if code := runOut([]string{"apply", "--config", cfg, "--state", st}, &out); code != 0 {
		t.Fatalf("apply exit %d\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), movedLine) {
		t.Fatalf("apply should show the move:\n%s", out.String())
	}
	if got := strings.Join(stateAddrs(t, st), ","); got != `mock_thing.sprites["keep.png"],mock_thing.sprites["ui/new.png"]` {
		t.Fatalf("state after apply = %s", got)
	}
	out.Reset()
	if code := runOut([]string{"plan", "--config", cfg, "--state", st}, &out); code != 0 {
		t.Fatalf("re-plan should converge (exit 0), got %d\n%s", code, out.String())
	}
}

// Moves are state-only and idempotent, so --target doesn't filter them: they
// are applied even when the target names another resource.
func TestApplyTargetStillAppliesMoves(t *testing.T) {
	cfg, st := pdSetup(t, movedBefore)
	writePDCfg(t, cfg, movedAfter)
	var out bytes.Buffer
	if code := runOut([]string{"apply", "--target", `mock_thing.sprites["keep.png"]`, "--config", cfg, "--state", st}, &out); code != 0 {
		t.Fatalf("apply exit %d\n%s", code, out.String())
	}
	if got := strings.Join(stateAddrs(t, st), ","); got != `mock_thing.sprites["keep.png"],mock_thing.sprites["ui/new.png"]` {
		t.Fatalf("state after targeted apply = %s", got)
	}
}

// destroy applies moves before checking prevent_destroy, so a resource that
// was renamed in config is protected under its new name.
func TestDestroyChecksPreventDestroyAfterMoves(t *testing.T) {
	cfg, st := pdSetup(t, `
resource "mock_thing" "old" {
  name = "prod"
}`)
	writePDCfg(t, cfg, `
resource "mock_thing" "prod" {
  name = "prod"
  lifecycle {
    prevent_destroy = true
  }
}
moved {
  from = mock_thing.old
  to   = mock_thing.prod
}`)
	var out bytes.Buffer
	if code := runOut([]string{"destroy", "--config", cfg, "--state", st}, &out); code != 1 {
		t.Fatalf("expected exit 1, got %d\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "Error: "+pdError) {
		t.Fatalf("want the prevent_destroy error for mock_thing.prod:\n%s", out.String())
	}
	if got := strings.Join(stateAddrs(t, st), ","); got != "mock_thing.old" {
		t.Fatalf("a refused destroy must not write state (moves included), got %s", got)
	}
}

func TestPlanMovedErrorExitsOne(t *testing.T) {
	cfg, st := pdSetup(t, movedBefore)
	writePDCfg(t, cfg, `
resource "mock_thing" "sprites" {
  for_each = toset(["ui/old.png", "keep.png"])
  name     = "sprite"
}
moved {
  from = mock_thing.sprites["ui/old.png"]
  to   = mock_child.sprites["ui/old.png"]
}`)
	var out bytes.Buffer
	if code := runOut([]string{"plan", "--config", cfg, "--state", st}, &out); code != 1 {
		t.Fatalf("expected exit 1, got %d\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "type differs") {
		t.Fatalf("want the type-change error:\n%s", out.String())
	}
}
