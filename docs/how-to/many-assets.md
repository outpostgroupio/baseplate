# How-to: manage many assets

A game with hundreds of images, sprites and icons shouldn't need hundreds of hand-written
`resource` blocks. This guide shows the end-to-end pattern: find files with `fileset`, declare one
`roblox_asset` per file with `for_each`, adopt the ids you've already uploaded with an
`import {}` block, then read the result back with `baseplate show --json` to generate a Luau module.
If your current uploader creates Decals, see
[Migrating from a Decal-based uploader](#migrating-from-a-decal-based-uploader).

## 1. Declare one asset per file

```hcl
locals {
  sprites = fileset("${path.config}/assets/sprites", "**/*.png")
}

resource "roblox_asset" "sprites" {
  for_each = local.sprites

  file             = "${path.config}/assets/sprites/${each.key}"
  display_name     = "sprite/${trimsuffix(each.key, ".png")}"
  description      = "Game sprite"
  asset_type       = "Image"
  creator_group_id = 123456
}
```

- `fileset(dir, pattern)` returns every **file** under `dir` matching the pattern (`**` crosses
  directories), relative to `dir`, with forward slashes on every OS. A missing directory is an
  error, so a typo can't silently plan to destroy everything.
- `for_each` makes one **instance** per key. Each instance's address is
  `roblox_asset.sprites["ui/gem.png"]`, and inside the block `each.key` (and `each.value`) is that
  file's path.
- `path.config` is the directory holding the config file, so the config works no matter where you
  run `baseplate` from. Plain relative paths resolve against the current directory.

`plan` lists one create per file:

```console
$ baseplate plan
+ roblox_asset.sprites["ui/coin.png"] (create)
    ...
+ roblox_asset.sprites["ui/gem.png"] (create)
    ...

Plan: 0 to import, 2 to add, 0 to change, 0 to destroy.
```

`for_each` also takes a map, which is handy when each file needs its own settings:

```hcl
resource "roblox_asset" "icons" {
  for_each = {
    "ui/gem.png"  = { name = "Gem" }
    "ui/coin.png" = { name = "Coin" }
  }

  file             = "${path.config}/assets/${each.key}"
  display_name     = each.value.name
  description      = "Shop icon"
  creator_group_id = 123456
}
```

A plain list isn't accepted (list order isn't identity); wrap it in `toset(...)`.

**Asset names are public and moderated.** Roblox shows `display_name` in Creator Hub and moderates
it, so derive it from something predictable (like the key, as above) rather than free text.

## 2. Adopt assets you've already uploaded

If the game already references uploaded asset ids, adopt them instead of uploading again. Keep the
old uploader's `path → assetId` map as JSON:

```json
{ "ui/gem.png": "1234567890", "ui/coin.png": "1234567891" }
```

and import every entry that has a matching file in one block:

```hcl
locals {
  existing_ids = jsondecode(file("${path.config}/tools/asset-ids.json"))
}

import {
  for_each = { for path, id in local.existing_ids : path => id if contains(local.sprites, path) }
  to       = roblox_asset.sprites[each.key]
  id       = each.value
}
```

```console
$ baseplate plan
roblox_asset.sprites["ui/coin.png"] will be imported (id 1234567891)
roblox_asset.sprites["ui/gem.png"] will be imported (id 1234567890)
+ roblox_asset.sprites["units/warship.png"] (create)
    ...
```

