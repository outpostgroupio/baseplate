# CLI reference

```
baseplate <command> [flags]
```

## Global flags

| Flag | Default | Description |
|---|---|---|
| `--config` | `baseplate.bp.hcl` | Path to the HCL config file. |
| `--state` | `baseplate.bpstate` | Path to the state file. |
| `--target` | *(none)* | Limit `plan`/`apply`/`destroy` to one resource (`roblox_asset.icon`; for a `for_each` resource, every instance) or one instance (`'roblox_asset.sprites["ui/gem.png"]'`). See [Addresses](#addresses). A malformed address is an error. |
| `--json` | `false` | Emit machine-readable output. Works on every command. Results are a versioned envelope `{ "format_version": 1, "command": ..., "result": ... }` on stdout; errors are `{ "format_version": 1, "command": ..., "error": { "message": ... } }` on stderr. |

State can also be stored remotely with a `backend "http"` block; see [State](state.md). With one,
`--state` makes `plan`, `import` and `show` use that local file instead, without touching the
backend, and `apply` and `destroy` refuse it.

### Approval flags (`apply` / `destroy` / `restart-servers`)

| Flag | Default | Description |
|---|---|---|
| `--auto-approve` | `false` | Skip the interactive approval prompt and apply immediately. |
| `--yes` | `false` | Alias for `--auto-approve`. |

`apply` and `destroy` render the plan (`restart-servers` describes the restart) and then, on an interactive terminal, prompt for
confirmation. Only `yes` proceeds. In a non-interactive session (no TTY, e.g. CI) the prompt is
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
$ baseplate import 'roblox_asset.sprites["ui/gem.png"]' 2233445566
```

An unquoted key (`roblox_asset.sprites[ui/gem.png]`) is rejected with
`invalid address "...": key must be a quoted string`. `plan` and `show` print addresses in exactly
this form, so you can copy them.

## Environment

`BASEPLATE_STATE_TOKEN`, when set, is sent as the bearer token to a remote state server, ahead of
a GitHub Actions OIDC token or the token stored by `login`. `BASEPLATE_CREDENTIALS_FILE` overrides
where `login` stores tokens.

`ROBLOX_API_KEY` is the Open Cloud API key. It's needed to manage `roblox_*` resources; read it in
the config with `env("ROBLOX_API_KEY")`. `validate` and `show` work without it. `plan`, `apply`,
`destroy` and `import` fail with `ROBLOX_API_KEY is not set` when the config has `roblox_*`
resources and the key is missing.

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
type, or has an invalid attribute. Does not contact Roblox or read state, and doesn't need
`ROBLOX_API_KEY`, so CI can run it on pull requests from forks.

```console
$ baseplate validate
Configuration is valid.
```

### `plan`

Show the changes `apply` would make, as a colorized diff. Read-only: it reads state and queries the
provider but mutates nothing. It holds the state lock while it runs, so it never reads state that
an `apply` is rewriting; `--lock=false` skips the lock. Exits `2` if there are changes, `0` if
converged.

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

Each change is a create (`+`), update (`~`), delete (`-`) or replace (`-/+`). An update lists only
the attributes and triggers that change, as `old => new`, and counts the declared attributes it
leaves alone:

```console
~ roblox_experience.main (update)
    vr_enabled: true => false
    # (4 unchanged attribute(s) hidden)
```

A create lists every declared attribute, and a delete every recorded one. A replace happens
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
    # old object (id 2233445566) is left in place, no longer managed

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
Roblox's text filter replaces each character of a word it rejects with `#`. When a name or
description reads back as the config's text with only that change, `plan` treats it as unchanged,
since writing it again would be filtered the same way, and prints a `Warning:` with the filtered
text.
Resources can add warnings of their own, about something that didn't stop the plan (for example a
`roblox_asset` Decal whose `texture_id` lookup failed). They're printed and listed the same way.
`apply` prints those reported while it runs after `Apply complete:`, and `apply --json` and
`import --json` list them under `result.warnings`.

`--generate-config=FILE` writes resource blocks for `import {}` blocks whose target isn't declared.
It doesn't support `for_each` targets; declare those by hand.

### `apply`

Reconcile resources to the config, creating/updating as needed and writing state. Takes an advisory
lock on the state file for the duration, so a concurrent `apply` can't clobber it. Applies `moved {}`
blocks to state first (written even if nothing else changes), then resolves `import {}` blocks
(adopt + reconcile in one pass) before applying. `--json` adds a `moves` array like `plan`'s.

