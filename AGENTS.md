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
`receive`, `inspect`, `cleanup`), `vault`, `update`, `release`, `version`.

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
| `cleanup.go` | backups of overwritten files, their log, and `migrate cleanup` (local backups and vault migrations) |
| `inspect.go` | `migrate inspect`: a bundle's contents on stdout, never file contents |
| `connector.go` | the `Connector` interface, the shared read gate `jatWrote`, and the title pattern |
| `onepassword.go` | the 1Password connector (`op`) and its allowlist |
| `bitwarden.go` | the Bitwarden connector (`bw`) and its allowlist |
| `vault.go` | `jat vault set`, and the manager-neutral list/fetch/store flow |
| `picker.go` | the Bubble Tea multi/single-select picker used by migrate, with its preview pane |
| `highlight.go` | small per-format tokenizers for syntax colour in the pane; no library by design |
| `preview.go` | what the pane shows: diffs, file heads, trees — and never a Secret item's contents |
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
make install    # copies into ~/.local/bin via rename, safe while jat is running
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
- **The vault scope rules live in code, in `connector.go`.** Each password
  manager is a `Connector`; its CLI runs only through `runAllowed` and that
  connector's allowlist, and nothing that returns an item's field value is on
  it. Every read passes `jatWrote`: the configured vault, jat's title pattern,
  and a manager-native mark naming the same key. Do not add a title-search
  fallback, do not widen an allowlist without saying why in the commit, and
  add a manager by adding a connector, not by branching in `migrate.go`.
- **Prerelease tags (`-rcN`) build but never publish.** Cutting a release is a
  deliberate act; do not automate it away.
- **A Secret item's contents never reach a `Preview`.** `previewSecret` is the
  only builder such an item may pass through; it shows names and states, never
  bytes or diffs. `TestSecretItemsAreNeverPreviewed` guards it. Do not add a
  "just the first line" exception.
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
