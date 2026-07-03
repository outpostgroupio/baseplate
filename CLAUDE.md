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
- Next: **Phase 2** (see Roadmap below).

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
- Developer products: create/update are **multipart form fields** (`name`, `price`, `isForSale`,
  `imageFile`, ...). The icon is a **direct file upload** (`imageFile`), NOT an assetId
  reference. Can't hard-delete → destroy sets `isForSale=false`. Base `https://apis.roblox.com`.
- Universe config: `GET/PATCH /cloud/v2/universes/{id}?updateMask=...`, base `https://apis.roblox.com`.

## Roadmap & tracked deferrals (from the review loops)
**Phase 2 — coverage + robustness:**
- `roblox_place` (.rbxl publish + versioning) — the deferred half of the original v1 slice.
- More resources: game passes, badges, thumbnails/icons, social links, notifications, localization.
- **Map HTTP 404 → `provider.ErrNotFound`** in the Roblox resources' `Read`, so out-of-band
  deletion drift-recreates instead of hard-erroring the plan (currently NOT mapped).
- **Add a live end-to-end test for `roblox_asset`** (only unit-tested with a fake client today;
  the real `/v1/assets` multipart + operation-poll shape is unverified live).
- `baseplate import` / adopt existing resources; richer env overlays; config modules.
- **Non-string referenced-attribute convergence gap:** apply resolves refs from in-memory
  `cty.Value`, but plan resolves from state via `ctyjson.Unmarshal(raw, DynamicPseudoType)`. For
  numbers/objects the round-trip can change the concrete type and break `RawEquals` → spurious
  perpetual diff. Our current refs are strings (`.id`) so it's latent; add a test + fix before
  any numeric/object reference ships.

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
