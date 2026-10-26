package resourcedocs_test

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/resourcedocs"
)

// fakeRes is a minimal Resource (+ Documented) for generator tests.
type fakeRes struct {
	typ      string
	spec     hcldec.ObjectSpec
	computed []string
	doc      provider.ResourceDoc
	noDoc    bool
}

func (f *fakeRes) Type() string                                              { return f.typ }
func (f *fakeRes) DecodeSpec() hcldec.Spec                                   { return f.spec }
func (f *fakeRes) Computed() []string                                        { return f.computed }
func (f *fakeRes) Dependencies(provider.Attributes) []provider.Ref           { return nil }
func (f *fakeRes) Read(context.Context, string) (provider.Attributes, error) { return nil, nil }
func (f *fakeRes) Create(context.Context, provider.Attributes) (string, provider.Attributes, error) {
	return "", nil, nil
}
func (f *fakeRes) Update(context.Context, string, provider.Attributes) (provider.Attributes, error) {
	return nil, nil
}
func (f *fakeRes) Delete(context.Context, string) error { return nil }
func (f *fakeRes) Doc() provider.ResourceDoc            { return f.doc }

type fakeProv struct{ res map[string]provider.Resource }

func (p fakeProv) Resources() map[string]provider.Resource { return p.res }

func specWith(attrs ...string) hcldec.ObjectSpec {
	s := hcldec.ObjectSpec{}
	for _, a := range attrs {
		s[a] = &hcldec.AttrSpec{Name: a, Type: cty.String, Required: a == "universe_id"}
	}
	return s
}

func TestGenerateRendersSections(t *testing.T) {
	p := fakeProv{res: map[string]provider.Resource{
		"roblox_b": &fakeRes{typ: "roblox_b", spec: specWith("universe_id", "name"),
			computed: []string{"id", "universe_id"},
			doc: provider.ResourceDoc{Summary: "Bee.", Attributes: map[string]string{
				"universe_id": "The universe.", "name": "The name."},
				Notes: "No delete.", Scopes: []string{"b:write"}}},
		"roblox_a": &fakeRes{typ: "roblox_a", spec: specWith("universe_id"),
			computed: []string{"id"},
			doc: provider.ResourceDoc{Summary: "Ay.", Attributes: map[string]string{
				"universe_id": "The universe."}, Scopes: []string{"a:write"}}},
	}}
	out, err := resourcedocs.Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	// roblox_a section must come before roblox_b (sorted by Type()).
	ia, ib := strings.Index(out, "## `roblox_a`"), strings.Index(out, "## `roblox_b`")
	if ia < 0 || ib < 0 || ia > ib {
		t.Fatalf("sections missing or out of order: ia=%d ib=%d", ia, ib)
	}
	// universe_id is required + in Computed → "required, create-only".
	if !strings.Contains(out, "| `universe_id` | string | required, create-only | The universe. |") {
		t.Errorf("missing/incorrect universe_id row:\n%s", out)
	}
	// name is optional + not in Computed → "optional, diffable".
	if !strings.Contains(out, "| `name` | string | optional, diffable | The name. |") {
		t.Errorf("missing/incorrect name row:\n%s", out)
	}
	if !strings.Contains(out, "No delete.") || !strings.Contains(out, "`b:write`") {
		t.Errorf("missing notes/scopes:\n%s", out)
	}
}

func TestGenerateErrorsOnMissingDoc(t *testing.T) {
	p := fakeProv{res: map[string]provider.Resource{
		"roblox_x": &fakeRes{typ: "roblox_x", spec: specWith("universe_id", "name"),
			computed: []string{"id"},
			doc:      provider.ResourceDoc{Summary: "X.", Attributes: map[string]string{"universe_id": "u"}}},
	}}
	_, err := resourcedocs.Generate(p)
	if err == nil || !strings.Contains(err.Error(), "name") {
		t.Fatalf("want error naming the undocumented attribute, got %v", err)
	}
}

