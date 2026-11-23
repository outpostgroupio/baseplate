# State

Baseplate records what it manages in a state file: each resource's address, its remote id, and the
attribute values from the last apply. `plan` compares your config with this state and with what
Roblox reports.

## Local state

By default state is a local file, `baseplate.bpstate`, which you can change with `--state`.
`plan`, `apply`, `destroy` and `import` take an advisory lock on it (`baseplate.bpstate.lock`) so
two runs can't use it at once. `show` doesn't lock. `plan --lock=false` skips the lock, as in
Terraform. The operating system releases the lock when the process exits, so a local lock never
gets stuck.

Don't commit the state file. It holds ids and attribute values, including any value you passed in
with `env()` (secret values from `roblox_universe_secret` are the exception and are never stored).

## Remote state (HTTP backend)

To share state across a team or CI, add a `backend "http"` block to the config. It speaks the same
protocol as the Terraform/OpenTofu `http` backend.

The simplest option is the reference state server for Cloudflare, which you deploy to your own
account. It keeps state in R2, and people sign in with GitHub, so no one needs storage credentials.
See [Remote state on Cloudflare](how-to/remote-state-cloudflare.md). To write your own server, see
[State server protocol](state-server-protocol.md).

Hosts such as GitLab's Terraform state API work too:

```hcl
backend "http" {
  address        = "https://gitlab.com/api/v4/projects/<id>/terraform/state/prod"
  lock_address   = "https://gitlab.com/api/v4/projects/<id>/terraform/state/prod/lock"
  unlock_address = "https://gitlab.com/api/v4/projects/<id>/terraform/state/prod/lock"
  lock_method    = "POST"
  unlock_method  = "DELETE"
  username       = "baseplate-ci"
  password       = env("STATE_PASSWORD")
}
```

- `address` is required. State is read with `GET` and written with `POST`.
- Locking is enabled when `lock_address` is set. The default methods are `LOCK` and `UNLOCK`;
  GitLab needs `lock_method = "POST"` and `unlock_method = "DELETE"`.
- `username` and `password` are sent as HTTP Basic auth. Without them, Baseplate sends a bearer
  token when it has one: `BASEPLATE_STATE_TOKEN`, then a GitHub Actions OIDC token (in a workflow
  with `id-token: write`, for servers that accept them), then the token stored by
  `baseplate login`. See [Authentication](state-server-protocol.md#authentication).
- Addresses must use `https://`. Plain `http://` is only accepted for `localhost`, `127.0.0.1`
  and `::1`.

Commands that read state find the backend through `--config`. If you run `destroy` with a remote
backend, pass `--config` too; otherwise it falls back to local state and prints a note. `--state`
is refused when the config has a backend block, since the backend holds the state.

### Locks

With `lock_address` set, `plan`, `apply`, `destroy` and `import` take the lock, and a second run
fails with the holder's name, the operation, when it started and the lock id. A remote lock outlives
a run that dies (a cancelled CI job, say). Once you're sure that run has stopped, release it:

```console
$ baseplate force-unlock 5c1a3e0e-1e1b-4a5e-9e5a-0c3b9d1f2a77
```

## Versioning

The state file has a `version` field. This release writes version 2.

| Version | Change |
|---|---|
| 1 | Initial format. |
| 2 | Resources can carry an `index_key`, the key of a `for_each` instance. Version 1 files are upgraded when read. |

Baseplate refuses a state file written by a newer version, so an old binary can't damage it.
Upgrade Baseplate instead. Future format changes will come with an automatic upgrade on read. Both
backends store the same document.
