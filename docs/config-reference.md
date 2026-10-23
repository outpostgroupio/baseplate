# Config reference

A Baseplate config is an HCL file (default `baseplate.bp.hcl`). This page covers the language:
blocks, `for_each`, references, `locals` and functions. The attributes of each `roblox_*` resource
are in the [resource reference](resource-reference.md).

## Blocks

| Block | Purpose |
|---|---|
| `provider "roblox" { ... }` | Provider settings, e.g. `api_key = env("ROBLOX_API_KEY")`. |
| `resource "<type>" "<name>" { ... }` | A resource Baseplate manages. `type.name` must be unique. |
| `import { to = ..., id = ... }` | Adopt an existing remote resource into state on the next `apply`. See [importing](how-to/importing-existing-resources.md). |
| `locals { ... }` | Named values computed once at load time. |
| `backend "http" { ... }` | Remote state. See [remote state](how-to/remote-state.md). |

## `for_each`

`for_each` on a `resource` block declares one **instance** per key:

```hcl
resource "roblox_asset" "icons" {
  for_each = toset(["ui/gem.png", "ui/coin.png"])

  file         = "assets/${each.key}"
  display_name = "icon/${each.key}"
  description  = "Shop icon"
}
```

- The value must be a **map** (or object), whose keys become instance keys and whose values become
  `each.value`, or a **set of strings**, where `each.key` and `each.value` are the same.
- A **list is rejected**; wrap it in `toset(...)`. List order isn't identity.
- The value must be **known when the config loads**: literals, `local.*`, `env()` and functions
  are fine; resource attributes (known only at apply time) are not.
- Keys must be non-empty strings. An empty map or set declares no instances, so any instances
  already in state are planned for deletion, and `plan` warns:
  `for_each for X is empty; all N instances will be destroyed.`
- `each.key` and `each.value` exist only inside a block that sets `for_each`.
- A resource is either singular or `for_each`. Adding or removing `for_each` changes every address,
  which plans a delete plus a create.

### Instance addresses

An instance's address is `type.name["key"]`, for example `roblox_asset.icons["ui/gem.png"]`. The
key is an HCL quoted string: `"` and `\` are escaped with a backslash. This is the form `plan`
prints and the form `--target`, `import` and `show` accept. A resource without `for_each` keeps
the address `type.name`.

Renaming a key (for example, moving a file whose path is the key) changes the address, so the old
instance is destroyed and a new one created. `plan` prints a warning when that happens; see
[managing many assets](how-to/many-assets.md#renaming-or-moving-a-file-replaces-the-asset).

### `for_each` on `import` blocks

`import` blocks accept `for_each` too, with `each` available in `to` and `id`. `to` can index the
target statically or with an expression:

```hcl
import {
  for_each = { "ui/gem.png" = "1234567890", "ui/coin.png" = "1234567891" }
  to       = roblox_asset.icons[each.key]
  id       = each.value
}
```

Every expanded `to` must be a declared instance. Two imports can't target the same instance or use
the same id, and an id that another address in state already manages is refused.
`id` may be a string or a number. `plan --generate-config` doesn't support `for_each` resources;
declare those resource blocks by hand.

## References

A reference reads another resource's attribute, usually its `id`. Baseplate orders resources by
their references and resolves the values at apply time; see
[references and `env()`](how-to/references-and-env.md).

| Form | Meaning |
|---|---|
| `type.name.attr` | Attribute of a resource without `for_each`. |
| `type.name["key"].attr` | Attribute of one `for_each` instance. |
| `type.name[each.key].attr` | Same, with the key computed (any expression that gives a known string). |
| `type.name` | Dependency only. For a `for_each` resource, depends on **every** instance. |

A reference to a key that isn't declared is a load error that lists the declared keys.
`type.name.attr` on a `for_each` resource is an error too: pick an instance.

## `locals`

```hcl
locals {
  sprite_dir = "${path.config}/assets/sprites"
  sprites    = fileset(local.sprite_dir, "**/*.png")
}
```

- Any number of `locals` blocks; each name must be unique across all of them.
- Locals can use other locals (in any order, cycles are an error), `path.config`, `env()` and
  functions. They can't reference resources, so every local is known at load time.
- Read a local with `local.<name>`.

## `path.config`

`path.config` is the directory containing the config file. Relative paths in functions and in
resource attributes such as `file` resolve against the **current working directory**, so build
paths from `path.config` when the config should work wherever `baseplate` runs.

## Functions

| Function | Description |
|---|---|
| `env(name)` | The environment variable `name`, or `""` if unset. |
| `fileset(dir, pattern)` | Set of files (not directories) under `dir` matching a glob; `**` crosses directories. Paths are relative to `dir` and use `/`. A missing `dir` is an error. |
| `file(path)` | The file's contents as a string. The file must be UTF-8. |
| `basename(path)` | Last element of a `/`-separated path: `basename("ui/gem.png")` is `"gem.png"`. |
| `dirname(path)` | Everything but the last element: `dirname("ui/gem.png")` is `"ui"`. |
| `jsondecode(str)`, `jsonencode(value)` | Parse or produce JSON. |
| `toset(v)`, `tomap(v)`, `tolist(v)` | Type conversions. |
| `merge(maps...)`, `keys(map)`, `values(map)`, `lookup(map, key, default)` | Map helpers. |
| `contains(coll, value)`, `length(coll)`, `concat(lists...)`, `distinct(list)`, `flatten(list)` | Collection helpers. |
| `format(spec, args...)`, `join(sep, list)`, `split(sep, str)` | String formatting. |
| `lower(s)`, `upper(s)`, `replace(s, old, new)`, `trimprefix(s, p)`, `trimsuffix(s, p)`, `trimspace(s)`, `regex(pattern, s)` | String helpers. |

HCL's `for` expressions and conditionals work everywhere too, e.g.
`{ for f in local.files : f => basename(f) if f != "skip.png" }`.

## See also

- [Resource reference](resource-reference.md)
- [CLI reference](cli-reference.md)
- [How-to: manage many assets](how-to/many-assets.md)
