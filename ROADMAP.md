# Baseplate — status, roadmap & tracked deferrals

This is the living plan for Baseplate. `CLAUDE.md` points here for status and roadmap so it can
stay focused on how to work in the repo and doesn't need frequent status edits. Per-slice design
docs live in `docs/superpowers/specs/`; the executed task-by-task plans in `docs/superpowers/plans/`.

## Status (2026-07-12)
- **Phase 0 COMPLETE (on main):** provider-agnostic engine — HCL config → dependency graph →
  deterministic plan → apply/destroy → state file (serial/lineage, flock locking). Proven
  against an in-memory mock provider.
- **Phase 1 COMPLETE (on main):** real Roblox provider, **proven live** (plan→apply→converge→
  destroy against a real universe). Resources: `roblox_experience`, `roblox_asset`,
  `roblox_developer_product`, with apply-time reference resolution.
- **Phase 2 slice 1 COMPLETE (robustness hardening):** 404→`provider.ErrNotFound` across all
  resources (out-of-band deletes drift-recreate); non-string reference convergence fixed by
  type-preserving state marshal (`Marshal(v, cty.DynamicPseudoType)` — the write side previously
  emitted bare JSON that every `DynamicPseudoType` read silently dropped); gated live
  `roblox_asset` test, **proven live**. Live-discovered + fixed: `assetId` is a wire **string**
  (not int64); Image/Decal assets are **not archivable** → `asset.Delete` unmanages-in-place.
  See `docs/superpowers/specs/2026-07-03-baseplate-phase2-robustness-design.md`.
- **Phase 2 slice 2 COMPLETE (`roblox_place` + Triggers), proven live:** new reusable engine
  capability `provider.Triggerable` (config-computed values persisted in state that force an Update
  on divergence, rendered via AttrDiff) + `state.Resource.Triggers`; `roblox_place` publishes an
  existing place's `.rbxl`/`.rbxlx` and re-publishes on content change (all attrs `Computed`, change
  detection via `content_hash` trigger); `opencloud.Client.DoRaw` + `PlaceClient.Publish`. Live
  lifecycle verified (publish→converge→re-publish→converge→no-op destroy).
  See `docs/superpowers/specs/2026-07-03-baseplate-phase2-place-design.md`.
- **Phase 2 slice 3 COMPLETE (`baseplate import`), proven live:** classic-CLI, config-required,
  state-only adoption of an existing resource — `baseplate import <address> <id>` reads the live
  resource via the provider and writes a state entry (under the flock lock), so plan/apply
  reconcile instead of creating a duplicate. Engine fn `apply.Import` (state-only; reuses the
  state-writing + trigger helpers). Errors on not-declared / already-in-state / not-found. Live
  import of the test universe converged. See
  `docs/superpowers/specs/2026-07-04-baseplate-phase2-import-design.md`.
- **Phase 2 slice 4 COMPLETE (declarative `import {}` block), proven live:** modern
  config-driven import — `import { to = <address>  id = "<id>" }` blocks that `plan` previews
  (`will be imported (id …)`, `N to import`) and `apply` executes **one-pass** (adopt the live
  resource, trigger baseline from config, then reconcile to config in the same apply). Imports are a
  read-only POST-PASS `plan.ResolveImports` over the built plan (chosen to leave `plan.Build`'s ~38
  call sites untouched); `config.Load` now returns `[]plan.ImportBlock`; `Change.ImportID` +
  `render`. Errors on not-declared / not-found / duplicate. Live import-block adopt+converge
  verified. See `docs/superpowers/specs/2026-07-05-baseplate-phase2-import-block-design.md`.
- **Phase 2 slice 5 COMPLETE (`roblox_universe_secret`), proven live:** first
  resource with a full CRUD lifecycle incl. a **real hard-delete**. Plaintext `value` is
  `env()`-sourced, sealed client-side with a LibSodium sealed box
  (`golang.org/x/crypto/nacl/box.SealAnonymous`, pure Go), and NEVER persisted to state
  (provider returns attrs sans `value`) or rendered in plan output (new `provider.Sensitive`
  engine capability redacts it in text + JSON + import previews). Value-change detection via a
  `value_hash` trigger (reuses `Triggerable`); `domain` is the one normal diffable attr. No
  single-GET → `Read` = list-and-find; PATCH has no updateMask. New `opencloud.SecretClient`.
  Live lifecycle verified (create→converge→rotate value+domain→converge→destroy→verified gone).
  Live-discovered + fixed: the secrets list endpoint **always returns a non-empty
  `nextPageCursor`** (even on the empty final page), so `List` must terminate on an **empty
  page**, not an empty cursor — the naive cursor loop paginated forever and tripped the 120/min
  rate limit (429). See `docs/superpowers/specs/2026-07-10-baseplate-phase2-universe-secret-design.md`.
