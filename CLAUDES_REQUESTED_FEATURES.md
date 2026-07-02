What I'd want from a Roblox IaC tool

  Core engine

  - plan/apply split — dry-run diff showing every create/update/delete before mutating
  - Human-readable, colorized diffs
  - Idempotent reconciliation (desired vs actual → converge; safe to re-run)
  - Import/adopt existing resources without recreating them
  - Drift detection (flag out-of-band Creator-Hub changes)
  - Dependency graph + correct ordering (icon uploads before the product referencing it, place before its config)
  - State locking (prevent concurrent CI + local applies)
  - Stateless-by-listing option (reconcile by querying the platform, no fragile state file)
  - Targeted apply (--target a single resource)
  - Partial-failure resume (record progress, continue where it left off)
  - Automatic rate-limit backoff + retry (respect the 3/s write caps)
  - Deterministic plan output (stable ordering, no spurious diffs)
  - Rollback / plan history

  Config

  - Programmable config (Luau-authored) with loops/generators for bulk resources
  - Base + per-environment overlays (staging/prod) with typed merging
  - Schema validation before any API call
  - Config modules/includes (DRY)
  - Secrets by reference only (env/secret-manager), never inline
  - JSON-schema/LSP autocomplete for the config format

  Resource coverage

  - Experiences/universes: name, description, genre, playability, paid access + fee, age/maturity rating, avatar settings, voice chat, social/Discord links, in-experience mentions
  - Places: config (max players, streaming, allowed gear), .rbxl upload via Open Cloud, versioning
  - Developer products (name, price, description, icon, isForSale, managed pricing)
  - Game passes (same surface)
  - Badges
  - Assets: image/icon/thumbnail upload
  - Decal→texture id resolution (bake in the cloud-Luau-exec workaround)
  - Localization / i18n tables (auto-translation, supported languages)
  - Thumbnails/icons for experiences and places
  - Notifications config
  - Collaborators/permissions

  Roblox-churn resilience

  - API clients generated from Roblox's Open Cloud OpenAPI specs
  - Provider/adapter seam isolating each resource's API calls
  - Quarantined legacy adapter (cookie/CSRF) for anything not in Open Cloud, loudly flagged
  - API version/capability detection

  Auth & keys

  - One key model, scoped by permission
  - Reuse a single key across all operations (publish, assets, monetization)
  - Clear "your key is missing scope X" errors
  - Per-environment credential resolution

  DX & CI

  - --json output + meaningful exit codes for CI
  - Non-interactive mode
  - Roblox API error translation into actionable messages
  - Verbose/debug logging of raw requests
  - rojo build integration as a place-upload build step (clean seam: Rojo = tree, this = platform config)
  - Single binary, Rokit/Foreman-installable
  - Mock Roblox server + contract tests

  Integrations / ecosystem

  - Provider abstraction so Roblox is a provider, not hardcoded (room for e.g. the Cloudflare Worker matchmaker)
  - Lockfile pinning of asset/place versions
  - Works alongside Rojo without overlap
  - Multi-place / multi-universe repos as a first-class concept
