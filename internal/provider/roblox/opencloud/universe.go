package opencloud

import (
	"context"
	"net/http"
	"strings"
)

// Universe holds the WRITABLE universe (experience) config fields. displayName/
// description/visibility are read-only on this API and intentionally omitted.
type Universe struct {
	ID               string
	VoiceChatEnabled *bool
	DesktopEnabled   *bool
	MobileEnabled    *bool
	TabletEnabled    *bool
}

type UniverseClient interface {
	Get(ctx context.Context, universeID string) (Universe, error)
	Update(ctx context.Context, u Universe, mask []string) (Universe, error)
}

func NewUniverseClient(c *Client) UniverseClient { return &universeClient{c: c} }

type universeClient struct{ c *Client }

type universeWire struct {
	Path             string `json:"path,omitempty"`
	VoiceChatEnabled *bool  `json:"voiceChatEnabled,omitempty"`
	DesktopEnabled   *bool  `json:"desktopEnabled,omitempty"`
	MobileEnabled    *bool  `json:"mobileEnabled,omitempty"`
	TabletEnabled    *bool  `json:"tabletEnabled,omitempty"`
}

func (w universeWire) toUniverse(fallbackID string) Universe {
	id := strings.TrimPrefix(w.Path, "universes/")
	if id == "" {
		id = fallbackID
	}
	return Universe{
		ID:               id,
		VoiceChatEnabled: w.VoiceChatEnabled,
		DesktopEnabled:   w.DesktopEnabled,
		MobileEnabled:    w.MobileEnabled,
		TabletEnabled:    w.TabletEnabled,
	}
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
		VoiceChatEnabled: u.VoiceChatEnabled,
		DesktopEnabled:   u.DesktopEnabled,
		MobileEnabled:    u.MobileEnabled,
		TabletEnabled:    u.TabletEnabled,
	}
	path := "/cloud/v2/universes/" + u.ID + "?updateMask=" + strings.Join(mask, ",")
	var w universeWire
	if _, err := uc.c.Do(ctx, http.MethodPatch, path, body, &w); err != nil {
		return Universe{}, err
	}
	return w.toUniverse(u.ID), nil
}
