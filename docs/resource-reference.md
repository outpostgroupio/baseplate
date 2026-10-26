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

Built on Roblox's EXPERIMENTAL analytics-alerts API (`analytics-alert-control-plane/v1`), which may change without notice. Alerts are only available to experiences that meet the Performance dashboard eligibility requirement (100+ daily active users). Roblox documents no opt-in setting. Other experiences get `403 … not enrolled in PerformanceMonitoringV2` on every call, which Baseplate reports as "not eligible for analytics alerts". A universe can have at most 20 alerts.New alerts report `config_state = "Syncing"` briefly before `Enabled`. Removing an optional attribute from config stops managing it but leaves the remote value in place; to clear one, set it to its empty value (`""`, `{}`, `[]`). `universe_id` is create-only. Import with id `<universe_id>/<alert_id>`.

Requires the `universe.analytics.alert:read`, `universe.analytics.alert:write` [scopes](how-to/api-key-scopes.md).

Example: [`examples/analytics-alert.bp.hcl`](../examples/analytics-alert.bp.hcl).

---

## `roblox_asset`

Uploads a user- or group-owned image, decal or audio asset.

| Attribute | Type | Kind | Description |
|---|---|---|---|
| `asset_type` | string | optional, diffable | Asset type: `Image`, `Decal` or `Audio`. Defaults to `Audio` for audio files and `Image` otherwise. Use `Image` for anything the game shows in an `ImageLabel` or `Decal`. Roblox can't change an existing asset's type. |
| `creator_group_id` | number | optional, create-only | Owning group id (set exactly one of creator_user_id/creator_group_id). |
| `creator_user_id` | number | optional, create-only | Owning user id (set exactly one of creator_user_id/creator_group_id). |
| `description` | string | required, diffable | Asset description. |
| `display_name` | string | required, diffable | Asset name. |
| `file` | string | required, create-only | Path to the file to upload: `.png`/`.jpg`/`.bmp`/`.tga` for images and decals, `.mp3`/`.ogg`/`.wav`/`.flac` for audio. |

The `id` output is the `assetId` (a numeric string). For an `Image` asset (the default for image files) that id is the texture id: use it directly as `rbxassetid://<id>` in `ImageLabel.Image` or `Decal.Texture`. A `Decal` asset's id is not a texture id. Each Decal wraps a separate Image asset that Roblox creates alongside it, owned by the same creator. To adopt assets from a Decal-based uploader, import those underlying image ids, not the decal ids (see the many-assets how-to). Changing `file` doesn't re-upload: Open Cloud only accepts new content for Model assets, so a changed image or sound needs a new resource (and gets a new id). Audio is limited to 7 minutes and 20 MB, and Roblox caps audio uploads per month (10, or 100 for ID-verified accounts). Image/decal assets are not archivable, so `destroy` unmanages the asset in place (drops it from state, leaves it uploaded).

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
| `console_enabled` | bool | optional, diffable | Playable on console. |
| `desktop_enabled` | bool | optional, diffable | Playable on desktop. |
| `discord_link_title` | string | optional, diffable | Discord social link title (1–70 characters). |
| `discord_link_uri` | string | optional, diffable | Discord social link URL. `""` removes the link. Creating a link needs `discord_link_title` too. |
| `facebook_link_title` | string | optional, diffable | Facebook social link title (1–70 characters). |
| `facebook_link_uri` | string | optional, diffable | Facebook social link URL. `""` removes the link. Creating a link needs `facebook_link_title` too. |
| `guilded_link_title` | string | optional, diffable | Guilded social link title (1–70 characters). |
| `guilded_link_uri` | string | optional, diffable | Guilded social link URL. `""` removes the link. Creating a link needs `guilded_link_title` too. |
| `mobile_enabled` | bool | optional, diffable | Playable on mobile. |
| `private_server_price` | number | optional, diffable | Private server price in Robux: `0` (free) or 10–500. Setting it enables private servers. |
| `private_servers_enabled` | bool | optional, diffable | Whether private servers can be bought. `false` disables them (and must not be combined with `private_server_price`). `true` needs `private_server_price` unless they're already enabled. |
| `public` | bool | optional, diffable | Whether the experience is public (`true`) or private (`false`). Uses the EXPERIMENTAL legacy-develop activate/deactivate API and needs the extra `legacy-universe:manage` scope. Roblox can still refuse to make an experience public (403 "You are not authorized to configure this universe."); check it can be made public in Creator Hub. |
| `roblox_group_link_title` | string | optional, diffable | Roblox group (community) social link title (1–70 characters). |
| `roblox_group_link_uri` | string | optional, diffable | Roblox group (community) social link URL. `""` removes the link. Creating a link needs `roblox_group_link_title` too. |
| `tablet_enabled` | bool | optional, diffable | Playable on tablet. |
| `twitch_link_title` | string | optional, diffable | Twitch social link title (1–70 characters). |
| `twitch_link_uri` | string | optional, diffable | Twitch social link URL. `""` removes the link. Creating a link needs `twitch_link_title` too. |
| `twitter_link_title` | string | optional, diffable | Twitter/X social link title (1–70 characters). |
| `twitter_link_uri` | string | optional, diffable | Twitter/X social link URL. `""` removes the link. Creating a link needs `twitter_link_title` too. |
| `universe_id` | string | required, diffable | The universe to manage. Its `id` output equals this. |
| `voice_chat_enabled` | bool | optional, diffable | Enable voice chat. |
| `vr_enabled` | bool | optional, diffable | Playable in VR. |
| `youtube_link_title` | string | optional, diffable | YouTube social link title (1–70 characters). |
| `youtube_link_uri` | string | optional, diffable | YouTube social link URL. `""` removes the link. Creating a link needs `youtube_link_title` too. |

