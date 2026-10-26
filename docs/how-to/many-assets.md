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

Adopting **does not re-upload**. An asset's `file` and owner are create-only, so after the import
the only possible differences are metadata (`display_name`, `description`), which `apply` updates in
place. Ids may be strings or numbers in the JSON.

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

## Renaming or moving a file replaces the asset

The key **is** the address. Moving `ui/gem.png` to `icons/gem.png` changes the address from
`roblox_asset.sprites["ui/gem.png"]` to `roblox_asset.sprites["icons/gem.png"]`, so the plan
destroys the old instance and creates a new one:

- `roblox_asset` destroy only **unmanages** the old asset (image assets can't be archived), so
  nothing breaks on the platform;
- but the create **uploads a new asset with a new id**, so the game's references change the next
  time you generate the module, and it counts against Roblox's upload limits.

`plan` warns when one resource both destroys and creates instances:

```console
Warning: roblox_asset.sprites: this plan destroys 1 instance(s) and creates 1. Each created instance
is a new remote object with a new id (for roblox_asset, a new upload), so if a for_each key was
renamed the object is replaced, not moved. moved {} blocks, which will rename an instance in place,
are planned.
```

Until `moved {}` blocks land, a rename can't keep the old id: Baseplate refuses to import an id that
another address in state already manages, because two addresses managing one object would let a
later destroy of one delete the object the other still uses. If the new id matters, keep the old
key (for example by keeping the file where it is) until `moved {}` is available.

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
