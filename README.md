# jat-util
`jat` is a general-purpose machine utility: one binary for installing tools,
managing configs, moving between machines, and whatever other housekeeping
jobs turn out to be worth a subcommand. Migration is one function among many.

Current subcommands: `init`, `install`, `list`, `migrate` (`send`, `receive`,
`inspect`), `update`, `release`, `version`.

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
- `inspect` never prints file contents.