Adopting **does not re-upload**. An asset's owner is create-only, and the import records a hash of
each `file`'s current content as its baseline instead of comparing it with what was uploaded (the
API can't read content back), so after the import the only possible differences are metadata
(`display_name`, `description`), which `apply` updates in place. Ids may be strings or numbers in
the JSON. From then on, editing a file replaces its asset (see
[below](#changing-a-files-content-replaces-the-asset)).

The same holds for assets created by a Baseplate version from before content changes were tracked:
their state has no hash, so the first `apply` records the current file's hash and uploads nothing.

To adopt one instance by hand instead, quote the address for your shell:

```console
$ baseplate import 'roblox_asset.sprites["ui/gem.png"]' 1234567890
```

## Migrating from a Decal-based uploader

Many uploaders create **Decal** assets. A Decal's asset id isn't what `ImageLabel.Image`,
`ImageButton.Image` or `Decal.Texture` need: each Decal wraps a separate **Image** asset that Roblox
creates alongside it, and that image's id is the texture id. So these pipelines resolve every decal
afterwards, typically by running Luau (`InsertService:LoadAsset(decalId)`, then reading
`Decal.Texture`).

With Baseplate you don't need that step:

- **New uploads: use `asset_type = "Image"`**, the default for image files. An Image asset *is* the
  texture, so its `id` goes straight into `rbxassetid://<id>`. There's no wrapper and nothing to
  look up.
- **Existing assets: import the texture ids, not the decal ids.** The image Roblox created behind
  each decal is a normal Image asset owned by the same user or group. Open Cloud can read it, so
  `import` adopts it like any other asset, without re-uploading. Your old pipeline already
  resolved these ids, so keep its `path → textureId` map as JSON and import from that:

```json
{ "ui/gem.png": "9876543210", "ui/coin.png": "9876543211" }
```

```hcl
locals {
  sprites     = fileset("${path.config}/assets/sprites", "**/*.png")
  texture_ids = jsondecode(file("${path.config}/tools/texture-ids.json"))
}

resource "roblox_asset" "sprites" {
  for_each = local.sprites

  file             = "${path.config}/assets/sprites/${each.key}"
  display_name     = "sprite/${trimsuffix(each.key, ".png")}"
  description      = "Game sprite"
  asset_type       = "Image" # explicit, so a decal id in the map shows up in the plan
  creator_group_id = 123456
}

import {
  for_each = { for path, id in local.texture_ids : path => id if contains(local.sprites, path) }
  to       = roblox_asset.sprites[each.key]
  id       = each.value
}
```

The auto-created image takes the decal's name and description, so expect the first plan to rename
it to your `display_name`. Renaming the image doesn't touch the Decal, and the Decal still points at
the same image:

```console
$ baseplate plan
roblox_asset.sprites["ui/coin.png"] will be imported (id 9876543211)
    display_name: Coin => sprite/ui/coin
roblox_asset.sprites["ui/gem.png"] will be imported (id 9876543210)
    display_name: Gem => sprite/ui/gem
```

After `apply`, the next `plan` shows no changes. Files without an entry in the map are new uploads,
and they're created as Image assets.

**Check that the map holds texture ids.** If a decal id got in by mistake, Baseplate adopts the
Decal without complaint, and `rbxassetid://<decalId>` won't render in an `ImageLabel`. Declaring
`asset_type = "Image"`, as above, catches this. The import then plans
`asset_type: Decal => Image`. Roblox can't change an asset's type, so `apply` can't fix that
diff. Replace the entry with the decal's texture id instead.

The decals themselves stay where they are. Nothing in the game needs them once it uses the texture
ids, and Decal assets can't be archived through Open Cloud anyway.

The Luau module from [step 3](#3-read-the-ids-back-show---json) then works unchanged. Every managed
`id` is an image id, so `rbxassetid://<id>` is the texture.

## 3. Read the ids back: `show --json`

`baseplate show --json` prints what state manages, without touching the network or needing an API
key. Each resource has its `address`, `type`, `name`, `key` (for `for_each` instances), `id` and
`attributes`:

```console
$ baseplate show --json | jq -r '.result.resources[] | select(.type == "roblox_asset") | "\(.key)\t\(.id)"'
ui/coin.png	1234567891
ui/gem.png	1234567890
units/warship.png	1234567892
```

A generated Luau module for the game is one `jq` program away:

```sh
baseplate show --json | jq -r '
  "-- Generated by baseplate show --json. Do not edit.\nreturn {",
  (.result.resources[]
    | select(.type == "roblox_asset" and .name == "sprites")
    | "\t[\(.key | @json)] = \"rbxassetid://\(.id)\","),
  "}"' > src/shared/AssetIds.luau
```

```lua
-- Generated by baseplate show --json. Do not edit.
return {
	["ui/coin.png"] = "rbxassetid://1234567891",
	["ui/gem.png"] = "rbxassetid://1234567890",
	["units/warship.png"] = "rbxassetid://1234567892",
}
```

Run it after every `apply` (in CI, right after the apply step) so the module always matches state.

## 4. Applying part of the set

`--target` takes a whole resource or one instance:

```console
$ baseplate apply --target roblox_asset.sprites                       # every sprite
$ baseplate apply --target 'roblox_asset.sprites["units/warship.png"]' # just one
```

## Changing a file's content replaces the asset

Baseplate hashes each asset's `file` (the `content_hash` trigger). When you edit an image or sound,
the next plan replaces that asset:

```console
$ baseplate plan
-/+ roblox_asset.sprites["ui/gem.png"] (replace)
    display_name = ui/gem.png
    ...
    content_hash: 6843b53e… => abeeb0b9…  # forces replacement

    # old object (id 1234567890) is left in place, no longer managed

Plan: 0 to import, 1 to add, 0 to change, 0 to destroy.
```

Open Cloud can't put new content into an existing Image, Decal or Audio asset, so `apply` uploads
a new asset and records its **new id** in state. The old asset is **left in place** on Roblox (it
is never archived, not even audio) and is no longer managed: servers that are still running keep
loading `rbxassetid://<old id>` until you republish the place. Anything that references the
asset's `id` is updated in the same apply, and `baseplate show --json` reports the new id, so
regenerating the Luau module after `apply` picks it up. Each replace counts against Roblox's upload
limits (watch the monthly audio cap).

- A **Model** asset (`.fbx`/`.gltf`/`.glb`/`.rbxm`/`.rbxmx`) takes new content in place instead:
  `apply` uploads a new version under the same id, shown as an ordinary update. Editing only its
  name or description doesn't re-upload the content.
- Changing `asset_type`, or the file extension when `asset_type` is omitted (e.g. `.png` to `.fbx`),
  also replaces: Roblox can't change an existing asset's type.
- If the source files aren't on the machine running `plan` (e.g. CI), assets already in state keep
  their recorded hashes and `plan` warns that content changes can't be detected.
- Moving or renaming a file without changing its bytes doesn't change the hash. (It can still
  change the `for_each` key, which is covered next.)
- `lifecycle { prevent_destroy = true }` blocks a replace, as in Terraform.
- `--target` limits a replace like anything else. If you target only the asset, a resource that
  references its id is updated on the next untargeted run.

## Renaming or moving a file

The key **is** the address. Moving `ui/gem.png` to `icons/gem.png` changes the address from
`roblox_asset.sprites["ui/gem.png"]` to `roblox_asset.sprites["icons/gem.png"]`. On its own, that
makes the plan destroy the old instance and create a new one:

- `roblox_asset` destroy only **unmanages** the old asset (image assets can't be archived), so
  nothing breaks on the platform;
- but the create **uploads a new asset with a new id**, so the game's references change the next
  time you generate the module, and it counts against Roblox's upload limits.

`plan` warns when one resource both destroys and creates instances:

```console
Warning: roblox_asset.sprites: this plan destroys 1 instance(s) and creates 1. Each created instance
is a new remote object with a new id (for roblox_asset, a new upload), so if a for_each key was
renamed the object is replaced, not moved. To keep the existing object and its id, declare the
rename with a moved block, e.g. moved { from = roblox_asset.sprites["ui/gem.png"]  to =
roblox_asset.sprites["icons/gem.png"] } (moved blocks also take for_each, to move many keys at once).
```

### Keeping the asset with a `moved` block

A [`moved` block](../config-reference.md#moved) records the rename. Baseplate renames the state
entry before planning, so the asset keeps its id and nothing is uploaded:

```hcl
moved {
  from = roblox_asset.sprites["ui/gem.png"]
  to   = roblox_asset.sprites["icons/gem.png"]
}
```

```console
$ baseplate plan
~ roblox_asset.sprites["ui/gem.png"] has moved to roblox_asset.sprites["icons/gem.png"]

Plan: 0 to import, 0 to add, 0 to change, 0 to destroy, 1 to move.
```

If `display_name` is derived from the key, the plan also shows an update for the new name. That's a
metadata update, not an upload.

### Moving a whole directory

A `moved` block takes `for_each` (a Baseplate extension; Terraform doesn't allow it), so one block
can move every renamed file. Keep the renames in a JSON file of `old key => new key`:

```hcl
locals {
  sprite_renames = jsondecode(file("${path.config}/sprite-renames.json"))
}

moved {
  for_each = local.sprite_renames
  from     = roblox_asset.sprites[each.key]
  to       = roblox_asset.sprites[each.value]
}
```

Git already knows which files were renamed, so the map can be generated. This lists the renames
under `assets/sprites` between `origin/main` and the working tree, strips the directory prefix (the
keys are relative to the `fileset` directory) and writes the JSON with `jq`:

```sh
git diff --name-status -M --diff-filter=R origin/main -- assets/sprites \
  | awk -F'\t' '{ sub("^assets/sprites/", "", $2); sub("^assets/sprites/", "", $3); print $2 "\t" $3 }' \
  | jq -Rn '[inputs | split("\t") | {(.[0]): .[1]}] | add // {}' > sprite-renames.json
```

```json
{
  "ui/coin.png": "icons/coin.png",
  "ui/gem.png": "icons/gem.png"
}
```

Move the files with `git mv` (or `git add -A` after moving them), since `git diff` doesn't see
untracked files. Review the JSON before committing it, since Git's rename detection is a
similarity guess. A file that
was renamed and heavily edited may show up as a delete and an add instead.

Moves are idempotent: once applied, the old key isn't in state and the new one is, so the block does
nothing. Keep the entries until every environment's state has been applied, then empty the file
(`{}`) or delete the block. A map can also chain renames (`a => b` and `b => c`): Baseplate applies
them in order.

If both the old and the new key are already in state (for example, an earlier apply re-uploaded the
file under its new key), the move is an error. Baseplate can't tell which asset should keep the
address.

## An empty `for_each` destroys everything

If `fileset` matches nothing (a renamed directory, a wrong pattern), `for_each` declares zero
instances and the plan destroys every instance in state. `plan` calls this out separately:

```console
Warning: for_each for roblox_asset.sprites is empty; all 230 instances will be destroyed.
```

Both warnings are also listed under `result.warnings` in `plan --json`, so CI can stop on them.

## See also

- [Config reference](../config-reference.md): `for_each`, `each`, `locals` and every function.
- [CLI reference](../cli-reference.md): `show`, `--target` and address quoting.
- [How-to: import existing resources](importing-existing-resources.md)
- [Resource reference: `roblox_asset`](../resource-reference.md#roblox_asset)
