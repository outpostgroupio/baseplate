package plan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/provider"
)

func TestRenderNumbersBoolsAndKnownAfterApply(t *testing.T) {
	pl := &Plan{Changes: []Change{
		{Address: "roblox_developer_product.gems", Type: "roblox_developer_product",
			Action: provider.ActionCreate,
			Desired: provider.Attributes{
				"price":       cty.NumberIntVal(99),
				"is_for_sale": cty.True,
			},
			Refs: map[string]provider.Ref{"universe_id": {Address: "roblox_experience.main", Attr: "id"}},
		},
	}}
	got := pl.RenderText(false)
	for _, want := range []string{"price = 99", "is_for_sale = true", "universe_id = (known after apply)"} {
		if !strings.Contains(got, want) {
			t.Fatalf("render missing %q:\n%s", want, got)
		}
	}
}

func TestRenderTextShowsImport(t *testing.T) {
	pl := &Plan{Changes: []Change{
		{Address: "mock_thing.a", Type: "mock_thing", Action: provider.ActionNoop, ImportID: "mock_thing/7"},
	}}
	out := pl.RenderText(false)
	if !strings.Contains(out, "mock_thing.a will be imported (id mock_thing/7)") {
		t.Fatalf("expected an import line, got:\n%s", out)
	}
	if !strings.Contains(out, "1 to import") {
		t.Fatalf("expected the import count in the summary, got:\n%s", out)
	}
}

func TestRenderTextGolden(t *testing.T) {
	pl := &Plan{Changes: []Change{
		{Address: "mock_thing.a", Type: "mock_thing", Action: provider.ActionCreate,
			Desired: provider.Attributes{"name": cty.StringVal("root")}},
	}}
	got := pl.RenderText(false)
	golden := filepath.Join("..", "..", "..", "testdata", "plan_create.golden")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		_ = os.WriteFile(golden, []byte(got), 0o644)
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("render mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestRenderTextRedactsSensitive(t *testing.T) {
	p := &Plan{Changes: []Change{
		{Address: "roblox_universe_secret.s", Type: "roblox_universe_secret",
			Action: provider.ActionCreate, Sensitive: []string{"value"},
			Desired: provider.Attributes{
				"name":  cty.StringVal("aws"),
				"value": cty.StringVal("hunter2"),
			}},
	}}
	out := p.RenderText(false)
	if strings.Contains(out, "hunter2") {
		t.Fatalf("plaintext leaked into text plan:\n%s", out)
	}
	if !strings.Contains(out, "value = (sensitive value)") {
		t.Fatalf("expected redaction marker, got:\n%s", out)
	}
}

func TestRenderJSONRedactsSensitive(t *testing.T) {
	p := &Plan{Changes: []Change{
		{Address: "roblox_universe_secret.s", Type: "roblox_universe_secret",
			Action: provider.ActionUpdate, Sensitive: []string{"value"},
			AttrDiffs: []AttrDiff{{Name: "value", Old: cty.StringVal("oldsecret"), New: cty.StringVal("hunter2")}}},
	}}
	b, err := p.RenderJSON()
	if err != nil {
		t.Fatal(err)
	}
	// NOTE: checking for the bare plaintext value "oldsecret" here rather than
	// "old" — jsonAttr's Old field serializes with JSON key "old"
	// (`json:"old,omitempty"`), so a literal `"old"` substring check would
	// always match the field name itself regardless of redaction.
	if strings.Contains(string(b), "hunter2") || strings.Contains(string(b), "oldsecret") {
		t.Fatalf("plaintext leaked into json plan:\n%s", b)
	}
	if !strings.Contains(string(b), "(sensitive value)") {
		t.Fatalf("expected redaction marker, got:\n%s", b)
	}
}

func TestRenderTextRedactsSensitiveImport(t *testing.T) {
	p := &Plan{Changes: []Change{
		{Address: "roblox_universe_secret.s", Type: "roblox_universe_secret",
			ImportID: "100/aws", Sensitive: []string{"value"},
			AttrDiffs: []AttrDiff{{Name: "value", Old: cty.NullVal(cty.String), New: cty.StringVal("hunter2")}}},
	}}
	out := p.RenderText(false)
	if strings.Contains(out, "hunter2") {
		t.Fatalf("plaintext leaked into import preview:\n%s", out)
	}
	if !strings.Contains(out, "value: (sensitive value) => (sensitive value)") {
		t.Fatalf("expected redaction in import preview, got:\n%s", out)
	}
}

func TestJSONChangesAndSummary(t *testing.T) {
	pl := &Plan{Changes: []Change{
		{Address: "mock_thing.a", Action: provider.ActionCreate},
		{Address: "mock_thing.b", Action: provider.ActionUpdate,
			AttrDiffs: []AttrDiff{{Name: "name", Old: cty.StringVal("x"), New: cty.StringVal("y")}}},
		{Address: "mock_thing.c", Action: provider.ActionDelete},
		{Address: "mock_thing.d", ImportID: "id-9"},
	}}

	got := pl.JSONChanges()
	if len(got) != 4 {
		t.Fatalf("want 4 changes, got %d", len(got))
	}
	if got[1].Address != "mock_thing.b" || len(got[1].Attrs) != 1 || got[1].Attrs[0].New != "y" {
		t.Fatalf("update change wrong: %+v", got[1])
	}
	if got[3].ImportID != "id-9" {
		t.Fatalf("import id not carried: %+v", got[3])
	}

	s := pl.Summary()
	if s.Add != 1 || s.Change != 1 || s.Destroy != 1 || s.Import != 1 {
		t.Fatalf("summary wrong: %+v", s)
	}

	if empty := (&Plan{}).JSONChanges(); empty == nil {
		t.Fatal("JSONChanges must return a non-nil slice for an empty plan")
	}
}
