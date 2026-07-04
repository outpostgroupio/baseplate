# Baseplate — project guide for Claude

**Baseplate is a Terraform-style Infrastructure-as-Code tool for Roblox, in Go.** It manages
Roblox resources declaratively via Open Cloud: `baseplate plan/apply/destroy` over an HCL
config, backed by a state file. A maintained, Go-native successor to Mantle. Module:
`github.com/outpostgroup/baseplate`.

## Status (2026-07-03)
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
- Next: **Phase 2** remaining slices (see Roadmap below).

Read `docs/superpowers/specs/2026-07-03-baseplate-phase1-roblox-provider-design.md` for the
Phase 1 design + rationale, and `docs/superpowers/plans/` for the executed task-by-task plans.

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

**Run the CLI:** `go run ./cmd/baseplate {validate|plan|apply|destroy} --config X.bp.hcl --state X.tfstate [--target A] [--json]`.
Exit codes: `0` no changes, `2` changes present, `1` error. With `ROBLOX_API_KEY` set the CLI
uses the real provider; otherwise the in-memory mock.

**Live tests** (real Roblox, opt-in): put `ROBLOX_API_KEY` + `BASEPLATE_TEST_UNIVERSE` in a
gitignored `.env.local`, then `set -a; . ./.env.local; set +a; BASEPLATE_LIVE=1 go test
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

## Roblox spec gotchas (learned; verify against vendored specs before assuming)
- Universe `displayName`/`description`/`visibility`/`ageRating` are **read-only** on the v2 API
  (set via the root place). Writable: `voiceChatEnabled`, `desktop/mobile/tabletEnabled`,
  `privateServerPriceRobux`.
- Assets are **user- or group-owned** (`creationContext.creator.{userId|groupId}`), not
  universe-owned. Create = multipart (`request` JSON part + `fileContent` file) → async
  Operation → poll `/v1/operations/{id}`. Base `https://apis.roblox.com/assets`.
  - `assetId` comes back as a JSON **string**, not the int64 the spec declares (Roblox serializes
    64-bit ids as strings — decode as `string`). Verified live.
  - **Image/Decal assets are NOT archivable** (`:archive` → 400 "not an archivable asset type"),
    so there's no Open Cloud delete path; `roblox_asset` destroy unmanages-in-place (drops from
    state, leaves the asset), like a dev product that can't be hard-deleted. Verified live.
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

## Roadmap & tracked deferrals (from the review loops)
**Phase 2 — coverage + robustness:**
- **DONE (slice 1):** 404→`provider.ErrNotFound` across all resources; live `roblox_asset` test;
  non-string reference convergence (root cause was a state wire-format mismatch — write emitted
  bare JSON, reads used `DynamicPseudoType` wrapper and silently dropped every non-`id` attr;
  fixed by marshaling state against `cty.DynamicPseudoType`).
- **DONE (slice 2):** `roblox_place` (.rbxl/.rbxlx publish + versioning) + the reusable
  `Triggerable` engine capability. Proven live.
- More resources: game passes, badges, thumbnails/icons, social links, notifications, localization.
- `baseplate import` / adopt existing resources; richer env overlays; config modules.
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

**Phase 3 — CI layer:** prebuilt GitHub Actions (plan-on-PR comment, apply-on-merge), remote
state backend + CI locking, `--json`/exit-code hardening, non-interactive mode.

**Phase 4 — churn resilience:** the schema-drift watcher (`regen.sh` + the vendored spec seed
it — fetch Roblox/creator-docs `openapi.json`, semantic-diff on change, open an issue/PR),
capability detection, contract-test suite, Rokit distribution.

## Workflow used here
Superpowers: brainstorm → spec → bite-sized TDD plan → build (subagent-per-task, or inline for
subtle engine bits) → per-task spec+quality review → whole-branch review before each merge. The
transient SDD ledger lives at `.superpowers/sdd/progress.md` (gitignored scratch — the durable
knowledge is consolidated into this file and the committed specs/plans). End commit messages with
`Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>` only when the user asks to commit.
