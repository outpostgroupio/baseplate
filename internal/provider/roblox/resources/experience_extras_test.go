package resources

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/roblox/opencloud"
)

// maskingUniverse is a fake UniverseClient that applies updateMask semantics the
// way Roblox does (verified live): a masked field absent from the body is
// cleared, and "xSocialLink.uri"/".title" patch half of an existing link.
type maskingUniverse struct {
	cur       opencloud.Universe
	masks     [][]string
	setPublic []bool
	publicErr error
	// visLag delays a SetPublic visibility change by this many Get calls,
	// to model eventual consistency in the v2 read-back.
	visLag     int
	pendingVis string
	gets       int
}

func (f *maskingUniverse) Get(_ context.Context, id string) (opencloud.Universe, error) {
	f.gets++
	if f.pendingVis != "" {
		if f.visLag == 0 {
			f.cur.Visibility, f.pendingVis = f.pendingVis, ""
		} else {
			f.visLag--
		}
	}
	u := f.cur
	u.ID = id
	return u, nil
}

func (f *maskingUniverse) Update(_ context.Context, u opencloud.Universe, mask []string) (opencloud.Universe, error) {
	f.masks = append(f.masks, mask)
	for _, m := range mask {
		field, sub, _ := strings.Cut(m, ".")
		switch {
		case field == "privateServerPriceRobux":
			f.cur.PrivateServerPriceRobux = u.PrivateServerPriceRobux
		case strings.HasSuffix(field, "SocialLink"):
			if f.cur.SocialLinks == nil {
				f.cur.SocialLinks = map[string]opencloud.SocialLink{}
			}
			l, ok := u.SocialLinks[field]
			switch sub {
			case "":
				if ok {
					f.cur.SocialLinks[field] = l
				} else {
					delete(f.cur.SocialLinks, field)
				}
			case "uri":
				c := f.cur.SocialLinks[field]
				c.URI = l.URI
				f.cur.SocialLinks[field] = c
			case "title":
				c := f.cur.SocialLinks[field]
				c.Title = l.Title
				f.cur.SocialLinks[field] = c
			}
		case field == "voiceChatEnabled":
			f.cur.VoiceChatEnabled = u.VoiceChatEnabled
		}
	}
	return f.cur, nil
}

func (f *maskingUniverse) SetPublic(_ context.Context, _ string, public bool) error {
	f.setPublic = append(f.setPublic, public)
	if f.publicErr != nil {
		return f.publicErr
	}
	vis := "PRIVATE"
	if public {
		vis = "PUBLIC"
	}
	if f.visLag > 0 {
		f.pendingVis = vis
	} else {
		f.cur.Visibility = vis
	}
	return nil
}

func i64(n int64) *int64 { return &n }

