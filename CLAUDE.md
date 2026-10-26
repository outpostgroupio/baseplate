# Baseplate — project guide for Claude

**Baseplate is a Terraform-style Infrastructure-as-Code tool for Roblox, in Go.** It manages
Roblox resources declaratively via Open Cloud: `baseplate plan/apply/destroy` over an HCL
config, backed by a state file. A maintained, Go-native successor to Mantle. Module:
`github.com/outpostgroupio/baseplate`.

## Status & roadmap
Current status (phase/slice), completed work, the remaining roadmap, and tracked deferrals live
in **[ROADMAP.md](ROADMAP.md)** — kept there so this file stays focused on how to work in the repo
and doesn't need frequent status edits. Per-slice design docs are in `docs/superpowers/specs/`;
the executed task-by-task plans in `docs/superpowers/plans/`.

## Environment — READ THIS FIRST
This repo lives in **WSL** (`~/projects/baseplate`, i.e. `/home/josh/projects/baseplate`) on a
Windows host. Claude's Bash tool runs **Windows Git Bash, not Linux**.
- **Run all shell/go/git commands through WSL:** `wsl -d ubuntu-22.04 -- bash -lc 'cd ~/projects/baseplate && <cmd>'`
- **Keep those commands SIMPLE** — one per call. Multi-line shell with `${}` expansion, `for`
  loops, `$(...)`, or nested quotes frequently **mangles** through the Windows→WSL arg bridge.
  For anything non-trivial, write a small script file (e.g. under `.superpowers/sdd/`) and run it.
- **Edit files** with the Write/Edit tools using the UNC prefix
  `\\wsl.localhost\ubuntu-22.04\home\josh\projects\baseplate\<path>`.
- **Go** is `go1.25.4` at `~/sdk/go1.25.4/bin/go`, on the WSL login-shell PATH (`bash -lc` finds
  it). Module floor is **Go 1.24**.
- Network: general egress + the Go module proxy work, but **`raw.githubusercontent.com` is
  unreachable** here — fetch files from GitHub via the API contents endpoint with
  `-H "Accept: application/vnd.github.raw"`.

## Build / test / run
```bash
go test ./... -race        # full hermetic suite (no network, no Roblox)
go vet ./...
CGO_ENABLED=0 go build ./... # distribution must be pure Go (a hard constraint)
```
CI (`.github/workflows/ci.yml`) builds with `CGO_ENABLED=0` but runs `-race` under the default
toolchain (the race detector needs cgo).

**Run the CLI:** `go run ./cmd/baseplate {validate|plan|apply|destroy} --config X.bp.hcl --state X.bpstate [--target A] [--json]`.
Exit codes: `0` no changes, `2` changes present, `1` error. With `ROBLOX_API_KEY` set the CLI
uses the real provider; otherwise the in-memory mock.

**Live tests** (real Roblox, opt-in): put `ROBLOX_API_KEY` + `BASEPLATE_TEST_UNIVERSE` in a
gitignored `.env`, then `set -a; . ./.env; set +a; BASEPLATE_LIVE=1 go test
./internal/provider/roblox/ -run Live -v`. See `docs/roblox-live-testing.md`. Test universe is
`10438196190` (user-owned, `users/5822334358`).

## Architecture map
```
cmd/baseplate                 CLI entrypoint
internal/config               HCL loader: env() secrets, reference/dependency extraction,
                              drops unset-optional (hcldec-null) attrs
internal/engine/graph         deterministic topological sort (+ cycle / unknown-dep detection)
internal/engine/plan          plan/diff (diffs ONLY declared attrs, minus computed) + rendering
internal/engine/apply         apply with state write-back; apply-time reference resolution (outputs map)
internal/state[/backend]      state model (serial/lineage) + local backend (atomic write, flock)
internal/provider             Provider/Resource interfaces (the seam); Attributes/Ref/ChangeAction
internal/provider/mock        in-memory mock provider (drives engine tests)
internal/provider/roblox      roblox.New(apiKey) assembles resources; CLI swaps it in when key set
  .../opencloud               HTTP client core (auth/backoff/operation-poll/multipart/error-xlate)
                              + UniverseClient/AssetClient/DeveloperProductClient + generated types
  .../opencloud/gen           GENERATED wire types (regen.sh) — do not hand-edit
  .../opencloud/specs         vendored per-service Open Cloud specs (unified 3MB one is gitignored)
  .../resources               experience.go / asset.go / developer_product.go (provider.Resource impls)
```
**Key invariants:** resources depend only on client *interfaces* (not `gen/*`) — codegen is a
swappable detail. `.id` is reserved for Baseplate's state id; no resource declares a config
`id`. Create-only inputs (e.g. asset `file`/creator, product `universe_id`/`icon_file`) are in a
resource's `Computed()` so they don't diff. `Computed()` = "excluded from diff."
Output-only attributes (e.g. asset `texture_id`) implement `provider.Outputter` (and sit in
`Computed()`); apply records them on a no-op and keeps a recorded value when Read omits one.
Non-fatal problems go through `provider.Warn(ctx, msg)` (the CLI prints them as warnings), never
a failed plan.

