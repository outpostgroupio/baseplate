# Releasing Baseplate

Releases are automated with [goreleaser](https://goreleaser.com). Pushing a `v*` tag triggers
`.github/workflows/release.yml`, which cross-compiles the binaries and publishes a GitHub Release.

## Cut a release

1. Make sure `main` is green (CI passing) and you're on the commit you want to ship.
2. Tag it. Pre-1.0 / release-candidate builds use a prerelease suffix so they're flagged as
   pre-releases automatically:
   ```console
   $ git tag v0.1.0-rc.1
   $ git push origin v0.1.0-rc.1
   ```
   A bare tag (e.g. `v0.1.0`) publishes as a full release.
3. Watch the **release** workflow in the Actions tab. On success it creates the Release with:
   - 6 archives — `linux`/`darwin`/`windows` × `amd64`/`arm64` (`.tar.gz`, `.zip` on Windows),
     each bundling the binary + `LICENSE` + `README.md`;
   - `checksums.txt` (SHA-256);
   - an auto-generated changelog grouped into Features / Bug fixes / Others.

## Prerelease policy

`.goreleaser.yaml` uses `release.prerelease: auto`: a tag with a SemVer prerelease suffix
(`-rc.N`, `-beta.N`, `-alpha.N`) is marked as a GitHub pre-release; a bare tag is a full release.
While Baseplate is pre-1.0, ship release candidates (`-rc.N`). To instead mark *every* `0.x` tag as
a pre-release regardless of suffix, set `release.prerelease: true` until you cut `v1.0.0`.

## Version stamping

The build stamps `internal/version` via `-ldflags -X` (`Version`, `Commit`, `Date`). Users see it
with `baseplate version` (or `baseplate --version`). An unstamped local `go build` reports
`baseplate dev (commit none, built unknown)`.

## Test the pipeline without publishing

goreleaser must run where `git` resolves the repo (a normal clone, not this WSL/UNC worktree):

```console
$ go install github.com/goreleaser/goreleaser/v2@latest
$ goreleaser check                     # validate .goreleaser.yaml
$ goreleaser release --snapshot --clean # full local build into ./dist, no publish
```

The workflow needs no extra secrets — it uses the automatic `GITHUB_TOKEN` with `contents: write`.