func TestGenerateRendersTriggerKind(t *testing.T) {
	p := fakeProv{res: map[string]provider.Resource{
		"roblox_t": &fakeRes{typ: "roblox_t", spec: specWith("universe_id", "file"),
			computed: []string{"id", "universe_id", "file"},
			doc: provider.ResourceDoc{Summary: "Tee.", Attributes: map[string]string{
				"universe_id": "The universe.", "file": "The file."},
				TriggerAttrs: []string{"file"}}},
	}}
	out, err := resourcedocs.Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	// file is optional (not "universe_id") + in TriggerAttrs → "optional, trigger",
	// which takes precedence over its also being in Computed() ("create-only").
	if !strings.Contains(out, "| `file` | string | optional, trigger | The file. |") {
		t.Errorf("missing/incorrect trigger row:\n%s", out)
	}
}

func TestGenerateErrorsOnStaleTriggerKey(t *testing.T) {
	p := fakeProv{res: map[string]provider.Resource{
		"roblox_x": &fakeRes{typ: "roblox_x", spec: specWith("universe_id"),
			computed: []string{"id"},
			doc: provider.ResourceDoc{Summary: "X.", Attributes: map[string]string{
				"universe_id": "u"}, TriggerAttrs: []string{"ghost"}}},
	}}
	_, err := resourcedocs.Generate(p)
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("want error naming the stale trigger key, got %v", err)
	}
}

func TestGenerateErrorsOnStaleDocKey(t *testing.T) {
	p := fakeProv{res: map[string]provider.Resource{
		"roblox_x": &fakeRes{typ: "roblox_x", spec: specWith("universe_id"),
			computed: []string{"id"},
			doc: provider.ResourceDoc{Summary: "X.", Attributes: map[string]string{
				"universe_id": "u", "ghost": "not a real attr"}}},
	}}
	_, err := resourcedocs.Generate(p)
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("want error naming the stale doc key, got %v", err)
	}
}

// outRes is fakeRes plus computed outputs (provider.Outputter).
type outRes struct {
	fakeRes
	outs map[string]cty.Type
}

func (o *outRes) Outputs() map[string]cty.Type { return o.outs }

func TestGenerateRendersOutputs(t *testing.T) {
	r := &outRes{fakeRes: fakeRes{typ: "roblox_o", spec: specWith("name"), computed: []string{"id", "tex"},
		doc: provider.ResourceDoc{Attributes: map[string]string{"name": "N."}, Outputs: map[string]string{"tex": "The texture."}}},
		outs: map[string]cty.Type{"tex": cty.String}}
	out, err := resourcedocs.Generate(fakeProv{res: map[string]provider.Resource{"roblox_o": r}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "| `tex` | string | computed | The texture. |") {
		t.Fatalf("missing output row:\n%s", out)
	}
}

func TestGenerateErrorsOnOutputDocMismatch(t *testing.T) {
	undocumented := &outRes{fakeRes: fakeRes{typ: "roblox_o", spec: specWith("name"),
		doc: provider.ResourceDoc{Attributes: map[string]string{"name": "N."}}},
		outs: map[string]cty.Type{"tex": cty.String}}
	if _, err := resourcedocs.Generate(fakeProv{res: map[string]provider.Resource{"roblox_o": undocumented}}); err == nil || !strings.Contains(err.Error(), "tex") {
		t.Fatalf("want an error for an undocumented output, got %v", err)
	}
	stale := &fakeRes{typ: "roblox_s", spec: specWith("name"),
		doc: provider.ResourceDoc{Attributes: map[string]string{"name": "N."}, Outputs: map[string]string{"gone": "x"}}}
	if _, err := resourcedocs.Generate(fakeProv{res: map[string]provider.Resource{"roblox_s": stale}}); err == nil || !strings.Contains(err.Error(), "gone") {
		t.Fatalf("want an error for a stale Outputs key, got %v", err)
	}
}
