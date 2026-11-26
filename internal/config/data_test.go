package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/provider/mock"
)

const assetsData = `
data "baseplate_remote_state" "assets" {
  backend = "http"
  config  = { address = "https://state.example.com/states/assets" }
  defaults = {
    fallback = "d"
    icon     = "overridden"
  }
}
`

// stubReader returns outputs for any source, recording what it was asked.
func stubReader(outputs map[string]cty.Value, got *[]RemoteStateSource) RemoteStateReader {
	return func(src RemoteStateSource) (map[string]cty.Value, error) {
		if got != nil {
			*got = append(*got, src)
		}
		return outputs, nil
	}
}

func loadWith(t *testing.T, src string, read RemoteStateReader) (*Config, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "c.bp.hcl")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return LoadConfigWith(p, mock.New(), LoadOptions{RemoteState: read})
}

var assetsOutputs = map[string]cty.Value{
	"icon":   cty.StringVal("106681856624842"),
	"thumbs": cty.SetVal([]cty.Value{cty.StringVal("1"), cty.StringVal("2")}),
}

// A data source's outputs are known when the config loads, so they can be
// used anywhere a local can: attributes, inside lists, for_each and locals.
// defaults fill in outputs the state doesn't have.
func TestRemoteStateOutputsAreKnownAtLoad(t *testing.T) {
	var asked []RemoteStateSource
	cfg, err := loadWith(t, assetsData+`
locals {
  icon = data.baseplate_remote_state.assets.outputs.icon
}
resource "mock_thing" "icon" { name = local.icon }
resource "mock_thing" "fallback" { name = data.baseplate_remote_state.assets.outputs.fallback }
resource "mock_thing" "thumb" {
  for_each = data.baseplate_remote_state.assets.outputs.thumbs
  name     = "t-${each.key}"
}
output "first" { value = [data.baseplate_remote_state.assets.outputs.icon][0] }
`, stubReader(assetsOutputs, &asked))
	if err != nil {
		t.Fatal(err)
	}
	if len(asked) != 1 || asked[0].Name != "assets" || asked[0].Backend != "http" || asked[0].Config["address"].AsString() != "https://state.example.com/states/assets" {
		t.Fatalf("reader asked for %+v", asked)
	}
	names := map[string]string{}
	for _, d := range cfg.Resources {
		names[d.Address()] = d.Attributes["name"].AsString()
	}
	want := map[string]string{
		"mock_thing.icon": "106681856624842", "mock_thing.fallback": "d",
		`mock_thing.thumb["1"]`: "t-1", `mock_thing.thumb["2"]`: "t-2",
	}
	for a, n := range want {
		if names[a] != n {
			t.Fatalf("%s name = %q, want %q (all: %v)", a, names[a], n, names)
		}
	}
	vals, err := cfg.EvalOutputs(nil)
	if err != nil || vals["first"].Value.AsString() != "106681856624842" {
		t.Fatalf("output = %+v, %v", vals, err)
	}
}

// A state that doesn't exist yet has no outputs; defaults still apply.
func TestRemoteStateMissingStateUsesDefaults(t *testing.T) {
	cfg, err := loadWith(t, assetsData+`
resource "mock_thing" "a" { name = data.baseplate_remote_state.assets.outputs.fallback }
`, stubReader(nil, nil))
	if err != nil || cfg.Resources[0].Attributes["name"].AsString() != "d" {
		t.Fatalf("err = %v", err)
	}
}

func TestRemoteStateErrors(t *testing.T) {
	for src, want := range map[string]string{
		assetsData + `resource "mock_thing" "a" { name = data.baseplate_remote_state.assets.outputs.nope }`: `has no output "nope" (it has: fallback, icon, thumbs)`,
		`resource "mock_thing" "a" { name = data.baseplate_remote_state.other.outputs.x }`:                  "no data block baseplate_remote_state.other is declared",
		assetsData + `resource "mock_thing" "a" { name = data.baseplate_remote_state.assets.state }`:        "only attribute is outputs",
		`data "terraform_remote_state" "x" { backend = "http" }`:                                            `unknown data source type "terraform_remote_state"`,
		`data "baseplate_remote_state" "x" {}`:                                                              `"backend" is required`,
		`data "baseplate_remote_state" "x" {
  backend = "http"
  config  = "https://x"
}`: "config must be an object",
		`locals { base = "https://x" }
data "baseplate_remote_state" "x" {
  backend = "http"
  config  = { address = local.base }
}`: "local.base can't be used here",
		assetsData + assetsData: "duplicate data block",
	} {
		if _, err := loadWith(t, src, stubReader(assetsOutputs, nil)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s\nerr = %v, want %q", src, err, want)
		}
	}
	_, err := loadWith(t, assetsData, func(RemoteStateSource) (map[string]cty.Value, error) {
		return nil, errors.New("401 unauthorized")
	})
	if err == nil || !strings.Contains(err.Error(), "data.baseplate_remote_state.assets: 401 unauthorized") {
		t.Fatalf("read error = %v", err)
	}
}

// Without a reader (validate), the outputs are unknown: values that use them
// aren't errors, and a for_each over them declares no instances yet, but
// still accepts references to its instances.
func TestRemoteStateUnreadIsUnknown(t *testing.T) {
	cfg, err := loadWith(t, assetsData+`
locals { icon = data.baseplate_remote_state.assets.outputs.icon }
resource "mock_thing" "icon" { name = local.icon }
resource "mock_thing" "thumb" {
  for_each = data.baseplate_remote_state.assets.outputs.thumbs
  name     = each.key
}
resource "mock_ref" "r" {
  name   = "r"
  target = mock_thing.thumb["1"].id
}
`, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range cfg.Resources {
		if d.Type == "mock_thing" && d.Name == "thumb" {
			t.Fatalf("an unread for_each should declare no instances, got %s", d.Address())
		}
	}
	if _, err := loadWith(t, assetsData+`resource "mock_thing" "a" { name = data.baseplate_remote_state.assets.outputs.nope }`, nil); err != nil {
		t.Fatalf("an unread state can't say which outputs it has: %v", err)
	}
}
