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
- More resources: game passes, badges, thumbnails/icons, social links, notifications, localization.
- **DONE (slice 3):** `baseplate import <address> <id>` — classic-CLI, config-required, state-only
  adoption (engine `apply.Import`). Proven live.
- **DONE (slice 4):** declarative `import {}` block — `plan.ResolveImports` post-pass + one-pass
  adopt+reconcile apply. Proven live. Follow-up: config generation (`-generate-config-out`
  equivalent) as its own slice.
- **DONE (slice 5):** `roblox_universe_secret` — full CRUD incl. real hard-delete; client-side
  LibSodium sealed box; no-plaintext-in-state (`value_hash` trigger + new `provider.Sensitive`
  render redaction). NOTE: **subscription-products has NO Open Cloud write surface** (read-only
  per-user GET only; products are Creator-Dashboard-managed only), so the roadmap's
  `subscription-products` monetization resource is not implementable — secrets was chosen
  instead.
- More resources: game passes, badges, thumbnails/icons, social links, notifications, localization
  (NOTE: game-passes/badges are LEGACY-API-only — no `/cloud/v2` writable surface; create-via-Open-
  Cloud is uncertain).
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

**Documentation (user-facing) — markdown-first, started 2026-07-12:**
- **IN PROGRESS:** `README.md` (front door) + `docs/getting-started.md` (install → API key + scopes
  → first `.bp.hcl` → plan/apply/converge/destroy).
- CLI reference — commands (`validate`/`plan`/`apply`/`destroy`/`import`), flags
  (`--config`/`--state`/`--target`/`--json`, with defaults), exit codes (0 no changes, 2 changes, 1 error).
- Resource reference — one page per `roblox_*` (attributes: required vs optional, create-only vs
  diffable, provider outputs; examples; the hard-won gotchas). Should be autogenerated (see below).
- How-to guides — managing secrets, importing existing resources (classic + `import {}`), references +
  `env()`, live vs mock provider, API-key scopes per resource.
- Annotated examples — grow `examples/` into a walk-through set.
- **PLANNED SLICE — autodocs generator (`resource-docs`):** a Go generator (à la Terraform
  `tfplugindocs`) that walks `provider.Resources()`, introspects each resource's `DecodeSpec()` +
  `Computed()`, and emits the resource-reference markdown so attribute tables never drift from code.
  Prereq: add an attribute-description capability to resources (`hcldec.AttrSpec` carries no
  description) — likely a small `Describe() map[string]string` (or richer schema) that both the
  generator and future validation can read. Emits markdown, so it is framework-independent. Until it
  lands, the resource reference is hand-authored (`docs/resource-reference.md`).
- **Docs site (deferred):** publish the markdown to GitHub Pages. Target framework: **MkDocs Material**
  (simple, reference-doc-friendly, one-command `gh-deploy`); Astro Starlight if a slicker product site
  is wanted later. The autodocs generator is ours regardless of framework.

**Phase 3 — CI layer:** prebuilt GitHub Actions (plan-on-PR comment, apply-on-merge), remote
state backend + CI locking, `--json`/exit-code hardening, non-interactive mode.

**Phase 4 — churn resilience:** the schema-drift watcher (`regen.sh` + the vendored spec seed
it — fetch Roblox/creator-docs `openapi.json`, semantic-diff on change, open an issue/PR),
capability detection, contract-test suite, Rokit distribution.
