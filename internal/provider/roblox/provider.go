package roblox

import (
	"errors"

	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/builtin"
	"github.com/outpostgroupio/baseplate/internal/provider/roblox/opencloud"
	"github.com/outpostgroupio/baseplate/internal/provider/roblox/resources"
)

const (
	// rootBase serves the cloud (universe), developer-products and
	// creator-configs (Experience Config) APIs.
	rootBase = "https://apis.roblox.com"
	// assetsBase serves the assets API (paths are /v1/assets...).
	assetsBase = "https://apis.roblox.com/assets"
	// badgesBase serves the public legacy badge READ endpoint (badges.roblox.com);
	// badge writes go through rootBase (apis.roblox.com). See the badge design spec.
	badgesBase = "https://badges.roblox.com"
)

// Provider is the real Roblox Open Cloud provider.
type Provider struct {
	resources map[string]provider.Resource
	// restarter backs the provider.ServerRestarter capability (restart.go).
	restarter opencloud.ServerRestartClient
}

// New builds the Roblox provider from an Open Cloud API key. The key must be
// non-empty; it is used as the x-api-key on every request.
func New(apiKey string) (*Provider, error) {
	if apiKey == "" {
		return nil, errors.New(`roblox: ROBLOX_API_KEY is required (provide it via env and reference it with env("ROBLOX_API_KEY"))`)
	}
	root := opencloud.New(apiKey, rootBase)
	assets := opencloud.New(apiKey, assetsBase)
	badges := opencloud.New(apiKey, badgesBase)
	return &Provider{resources: map[string]provider.Resource{
		"roblox_experience":        resources.Experience(opencloud.NewUniverseClient(root)),
		"roblox_asset":             resources.Asset(opencloud.NewAssetClient(assets), opencloud.NewTextureClient(root)),
		"roblox_developer_product": resources.DeveloperProduct(opencloud.NewDeveloperProductClient(root)),
		"roblox_game_pass":         resources.GamePass(opencloud.NewGamePassClient(root)),
		"roblox_badge":             resources.Badge(opencloud.NewBadgeClient(root, badges)),
		"roblox_place":             resources.Place(opencloud.NewPlaceClient(root)),
		"roblox_place_config":      resources.PlaceConfig(opencloud.NewPlaceConfigClient(root)),
		"roblox_universe_secret":   resources.UniverseSecret(opencloud.NewSecretClient(root)),
		"roblox_experience_event":  resources.ExperienceEvent(opencloud.NewGameEventClient(root)),
		"roblox_analytics_alert":   resources.AnalyticsAlert(opencloud.NewAnalyticsAlertClient(root)),
		"roblox_experience_config": resources.ExperienceConfig(opencloud.NewExperienceConfigClient(root)),
		builtin.DataType:           builtin.Data(),
	}, restarter: opencloud.NewServerRestartClient(root)}, nil
}

// Resources implements provider.Provider.
func (p *Provider) Resources() map[string]provider.Resource { return p.resources }

// SchemaProvider builds the provider's resource set with nil Open Cloud clients,
// for schema/documentation introspection only (e.g. the resource-docs generator).
// The resources' network methods (Create/Read/Update/Delete) must not be called.
func SchemaProvider() *Provider {
	return &Provider{resources: map[string]provider.Resource{
		"roblox_experience":        resources.Experience(nil),
		"roblox_asset":             resources.Asset(nil, nil),
		"roblox_developer_product": resources.DeveloperProduct(nil),
		"roblox_place":             resources.Place(nil),
		"roblox_place_config":      resources.PlaceConfig(nil),
		"roblox_universe_secret":   resources.UniverseSecret(nil),
		"roblox_game_pass":         resources.GamePass(nil),
		"roblox_badge":             resources.Badge(nil),
		"roblox_experience_event":  resources.ExperienceEvent(nil),
		"roblox_analytics_alert":   resources.AnalyticsAlert(nil),
		"roblox_experience_config": resources.ExperienceConfig(nil),
		builtin.DataType:           builtin.Data(),
	}}
}
