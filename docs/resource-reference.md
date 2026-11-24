# Resource reference

The resources Baseplate can manage, and their attributes.

> This file is generated from each resource's schema and `Doc()`. Don't edit it by hand; run
> `go generate ./...` instead.

The _Kind_ column uses these terms:

- required / optional: whether the attribute must be set in config.
- create-only: used when the resource is created. Changing it later doesn't plan an update
  (it's left out of the diff), so to change it you have to recreate the resource.
- diffable: a normal managed attribute. Changing it in config plans an update.
- sensitive: redacted from all plan output and never written to state.
- trigger: not compared directly. Baseplate detects a change through a hash or marker and plans an
  update, or a replace where the resource notes say so (see `-/+` in the CLI reference).
- computed: an output Baseplate reads from Roblox. You can't set it, but you can reference it like
  `id` (`type.name.attr`, resolved at apply time), and `baseplate show --json` lists it.

Every resource also has an implicit `id` (Baseplate's state id), which you never set in config.

---

## `baseplate_data`

Stores a value in state, for `lifecycle { replace_triggered_by }`. The equivalent of Terraform's `terraform_data`; it makes no requests.

| Attribute | Type | Kind | Description |
|---|---|---|---|
| `input` | any | optional, diffable | Any value. A change updates the resource, which replaces any resource that lists it in `replace_triggered_by`. |
| `triggers_replace` | any | optional, diffable | Any value. A change replaces this resource (with a new `id`). |
| `output` | any | computed | The value of `input`. |

To replace a resource whenever a value changes, store the value in a `baseplate_data` and list it in the resource's `lifecycle { replace_triggered_by = [...] }`; with `for_each`, give both the same keys and reference `baseplate_data.<name>[each.key]`. Creating a `baseplate_data` doesn't trigger a replace, so adding one for existing resources replaces nothing. Importing one is optional, and only keeps an adoption plan from listing an add for each: there is nothing remote to read, so the import takes the values from config and plans no change, and the import id (any string no other `baseplate_data` uses) becomes its `id`. One whose `input` refers to a resource that isn't created yet can't be imported until that resource is applied.

---

## `roblox_analytics_alert`

Manages an analytics alert on an experience metric such as crash rate or concurrent players. Uses an experimental Roblox API. Alerts can be created, updated and deleted.

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

Built on Roblox's experimental analytics alerts API (`analytics-alert-control-plane/v1`), which may change without notice. Alerts are only available to experiences that meet the Performance dashboard eligibility requirement (100+ daily active users). Roblox documents no opt-in setting. Other experiences get `403 … not enrolled in PerformanceMonitoringV2` on every call, which Baseplate reports as "not eligible for analytics alerts". A universe can have at most 20 alerts. New alerts report `config_state = "Syncing"` briefly before `Enabled`. Removing an optional attribute from config stops managing it but leaves the remote value in place; to clear one, set it to its empty value (`""`, `{}`, `[]`). `universe_id` is create-only. Import with id `<universe_id>/<alert_id>`.

Requires the `universe.analytics.alert:read`, `universe.analytics.alert:write` [scopes](how-to/api-key-scopes.md).

Example: [`examples/analytics-alert.bp.hcl`](../examples/analytics-alert.bp.hcl).

---

## `roblox_asset`

Uploads a user- or group-owned image, decal, audio or model asset.

| Attribute | Type | Kind | Description |
|---|---|---|---|
| `asset_type` | string | optional, diffable | Asset type: `Image`, `Decal`, `Audio` or `Model`. Defaults to `Audio` for audio files, `Model` for model files and `Image` otherwise. Changing it (including the default, by changing the file's extension) replaces the asset, because Roblox can't change an existing asset's type. Use `Image` for anything the game shows in an `ImageLabel` or `Decal`. |
| `creator_group_id` | number | optional, create-only | Owning group id (set exactly one of creator_user_id/creator_group_id). |
| `creator_user_id` | number | optional, create-only | Owning user id (set exactly one of creator_user_id/creator_group_id). |
| `description` | string | required, diffable | Asset description. |
| `display_name` | string | required, diffable | Asset name. |
| `file` | string | required, trigger | Path to the file to upload: `.png`/`.jpg`/`.bmp`/`.tga` for images and decals, `.mp3`/`.ogg`/`.wav`/`.flac` for audio, `.fbx`/`.gltf`/`.glb`/`.rbxm`/`.rbxmx` for models. A change to the file's content is detected by hash: an Image or Model takes the new content in place (same id); a Decal, Audio or Video asset is replaced (a new upload with a new id; the old asset is left in place). |
| `source_hash` | string | optional, trigger | Optional. A value that changes when the asset's source changes, used instead of the hash of `file` to detect a content change, e.g. `filesha256("art/logo.svg")` for a PNG generated from an SVG. Then regenerating `file` with different bytes from the same source (a new converter version, say) doesn't upload it again, and a plan doesn't need `file` to exist. Adding it to an existing asset doesn't upload anything. |
| `texture_id` | string | computed | The texture id, for `rbxassetid://<texture_id>` in `ImageLabel.Image`, `ImageButton.Image` or `Decal.Texture`. For an `Image` asset it equals `id`. For a `Decal` it is the id of the separate Image asset Roblox creates behind the decal, looked up through toolbox-service, which needs the optional `creator-store-product:read` scope. Unset (absent from state and `show --json`, and an error if referenced) for `Model`, `Audio` and `Video` assets, and for a Decal whose lookup failed (see the notes). |

The `id` output is the `assetId` (a numeric string). For an `Image` asset (the default for image files) that id is the texture id, and `texture_id` equals it. A `Decal` asset's id is not a texture id: each Decal wraps a separate Image asset that Roblox creates alongside it, owned by the same creator, and `texture_id` is that image's id. Use `texture_id` and you don't need to know which kind an asset is. Resolving a Decal's `texture_id` costs one extra request per Decal on every `plan` (Images cost nothing extra). If the lookup fails, plan and apply still succeed: `texture_id` stays unset (or keeps the value already recorded) and Baseplate prints a warning with the reason. The usual reasons: the API key lacks `creator-store-product:read` (one warning, and the remaining Decals skip the lookup); the decal was just uploaded (toolbox-service returns 404 for a new decal for about a minute, so a later `apply` records it even when nothing else changed); or Roblox doesn't show the key the decal's details (group-owned decals are unverified). To adopt assets from a Decal-based uploader, import those underlying image ids, not the decal ids (see the many-assets how-to). When the content of `file` changes (its `content_hash` trigger), an `Image` or `Model` asset is updated in place: apply uploads the file as a new version of the same asset, so the id doesn't change (Roblox documents content updates for Models only, but Images accept them too; a metadata-only edit doesn't re-send the content). A `Decal`, `Audio` or `Video` asset can't take new content (Roblox rejects a Decal content update as not supported yet), so plan shows `-/+ (replace)`: apply uploads a new asset, which gets a new id. The old asset is left in place, because live servers keep using its id until the place is republished, and recorded in state for `baseplate cleanup` to archive once no deployed build uses it. A change to `asset_type` always replaces. To give an Image or Model a new id on a content change instead of updating it in place, store a hash in a `baseplate_data` and list it in `lifecycle { replace_triggered_by = [...] }` (see the config reference). If `file` is missing on the machine running `plan` (e.g. CI without the source images), an asset already in state keeps its recorded hash and plan warns that content changes can't be detected; creating an asset still needs the file. Renaming or moving the file without changing its bytes does nothing. References to the asset's `id` and `baseplate show --json` pick up the new id in the same apply. An imported asset records the current file's hash as its baseline, so adopting existing assets never re-uploads them; the same goes for assets created by Baseplate versions without the hash, on their next apply. `lifecycle { prevent_destroy = true }` blocks a replace. Audio is limited to 7 minutes and 20 MB, and Roblox caps audio uploads per month (10, or 100 for ID-verified accounts). Image and Model assets are not archivable, so `destroy` unmanages the asset in place (drops it from state, leaves it uploaded); a Decal is archived, and can be restored.

Requires the `asset:read`, `asset:write` [scopes](how-to/api-key-scopes.md).

---

## `roblox_badge`

Manages a badge (an achievement players can earn in an experience).

| Attribute | Type | Kind | Description |
|---|---|---|---|
| `description` | string | optional, diffable | Badge description. |
| `enabled` | bool | optional, diffable | Whether the badge is active and awardable (default `true`). |
| `expected_cost` | number | optional, create-only | Robux cost you accept for creating the badge (default `0`). Creation is free in practice; a non-zero value opts into a paid badge. |
| `icon_file` | string | required, trigger | Path to an icon image (uploaded directly; required, because Roblox rejects a badge created without one). Change detected via content hash. |
| `name` | string | required, diffable | Badge name. |
| `payment_source` | string | optional, create-only | Funding source when `expected_cost` > 0: `user` (default) or `group`. |
| `universe_id` | string | required, create-only | The universe. Often a reference to an experience. |

Badges can't be hard-deleted, so `destroy` sets `enabled = false` and unmanages the badge. The icon is reconciled via a content-hash trigger and re-uploaded only when its content changes. Reads use the public badge endpoint on `badges.roblox.com` (writes use Open Cloud on `apis.roblox.com`).

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
| `public` | bool | optional, diffable | Whether the experience is public (`true`) or private (`false`). Uses the experimental legacy-develop activate/deactivate API and needs the extra `legacy-universe:manage` scope. Roblox can still refuse to make an experience public (403 "You are not authorized to configure this universe."); check it can be made public in Creator Hub. |
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

Manages a universe's Experience Config (a beta Roblox feature): the key/value settings, such as feature flags and tuning values, that a published game reads at runtime through `ConfigService`. Each apply stages the key changes as a draft and publishes it.

| Attribute | Type | Kind | Description |
|---|---|---|---|
| `deployment_strategy` | string | optional, create-only | `Immediate` (default) or `GradualRollout` (~15-20 min). Used for every publish, but not readable back, so it isn't diffed (listed as create-only): changing it alone doesn't publish. |
| `entries` | any | required, diffable | Map of config key to value. Values are written as native HCL: bool, number, string, or an object/list (a JSON config), e.g. `{ enable_tutorial = true, boss_health = 100, shop = { discount = 0.2 } }`. Keys in the repository but not listed here are removed on apply. Max 100 keys, 256-char keys; `.RBX.` is reserved in keys. |
| `repository` | string | optional, create-only | Config repository. Default `InExperienceConfig` (the only one Roblox documents). |
| `universe_id` | string | required, create-only | The universe. |

The resource owns the repository's base keys: an apply removes any published key that isn't in `entries`. To protect an existing game, the first apply (create) refuses if the repository already has keys that aren't declared. `import` it first so the plan shows what would be removed. Conditional rules and the conditional (targeted) values of keys you keep are preserved (Baseplate never sends them, and verifies the staged draft and the published result); a removed key's conditional values are removed with it, and a type change for a key with conditional values is rejected by Roblox. Apply refuses if the repository has an unpublished draft (for example, pending edits in Creator Hub). Publish or discard it first. `destroy` removes every plain key in the repository and publishes (the same ownership as apply; conditional rules are left alone, and an empty or never-published repository is a no-op). Protect production config with [`lifecycle { prevent_destroy = true }`](config-reference.md#lifecycle), which makes any plan that would destroy it fail. With `GradualRollout` (observed ~20 minutes), the API keeps returning the previous values until the rollout completes and rejects new drafts meanwhile, so a re-plan in that window shows the change again and an apply fails with a retry-later error. Numbers are stored as doubles (exact only up to 2^53).

Requires the `universe:read`, `universe:write` [scopes](how-to/api-key-scopes.md).

Example: [`examples/experience-config.bp.hcl`](../examples/experience-config.bp.hcl).

---

## `roblox_experience_event`

Manages an experience event (virtual event) for live-ops: a scheduled, titled event shown on the experience page. Uses the experimental `virtual-events/v3` Open Cloud API.

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

Game passes can't be hard-deleted, so `destroy` sets `is_for_sale = false` and unmanages it. Managed pricing isn't exposed, because it requires prior manual acceptance of Roblox's managed-pricing terms in the Creator Dashboard.

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

Place content isn't remotely readable, so change detection is by file content hash. For the same reason an import can't tell whether the live place matches `file`: it records the content as unverified, and the next plan publishes the file. If you know the live place already matches, `baseplate import --assume-current` records the file's hash instead. `destroy` is a no-op (a place can't be unpublished).

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

## `roblox_place_media`

Manages a place's icon and detail-page thumbnails. On the root place these are the experience's icon and thumbnails. Adopts the place; it does not create places.

| Attribute | Type | Kind | Description |
|---|---|---|---|
| `icon` | string | optional, diffable | Image asset id of the icon (square). `""` removes the icon. |
| `place_id` | string | required, create-only | The place. Use the root place for the experience's icon and thumbnails. |
| `thumbnails` | list of object | optional, diffable | Ordered list of `{ asset_id, alt_text }` (alt text optional), the order shown on the game page. Image asset ids; at most 10, no duplicates. `[]` removes them all. |

Only declared attributes are managed; omit `icon` or `thumbnails` to leave it as it is. Each apply replaces the whole thumbnail list. `icon` can be a reference (`roblox_asset.icon.id`); `thumbnails` takes literal ids for now, because references inside a list aren't supported yet. An image still in moderation review is accepted. Uses the Assets API's place-asset `icon` and `previews` fields (beta). Roblox's reads lag behind its writes, and an update can report done without being applied, so apply waits until the place reads back as written, re-sends the update (with a warning) if it doesn't, and fails after three tries. For a minute or two after an apply, a plan can still read the old values and show them as a change; applying again is harmless. Video thumbnails aren't supported: Roblox's docs only describe image entries, uploading a Video asset through Open Cloud costs Robux, and a video added in Creator Dashboard hasn't been tested with this API. Homepage thumbnails (thumbnail personalization) and translated icons and thumbnails are separate APIs that this resource doesn't manage. `destroy` unmanages the place (the icon and thumbnails are left as they are). Import by place id.

Requires the `asset:read`, `asset:write` [scopes](how-to/api-key-scopes.md).

Example: [`examples/place-media.bp.hcl`](../examples/place-media.bp.hcl).

---

## `roblox_universe_secret`

Manages an Open Cloud secret for a universe. Secrets can be created, updated and deleted. The value is encrypted client-side (LibSodium sealed box) before upload and is never written to state or printed in a plan.

| Attribute | Type | Kind | Description |
|---|---|---|---|
| `domain` | string | optional, diffable | HttpService domain wildcard (e.g. `api.example.com`, `*`). Omit for a private key. |
| `name` | string | required, create-only | Secret name (the API id): 1-64 chars, alphanumeric/underscore, not starting with a digit. |
| `universe_id` | string | required, create-only | The universe. |
| `value` | string | required, trigger, sensitive | The plaintext. Usually `env("…")`. Sealed client-side; change detected via a hash. |

Renaming a secret (`name`) is a recreate, not an in-place rename.

Requires the `universe.secret:read`, `universe.secret:write` [scopes](how-to/api-key-scopes.md).
