package resources

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/roblox/opencloud"
)

// fakeConfig emulates the draft/publish flow of the Experience Config API.
type fakeConfig struct {
	published  opencloud.ConfigValues
	draft      *opencloud.ConfigDraft
	publishErr error

	gotRepo    string
	patches    []map[string]json.RawMessage
	overwrites int
	publishes  []string // strategies
	discarded  []string // draft hashes
	patchErr   error
}

// PatchDraft emulates the live merge semantics: the draft starts from the
// published config, nil deletes a key and its conditional branches, rules are
// untouched.
func (f *fakeConfig) PatchDraft(_ context.Context, _, _ string, changes map[string]json.RawMessage) (string, error) {
	f.patches = append(f.patches, changes)
	if f.patchErr != nil {
		return "", f.patchErr
	}
	entries := map[string]json.RawMessage{}
	for k, v := range f.published.Entries {
		entries[k] = v
	}
	for k, v := range changes {
		if v == nil {
			for ek := range entries {
				if ek == k || strings.HasPrefix(ek, k+opencloud.ConfigBranchMarker) {
					delete(entries, ek)
				}
			}
			continue
		}
		entries[k] = v
	}
	f.draft = &opencloud.ConfigDraft{Hash: "H", Entries: entries, ConditionalRules: f.published.ConditionalRules}
	return "H", nil
}

func (f *fakeConfig) GetPublished(_ context.Context, _, repo string) (opencloud.ConfigValues, error) {
	f.gotRepo = repo
	if f.published.Entries == nil {
		f.published.Entries = map[string]json.RawMessage{}
	}
	return f.published, nil
}
func (f *fakeConfig) GetDraft(context.Context, string, string) (*opencloud.ConfigDraft, error) {
	return f.draft, nil
}
func (f *fakeConfig) OverwriteDraft(_ context.Context, _, _ string, w opencloud.ConfigOverwrite) (string, error) {
	// The resource must never overwrite: draft:overwrite deletes every rule and
	// branch that isn't echoed back.
	f.overwrites++
	return "", errors.New("unexpected OverwriteDraft")
}
func (f *fakeConfig) Publish(_ context.Context, _, _, hash, _, strategy string) (int64, error) {
	f.publishes = append(f.publishes, strategy)
	if f.publishErr != nil {
		return 0, f.publishErr
	}
	f.published = opencloud.ConfigValues{Version: f.published.Version + 1, Entries: f.draft.Entries, ConditionalRules: f.draft.ConditionalRules}
	f.draft = nil
	return f.published.Version, nil
}
func (f *fakeConfig) DiscardDraft(_ context.Context, _, _, hash string) error {
	f.discarded = append(f.discarded, hash)
	f.draft = nil
	return nil
}

func raws(m map[string]string) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for k, v := range m {
		out[k] = json.RawMessage(v)
	}
	return out
}

func configDesired(entries cty.Value) provider.Attributes {
	return provider.Attributes{"universe_id": cty.StringVal("42"), "entries": entries}
}

var sampleEntries = cty.ObjectVal(map[string]cty.Value{
	"enable_tutorial": cty.True,
	"boss_health":     cty.NumberIntVal(100),
	"motd":            cty.StringVal("hi"),
	"shop": cty.ObjectVal(map[string]cty.Value{
		"discount": cty.NumberFloatVal(0.2),
		"items":    cty.TupleVal([]cty.Value{cty.StringVal("a"), cty.NumberIntVal(1)}),
	}),
})

func TestConfigCreateWritesTypedJSONAndPublishes(t *testing.T) {
	f := &fakeConfig{}
	id, out, err := ExperienceConfig(f).Create(context.Background(), configDesired(sampleEntries))
	if err != nil {
		t.Fatal(err)
	}
	if id != "42/InExperienceConfig" || f.gotRepo != "InExperienceConfig" {
		t.Fatalf("id = %q repo = %q", id, f.gotRepo)
	}
	if len(f.patches) != 1 || f.overwrites != 0 || len(f.publishes) != 1 || f.publishes[0] != "Immediate" {
		t.Fatalf("patches=%d overwrites=%d publishes=%v", len(f.patches), f.overwrites, f.publishes)
	}
	got := f.patches[0]
	want := map[string]string{
		"enable_tutorial": `true`, "boss_health": `100`, "motd": `"hi"`,
		"shop": `{"discount":0.2,"items":["a",1]}`,
	}
	for k, v := range want {
		if string(got[k]) != v {
			t.Errorf("entries[%s] = %s, want %s", k, got[k], v)
		}
	}
	if !out["entries"].RawEquals(sampleEntries) || out["config_version"].AsBigFloat().String() != "1" {
		t.Fatalf("out = %#v", out)
	}
}

