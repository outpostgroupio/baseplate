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

### Approval flags (`apply` / `destroy`)

| Flag | Default | Description |
|---|---|---|
| `--auto-approve` | `false` | Skip the interactive approval prompt and apply immediately. |
| `--yes` | `false` | Alias for `--auto-approve`. |

`apply` and `destroy` render the plan and then, on an interactive terminal, prompt for
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

The `2` code lets CI branch on "is there drift?" without parsing output. `apply`/`destroy` return
`1` both on error and when a confirmation prompt is declined.

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

## See also

- [Getting started](getting-started.md)
- [Resource reference](resource-reference.md)
