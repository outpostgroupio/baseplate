# Roadmap

Baseplate is pre-1.0. The config language and CLI may still change between release candidates.
Feature requests and bug reports go in [GitHub issues](https://github.com/outpostgroupio/baseplate/issues).

## Supported now

- Resources: `roblox_experience`, `roblox_place`, `roblox_place_config`, `roblox_asset`,
  `roblox_developer_product`, `roblox_game_pass`, `roblox_badge`, `roblox_experience_event`,
  `roblox_universe_secret`, `roblox_analytics_alert` and `roblox_experience_config`.
- `plan`, `apply`, `destroy`, `import`, `show`, `validate` and `restart-servers`, with `--json`
  output and `--target`.
- `import`, `moved` and `lifecycle { prevent_destroy }` blocks, `for_each`, `locals` and file
  functions, and `plan --generate-config` for imported resources.
- Local state with locking, and a remote HTTP backend compatible with Terraform's.
- A reference state server for Cloudflare (R2 storage, Durable Object locks), with
  `baseplate login` through GitHub and GitHub Actions OIDC for CI, plus `force-unlock`.
- A GitHub Action with example plan-on-PR and apply-on-merge workflows.
- Installing with Rokit, Homebrew or Scoop, and Sigstore-signed release checksums.

## Planned

- `lifecycle { replace_triggered_by }`, to force a replace on demand.
- An S3-compatible state backend, for teams that already have S3 or R2 credentials.
- A `--debug` flag that logs Open Cloud requests with credentials redacted.
- Managed pricing for game passes.
- Possibly: collaborators and permissions, Creator Store products, localization tables, and
  restarts through the beta server-management API once it's stable.
- A docs site.

## Not possible through Open Cloud

These have no write API that an API key can use, so Baseplate can't manage them:

- Subscription products (read-only).
- The universe's age rating. Its name and description are read-only on the universe too, but they
  follow the root place, which `roblox_place_config` manages.
- Experience thumbnails and icons (the write API needs first-party OAuth).
- Place settings such as streaming and allowed gear.

Some resources exist but can't be deleted, so `destroy` disables them or stops managing them:
developer products and game passes are taken off sale, badges are disabled, Image and Model assets
and published places are left as they are.
