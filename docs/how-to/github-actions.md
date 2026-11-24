# GitHub Actions

The repository is also a composite action, and `examples/github-actions/` has two workflows you can
copy: one that comments the plan on pull requests and one that applies on merge.

## The action

```yaml
- uses: outpostgroupio/baseplate@v0.1.0-rc.2
  with:
    command: plan
    args: --config baseplate.bp.hcl
    api-key: ${{ secrets.ROBLOX_API_KEY }}
```

Pin the action to a release tag or a full commit SHA. Releases before 1.0 are release candidates
(`v0.1.0-rc.N`); see the [releases page](https://github.com/outpostgroupio/baseplate/releases).

Inputs:

- `command`: `plan` (default), `apply`, `destroy`, `validate` or `restart-servers`.
- `args`: extra arguments, such as `--config infra.bp.hcl --auto-approve`.
- `api-key`: the Open Cloud key, exported as `ROBLOX_API_KEY`.
- `working-directory`: where to run (default `.`).
- `version`: which Baseplate to install. Leave it empty to install the version that matches the
  ref you pinned the action to. You can also pass a tag, a commit SHA or `latest` (the newest full
  release, or the newest release candidate while there's no full release). (In `v0.1.0-rc.2` and
  earlier the default was `latest`, so set it explicitly there.)
- `token`: the GitHub token used to download the release. Defaults to the workflow's token.

When the version is a release, the action downloads the release binary for the runner (Linux, macOS
or Windows, on x64 or arm64) and checks it against the release's `checksums.txt`, so the job doesn't
need Go. Any other ref, such as a commit SHA, is built from source.

Outputs: `output` (the command's output) and `changes` (`true` or `false`, for `plan`).

## Plan on pull requests

Copy `examples/github-actions/plan-on-pr.yml` to `.github/workflows/`. On each pull request it runs
`baseplate plan` and posts the result as a comment, updating the same comment on later pushes.
`plan` exits with `2` when there are changes; the action counts that as success.

With many assets, reading every one from Roblox makes the plan slow. Add `args: --refresh=false` to
compare the pull request's config with state instead. That shows what the pull request changes,
though not changes made outside Baseplate; the apply on merge still reads everything.

Pull requests from forks are skipped, because they get no secrets and a read-only token. The comment
shows attribute values. Secret values are redacted, but everything else is visible to anyone who
can read the pull request.

## Apply on merge

Copy `examples/github-actions/apply-on-merge.yml`. On every push to `main` it runs
`baseplate apply --auto-approve`, and a `concurrency` group makes sure only one apply runs at a
time.

If the config manages `roblox_place` files, add `--restart-servers` so a merge that publishes a new
place version also moves players onto it. Servers are only restarted when the apply succeeds and
publishes at least one place. `--restart-bleed-off` gives players time to leave before old servers
shut down (whole minutes, up to `60m`):

```yaml
- uses: outpostgroupio/baseplate@v0.1.0-rc.2
  with:
    command: apply
    args: --config baseplate.bp.hcl --auto-approve --restart-servers --restart-bleed-off 10m
    api-key: ${{ secrets.ROBLOX_API_KEY }}
```

The restart needs the `universe:write` scope. If the apply succeeds but the restart fails, the job
fails with `apply succeeded, but restarting servers failed: ...`. The new version is already live,
so you can run `baseplate restart-servers` by hand, or from a `workflow_dispatch` job with
`command: restart-servers` and `args: --universe <id> --bleed-off 10m --yes`.

## Protecting production

An apply on merge deletes whatever the merged config no longer declares. So does a `destroy` in the
wrong directory, or a `for_each` key removed by mistake. Mark resources you can't afford to lose
with [`prevent_destroy`](../config-reference.md#lifecycle):

```hcl
resource "roblox_universe_secret" "prod_webhook" {
  universe_id = "1234567890"
  name        = "webhook"
  value       = env("PROD_WEBHOOK")

  lifecycle {
    prevent_destroy = true
  }
}
```

A plan that would delete it then fails with `Instance cannot be destroyed: ...`, both in the PR
comment and in the apply job, and nothing changes. To retire the resource on purpose, merge a change
that removes the `lifecycle` block first, then one that removes the resource.

The setting lives in the config only, as in Terraform. A change that deletes the whole `resource`
block removes the protection with it, so review pull requests that delete protected blocks. It also
doesn't stop anyone deleting the resource in Creator Hub.

## State and secrets in CI

A local state file doesn't survive between CI runs. For CI, use the
[HTTP state backend](../state.md#remote-state-http-backend).

With the [Cloudflare state server](remote-state-cloudflare.md), the workflow needs no state secret:
grant the job `id-token: write` and list the repository in the server's `OIDC_REPOSITORIES`.
Baseplate gets a short-lived token from GitHub for each run:

```yaml
permissions:
  contents: read
  id-token: write
```

With another backend, store its password as a repository secret.

The workflows need `ROBLOX_API_KEY` as a secret, plus the backend password if you use one.
Pull requests from forks get neither secrets nor `id-token: write`, so run `validate` for them; it
needs no API key and no state.
