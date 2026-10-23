package resources

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"

	"github.com/outpostgroupio/baseplate/internal/config/configgen"
	"github.com/outpostgroupio/baseplate/internal/provider/roblox/opencloud"
)

// TestExperienceGeneratedConfigConverges checks that `--generate-config` output
// for an experience re-decodes to attributes that diff clean against Read.
func TestExperienceGeneratedConfigConverges(t *testing.T) {
	f := &maskingUniverse{cur: opencloud.Universe{
		Visibility:              "PUBLIC",
		PrivateServerPriceRobux: i64(25),
		SocialLinks: map[string]opencloud.SocialLink{
			"discordSocialLink": {Title: "Join", URI: "https://discord.gg/x"},
		},
	}}
	e := Experience(f)
	actual, err := e.Read(context.Background(), "1")
	if err != nil {
		t.Fatal(err)
	}
	block, err := configgen.Generate(e, "main", "1", actual)
	if err != nil {
		t.Fatal(err)
	}
	file := hclwrite.NewEmptyFile()
	file.Body().AppendBlock(block)
	src := file.Bytes()

	parsed, diags := hclsyntax.ParseConfig(src, "gen.bp.hcl", hcl.InitialPos)
	if diags.HasErrors() {
		t.Fatalf("parse: %v\n%s", diags, src)
	}
	content, diags := parsed.Body.Content(&hcl.BodySchema{Blocks: []hcl.BlockHeaderSchema{{Type: "resource", LabelNames: []string{"type", "name"}}}})
	if diags.HasErrors() {
		t.Fatal(diags)
	}
	val, diags := hcldec.Decode(content.Blocks[0].Body, e.DecodeSpec(), nil)
	if diags.HasErrors() {
		t.Fatalf("decode: %v\n%s", diags, src)
	}
	for k, v := range val.AsValueMap() {
		if v.IsNull() {
			continue
		}
		if a, ok := actual[k]; !ok || !a.RawEquals(v) {
			t.Errorf("%s: generated %#v, read %#v", k, v, actual[k])
		}
	}
	for _, want := range []string{"discord_link_uri", "discord_link_title", "private_server_price", "public"} {
		if !strings.Contains(string(src), want) {
			t.Fatalf("generated config is missing %s:\n%s", want, src)
		}
	}
}
