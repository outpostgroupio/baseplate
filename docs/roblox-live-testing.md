# Running Baseplate against real Roblox (live integration)

Baseplate's unit/contract tests are fully hermetic (a mock Open Cloud server), so
`go test ./...` never touches Roblox. The **live** integration test is gated and
opt-in.

## 1. Create a throwaway test universe
Create a new experience in Roblox Studio or the Creator Dashboard (don't use a real
game). Note its **Universe ID** (the number in the Creator Dashboard URL / settings —
not the Place ID).

## 2. Create a scoped Open Cloud API key
Creator Hub → **Open Cloud → API Keys → Create API Key**. Add these API systems,
scoped to your test universe:

- **Assets** — read + write
- **Universe** (the v2 Universe/Cloud API) — read + write
- **Developer Products** ("Configure Developer Products/Game Passes") — read + write
- **Universe Secrets** (`universe.secret:read` + `universe.secret:write`) — only
  needed for the `roblox_universe_secret` live test; distinct from `universe:write`
  (same pattern as places needing their own `universe-places:write` scope) — without
  it the API returns `401 insufficient scopes`.

Set an IP allowlist (`0.0.0.0/0` for quick testing) and an expiration you're
comfortable with. Copy the key once — you can't view it again.

## 3. Put credentials in a gitignored file
Create `.env.local` in the repo root (already gitignored):

```
ROBLOX_API_KEY=<your key>
BASEPLATE_TEST_UNIVERSE=<your universe id>
```

## 4. Run the live test
```bash
set -a; . ./.env.local; set +a
BASEPLATE_LIVE=1 go test ./internal/provider/roblox/ -run Live -v
```

It runs the full lifecycle against your universe: adopts + configures the
experience, creates a developer product whose `universe_id` is a **reference** to
the experience's id (resolved at apply time), asserts a second plan converges to
no changes, then cleans up (the product is taken off sale; experience config is
left as set — Baseplate adopts universes, it doesn't own their lifecycle).

`-run Live` also picks up `TestLiveSecretLifecycle` (`roblox_universe_secret`),
which reuses the same `BASEPLATE_TEST_UNIVERSE` and creates/rotates/destroys a
throwaway `bp_test_<unixtime>` secret — a real hard delete, verified by listing
secrets after destroy. Run it in isolation with `-run LiveSecret`.

`TestLiveAnalyticsAlertLifecycle` (`roblox_analytics_alert`, run alone with
`-run LiveAnalyticsAlert`) needs the **`universe.analytics.alert:read`** +
**`universe.analytics.alert:write`** scopes. It creates a never-firing
`Baseplate live test <unixtime>` alert, converges, updates and disables it, converges,
destroys it and verifies it's gone (a `t.Cleanup` deletes it if a step fails). If a
preflight list returns 401/403 (missing scopes, or an experience that isn't
alert-eligible) the test **skips** rather than fails.

## Using the CLI directly
With `ROBLOX_API_KEY` exported, the `baseplate` CLI uses the real provider
automatically (otherwise it uses the in-memory mock). See
[`examples/baseplate.bp.hcl`](../examples/baseplate.bp.hcl); replace the
placeholder IDs, then:

```bash
set -a; . ./.env.local; set +a
go run ./cmd/baseplate plan   --config examples/baseplate.bp.hcl --state /tmp/bp.bpstate
go run ./cmd/baseplate apply  --config examples/baseplate.bp.hcl --state /tmp/bp.bpstate
go run ./cmd/baseplate destroy --config examples/baseplate.bp.hcl --state /tmp/bp.bpstate
```
