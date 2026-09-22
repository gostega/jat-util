# jat-util
`jat` is a general-purpose machine utility: one binary for installing tools,
managing configs, moving between machines, and whatever other housekeeping
jobs turn out to be worth a subcommand. Migration is one function among many.

Current subcommands: `init`, `install`, `list`, `migrate` (`send`, `receive`,
`inspect`, `cleanup`), `vault`, `update`, `release`, `version`.

## ABOUT

- OS-agnostic, opinionated per profile. Profile (`macos`, `debian`, ...) is
  chosen once at `jat init` and saved to config (~/.jat/config)
- Each tool has a default install method per profile — e.g. on `macos`,
  `jat install gh` defaults to brew.
- Override the method per invocation: `jat install gh --curlscript` (also
  `--brew`, `--mise`, `--npm`, `--manual`, ...).
- `--show` prints the exact command that would run instead of running it.
  For `--curlscript` it also prints the official installer page URL, so the
  operator can verify the script hasn't changed before trusting it.
- `tools.md` / `sort.html` in this repo are the seed data for the
  tool → default-method mapping.

## MOVING CONFIG BETWEEN MACHINES

```sh
# old machine: pick what leaves, get one bundle
jat migrate send                       # wizard; --transport file --all to script it
jat migrate send --show                # list what would be bundled, write nothing

# either machine: look before you leap
jat migrate inspect jat-migrate-7f3a.tar.gz [--files]

# new machine: see what it would do here, then apply what you tick
jat migrate receive jat-migrate-7f3a.tar.gz [--show] [--all]
```

- Only paths named in `configs.go` travel. Secrets (keys, tokens) are left out
  unless `--include-secrets`, and are never ticked by default.
- `receive` compares each incoming file with the one on disk and marks every
  item `absent`, `identical` or `differs`. `identical` arrives unticked;
  `differs` means ticking it overwrites a file that changed on this machine.
- A bundle is treated as untrusted input: an entry that would land outside
  `$HOME` refuses the whole bundle before anything is written.
- In the picker, `→` opens a preview of the row under the cursor: on receive
  a diff of what would change, on send the file as it is here; directories
  show as a tree. Below 100 columns the preview takes the whole screen
  (`←`, `q` or `esc` return). Secret items show file names and states only,
  never contents; set `"preview": {"hideSecretNames": true}` in
  `~/.jat/config.json` to hide the names too.
- The file view in the pane is syntax-coloured for the formats jat carries
  (shell, INI/gitconfig, TOML, JSON, YAML, ssh_config, key = value).
  Diffs stay red/green only.
- `inspect` never prints file contents.
- A file `receive` overwrites is kept beside it as `<name>.pre-jat-<key>`. When
  you are happy with the result, `jat migrate cleanup <key> --show` lists those
  backups and `jat migrate cleanup <key>` deletes them after asking. `send`
  never bundles them.

### Through a password manager instead of a file

```sh
jat vault set                          # once per machine: which manager, which vault
jat migrate send --transport vault
jat migrate receive --transport vault [--key 7f3a]
jat migrate inspect --key 7f3a
```

- 1Password (`op`) and Bitwarden (`bw`) are supported. `jat vault set` picks
  whichever CLI is installed, or `--manager 1password|bitwarden` when both
  are. `--transport 1password` / `--transport bitwarden` also work, as
  spellings of `vault` that must match what was set.
- The bundle is the same bytes either way; the manager only carries it. In
  1Password it is a document titled `jat/migrate/<key>/<host>/<user>` and
  tagged `jat-migrate`; in Bitwarden it is the attachment on a secure note
  of that name in a `jat-migrate` folder (attachments need Premium).
- `jat vault set` refuses anything that is not your private vault: in
  1Password the vault's type must be PERSONAL; in Bitwarden, items owned by
  an organization are never touched.
- jat only ever opens items carrying both its title pattern and its own
  mark, and it cannot run `op item get`, `op read`, `bw get item` or
  `bw get password` at all.
- Bitwarden has no desktop-app hand-off, so when its vault is locked jat runs
  `bw unlock` for you: bw asks for your master password and jat keeps the
  session key in memory for that one run. A `BW_SESSION` you exported
  yourself is used as-is. Signing in (`bw login`) stays yours to do once.
- `--key` matters only when more than one migration is waiting.
