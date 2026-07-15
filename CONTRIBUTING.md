# Contributing to vault-cost

Thanks for taking the time to contribute. This project is a Kubecost/OpenCost
custom-cost plugin for HashiCorp Vault Enterprise client costs, written in Go.

## Getting started

You will need:

- Go 1.24 or newer (the CI tracks the version in [go.mod](go.mod))
- [golangci-lint](https://golangci-lint.run) v2
- Docker with Buildx (only needed to build the container image)

Clone the repo and run the checks:

```sh
make tidy   # go mod tidy
make lint   # golangci-lint run
make test   # go test ./... with the race detector
make build  # cross-compile the linux plugin binary
```

`make check` runs formatting, linting, tests, and the vulnerability scan in one
go — the same gates CI enforces.

## Making a change

1. Fork the repository and create a topic branch from `main`.
2. Keep changes focused. Add or update tests for any behavior change — the cost
   logic is covered by golden-file tests in [pkg/cost](pkg/cost).
3. Run `make check` locally and make sure it is green.
4. Open a pull request against `main` and fill in the template.

## Commit and PR conventions

Commit messages follow [Conventional Commits](https://www.conventionalcommits.org).
The release notes are generated from them, so the prefix matters:

- `feat:` a user-facing feature (shows under **Features**)
- `fix:` a bug fix (shows under **Bug fixes**)
- `docs:`, `test:`, `chore:`, `ci:` are excluded from release notes

Example: `feat(cost): break out ACME clients as a separate cost line`

Keep pull requests small and squash-friendly. The CI must pass (lint, tests on
the supported Go versions, build, `go mod tidy` check, and `govulncheck`) before
a PR can merge.

## Releasing (maintainers)

Releases are automated. Tag a commit on `main` with a semver tag and push it:

```sh
git tag v0.3.0
git push origin v0.3.0
```

The `release` workflow then builds the signed multi-arch binaries and container
image, generates an SBOM, and publishes the GitHub release and the
`ghcr.io/timkrebs/vault-cost` image.

## Reporting security issues

Please do not open public issues for security problems. See
[SECURITY.md](SECURITY.md).
