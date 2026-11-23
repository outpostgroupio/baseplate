# Contributing

Thanks for helping out. For anything bigger than a small fix, please open an issue first so we can
agree on the approach before you write the code.

Security problems go through [SECURITY.md](SECURITY.md), not public issues.

## Setup

You need Go 1.24 or later. The release binary must build without cgo, so don't add dependencies
that need it.

```bash
CGO_ENABLED=0 go build ./...
go vet ./...
go test ./... -race
```

The test suite doesn't touch the network or need Roblox credentials. (The race detector needs cgo,
so the tests run with cgo enabled; only the release build has to be cgo-free.)

To try the CLI:

```bash
go run ./cmd/baseplate plan --config X.bp.hcl --state X.bpstate
```

Without `ROBLOX_API_KEY` the CLI uses an in-memory mock provider, which is enough for most engine
work but doesn't know the `roblox_*` resource types.

## Making changes

- Add tests for new behaviour and bug fixes.
- Run `gofmt` and follow the style of the code around your change.
- Keep each PR to one logical change.
- If you change a resource's schema or its `Doc()`, run `go generate ./...` to update
  `docs/resource-reference.md`. A test fails if the file is out of date.

[docs/development.md](docs/development.md) covers the code layout, live tests against Roblox,
releases, and notes on Open Cloud behaviour that isn't in Roblox's docs.

## PR titles

PRs are squash-merged, so the PR title becomes the commit message on `main` and the line in the
release notes. Titles must follow [Conventional Commits](https://www.conventionalcommits.org/), and
a CI check enforces it:

```
feat(cli): add --auto-approve to apply
fix(state): resend original lock payload on unlock
docs: clarify the http backend setup
feat(engine)!: change the plan JSON envelope
```

A `!` marks a breaking change. `feat` and `fix` appear in the release notes under their own
headings; `refactor`, `perf` and `revert` go under "Other changes"; `docs`, `test`, `build`, `ci`,
`chore` and `style` are left out.

## CI

Every PR runs the build, `go vet` and the race-enabled tests (`ci`), plus the PR title check
(`pr-title`). Both must pass before merging.

## License

By contributing, you agree that your contributions are licensed under the [MIT License](LICENSE).