// Reading back what Roblox returns (numbers as 100.0, nested floats) must equal
// the HCL-literal desired value, or every plan would show drift.
func TestConfigReadRoundTripsToDesired(t *testing.T) {
	f := &fakeConfig{published: opencloud.ConfigValues{Version: 3, Entries: raws(map[string]string{
		"enable_tutorial": `true`, "boss_health": `100.0`, "motd": `"hi"`,
		"shop":                    `{"items":["a",1.0],"discount":0.2}`,
		"motd.RBX.conditional.c1": `"hola"`, // conditional branch: not a base key
	})}}
	got, err := ExperienceConfig(f).Read(context.Background(), "42")
	if err != nil {
		t.Fatal(err)
	}
	if !got["entries"].RawEquals(sampleEntries) {
		t.Fatalf("entries = %#v\nwant %#v", got["entries"], sampleEntries)
	}
	if got["repository"].AsString() != "InExperienceConfig" || got["universe_id"].AsString() != "42" {
		t.Fatalf("got %#v", got)
	}
}

func TestConfigReadEmptyRepository(t *testing.T) {
	got, err := ExperienceConfig(&fakeConfig{}).Read(context.Background(), "42/InExperienceConfig")
	if err != nil {
		t.Fatal(err)
	}
	if !got["entries"].RawEquals(cty.EmptyObjectVal) {
		t.Fatalf("entries = %#v", got["entries"])
	}
}

func TestConfigUpdatePreservesConditionalRulesAndKeptBranches(t *testing.T) {
	rules := json.RawMessage(`{"rules":{"c1":{"tokens":[]}},"rulesOrder":["c1"]}`)
	f := &fakeConfig{published: opencloud.ConfigValues{Version: 5, ConditionalRules: rules, Entries: raws(map[string]string{
		"motd": `"hi"`, "motd.RBX.conditional.c1": `"hola"`,
		"old": `1.0`, "old.RBX.conditional.c1": `2.0`,
	})}}
	desired := configDesired(cty.ObjectVal(map[string]cty.Value{"motd": cty.StringVal("hello")}))
	desired["deployment_strategy"] = cty.StringVal("GradualRollout")
	out, err := ExperienceConfig(f).Update(context.Background(), "42/InExperienceConfig", desired)
	if err != nil {
		t.Fatal(err)
	}
	// The patch: desired keys + an explicit delete for the undeclared base key;
	// never a branch key (rules are never part of a patch).
	p := f.patches[0]
	if len(p) != 2 || string(p["motd"]) != `"hello"` || p["old"] != nil {
		t.Fatalf("patch = %v", p)
	}
	if _, ok := p["old"]; !ok {
		t.Fatal("undeclared key must be deleted (authoritative)")
	}
	// The published result (fake emulates the live merge semantics).
	if string(f.published.ConditionalRules) != string(rules) {
		t.Fatalf("rules not preserved: %s", f.published.ConditionalRules)
	}
	if string(f.published.Entries["motd.RBX.conditional.c1"]) != `"hola"` {
		t.Fatal("branch of a kept key must be preserved")
	}
	if _, ok := f.published.Entries["old.RBX.conditional.c1"]; ok {
		t.Fatal("branch of a removed key must be dropped with it")
	}
	if f.publishes[0] != "GradualRollout" || out["deployment_strategy"].AsString() != "GradualRollout" {
		t.Fatalf("strategy = %v", f.publishes)
	}
	if out["config_version"].AsBigFloat().String() != "6" {
		t.Fatalf("config_version = %#v", out["config_version"])
	}
}

