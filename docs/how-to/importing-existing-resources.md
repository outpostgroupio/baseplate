# Importing existing resources

If a resource already exists on Roblox (a universe, a product you made in Creator Hub, assets
uploaded by another tool), declaring it in the config isn't enough. Baseplate has no state for it,
so `apply` would try to create a new one. Importing tells Baseplate which existing object a
`resource` block refers to, and from then on it's managed like anything else.

## With an `import` block

Add an `import` block next to the resource. The next `apply` adopts the existing object and then
updates it to match the config, in one run:

```hcl
resource "roblox_experience" "main" {
  universe_id        = "1234567890"
  voice_chat_enabled = true
}

import {
  to = roblox_experience.main
  id = "1234567890"
}
```

`plan` shows the import:

```console
$ baseplate plan
roblox_experience.main will be imported (id 1234567890)

Plan: 1 to import, 0 to add, 0 to change, 0 to destroy.
```

`plan` exits `2` while any import is pending. `apply` runs the imports, or, to fill state without
applying anything else, run `baseplate import` with no arguments: it runs every pending `import`
block, changes nothing on Roblox, and writes state once.

```console
$ baseplate import
Imported 1 resource(s):
  roblox_experience.main (id 1234567890)
```

Once the resource is in state the block does nothing, so you can delete it or leave it.

`import` blocks take `for_each`, which is the easiest way to adopt a set of assets from a JSON map
of ids:

```hcl
import {
  for_each = jsondecode(file("${path.config}/asset-ids.json"))  # { "ui/gem.png": "1234567890", ... }
  to       = roblox_asset.sprites[each.key]
  id       = each.value
}
```

See [managing many assets](many-assets.md#2-adopt-assets-youve-already-uploaded).

If you don't want to write the `resource` block by hand, `baseplate plan --generate-config=FILE`
writes one for each `import` block whose target isn't declared yet. It doesn't support `for_each`
targets.

## With the CLI

`baseplate import` adopts one resource straight away. The resource must already be declared in the
config:

```console
$ baseplate import roblox_experience.main 1234567890
$ baseplate import 'roblox_asset.sprites["ui/gem.png"]' 1234567890
```

It reads the remote object and writes it to state. It never changes anything on Roblox. Run `plan`
afterwards to see how the object differs from your config.

## Places

Roblox doesn't let Baseplate download a published place, so an import can't tell whether the live
place matches your local `file`. Importing a `roblox_place` records its content as unverified and
prints a warning, and the next `plan` shows an update that publishes the file:

```console
$ baseplate plan
~ roblox_place.lobby (update)
    content_hash: (unverified: imported) => 9f2c41d0…
    # (3 unchanged attribute(s) hidden)
```

If you know the live place already matches the file, import it with `--assume-current` (either
form of `baseplate import` takes it) to record the file's hash instead, and the plan stays clean.

## Import ids

| Resource | Import id |
|---|---|
| `roblox_experience` | `<universeId>` |
| `roblox_asset` | `<assetId>` |
| `roblox_place` | `<universeId>/<placeId>` |
| `roblox_place_config` | `<universeId>/<placeId>` |
| `roblox_place_media` | `<placeId>` |
| `roblox_developer_product` | `<universeId>/<productId>` |
| `roblox_game_pass` | `<universeId>/<gamePassId>` |
| `roblox_badge` | `<universeId>/<badgeId>` |
| `roblox_universe_secret` | `<universeId>/<secretName>` |
| `roblox_analytics_alert` | `<universeId>/<alertId>` |
| `roblox_experience_config` | `<universeId>/<repository>` |
| `roblox_experience_event` | `<eventId>` |

Both ways of importing fail if the address isn't declared in the config, is already in state, or
the id doesn't exist. They also refuse an id that another address in state already manages.
