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

## Workflow used here
Superpowers: brainstorm → spec → bite-sized TDD plan → build (subagent-per-task, or inline for
subtle engine bits) → per-task spec+quality review → whole-branch review before each merge. The
transient SDD ledger lives at `.superpowers/sdd/progress.md` (gitignored scratch — the durable
knowledge is consolidated into this file, `ROADMAP.md`, and the committed specs/plans). End commit messages with
`Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>` only when the user asks to commit.
