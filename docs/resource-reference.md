# Resource reference

The resources Baseplate can manage, and their attributes.

> **Generated file** — do not edit by hand. Run `go generate ./...` to regenerate from each
> resource's schema and `Doc()`.

**Legend for the _Kind_ column:**

- **required** / **optional** — whether the attribute must be set in config.
- **create-only** — set at create; changing it in config does **not** produce an update (it's
  excluded from the diff). To change it, taint/recreate the resource.
- **diffable** — a normal managed attribute; a config change plans an update.
- **sensitive** — redacted from all plan output and never written to state.
- **trigger** — not diffed directly; a change is detected via a hash/marker and forces an update.

Every resource also has an implicit `id` (Baseplate's state id) — you never set it in config.

---

## `roblox_asset`

Uploads a user- or group-owned image/decal asset.

| Attribute | Type | Kind | Description |
|---|---|---|---|
| `asset_type` | string | optional, diffable | Asset type (e.g. image/decal). |
| `creator_group_id` | number | optional, create-only | Owning group id (set exactly one of creator_user_id/creator_group_id). |
| `creator_user_id` | number | optional, create-only | Owning user id (set exactly one of creator_user_id/creator_group_id). |
| `description` | string | required, diffable | Asset description. |
| `display_name` | string | required, diffable | Asset name. |
| `file` | string | required, create-only | Path to the image to upload. |

The `id` output is the `assetId` (a numeric string). Image/decal assets are not archivable, so `destroy` unmanages the asset in place (drops it from state, leaves it uploaded).

Requires the `asset:read`, `asset:write` [scopes](how-to/api-key-scopes.md).

---

## `roblox_badge`

Manages a badge (an achievement players can earn in an experience).

| Attribute | Type | Kind | Description |
|---|---|---|---|
| `description` | string | optional, diffable | Badge description. |
| `enabled` | bool | optional, diffable | Whether the badge is active and awardable (default `true`). |
| `expected_cost` | number | optional, create-only | Robux cost you accept for creating the badge (default `0`). Creation is free in practice; a non-zero value opts into a paid badge. |
| `icon_file` | string | required, trigger | Path to an icon image (direct upload, required — Roblox rejects a badge created without an icon). Change detected via content hash. |
| `name` | string | required, diffable | Badge name. |
| `payment_source` | string | optional, create-only | Funding source when `expected_cost` > 0: `user` (default) or `group`. |
| `universe_id` | string | required, create-only | The universe. Often a reference to an experience. |

Badges can't be hard-deleted, so `destroy` sets `enabled = false` and unmanages the badge. The icon is reconciled via a content-hash trigger; any update re-uploads the icon. Reads use the public badge endpoint on `badges.roblox.com` (writes use Open Cloud on `apis.roblox.com`).

Requires the `legacy-universe.badge:manage-and-spend-robux`, `legacy-universe.badge:write`, `legacy-badge:manage` [scopes](how-to/api-key-scopes.md).

Example: [`examples/badge.bp.hcl`](../examples/badge.bp.hcl).

---

## `roblox_developer_product`

Manages a developer product (an in-experience consumable purchase).

| Attribute | Type | Kind | Description |
|---|---|---|---|
| `description` | string | optional, diffable | Product description. |
| `icon_file` | string | optional, create-only | Path to an icon image (direct upload). |
| `is_for_sale` | bool | optional, diffable | On sale (default `true`). |
| `name` | string | required, diffable | Product name. |
| `price` | number | required, diffable | Price in Robux. |
| `universe_id` | string | required, create-only | The universe. Often a reference to an experience. |

Developer products can't be hard-deleted, so `destroy` sets `is_for_sale = false` and unmanages it.

Requires the `developer-product:read`, `developer-product:write` [scopes](how-to/api-key-scopes.md).

Example: [`examples/baseplate.bp.hcl`](../examples/baseplate.bp.hcl).

---

## `roblox_experience`

Manages a universe's writable configuration. Adopts an existing universe (create one in Creator Hub first); it does not create universes.

| Attribute | Type | Kind | Description |
|---|---|---|---|
| `desktop_enabled` | bool | optional, diffable | Playable on desktop. |
| `mobile_enabled` | bool | optional, diffable | Playable on mobile. |
| `tablet_enabled` | bool | optional, diffable | Playable on tablet. |
| `universe_id` | string | required, diffable | The universe to manage. Its `id` output equals this. |
| `voice_chat_enabled` | bool | optional, diffable | Enable voice chat. |

`displayName`/`description`/`visibility`/`ageRating` are read-only on the Open Cloud v2 API (they're set via the root place), so they are not manageable here. `destroy` unmanages the universe (it is never deleted).

Requires the `universe:write` [scope](how-to/api-key-scopes.md).

Example: [`examples/baseplate.bp.hcl`](../examples/baseplate.bp.hcl).

---

## `roblox_game_pass`

Manages a game pass (a one-time, permanent in-experience purchase).

| Attribute | Type | Kind | Description |
|---|---|---|---|
| `description` | string | optional, diffable | Game pass description. |
| `icon_file` | string | optional, create-only | Path to an icon image (direct upload). |
| `is_for_sale` | bool | optional, diffable | On sale (default `true`). |
| `name` | string | required, diffable | Game pass name. |
| `price` | number | required, diffable | Price in Robux. |
| `universe_id` | string | required, create-only | The universe. Often a reference to an experience. |

Game passes can't be hard-deleted, so `destroy` sets `is_for_sale = false` and unmanages it. Managed pricing is intentionally not exposed — it requires prior manual acceptance of Roblox's managed-pricing terms in the Creator Dashboard.

Requires the `game-pass:read`, `game-pass:write` [scopes](how-to/api-key-scopes.md).

Example: [`examples/game-pass.bp.hcl`](../examples/game-pass.bp.hcl).

---

## `roblox_place`

Publishes a place file (`.rbxl`/`.rbxlx`) to an existing place and re-publishes when the file content changes.

| Attribute | Type | Kind | Description |
|---|---|---|---|
| `file` | string | required, trigger | Path to the `.rbxl`/`.rbxlx`. Change detected via content hash. |
| `place_id` | string | required, create-only | The existing place to publish to. |
| `universe_id` | string | required, create-only | The universe the place belongs to. |
| `version_type` | string | optional, trigger | `Published` (default) or `Saved`. |

Place content isn't remotely readable, so change detection is by file content hash. `destroy` is a no-op (a place can't be unpublished).

Requires the `universe-places:write` [scope](how-to/api-key-scopes.md).

---

## `roblox_universe_secret`

Manages an Open Cloud secret for a universe — the only resource with a full create/update/real delete lifecycle. The value is encrypted client-side (LibSodium sealed box) before upload and is never written to state or printed in a plan.

| Attribute | Type | Kind | Description |
|---|---|---|---|
| `domain` | string | optional, diffable | HttpService domain wildcard (e.g. `api.example.com`, `*`). Omit for a private key. |
| `name` | string | required, create-only | Secret name (the API id): 1-64 chars, alphanumeric/underscore, not starting with a digit. |
| `universe_id` | string | required, create-only | The universe. |
| `value` | string | required, trigger, sensitive | The plaintext. Usually `env("…")`. Sealed client-side; change detected via a hash. |

Renaming a secret (`name`) is a recreate, not an in-place rename.

Requires the `universe.secret:read`, `universe.secret:write` [scopes](how-to/api-key-scopes.md).
