# Getting started

This guide takes you from nothing to a live `plan` → `apply` → `destroy` cycle against a Roblox
universe. It assumes you have a Roblox account and an experience (universe) you own.

## 1. Install

Download the prebuilt binary for your platform from the
[Releases](https://github.com/outpostgroup/baseplate/releases) page and put it on your `PATH`:

```console
$ chmod +x baseplate && mv baseplate /usr/local/bin/
$ baseplate --help
```

> Release binaries are in progress — see the [roadmap](../ROADMAP.md).

## 2. How it works

Baseplate reads your `.bp.hcl` config, builds a dependency-ordered plan, and reconciles it through
the Roblox Open Cloud API using your API key. State (which resources it manages, and their last-known
values) is written to `baseplate.bpstate` — overridable with `--state`.

You need `ROBLOX_API_KEY` set to manage `roblox_*` resources. Without it, the CLI falls back to an
internal **mock** provider used for the engine's own tests — it doesn't understand the `roblox_*`
types, so an `unknown resource type` error usually means the key isn't set.

## 3. API keys & scopes

To manage real resources, create an **Open Cloud API key** in the
[Creator Dashboard → Credentials](https://create.roblox.com/dashboard/credentials):

- Add your target **universe** to the key.
- Grant the **scopes** for the resources you'll manage. Baseplate surfaces a clear error
  (`401/403 … insufficient scope`) if one is missing.

A few scopes are easy to miss because they're distinct from the general universe scope:

| Resource | Needs |
|---|---|
| `roblox_place` | `universe-places:write` (distinct from `universe:write` / `universe.place:write`) |
| `roblox_universe_secret` | `universe.secret:read` **and** `universe.secret:write` |

> **Tip:** Roblox scopes are configured **per universe**. Ticking a scope isn't enough — the specific
> universe must also be selected on the key, and you must **save** the key. A missing per-universe
> selection shows up as a `403 "Authorization scope does not allow this operation"`.

Export the key:

```console
$ export ROBLOX_API_KEY="…"        # or put it in a gitignored .env.local and source it
```

## 4. Your first real config

Point the config at a universe you own. `universe_id` is the id from the experience's Creator
Dashboard URL. Baseplate **adopts** an existing universe and manages its writable config — it does
not create universes.

```hcl
provider "roblox" {
  api_key = env("ROBLOX_API_KEY")
}

resource "roblox_experience" "main" {
  universe_id        = "YOUR_UNIVERSE_ID"
  voice_chat_enabled = true
  desktop_enabled    = true
}

# A developer product whose universe_id REFERENCES the experience above.
# References resolve at apply time, after the experience is reconciled.
resource "roblox_developer_product" "gems_100" {
  universe_id = roblox_experience.main.id
  name        = "100 Gems"
  price       = 25
  description = "Buy 100 gems"
}

# A secret, pulled from the environment. The plaintext is sealed client-side and
# never written to state or printed in a plan.
resource "roblox_universe_secret" "discord" {
  universe_id = roblox_experience.main.id
  name        = "discord_token"
  value       = env("DISCORD_TOKEN")
  domain      = "discord.com"        # omit for a private key
}
```

## 5. Plan, apply, converge, destroy

`plan` previews, `apply` converges, and a re-`plan` shows nothing left to do:

```console
$ baseplate plan
+ roblox_developer_product.gems_100 (create)
    description = Buy 100 gems
    name = 100 Gems
    price = 25
    universe_id = (known after apply)

Plan: 0 to import, 1 to add, 0 to change, 0 to destroy.

$ baseplate apply      # create/update; state written under a lock
$ baseplate plan
Plan: 0 to import, 0 to add, 0 to change, 0 to destroy.

$ baseplate destroy    # remove what's managed (some resources can't be hard-deleted; see below)
```

(Referenced attributes like `universe_id` show as `(known after apply)` until the resource they
point at is reconciled.) **Exit codes** (useful in CI): `0` = no changes, `2` = changes present,
`1` = error.

**Not everything hard-deletes.** Roblox has no delete path for some resources, so `destroy`
*unmanages them in place* (drops them from state, leaves them on the platform): `roblox_asset`
(image/decal), `roblox_developer_product` (set to not-for-sale), `roblox_place` (can't unpublish).
`roblox_universe_secret` is the exception — it performs a real delete.

## 6. Adopting existing resources (import)

If a resource already exists in Creator Hub, don't let `apply` create a duplicate — import it first.

**Declarative (recommended)** — add an `import` block; `apply` adopts and reconciles in one pass:

```hcl
import {
  to = roblox_experience.main
  id = "YOUR_UNIVERSE_ID"
}
```

**One-off CLI:**

```console
$ baseplate import roblox_experience.main YOUR_UNIVERSE_ID
```

## 7. Where to go next

- [`examples/`](../examples/) — annotated configs.
- [Live testing against real Roblox](roblox-live-testing.md) — the opt-in live test setup.
- [ROADMAP.md](../ROADMAP.md) — what's built, what's next, and known limitations.
