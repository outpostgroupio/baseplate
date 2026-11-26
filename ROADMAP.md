# Roadmap

Baseplate is pre-1.0. The config language and CLI may still change between release candidates.
Feature requests and bug reports go in [GitHub issues](https://github.com/outpostgroupio/baseplate/issues).

## Supported now

- Resources: `roblox_experience`, `roblox_place`, `roblox_place_config`, `roblox_place_media`,
  `roblox_asset`, `roblox_developer_product`, `roblox_game_pass`, `roblox_badge`,
  `roblox_experience_event`, `roblox_universe_secret`, `roblox_analytics_alert` and
  `roblox_experience_config`.
- `plan`, `apply`, `destroy`, `import`, `show`, `output`, `validate`, `restart-servers` and
  `cleanup`, with `--json` output and `--target`.
- `import` and `moved` blocks, `lifecycle { prevent_destroy, replace_triggered_by }` with
  `baseplate_data`, `for_each`, `locals` and file functions, `output` blocks and the
  `baseplate_remote_state` data source, and `plan --generate-config` for imported resources.
- Local state with locking, and a remote HTTP backend compatible with Terraform's.
- A reference state server for Cloudflare (R2 storage, Durable Object locks), with
  `baseplate login` through GitHub and GitHub Actions OIDC for CI, plus `force-unlock`.
- A GitHub Action with example plan-on-PR and apply-on-merge workflows.
- Installing with Rokit, Homebrew or Scoop, and Sigstore-signed release checksums.

## Planned

- An S3-compatible state backend, for teams that already have S3 or R2 credentials.
- A `--debug` flag that logs Open Cloud requests with credentials redacted.
- Managed pricing for game passes.
- References in `for` expressions (e.g. `[for k, a in roblox_asset.store : { asset_id = a.id }]`).
- Possibly: homepage thumbnails (thumbnail personalization) and translated icons and thumbnails,
  which have their own API-key endpoints.
- Possibly: collaborators and permissions, Creator Store products, localization tables, and
  restarts through the beta server-management API once it's stable.
- A docs site.

## Not possible through Open Cloud

These have no write API that an API key can use, so Baseplate can't manage them:

- Subscription products (read-only).
- The universe's age rating. Its name and description are read-only on the universe too, but they
  follow the root place, which `roblox_place_config` manages.
- Uploading a video thumbnail. Uploading a Video asset through Open Cloud costs 2000 Robux and
  needs a public experience. A video added in Creator Dashboard can be listed in
  `roblox_place_media.thumbnails` by its id.
- Place settings such as streaming and allowed gear.

Some resources exist but can't be deleted, so `destroy` disables them or stops managing them:
developer products and game passes are taken off sale, badges are disabled, Image and Model assets
and published places are left as they are.
