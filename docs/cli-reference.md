# CLI reference

```
baseplate <command> [flags]
```

Baseplate manages Roblox resources declaratively: you describe the desired state in an HCL config,
and the CLI reconciles the platform to match, tracking what it manages in a state file.

## Global flags

These are accepted by every command.

| Flag | Default | Description |
|---|---|---|
| `--config` | `baseplate.bp.hcl` | Path to the HCL config file. |
| `--state` | `baseplate.bpstate` | Path to the state file. |
| `--target` | *(none)* | Limit `plan`/`apply`/`destroy` to one resource (`roblox_asset.icon`; for a `for_each` resource, every instance) or one instance (`'roblox_asset.sprites["ui/gem.png"]'`). See [Addresses](#addresses). A malformed address is an error. |
| `--json` | `false` | Emit machine-readable output on **every** command. Results are a versioned envelope `{ "format_version": 1, "command": ..., "result": ... }` on stdout; errors are `{ "format_version": 1, "command": ..., "error": { "message": ... } }` on stderr. |

State can also be stored remotely via a `backend "http" {}` block — see [Remote state](how-to/remote-state.md).

### Approval flags (`apply` / `destroy` / `restart-servers`)

| Flag | Default | Description |
|---|---|---|
| `--auto-approve` | `false` | Skip the interactive approval prompt and apply immediately. |
| `--yes` | `false` | Alias for `--auto-approve`. |

`apply` and `destroy` render the plan (`restart-servers` describes the restart) and then, on an interactive terminal, prompt for
confirmation — only `yes` proceeds. In a non-interactive session (no TTY, e.g. CI) the prompt is
skipped automatically, so jobs never hang waiting on stdin. Because a text prompt would corrupt the
JSON stream, `--json` on an interactive terminal requires `--auto-approve` (otherwise the command
errors).

## Addresses

`--target`, `import` and `show` take a resource address:

| Address | Selects |
|---|---|
| `type.name` | A resource. For a `for_each` resource, every instance (`--target`, `show`). |
| `type.name["key"]` | One instance of a `for_each` resource. |

The key is an HCL quoted string: inside it, write `\"` for a quote and `\\` for a backslash. The
brackets and quotes mean something to your shell, so wrap the whole address in single quotes:

```console
$ baseplate apply --target 'roblox_asset.sprites["ui/gem.png"]'
$ baseplate import 'roblox_asset.sprites["ui/gem.png"]' 1234567890
```

An unquoted key (`roblox_asset.sprites[ui/gem.png]`) is rejected with
`invalid address "...": key must be a quoted string`. `plan` and `show` print addresses in exactly
this form, so you can copy them.

## Environment

- **`ROBLOX_API_KEY`** — the Open Cloud API key. Required to manage `roblox_*` resources; reference
  it from the config with `env("ROBLOX_API_KEY")`. Without it the CLI falls back to an internal mock
  provider used for the engine's own tests (it doesn't understand the `roblox_*` types).

## Exit codes

| Code | Meaning |
|---|---|
| `0` | Success, no changes. |
| `2` | (`plan` only) Success, and there are changes to apply. |
| `1` | Error. |

The `2` code lets CI branch on "is there drift?" without parsing output. `apply`/`destroy`/
`restart-servers` return `1` both on error and when a confirmation prompt is declined.

## Commands

### `validate`

Load and type-check the config. Fails if the file is malformed, references an unknown resource
type, or has an invalid attribute. Does not contact Roblox or read state.

```console
$ baseplate validate
Configuration is valid.
```

### `plan`

Show the changes `apply` would make, as a colorized diff. Read-only: it reads state and queries the
provider but mutates nothing (and takes no lock). Exits `2` if there are changes, `0` if converged.

```console
$ baseplate plan
+ roblox_developer_product.gems_100 (create)
    name = 100 Gems
    price = 25

Plan: 0 to import, 1 to add, 0 to change, 0 to destroy.
```

`--json` emits the plan as structured JSON instead of the text diff. Sensitive attribute values
(e.g. a secret's `value`) are redacted in both formats. Each change has an `address` and, for a
`for_each` instance, its `key`.

Each change is a create (`+`), update (`~`), delete (`-`) or **replace** (`-/+`). A replace happens
when a change can't be made to the existing remote object, so a new one (with a new id) is created
first. The attribute or trigger that forced it is marked, and the last line says what happens to
the old object: some resources destroy it, others leave it in place and only stop managing it.
`roblox_asset` leaves it in place, because live servers keep loading the old id until the place is
republished:

```console
-/+ roblox_asset.click (replace)
    display_name = Click
    file = assets/click.ogg
    content_hash: 6843b53e… => abeeb0b9…  # forces replacement
    # old object (id 1234567890) is left in place, no longer managed

Plan: 0 to import, 1 to add, 0 to change, 0 to destroy.
```

A replace counts as one add, plus one destroy when the old object is destroyed (as in Terraform).
In `--json` its `action` is `"replace"`, with a `forces_replacement` list, the superseded `old_id`
and `abandons_old: true` when the old object is left in place; `result.summary.replace` counts
replaces, and `apply` prints the ids it left in place. Apply creates the new object first, writes
its id to state, then handles the old one, so a failed upload leaves the current object untouched.
Resources that reference the replaced one are planned with `(known after apply)` values and updated
in the same apply. `lifecycle { prevent_destroy = true }` blocks a replace. An `import {}` whose
declared attributes force a replace shows `will be imported (id …), then replaced`. Today only
`roblox_asset` replaces: when a decal, audio or video file's content changes (images and models are
updated in place, keeping their id), or when its `asset_type` changes. Forcing a replace on demand
will come with a general `lifecycle { replace_triggered_by = [...] }`.

