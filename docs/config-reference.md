# Config reference

A Baseplate config is an HCL file (default `baseplate.bp.hcl`). This page covers the language:
blocks, `for_each`, `moved`, `lifecycle`, references, `locals` and functions. The attributes of each `roblox_*` resource
are in the [resource reference](resource-reference.md).

## Blocks

| Block | Purpose |
|---|---|
| `provider "roblox" { ... }` | Provider settings, e.g. `api_key = env("ROBLOX_API_KEY")`. |
| `resource "<type>" "<name>" { ... }` | A resource Baseplate manages. `type.name` must be unique. |
| `import { to = ..., id = ... }` | Adopt an existing remote resource into state on the next `apply`. See [importing](how-to/importing-existing-resources.md). |
| `moved { from = ..., to = ... }` | Record that a resource or instance changed address, so state follows it instead of destroying and re-creating it. See [`moved`](#moved). |
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
  which plans a delete plus a create unless a [`moved`](#moved) block says where the existing object
  went.

### Instance addresses

An instance's address is `type.name["key"]`, for example `roblox_asset.icons["ui/gem.png"]`. The
key is an HCL quoted string: `"` and `\` are escaped with a backslash. This is the form `plan`
prints and the form `--target`, `import` and `show` accept. A resource without `for_each` keeps
the address `type.name`.

Renaming a key (for example, moving a file whose path is the key) changes the address, so the old
instance is destroyed and a new one created, unless a [`moved`](#moved) block records the rename.
`plan` prints a warning when a resource both destroys and creates instances; see
[managing many assets](how-to/many-assets.md#renaming-or-moving-a-file).

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

## `moved`

A `moved` block tells Baseplate that something in state now lives at a different address. Before
planning, Baseplate renames the state entry, keeping its remote id, so the plan updates (or leaves
alone) the existing object instead of destroying it and creating a new one. For `roblox_asset`, that
means no re-upload and no new asset id. It works like Terraform's `moved` blocks.

```hcl
# Rename one for_each instance (a file moved from ui/ to icons/).
moved {
  from = roblox_asset.sprites["ui/gem.png"]
  to   = roblox_asset.sprites["icons/gem.png"]
}

# Adopt for_each: the existing singular resource becomes one instance.
moved {
  from = roblox_asset.icon
  to   = roblox_asset.icons["ui/gem.png"]
}

# Rename a whole resource block: every instance moves, keys kept.
moved {
  from = roblox_asset.sprites
  to   = roblox_asset.images
}
```

| `from` → `to` | Meaning |
|---|---|
| `type.a["x"]` → `type.a["y"]` | One instance gets a new key. |
| `type.a` → `type.b["k"]` | The singular resource becomes an instance (adopting `for_each`). |
| `type.a["k"]` → `type.b` | An instance becomes a singular resource (dropping `for_each`). |
| `type.a` → `type.b` | The whole resource is renamed. Every instance moves and keeps its key. |

### `for_each` on `moved` blocks

**Baseplate extension:** Terraform doesn't allow `for_each` on `moved` blocks. Baseplate does,
so a whole directory of renamed files can move in one block. `each.key` and `each.value` can be
used in the index of `from` and `to`, and the rules for the `for_each` value are the same as for
resources:

```hcl
locals {
  # old key => new key
  renames = {
    "ui/gem.png"  = "icons/gem.png"
    "ui/coin.png" = "icons/coin.png"
  }
}

moved {
  for_each = local.renames
  from     = roblox_asset.sprites[each.key]
  to       = roblox_asset.sprites[each.value]
}
```

The map can come from a file, e.g. `jsondecode(file("${path.config}/asset-renames.json"))`. See
[managing many assets](how-to/many-assets.md#renaming-or-moving-a-file) for a way to generate it.

### How moves are applied

- **Moves only change state.** Nothing is called on Roblox. After the move, the resource is planned
  at its new address like any other: a no-op if nothing else changed, an update if its attributes
  differ.
- **`plan` doesn't write state.** It shows the moves (`~ X has moved to Y`) and counts them
  (`…, 1 to move.`; `summary.move` and a `moves` list in `--json`). Moves are changes, so `plan`
  exits `2`.
- **`apply` writes the moves to state**, under the state lock, even when nothing else changes.
  `destroy` applies them too, so `prevent_destroy` is checked against the new address.
- **Idempotent.** If `from` isn't in state and `to` is, the move already happened and the block does
  nothing. Keep the block until every state that needs it (every environment) has been applied,
  then you can delete it.
- **Neither in state:** nothing to do. This matches Terraform: the block may be older than the
  state, or the resource may never have been created.
- **Both in state:** an error. Baseplate can't tell which object should end up at `to`.
  (Terraform skips the move with a warning; Baseplate stops, because keeping both is how an asset
  ends up re-uploaded.)
- **Chains** (`a` → `b` and `b` → `c`) are resolved in order whatever order the blocks are written
  in, and `plan` shows the result as one move (`a` → `c`). A cycle (`a` → `b`, `b` → `a`) is an
  error.
- **`to` doesn't have to be declared.** As in Terraform, the move still happens and the plan then
  destroys the object at its new address.
- **`--target` doesn't filter moves.** They are applied in full even when the target names another
  resource. Terraform instead refuses a targeted plan that doesn't cover every moved address;
  Baseplate's `--target` takes a single address, so that rule would make most targeted runs
  impossible, and a move is a state-only rename that is safe to apply on its own.
- Dependencies recorded in state follow the move, on the moved resource and on everything that
  depends on it.
- `import` blocks are resolved after moves. An `import` whose `to` is a move's destination is
  already satisfied once the move has happened. The `baseplate import` command applies moves
  first too (under the state lock), so importing to a move's destination reports it as already in
  state, and a successful import writes any pending moves with it.

Load-time errors:

- `from` and `to` must be resource addresses (`type.name`, `type.name["key"]` or
  `type.name[expr]`), of a known type. A move can't change the type
  (`roblox_asset` → `roblox_badge`), and `from` and `to` can't be the same address.
- `from` must no longer be declared. If `from` is still in the config, the block is rejected:
  change the config so the resource is declared at `to` instead. This matches Terraform.
- Two blocks can't move the same `from`, or move two things to the same `to`.
- A whole-resource move (`a.b` → `a.c`) can't be combined with an instance move out of the same
  resource (`a.b["k"]` → …), since the whole move already takes every instance, or with an
  instance move into its destination resource (… → `a.c["k"]`). The error names both blocks.
  Chains through a renamed resource (`a.x["k"]` → `a.b["k"]`, then `a.b` → `a.c`) are fine.

## `lifecycle`

A `lifecycle` block inside a `resource` block changes how Baseplate plans that resource. It works
like Terraform's, but only `prevent_destroy` is supported so far:

```hcl
resource "roblox_experience" "prod" {
  universe_id = "1234567890"

  lifecycle {
    prevent_destroy = true
  }
}
```

With `prevent_destroy = true`, any plan that would delete the resource fails instead:

```text
Error: Instance cannot be destroyed: roblox_experience.prod has lifecycle.prevent_destroy set, but the plan calls for this resource to be destroyed. To avoid this error and continue with the plan, either disable lifecycle.prevent_destroy or reduce the scope of the plan using the --target flag.
```

- It covers every path that deletes: `baseplate destroy`, `plan`/`apply` when a `for_each` key
  is removed (or `for_each` becomes empty) while the block itself is still in the config, and a
  **replace** (`-/+`, e.g. a `roblox_asset` whose sound or `asset_type` changed), which destroys the current object
  after creating its successor (or, for `roblox_asset`, leaves it in place but stops managing it).
  `plan`, `apply` and `destroy` all fail before anything changes, with exit code `1`.
- With `for_each`, the setting applies to every instance.
- The value must be a literal `true` or `false`. References, `local.*`, `each.*` and function calls
  are rejected, because the setting has to be known when the config loads.
- `--target` narrows the check like it narrows the plan: if the protected resource isn't targeted,
  nothing blocks.
- **The protection lives only in the config.** It isn't written to state. Removing the
  `lifecycle` block, setting `prevent_destroy = false`, or deleting the whole `resource` block
  removes the protection, and the next plan deletes the resource as usual. This matches Terraform.
  A `destroy` run without a config file has nothing to read the setting from, so nothing is
  protected.
- It only guards Baseplate's own plans. It doesn't stop anyone deleting the resource on Roblox.
- Terraform's other lifecycle arguments (`ignore_changes`, `create_before_destroy`,
  `replace_triggered_by`, which is planned as the general way to force a replace) and the
  `precondition`/`postcondition` blocks aren't supported yet and
  are rejected at load time, as is a second `lifecycle` block.

See [protecting production](how-to/github-actions.md#protecting-production).

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