func TestExperiencePrivateServerPrice(t *testing.T) {
	f := &maskingUniverse{}
	e := Experience(f)

	_, attrs, err := e.Create(context.Background(), provider.Attributes{
		"universe_id":          cty.StringVal("1"),
		"private_server_price": cty.NumberIntVal(0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.masks, [][]string{{"privateServerPriceRobux"}}) {
		t.Fatalf("masks = %v", f.masks)
	}
	if f.cur.PrivateServerPriceRobux == nil || *f.cur.PrivateServerPriceRobux != 0 {
		t.Fatalf("price 0 must be sent as a real (free) price, got %v", f.cur.PrivateServerPriceRobux)
	}
	if !attrs["private_servers_enabled"].True() || !attrs["private_server_price"].RawEquals(cty.NumberIntVal(0)) {
		t.Fatalf("attrs = %#v", attrs)
	}
}

func TestExperienceDisablePrivateServers(t *testing.T) {
	f := &maskingUniverse{cur: opencloud.Universe{PrivateServerPriceRobux: i64(50)}}
	e := Experience(f)

	attrs, err := e.Update(context.Background(), "1", provider.Attributes{
		"universe_id":             cty.StringVal("1"),
		"private_servers_enabled": cty.False,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.masks, [][]string{{"privateServerPriceRobux"}}) || f.cur.PrivateServerPriceRobux != nil {
		t.Fatalf("masks = %v price = %v", f.masks, f.cur.PrivateServerPriceRobux)
	}
	if attrs["private_servers_enabled"].True() {
		t.Fatalf("attrs = %#v", attrs)
	}
	if _, ok := attrs["private_server_price"]; ok {
		t.Fatalf("disabled private servers must have no price, attrs = %#v", attrs)
	}
}

func TestExperiencePrivateServerConflicts(t *testing.T) {
	f := &maskingUniverse{}
	e := Experience(f)
	_, err := e.Update(context.Background(), "1", provider.Attributes{
		"universe_id":             cty.StringVal("1"),
		"private_servers_enabled": cty.False,
		"private_server_price":    cty.NumberIntVal(10),
	})
	if err == nil || !strings.Contains(err.Error(), "private_server_price") {
		t.Fatalf("want a conflict error, got %v", err)
	}
	if len(f.masks) != 0 {
		t.Fatalf("must not call the API, masks = %v", f.masks)
	}

	// Enabling with no price can't be expressed on the wire when disabled...
	_, err = e.Update(context.Background(), "1", provider.Attributes{
		"universe_id":             cty.StringVal("1"),
		"private_servers_enabled": cty.True,
	})
	if err == nil || !strings.Contains(err.Error(), "private_server_price") {
		t.Fatalf("want a missing-price error, got %v", err)
	}

	// ...but is a no-op when they're already enabled.
	f.cur.PrivateServerPriceRobux = i64(25)
	attrs, err := e.Update(context.Background(), "1", provider.Attributes{
		"universe_id":             cty.StringVal("1"),
		"private_servers_enabled": cty.True,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.masks) != 0 || !attrs["private_servers_enabled"].True() {
		t.Fatalf("masks = %v attrs = %#v", f.masks, attrs)
	}
}

func TestExperienceSocialLinks(t *testing.T) {
	f := &maskingUniverse{cur: opencloud.Universe{SocialLinks: map[string]opencloud.SocialLink{
		"twitchSocialLink":  {Title: "Old", URI: "https://twitch.tv/old"},
		"youtubeSocialLink": {Title: "YT", URI: "https://youtube.com/@x"},
	}}}
	e := Experience(f)

	attrs, err := e.Update(context.Background(), "1", provider.Attributes{
		"universe_id":           cty.StringVal("1"),
		"discord_link_uri":      cty.StringVal("https://discord.gg/x"),
		"discord_link_title":    cty.StringVal("Join"),
		"twitch_link_uri":       cty.StringVal("https://twitch.tv/new"), // title left unmanaged
		"youtube_link_uri":      cty.StringVal(""),                      // remove
		"roblox_group_link_uri": cty.StringVal(""),                      // already absent
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"discordSocialLink", "robloxGroupSocialLink", "twitchSocialLink.uri", "youtubeSocialLink"}
	if !reflect.DeepEqual(f.masks, [][]string{want}) {
		t.Fatalf("masks = %v", f.masks)
	}
	if got := f.cur.SocialLinks["twitchSocialLink"]; got.Title != "Old" || got.URI != "https://twitch.tv/new" {
		t.Fatalf("twitch = %+v", got)
	}
	if _, ok := f.cur.SocialLinks["youtubeSocialLink"]; ok {
		t.Fatal("youtube link should be cleared")
	}
	checks := map[string]cty.Value{
		"discord_link_uri":      cty.StringVal("https://discord.gg/x"),
		"discord_link_title":    cty.StringVal("Join"),
		"twitch_link_uri":       cty.StringVal("https://twitch.tv/new"),
		"twitch_link_title":     cty.StringVal("Old"),
		"youtube_link_uri":      cty.StringVal(""),
		"roblox_group_link_uri": cty.StringVal(""),
		"guilded_link_uri":      cty.StringVal(""),
	}
	for k, v := range checks {
		if !attrs[k].RawEquals(v) {
			t.Errorf("%s = %#v, want %#v", k, attrs[k], v)
		}
	}
	if _, ok := attrs["youtube_link_title"]; ok {
		t.Errorf("an unset link must not report a title, attrs = %#v", attrs)
	}
}

func TestExperienceSocialLinkConflict(t *testing.T) {
	f := &maskingUniverse{}
	_, err := Experience(f).Update(context.Background(), "1", provider.Attributes{
		"universe_id":        cty.StringVal("1"),
		"discord_link_uri":   cty.StringVal(""),
		"discord_link_title": cty.StringVal("Join"),
	})
	if err == nil || !strings.Contains(err.Error(), "discord_link_title") {
		t.Fatalf("want a conflict error, got %v", err)
	}
}

func TestExperiencePublicToggle(t *testing.T) {
	f := &maskingUniverse{cur: opencloud.Universe{Visibility: "PRIVATE"}}
	e := Experience(f)
	desired := provider.Attributes{"universe_id": cty.StringVal("1"), "public": cty.True}

	attrs, err := e.Update(context.Background(), "1", desired)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.setPublic, []bool{true}) || !attrs["public"].True() {
		t.Fatalf("setPublic = %v attrs = %#v", f.setPublic, attrs)
	}
	// Already public: no further activate call.
	if _, err := e.Update(context.Background(), "1", desired); err != nil {
		t.Fatal(err)
	}
	if len(f.setPublic) != 1 {
		t.Fatalf("setPublic = %v", f.setPublic)
	}
	if len(f.masks) != 0 {
		t.Fatalf("visibility must not go through the v2 PATCH, masks = %v", f.masks)
	}
}

func TestExperiencePublicScopeError(t *testing.T) {
	f := &maskingUniverse{cur: opencloud.Universe{Visibility: "PRIVATE"},
		publicErr: &opencloud.APIError{Status: 403, Message: "Scope not authorized."}}
	_, err := Experience(f).Update(context.Background(), "1", provider.Attributes{
		"universe_id": cty.StringVal("1"), "public": cty.True,
	})
	var apiErr *opencloud.APIError
	if err == nil || !strings.Contains(err.Error(), "legacy-universe:manage") || !errors.As(err, &apiErr) {
		t.Fatalf("want an actionable scope error wrapping the API error, got %v", err)
	}
}

func TestExperiencePublicEligibilityError(t *testing.T) {
	// Live: with the scope granted, activate can still be refused with a
	// generic 403 code 3; that must not be blamed on the scope.
	f := &maskingUniverse{cur: opencloud.Universe{Visibility: "PRIVATE"},
		publicErr: &opencloud.APIError{Status: 403, Message: "You are not authorized to configure this universe."}}
	_, err := Experience(f).Update(context.Background(), "1", provider.Attributes{
		"universe_id": cty.StringVal("1"), "public": cty.True,
	})
	var apiErr *opencloud.APIError
	if err == nil || !errors.As(err, &apiErr) {
		t.Fatalf("want an error wrapping the API error, got %v", err)
	}
	if strings.Contains(err.Error(), "needs the legacy-universe:manage scope") {
		t.Fatalf("eligibility refusal must not be reported as a missing scope: %v", err)
	}
	if !strings.Contains(err.Error(), "refused to make") {
		t.Fatalf("want an actionable refusal message, got %v", err)
	}
}

func TestExperiencePublicWaitsForVisibility(t *testing.T) {
	f := &maskingUniverse{cur: opencloud.Universe{Visibility: "PRIVATE"}, visLag: 2}
	e := Experience(f).(*experience)
	var slept int
	e.sleep = func(time.Duration) { slept++ }

	attrs, err := e.Update(context.Background(), "1", provider.Attributes{
		"universe_id": cty.StringVal("1"), "public": cty.True,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !attrs["public"].True() {
		t.Fatalf("read-back should wait for the visibility change, attrs = %#v", attrs)
	}
	if slept == 0 {
		t.Fatal("expected a poll with sleeps between reads")
	}
}

func TestExperiencePublicPollIsBounded(t *testing.T) {
	f := &maskingUniverse{cur: opencloud.Universe{Visibility: "PRIVATE"}, visLag: 1000}
	e := Experience(f).(*experience)
	e.sleep = func(time.Duration) {}

	attrs, err := e.Update(context.Background(), "1", provider.Attributes{
		"universe_id": cty.StringVal("1"), "public": cty.True,
	})
	if err != nil {
		t.Fatal(err)
	}
	if attrs["public"].True() {
		t.Fatal("never-converging fake should still read back PRIVATE")
	}
	if f.gets > 20 {
		t.Fatalf("poll must be bounded, did %d gets", f.gets)
	}
}

func TestExperienceReadReportsExtras(t *testing.T) {
	f := &maskingUniverse{cur: opencloud.Universe{Visibility: "PRIVATE"}}
	attrs, err := Experience(f).Read(context.Background(), "1")
	if err != nil {
		t.Fatal(err)
	}
	if attrs["public"].True() || attrs["private_servers_enabled"].True() {
		t.Fatalf("attrs = %#v", attrs)
	}
	for _, l := range []string{"facebook", "twitter", "youtube", "twitch", "discord", "roblox_group", "guilded"} {
		if !attrs[l+"_link_uri"].RawEquals(cty.StringVal("")) {
			t.Errorf("%s_link_uri = %#v", l, attrs[l+"_link_uri"])
		}
	}
}
