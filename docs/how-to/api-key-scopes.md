# How-to: API key scopes

Every resource Baseplate manages maps to an Open Cloud API surface, and each surface requires
specific **scopes** on your API key. This guide lists what each resource needs and how to avoid the
common pitfalls.

## Scopes per resource

Grant the scopes for the resources you actually manage. Reads happen during `plan`/refresh, writes
during `apply`/`destroy`, so grant both sides.

| Resource | Scopes |
|---|---|
| `roblox_experience` | `universe:write` |
| `roblox_place` | `universe-places:write` |
| `roblox_asset` | `asset:read`, `asset:write` |
| `roblox_developer_product` | `developer-product:read`, `developer-product:write` |
| `roblox_game_pass` | `game-pass:read`, `game-pass:write` |
| `roblox_badge` | `legacy-universe.badge:manage-and-spend-robux`, `legacy-universe.badge:write`, `legacy-badge:manage` |
| `roblox_universe_secret` | `universe.secret:read`, `universe.secret:write` |
| `roblox_analytics_alert` | `universe.analytics.alert:read`, `universe.analytics.alert:write` |

A few are easy to miss because they don't match the "obvious" name:

- **`universe-places:write`** for `roblox_place` is distinct from `universe:write` and from
  `universe.place:write`. Using the wrong one gives `401 … insufficient scope`.
- **`universe.secret:read` / `universe.secret:write`** are separate from `universe:write` — a key
  that can edit universe config still can't touch secrets without them.
- **`roblox_badge`** needs three legacy scopes: `legacy-universe.badge:manage-and-spend-robux`
  (create — the "spend-robux" name is a legacy of the old per-badge fee; creation is free in
  practice), `legacy-universe.badge:write` (update name/description/enabled), and
  `legacy-badge:manage`, which the spec advertises for icon upload — though live testing found the
  two `legacy-universe.badge:*` scopes alone sufficient for the full lifecycle, including icon
  upload. Grant all three to match the spec. Reads are public. The badge itself is never
  hard-deleted; `destroy` disables it.
- **`universe.analytics.alert:read` / `universe.analytics.alert:write`** for
  `roblox_analytics_alert` are their own scopes, separate from `universe:write`. Without them the
  gateway answers `403 Scope not authorized.` Roblox also says the experience itself must be
  eligible for analytics alerts, or every call returns `403 PERMISSION_DENIED`.

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

Export it in your shell, or keep it in a gitignored `.env.local` and source it. Never commit an API
key.

## See also

- [Getting started](../getting-started.md#3-api-keys--scopes)
- [Resource reference](../resource-reference.md)