When a file a resource hashes (an asset's `file`, a place's `file`, a badge's `icon_file`) is
missing on the machine running `plan`, for example in CI without the source files, a resource
already in state keeps its recorded hash, so nothing is planned for its content, and `plan` prints
a `Warning:` that content changes can't be detected. Creating a resource still needs the file.

[`moved` blocks](config-reference.md#moved) are applied to the state read by `plan` (in memory
only; `plan` never writes state) before the diff. Each move is listed before the changes, and the
footer counts them when there are any:

```console
$ baseplate plan
~ roblox_asset.sprites["ui/old.png"] has moved to roblox_asset.sprites["ui/new.png"]

Plan: 0 to import, 0 to add, 0 to change, 0 to destroy, 1 to move.
```

In `--json`, the result has a `moves` array (`[{"from": "...", "to": "..."}]`, omitted when empty)
and `summary.move`. A plan whose only change is a move exits `2`. `--target` doesn't filter moves.

When one resource both destroys and creates instances in the same plan (what a renamed `for_each`
key looks like), `plan` prints a `Warning:` that the created instances are new objects with new ids,
with an example `moved` block that keeps the existing object instead.
When a `for_each` resource declares zero instances while state still holds some, it warns that all
of them will be destroyed. `--json` lists both under `result.warnings`. See
[managing many assets](how-to/many-assets.md#renaming-or-moving-a-file).

`--generate-config=FILE` writes resource blocks for `import {}` blocks whose target isn't declared.
It doesn't support `for_each` targets; declare those by hand.

### `apply`

Reconcile resources to the config, creating/updating as needed and writing state. Takes an advisory
lock on the state file for the duration, so a concurrent `apply` can't clobber it. Applies `moved {}`
blocks to state first (written even if nothing else changes), then resolves `import {}` blocks
(adopt + reconcile in one pass) before applying. `--json` adds a `moves` array like `plan`'s.

```console
$ baseplate apply
Apply complete. 1 change(s).
```

On a terminal it prompts before mutating; pass `--auto-approve` (or `--yes`) to skip the prompt in
scripts. CI (no TTY) skips it automatically.

| Flag | Default | Description |
|---|---|---|
| `--restart-servers` | `false` | After a **successful** apply that published at least one `roblox_place` (a create or update), restart the game servers running an older version of those places so players move onto the new version. Nothing is restarted if the apply fails or publishes no place. |
| `--restart-bleed-off` | *(immediate)* | With `--restart-servers`: stop matchmaking into the old servers and shut them down after this long (whole minutes, `1m`–`60m`). Requires `--restart-servers`. |

With `--restart-servers`, the confirmation prompt lists the places whose servers will be restarted
and how. The restart is one request per universe, scoped to the published place ids, taken from
state after the apply (so a `universe_id` that references `roblox_experience.main.id` works).
The flag is checked before anything is applied: without `ROBLOX_API_KEY` or with an invalid bleed-off
the command fails up front. If the apply succeeds but the restart request fails, the command exits
`1` with `apply succeeded, but restarting servers failed: …` (state is already written). `--json`
adds a `restarts` array to the apply result (same shape as the `restart-servers` result below).

### `destroy`

Remove everything the state manages. Equivalent to applying an empty config: every managed resource
is planned for deletion, in reverse-dependency order. Takes the state lock.

Some Roblox resources have no delete path, so `destroy` **unmanages them in place** (drops them from
state, leaves them on the platform) rather than deleting: `roblox_asset` (image/decal),
`roblox_developer_product` (set to not-for-sale), `roblox_place` (can't unpublish).
`roblox_universe_secret` is really deleted.

```console
$ baseplate destroy
Apply complete. 1 change(s).
```

On a terminal it prompts before mutating; pass `--auto-approve` (or `--yes`) to skip the prompt in
scripts. CI (no TTY) skips it automatically.

`destroy` reads the config file (when it exists) for
[`lifecycle { prevent_destroy = true }`](config-reference.md#lifecycle). If any resource in the plan
has it set, `destroy` fails with `Instance cannot be destroyed: ...` before deleting anything. Use
`--target` to destroy other resources, or remove the setting first. Because the setting is read from
config, an invalid config file fails `destroy`, and a missing config file means nothing is
protected.

### `import <address> <id>`

Adopt an existing resource into state without recreating it, so a subsequent `plan`/`apply`
reconciles rather than creating a duplicate. The resource must already be **declared in the config**.
Reads the live resource and writes a state entry under the state lock; it never mutates the remote.
Like `plan` and `apply`, it applies the config's [`moved` blocks](config-reference.md#moved) to state
first, and writes them along with the imported entry.

```console
$ baseplate import roblox_experience.main 10438196190
$ baseplate import 'roblox_asset.sprites["ui/gem.png"]' 1234567890
```

For a `for_each` resource, import one instance at a time using its [address](#addresses). Errors if
the address isn't declared in the config, is already in state, or can't be found on the platform, or
if another address in state already manages that id (one remote object is only ever managed at one
address; otherwise destroying one address would delete the object the other still manages). For a config-driven alternative that runs during `apply`, use an
[`import {}` block](getting-started.md#6-adopting-existing-resources-import).

### `restart-servers`

Restart an experience's live game servers so players move onto the latest published place version.
This is an imperative action, not a resource: it changes no state and needs no state file. It uses
the stable Open Cloud `POST /cloud/v2/universes/{id}:restartServers` endpoint and needs the
`universe:write` scope.

**This disconnects live players.** The prompt says exactly what will happen before anything is sent.

| Flag | Default | Description |
|---|---|---|
| `--universe` | *(from config)* | Universe id. If omitted, it is taken from the config's `roblox_experience` `universe_id`; this must be unambiguous (exactly one distinct literal id), otherwise pass `--universe`. |
| `--place` | *(all places)* | Restrict the restart to this place id. Repeatable (`--place 1 --place 2`) or comma-separated. |
| `--all-versions` | `false` | Restart servers on **every** version, including ones already on the latest. By default only servers running an older version than the latest published one are restarted, and if nothing newer has been published, nothing is restarted. |
| `--bleed-off` | *(immediate)* | Stop matchmaking new players into the affected servers now, then shut them down after this duration. Whole minutes, `1m`–`60m`. Without it, servers shut down immediately. |
| `--auto-approve` / `--yes` | `false` | Skip the prompt (see [Approval flags](#approval-flags-apply--destroy--restart-servers)). |

```console
$ baseplate restart-servers --bleed-off 5m
Baseplate will restart live game servers in universe 10438196190:
  places:   every place in the universe
  servers:  only servers running an older place version than the latest published one
            (if nothing newer has been published, no servers are restarted)
  shutdown: after a 5-minute bleed-off: no new players are matched into these servers
            from now on, and any players still in them after 5 minute(s) are removed

Do you want to restart these servers? Only 'yes' will be accepted to confirm.
Enter a value: yes
Restart requested for universe 10438196190. Roblox restarts the servers asynchronously.
```

Roblox accepts the request and restarts servers asynchronously; it reports no progress, so the
command returns as soon as the request is accepted. Exit codes: `0` when the request was accepted,
`1` on error or a declined prompt. `--json` result:

```json
{
  "format_version": 1,
  "command": "restart-servers",
  "result": { "universe_id": "10438196190", "place_ids": [], "all_versions": false, "bleed_off_minutes": 5 }
}
```

`place_ids` is empty when every place is targeted. An unknown `--place` fails with
`Roblox API 404: The desired Place was not found.`

### `show [address]`

Print what the state manages. Reads state through the configured backend (local, or the `backend`
block in `--config`) without taking the lock, makes no provider calls, and needs no
`ROBLOX_API_KEY`.

```console
$ baseplate show
roblox_asset.sprites["ui/coin.png"]  1234567891
roblox_asset.sprites["ui/gem.png"]   1234567890
roblox_experience.main               10438196190
```

With an [address](#addresses), it prints that resource's (or each matching instance's) attributes:

```console
$ baseplate show 'roblox_asset.sprites["ui/gem.png"]'
roblox_asset.sprites["ui/gem.png"]
    id           = 1234567890
    asset_type   = Image
    description  = Game sprite
    display_name = sprite/ui/gem
```

`--json` returns the inventory sorted by address. `key` is present only for `for_each` instances,
and `attributes` are plain JSON values. Sensitive attributes are never included.

```json
{
  "format_version": 1,
  "command": "show",
  "result": {
    "serial": 42,
    "lineage": "…",
    "resources": [
      {
        "address": "roblox_asset.sprites[\"ui/gem.png\"]",
        "type": "roblox_asset",
        "name": "sprites",
        "key": "ui/gem.png",
        "id": "1234567890",
        "attributes": { "asset_type": "Image", "display_name": "sprite/ui/gem" }
      }
    ]
  }
}
```

This is the stable way to feed deployed ids into other tooling; see
[managing many assets](how-to/many-assets.md#3-read-the-ids-back-show---json) for a Luau generator.
An empty or missing state prints `No resources in state.` (`"resources": []` in JSON). Showing an
address that isn't in state is an error.

## See also

- [Config reference](config-reference.md)

- [Getting started](getting-started.md)
- [Resource reference](resource-reference.md)
