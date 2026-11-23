package resources

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/roblox/opencloud"
)

// experienceBoolWire maps HCL attribute names to universe wire field names.
var experienceBoolWire = map[string]string{
	"voice_chat_enabled": "voiceChatEnabled",
	"desktop_enabled":    "desktopEnabled",
	"mobile_enabled":     "mobileEnabled",
	"tablet_enabled":     "tabletEnabled",
	"console_enabled":    "consoleEnabled",
	"vr_enabled":         "vrEnabled",
}

// experienceLinkWire maps each social-link HCL prefix (<prefix>_link_uri /
// <prefix>_link_title) to its universe wire field.
var experienceLinkWire = map[string]string{
	"facebook":     "facebookSocialLink",
	"twitter":      "twitterSocialLink",
	"youtube":      "youtubeSocialLink",
	"twitch":       "twitchSocialLink",
	"discord":      "discordSocialLink",
	"roblox_group": "robloxGroupSocialLink",
	"guilded":      "guildedSocialLink",
}

// experienceLinkLabel is the human name of each link, for docs.
var experienceLinkLabel = map[string]string{
	"facebook":     "Facebook",
	"twitter":      "Twitter/X",
	"youtube":      "YouTube",
	"twitch":       "Twitch",
	"discord":      "Discord",
	"roblox_group": "Roblox group (community)",
	"guilded":      "Guilded",
}

const priceWire = "privateServerPriceRobux"

// visibilityPolls bounds how many times apply re-reads the universe waiting for
// v2 `visibility` to reflect an activate/deactivate call (visibilityPollEvery
// apart). If it never does, apply returns what it read and the next plan shows
// the drift instead of hanging.
const (
	visibilityPolls     = 10
	visibilityPollEvery = time.Second
)

type experience struct {
	uc    opencloud.UniverseClient
	sleep func(time.Duration)
}

// Experience returns the roblox_experience resource backed by a UniverseClient.
func Experience(uc opencloud.UniverseClient) provider.Resource {
	return &experience{uc: uc, sleep: time.Sleep}
}

func (e *experience) Type() string { return "roblox_experience" }

func (e *experience) Doc() provider.ResourceDoc {
	attrs := map[string]string{
		"universe_id":             "The universe to manage. Its `id` output equals this.",
		"voice_chat_enabled":      "Enable voice chat.",
		"desktop_enabled":         "Playable on desktop.",
		"mobile_enabled":          "Playable on mobile.",
		"tablet_enabled":          "Playable on tablet.",
		"console_enabled":         "Playable on console.",
		"vr_enabled":              "Playable in VR.",
		"private_servers_enabled": "Whether private servers can be bought. `false` disables them (and must not be combined with `private_server_price`). `true` needs `private_server_price` unless they're already enabled.",
		"private_server_price":    "Private server price in Robux: `0` (free) or 10–500. Setting it enables private servers.",
		"public":                  "Whether the experience is public (`true`) or private (`false`). Uses the experimental legacy-develop activate/deactivate API and needs the extra `legacy-universe:manage` scope. Roblox can still refuse to make an experience public (403 \"You are not authorized to configure this universe.\"); check it can be made public in Creator Hub.",
	}
	for p, label := range experienceLinkLabel {
		attrs[p+"_link_uri"] = fmt.Sprintf("%s social link URL. `\"\"` removes the link. Creating a link needs `%s_link_title` too.", label, p)
		attrs[p+"_link_title"] = fmt.Sprintf("%s social link title (1–70 characters).", label)
	}
	return provider.ResourceDoc{
		Summary:     "Manages a universe's writable configuration. Adopts an existing universe (create one in Creator Hub first); it does not create universes.",
		Attributes:  attrs,
		Notes:       "Only declared attributes are managed. `displayName`/`description`/`ageRating` are read-only on the Open Cloud v2 API. The display name and description follow the root place's, so manage them with `roblox_place_config` on the root place. A social link's URL is validated by Roblox for its platform (e.g. a `discord.gg` invite for Discord). `public` additionally needs the `legacy-universe:manage` scope. `destroy` unmanages the universe (it is never deleted, and nothing is reverted).",
		Scopes:      []string{"universe:write"},
		ExampleFile: "examples/baseplate.bp.hcl",
	}
}

func (e *experience) DecodeSpec() hcldec.Spec {
	spec := hcldec.ObjectSpec{
		"universe_id":             &hcldec.AttrSpec{Name: "universe_id", Type: cty.String, Required: true},
		"private_servers_enabled": &hcldec.AttrSpec{Name: "private_servers_enabled", Type: cty.Bool},
		"private_server_price":    &hcldec.AttrSpec{Name: "private_server_price", Type: cty.Number},
		"public":                  &hcldec.AttrSpec{Name: "public", Type: cty.Bool},
	}
	for name := range experienceBoolWire {
		spec[name] = &hcldec.AttrSpec{Name: name, Type: cty.Bool, Required: false}
	}
	for p := range experienceLinkWire {
		spec[p+"_link_uri"] = &hcldec.AttrSpec{Name: p + "_link_uri", Type: cty.String}
		spec[p+"_link_title"] = &hcldec.AttrSpec{Name: p + "_link_title", Type: cty.String}
	}
	return spec
}