- **Phase 2 slice 7 COMPLETE (`roblox_game_pass`), proven live:** near-clone of
  `roblox_developer_product` — no DELETE surface, so `destroy` sets `is_for_sale = false` and
  unmanages it. New `opencloud.GamePassClient` on the `game-passes/v1` service: create/update are
  **multipart**, create is **synchronous** (no Operation poll, unlike assets), update's PATCH
  returns an **empty body** so `Update` returns error-only and the resource rebuilds state from the
  desired config (no read-back), and `gamePassId`/
  `iconAssetId` decode as JSON **numbers** into typed `int64` (opposite of `assetId`, which is a
  wire string). A risk-first live probe found Roblox's `isManagedPricingEnabled` requires prior
  manual acceptance in the Creator Dashboard, so **managed pricing was cut from v1** — `price` is
  Required instead. Live lifecycle verified (create→converge→update→converge→off-sale destroy).
  See `docs/superpowers/specs/2026-07-13-baseplate-phase2-game-pass-design.md`.
- **Phase 2 slice 8 COMPLETE (`roblox_badge`), proven live:** first resource with a **split
  read/write host** — write (create/update/icon) goes through the Open Cloud legacy bridge on
  `apis.roblox.com` (api-key; create used `legacy-universe.badge:manage-and-spend-robux`, update
  used `legacy-universe.badge:write`. The icon-overwrite endpoint is spec-documented as requiring
  `legacy-badge:manage`, but the live lifecycle — including the icon upload — passed with only the
  two `legacy-universe.badge:*` scopes granted, so the third scope's necessity is unconfirmed; see
  `docs/how-to/api-key-scopes.md`), while
  `Get` is a **public, unauthenticated single-GET** on a different host,
  `GET https://badges.roblox.com/v1/badges/{badgeId}`. New
  `opencloud.BadgeClient` takes two `*opencloud.Client`s (`write`, `read`) for this reason. Live-
  confirmed: create is **free** (`expectedCost=0` accepted) but the icon (`files` multipart part) is
  **required** — omitting it 400s with code `11` ("The badge icon is invalid"); `id`/`iconImageId`
  decode as JSON **numbers** (int64), same convention as `roblox_game_pass`; the icon-overwrite
  multipart field is **`Files`** (capital), asymmetric with create's lowercase `files`; the error
  envelope is `{"errors":[{code,message}]}` (an array), which doesn't match `opencloud.APIError`'s
  flat decode. No DELETE surface → `destroy` sets `enabled=false` and unmanages, same pattern as
  `roblox_game_pass`/`roblox_developer_product`. Live lifecycle verified end-to-end (create→
  converge→update name+disable→converge→icon change via the `content_hash` Trigger→converge→
  disable+unmanage destroy) in `internal/provider/roblox/live_badge_test.go`. Deferred: `Update` is
  stateless w.r.t. prior triggers, so a name-only edit still re-uploads/re-moderates the icon (see
  the slice-8 deferral note in the Roadmap section below — a general engine fix, not badge-specific).
  See `docs/superpowers/specs/2026-07-13-baseplate-phase2-badge-design.md`.
