# Baseplate state server for Cloudflare Workers

A remote state server for Baseplate's `backend "http"`. It stores state in R2, keeps each state's
lock in a Durable Object, signs people in with GitHub through `baseplate login`, and accepts GitHub
Actions OIDC tokens from CI.

- Deploying it: [Remote state on Cloudflare](../../docs/how-to/remote-state-cloudflare.md)
- What it implements: [State server protocol](../../docs/state-server-protocol.md)

## Layout

```
src/index.ts      routing, the sign-in flow, authentication, and the http backend protocol
src/state.ts      StateObject: one Durable Object per state, holding its lock and writing to R2
src/identity.ts   the IdentityProvider interface
src/provider.ts   which IdentityProvider is used
src/github.ts     GitHub sign-in and org/team membership checks
src/oidc.ts       GitHub Actions OIDC token verification
src/seal.ts       sealed (AES-GCM) tokens, codes and sign-in state
```

To sign in with something other than GitHub, implement `IdentityProvider` and select it in
`src/provider.ts`. Nothing else needs to change.

## Development

```console
$ npm ci
$ npm test           # vitest, in the Workers runtime
$ npm run typecheck
$ npm run dev        # local server on :8787 with local R2 and Durable Objects
```

For `npm run dev`, put the secrets in `.dev.vars` (ignored by git):

```
SESSION_SECRET=dev-secret
ADMIN_TOKEN=dev-admin
```

Then point Baseplate at `http://127.0.0.1:8787/states/<name>` with
`BASEPLATE_STATE_TOKEN=dev-admin`. Plain `http` is accepted for loopback addresses only.

`.npmrc` sets `legacy-peer-deps`, because npm 10 crashes resolving a peer dependency set that
wrangler pulls in.