func TestConfigRefusesWhenDraftPending(t *testing.T) {
	f := &fakeConfig{draft: &opencloud.ConfigDraft{Hash: "X", Entries: raws(map[string]string{"pending": `1`})}}
	_, err := ExperienceConfig(f).Update(context.Background(), "42", configDesired(sampleEntries))
	if err == nil || !strings.Contains(err.Error(), "unpublished draft") || !strings.Contains(err.Error(), "pending") {
		t.Fatalf("err = %v", err)
	}
	if len(f.patches) != 0 || len(f.discarded) != 0 {
		t.Fatal("must not touch someone else's draft")
	}
}

func TestConfigCreateRefusesToDeleteUndeclaredKeys(t *testing.T) {
	f := &fakeConfig{published: opencloud.ConfigValues{Version: 2, Entries: raws(map[string]string{
		"motd": `"x"`, "live_flag": `true`, "live_flag.RBX.conditional.c1": `false`,
	})}}
	_, _, err := ExperienceConfig(f).Create(context.Background(), configDesired(cty.ObjectVal(map[string]cty.Value{"motd": cty.StringVal("y")})))
	if err == nil || !strings.Contains(err.Error(), "live_flag") || !strings.Contains(err.Error(), "Import it first") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "RBX") {
		t.Fatalf("branch keys must not be listed as undeclared: %v", err)
	}
	if len(f.patches) != 0 {
		t.Fatal("must not write")
	}
}

func TestConfigEmptyDraftPublishIsDiscardedNotFailed(t *testing.T) {
	f := &fakeConfig{
		published:  opencloud.ConfigValues{Version: 9, Entries: raws(map[string]string{"a": `true`})},
		publishErr: &opencloud.APIError{Status: 400, Message: `{"validationErrors":[{"code":"EmptyDraft"}]}`},
	}
	out, err := ExperienceConfig(f).Update(context.Background(), "42", configDesired(cty.ObjectVal(map[string]cty.Value{"a": cty.True})))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.discarded) != 1 || f.draft != nil {
		t.Fatal("no-op draft must be discarded")
	}
	if out["config_version"].AsBigFloat().String() != "9" {
		t.Fatalf("config_version = %#v", out["config_version"])
	}
}

func TestConfigPublishFailureDiscardsDraft(t *testing.T) {
	f := &fakeConfig{publishErr: &opencloud.APIError{Status: 400, Message: "InvalidVariantConfiguration"}}
	_, err := ExperienceConfig(f).Update(context.Background(), "42", configDesired(sampleEntries))
	if err == nil || !strings.Contains(err.Error(), "InvalidVariantConfiguration") || !strings.Contains(err.Error(), "discarded") {
		t.Fatalf("err = %v", err)
	}
	if len(f.discarded) != 1 || f.discarded[0] != "H" {
		t.Fatalf("discarded = %v", f.discarded)
	}
}

func TestConfigRolloutInFlightHasClearError(t *testing.T) {
	f := &fakeConfig{patchErr: &opencloud.APIError{Status: 400, Message: "MultipleDraftNotSupported"}}
	_, err := ExperienceConfig(f).Update(context.Background(), "42", configDesired(sampleEntries))
	if err == nil || !strings.Contains(err.Error(), "still in flight") || !strings.Contains(err.Error(), "GradualRollout") {
		t.Fatalf("err = %v", err)
	}
	if len(f.publishes) != 0 {
		t.Fatal("must not publish")
	}
}

