# State server protocol

This is what a server must implement to store Baseplate state with `backend "http"`, sign users in
with `baseplate login`, and accept GitHub Actions tokens from CI. The
[reference Cloudflare Worker](../workers/state) implements all of it, and is the easiest starting
point for your own: to sign users in against something other than GitHub, replace its identity
provider (`workers/state/src/github.ts`) and keep the rest.

There are three parts:

1. [State and locking](#state-and-locking): the Terraform/OpenTofu `http` backend protocol. This is
   the only required part.
2. [Authentication](#authentication): how Baseplate sends credentials.
3. [Discovery and sign-in](#discovery-and-sign-in): `/.well-known/baseplate.json`, `login.v1`
   (`baseplate login`) and `oidc.v1` (GitHub Actions).

## State and locking

The config points `address` at one state, and `lock_address` (usually the same URL) at its lock:

```hcl
backend "http" {
  address      = "https://state.example.com/states/staging"
  lock_address = "https://state.example.com/states/staging"
}
```

| Request | Meaning | Responses |
|---|---|---|
| `GET <address>` | Read the state. | `200` with the state document; `204` or `404` when there is none yet. |
| `POST <address>[?ID=<lock id>]` | Replace the state with the request body (JSON, `Content-Type: application/json`). Baseplate sends `?ID=` while it holds the lock. | `2xx` on success. `409` when the state is locked and `ID` isn't the held lock's. |
| `LOCK <lock_address>` | Take the lock. The body is the lock (below). | `200` when taken. `423` (or `409`) when already locked, with the held lock as the body. |
| `UNLOCK <lock_address>` | Release the lock. The body carries the lock's `ID`. | `200` when released, or when nothing is locked. `409` (or `423`) when a different lock is held, with that lock as the body. |

Clients can change the methods with `update_method`, `lock_method` and `unlock_method` (GitLab, for
example, uses `POST` and `DELETE` on `<state>/lock`), so accept those too if you can.

The lock body is a JSON object with capitalised keys, as Terraform sends it:

```json
{
  "ID": "5c1a3e0e-1e1b-4a5e-9e5a-0c3b9d1f2a77",
  "Operation": "apply",
  "Who": "josh@laptop",
  "Version": "0.1.0",
  "Created": "2026-11-23T19:25:36Z",
  "Path": "https://state.example.com/states/staging",
  "Info": ""
}
```

Only `ID` is required. `Operation` is the Baseplate command (`plan`, `apply`, `destroy` or
`import`). When a lock is refused, Baseplate shows the held lock's `Who`, `ID`, `Operation` and
`Created` to the user, so servers should return the stored lock as it was sent. They may add to
`Who`, for example the authenticated identity, as the reference Worker does
(`"josh@laptop (github:josh)"`).

What a server must guarantee:

- **One holder.** Two concurrent `LOCK` requests must not both succeed. Use a store with atomic
  compare-and-set (a Durable Object, a database row, a conditional write).
- **Writes respect the lock.** While locked, a `POST` must carry the held lock's `ID`, checked and
  written in one step.
- **Whole writes.** Store each `POST` body as one object. A reader must never see a partial state.
- **Locks don't expire.** A lock stays until it is released. `baseplate force-unlock <id>` sends
  `UNLOCK` with only the `ID` to clear a lock left by a run that died. Releasing must require the
  matching `ID`.

Baseplate never sends secret values from `roblox_universe_secret` in the state, but the state holds
everything else read from Roblox and any value read with `env()`. Treat it as confidential.

## Authentication

Baseplate sends credentials on every request:

- With `username` and `password` in the backend block: HTTP Basic auth.
- Otherwise, when it has a token: `Authorization: Bearer <token>`. It looks for one in this order:
  1. `BASEPLATE_STATE_TOKEN`;
  2. a GitHub Actions OIDC token, when running in Actions with `id-token: write` and the server
     advertises [`oidc.v1`](#oidcv1-github-actions);
  3. the token `baseplate login` stored for the server's host.

Reply `401` to a missing or invalid credential. Baseplate tells the user to sign in.

The token's format is up to the server. The reference Worker's tokens are opaque and start with
`bp1.`. It also accepts its tokens as the Basic auth password, so Terraform and OpenTofu clients
can use them.

## Discovery and sign-in

`GET /.well-known/baseplate.json` on the server's origin describes what it supports. Leave out what
you don't support. A `404` means neither.

```json
{
  "login.v1": {
    "client": "baseplate-cli",
    "grant_types": ["authz_code"],
    "authz": "/oauth/authorize",
    "token": "/oauth/token",
    "ports": [10000, 10010]
  },
  "oidc.v1": {
    "issuers": ["https://token.actions.githubusercontent.com"]
  }
}
```

### `login.v1`: `baseplate login`

This is the same OAuth 2.0 authorization code flow with PKCE that Terraform's `login.v1` uses, with
the same fields. `authz` and `token` are URLs, resolved against the server's origin. `token` must be
on the same origin. `ports` is the inclusive range of local ports the CLI may listen on.

1. The user runs `baseplate login [host]`. Without a host, it uses the host of the config's backend
   `address`.
2. The CLI listens on the first free port `P` in `ports` and opens the browser at `authz` with:

   | Parameter | Value |
   |---|---|
   | `response_type` | `code` |
   | `client_id` | the `client` from discovery |
   | `redirect_uri` | `http://127.0.0.1:P/login` |
   | `state` | random |
   | `code_challenge` | `BASE64URL(SHA256(code_verifier))` |
   | `code_challenge_method` | `S256` |

3. The server signs the user in however it likes, then redirects the browser to `redirect_uri`
   with `code` and the unchanged `state`. On failure it redirects with `error` (and optionally
   `error_description`) and `state`.
4. The CLI checks `state` and posts a form to `token`: `grant_type=authorization_code`,
   `client_id`, `code`, `code_verifier` and `redirect_uri`.
5. The server checks the code, that `redirect_uri` matches, and that
   `BASE64URL(SHA256(code_verifier))` equals the `code_challenge`. It replies `200` with
   `{"access_token": "...", "token_type": "bearer"}`, or `400` with `{"error": "invalid_grant"}`.

The CLI stores the token per host in `credentials.json` in the user's config directory
(`~/Library/Application Support/baseplate` on macOS, `~/.config/baseplate` on Linux,
`%AppData%\baseplate` on Windows), mode `0600`. `BASEPLATE_CREDENTIALS_FILE` overrides the path.
`baseplate logout` deletes the stored token. It doesn't contact the server, so if your tokens can be
revoked, revoke them through your own tooling.

Servers must:

- only accept a `redirect_uri` of `http://127.0.0.1:<port>/...` or `http://localhost:<port>/...`
  with the port in the advertised range;
- make codes short-lived (a minute is plenty), and accept a code only with its matching
  `code_verifier`;
- check on each request that the token's user is still allowed, if access can be withdrawn (the
  reference Worker re-checks GitHub org membership, cached for a minute).

### `oidc.v1`: GitHub Actions

A server that advertises `oidc.v1` accepts GitHub Actions OIDC tokens as bearer tokens. In a
workflow with `permissions: id-token: write`, Baseplate requests a token whose audience is the
server's origin (for example `https://state.example.com`), and sends it. The audience is always the
origin, so a token Baseplate fetched for one server can't be replayed against another.

To accept one, verify:

- the RS256 signature, against the keys at
  `https://token.actions.githubusercontent.com/.well-known/jwks`;
- `iss` is `https://token.actions.githubusercontent.com`;
- `aud` is your origin;
- `exp` and `nbf`, allowing a minute of clock skew;
- `repository` (or `repository_owner`, `ref`, `environment`, as your policy needs) is one you allow.

Tokens from pull requests from forks are never issued, since those workflows can't get
`id-token: write`.
