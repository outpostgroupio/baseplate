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
| `--target` | *(none)* | Limit the operation to a single resource address (e.g. `roblox_asset.icon`). |
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
(e.g. a secret's `value`) are redacted in both formats.

### `apply`

Reconcile resources to the config, creating/updating as needed and writing state. Takes an advisory
lock on the state file for the duration, so a concurrent `apply` can't clobber it. Resolves
`import {}` blocks (adopt + reconcile in one pass) before applying.

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

### `import <address> <id>`

Adopt an existing resource into state without recreating it, so a subsequent `plan`/`apply`
reconciles rather than creating a duplicate. The resource must already be **declared in the config**.
Reads the live resource and writes a state entry under the state lock; it never mutates the remote.

```console
$ baseplate import roblox_experience.main 10438196190
```

Errors if the address isn't declared in the config, is already in state, or can't be found on the
platform. For a config-driven alternative that runs during `apply`, use an
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

## See also

- [Getting started](getting-started.md)
- [Resource reference](resource-reference.md)
