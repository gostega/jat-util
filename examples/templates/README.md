# Example templates

Templates to copy and edit, not to apply as they stand. The built-in ones
(`jat template list`) each do one job; these show how to combine them.

```sh
cp -R examples/templates/mac-dev ~/.jat/templates/my-mac
$EDITOR ~/.jat/templates/my-mac/template.json
jat migrate receive --template my-mac --show    # see exactly what it would do
jat init --template my-mac                       # on a new machine
```

`mac-dev` shows:

- **`extends`** — the built-in `ohmybash` and `iterm2-natural-text` are
  applied first; anything this template adds or overrides wins.
- **actions** — installs and `mac-dock-clean`, each the same as running its
  `jat` command, each shown as done or to do in the picker.
- **`when`** — `home/.config/ghostty/config` is only offered when Ghostty is
  installed, so one template suits an iTerm2 machine and a Ghostty one.

The fastest way to start your own is from a machine you already like:
`jat migrate send --to-template my-mac`, then add `extends` and actions to
the `template.json` it writes. Keep personal templates out of public repos.
