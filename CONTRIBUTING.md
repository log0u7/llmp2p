# Contributing

Thanks for helping make model distribution less centralized. English only in all
committed artifacts (code, comments, docs, commit messages).

## Getting started

```sh
git clone git@github.com:log0u7/llmp2p.git && cd llmp2p
go build ./...
go test ./...
```

Go version is pinned in `go.mod`; with `GOTOOLCHAIN=auto` (default since Go 1.21)
no manual setup is needed.

## Commits

- [Conventional Commits](https://www.conventionalcommits.org/): `feat(cli): ...`,
  `fix(engine): ...`, `docs: ...`, `chore: ...`. Breaking changes get `!` and a
  `BREAKING CHANGE:` footer.
- Atomic: one logical change per commit. Mixed refactor + feature = two commits.
- Subject in imperative mood, <= 50 chars when possible, hard cap 72.
- Body only when the *why* is not obvious from the diff.

## Secrets

- A gitleaks pre-commit hook is provided (`.pre-commit-config.yaml`, pinned):
  `pip install pre-commit && pre-commit install`.
- CI also runs gitleaks on every push. Never hardcode tokens; the CLI reads
  `HF_TOKEN` from the environment.

## Tests

- Every bug fix and feature ships with tests. `go test ./...` must pass, and CI
  runs it with `-race`.
- The engine has a localhost swarm integration test (seeder + two leechers);
  keep it green, it is the heart of the project.

## Lint

`make lint` runs golangci-lint (falls back to `go vet` when not installed). CI
enforces golangci-lint.

## Documentation

- Docs follow [Diátaxis](https://diataxis.fr/): put content in the right quadrant
  (tutorials / how-to / reference / explanation). See docs/README.md.
- User-facing behavior changes must update the relevant reference page and, when
  they change how something works, the matching explanation page.

## Architecture decisions

Substantial or contentious technical decisions are recorded as ADRs in
[docs/adr/](docs/adr/) using [MADR](https://adr.github.io/madr/) format:

1. `docs/adr/NNNN-title.md` with the next free number.
2. Fill: context, decision drivers, considered options, decision outcome with
   pros/cons.
3. ADRs are immutable once accepted: to change a decision, write a new ADR that
   supersedes the old one and mark the old one accordingly.

## Publishing a model to the public index

The bootstrap `index.json` at the repository root makes models discoverable.
To add one: pull it locally, then open a PR adding the entry from your local
`index.json` (model, infoHash, manifestSha256, revision, size). Manifests go
under `manifests/<sha256>.json` in the same PR. See
[docs/how-to/contribute-index-entry.md](docs/how-to/contribute-index-entry.md).

## Reporting issues

Include: llmp2p version (`llmp2p --version`), the exact command, full output,
OS, and whether `HF_TOKEN` was set (never paste the token itself).
