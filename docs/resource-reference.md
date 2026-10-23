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

## `roblox_analytics_alert`

Manages an analytics alert on an experience metric (production alerting, e.g. crash rate or concurrent players). EXPERIMENTAL Roblox API. Full create/update/real-delete lifecycle.

| Attribute | Type | Kind | Description |
|---|---|---|---|
| `breakdown_dimension` | string | optional, diffable | Evaluate the metric separately per value of this dimension (which must also be a key of `filters`). Set `""` to clear. |
| `consecutive_occurrences` | number | required, diffable | Consecutive breaching evaluations before the alert fires. `OneMinute` allows 6-30; the other intervals 1-10. |
| `description` | string | optional, diffable | Free-text description (at most 200 characters; moderated). Set `""` to clear. |
| `enabled` | bool | optional, diffable | Whether the alert is enabled (default `true`). `false` pauses it without deleting it. |
| `evaluation_mode` | string | optional, diffable | `Absolute` (default) compares the raw value; `PeriodOverPeriod` compares the percentage change from an earlier period. |
| `filters` | map of set of string | optional, diffable | Dimension filters: a map of dimension name to the allowed values, e.g. `{ Platform = ["Phone", "Tablet"] }`. Set `{}` to clear. |
| `interval` | string | required, diffable | Evaluation frequency: `OneMinute`, `HalfHour`, `OneHour` or `OneDay`. Not every metric supports every interval. |
| `metric` | string | required, diffable | Metric to evaluate, e.g. `ClientCrashRate15m`, `PeakConcurrentPlayers` (case-sensitive). See Roblox's alerts "Supported metrics" guide. |
| `name` | string | required, diffable | Alert name. Must be unique within the experience; at most 50 characters; subject to Roblox text moderation. |
| `operator` | string | required, diffable | Comparison operator: `Gt`, `Gte`, `Lt` or `Lte`. |
| `period_offset_multiplier` | number | optional, diffable | `PeriodOverPeriod` only: how many intervals back the comparison period is (Roblox defaults to 1). Must be omitted for `Absolute`. |
| `severity` | string | required, diffable | `SEV_0` (critical), `SEV_1` (medium) or `SEV_2` (low). |
| `threshold` | number | required, diffable | Value the metric is compared against. For `PeriodOverPeriod` it is a percentage change (e.g. `-30` = a 30% drop). |
| `universe_id` | string | required, create-only | The universe. Often a reference to an experience. |
| `webhook_configuration_ids` | set of string | optional, diffable | UUIDs of webhooks to notify when the alert fires or resolves. Webhooks can only be created in the Creator Dashboard (Webhooks); Open Cloud has no API for them. Set `[]` to remove all. |

Built on Roblox's EXPERIMENTAL analytics-alerts API (`analytics-alert-control-plane/v1`), which may change without notice. A universe can have at most 20 alerts. New alerts report `config_state = "Syncing"` briefly before `Enabled`. Removing an optional attribute from config stops managing it but leaves the remote value in place; to clear one, set it to its empty value (`""`, `{}`, `[]`). `universe_id` is create-only. Import with id `<universe_id>/<alert_id>`.

Requires the `universe.analytics.alert:read`, `universe.analytics.alert:write` [scopes](how-to/api-key-scopes.md).

Example: [`examples/analytics-alert.bp.hcl`](../examples/analytics-alert.bp.hcl).

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
