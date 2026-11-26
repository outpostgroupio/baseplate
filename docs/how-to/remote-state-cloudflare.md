# Remote state on Cloudflare

To share state across a team and CI, deploy the reference state server in
[`workers/state`](../../workers/state) to your own Cloudflare account. It's a Worker that:

- stores state in an R2 bucket, with a copy of every version under `history/`;
- holds each state's lock in a Durable Object, so two runs can never both hold it;
- signs people in with GitHub (`baseplate login`), limited to members of an org or team;
- lets GitHub Actions workflows in the repositories you list use it with no stored secret.

No one needs R2 or Cloudflare credentials to use it. Only whoever deploys it does.

Everything here fits in Cloudflare's free tier for a normal team.

## 1. Create the bucket

You need Node.js 20 or later. From a clone of the Baseplate repository:

```console
$ cd workers/state
$ npm ci
$ npx wrangler login
$ npx wrangler r2 bucket create baseplate-state
```

To use another bucket name, change `bucket_name` in `wrangler.jsonc`. The Worker reaches the bucket
through a binding, so there's no R2 API token to create or share.

## 2. Create a GitHub OAuth app

In GitHub, go to your org's **Settings → Developer settings → OAuth Apps → New OAuth App**. Use
your Worker's URL. You'll see it after the first deploy; by default it's
`https://baseplate-state.<your-subdomain>.workers.dev`.

| Field | Value |
|---|---|
| Homepage URL | `https://baseplate-state.<your-subdomain>.workers.dev` |
| Authorization callback URL | `https://baseplate-state.<your-subdomain>.workers.dev/oauth/callback` |

Generate a client secret. The app asks users for the `read:org` scope, which lets the Worker check
their org membership.

If your org restricts OAuth app access, approve the app in the org's settings
(**Third-party access → OAuth app policy**).

## 3. Configure and deploy

Set who may sign in, and which repositories' workflows may use it, in `wrangler.jsonc`:

```jsonc
"vars": {
  "GITHUB_ORG": "openfrontio",
  "GITHUB_TEAM": "",                         // optional: a team slug in that org
  "OIDC_REPOSITORIES": "openfrontio/roblox", // comma-separated owner/repo
  "OIDC_OUTPUT_REPOSITORIES": "",           // optional: repos that may only read outputs
  "TOKEN_TTL_DAYS": "30",
  "KEEP_HISTORY": "true"
}
```

Then add the secrets and deploy:

```console
$ openssl rand -base64 48 | npx wrangler secret put SESSION_SECRET
$ npx wrangler secret put GITHUB_CLIENT_ID
$ npx wrangler secret put GITHUB_CLIENT_SECRET
$ openssl rand -base64 48 | tee admin-token.txt | npx wrangler secret put ADMIN_TOKEN   # optional
$ npx wrangler deploy
```

`SESSION_SECRET` seals the tokens the Worker issues. Changing it signs everyone out.
`ADMIN_TOKEN` is an emergency credential with full access, for example to clear a stuck lock when
GitHub is down. Keep it in your password manager, not in CI.

To serve it on your own domain, add a [custom domain](https://developers.cloudflare.com/workers/configuration/routing/custom-domains/)
to the Worker and use that URL everywhere instead, including in the OAuth app.

## 4. Point Baseplate at it

Each state is a path under `/states/`. Use one per environment:

```hcl
backend "http" {
  address      = "https://baseplate-state.example.workers.dev/states/staging"
  lock_address = "https://baseplate-state.example.workers.dev/states/staging"
}
```

State names can contain letters, digits, `.`, `_`, `-` and `/` (`openfront/staging`).

To move existing local state into it, put the state file's contents into the new state, then add
the backend block:

```console
$ curl -X POST -H "Authorization: Bearer $(cat admin-token.txt)" \
    --data-binary @baseplate/staging.bpstate \
    https://baseplate-state.example.workers.dev/states/staging
```

## 5. Sign in

Each person signs in once:

```console
$ baseplate login
Opening your browser to sign in to baseplate-state.example.workers.dev. If it doesn't open, visit:
...
Signed in to baseplate-state.example.workers.dev. The token is stored in ~/.config/baseplate/credentials.json.
```

Without an argument, `login` signs in to the host in the config's backend block. Tokens last
`TOKEN_TTL_DAYS`. A token stops working as soon as its owner leaves the org or team.

## 6. CI

GitHub Actions workflows in `OIDC_REPOSITORIES` need no secret for the state. Grant the job
`id-token: write`, and Baseplate requests a token from GitHub on its own:

```yaml
permissions:
  contents: read
  id-token: write

steps:
  - uses: actions/checkout@v4
  - uses: outpostgroupio/baseplate@v0.1.0-rc.5
    with:
      command: plan
      args: --config baseplate/staging.bp.hcl
      api-key: ${{ secrets.ROBLOX_API_KEY }}
```

Pull requests from forks don't get `id-token: write`, so they can't read or write state. Run
`validate` for those instead; it needs neither the state nor an API key.

Workflows in `OIDC_OUTPUT_REPOSITORIES` may only read states' [outputs](../config-reference.md#output),
for a [`baseplate_remote_state`](../config-reference.md#data-baseplate_remote_state) data source:
they can't read the rest of any state, write it or lock it. Use this for a repository that consumes
another's outputs but manages nothing in this server.

For CI elsewhere, set `BASEPLATE_STATE_TOKEN` to a token from `baseplate login`, or to the admin
token.

## Locks

`plan`, `apply`, `destroy` and `import` take the lock. A second run fails and says who holds it:

```console
$ baseplate plan
Error: http backend: state locked by runner@fv-az123 (github-actions:openfrontio/roblox@refs/heads/main)
(ID 5c1a3e0e-…, operation apply, since 2026-11-23T19:25:36Z). If that run is no longer active,
release the lock with: baseplate force-unlock 5c1a3e0e-…
```

If a job was cancelled mid-apply, run `baseplate force-unlock <id>` once you're sure it has stopped.

## Old versions

Every write also saves a copy to `history/<state name>/<timestamp>.bpstate` in the bucket. To roll
back, download the version you want from the R2 dashboard and `POST` it as in step 4. To limit how
long copies are kept, add an
[object lifecycle rule](https://developers.cloudflare.com/r2/buckets/object-lifecycles/) for the
`history/` prefix, or set `KEEP_HISTORY` to `"false"`.

## Using another identity provider

To sign people in against something other than GitHub, fork the Worker and replace
`src/github.ts` with your own `IdentityProvider` (see `src/identity.ts`). The CLI side doesn't
change. [State server protocol](../state-server-protocol.md) describes what the server has to do.
