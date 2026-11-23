# Universe secrets

`roblox_universe_secret` manages a universe's Open Cloud
[secrets](https://create.roblox.com/docs/cloud/open-cloud/usage-and-limits): values your experience reads at
runtime through `HttpService`, such as API keys and tokens.

```hcl
resource "roblox_universe_secret" "discord" {
  universe_id = roblox_experience.main.id
  name        = "discord_token"        # 1-64 letters, digits or underscores; can't start with a digit
  value       = env("DISCORD_TOKEN")
  domain      = "discord.com"
}
```

```console
$ export DISCORD_TOKEN="..."
$ baseplate apply
```

The key needs the `universe.secret:read` and `universe.secret:write` [scopes](api-key-scopes.md).

## How the value is handled

`value` gets special handling:

- Baseplate encrypts it with the universe's public key (a libsodium sealed box) before sending it,
  so only Roblox can decrypt it.
- It isn't written to state. State keeps a salted, deliberately slow hash of it (`value_hash`) so
  Baseplate can tell when it changes.
- Both `value` and `value_hash` are redacted from `plan` output, text and JSON.

Use `env()` (or `file()` on a gitignored file) to supply it, so the plaintext isn't in your config.
Note that this protection only applies to `value`. If you use `env()` for any other attribute, that
value is stored in state and shown in plans as usual.

## Domain

`domain` limits which hosts `HttpService` may send the secret to:

- `"discord.com"`: only that host.
- `"*.example.org"`: any subdomain.
- `"*"`: any host.
- Omitted: a private key, which can't be attached to requests as a header or URL and can't be
  changed with `addPrefix`/`addSuffix`.

Changing `domain` updates the secret in place.

## Rotating

Change the value in the environment and apply again. The plan shows that the value changed, but
not what it is:

```console
$ export DISCORD_TOKEN="new-token"
$ baseplate plan
~ roblox_universe_secret.discord (update)
    value_hash: (sensitive value) => (sensitive value)

Plan: 0 to import, 0 to add, 1 to change, 0 to destroy.
```

After upgrading from a release that used a plain hash, the first plan shows an update for every
secret. Applying it re-sends the same values and records the new hash, so the secrets keep their values.

## Renaming and deleting

`name` can't be changed in place. Changing it deletes the old secret and creates a new one.

Secrets can be really deleted: removing the resource from the config, or running `destroy`,
deletes the secret from the universe.
