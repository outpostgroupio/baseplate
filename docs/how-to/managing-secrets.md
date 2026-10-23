# How-to: manage universe secrets

`roblox_universe_secret` manages Open Cloud [secrets](https://create.roblox.com/docs/cloud/open-cloud/usage-and-limits)
for a universe — values your experience reads at runtime via `HttpService` (API keys, tokens, signing
keys). Baseplate treats them as first-class managed resources with a careful security model.

## The security model

- **Value comes from the environment.** You never write the plaintext in the config file — you
  reference it with `env()`.
- **Sealed client-side.** Baseplate encrypts the value with the universe's public key (a LibSodium
  sealed box) *before* it leaves your machine. Roblox only ever sees ciphertext.
- **Never stored, never printed.** The plaintext is not written to state and is redacted from all
  `plan` output (text and JSON). State keeps only a one-way hash (to detect changes) plus the
  non-secret metadata.

## Declare a secret

```hcl
resource "roblox_universe_secret" "discord" {
  universe_id = roblox_experience.main.id
  name        = "discord_token"          # 1–64 chars, alphanumeric/underscore, not starting with a digit
  value       = env("DISCORD_TOKEN")     # plaintext, from the environment
  domain      = "discord.com"            # restrict where the secret may be used
}
```

Provide the value in the environment (e.g. a gitignored `.env`), then apply:

```console
$ export DISCORD_TOKEN="…"
$ baseplate apply
```

Requires the `universe.secret:read` and `universe.secret:write`
[scopes](api-key-scopes.md).

## `domain` — restricting a secret

`domain` limits which `HttpService` targets a secret may be used with:

- `"discord.com"` — usable only against that host.
- `"*.example.org"` — a wildcard subdomain.
- `"*"` — any domain.
- **omitted** — a *private key*: it can't be attached to requests as a header/URL and can't be
  transformed with `addPrefix`/`addSuffix`.

`domain` is the one attribute you can change in place — updating it plans a normal update.

## Rotating a secret

Change the environment value and re-apply. Because the plaintext isn't stored, Baseplate detects the
change via the value's hash and plans an update — the plan shows *that* it changed, never the value:

```console
$ export DISCORD_TOKEN="new-token"
$ baseplate plan
~ roblox_universe_secret.discord (update)
    value_hash: <old> => <new>

Plan: 0 to import, 0 to add, 1 to change, 0 to destroy.
```

## Deleting a secret

Unlike most Roblox resources, secrets support a real delete. Removing the resource from config (or
running `destroy`) **permanently deletes** it from the universe.

## Renaming

`name` is create-only — changing it doesn't rename the secret in place; it's a recreate (delete the
old, create the new). Rotate the value instead if that's what you actually want.

## See also

- [Resource reference → `roblox_universe_secret`](../resource-reference.md#roblox_universe_secret)
- [How-to: API key scopes](api-key-scopes.md)
