# jat-util
`jat` is a general-purpose machine utility: one binary for installing tools,
managing configs, moving between machines, and whatever other housekeeping
jobs turn out to be worth a subcommand. Migration is one function among many.

Current subcommands: `init`, `install`, `list`, `migrate` (`send`, `receive`,
`inspect`, `cleanup`), `template`, `shell`, `vault`, `mac` (`dock-clean`,
`default`), `update`, `release`, `version`.

## INSTALL

Prebuilt binaries for macOS and Linux are attached to each
[release](https://github.com/gostega/jat-util/releases); no account needed.
Download the one for your machine and let it install itself:

```sh
curl -fsSL -o jat https://github.com/gostega/jat-util/releases/latest/download/jat-darwin-arm64
chmod +x jat
./jat install --self
```

Assets: `jat-darwin-arm64` (Apple silicon), `jat-darwin-amd64`,
`jat-linux-arm64`, `jat-linux-amd64`.

`install --self` copies the binary into the first of `~/.local/bin`,
Homebrew's `bin` or `/usr/local/bin` that is on your PATH and writable. If
none is (a fresh Mac has `~/.local/bin` off PATH), it uses `~/.local/bin`
and offers to add the PATH line to your shell's rc file — `--yes` skips the
question, `--show` only says what it would do. It also clears macOS's
quarantine flag, removes the download, and runs `jat init` if this machine
has no config yet. Then open a new shell and `jat version`.

From source (needs Go, the version in `go.mod`):

```sh
git clone https://github.com/gostega/jat-util && cd jat-util
make install
```

## UPDATE

```sh
jat update --check                     # what is installed, what is latest
jat update                             # replace this binary with the latest release
```

`update` swaps the file in place by rename, so it is safe to run from the
binary being replaced. It only updates a binary that came from a release: a
build from source reports `dev` (or a bare commit hash) and is left alone —
rebuild with `make install` instead. Release candidates (`-rcN` tags) are
built by CI but never published, so `update` never offers one.

## INSTALLING TOOLS

```sh
jat install                  # menu: tick what you want, grouped by tab, with the dependency tree
jat install gh               # one tool by this profile's default method
jat install gh --curlscript  # …or a specific method
jat install --all            # everything jat knows that is not installed yet
jat install --all --dry-run  # the plan, in dependency order, running nothing (--preview, --show)
```

- The menu has a tab per group (Apps, Package managers, CLI tools, Runtimes,
  Other) plus All; `tab`/`shift+tab` switch. Rows are drawn as the dependency
  tree: casks and formulae under `homebrew`, runtimes under `mise`, npm tools
  under `node`. Ticking a tool ticks what it needs (shown in amber);
  unticking it lets go of anything nothing else needs.
- Tools already on this machine are marked `installed`, greyed and left out
  of `ctrl+a` / `ctrl+t` (invert), though you can tick one to reinstall.
- Manual and App Store tools can be ticked; they are listed with their URLs
  at the end of the run instead of being executed.
- Installs run parents first, stream each command's output, carry on past a
  failure, and skip anything that needed the failed tool.

## MAC

```sh
jat mac dock-clean --show              # what would go, what stays, the exact commands
jat mac dock-clean                     # take Apple's bundled apps out of the Dock
```

`dock-clean` removes only apps that ship with macOS (those under
`/System/Applications`, plus Safari); anything you pinned yourself stays. The
Dock is backed up to `~/.jat/mac/dock-<timestamp>.plist` first, and the undo
command is printed at the end.

```sh
jat mac default com.example.app SomeKey -bool true --show   # defaults write, with a dry run
```

`jat mac default` is `defaults write` that says what it would do, and does
nothing when the value is already set.

## SHELL

```sh
jat install bash        # bash 5 from Homebrew; macOS's own /bin/bash is 3.2 forever
jat shell bash --show   # what making it the login shell would run
jat shell bash          # add it to /etc/shells (sudo), then chsh — both ask for your password
```

`jat shell` only switches to a shell jat counts as installed, so on a Mac it
will not chsh you to the stock bash 3.2.

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
- Add your own installers, or change a tool's default, without a jat release.
  They live in `~/.jat/config.json` and sit between the built-in default and
  `--<method>`:

  ```sh
  jat install add-installer claude-native --method curlscript \
      --command "curl -fsSL https://claude.ai/install.sh | bash" \
      --docs https://code.claude.com/docs/en/overview
  jat install set-default claude claude-native   # or a built-in method: set-default mise curlscript
  jat install claude                             # runs the script above
  jat install claude --manual                    # a one-off override still wins
  jat install list-installers                    # what you have configured
  jat install unset-default claude               # back to jat's default
  jat install remove-installer claude-native
  ```

  `set-default` also works for a tool jat doesn't know yet, which adds it.
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
- A file `receive` overwrites is kept beside it as `<name>.pre-jat-<key>`.
  `jat migrate cleanup` opens a menu of everything that can be tidied — those
  backups, and migrations still sitting in your password manager — shows
  exactly what would go, and asks before deleting. `jat migrate cleanup <key>`
  takes one migration's backups and vault copy together; `--show` previews,
  `--yes` skips the question. `send` never bundles a backup.

### From a template instead of another machine

Lost the old machine, or setting up a first one? A **template** is a bundle
nobody had to send: files laid out as they sit under `$HOME`, plus the jat
steps to run (installs, login shell, macOS defaults). Receiving one goes
through the same picker, diffs and backups as a bundle.

```sh
jat template list                              # built-ins, and yours
jat init --template ohmybash                   # init, then apply it — or pick one at init's prompt
jat migrate receive --template iterm2-natural-text --show
jat migrate send --to-template my-mac          # save this machine's config as a template
```

- **Built-in templates** ship inside the binary, so a rebuilt machine has
  them before anything is cloned:
  - `ohmybash` — bash 5, oh-my-bash, bash as your login shell, and a
    `.bash_profile` that puts Homebrew on PATH and loads `.bashrc`.
  - `iterm2-natural-text` — iTerm2 keys that edit text like a Mac app
    (⌘←/→ line start/end, ⌥←/→ by word, ⌘⌫ delete line), as a dynamic
    profile made the default. Skipped entirely when iTerm2 isn't installed.
- **Your own** live in `~/.jat/templates/<name>/` (a name there hides a
  built-in of the same name), or anywhere, passed as a path. A template is:

  ```
  my-mac/template.json     {"description": "...", "extends": ["ohmybash"],
                            "profiles": ["macos"],
                            "when": {"iterm2": {"app": "iTerm"}},
                            "actions": [{"do": "install", "tool": "iterm2"}]}
  my-mac/home/.gitconfig   files at their $HOME-relative paths
  ```

  `extends` applies other templates first (this one's files win); `when`
  keeps a config item's files out unless the condition holds (`app`, `bin`,
  `profile`); actions are `install`, `login-shell`, `mac-dock-clean` and
  `mac-default`, each the same as running its jat command, and each shown as
  done or to do. Actions run before files, since an installer can create a
  file the template then replaces — and that file is backed up first.
- Every file must belong to a config item jat carries, and **templates never
  hold secrets**: a template with a key in it is refused before anything is
  written, and `send --to-template` will not take `--include-secrets`.
- `send --to-template` writes `template.json` only when it is missing, so the
  actions and conditions you added by hand survive a refresh.

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
  1Password the vault's type must be PERSONAL (named Private on a personal
  account, Employee on a Business one); in Bitwarden, items owned by
  an organization are never touched.
- jat only ever opens items carrying both its title pattern and its own
  mark, and it cannot run `op item get`, `op read`, `bw get item` or
  `bw get password` at all.
- Bitwarden has no desktop-app hand-off, so when its vault is locked jat runs
  `bw unlock` for you: bw asks for your master password and jat keeps the
  session key in memory for that one run. A `BW_SESSION` you exported
  yourself is used as-is. Signing in (`bw login`) stays yours to do once.
- `--key` matters only when more than one migration is waiting.
