# API key scopes

Each resource uses a different part of Open Cloud, and each part needs its own scopes on your API
key. `plan` reads and `apply` writes, so grant both the read and write scopes for the resources you
manage.

| Resource | Scopes |
|---|---|
| `roblox_experience` | `universe:write`, plus `legacy-universe:manage` if you set `public` |
| `roblox_place` | `universe-places:write` |
| `roblox_place_config` | `universe.place:read`, `universe.place:write` |
| `roblox_place_media` | `asset:read`, `asset:write` |
| `roblox_asset` | `asset:read`, `asset:write`, and optionally `creator-store-product:read` for a Decal's `texture_id` |
| `roblox_developer_product` | `developer-product:read`, `developer-product:write` |
| `roblox_game_pass` | `game-pass:read`, `game-pass:write` |
| `roblox_badge` | `legacy-universe.badge:manage-and-spend-robux`, `legacy-universe.badge:write`, `legacy-badge:manage` |
| `roblox_universe_secret` | `universe.secret:read`, `universe.secret:write` |
| `roblox_experience_event` | `universe.event:read`, `universe.event:write` |
| `roblox_analytics_alert` | `universe.analytics.alert:read`, `universe.analytics.alert:write` |
| `roblox_experience_config` | `universe:read`, `universe:write` |
| `restart-servers`, `apply --restart-servers` | `universe:write` |

When Roblox refuses the key, the error names the resource, the request that failed, and the scopes
that resource needs:

```
Error: roblox_place.main: Roblox API 403 on POST /universes/v1/123/places/456/versions: Scope not authorized. Check that the API key has the scopes roblox_place needs: universe-places:write (see …)
```

## Scopes are per universe

In [Creator Dashboard → Credentials](https://create.roblox.com/dashboard/credentials), scopes are
granted per universe. Add the universe to the key, tick the scopes for that universe, then save the
key. If a scope is ticked but the universe isn't selected, or the key wasn't saved, calls fail with
`403 Authorization scope does not allow this operation`. A scope that's missing altogether usually
gives `401 insufficient scope` or `403 Scope not authorized.`

## Scopes that are easy to get wrong

`roblox_place` publishes with `universe-places:write`. That's a different scope from
`universe:write` and from `universe.place:write`, which `roblox_place_config` uses for a place's
name, description and server size.

`roblox_experience.public` switches visibility through an experimental legacy endpoint, which needs
`legacy-universe:manage`. The rest of `roblox_experience` only needs `universe:write`. If making
the experience public fails with `403 You are not authorized to configure this universe.` even
though the scope is granted, complete the experience's maturity questionnaire in Creator Hub first.

Secrets, experience events and analytics alerts each have their own scopes, separate from
`universe:write`. Without them every call fails, reads included.

Analytics alerts also need an eligible experience: one that meets the Performance dashboard's
requirement of 100+ daily active users. Otherwise Roblox returns `403 ... not enrolled in
PerformanceMonitoringV2`, which Baseplate reports as "not eligible for analytics alerts".

`roblox_badge` uses legacy badge scopes. `legacy-universe.badge:manage-and-spend-robux` is for
creating badges (the name dates from when badges cost Robux), `legacy-universe.badge:write` is for
updates, and Roblox's spec lists `legacy-badge:manage` for icon uploads. Grant all three.

`creator-store-product:read` is optional. `roblox_asset` uses it to look up the Image behind a
Decal, which is the Decal's `texture_id`. It works for your own private decals despite the name.
Without it, plans and applies still succeed, Decals have no `texture_id`, and Baseplate prints a
warning. Image assets don't need it, because an Image's `texture_id` is its own id.

## Supplying the key

Read the key from the environment in your config, and never commit it:

```hcl
provider "roblox" {
  api_key = env("ROBLOX_API_KEY")
}
```
