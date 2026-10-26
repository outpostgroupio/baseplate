# How-to: API key scopes

Every resource Baseplate manages maps to an Open Cloud API surface, and each surface requires
specific **scopes** on your API key. This guide lists what each resource needs and how to avoid the
common pitfalls.

## Scopes per resource

Grant the scopes for the resources you actually manage. Reads happen during `plan`/refresh, writes
during `apply`/`destroy`, so grant both sides.

| Resource | Scopes |
|---|---|
| `roblox_experience` | `universe:write` (plus `legacy-universe:manage` if you set `public`) |
| `roblox_place` | `universe-places:write` |
| `roblox_place_config` | `universe.place:read`, `universe.place:write` |
| `roblox_asset` | `asset:read`, `asset:write` (plus, optionally, `creator-store-product:read` for Decal `texture_id`s) |
| `roblox_developer_product` | `developer-product:read`, `developer-product:write` |
| `roblox_game_pass` | `game-pass:read`, `game-pass:write` |
| `roblox_badge` | `legacy-universe.badge:manage-and-spend-robux`, `legacy-universe.badge:write`, `legacy-badge:manage` |
| `roblox_universe_secret` | `universe.secret:read`, `universe.secret:write` |
| `roblox_experience_event` | `universe.event:read`, `universe.event:write` |
| `roblox_analytics_alert` | `universe.analytics.alert:read`, `universe.analytics.alert:write` |
| `roblox_experience_config` | `universe:read`, `universe:write` |

A few are easy to miss because they don't match the "obvious" name:

- **`universe-places:write`** for `roblox_place` is distinct from `universe:write` and from
  `universe.place:write`. Using the wrong one gives `401 … insufficient scope`. Conversely,
  `roblox_place_config` (name/description/server size) uses `universe.place:write`, not
  `universe-places:write`.
- **`legacy-universe:manage`** is needed only for `roblox_experience.public`, which flips
  visibility through Roblox's EXPERIMENTAL `legacy-develop` activate/deactivate endpoints (the v2
  universe API can't write visibility). Without it, apply fails with
  `403 Scope not authorized.` The other `roblox_experience` attributes need only `universe:write`.
  Even with the scope, Roblox can refuse to make an experience public with
  `403 You are not authorized to configure this universe.`. That isn't a scope problem, so check
  in Creator Hub that the experience can be made public.
- **`universe.secret:read` / `universe.secret:write`** are separate from `universe:write` — a key
  that can edit universe config still can't touch secrets without them.
- **`universe.event:read` / `universe.event:write`** for `roblox_experience_event` are also separate
  from `universe:write`. Without them every events call (even a read) fails with
  `403 Scope not authorized.`
- **`roblox_badge`** needs three legacy scopes: `legacy-universe.badge:manage-and-spend-robux`
  (create — the "spend-robux" name is a legacy of the old per-badge fee; creation is free in
  practice), `legacy-universe.badge:write` (update name/description/enabled), and
  `legacy-badge:manage`, which the spec advertises for icon upload — though live testing found the
  two `legacy-universe.badge:*` scopes alone sufficient for the full lifecycle, including icon
  upload. Grant all three to match the spec. Reads are public. The badge itself is never
  hard-deleted; `destroy` disables it.
- **`universe.analytics.alert:read` / `universe.analytics.alert:write`** for
  `roblox_analytics_alert` are their own scopes, separate from `universe:write`. Without them the
  gateway answers `403 Scope not authorized.` Even with the scopes, the experience itself must be
  eligible for alerts (the Performance dashboard's 100+ daily-active-user requirement; no opt-in
  is documented). Otherwise every call returns `403 PERMISSION_DENIED … not enrolled in
  PerformanceMonitoringV2`, which Baseplate reports as "not eligible for analytics alerts".
- **`roblox_experience_config`** uses the plain `universe:read` / `universe:write` scopes (no
  dedicated "config" scope). `plan` needs `universe:read` (reads the published config and checks for
  a pending draft); `apply` and `destroy` need `universe:write` (draft + publish). Without view
  access the API answers `403 "User does not have View permission for universe …"`.
- **`creator-store-product:read`** is optional, and only `roblox_asset` uses it: it's how
  Baseplate finds a **Decal**'s `texture_id` (the Image asset behind the decal), through
  toolbox-service. Despite the name, it works for your own private decals, not only Creator Store
  items. Image assets don't need it, since an Image's `texture_id` is its own `id`. Without the
  scope, plan and apply still succeed; Decals just have no `texture_id`, and Baseplate prints a
  warning naming the scope.

## Scopes for commands

Commands that act on Roblox directly, rather than through a resource, need scopes too:

| Command | Scopes |
|---|---|
| `baseplate restart-servers`, `apply --restart-servers` | `universe:write` |

`apply --restart-servers` needs `universe:write` on top of the scopes for the resources you apply
(e.g. `universe-places:write` for the `roblox_place` that gets published).

## Scopes are per-universe — and you must save

The single most common failure isn't the scope itself, it's **the universe binding**. On the
[Creator Dashboard → Credentials](https://create.roblox.com/dashboard/credentials) page, an API
key's scopes are granted **per universe**:

1. Add the target universe to the key.
2. Tick the scopes above **for that universe**.
3. **Save** the key.

If the scope is ticked but the specific universe isn't selected (or you forgot to save), calls fail
with:

```
403  Authorization scope does not allow this operation
```

A scope that's missing entirely (not just unbound) typically shows as:

```
401  … insufficient scope
```

## Where the key comes from

Baseplate reads the key from the environment via `env()` in your config:

```hcl
provider "roblox" {
  api_key = env("ROBLOX_API_KEY")
}
```

Export it in your shell, or keep it in a gitignored `.env` and source it. Never commit an API
key.

## See also

- [Getting started](../getting-started.md#3-api-keys--scopes)
- [Resource reference](../resource-reference.md)
