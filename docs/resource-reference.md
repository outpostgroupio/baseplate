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

Uploads a user- or group-owned image, decal or audio asset.

| Attribute | Type | Kind | Description |
|---|---|---|---|
| `asset_type` | string | optional, diffable | Asset type: `Image`, `Decal` or `Audio`. Defaults to `Audio` for audio files and `Image` otherwise. |
| `creator_group_id` | number | optional, create-only | Owning group id (set exactly one of creator_user_id/creator_group_id). |
| `creator_user_id` | number | optional, create-only | Owning user id (set exactly one of creator_user_id/creator_group_id). |
| `description` | string | required, diffable | Asset description. |
| `display_name` | string | required, diffable | Asset name. |
| `file` | string | required, create-only | Path to the file to upload: `.png`/`.jpg`/`.bmp`/`.tga` for images and decals, `.mp3`/`.ogg`/`.wav`/`.flac` for audio. |

The `id` output is the `assetId` (a numeric string). Changing `file` doesn't re-upload: Open Cloud only accepts new content for Model assets, so a changed image or sound needs a new resource (and gets a new id). Audio is limited to 7 minutes and 20 MB, and Roblox caps audio uploads per month (10, or 100 for ID-verified accounts). Image/decal assets are not archivable, so `destroy` unmanages the asset in place (drops it from state, leaves it uploaded).

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
| `public` | bool | optional, diffable | Whether the experience is public (`true`) or private (`false`). Uses the EXPERIMENTAL legacy-develop activate/deactivate API and needs the extra `legacy-universe:manage` scope. |
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