func (e *experience) Computed() []string                              { return []string{"id"} }
func (e *experience) Dependencies(provider.Attributes) []provider.Ref { return nil }

func (e *experience) Create(ctx context.Context, desired provider.Attributes) (string, provider.Attributes, error) {
	id := desired["universe_id"].AsString()
	attrs, err := e.apply(ctx, id, desired)
	if err != nil {
		return "", nil, err
	}
	return id, attrs, nil
}

func (e *experience) Read(ctx context.Context, id string) (provider.Attributes, error) {
	got, err := e.uc.Get(ctx, id)
	if err != nil {
		if opencloud.IsNotFound(err) {
			return nil, provider.ErrNotFound
		}
		return nil, err
	}
	return universeAttrs(id, got), nil
}

func (e *experience) Update(ctx context.Context, id string, desired provider.Attributes) (provider.Attributes, error) {
	return e.apply(ctx, id, desired)
}

// apply reconciles the universe to desired: v2 PATCH for the config fields, then
// the legacy activate/deactivate call for `public`, then a read-back.
func (e *experience) apply(ctx context.Context, id string, desired provider.Attributes) (provider.Attributes, error) {
	u, mask, err := universeFromDesired(id, desired)
	if err != nil {
		return nil, err
	}

	// Some attributes need the current state: `public` (only toggle when it
	// differs) and `private_servers_enabled = true` without a price (only valid
	// when private servers are already enabled).
	wantPublic, hasPublic := boolAttr(desired, "public")
	wantPS, hasPS := boolAttr(desired, "private_servers_enabled")
	if hasPublic || (hasPS && wantPS && isNullOrAbsent(desired, "private_server_price")) {
		cur, err := e.uc.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		if hasPS && wantPS && isNullOrAbsent(desired, "private_server_price") && cur.PrivateServerPriceRobux == nil {
			return nil, errors.New("roblox_experience: private_servers_enabled = true needs private_server_price (0 for free, or 10-500 Robux) because private servers are currently disabled")
		}
		if hasPublic && visibilityKnown(cur.Visibility) && (cur.Visibility == "PUBLIC") == wantPublic {
			hasPublic = false // already as desired
		}
	}

	if len(mask) > 0 {
		if _, err := e.uc.Update(ctx, u, mask); err != nil {
			return nil, err
		}
	}
	if hasPublic {
		if err := e.uc.SetPublic(ctx, id, wantPublic); err != nil {
			return nil, publicError(id, wantPublic, err)
		}
	}

	got, err := e.uc.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	for i := 0; hasPublic && i < visibilityPolls && (got.Visibility == "PUBLIC") != wantPublic; i++ {
		e.sleep(visibilityPollEvery)
		if got, err = e.uc.Get(ctx, id); err != nil {
			return nil, err
		}
	}
	return universeAttrs(id, got), nil
}

// publicError makes a failed activate/deactivate actionable. Roblox answers a
// missing scope with 403 "Scope not authorized." but also refuses activation
// for a key that has the scope with 403 "You are not authorized to configure
// this universe." (code 3), so only the former is blamed on the scope.
func publicError(id string, public bool, err error) error {
	var apiErr *opencloud.APIError
	if !errors.As(err, &apiErr) || (apiErr.Status != http.StatusUnauthorized && apiErr.Status != http.StatusForbidden) {
		return err
	}
	if strings.Contains(strings.ToLower(apiErr.Message), "scope") {
		return explained{fmt.Errorf("roblox_experience: changing `public` needs the legacy-universe:manage scope on the API key for universe %s: %w", id, err)}
	}
	verb := "private"
	if public {
		verb = "public"
	}
	return explained{fmt.Errorf("roblox_experience: Roblox refused to make universe %s %s; check in Creator Hub that the key's owner can configure it and, when making it public, that the experience's maturity questionnaire is complete: %w", id, verb, err)}
}

// explained wraps a 401/403 whose message already says what to fix, so the
// engine doesn't also list the resource's documented scopes.
type explained struct{ error }

func (e explained) Unwrap() error    { return e.error }
func (explained) AccessDenied() bool { return false }

// Delete is a no-op: Baseplate adopts an existing universe's config but does
// not own its lifecycle, so dropping the resource just stops managing it.
func (e *experience) Delete(ctx context.Context, id string) error { return nil }

func boolAttr(a provider.Attributes, k string) (val, ok bool) {
	v, present := a[k]
	if !present || v.IsNull() {
		return false, false
	}
	return v.True(), true
}

func stringAttr(a provider.Attributes, k string) (val string, ok bool) {
	v, present := a[k]
	if !present || v.IsNull() {
		return "", false
	}
	return v.AsString(), true
}

