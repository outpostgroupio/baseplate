# Baseplate

Baseplate is a Terraform-style infrastructure-as-code tool for Roblox, written in Go. You describe
your experience's settings, products, assets and so on in an HCL file, and Baseplate makes Roblox
match it through the [Open Cloud](https://create.roblox.com/docs/cloud/open-cloud) API. `plan`
shows what would change, `apply` makes the changes, and `destroy` removes what it manages.

It was inspired by [Mantle](https://github.com/blake-mealey/mantle), which is no longer maintained.

Baseplate is not affiliated with or endorsed by Roblox Corporation.

```hcl
provider "roblox" {
  api_key = env("ROBLOX_API_KEY")
}

resource "roblox_experience" "main" {
  universe_id        = "1234567890"
  voice_chat_enabled = true
}

resource "roblox_developer_product" "gems_100" {
  universe_id = roblox_experience.main.id  # resolved at apply time
  name        = "100 Gems"
  price       = 25
}
```

```console
$ baseplate plan      # preview changes (exits 2 if there are any)
$ baseplate apply     # make Roblox match the config
$ baseplate destroy   # remove what Baseplate manages
```

## Features

- Plan and apply, with a readable diff before anything changes. Running `apply` twice in a row does
  nothing the second time.
- Resources are ordered by their references, so an icon uploads before the product that uses it.
  References such as `roblox_experience.main.id` are resolved during `apply`.
- Existing resources (made in Creator Hub) can be imported instead of recreated.
- `for_each`, `moved` blocks and `lifecycle { prevent_destroy = true }` work much as they do in
  Terraform.
- State is a local file with a lock, or a remote HTTP backend (the same protocol as Terraform's
  `http` backend).
- `roblox_universe_secret` values are encrypted on your machine before they're sent, never written
  to state, and redacted from plans. Other attributes are not treated as secret: a value read with
  `env()` anywhere else is stored in state and shown in plans like any other value.
- `--json` output and CI-friendly exit codes (`0` no changes, `2` changes, `1` error). There's a
  GitHub Action too.
- A single static binary with no runtime dependencies.

## Resources

| Resource | What it manages |
|---|---|
| `roblox_experience` | A universe's writable settings: voice chat, devices (including console and VR), private servers, social links, public/private. |
| `roblox_place` | Publishes a `.rbxl`/`.rbxlx` file to an existing place, and republishes when the file changes. |
| `roblox_place_config` | A place's name, description and server size. The root place's name and description are the experience's. |
| `roblox_asset` | User- or group-owned Image, Decal, Audio and Model assets. Images and Models take new content in place (same id); other types are re-uploaded. |
| `roblox_developer_product` | Developer products: name, price, description, icon, on sale. |
| `roblox_game_pass` | Game passes: name, price, description, icon, on sale. |
| `roblox_badge` | Badges: name, description, icon, enabled. |
| `roblox_experience_event` | Experience events: title, schedule, visibility, venue, thumbnails. Uses an experimental Roblox API. |
| `roblox_universe_secret` | Open Cloud secrets your experience reads through `HttpService`. |
| `roblox_analytics_alert` | Analytics alerts on experience metrics. Uses an experimental Roblox API and needs an experience with 100+ daily active users. |
| `roblox_experience_config` | Experience Config (beta) keys read through `ConfigService`. |

See the [resource reference](docs/resource-reference.md) for every attribute. Some resources can't
be deleted through Open Cloud, so `destroy` disables them or just stops managing them; the
reference says which.

Some things can't be managed at all because Open Cloud has no write API for them, for example
subscription products and the universe's age rating. The [roadmap](ROADMAP.md) has the list.

## Install

Download an archive for your platform from the
[releases page](https://github.com/outpostgroupio/baseplate/releases), extract it and put the binary
on your `PATH`:

```console
$ tar xzf baseplate_*_linux_amd64.tar.gz   # or unzip on Windows
$ mv baseplate /usr/local/bin/
$ baseplate version
```

Each release has a `checksums.txt` you can check with `sha256sum -c checksums.txt`.

Or build from source with Go 1.24 or later:

```console
$ go install github.com/outpostgroupio/baseplate/cmd/baseplate@latest
```

Baseplate is pre-1.0. Releases so far are release candidates (tags like `v0.1.0-rc.2`) and are
marked as pre-releases on GitHub.

## Quick start

1. Create an [Open Cloud API key](https://create.roblox.com/dashboard/credentials) with the
   [scopes](docs/how-to/api-key-scopes.md) your resources need, and export it:
   ```console
   $ export ROBLOX_API_KEY="..."
   ```
2. Write a `baseplate.bp.hcl`. The [`examples/`](examples/) directory has one per resource.
3. Run `baseplate plan`, then `baseplate apply`.

[Getting started](docs/getting-started.md) walks through this in more detail.

## Documentation

- [Getting started](docs/getting-started.md)
- [CLI reference](docs/cli-reference.md): commands, flags, exit codes and JSON output
- [Config reference](docs/config-reference.md): blocks, `for_each`, `moved`, `lifecycle`,
  references, `env()` and functions
- [Resource reference](docs/resource-reference.md)
- [State](docs/state.md): the state file, locking and the HTTP backend
- Guides: [API key scopes](docs/how-to/api-key-scopes.md),
  [importing existing resources](docs/how-to/importing-existing-resources.md),
  [managing many assets](docs/how-to/many-assets.md),
  [universe secrets](docs/how-to/managing-secrets.md),
  [GitHub Actions](docs/how-to/github-actions.md)

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). To report a security problem, follow
[SECURITY.md](SECURITY.md) rather than opening a public issue.

## License

[MIT](LICENSE). The vendored Roblox Open Cloud API specs are under their own license; see
[NOTICE](NOTICE).
