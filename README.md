# Baseplate

**Terraform-style Infrastructure-as-Code for Roblox, in Go.** Declare your Roblox resources in a
config file and let Baseplate reconcile them via [Open Cloud](https://create.roblox.com/docs/cloud/open-cloud):
`plan` shows the diff, `apply` makes it so, `destroy` tears it down — idempotent and safe to re-run.
A maintained, Go-native successor to [Mantle](https://github.com/blake-mealey/mantle).

```hcl
provider "roblox" {
  api_key = env("ROBLOX_API_KEY")
}

resource "roblox_experience" "main" {
  universe_id        = "10438196190"
  voice_chat_enabled = true
}

resource "roblox_developer_product" "gems_100" {
  universe_id = roblox_experience.main.id  # resolved at apply time
  name        = "100 Gems"
  price       = 25
}
```

```console
$ baseplate plan      # preview changes (exit 2 if any)
$ baseplate apply     # converge to the config
$ baseplate destroy   # tear down what's managed
```

## Why Baseplate

- **Plan/apply workflow** — a colorized, human-readable diff of every create/update/delete before
  anything mutates. Deterministic output, no spurious diffs.
- **Idempotent reconciliation** — desired vs. actual → converge. Re-running is a no-op.
- **Dependency graph** — resources are ordered automatically (an icon uploads before the product
  that references it). References like `roblox_experience.main.id` resolve at apply time.
- **Import/adopt** existing resources (created in Creator Hub) without recreating them.
- **State with locking** — a local state file with an advisory lock, so a CI apply and a local apply
  can't clobber each other.
- **Secrets by reference** — pull secret values from `env()`; they are never written to state or
  printed in a plan.
- **Single self-contained binary** — no runtime dependencies (built pure-Go, `CGO_ENABLED=0`).
- **CI-friendly** — `--json` output and meaningful exit codes (`0` no changes, `2` changes, `1` error).

## Supported resources

| Resource | What it manages |
|---|---|
| `roblox_experience` | A universe's writable config (voice chat, device enablement incl. console/VR). |
| `roblox_place` | Publishes a place's `.rbxl`/`.rbxlx` and re-publishes on content change. |
| `roblox_place_config` | A place's name, description and server size (the root place's name/description are the experience's). |
| `roblox_asset` | User- or group-owned image, decal and audio assets (multipart upload). |
| `roblox_developer_product` | Developer products (name, price, description, icon, on-sale). |
| `roblox_game_pass` | Game passes (name, price, description, icon, on-sale). |
| `roblox_universe_secret` | Open Cloud secrets — full CRUD incl. real delete; values sealed client-side, never stored. |

Plus declarative `import { to = <address>  id = "<id>" }` blocks to adopt existing resources during a
normal `apply`.

**Planned:** badges and creator-store products both have a usable Open Cloud write surface and are
candidate resources — see the [roadmap](ROADMAP.md#resource-surface-audit-2026-07-13).

**Not manageable via Open Cloud** (no write API — Roblox exposes these only through the Creator
Dashboard, so Baseplate can't and won't manage them):

- **Subscription products** — read-only API (per-user subscription lookups only); no create/update.
- **Experience social links** — a write API exists for *groups* only, not experiences/universes.
- **Universe visibility and age rating** — read-only on the API. (The experience's display name and
  description are read-only on the universe too, but they follow the root place's, which
  `roblox_place_config` manages.) Baseplate manages the writable universe settings (voice chat,
  device enablement) via `roblox_experience`.

## Install

Baseplate is a single self-contained binary — no runtime dependencies.

**Download a release build** for your platform from the
[Releases](https://github.com/outpostgroupio/baseplate/releases) page, extract it, and put it on
your `PATH`:

```console
$ tar xzf baseplate_*_linux_amd64.tar.gz   # or unzip on Windows
$ chmod +x baseplate && mv baseplate /usr/local/bin/
$ baseplate version
```

Each release ships `checksums.txt`; verify your download with `sha256sum -c checksums.txt`.

**Or install from source** (requires Go 1.24+):

```console
$ go install github.com/outpostgroupio/baseplate/cmd/baseplate@latest
```

> Pre-1.0 builds are published as pre-releases (tags like `v0.1.0-rc.1`). A Rokit/Foreman install
> is on the [roadmap](ROADMAP.md).

## Quick start

1. Create an [Open Cloud API key](https://create.roblox.com/dashboard/credentials) with the scopes
   for the resources you'll manage (see [Getting Started](docs/getting-started.md#3-api-keys--scopes)),
   and export it:
   ```console
   $ export ROBLOX_API_KEY="…"
   ```
   `roblox_*` resources need the key; without it the CLI falls back to an internal mock provider used
   for the engine's own tests.
2. Write a `baseplate.bp.hcl` (see [`examples/`](examples/)).
3. Preview and apply:
   ```console
   $ baseplate plan
   $ baseplate apply
   ```

Full walkthrough: **[docs/getting-started.md](docs/getting-started.md)**.

## CLI

```
baseplate <command> [--config baseplate.bp.hcl] [--state baseplate.bpstate] [--target ADDR] [--json]

  validate   Load and type-check the config.
  plan       Show the changes apply would make. Exit 2 if there are changes.
  apply      Reconcile resources to the config, writing state.
  destroy    Remove everything the state manages.
  import <address> <id>   Adopt an existing resource into state.
  show [address]          Print the resources in state (--json for tooling).
```

`--config` defaults to `baseplate.bp.hcl`, `--state` to `baseplate.bpstate`. `--target` limits an
apply/plan to one resource, or one `for_each` instance (`'type.name["key"]'`); `--json` emits machine-readable output for CI.

## Status

Baseplate is pre-1.0 and under active development. The engine and the Roblox provider are proven
against a live universe. Current status, the remaining roadmap, and tracked deferrals live in
**[ROADMAP.md](ROADMAP.md)**.

## Documentation

- [Getting started](docs/getting-started.md) — install → API key → first config → apply → destroy.
- [CLI reference](docs/cli-reference.md) — commands, flags, exit codes.
- [Config reference](docs/config-reference.md) — blocks, `for_each`, references, `locals`, functions.
- [Resource reference](docs/resource-reference.md) — every `roblox_*` resource and its attributes.
- **How-to guides:**
  - [Import existing resources](docs/how-to/importing-existing-resources.md)
  - [Manage many assets (`for_each`, `fileset`, `show --json`)](docs/how-to/many-assets.md)
  - [Manage universe secrets](docs/how-to/managing-secrets.md)
  - [References and `env()`](docs/how-to/references-and-env.md)
  - [API key scopes](docs/how-to/api-key-scopes.md)
- [Examples](examples/) — annotated `.bp.hcl` configs.
- [Live testing against real Roblox](docs/roblox-live-testing.md).