```console
$ baseplate apply
Apply complete: 0 imported, 1 added, 0 changed, 0 destroyed.
```

The counts match the plan's `Plan:` line; resources that were already up to date aren't counted.
`--json` gives the same counts as `result.summary`, and the number of resources that changed as
`result.applied`.
`result.changes` lists every declared resource, including unchanged ones (`"action": "noop"`).

On a terminal it prompts before mutating; pass `--auto-approve` (or `--yes`) to skip the prompt in
scripts. CI (no TTY) skips it automatically.

| Flag | Default | Description |
|---|---|---|
| `--restart-servers` | `false` | After a successful apply that published at least one `roblox_place` (a create or update), restart the game servers running an older version of those places so players move onto the new version. Nothing is restarted if the apply fails or publishes no place. |
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

Some Roblox resources can't be deleted through Open Cloud. For those, `destroy` drops them from
state and leaves them on Roblox, after disabling them where possible: developer products and game
passes are taken off sale, badges are disabled, and published places and Image or Model assets are
left as they are. Secrets, experience events and analytics alerts are deleted. The
[resource reference](resource-reference.md) describes each type.

```console
$ baseplate destroy
Destroy complete: 0 imported, 0 added, 0 changed, 1 destroyed.
```

On a terminal it prompts before mutating; pass `--auto-approve` (or `--yes`) to skip the prompt in
scripts. CI (no TTY) skips it automatically.