func isNullOrAbsent(a provider.Attributes, k string) bool {
	v, ok := a[k]
	return !ok || v.IsNull()
}

func visibilityKnown(v string) bool { return v == "PUBLIC" || v == "PRIVATE" }

// universeFromDesired builds the v2 PATCH body and updateMask for the declared
// attributes. Clearing (disabled private servers, a removed social link) is a
// masked field omitted from the body: Roblox 400s on an explicit null price.
func universeFromDesired(id string, desired provider.Attributes) (opencloud.Universe, []string, error) {
	u := opencloud.Universe{ID: id}
	var mask []string
	for attr, wire := range experienceBoolWire {
		b, ok := boolAttr(desired, attr)
		if !ok {
			continue
		}
		switch attr {
		case "voice_chat_enabled":
			u.VoiceChatEnabled = &b
		case "desktop_enabled":
			u.DesktopEnabled = &b
		case "mobile_enabled":
			u.MobileEnabled = &b
		case "tablet_enabled":
			u.TabletEnabled = &b
		case "console_enabled":
			u.ConsoleEnabled = &b
		case "vr_enabled":
			u.VREnabled = &b
		}
		mask = append(mask, wire)
	}

	// Private servers: a price enables them (0 = free); enabled=false disables.
	enabled, hasEnabled := boolAttr(desired, "private_servers_enabled")
	if pv, ok := desired["private_server_price"]; ok && !pv.IsNull() {
		if hasEnabled && !enabled {
			return u, nil, errors.New("roblox_experience: private_server_price can't be set when private_servers_enabled = false")
		}
		n, _ := pv.AsBigFloat().Int64()
		u.PrivateServerPriceRobux = &n
		mask = append(mask, priceWire)
	} else if hasEnabled && !enabled {
		mask = append(mask, priceWire)
	}

	// Social links: uri+title → whole field; one of them → sub-path mask
	// (patches half of an existing link); uri = "" → masked + omitted (clear).
	for p, wire := range experienceLinkWire {
		uri, hasURI := stringAttr(desired, p+"_link_uri")
		title, hasTitle := stringAttr(desired, p+"_link_title")
		switch {
		case hasURI && uri == "":
			if hasTitle && title != "" {
				return u, nil, fmt.Errorf("roblox_experience: %s_link_title can't be set when %s_link_uri is \"\" (which removes the link)", p, p)
			}
			mask = append(mask, wire)
		case hasURI && hasTitle:
			setLink(&u, wire, opencloud.SocialLink{URI: uri, Title: title})
			mask = append(mask, wire)
		case hasURI:
			setLink(&u, wire, opencloud.SocialLink{URI: uri})
			mask = append(mask, wire+".uri")
		case hasTitle:
			setLink(&u, wire, opencloud.SocialLink{Title: title})
			mask = append(mask, wire+".title")
		}
	}
	sort.Strings(mask)
	return u, mask, nil
}

func setLink(u *opencloud.Universe, wire string, l opencloud.SocialLink) {
	if u.SocialLinks == nil {
		u.SocialLinks = map[string]opencloud.SocialLink{}
	}
	u.SocialLinks[wire] = l
}

func universeAttrs(id string, u opencloud.Universe) provider.Attributes {
	a := provider.Attributes{"universe_id": cty.StringVal(id)}
	if u.VoiceChatEnabled != nil {
		a["voice_chat_enabled"] = cty.BoolVal(*u.VoiceChatEnabled)
	}
	if u.DesktopEnabled != nil {
		a["desktop_enabled"] = cty.BoolVal(*u.DesktopEnabled)
	}
	if u.MobileEnabled != nil {
		a["mobile_enabled"] = cty.BoolVal(*u.MobileEnabled)
	}
	if u.TabletEnabled != nil {
		a["tablet_enabled"] = cty.BoolVal(*u.TabletEnabled)
	}
	if u.ConsoleEnabled != nil {
		a["console_enabled"] = cty.BoolVal(*u.ConsoleEnabled)
	}
	if u.VREnabled != nil {
		a["vr_enabled"] = cty.BoolVal(*u.VREnabled)
	}
	// Roblox omits privateServerPriceRobux when private servers are disabled.
	a["private_servers_enabled"] = cty.BoolVal(u.PrivateServerPriceRobux != nil)
	if u.PrivateServerPriceRobux != nil {
		a["private_server_price"] = cty.NumberIntVal(*u.PrivateServerPriceRobux)
	}
	if visibilityKnown(u.Visibility) {
		a["public"] = cty.BoolVal(u.Visibility == "PUBLIC")
	}
	// An unset link reads back as uri "" (so `<p>_link_uri = ""` converges);
	// its title is left unset.
	for p, wire := range experienceLinkWire {
		l, ok := u.SocialLinks[wire]
		if !ok {
			a[p+"_link_uri"] = cty.StringVal("")
			continue
		}
		a[p+"_link_uri"] = cty.StringVal(l.URI)
		a[p+"_link_title"] = cty.StringVal(l.Title)
	}
	return a
}
