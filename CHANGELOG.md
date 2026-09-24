# Changelog

Newest first, one section per promoted release. Entries come from commit
trailers (`Changelog:`, `Release-Note:`, `Upgrade:`); `make changelog` prints
the unreleased block. Every entry ends with an `Upgrade:` line, even when it
is "nothing to do".

## Unreleased

### Added

- `jat migrate cleanup` opens a menu to remove finished migrations from your
  password manager as well as the local backups; `cleanup <key>` takes both
  halves of one migration.

### Changed

- The 1Password vault transport has been verified end to end (`vault set`,
  `migrate send`, `inspect --key`, `receive`) against op 2.39.0 on a
  1Password Business account. Bitwarden is still exercised against stand-ins
  only.
- The preview pane's list column scales with the terminal width, and
  identical items show their contents instead of a one-line note.

Upgrade: nothing to do

## v0.3.0 — 2026-09-23

### Added

- `jat migrate receive` applies a bundle, showing per item whether it is
  absent, identical or differs from this machine before anything is written.
- `jat migrate inspect` shows what a bundle contains without touching the
  machine.
- `jat migrate receive` keeps a `.pre-jat-<key>` copy of every file it
  overwrites, and `jat migrate cleanup <key>` previews and removes those copies.
- The migrate picker gains a preview pane (`→`): a diff of what would change
  on receive, the file as it is on send, and a full-screen view on narrow
  terminals. Secret items show names and states, never contents.
- The preview pane colours config files (shell, gitconfig, TOML, JSON, YAML,
  ssh_config) for readability.
- `jat vault set` chooses the private password-manager vault jat may use,
  refusing any vault that is not yours alone.
- `jat migrate send|receive --transport vault` moves a bundle through that
  vault, and `jat migrate inspect --key` looks inside one. 1Password (`op`)
  and Bitwarden (`bw`) are supported. At this release neither had been run
  against its real CLI; both were exercised against stand-ins only (1Password
  was verified afterwards — see Unreleased).

### Changed

- With Bitwarden, jat asks for your master password itself when the vault is
  locked instead of requiring `export BW_SESSION=...` first.
- README describes jat as a general utility; AGENTS.md explains the changelog
  trailer convention.

### Fixed

- `jat migrate send --transport file --all` no longer fails with
  `unknown transport "--all"`.
- The migrate menus are full screen and no longer flicker between questions.
- `make install` puts jat in `~/.local/bin` and says what to do if that is not
  on your PATH.

### Removed

- `jat export` and `jat import`; use `jat migrate send` and `jat migrate
  receive`. Bundles written by `export` are still readable by `receive`.

Upgrade: manual step — (1) replace `jat export` / `jat import` in any script
with `jat migrate send --transport file --all` / `jat migrate receive <bundle>
--all`; (2) remove any old copy at `~/bin/jat`, since `make install` now
targets `~/.local/bin`; (3) before using `--transport vault`, run
`jat vault set` once on each machine. No data is migrated on start.

## v0.1.1 and earlier

Before this file existed. `init`, `install`, `list`, `migrate send` (file
transport), `export`/`import`, `update`, `release`, `version`.
