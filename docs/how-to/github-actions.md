# GitHub Actions

Baseplate ships a composite action and two example workflows so you can run plan-on-PR and
apply-on-merge in your own repo.

## The action

`uses: outpostgroupio/baseplate@<ref>` — installs Baseplate and runs a subcommand.

Inputs: `command` (default `plan`), `args`, `version` (default `latest`), `api-key`,
`working-directory`. Outputs: `output` (the command's text output) and `changes` (`true`/`false` for
`plan`). **Pin `<ref>` to a released tag (e.g. `@v0.1.0`) or a commit SHA — not `@latest`.**

## Plan on pull requests

Copy `examples/github-actions/plan-on-pr.yml` into `.github/workflows/`. On each PR it runs
`baseplate plan` and posts (or updates) a single comment with the diff. `plan` exits `2` when there are
changes; the action treats that as success, so a PR with changes doesn't fail CI.

Fork PRs are skipped (they have no secrets and a read-only token). The plan comment shows resource
attribute values — sensitive attributes (e.g. secret values) are redacted, but other values are
visible to anyone who can see the PR.

## Apply on merge

Copy `examples/github-actions/apply-on-merge.yml`. On push to `main` it runs
`baseplate apply --auto-approve` (non-interactive). A `concurrency` group serializes applies.

### Restarting servers after a publish

If the config manages `roblox_place` files, add `--restart-servers` so a merge that publishes a new
place version also moves players onto it. The restart only happens when the apply **succeeds** and
publishes at least one `roblox_place`; other merges leave servers alone. `--restart-bleed-off` gives
players time to finish before old servers shut down (whole minutes, up to `60m`):

```yaml
      - uses: outpostgroupio/baseplate@v0.1.0 # pin to a released tag or commit SHA
        with:
          command: apply
          args: --config baseplate.bp.hcl --auto-approve --restart-servers --restart-bleed-off 10m
          api-key: ${{ secrets.ROBLOX_API_KEY }}
```

The API key needs the `universe:write` scope for the restart, in addition to the place's
`universe-places:write`. If the apply succeeds but the restart request fails, the job fails with
`apply succeeded, but restarting servers failed: …`. The new version is already live, so re-run the
restart by hand with `baseplate restart-servers`. You can also restart on demand from a
`workflow_dispatch` job with `command: restart-servers` and `args: --universe <id> --bleed-off 10m --yes`.

## State in CI (important)

The default local state file does not persist between CI runs. For real team CI, configure a remote
[HTTP state backend](remote-state.md) and store its `password` as a GitHub secret, so state persists
and concurrent runs lock.

## Secrets

- `ROBLOX_API_KEY` — required. Passed to the action's `api-key` input.
- Any state backend `password` — if you use a remote backend.
