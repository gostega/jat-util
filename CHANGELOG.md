# Changelog

Newest first, one section per promoted release. Entries come from commit
trailers (`Changelog:`, `Release-Note:`, `Upgrade:`); `make changelog` prints
the unreleased block. Every entry ends with an `Upgrade:` line, even when it
is "nothing to do".

## v0.5.0 — 2026-11-27

### Added

- **Templates** — apply a known setup without another machine to copy from.
  `jat init --template <name>` (or pick one at `init`'s prompt, which
  defaults to none) and `jat migrate receive --template <name|dir>` go
  through the same picker, diffs, `--show` and backups as a bundle, with
  installs, login shell and macOS defaults shown as done / to do beside the
  files. Two are built in — `ohmybash` (bash 5, oh-my-bash, bash as login
  shell) and `iterm2-natural-text` (⌘←/→ to line start/end and friends,
  skipped when iTerm2 isn't installed) — and `jat template list` shows them
  with yours from `~/.jat/templates`. `jat migrate send --to-template <name>`
  saves this machine's config as one. Templates support `extends`,
  `profiles` and per-item `when` conditions, may symlink to the real files,
  and never carry secrets.
- `examples/templates/mac-dev`: a template to copy and edit, combining both
  built-ins with installs, `dock-clean` and a Ghostty-only key map.
- `jat shell <tool>`: make an installed shell the login shell (`/etc/shells`,
  then `chsh`). It refuses a shell jat doesn't count as installed, so never
  macOS's bash 3.2, and carries jat's PATH line to the new shell.
- `jat mac dock-clean` removes every Apple-bundled app from the Dock (even
  ones you pinned) and keeps the rest; `--show` previews, and the old Dock is
  backed up to `~/.jat/mac`. `jat mac default` is `defaults write` with a dry
  run that skips values already set.
- `jat install bash` installs a current bash, and counts the stock macOS
  bash 3.2 as not installed.
- New installs: `bitwarden`, `discord`, `iterm2`, `orbstack`, `protonvpn`
  (Homebrew casks on macOS).
- `jat install` with no tool opens a menu grouped by category, drawn as a
  dependency tree with installed tools marked; `jat install --all
  [--dry-run]` installs or plans everything not yet present.
- Your own installers and per-tool defaults for `jat install`, kept in
  `~/.jat/config.json` and sitting between jat's built-in default and a
  one-off `--<method>`: `jat install add-installer`, `set-default`,
  `unset-default`, `remove-installer` and `list-installers`. A default can
  also add a tool jat doesn't know yet. `--show` says when the command
  comes from your config, and `jat list` marks those tools.
- An `iterm2` config item for `jat migrate`: iTerm2's dynamic profiles.

### Changed

- The oh-my-bash install runs `--unattended`, so its installer no longer
  takes over the terminal.
- The release workflow runs only when a `v*` tag is pushed, so a release
  shows a single run; vet and test on pull requests moved to a separate
  `ci` workflow, and pushes to `main` no longer run CI.

### Fixed

- `jat install` no longer offers oh-my-bash on a machine that already has it.
- `jat install jat` recognises an existing install reached through a
  symlinked directory (macOS's `/var`) instead of re-installing.
- `jat release` releases the repo it runs in (from the remote URL, or
  `--repo`), taking the version from that repo's tags instead of jat-util's
  releases.

Upgrade: nothing to do

## v0.4.0 — 2026-09-25

### Added

- `jat install --self` (or `jat install jat`): a freshly downloaded binary
  puts itself on your PATH — into `~/.local/bin`, Homebrew's `bin` or
  `/usr/local/bin`, adding the PATH line to your shell rc if none is on PATH —
  clears macOS quarantine, removes the download and runs `init`.
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
- The module is now `github.com/gostega/jat-util`, so
  `go install github.com/gostega/jat-util@latest` works. Source moved into
  `cli/`, with black-box tests in `test/`; no user-facing change.

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
