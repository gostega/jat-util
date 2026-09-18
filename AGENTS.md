# AGENTS.md

Instructions for coding agents (and humans) working in this repo. `CLAUDE.md`
is a symlink to this file.

## What this is

`jat` is a single-binary Go CLI: a general helper utility for installs,
configs and everyday machine housekeeping. Today it installs tools by a
per-profile default method, lists what it knows, carries a curated set of
config files between machines, updates itself and cuts its own releases. New
functions are added as subcommands; migration is one of them, not the point of
the tool. See `README.md` for the user view.

Subcommands live in `main.go`: `init`, `install`, `list`, `migrate` (`send`,
`receive`, `inspect`), `update`, `release`, `version`.

## Layout

Flat package `main`, one file per concern:

| File | Holds |
|---|---|
| `main.go` | entry point, subcommand dispatch, profile detection, version stamp |
| `tools.go` | the tool → install-method table (seed data, curated by hand) |
| `configs.go` | the curated list of config paths `migrate` may carry |
| `transfer.go` | bundle format, the serialiser, and the entry-name guard |
| `migrate.go` | the `migrate` wizard, `send`, and its transports (file, 1password) |
| `receive.go` | `migrate receive`: classify a bundle against disk, then write what was ticked |
| `inspect.go` | `migrate inspect`: a bundle's contents on stdout, never file contents |
| `picker.go` | the Bubble Tea multi/single-select picker used by migrate |
| `update.go` | self-update from GitHub releases; asset naming |
| `release.go` | `jat release`: tag and publish from a clean, in-sync HEAD |

Tests sit next to the code (`*_test.go`). `TestEveryToolResolvesSomewhere`
fails if a seeded tool cannot resolve on any profile, so dead table rows are
caught.

## Build, test, run

```sh
make build      # bin/jat, version stamped from git describe
make test       # go test ./...
make vet
make install    # copies into ~/bin via rename, safe while jat is running
```

Go version floor is whatever `go.mod` declares. CI (`.github/workflows/release.yml`)
runs vet and test on every push and PR, and publishes cross-platform binaries
only for non-`rc` tags.

## Rules that are easy to get wrong

- **Version is never a literal in source.** `Version` and `Commit` are set by
  `ldflags` from the Makefile and the release workflow. A binary reporting
  `dev` was built from source and `jat update` deliberately will not replace it.
- **Release asset names are a contract.** `assetName()` in `update.go` must
  match what the workflow uploads. Change both or neither.
- **`configs.go` is a curated list, not a sweep.** Send only touches paths
  named there. Adding a path is a product decision; say why in the commit.
- **Transports carry the bundle; they do not change it.** New transports go in
  `migrate.go` behind the same serialiser and picker. Do not fork the format.
- **Prerelease tags (`-rcN`) build but never publish.** Cutting a release is a
  deliberate act; do not automate it away.
- **`--show` must always be a faithful dry run.** Whatever `install` would
  execute, `--show` prints exactly that.

## Commits and releases

- Subject line: `<scope>: <description>` completing "applying this commit
  will …".
- Work on a branch; merge into `main` as whole, tested chunks. Release tags go
  on `main`.
- Semantic versioning: a new user-visible capability bumps the minor, fixes
  bump the patch.
- Never push, force-push, tag remotely or delete remotely unless asked.

### Changelog via git trailers

`CHANGELOG.md` is assembled from commit trailers, never harvested from the
whole log. A commit opts in by carrying trailers in its **final paragraph**
(the same block as `Co-Authored-By`; git only parses the last paragraph as
trailers, so a blank line above them silently turns them into prose):

| Trailer | Value | When |
|---|---|---|
| `Changelog:` | `added` · `changed` · `fixed` · `removed` · `security` | the commit changed something a user would notice |
| `Release-Note:` | one user-facing sentence | the subject is internal shorthand; this overrides it |
| `Upgrade:` | what a promotion must do | on the commit that causes it |

No `Changelog:` trailer, no changelog line. Every release entry ends with an
`Upgrade:` line, even when it is `Upgrade: nothing to do`; a missing line means
the entry is unfinished, not that nothing changed. Second view without tooling:

```sh
git shortlog --group=trailer:changelog --format=reference <last-tag>..HEAD
```

## Local, uncommitted files

`CLAUDE.local.md` and `backlog/` are gitignored. They hold the maintainer's
personal instructions and task tracking, and are not part of the project.