Only declared attributes are managed. `displayName`/`description`/`ageRating` are read-only on the Open Cloud v2 API. The display name and description follow the root place's, so manage them with `roblox_place_config` on the root place. A social link's URL is validated by Roblox for its platform (e.g. a `discord.gg` invite for Discord). `public` additionally needs the `legacy-universe:manage` scope. `destroy` unmanages the universe (it is never deleted, and nothing is reverted).

Requires the `universe:write` [scope](how-to/api-key-scopes.md).

Example: [`examples/baseplate.bp.hcl`](../examples/baseplate.bp.hcl).

---

## `roblox_experience_config`

Manages a universe's Experience Config (BETA): the key/value settings a published game reads at runtime through `ConfigService` — declarative feature flags and tuning values. Each apply stages the key changes as a draft and publishes it.

| Attribute | Type | Kind | Description |
|---|---|---|---|
| `deployment_strategy` | string | optional, create-only | `Immediate` (default) or `GradualRollout` (~15-20 min). Used for every publish, but not readable back, so it isn't diffed (listed as create-only): changing it alone doesn't publish. |
| `entries` | any | required, diffable | Map of config key to value. Values are written as native HCL: bool, number, string, or an object/list (a JSON config), e.g. `{ enable_tutorial = true, boss_health = 100, shop = { discount = 0.2 } }`. **Authoritative:** keys in the repository but not here are removed on apply. Max 100 keys, 256-char keys; `.RBX.` is reserved in keys. |
| `repository` | string | optional, create-only | Config repository. Default `InExperienceConfig` (the only one Roblox documents). |
| `universe_id` | string | required, create-only | The universe. |

