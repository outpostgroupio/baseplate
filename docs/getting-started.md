# Getting started

This takes you from installing Baseplate to a first `plan`, `apply` and `destroy` against an
experience you own.

## 1. Install

Install with Rokit, Homebrew or Scoop, or download the archive for your platform from the
[releases page](https://github.com/outpostgroupio/baseplate/releases), extract it and put
`baseplate` on your `PATH`. The [README](../README.md#install) has the command for each. Or, with
Go 1.24 or later:

```console
$ go install github.com/outpostgroupio/baseplate/cmd/baseplate@latest
```

## 2. Create an API key

Create an Open Cloud API key in
[Creator Dashboard → Credentials](https://create.roblox.com/dashboard/credentials). Add your
experience's universe to the key, grant the scopes for the resources you'll manage (listed in
[API key scopes](how-to/api-key-scopes.md)), and save it. Scopes are granted per universe, so
ticking a scope without selecting the universe isn't enough.

Export the key:

```console
$ export ROBLOX_API_KEY="..."
```

You can also keep it in a gitignored `.env` file and load it with `set -a; . ./.env; set +a`.

`baseplate validate` doesn't need the key: it checks the config offline. `plan`, `apply`,
`destroy` and `import` do, and fail with `ROBLOX_API_KEY is not set` without it.

## 3. Write a config

Baseplate doesn't create universes. It adopts one you already have and manages its settings. The
universe id is the number in the experience's Creator Dashboard URL.

Save this as `baseplate.bp.hcl`:

```hcl
provider "roblox" {
  api_key = env("ROBLOX_API_KEY")
}

resource "roblox_experience" "main" {
  universe_id        = "1234567890"
  voice_chat_enabled = true
  desktop_enabled    = true
}

# References like roblox_experience.main.id are resolved during apply, after the
# experience has been reconciled.
resource "roblox_developer_product" "gems_100" {
  universe_id = roblox_experience.main.id
  name        = "100 Gems"
  price       = 25
  description = "Buy 100 gems"
}
```

## 4. Plan and apply

```console
$ baseplate plan
+ roblox_developer_product.gems_100 (create)
    description = Buy 100 gems
    name = 100 Gems
    price = 25
    universe_id = (known after apply)

Plan: 0 to import, 1 to add, 0 to change, 0 to destroy.

$ baseplate apply
$ baseplate plan
Plan: 0 to import, 0 to add, 0 to change, 0 to destroy.
```

`apply` asks for confirmation on a terminal; pass `--auto-approve` to skip it. State is written to
`baseplate.bpstate` (see [State](state.md)). `plan` exits with `0` when there's nothing to do, `2`
when there are changes, and `1` on error, which is handy in CI.

## 5. Destroy

```console
$ baseplate destroy
```

Not everything can be deleted through Open Cloud. Developer products and game passes are taken off
sale, badges are disabled, and published places and Image or Model assets are left alone; in each
case Baseplate stops managing them. Secrets, experience events and analytics alerts are really
deleted. The [resource reference](resource-reference.md) has the details for each type.

## 6. Resources that already exist

If something already exists in Creator Hub, import it so `apply` doesn't create a duplicate:

```hcl
import {
  to = roblox_developer_product.gems_100
  id = "1234567890/2233445566"   # <universeId>/<productId>
}
```

See [importing existing resources](how-to/importing-existing-resources.md).

## Next steps

- [`examples/`](../examples/) has a config for each resource.
- [Config reference](config-reference.md) covers `for_each`, `locals`, `moved`, `lifecycle` and
  functions.
- [Managing many assets](how-to/many-assets.md) shows one block for a whole directory of images.
- [GitHub Actions](how-to/github-actions.md) sets up plan-on-PR and apply-on-merge.