- **`roblox_analytics_alert` IMPLEMENTED (not yet proven live):** production alerting on
  experience metrics through Roblox's **EXPERIMENTAL** `analytics-alert-control-plane/v1` API. Full
  CRUD with a real hard delete. `Read` lists and finds by id (`?ids=` filter; there is no single
  GET), and import ids are `<universe>/<alertId>`. New `opencloud.AnalyticsAlertClient`. To make
  read-back converge, the nested wire shape is flattened into top-level attrs: `operator`/`threshold`/
  `evaluation_mode`/`period_offset_multiplier` (from `condition`), `breakdown_dimension` (the API
  allows one), `filters` as `map(set(string))`, `webhook_configuration_ids` as `set(string)` and
  `enabled` (from `configState`). Sets are used so wire order can't cause drift. Read always
  returns the empty value for unset optionals, so `""`/`{}`/`[]` in config converge and clear the
  remote value. General fixes made along the way: plan rendering prints collection values as
  compact JSON instead of a cty Go dump, `configgen.zeroValue` now gives typed empty collections
  (closing the slice-6 deferral), and `opencloud.parseAPIError` also decodes the
  `{"errorCode"}` and gateway `{"errors":[…]}` envelopes. **Live status:** the key first lacked
  `universe.analytics.alert:*` (`403 Scope not authorized.`). Once the scopes were granted, every
  endpoint returned `403 PERMISSION_DENIED "Universe … is not enrolled in
  PerformanceMonitoringV2."`. The docs gate alerts on the Performance dashboard's **100+ DAU**
  eligibility and document no opt-in, and the test universe is below that bar. The client now
  translates that 403 into an actionable `AlertsNotEligibleError`, and the gated
  `TestLiveAnalyticsAlertLifecycle` skips on it (and on any other 401/403). **Before calling this
  proven,** run the live test against a universe with 100+ DAU and check: that `alertId` is really a string, that the list is
  really unpaginated, that an empty `description`/`filter`/`breakdown` PATCH clears the value, how
  `configState` behaves while `Syncing`, and how a moderated name is reported.
- **Release automation COMPLETE:** goreleaser pipeline (`.goreleaser.yaml` + `.github/workflows/release.yml`)
  — a `v*` tag push cross-compiles 6 static `CGO_ENABLED=0` binaries (linux/darwin/windows ×
  amd64/arm64) and publishes a GitHub Release (archives, `checksums.txt`, grouped changelog,
  `prerelease: auto`). New `internal/version` stamped via `-ldflags`, surfaced by `baseplate version`.
  See `docs/superpowers/specs/2026-07-13-baseplate-release-automation-design.md`.
- Next: **Phase 2** remaining slices (see Roadmap below).

Read `docs/superpowers/specs/2026-07-03-baseplate-phase1-roblox-provider-design.md` for the
Phase 1 design + rationale, and `docs/superpowers/plans/` for the executed task-by-task plans.

## Roadmap & tracked deferrals (from the review loops)
**Phase 2 — coverage + robustness:**
- **DONE (slice 1):** 404→`provider.ErrNotFound` across all resources; live `roblox_asset` test;
  non-string reference convergence (root cause was a state wire-format mismatch — write emitted
  bare JSON, reads used `DynamicPseudoType` wrapper and silently dropped every non-`id` attr;
  fixed by marshaling state against `cty.DynamicPseudoType`).
- **DONE (slice 2):** `roblox_place` (.rbxl/.rbxlx publish + versioning) + the reusable
  `Triggerable` engine capability. Proven live.
