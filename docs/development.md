# Development notes

Notes for people working on Baseplate itself: how the code is laid out, how to run the live tests,
how releases work, and what we've learned about Open Cloud that Roblox's docs don't say.

## Code layout

```
cmd/baseplate                 CLI entry point
internal/cli                  commands and flags
internal/config               HCL loading, env(), references, for_each/moved/import blocks
internal/engine/graph         topological sort, cycle and unknown-dependency checks
internal/engine/plan          diffing and plan rendering
internal/engine/apply         apply, state write-back, apply-time reference resolution
internal/state[/backend]      state model (serial, lineage, version) and the local/http backends
internal/provider             Provider and Resource interfaces, plus optional capabilities
internal/provider/mock        in-memory provider used by the engine tests
internal/provider/resourcedocs  generates docs/resource-reference.md
internal/provider/roblox      the Roblox provider
  opencloud                   HTTP client: auth, backoff, operation polling, multipart, errors
  opencloud/gen               generated wire types (regen.sh); don't edit by hand
  opencloud/specs             vendored Open Cloud specs (see NOTICE)
  resources                   one file per roblox_* resource
```

A few rules the code relies on:

- Resources depend on client interfaces, not on the generated types in `opencloud/gen`.
- `id` is Baseplate's state id. No resource declares a config attribute called `id`.
- `Computed()` means "left out of the diff". Create-only inputs (an asset's `file` and creator, a
  product's `universe_id`) go there so changing them doesn't plan an update.
- Output-only attributes (such as `roblox_asset.texture_id`) implement `provider.Outputter`.
  Apply records them even on a no-op, and keeps the recorded value when a read doesn't return one.
- Problems that shouldn't fail a plan go through `provider.Warn(ctx, msg)`, which the CLI prints
  as a warning.
- Resource documentation lives in each resource's `Doc()`. Run `go generate ./...` after changing
  it; `TestResourceReferenceUpToDate` fails if `docs/resource-reference.md` is stale.

## Live tests

The normal test suite uses a fake Open Cloud server. A separate set of tests runs against real
Roblox. They only run when `BASEPLATE_LIVE=1`, and most contributors never need them.

1. Create a throwaway experience. Don't use a real game: the tests change its settings and create
   products, passes, badges, secrets and events in it.
2. Create an API key in [Creator Dashboard → Credentials](https://create.roblox.com/dashboard/credentials),
   add the test universe to it, and grant the scopes listed in `.env.example` (see also
   [API key scopes](how-to/api-key-scopes.md)).
3. Copy `.env.example` to `.env` (gitignored) and fill it in. `ROBLOX_API_KEY`,
   `BASEPLATE_TEST_UNIVERSE` and `BASEPLATE_TEST_USER` (the user id that owns the key) are
   required. `BASEPLATE_TEST_PLACE` is needed by the place tests.
4. Run them:

```bash
set -a; . ./.env; set +a
BASEPLATE_LIVE=1 go test ./internal/provider/roblox/ -run Live -v
```

Narrow the run with `-run`, for example `-run LiveSecret`, `-run LiveExperienceEvent`,
`-run LiveAssetTextureID` or `-run LiveAnalyticsAlert`. Some tests need extra scopes or settings:

- The experience event test needs `universe.event:read`/`write` and skips without them.
  `BASEPLATE_TEST_EVENT_THUMBNAIL` (an image asset id) also exercises thumbnails.
- The texture id test needs `creator-store-product:read`. A new decal takes about a minute to
  resolve, so the test retries for up to three minutes. Set `BASEPLATE_TEST_IMAGE_ID` and
  `BASEPLATE_TEST_DECAL_ID` to existing assets to import those instead of uploading two more.
- The analytics alert test needs `universe.analytics.alert:read`/`write` and an experience with
  100+ daily active users. It skips if the preflight list returns 401 or 403.

Tests clean up after themselves where Roblox allows it. Products and passes are taken off sale,
badges are disabled, and assets that can't be archived are left behind.

## Releases

Releases are built by [GoReleaser](https://goreleaser.com) when a `v*` tag is pushed
(`.github/workflows/release.yml`). Besides the default `GITHUB_TOKEN`, it uses two secrets:
`HOMEBREW_TAP_DEPLOY_KEY` and `SCOOP_BUCKET_DEPLOY_KEY`, SSH deploy keys with write access to
[outpostgroupio/homebrew-tap](https://github.com/outpostgroupio/homebrew-tap) and
[outpostgroupio/scoop-bucket](https://github.com/outpostgroupio/scoop-bucket). GoReleaser pushes the
cask and the Scoop manifest there with them.

```console
$ git tag v0.1.0-rc.3
$ git push origin v0.1.0-rc.3
```

A tag with a pre-release suffix (`-rc.N`, `-beta.N`) is published as a GitHub pre-release; a bare
tag is a full release. Until 1.0, tag release candidates. Each release has archives for
linux/darwin/windows on amd64 and arm64, `checksums.txt`, and a changelog built from the squashed
PR titles. `checksums.txt` is signed keylessly with cosign (`checksums.txt.sigstore.json`), and the
workflow adds GitHub build provenance for the archives. Provenance is skipped while the repository
is private, because GitHub only offers it for private repositories on Enterprise Cloud.

The tap and the bucket take release candidates too, since there's no full release yet. At v1.0.0,
set `skip_upload: auto` on `homebrew_casks` and `scoops` in `.goreleaser.yaml` so they only follow
full releases. Rokit needs nothing from us: it finds the right archive by its name.

The build stamps `internal/version` through `-ldflags -X`, which `baseplate version` prints. An
unstamped `go build` reports `baseplate dev`.

To check the GoReleaser config without publishing, in a normal clone:

```console
$ goreleaser check
$ goreleaser release --snapshot --clean --skip=sign
```

`--skip=sign` skips the cosign step, which only works in the release workflow.

## Open Cloud notes

Things we found by calling the APIs, which are either missing from Roblox's docs or contradict
them. Check the vendored specs and re-test before relying on any of this; Roblox changes these
APIs, and several are marked experimental or beta.

### General

- The client doesn't follow a redirect to a different host, so the API key is only ever sent to
  Roblox's own hosts.
- Roblox often serialises 64-bit ids as JSON strings even where the spec says integer (asset
  `assetId`, event `id`). Others are numbers (`gamePassId`, event `universeId`/`placeId`). Decode
  each one as what the API actually returns.
- Error bodies vary by service: `{code, message}`, `{"errors":[...]}` on legacy endpoints,
  RFC 7807 problem JSON, bare JSON strings and plain text all occur. `parseAPIError` falls back to
  the body text.
- A missing scope usually comes back from the gateway as `403 {"code":0,"message":"Scope not
  authorized."}` (wrapped in `errors` on some services), including on reads. A universe that is
  selected on the key but not saved gives `403 Authorization scope does not allow this operation`.

### Universe (`roblox_experience`)

- `GET/PATCH /cloud/v2/universes/{id}?updateMask=...`. `displayName`, `description`,
  `visibility` and `ageRating` are read-only. Name and description follow the root place, which
  `roblox_place_config` manages.
- Unset fields are omitted from GET. To clear `privateServerPriceRobux` or a social link, put it in
  the update mask and leave it out of the body. An explicit `null` price returns 400. Price `0` is a
  real (free) price; valid prices are 0 or 10 to 500.
- Social links accept sub-path masks (`discordSocialLink.uri`) for an existing link, but creating
  a link needs both fields in the mask.
- Visibility uses `POST /legacy-develop/v1/universes/{id}/activate|deactivate` (experimental,
  scope `legacy-universe:manage`). `deactivate` is idempotent. `activate` returns
  `403 {"code":3,"message":"You are not authorized to configure this universe."}` until the
  experience's maturity questionnaire has been completed in Creator Hub, the same error as for a
  universe the key doesn't own. After a toggle we poll v2 `visibility` (up to 10 times, 1s apart).

### Places

- Publishing is `POST /universes/v1/{universeId}/places/{placeId}/versions?versionType=Published`
  with the raw file as the body (`application/octet-stream` for `.rbxl`, `application/xml` for
  `.rbxlx`). It needs `universe-places:write`, which is a different scope from `universe:write` and
  `universe.place:write`. A minimal `<roblox version="4"></roblox>` file is accepted. There's no
  create, no content read-back and no unpublish, so change detection hashes the file.
- Place config is `GET/PATCH /cloud/v2/universes/{u}/places/{p}` (scope `universe.place:write`).
  PATCH returns 409 while the place is open in Team Create.

### Assets

- Assets are owned by a user or group (`creationContext.creator`), not a universe. Create is a
  multipart request (`request` JSON plus `fileContent`) that returns an Operation to poll.
- Image and Model assets can't be archived (`400 not an archivable asset type`), so destroy only
  drops them from state. Decals can be archived and restored.
- Content updates (`PATCH /assets/v1/assets/{id}` with a `fileContent` part) are documented for
  Models only, but Images accept them too and keep their id. Decals return
  `400 Updating Decal is not supported yet`. Audio and Video are documented as not updatable. So
  Image and Model update in place and other types are replaced.
- A content PATCH sent within about 3 seconds of a separate metadata PATCH can end up with the
  older name and description, sometimes only showing up after a read-back has returned the new
  ones. `roblox_asset` sends content and metadata in one PATCH and re-sends the metadata if the
  read-back differs.
- Uploading a Decal also creates a separate Image asset with its own id, owned by the same creator
  and copying the decal's name. That image id is the texture id. The Assets API doesn't expose it;
  `GET /toolbox-service/v2/assets/{decalId}` (scope `creator-store-product:read`) returns it as
  `asset.textureId`, even for a private decal. That endpoint 404s for about a minute after a
  decal is uploaded, and for archived decals.

### Developer products, game passes, badges

- Developer products and game passes are created and updated with multipart form fields. Icons
  are file uploads (`imageFile`), not asset ids. Neither can be deleted, so destroy takes them off
  sale.
- Game pass create is synchronous (no Operation), PATCH returns an empty body, and
  `isManagedPricingEnabled` returns 400 until managed pricing has been accepted in the Creator
  Dashboard.
- Badges use legacy endpoints (`legacy-badges/v1`, `legacy-publish/v1` for the icon) and are read
  from the public `badges.roblox.com` host. The two `legacy-universe.badge:*` scopes were enough
  for the whole lifecycle in testing, although the spec also lists `legacy-badge:manage` for the
  icon. Badges can't be deleted, so destroy disables them.

### Universe secrets

- Values are encrypted with a libsodium sealed box using the universe's public key
  (`GET .../secrets/public-key`, key in the `secret` field), then base64-encoded.
- There's no single-secret GET, so reads list and search. The list endpoint always returns a
  non-empty `nextPageCursor`, even on the last page, so pagination stops at an empty page. A loop
  that waits for an empty cursor never ends and hits the 120 requests/minute limit.
- PATCH has no update mask. DELETE is a real delete. Fields are snake_case. An empty `domain` means
  a private key; `*` means any domain.

### Experience events (`virtual-events/v3`, experimental)

- Create is universe-scoped; read, PATCH and DELETE use `/virtual-events/v3/game-events/{id}`, so
  the state and import id is the bare event id.
- The single-event GET always returns `"thumbnails": []`. Only the universe list returns them, so
  reads do a GET plus a list lookup.
- Times come back as `+00:00` with millisecond precision; both sides are normalised to UTC whole
  seconds. An unset venue reads back as `placeId: 0`.
- Thumbnail ranks must be 0-based and contiguous. Categories are rejected with
  `InvalidCategories` whatever you send, and a create that includes them still creates the event,
  so categories aren't exposed.
- Start must be in the future on create and whenever it changes; end must be at least 15 minutes
  after start. `tagline: ""` is ignored, so a tagline can't be cleared.
- Reads alternate between old and new values for a few seconds after a write, so create and update
  wait for three matching reads (about 10 seconds at most). DELETE is eventually consistent.

### Analytics alerts (experimental)

- JSON CRUD under `/analytics-alert-control-plane/v1/resource/Universe/id/{universeId}/alerts`.
  List returns a bare array and there's no single GET. At most 20 alerts per universe. PATCH is
  partial; `filter` and `breakdown` are replaced whole, and removing webhooks needs
  `webhookReceiverConfig: {receivers: []}` because `null` means unchanged. Create has no
  `configState`, so disabling is a second PATCH.
- An experience below the Performance dashboard's 100 DAU bar gets
  `403 {"errorCode":"PERMISSION_DENIED","message":"Universe N is not enrolled in PerformanceMonitoringV2."}`
  on every call. The client turns this into `opencloud.AlertsNotEligibleError`. Our test universe
  is below the bar, so the request and response shapes come from the spec, not live calls.

### Experience Config (beta)

- `/creator-configs-public-api/v1/configs/universes/{id}/repositories/{repo}`, with plain
  `universe:read`/`universe:write`.
- A repository that has never been published returns 404. The values GET has also returned 404
  briefly right after a publish, so a 404 only counts as empty when `.../revisions` confirms
  nothing was published.
- `entries` is a flat map, and numbers come back as floats. Conditional branches are extra keys
  named `<key>.RBX.conditional.<ruleId>`.
- `draft:overwrite` without `conditionalRules` deletes every rule and branch. `PATCH .../draft` is
  safe: `null` deletes a key and its branches, and omitted rules are left alone. Baseplate writes
  with PATCH, reads the draft back and discards it if it would delete anything unexpected, and
  checks again after publishing. (Once, in about 15 runs, a rules-free PATCH right after a publish
  still dropped every rule.)
- Publishing an empty draft returns 400 `EmptyDraft`. Draft writes shortly after another write or
  publish can return `ETagMismatch` or `MultipleDraftNotSupported`; the client retries for about
  15 seconds. A publish with the hash the PATCH just returned can still fail with a stale-hash
  error, so the resource re-reads the draft and uses its current hash.
- Reads alternate between versions for a few seconds after a publish. A `GradualRollout` publish
  took about 20 minutes, during which reads return the old version and new drafts are refused.
  Publishing is tightly rate-limited.

### No write API

Subscription products, universe age rating, collaborators and experience thumbnails have no
API-key write endpoint (thumbnail writes need first-party OAuth). Notifications, translation,
messaging and server restarts are one-off actions rather than state; `restart-servers` is a CLI
command for that reason.
