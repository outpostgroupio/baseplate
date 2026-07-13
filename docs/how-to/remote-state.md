# Remote state (HTTP backend)

By default Baseplate stores state in a local file (`--state`, default `baseplate.bpstate`). To share
state across a team or CI, configure a remote **HTTP backend** — Baseplate speaks the same protocol as
the OpenTofu/Terraform `http` backend, so managed-state hosts like GitLab's Terraform state API work
directly.

Add a `backend` block to your config:

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

- **`address`** (required) — the state endpoint. `GET` fetches, `POST` (default) persists.
- **Locking** — set `lock_address` to enable state locking. If omitted, locking is disabled. The
  default lock/unlock methods are `LOCK`/`UNLOCK`; **GitLab-style hosts require `lock_method = "POST"`
  and `unlock_method = "DELETE"`**.
- **Auth** — `username`/`password` send HTTP Basic auth (use `env()` for the secret).

Without a `backend` block, Baseplate uses the local file backend (unchanged). Note: with a remote
backend, `destroy` needs `--config` so it can find the backend — otherwise it falls back to local
state and prints a note.

S3 and Cloudflare R2 backends are planned on the same pluggable seam.
