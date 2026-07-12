# How-to: import existing resources

Most Roblox resources already exist — a universe you created in Creator Hub, a place, a developer
product. If you just declare them in config and run `apply`, Baseplate has no state for them and
tries to **create duplicates** (or fails). **Importing** tells Baseplate "this config block already
exists on the platform with this id" so it reconciles the existing resource instead.

Baseplate offers two ways to import.

## Option A: declarative `import {}` block (recommended)

Add an `import` block alongside the resource. On the next `apply`, Baseplate adopts the live resource
and reconciles it to your config **in a single pass**.

```hcl
resource "roblox_experience" "main" {
  universe_id        = "10438196190"
  voice_chat_enabled = true
}

import {
  to = roblox_experience.main   # the resource address to adopt into
  id = "10438196190"            # the platform id to adopt
}
```

`plan` previews it:

```console
$ baseplate plan
roblox_experience.main will be imported (id 10438196190)

Plan: 1 to import, 0 to add, 0 to change, 0 to destroy.
```

`apply` adopts and reconciles. Once imported, you can delete the `import {}` block — it's a no-op
after the resource is in state, so it's also fine to leave it in for reproducibility.

## Option B: one-off CLI import

Adopt a single resource without editing config. The resource must already be **declared** in the
config file.

```console
$ baseplate import roblox_experience.main 10438196190
```

This reads the live resource and writes a state entry under the state lock; it never mutates the
remote. Afterwards, `plan` reconciles any differences between the live resource and your config.

## Which id do I use?

The `id` is the resource's platform identifier — the same value the resource's state id is built
from:

| Resource | Import id |
|---|---|
| `roblox_experience` | the universe id |
| `roblox_developer_product` | `<universeId>/<productId>` |
| `roblox_place` | `<universeId>/<placeId>` |
| `roblox_asset` | the asset id |
| `roblox_universe_secret` | `<universeId>/<name>` |

## Errors

Both paths fail clearly if:

- the address **isn't declared** in the config,
- the resource is **already in state** (nothing to import),
- the id **can't be found** on the platform.

## Verifying

After importing, run `plan`. A clean import of a resource whose config already matches the platform
converges to no changes:

```console
$ baseplate plan
Plan: 0 to import, 0 to add, 0 to change, 0 to destroy.
```

If your config differs from the live resource, `plan` shows exactly what `apply` will change to make
the platform match your config.

## See also

- [Getting started → import](../getting-started.md#6-adopting-existing-resources-import)
- [CLI reference → import](../cli-reference.md#import-address-id)