`destroy` reads the config file (when it exists) for
[`lifecycle { prevent_destroy = true }`](config-reference.md#lifecycle). If any resource in the plan
has it set, `destroy` fails with `Instance cannot be destroyed: ...` before deleting anything. Use
`--target` to destroy other resources, or remove the setting first. Because the setting is read from
config, an invalid config file fails `destroy`, and a missing config file means nothing is
protected.

### `import [<address> <id>]`

Adopt an existing resource into state without recreating it, so a subsequent `plan`/`apply`
reconciles rather than creating a duplicate. The resource must already be declared in the config.
Reads the live resource and writes a state entry under the state lock; it never mutates the remote.
Like `plan` and `apply`, it applies the config's [`moved` blocks](config-reference.md#moved) to state
first, and writes them along with the imported entry.

```console
$ baseplate import roblox_experience.main 1234567890
$ baseplate import 'roblox_asset.sprites["ui/gem.png"]' 2233445566
```

For a `for_each` resource, import one instance at a time using its [address](#addresses). Errors if
the address isn't declared in the config, is already in state, or can't be found on the platform, or
if another address in state already manages that id (one remote object is only ever managed at one
address; otherwise destroying one address would delete the object the other still manages). For a
config-driven alternative that runs during `apply`, use an
[`import` block](how-to/importing-existing-resources.md). Import ids for each resource type are listed
there too.

With no arguments, `import` runs every `import {}` block in the config whose target isn't in state
yet, and nothing else: it never changes Roblox, and it writes state once, only if every block
succeeds. Blocks whose target is already in state are skipped. Use it to adopt a live universe
without an `apply`; the next `plan` then shows only real differences.

```console
$ baseplate import
Imported 12 resource(s):
  roblox_developer_product.products["gems_100"] (id 1234567890/2233445566)
  ...
```

`--json` returns `{"imported": [{"address": ..., "id": ...}], "warnings": [...]}`.

Some content can't be read back from Roblox, so an import can't tell whether the live object
matches your config. Today that's a `roblox_place`'s `file`. Rather than recording the local
file's hash as if it were live, the import marks the content as unverified and warns, and the next
`plan` shows an update (a publish) with `content_hash: (unverified: imported) => …`. If you know the
live place already matches the file, pass `--assume-current` to record the file's hash instead.
An `import {}` block run by `apply` shows `will be imported (id …), then updated` and publishes in
the same apply.

| Flag | Default | Description |
|---|---|---|
| `--assume-current` | `false` | Record content that can't be read back (a place's `file`) as already matching the config, so the next plan doesn't publish it. |

### `force-unlock <id>`

Release a remote state lock left behind by a run that is no longer active, such as a cancelled CI
job. The id is in the error a blocked command prints. Releasing the lock of a run that is still
going lets two runs write state at once, so check first. Prompts for confirmation on a terminal;
`--force` skips the prompt.

```console
$ baseplate force-unlock 5c1a3e0e-1e1b-4a5e-9e5a-0c3b9d1f2a77
Released the state lock 5c1a3e0e-1e1b-4a5e-9e5a-0c3b9d1f2a77.
```

The server only releases the lock if the id matches. Local state needs no force-unlock: the
operating system releases its lock when the process exits.

### `login [host]` / `logout [host]`

Sign in to a remote state server in your browser, and store the token it issues. Without a host,
`login` uses the host of the config's `backend "http"` address. The server must support sign-in
(see [State server protocol](state-server-protocol.md#loginv1-baseplate-login)), as the
[Cloudflare state server](how-to/remote-state-cloudflare.md) does.

```console
$ baseplate login
Opening your browser to sign in to state.example.com. If it doesn't open, visit:
...
Signed in to state.example.com. The token is stored in ~/.config/baseplate/credentials.json.
```

The browser hands the sign-in back to a port on `127.0.0.1`, so it must run on the same machine.
Where there's no browser (over SSH, say), set `BASEPLATE_STATE_TOKEN` instead. `logout` deletes
the stored token.

### `restart-servers`

Restart an experience's live game servers so players move onto the latest published place version.
This is an imperative action, not a resource: it changes no state and needs no state file. It uses
the stable Open Cloud `POST /cloud/v2/universes/{id}:restartServers` endpoint and needs the
`universe:write` scope.

This disconnects live players. The prompt says exactly what will happen before anything is sent.

| Flag | Default | Description |
|---|---|---|
| `--universe` | *(from config)* | Universe id. If omitted, it is taken from the config's `roblox_experience` `universe_id`; this must be unambiguous (exactly one distinct literal id), otherwise pass `--universe`. |
| `--place` | *(all places)* | Restrict the restart to this place id. Repeatable (`--place 1 --place 2`) or comma-separated. |
| `--all-versions` | `false` | Restart servers on every version, including ones already on the latest. By default only servers running an older version than the latest published one are restarted, and if nothing newer has been published, nothing is restarted. |
| `--bleed-off` | *(immediate)* | Stop matchmaking new players into the affected servers now, then shut them down after this duration. Whole minutes, `1m`–`60m`. Without it, servers shut down immediately. |
| `--auto-approve` / `--yes` | `false` | Skip the prompt (see [Approval flags](#approval-flags-apply--destroy--restart-servers)). |

```console
$ baseplate restart-servers --bleed-off 5m
Baseplate will restart live game servers in universe 1234567890:
  places:   every place in the universe
  servers:  only servers running an older place version than the latest published one
            (if nothing newer has been published, no servers are restarted)
  shutdown: after a 5-minute bleed-off: no new players are matched into these servers
            from now on, and any players still in them after 5 minute(s) are removed

Do you want to restart these servers? Only 'yes' will be accepted to confirm.
Enter a value: yes
Restart requested for universe 1234567890. Roblox restarts the servers asynchronously.
```

Roblox accepts the request and restarts servers asynchronously; it reports no progress, so the
command returns as soon as the request is accepted. Exit codes: `0` when the request was accepted,
`1` on error or a declined prompt. `--json` result:

```json
{
  "format_version": 1,
  "command": "restart-servers",
  "result": { "universe_id": "1234567890", "place_ids": [], "all_versions": false, "bleed_off_minutes": 5 }
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
roblox_asset.sprites["ui/coin.png"]  2233445567
roblox_asset.sprites["ui/gem.png"]   2233445566
roblox_experience.main               1234567890
```

With an [address](#addresses), it prints that resource's (or each matching instance's) attributes:

```console
$ baseplate show 'roblox_asset.sprites["ui/gem.png"]'
roblox_asset.sprites["ui/gem.png"]
    id           = 2233445566
    asset_type   = Image
    description  = Game sprite
    display_name = sprite/ui/gem
    texture_id   = 2233445566
```

`--json` returns the inventory sorted by address. `key` is present only for `for_each` instances,
and `attributes` are plain JSON values. Sensitive attributes are never included. `triggers` holds
the values Baseplate records to detect changes it can't read back from Roblox, such as a file's
`content_hash`; it's left out for resources that have none.

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
        "id": "2233445566",
        "attributes": { "asset_type": "Image", "display_name": "sprite/ui/gem", "texture_id": "2233445566" },
        "triggers": { "content_hash": "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08" }
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
- [Resource reference](resource-reference.md)
