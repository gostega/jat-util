# jat-util
General helper utility for installs, configs.

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
