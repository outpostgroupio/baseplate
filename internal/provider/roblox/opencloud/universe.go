package opencloud

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

// SocialLink is a universe social link. On update, an empty field is omitted
// from the body, so a sub-path mask (e.g. "discordSocialLink.uri") can patch
// one half of an existing link.
type SocialLink struct {
	Title string `json:"title,omitempty"`
	URI   string `json:"uri,omitempty"`
}

// SocialLinkFields lists the v2 Universe social-link wire fields.
var SocialLinkFields = []string{
	"facebookSocialLink",
	"twitterSocialLink",
	"youtubeSocialLink",
	"twitchSocialLink",
	"discordSocialLink",
	"robloxGroupSocialLink",
	"guildedSocialLink",
}

// Universe holds the universe (experience) config fields Baseplate manages.
// displayName/description/ageRating are read-only on this API and omitted.
//
// A nil PrivateServerPriceRobux means private servers are disabled (Roblox
// omits the field); on update, a nil price with "privateServerPriceRobux" in the
// mask disables them. SocialLinks is keyed by wire field name (see
// SocialLinkFields); an absent key is an unset link, and on update a masked
// link that is absent from the map is cleared. Visibility ("PUBLIC"/"PRIVATE")
// is read-only here; change it with SetPublic.
type Universe struct {
	ID                      string
	VoiceChatEnabled        *bool
	DesktopEnabled          *bool
	MobileEnabled           *bool
	TabletEnabled           *bool
	ConsoleEnabled          *bool
	VREnabled               *bool
	PrivateServerPriceRobux *int64
	SocialLinks             map[string]SocialLink
	Visibility              string
}

type UniverseClient interface {
	Get(ctx context.Context, universeID string) (Universe, error)
	Update(ctx context.Context, u Universe, mask []string) (Universe, error)
	// SetPublic makes the experience public (activate) or private (deactivate)
	// through the legacy-develop API (scope legacy-universe:manage).
	SetPublic(ctx context.Context, universeID string, public bool) error
}

func NewUniverseClient(c *Client) UniverseClient { return &universeClient{c: c} }

type universeClient struct{ c *Client }

// universeWire is both the GET response and the PATCH body. Every field is
// omitempty: Roblox rejects an explicit JSON null for privateServerPriceRobux,
// so "clear this field" is expressed as masked-and-omitted.
type universeWire struct {
	Path                    string      `json:"path,omitempty"`
	Visibility              string      `json:"visibility,omitempty"`
	VoiceChatEnabled        *bool       `json:"voiceChatEnabled,omitempty"`
	DesktopEnabled          *bool       `json:"desktopEnabled,omitempty"`
	MobileEnabled           *bool       `json:"mobileEnabled,omitempty"`
	TabletEnabled           *bool       `json:"tabletEnabled,omitempty"`
	ConsoleEnabled          *bool       `json:"consoleEnabled,omitempty"`
	VREnabled               *bool       `json:"vrEnabled,omitempty"`
	PrivateServerPriceRobux *int64      `json:"privateServerPriceRobux,omitempty"`
	FacebookSocialLink      *SocialLink `json:"facebookSocialLink,omitempty"`
	TwitterSocialLink       *SocialLink `json:"twitterSocialLink,omitempty"`
	YoutubeSocialLink       *SocialLink `json:"youtubeSocialLink,omitempty"`
	TwitchSocialLink        *SocialLink `json:"twitchSocialLink,omitempty"`
	DiscordSocialLink       *SocialLink `json:"discordSocialLink,omitempty"`
	RobloxGroupSocialLink   *SocialLink `json:"robloxGroupSocialLink,omitempty"`
	GuildedSocialLink       *SocialLink `json:"guildedSocialLink,omitempty"`
}

// links maps each social-link wire field name to its slot in w.
func (w *universeWire) links() map[string]**SocialLink {
	return map[string]**SocialLink{
		"facebookSocialLink":    &w.FacebookSocialLink,
		"twitterSocialLink":     &w.TwitterSocialLink,
		"youtubeSocialLink":     &w.YoutubeSocialLink,
		"twitchSocialLink":      &w.TwitchSocialLink,
		"discordSocialLink":     &w.DiscordSocialLink,
		"robloxGroupSocialLink": &w.RobloxGroupSocialLink,
		"guildedSocialLink":     &w.GuildedSocialLink,
	}
}

func (w universeWire) toUniverse(fallbackID string) Universe {
	id := strings.TrimPrefix(w.Path, "universes/")
	if id == "" {
		id = fallbackID
	}
	u := Universe{
		ID:                      id,
		Visibility:              w.Visibility,
		VoiceChatEnabled:        w.VoiceChatEnabled,
		DesktopEnabled:          w.DesktopEnabled,
		MobileEnabled:           w.MobileEnabled,
		TabletEnabled:           w.TabletEnabled,
		ConsoleEnabled:          w.ConsoleEnabled,
		VREnabled:               w.VREnabled,
		PrivateServerPriceRobux: w.PrivateServerPriceRobux,
	}
	for name, slot := range w.links() {
		if *slot != nil {
			if u.SocialLinks == nil {
				u.SocialLinks = map[string]SocialLink{}
			}
			u.SocialLinks[name] = **slot
		}
	}
	return u
}

func (uc *universeClient) Get(ctx context.Context, universeID string) (Universe, error) {
	var w universeWire
	if _, err := uc.c.Do(ctx, http.MethodGet, "/cloud/v2/universes/"+universeID, nil, &w); err != nil {
		return Universe{}, err
	}
	return w.toUniverse(universeID), nil
}

func (uc *universeClient) Update(ctx context.Context, u Universe, mask []string) (Universe, error) {
	body := universeWire{
		VoiceChatEnabled:        u.VoiceChatEnabled,
		DesktopEnabled:          u.DesktopEnabled,
		MobileEnabled:           u.MobileEnabled,
		TabletEnabled:           u.TabletEnabled,
		ConsoleEnabled:          u.ConsoleEnabled,
		VREnabled:               u.VREnabled,
		PrivateServerPriceRobux: u.PrivateServerPriceRobux,
	}
	for name, slot := range body.links() {
		if l, ok := u.SocialLinks[name]; ok {
			l := l
			*slot = &l
		}
	}
	path := "/cloud/v2/universes/" + u.ID + "?updateMask=" + strings.Join(mask, ",")
	var w universeWire
	if _, err := uc.c.Do(ctx, http.MethodPatch, path, body, &w); err != nil {
		return Universe{}, err
	}
	return w.toUniverse(u.ID), nil
}

func (uc *universeClient) SetPublic(ctx context.Context, universeID string, public bool) error {
	action := "deactivate"
	if public {
		action = "activate"
	}
	path := "/legacy-develop/v1/universes/" + url.PathEscape(universeID) + "/" + action
	_, err := uc.c.Do(ctx, http.MethodPost, path, nil, nil)
	return err
}
