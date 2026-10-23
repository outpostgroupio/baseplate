# How-to: references and `env()`

Two features let a config stay DRY and avoid hard-coded ids and secrets: **resource references** and
the **`env()`** function.

## `env("NAME")` — values from the environment

`env("NAME")` reads an environment variable at load time. Use it for anything you don't want in the
config file — API keys and secret values especially:

```hcl
provider "roblox" {
  api_key = env("ROBLOX_API_KEY")
}

resource "roblox_universe_secret" "token" {
  universe_id = roblox_experience.main.id
  name        = "api_token"
  value       = env("API_TOKEN")
}
```

Export the variables (or keep them in a gitignored `.env` and source it before running):

```console
$ export ROBLOX_API_KEY="…"
$ export API_TOKEN="…"
$ baseplate apply
```

## Resource references

Refer to another resource's attribute with `<type>.<name>.<attribute>`. The most common is `.id` —
one resource's platform id feeding another's config:

```hcl
resource "roblox_experience" "main" {
  universe_id = "10438196190"
}

resource "roblox_developer_product" "gems" {
  universe_id = roblox_experience.main.id   # reference
  name        = "100 Gems"
  price       = 25
}
```

A reference does two things:

1. **Dependency ordering.** Baseplate builds a graph from the references and reconciles dependencies
   first — the experience before the product that points at it, an asset upload before the product
   that uses its id. Ordering is automatic; you don't declare `depends_on`.
2. **Apply-time resolution.** The referenced value is resolved *during* `apply`, after the
   dependency is reconciled. In a `plan`, a referenced value that isn't known yet renders as
   `(known after apply)`:

   ```console
   $ baseplate plan
   + roblox_developer_product.gems (create)
       name = 100 Gems
       price = 25
       universe_id = (known after apply)
   ```

After the dependency exists in state, re-planning shows the resolved value, and the plan converges.

## Combining them

References and `env()` compose naturally — reference ids between resources, and pull secrets/config
from the environment:

```hcl
resource "roblox_experience" "main" {
  universe_id        = env("UNIVERSE_ID")
  voice_chat_enabled = true
}

resource "roblox_universe_secret" "token" {
  universe_id = roblox_experience.main.id
  name        = "api_token"
  value       = env("API_TOKEN")
}
```

## See also

- [Getting started](../getting-started.md)
- [How-to: manage universe secrets](managing-secrets.md)