The resource owns the repository's base keys: an apply removes any published key that isn't in `entries`. To protect an existing game, the **first** apply (create) refuses if the repository already has keys that aren't declared — `import` it first so the plan shows what would be removed. Conditional rules and the conditional (targeted) values of keys you keep are **preserved** (Baseplate never sends them, and verifies the staged draft and the published result); a removed key's conditional values are removed with it, and a type change for a key with conditional values is rejected by Roblox. Apply refuses if the repository has an unpublished draft (e.g. pending edits in Creator Hub) — publish or discard it first. `destroy` removes every plain key in the repository and publishes (the same ownership as apply; conditional rules are left alone, and an empty or never-published repository is a no-op). Protect production config with [`lifecycle { prevent_destroy = true }`](config-reference.md#lifecycle), which makes any plan that would destroy it fail. With `GradualRollout` (observed ~20 minutes), the API keeps returning the previous values until the rollout completes and rejects new drafts meanwhile, so a re-plan in that window shows the change again and an apply fails with a retry-later error. Numbers are stored as doubles (exact only up to 2^53).

Requires the `universe:read`, `universe:write` [scopes](how-to/api-key-scopes.md).

Example: [`examples/experience-config.bp.hcl`](../examples/experience-config.bp.hcl).

---

## `roblox_experience_event`

Manages an experience event (virtual event) for live-ops: a scheduled, titled event shown on the experience page. Uses the EXPERIMENTAL `virtual-events/v3` Open Cloud API.

| Attribute | Type | Kind | Description |
|---|---|---|---|
| `description` | string | optional, diffable | Event description. Set `""` to clear it. |
| `end_time` | string | required, diffable | End time, RFC 3339. Must be at least 15 minutes after `start_time`. |
| `featuring_enabled` | bool | optional, diffable | Opt the event into featuring review (`featuringStatus`). Defaults to Roblox's "not featured". |
| `group_id` | string | optional, create-only | Host group id. When unset, the API key's owner hosts the event. Create-only. |
| `place_id` | string | optional, diffable | Venue place id (a place in the universe). When unset, Roblox records no venue (`placeId` 0). |
| `start_time` | string | required, diffable | Start time, RFC 3339 (e.g. `2026-12-01T18:00:00Z`). Any offset is accepted; it is compared and stored in UTC at whole-second precision. Must be in the future when the event is created or the start is changed. |
| `subtitle` | string | required, diffable | Event subtitle (required by Roblox). |
| `tagline` | string | optional, diffable | Tagline shown to curators when featuring is enabled. Can't be cleared once set (Roblox ignores an empty tagline), so it must not be `""`. |
| `thumbnail_media_ids` | list of string | optional, diffable | Ordered list of thumbnail image asset ids (the first is the primary). `[]` clears them. |
| `title` | string | required, diffable | Event title. |
| `universe_id` | string | required, create-only | The universe that owns the event. Often a reference to an experience. Create-only. |
| `visibility` | string | required, diffable | `public` or `private` (case-insensitive). Roblox may set `moderated` server-side, which then shows as drift. |

`destroy` permanently deletes the event. Import by event id (`baseplate import roblox_experience_event.x <eventId>`). Changing `universe_id` or `group_id` does not move the event (both are create-only). An update sends only the fields that differ from the live event. Events that have started or ended can still be edited and deleted, but Roblox rejects any *new* `start_time` in the past. Categories and recurrence are not supported: the API currently rejects every categories payload, and recurrence has no effect.

Requires the `universe.event:read`, `universe.event:write` [scopes](how-to/api-key-scopes.md).

Example: [`examples/experience-event.bp.hcl`](../examples/experience-event.bp.hcl).

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

## `roblox_place_config`

Manages an existing place's configuration (name, description, server size). Adopts the place; it does not create places.

| Attribute | Type | Kind | Description |
|---|---|---|---|
| `description` | string | optional, diffable | The place's description. On the root place this is also the experience's description. |
| `display_name` | string | optional, diffable | The place's name. On the root place this is also the experience's name. |
| `place_id` | string | required, create-only | The place to configure. |
| `server_size` | number | optional, diffable | The maximum number of players in a single server. |
| `universe_id` | string | required, create-only | The universe the place belongs to. |

Only declared attributes are managed; omitted ones are left as they are in Roblox. The experience's display name and description are read-only on the universe API, so set them here on the root place. `destroy` unmanages the place (nothing is reverted or deleted). Roblox rejects changes (409) while the place is open in Team Create. Independent of `roblox_place`, which publishes the place file.

Requires the `universe.place:read`, `universe.place:write` [scopes](how-to/api-key-scopes.md).

Example: [`examples/baseplate.bp.hcl`](../examples/baseplate.bp.hcl).

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
