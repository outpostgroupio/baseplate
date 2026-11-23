# State

Baseplate records what it manages in a state file: each resource's address, its remote id, and the
attribute values from the last apply. `plan` compares your config with this state and with what
Roblox reports.

## Local state

By default state is a local file, `baseplate.bpstate`, which you can change with `--state`.
`apply`, `destroy` and `import` take an advisory lock on it (`baseplate.bpstate.lock`) so two
runs can't write at once. `plan` and `show` don't lock.

Don't commit the state file. It holds ids and attribute values, including any value you passed in
with `env()` (secret values from `roblox_universe_secret` are the exception and are never stored).

## Remote state (HTTP backend)

To share state across a team or CI, add a `backend "http"` block to the config. It speaks the same
protocol as the Terraform/OpenTofu `http` backend, so hosts such as GitLab's Terraform state API
work:

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
- `username` and `password` are sent as HTTP Basic auth.
- Addresses must use `https://`. Plain `http://` is only accepted for `localhost`, `127.0.0.1`
  and `::1`.

Commands that read state find the backend through `--config`. If you run `destroy` with a remote
backend, pass `--config` too; otherwise it falls back to local state and prints a note.

## Versioning

The state file has a `version` field. This release writes version 2.

| Version | Change |
|---|---|
| 1 | Initial format. |
| 2 | Resources can carry an `index_key`, the key of a `for_each` instance. Version 1 files are upgraded when read. |

Baseplate refuses a state file written by a newer version, so an old binary can't damage it.
Upgrade Baseplate instead. Future format changes will come with an automatic upgrade on read. Both
backends store the same document.