func TestConfigValidation(t *testing.T) {
	long := strings.Repeat("k", 257)
	many := map[string]cty.Value{}
	for i := 0; i < 101; i++ {
		many[strings.Repeat("x", i+1)] = cty.True
	}
	cases := map[string]provider.Attributes{
		"reserved key":   configDesired(cty.ObjectVal(map[string]cty.Value{"a.RBX.conditional.c1": cty.True})),
		"null value":     configDesired(cty.ObjectVal(map[string]cty.Value{"a": cty.NullVal(cty.Bool)})),
		"long key":       configDesired(cty.ObjectVal(map[string]cty.Value{long: cty.True})),
		"too many":       configDesired(cty.ObjectVal(many)),
		"not a map":      configDesired(cty.StringVal("nope")),
		"partly unknown": configDesired(cty.ObjectVal(map[string]cty.Value{"a": cty.UnknownVal(cty.String)})),
		"bad strategy": func() provider.Attributes {
			d := configDesired(sampleEntries)
			d["deployment_strategy"] = cty.StringVal("Slow")
			return d
		}(),
	}
	for name, d := range cases {
		f := &fakeConfig{}
		if _, err := ExperienceConfig(f).Update(context.Background(), "42", d); err == nil {
			t.Errorf("%s: expected an error", name)
		}
		if len(f.patches) != 0 {
			t.Errorf("%s: must fail before writing", name)
		}
	}
}

func TestConfigEmptyEntriesClearsRepository(t *testing.T) {
	f := &fakeConfig{published: opencloud.ConfigValues{Version: 1, Entries: raws(map[string]string{"a": `true`})}}
	if _, err := ExperienceConfig(f).Update(context.Background(), "42", configDesired(cty.EmptyObjectVal)); err != nil {
		t.Fatal(err)
	}
	if len(f.patches[0]) != 1 || len(f.published.Entries) != 0 {
		t.Fatalf("patch = %v published = %v", f.patches[0], f.published.Entries)
	}
}

func TestConfigDeleteRemovesPlainKeysAndKeepsRules(t *testing.T) {
	rules := json.RawMessage(`{"rules":{"c1":{"tokens":[]}},"rulesOrder":["c1"]}`)
	f := &fakeConfig{published: opencloud.ConfigValues{Version: 7, ConditionalRules: rules, Entries: raws(map[string]string{
		"a": `true`, "b": `{"x":1.0}`, "a.RBX.conditional.c1": `false`,
	})}}
	if err := ExperienceConfig(f).Delete(context.Background(), "42/InExperienceConfig"); err != nil {
		t.Fatal(err)
	}
	if len(f.patches) != 1 || len(f.publishes) != 1 || f.publishes[0] != "Immediate" {
		t.Fatalf("patches=%v publishes=%v", f.patches, f.publishes)
	}
	p := f.patches[0]
	if len(p) != 2 || p["a"] != nil || p["b"] != nil {
		t.Fatalf("patch must null exactly the plain keys, got %v", p)
	}
	if _, sent := p["a.RBX.conditional.c1"]; sent {
		t.Fatal("branch keys are never sent (they go with their base key)")
	}
	if len(f.published.Entries) != 0 {
		t.Fatalf("published entries = %v", f.published.Entries)
	}
	if string(f.published.ConditionalRules) != string(rules) {
		t.Fatalf("rules must be left alone, got %s", f.published.ConditionalRules)
	}
}

func TestConfigDeleteEmptyOrUnpublishedIsNoop(t *testing.T) {
	for name, pub := range map[string]opencloud.ConfigValues{
		"never published": {},
		"empty":           {Version: 3, Entries: raws(map[string]string{})},
		"rules only":      {Version: 3, Entries: raws(map[string]string{}), ConditionalRules: json.RawMessage(`{"rules":{}}`)},
	} {
		f := &fakeConfig{published: pub}
		if err := ExperienceConfig(f).Delete(context.Background(), "42"); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(f.patches) != 0 || len(f.publishes) != 0 {
			t.Fatalf("%s: must not write", name)
		}
	}
}

func TestConfigDeleteEmptyDraftPublishIsSuccess(t *testing.T) {
	f := &fakeConfig{
		published:  opencloud.ConfigValues{Version: 2, Entries: raws(map[string]string{"a": `1.0`})},
		publishErr: &opencloud.APIError{Status: 400, Message: `{"validationErrors":[{"code":"EmptyDraft"}]}`},
	}
	if err := ExperienceConfig(f).Delete(context.Background(), "42"); err != nil {
		t.Fatal(err)
	}
	if len(f.discarded) != 1 {
		t.Fatal("the no-op draft must be discarded")
	}
}