- More resources — see the **[Resource surface audit](#resource-surface-audit-2026-07-13)** below for
  the per-candidate Open Cloud write-surface findings (which are viable, which are not, and why).
- **DONE (slice 3):** `baseplate import <address> <id>` — classic-CLI, config-required, state-only
  adoption (engine `apply.Import`). Proven live.
- **DONE (slice 4):** declarative `import {}` block — `plan.ResolveImports` post-pass + one-pass
  adopt+reconcile apply. Proven live. Follow-up (done, slice 6): config generation.
- **DONE (slice 6):** config generation — `baseplate plan --generate-config=FILE` writes HCL for
  each `import {}` block that has no `resource` block (read-only; never touches state). New pure
  `internal/config/configgen` emitter (`hclwrite`, introspects `DecodeSpec()`, sources values from
  `Read`); required-unreadable inputs → placeholder + `# TODO`, optional-unreadable → omitted,
  **sensitive attrs never emit their value**. CLI partitions declared vs undeclared imports (+ dedup
  guard). Native flag name `--generate-config` (dropped Terraform's `-out`). Proven live (generated
  a real experience's config; re-parses valid). See
  `docs/superpowers/specs/2026-07-12-baseplate-phase2-config-generation-design.md`.
- **DONE (slice 5):** `roblox_universe_secret` — full CRUD incl. real hard-delete; client-side
  LibSodium sealed box; no-plaintext-in-state (`value_hash` trigger + new `provider.Sensitive`
  render redaction). NOTE: **subscription-products has NO Open Cloud write surface** (read-only
  per-user GET only; products are Creator-Dashboard-managed only), so the roadmap's
  `subscription-products` monetization resource is not implementable — secrets was chosen
  instead.
- More candidate resources — see the **[Resource surface audit](#resource-surface-audit-2026-07-13)**
  below (supersedes the old one-line "game passes, badges, thumbnails, social links, notifications,
  localization" list, which mixed viable and non-viable candidates together).
- Richer env overlays; config modules.
- **NEW deferrals (from slice-1 review):**
  - `stateResource` (apply.go) silently drops an attribute on a `ctyjson.Marshal` error
    (`if err == nil {…}`, no error surfaced) — the write-side mirror of the read-side bug slice 1
    fixed. Unreachable today (values are known provider outputs); surface the error anyway.
  - `opencloud.IsNotArchivable` matches the 400 message substring `"archivable"` (Open Cloud gives
    no structured code); pin it with a Phase 4 contract test.
  - **State on-disk wire format changed** this slice (bare → `{value,type}` wrapper); a
    version-gate / migration for old state files is deferred to Phase 3. Slice 2 further added a
    `triggers` object to state (`omitempty`) — same deferred-migration bucket.
  - **From slice 2 (render/UX polish, Phase 3):** a *create* plan for a triggerable resource shows
    no trigger line (triggers computed at apply) — surface initial trigger values like
    known-after-apply. Also: `plan` now does file I/O for triggerable resources (hashing the place
    file); unify `splitPlaceID`/`splitProductID` into one helper; `roblox_place` reads the file
    twice per apply (hash + upload) — read once if place files get large.
  - **From slice 4 (all defer, cosmetic):** `plan.nameFromAddress` duplicates `apply.name` (fold
    into the splitter-unification cleanup); `config.importAddress` truncates a 3-part `to`
    (`type.name.extra` → `type.name`) instead of erroring (add a `len(tr)!=2` guard); an
    import-that-updates does two `b.Put`/`Serial++` (adopt commit then Update commit — harmless,
    second supersedes).
  - **From slice 5 (all defer, cosmetic):** `splitSecretID` is the 3rd copy of the `split*ID`
    pattern (fold into the splitter-unification cleanup); `roblox_universe_secret.Computed()` lists
    `create_time`/`update_time` that `stateAttrs` never populates (harmless dead entries); the
    sensitive-set map is rebuilt at three render sites in `render.go` (factor into a helper); no
    client-level `SecretClient.Update` unit test (the re-seal path is covered by the live rotate).
  - **From slice 6 (config generation, all defer):** ~~`configgen.zeroValue` defaults a non-primitive
    required attr (list/map/object) to `""`~~ (FIXED with `roblox_analytics_alert`: typed empty
    collections); `configgen.Generate`'s doc comment says "sensitive is always a
    placeholder" but optional+sensitive is actually omitted (value still never leaks; only sensitive
    attr today, `secret.value`, is Required); `roblox_asset` "one-of" `creator_user_id`/
    `creator_group_id` is omitted from generated config (a `# TODO`-worthy gap the generic emitter
    can't flag — a future `GenerateHints()` capability could).
  - **From slice 8 (`roblox_badge`) — trigger-aware Update (own engine slice, general/reusable):**
    `Update(ctx, id, desired)` is stateless — it never receives the prior triggers — so a resource
    that backs an upload with a `content_hash` Trigger *and* also has normal diffable attrs (badge:
    icon trigger + name/description/enabled) can't tell which fired, and must re-run the upload on
    *every* Update (badge re-uploads/re-moderates its icon even on a name-only edit; no correctness
    impact, just churn). Fix: an optional capability that passes the old triggers map into `Update`
    so the upload fires only when its trigger actually changed. Deliberately NOT folded into the
    badge slice — it's a general engine capability. (`roblox_place` doesn't hit this because it is
    all-computed: the trigger is its *only* change signal.)
  - **From slice 8 (`roblox_badge`) review, minor deferrals:** `Delete` PATCHes
    `{name:"",description:"",enabled:false}` (zero-valued name/description, no updateMask) — may
    blank the abandoned badge's name on Roblox's side; untested. A read-then-patch or
    enabled-only disable would avoid mutating fields destroy shouldn't touch. The read-host
    not-found path (404 on `badges.roblox.com` → `provider.ErrNotFound` → drift-recreate) is
    unit-tested but not live-confirmed; the live probe only read an existing badge — relevant only
    to the rare dashboard-side-deletion / stale-state edge. The badge READ client is constructed
    with the api-key (`opencloud.New(apiKey, badgesBase)`) even though `badges.roblox.com` reads
    are public/unauthenticated; a keyless read client would avoid sending the credential to an
    endpoint that ignores it.

### Resource surface audit (2026-07-13)
Audit of the vendored unified Open Cloud spec (`opencloud/specs/openapi-unified.json`) for the
candidate resources, so we stop losing which are actually buildable. **Method-verified against the
spec, not yet live-verified** — Roblox specs routinely lie about what an API key can do, so every
"viable" row still needs a live probe before we commit a slice (cf. the assetId-is-a-string and
list-cursor surprises). Reproduce with `.superpowers/sdd/surface-probe.jq` /`detail-probe.jq`.

**Viable — has an advertised writable surface (build candidates):**
- **`roblox_game_pass`** — dedicated `game-passes/v1` service (NOT `legacy-`; the old roadmap note
  claiming "legacy-only, create-via-Open-Cloud uncertain" was **wrong**). `POST
  /game-passes/v1/universes/{universeId}/game-passes` (create), `PATCH …/{gamePassId}` (update),
  `GET …/game-passes/creator` (list) + `…/{gamePassId}/creator` (read). No DELETE → destroy
  unmanages/disables, same pattern as `roblox_developer_product`. **LIVE-VERIFIED 2026-07-13**
  (one-off probe, since removed — the API key created+got+updated a real game pass on the test
  universe; the slice's committed live test supersedes it).
  Wire facts learned live: create/update are **multipart** (`name`,`description`,`price`,`isForSale`,
  `imageFile`,`isManagedPricingEnabled`); create is **synchronous** (returns `GamePassConfigV2`
  directly, no Operation poll); **`gamePassId` is a JSON _number_, not a string** (opposite of
  `assetId`) and `iconAssetId` is a large int64 number → decode ids into typed `int64`, never
  `interface{}`/`float64`; **PATCH returns an empty body** → the client's `Update` returns error-only
  and the resource rebuilds state from the desired config; price nests under
  `priceInformation.defaultPriceInRobux`; `iconAssetId` is
  auto-assigned even with no upload (→ computed). Near-clone of `roblox_developer_product`.
  **SHIPPED (slice 7)** — see the Status entry above. Managed pricing (`isManagedPricingEnabled`)
  was cut from v1: it requires prior manual acceptance in the Creator Dashboard, so it isn't
  something Baseplate can turn on for you; `price` is Required instead of that being optional.
- **`roblox_creator_store_product`** — clean modern `/cloud/v2` CRUD (`POST /cloud/v2/creator-store-products`,
  `GET`/`PATCH …/{creator_store_product_id}` with `updateMask`; JSON, no DELETE). **DEPRIORITIZED after
  a 2026-07-13 schema spike:** it manages **selling an uploaded asset on the Creator Store
  marketplace** (a `oneOf` over plugin/model/audio/decal/meshPart/video/fontFamily asset id, each
  `x-immutable`/create-only; a nested USD `Money` `base_price` = `{significand, exponent}`; a
  `published` bool; read-only seller/purchasable/restrictions). That's **marketplace asset selling for
  asset creators**, NOT experience monetization — the latter is already covered by
  `roblox_developer_product` + `roblox_game_pass`. Still a viable slice if we ever target the
  asset-seller persona; design wrinkles noted (nested Money price → expose `price_usd` and convert;
  7-way create-only oneOf; scope string unconfirmed → live-probe first).
- **`roblox_badge`** — `POST /legacy-badges/v1/universes/{universeId}/badges` (create, api-key
  advertised), `PATCH /legacy-badges/v1/badges/{badgeId}` (update), `POST
  /legacy-publish/v1/badges/{badgeId}/icon` (icon). Legacy-prefixed → initially flagged as
  **higher live-probe risk** (legacy bridges sometimes reject api-keys in practice), and the first
  probe did hit a 403 until the user added the `legacy-universe.badge:manage-and-spend-robux` +
  `legacy-universe.badge:write` scopes to the test key. **LIVE-VERIFIED 2026-07-13** (create, update,
  icon overwrite, and read all exercised against the test universe; see the slice-8 Status entry
  above for the full wire-fact summary). **SHIPPED (slice 8)** — no DELETE surface, so `destroy`
  disables (`enabled=false`) and unmanages, same pattern as `roblox_game_pass`. Read is a public
  single-GET on the separate `badges.roblox.com` host, unlike every other resource's
  read-through-`apis.roblox.com`.
- **Localization tables** — `POST /v1/localization-table/tables` + `GET`/`PATCH …/{tableId}` +
  entries endpoints. Legacy, and entry management makes it a **heavy** resource; defer unless asked.

**Not viable — no writable Open Cloud surface (do NOT chase; kept here so we don't rediscover):**
- **`subscription-products`** — confirmed read-only: the only path is
  `/cloud/v2/universes/{id}/subscription-products/{productId}/subscriptions/{subscriptionId}` (read a
  single user's subscription). No product create/update. Creator-Dashboard-managed only. (Already
  noted at slice 5; re-confirmed.)
- **Experience/universe social links** — social-links write surface exists **only for groups**
  (`/v1/groups/{groupId}/social-links`), not universes/experiences. Out of scope unless Baseplate
  grows group management.
- **Universe `displayName`/`description`/`visibility`/`ageRating`** — read-only on the v2 API (set
  via the root place); see `CLAUDE.md`. Only device/voice/private-server fields are writable, which
  `roblox_experience` already covers.

**Actions, not resources (one-shot side effects — don't model as declarative state):**
`POST …/users/{id}/notifications` (send notification), `…:translateText`, `…:publishMessage`,
`…:restartServers`, `users/{id}:generateThumbnail`. These are imperative operations, not reconcilable
state — if ever wanted, they belong behind a CLI verb, not a `resource` block.

**Marginal / needs design:** game **thumbnails/icons** — only per-language legacy endpoints
(`/v1/game-thumbnails/games/{gameId}/language-codes/{lang}/…`) and user-avatar
`:generateThumbnail`; no clean experience-icon write resource surfaced. Revisit if users ask.

**Documentation (user-facing) — markdown-first, started 2026-07-12:**
- **DONE:** `README.md` (front door); `docs/getting-started.md` (install → API key + scopes → first
  `.bp.hcl` → plan/apply/converge/destroy); `docs/cli-reference.md` (commands, flags, exit codes);
  `docs/resource-reference.md` (per-resource attributes — hand-authored, to be replaced by the
  autodocs slice below).
- **DONE — how-to guides** (`docs/how-to/`): importing existing resources (classic + `import {}`),
  managing secrets, references + `env()`, API-key scopes per resource.
- **REMAINING — annotated examples:** grow `examples/` into a walk-through set.
- **DONE — autodocs generator (`resource-docs`):** `internal/provider/resourcedocs.Generate` walks
  `provider.Resources()`, derives each attribute's type + kind tags from `DecodeSpec()`/`Computed()`/
  `Sensitive()`, and merges in-code authored descriptions from the new `provider.Documented`
  capability (`Doc() ResourceDoc{Summary, Attributes, Notes, Scopes, TriggerAttrs, ExampleFile}`) to
  emit `docs/resource-reference.md`. Keyless `roblox.SchemaProvider()` feeds it (no API key needed);
  a `//go:generate` main writes the file and a golden test (`TestResourceReferenceUpToDate`) fails on
  drift. Generation errors if any attribute lacks a description, so docs can't silently drift. Examples
  are linked (`ExampleFile`), not inlined. `ResourceDoc` is the schema-description seam a future config
  validation can reuse. See `docs/superpowers/specs/2026-07-13-baseplate-resource-docs-design.md`.
- **Docs site (deferred):** publish the markdown to GitHub Pages. Target framework: **MkDocs Material**
  (simple, reference-doc-friendly, one-command `gh-deploy`); Astro Starlight if a slicker product site
  is wanted later. The autodocs generator is ours regardless of framework.

**Phase 3 — CI layer:**
- **DONE (slice 1 — CLI automation hardening):** confirmation prompt on `apply`/`destroy`
  (`--auto-approve`/`--yes`, non-TTY auto-skips), `--json` result envelopes + structured errors on
  **all** commands (versioned `format_version: 1`), stdout/stderr discipline via injected streams.
  See `docs/superpowers/specs/2026-07-13-baseplate-phase3-cli-hardening-design.md`.
- **DONE (slice 2 — remote state):** pluggable backend selection via a `backend "http" {}` block; a
  generic HTTP backend speaking the OpenTofu/Terraform http protocol (GET/update + LOCK/UNLOCK, Basic
  auth, custom methods) — GitLab-style managed state works. Local file backend remains the default.
  S3/R2 are additive follow-ups on the same seam. See
  `docs/superpowers/specs/2026-07-13-baseplate-phase3-remote-state-design.md`.
- **DONE (slice 3 — state versioning):** the state file is version-gated — an older binary refuses a
  newer-format state file with an upgrade message, and there's a migration seam (`migrations[v]`) for
  future format changes. `CurrentVersion` is 1 (no format change this slice). See
  `docs/superpowers/specs/2026-07-13-baseplate-phase3-state-versioning-design.md`.
- **DONE (slice 4 — GitHub Actions):** a composite `action.yml` + example `plan-on-pr` (comments the
  diff, treats plan exit-2 as success) and `apply-on-merge` (`--auto-approve`, concurrency-guarded)
  workflows, hardened against script injection and fork-PR token issues. See
  `docs/how-to/github-actions.md` and `docs/superpowers/specs/2026-07-13-baseplate-phase3-github-actions-design.md`.
  **Phase 3 (CI layer) complete.**

**Phase 4 — churn resilience:** the schema-drift watcher (`regen.sh` + the vendored spec seed
it — fetch Roblox/creator-docs `openapi.json`, semantic-diff on change, open an issue/PR),
capability detection, contract-test suite, Rokit distribution.

## Backlog — adopted from the feature wishlist (2026-07-13)
Reconciled from the old `CLAUDES_REQUESTED_FEATURES.md` (now deleted). The bulk of that file is
already **shipped** (plan/apply, diffs, idempotent reconcile, import/adopt, drift detection,
dependency graph, state locking, `--target`, rate-limit backoff, secrets-by-reference, developer
products, assets, generated clients, provider seam, one-key model, `--json`) or already **tracked**
above (env overlays + config modules, autodocs/schema validation, non-interactive mode, error
translation, capability detection, contract tests, Rokit, and the audit's badge/localization/
thumbnail/notification candidates). The genuinely new items we chose to adopt:

- **Resources (need a surface spike before slicing, like game passes got):**
  - **Place config** — max players, streaming, allowed gear on a place (distinct from the `.rbxl`
    publish `roblox_place` already does). Surface unconfirmed (candidates: `/v2/places/{placeId}`,
    universe/place `configuration` endpoints) — spike before committing.
  - **Collaborators / permissions** — manage universe/place collaborator access. Likely
    `legacy-develop/v1/universes/{id}/permissions` + team-create; legacy-prefixed → live-probe risk
    like `roblox_badge`.
- **Resource enhancements:**
  - **`roblox_game_pass` managed pricing** — re-add the `managed_pricing_enabled` attribute that was
    cut from the slice-7 v1. Live-verified blocker: enabling it via `game-passes/v1` returns
    `400 InvalidManagedPricing` ("Managed pricing must be accepted before it can be enabled") unless
    the creator has already accepted managed-pricing terms in the Creator Dashboard. To re-add
    safely, expose it as a documented "you must accept managed pricing in the dashboard first"
    attribute, and translate that 400 into an actionable error. See the slice-7 spec's
    "Managed-pricing live findings" section.
- **DX / observability:** a `--debug` flag (or `BASEPLATE_DEBUG`) that dumps raw Open Cloud
  requests/responses, **redacting the api-key and sealed/sensitive values**. Small, high
  troubleshooting value; folds into the Phase 3 DX hardening.
- **Architecture (longer-horizon):** multi-place / multi-universe repos as a first-class concept —
  shapes config + state layout (per-universe state vs. one config spanning many universes).
  Significant; revisit once Phase 2 resource coverage settles.
- **Known limitation (NOT a planned feature):** partial-failure resume is already effectively
  provided — `apply` commits state after every resource op (`commit` → `b.Put`) and the flow is
  idempotent, so a failed apply resumes by re-running. The only uncovered gap is a crash in the
  narrow window *between* a successful remote mutation and its state write (that resource could
  re-Create on the next run — a possible duplicate for non-idempotent creates). Crash-safe
  checkpointing is the only part a dedicated "resume" feature would add; deferred as low priority.

**Considered but not adopted** (recorded so they aren't re-litigated): stateless-by-listing
reconciliation, rollback / plan history, Luau-authored programmable config (competes with the
locked HCL decision), JSON-schema/LSP config autocomplete, decal→texture id resolution, Rojo build
integration, lockfile version pinning, and a quarantined legacy cookie/CSRF adapter (a deliberate
non-goal — Baseplate stays Open-Cloud-only).
