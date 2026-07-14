# Contributing to Baseplate

Thanks for your interest in Baseplate — a Terraform-style Infrastructure-as-Code tool for Roblox,
written in Go. This guide covers how to set up, make a change, and get it merged.

## Before you start

- Skim **[ROADMAP.md](ROADMAP.md)** for where the project is heading and what's in scope.
- For anything non-trivial, **open an issue first** to discuss the approach — it saves everyone time.
- Good first contributions: docs fixes, test coverage, and the smaller items called out in the roadmap.

## Development setup

Baseplate is pure Go with **no runtime dependencies on cgo** — that's a hard constraint (the binary
must build with `CGO_ENABLED=0`).

- **Go 1.24+** (module floor is 1.24).
- Clone your fork, then:

```bash
# Build (must succeed with cgo disabled — this is what CI checks)
CGO_ENABLED=0 go build ./...

# Vet
go vet ./...

# Full test suite (hermetic: no network, no Roblox credentials needed)
go test ./... -race
```

The race detector needs cgo, so tests run under the default toolchain; the *distribution* build is
what must be cgo-free.

### Running the CLI locally

```bash
go run ./cmd/baseplate {validate|plan|apply|destroy} --config X.bp.hcl --state X.bpstate
```

Without `ROBLOX_API_KEY` set, the CLI uses an in-memory mock provider, which is enough for most
engine-level work. Exit codes: `0` no changes, `2` (plan) changes present, `1` error.

## Making changes

- **Write tests.** New behavior and bug fixes should come with tests; we lean on test-driven
  development. `go test ./... -race` must stay green.
- **Match the surrounding code.** Run `gofmt` (or `go fmt ./...`), follow existing patterns, and keep
  files focused.
- **Keep it pure Go.** Avoid adding dependencies unless clearly justified; never anything that breaks
  the `CGO_ENABLED=0` build.
- **Keep changes focused.** One logical change per PR — it makes review and the changelog cleaner.

## Commits, PR titles, and merging

We **squash-merge every PR** (merge commits and rebase-merge are disabled). That means:

- Your PR's **title becomes the single commit on `main`** and the line that appears in release notes.
- Individual commit messages inside your PR are not preserved, so **you don't need to police every
  commit** — just write a good PR title.

**PR titles must follow [Conventional Commits](https://www.conventionalcommits.org/):**

```
<type>(<optional scope>): <description>
```

A CI check (`pr-title`) enforces this and comments on your PR if the title doesn't match. Examples:

- `feat(cli): add --auto-approve to apply`
- `fix(state): resend original lock payload on unlock`
- `docs: clarify the http backend setup`
- `feat(engine)!: change the plan JSON envelope` (a `!` marks a breaking change)

Allowed types:

| Type | Use for | In release notes? |
|---|---|---|
| `feat` | a new feature | ✅ **Features** |
| `fix` | a bug fix | ✅ **Bug fixes** |
| `refactor`, `perf`, `revert` | code changes with no new feature/fix | ✅ Other changes |
| `docs`, `test`, `build`, `ci`, `chore`, `style` | docs, tests, tooling, formatting | ❌ (filtered out) |

Release notes are generated from these titles, so a clear, user-facing description helps everyone.

## What CI checks

Every PR runs:

- **`ci`** — `CGO_ENABLED=0 go build ./...`, `go vet ./...`, and `go test ./... -race`.
- **`pr-title`** — validates the PR title against Conventional Commits.

All checks must pass before a maintainer merges.

## Live tests (optional)

Most contributions need only the hermetic suite above. A small set of opt-in tests run against a real
Roblox universe and require credentials — see **[docs/roblox-live-testing.md](docs/roblox-live-testing.md)**.
You do **not** need these to contribute; maintainers run them when relevant.

## License

By contributing, you agree that your contributions are licensed under the project's
[MIT License](LICENSE).