func TestConfigDeleteRefusesWhenDraftPending(t *testing.T) {
	f := &fakeConfig{
		published: opencloud.ConfigValues{Version: 2, Entries: raws(map[string]string{"a": `1.0`})},
		draft:     &opencloud.ConfigDraft{Hash: "X", Entries: raws(map[string]string{"someone_else": `1`})},
	}
	err := ExperienceConfig(f).Delete(context.Background(), "42")
	if err == nil || !strings.Contains(err.Error(), "unpublished draft") {
		t.Fatalf("err = %v", err)
	}
	if len(f.patches) != 0 || len(f.discarded) != 0 {
		t.Fatal("must not touch someone else's draft")
	}
}

func TestConfigDeletePublishFailureIsError(t *testing.T) {
	f := &fakeConfig{
		published:  opencloud.ConfigValues{Version: 2, Entries: raws(map[string]string{"a": `1.0`})},
		publishErr: &opencloud.APIError{Status: 400, Message: "boom"},
	}
	if err := ExperienceConfig(f).Delete(context.Background(), "42"); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
	if len(f.discarded) != 1 {
		t.Fatal("the staged draft must be discarded")
	}
}

func TestSplitConfigID(t *testing.T) {
	for id, want := range map[string][2]string{
		"42":                    {"42", "InExperienceConfig"},
		"42/InExperienceConfig": {"42", "InExperienceConfig"},
		"42/Other":              {"42", "Other"},
	} {
		u, r, err := splitConfigID(id)
		if err != nil || u != want[0] || r != want[1] {
			t.Errorf("splitConfigID(%q) = %q,%q,%v", id, u, r, err)
		}
	}
	for _, bad := range []string{"", "/x", "42/", "42/a/b"} {
		if _, _, err := splitConfigID(bad); err == nil {
			t.Errorf("splitConfigID(%q): expected error", bad)
		}
	}
}

func TestConfigReadPropagatesErrors(t *testing.T) {
	_, err := ExperienceConfig(&errConfig{}).Read(context.Background(), "42")
	if err == nil {
		t.Fatal("expected error")
	}
}

type errConfig struct{ fakeConfig }

func (*errConfig) GetPublished(context.Context, string, string) (opencloud.ConfigValues, error) {
	return opencloud.ConfigValues{}, errors.New("boom")
}

// TestConfigHCLLiteralConvergesWithReadBack decodes entries through the real
// HCL path (as config.Load does) and checks the value read back from Roblox's
// wire shape is RawEquals-identical — numbers (incl. non-integers parsed from
// decimal text), nested objects and lists included.
func TestConfigHCLLiteralConvergesWithReadBack(t *testing.T) {
	src := `
universe_id = "42"
entries = {
  enable_tutorial = true
  boss_health     = 100
  drop_rate       = 0.15
  motd            = "hi"
  "dotted.key"    = "x"
  shop = {
    discount = 0.2
    items    = ["a", 1, false]
    nested   = { deep = -3.5 }
  }
  empty_list = []
  empty_obj  = {}
}
`
	f, diags := hclsyntax.ParseConfig([]byte(src), "t.hcl", hcl.InitialPos)
	if diags.HasErrors() {
		t.Fatal(diags)
	}
	r := ExperienceConfig(nil)
	val, diags := hcldec.Decode(f.Body, r.DecodeSpec(), nil)
	if diags.HasErrors() {
		t.Fatal(diags)
	}
	desired := val.GetAttr("entries")

	// What Roblox returns for those values (floats for every number).
	fc := &fakeConfig{published: opencloud.ConfigValues{Version: 1, Entries: raws(map[string]string{
		"enable_tutorial": `true`, "boss_health": `100.0`, "drop_rate": `0.15`, "motd": `"hi"`, "dotted.key": `"x"`,
		"shop":       `{"discount":0.2,"items":["a",1.0,false],"nested":{"deep":-3.5}}`,
		"empty_list": `[]`, "empty_obj": `{}`,
	})}}
	got, err := ExperienceConfig(fc).Read(context.Background(), "42")
	if err != nil {
		t.Fatal(err)
	}
	if !got["entries"].RawEquals(desired) {
		t.Fatalf("read-back does not converge:\n got  %#v\n want %#v", got["entries"], desired)
	}
}