## Roblox spec gotchas (learned; verify against vendored specs before assuming)
- Universe `displayName`/`description`/`visibility`/`ageRating` are **read-only** on the v2 API
  (name/description follow the root place → `roblox_place_config`). Writable: `voiceChatEnabled`,
  `desktop/mobile/tablet/console/vrEnabled`, `privateServerPriceRobux`, the `*SocialLink` objects.
  **Unset fields are omitted from GET** (no price = private servers off; no link = unset).
  - **Clearing = masked + omitted from the body** (verified live). An explicit JSON `null` for
    `privateServerPriceRobux` 400s ("payload is not correctly formatted"); `null` for a link is
    accepted but omission is the safe form. Price `0` is a real (free) price: valid values are
    `0` or 10–500, so 0 ≠ disabled.
  - Social links accept **sub-path masks** (`discordSocialLink.uri`/`.title`) that patch half of
    an existing link; creating a link via a sub-path 400s ("update mask must contain all social
    link fields when the link is being created"). Title 1–70 chars; uri validated per platform.
  - Visibility is written via `POST /legacy-develop/v1/universes/{id}/activate|deactivate`
    (EXPERIMENTAL, scope `legacy-universe:manage`); legacy errors use the
    `{"errors":[{code,message}]}` envelope. Without the scope: `403 {"code":0,"message":"Scope
    not authorized."}`. With it (verified live): `deactivate` → `200 {}` (application/json),
    idempotent on an already-PRIVATE universe. `activate` → `403 {"code":3,"message":"You are not
    authorized to configure this universe."}` until the experience's **maturity questionnaire** is
    completed in Creator Hub (the same opaque 403 as for a universe the key doesn't own); after
    that, the public flip and restore pass live (2026-10-23). `roblox_experience` polls v2
    `visibility` (≤10×1s) after a toggle as a guard and only blames the scope when the 403 message
    mentions "scope".
- Place config: `GET/PATCH /cloud/v2/universes/{u}/places/{p}?updateMask=` (scope
  `universe.place:write`, distinct from publish's `universe-places:write`); PATCH 409s while the
  place is open in Team Create.
- Assets are **user- or group-owned** (`creationContext.creator.{userId|groupId}`), not
  universe-owned. Create = multipart (`request` JSON part + `fileContent` file) → async
  Operation → poll `/v1/operations/{id}`. Base `https://apis.roblox.com/assets`.
  - `assetId` comes back as a JSON **string**, not the int64 the spec declares (Roblox serializes
    64-bit ids as strings — decode as `string`). Verified live.
  - **Image and Model assets are NOT archivable** (`:archive` → 400 "not an archivable
    asset type"; Model verified live 2026-10-26), so there's no Open Cloud delete path;
    `roblox_asset` destroy unmanages-in-place (drops from state, leaves the asset), like a dev
    product that can't be hard-deleted. Verified live. **A Decal IS archivable** (corrects an
    older note): `roblox_asset` destroy of a Decal left it `"state":"Archived"` (2026-10-26), and
    `POST /assets/v1/assets/{id}:restore` brought it back (`Active`, same id, texture intact).
  - **Content updates** (`PATCH …/assets/{id}` multipart with a `fileContent` part plus the
    `request` part; `updateMask=displayName,description` still applies the metadata in the same
    call) → Operation, and a **new version under the same id**. Docs say Model-only (the guide even
    says `.fbx`-only), but live (2026-10-26) a **Model from `.rbxmx`** (`model/x-rbxm`) took it (v2,
    metadata updated too), and so did an **Image** (v2, moderation `Reviewing` then `Approved`; the
    public thumbnail then rendered the v2 pixels; live test keeps the id and converges). A
    **Decal** is rejected: `400 "Updating Decal is not supported yet"` (2026-10-26, no new
    version). Audio/Video are documented as not updatable (not probed: audio uploads are capped).
    So `roblox_asset`'s `contentInPlace` set is {Image, Model} (in place, same id); other types
    replace (new id, old asset abandoned, never archived). A 400/422 on an in-place update errors
    out saying Baseplate needs updating; there is no per-resource knob (a general
    `lifecycle { replace_triggered_by }` is the planned way to force a replace).
    **Race:** a content PATCH sent within ~3s of a *separate* metadata PATCH, carrying the same
    (new) name/description, can end up with the *earlier* name/description; it can show up
    asynchronously after a read-back already returned the new values. With a gap, or when the
    content PATCH itself changes the metadata, it sticks. `roblox_asset` sends both in one PATCH and
    re-sends the metadata if the read-back differs; across back-to-back applies the next plan shows
    a metadata update that fixes it.
    A metadata-only PATCH creates no new version. `GET …/versions` lists versions.
  - **Decal vs Image (verified live 2026-10-26):** uploading `assetType: Decal` also creates a
    separate **Image** asset (its own id, owned by the same creator, and it copies the decal's
    name and description). That image id is the texture id (`Decal.Texture` / `ImageLabel.Image`).
    An `assetType: Image` upload creates only the image. Economy `AssetTypeId` is 1 for Image and
    13 for Decal. The auto-created image is a normal asset: `GET /assets/v1/assets/{id}` returns
    `assetType: "Image"`, and `baseplate import` adopts it, renames it via PATCH and converges
    (the decal is untouched). Decal → texture id over an API key:
    `GET /toolbox-service/v2/assets/{decalId}` (BETA, scope `creator-store-product:read`) returns
    `asset.textureId` (a JSON number), and this works for the key owner's own **private**,
    non-Creator-Store decal. It 404s without a key, and 404s for an Image id. It also **404s a
    freshly uploaded decal** for a while (seconds after upload: 404; about a minute later: 200,
    verified live 2026-10-26), and for an archived one. The 200 body is
    `{"asset":{"id":N,"textureId":N,"assetTypeId":13,...},"creator":{...},"voting":{...},...}`
    (ids as JSON numbers). `roblox_asset.texture_id` uses it for Decals (an Image's is its own
    id); a failed lookup only warns. Not tried yet: a group-owned decal, another creator's
    private decal, or a key without the scope (the missing-scope response is assumed to be the
    gateway's `403 Scope not authorized.`). Plan only diffs *declared* attributes,
    so importing a decal id with `asset_type` omitted shows no diff. With
    `asset_type = "Image"` declared it plans `Decal => Image`, which `Update` can't apply.
    Neither `asset-delivery-api/v1/assetId/{id}` (scope `legacy-asset:manage`) nor Luau Execution
    (`universe.place.luau-execution-session:write`, STABLE, 5 tasks/min) is granted to the test
    key, so headless render checks (`InsertService:LoadAsset`, `PreloadAsync`) haven't run.
- Developer products: create/update are **multipart form fields** (`name`, `price`, `isForSale`,
  `imageFile`, ...). The icon is a **direct file upload** (`imageFile`), NOT an assetId
  reference. Can't hard-delete → destroy sets `isForSale=false`. Base `https://apis.roblox.com`.
- Universe config: `GET/PATCH /cloud/v2/universes/{id}?updateMask=...`, base `https://apis.roblox.com`.
- Places: publish is `POST /universes/v1/{universeId}/places/{placeId}/versions?versionType=Published`
  (base `https://apis.roblox.com`), **raw body** (.rbxl→`application/octet-stream`, .rbxlx→
  `application/xml`), response `{"versionNumber":N}` as `text/plain`. Publishes to an EXISTING place
  (no create; no content read-back). Requires the **`universe-places:write`** API-key scope
  (distinct from `universe:write`/`universe.place:write`) — else `401 insufficient scopes`. A
  minimal `<roblox version="4"></roblox>` .rbxlx is accepted (verified live). Can't unpublish →
  `roblox_place` destroy is a no-op. Change detection uses the engine's Triggers capability
  (content_hash of the file), since content isn't remotely readable.
- Universe secrets (`/cloud/v2/universes/{id}/secrets`, base `https://apis.roblox.com`): values
  must be **LibSodium sealed-box + base64** encrypted with the universe public key from
  `GET .../secrets/public-key` (returns the key in the `secret` field + `key_id`). No single-GET
  (list-and-find via `GET .../secrets`, cursor-paginated); PATCH has **no updateMask**. Requires
  the **`universe.secret:read`/`universe.secret:write`** scopes (distinct from `universe:write`).
  Real **DELETE** exists (hard delete). Wire fields are snake_case (`id`, `secret`, `key_id`,
  `domain`, `create_time`, `update_time`); `domain` empty/null = private key, `*` = all domains.
  **Pagination gotcha (verified live):** the list endpoint **always** returns a non-empty
  `nextPageCursor` — even on the empty final page — so paginate until an **empty `secrets` page**,
  not an empty cursor (a cursor-only loop never terminates and burns the 120-req/min limit → 429).
- Experience events (`virtual-events/v3`, base `https://apis.roblox.com`, **EXPERIMENTAL**; all
  verified live 2026-10-23): create `POST /virtual-events/v3/universes/{universeId}/game-events`;
  single read/PATCH/DELETE on `/virtual-events/v3/game-events/{eventId}` (not universe-scoped, so
  `roblox_experience_event`'s state/import id is the bare event id). JSON bodies; PATCH is partial
  (omitted = no change) and returns the event; DELETE is a real delete (204) but **eventually
  consistent** (a GET right after can still return the event briefly, then 404 `GameEventNotFound`).
  - Wire: event `id` is a JSON **string**; `universeId`/`placeId`/`hostId`/`mediaId` are JSON
    **numbers**. Times read back as `+00:00` with **millisecond** precision (`.123456Z` → `.123`);
    Baseplate normalises both sides to UTC whole seconds via `provider.Normalizer`. An unset venue
    reads back as `placeId: 0` (not the root place, despite the spec). Errors are RFC 7807
    ProblemDetails with a string `code` (`InvalidStartOrEndTime`, `InvalidThumbnails`, ...).
  - **The single-event GET always returns `"thumbnails": []`** (even with `?fields=thumbnails`); only
    the universe LIST returns them, so `GameEventClient.Get` does a GET + a list lookup by id.
  - Thumbnail `rank` is **0-based and contiguous** (rank 1 alone or 5 → `InvalidThumbnails`); `[]`
    clears them. Any image asset id works (an Image uploaded by the key's user was accepted).
  - **Categories are unusable:** every payload (each enum value, rank 0 or 1, even `[]` on PATCH)
    returns 400 `InvalidCategories`, and a **create carrying categories still creates the event**
    before returning 400 (orphan). Not exposed in v1. Recurrence is also not exposed (no effect).
  - Validation: start must be in the future on create, and any *changed* start must be in the
    future (`InvalidStartOrEndTime`); end must be **>= start + 15 min**; `visibility` is lowercase
    `public`/`private` (`moderated` → `InvalidEventVisibilityTransition`); a bad `placeId` →
    "The value of PlaceId is invalid". `description: ""` clears it, but **`tagline: ""` is ignored**
    (a set tagline can't be cleared).
  - Started (active) events can be edited, including re-sending the unchanged past start. **Ended**
    events stay readable, editable (title, even extending the end into the future) and deletable.
    The default LIST hides ended events (`endsAfter` defaults to now).
  - Scopes `universe.event:read`/`universe.event:write`; without them the gateway returns
    `403 {"errors":[{"code":0,"message":"Scope not authorized."}]}` on every call, reads included.
  - **Reads flap after a write (verified live):** for a few seconds after a PATCH, GETs alternate
    between the old and new values, so `Create`/`Update` wait for 3 consecutive matching reads
    (bounded, ~10s) before returning; otherwise a plan straight after apply shows phantom drift.
- Analytics alerts (`/analytics-alert-control-plane/v1/resource/Universe/id/{universeId}/alerts`,
  base `https://apis.roblox.com`, **EXPERIMENTAL**). JSON CRUD: POST create (201 + full alert),
  `GET` list (a **bare JSON array**, no pagination per spec; max 20 alerts per universe; `?ids=`
  repeated-param filter), `PATCH .../{alertId}` (partial: omitted fields are kept; within
  `condition` sub-fields can be omitted; `filter`/`breakdown` replace wholesale; to remove webhooks
  send `webhookReceiverConfig: {receivers: []}`, because `null` means "unchanged"), and a real
  `DELETE` (204). No single-GET. `alertId` is documented as a string-encoded int64. The create body
  has **no `configState`**, so disable is a follow-up PATCH. `breakdown` allows at most one entry
  with one dimension, and that dimension must also be in `filter`. Service errors are
  `{"errorCode","message"}`. Scopes are **`universe.analytics.alert:read`/`:write`**; without them
  the gateway returns `403 {"errors":[{"code":0,"message":"Scope not authorized."}]}`. With the
  scopes granted, a universe that isn't eligible gets this on **every** endpoint (list, create,
  patch and delete alike): `403 {"errorCode":"PERMISSION_DENIED","message":"Universe N is not
  enrolled in PerformanceMonitoringV2."}`. "PerformanceMonitoringV2" is undocumented. The documented
  rule (creator-docs `production/analytics/alerts.md`) is that alerts are available to experiences
  meeting the Performance dashboard eligibility requirement of **100+ DAU**, and no opt-in is
  documented. The client translates this error to `opencloud.AlertsNotEligibleError`. Both 403s
  are verified live (2026-10-23). The test universe is below the bar, so the request and response
  shapes above come from the spec and docs, not from live calls.
- Experience Config (BETA; `/creator-configs-public-api/v1/configs/universes/{id}/repositories/{repo}`,
  base `https://apis.roblox.com`, plain **`universe:read`/`universe:write`** suffice). All verified live:
  - A **never-published** repo 404s on GET (and on `…/revisions`, text/plain `ResourceNotFound`) —
    treat as empty. **But** the values GET has also 404'd transiently right after a publish, so the
    client only treats a 404 as empty when `…/revisions` confirms nothing was ever published; a universe you can't see is **403**
    `"User does not have View permission…"`. No draft → `GET …/draft` 404; `DELETE …/draft` 404s too.
  - GET values: `entries` is a **flat** map; numbers come back as floats (`100` → `100.0`); bool,
    string, object and array values all round-trip. **Conditional branches are flat entry keys**
    `<key>.RBX.conditional.<ruleId>` (undocumented); `conditionalRules` carries explicit nulls and can
    be sent back verbatim.
  - `draft:overwrite` **without `conditionalRules` deletes every rule and branch**. `PATCH …/draft`
    is the safe write: a changed base key keeps its branches, `null` deletes a key **and its
    branches**, and omitted `conditionalRules` leaves rules untouched — so `roblox_experience_config`
    writes via PATCH (desired keys + nulls for undeclared ones) and never echoes rules back (an
    overwrite built from a briefly-stale read would wipe them). **Once** (in ~15 back-to-back live
    runs) a rules-free PATCH right after a publish still published a version that dropped every rule
    + branch; the resource therefore reads the staged draft back and discards/re-stages it if it
    deletes anything unrequested, and re-checks after publish (loud error naming the version to
    restore). Publishing a no-op draft → 400
    `validationErrors[].code = "EmptyDraft"` (and the draft stays — discard it). `publish` works without
    `draftHash`; a stale hash → 400 bare string `"The provided draft hash does not match…"`.
  - **`GradualRollout` took ~20 min**: meanwhile GET serves the OLD version, the new revision isn't
    listed, and any `draft:overwrite` → 400 text/plain `MultipleDraftNotSupported`.
  - A draft write within a few seconds of the previous write/publish can 400 text/plain
    **`ETagMismatch`** or **`MultipleDraftNotSupported`** (even after an *Immediate* publish) —
    transient; the client retries both for ~15s. A `publish` with the hash the PATCH just returned
    can still 400 "The provided draft hash does not match the current draft." — the resource
    re-reads + re-verifies the draft and publishes with its current hash. Reads **flap between
    versions** for a few seconds after a publish, so a PATCH computed from one read can miss keys
    to delete: the resource takes the newest of 3 reads, then re-checks the published version and
    deletes any leftovers in a follow-up round. A GET right after an Immediate publish can still
    serve the previous version briefly (wait for `metadata.configVersion`). Publishes are tightly
    rate-limited (429 after a few dozen calls in a minute).
  - Error bodies are a mix of bare JSON strings, `{"validationErrors":[…]}`, RFC 9110 problem JSON and
    text/plain — none match `{code,message}`; `parseAPIError` now falls back to the body text.

## Workflow used here
Superpowers: brainstorm → spec → bite-sized TDD plan → build (subagent-per-task, or inline for
subtle engine bits) → per-task spec+quality review → whole-branch review before each merge. The
transient SDD ledger lives at `.superpowers/sdd/progress.md` (gitignored scratch — the durable
knowledge is consolidated into this file, `ROADMAP.md`, and the committed specs/plans). End commit messages with
`Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>` only when the user asks to commit.
