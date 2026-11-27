package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// Stamped in at link time by the Makefile / release workflow. A binary still
// reporting "dev" was built straight from source, which is how `jat update`
// knows not to replace it.
var (
	Version = "dev"
	Commit  = "unknown"
)

// Profile is the OS axis: it decides how things get installed. Host is the
// machine axis: work and personal Macs share a profile but not their config,
// so config selection keys off this instead.
type Config struct {
	Profile string `json:"profile"`
	Host    string `json:"host"`
	// Vault is the private password-manager vault jat may write to, and the
	// manager it lives in. Chosen and checked by `jat vault set`; never a
	// constant, since the manager, name and id differ per account.
	Vault *VaultRef `json:"vault,omitempty"`
	// Preview holds the picker's preview-pane preferences.
	Preview PreviewPrefs `json:"preview,omitempty"`
	// Installers and Defaults are the user's layer over the built-in tool
	// table: see installers.go.
	Installers map[string]Installer `json:"installers,omitempty"`
	Defaults   map[string]string    `json:"defaults,omitempty"`
}

type PreviewPrefs struct {
	// HideSecretNames hides even the file names of a Secret item in the
	// pane (counts and size only), for screen shares. Contents are never
	// shown either way.
	HideSecretNames bool `json:"hideSecretNames,omitempty"`
}

// Main is the whole program; main.go at the module root just calls it.
func Main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "init":
		err = cmdInit(os.Args[2:])
	case "install":
		err = cmdInstall(os.Args[2:])
	case "list":
		err = cmdList(os.Args[2:])
	case "migrate":
		err = cmdMigrate(os.Args[2:])
	case "vault":
		err = cmdVault(os.Args[2:])
	case "mac":
		err = cmdMac(os.Args[2:])
	case "template", "templates":
		err = cmdTemplate(os.Args[2:])
	case "shell":
		err = cmdShell(os.Args[2:])
	case "update":
		err = cmdUpdate(os.Args[2:])
	case "release":
		err = cmdRelease(os.Args[2:])
	case "version", "--version":
		fmt.Printf("jat %s (%s)\n", Version, Commit)
	default:
		usage()
		os.Exit(2)
	}

	releasePickerScreen()
	if err != nil {
		fmt.Fprintln(os.Stderr, "jat:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage:
  jat init [--profile <name>] [--host <name>] [--template <name|dir> [--all]]
                                     save the OS profile and this machine's name; optionally
                                     apply a template (asks, defaulting to none, on a terminal)
  jat install                        menu: pick tools by group, see what depends on what
  jat install <tool> [--<method>]    one tool by this profile's default method
  jat install --all                  everything jat knows that is not yet installed
      … [--dry-run|--preview|--show] print the plan, run nothing
  jat install --self [--yes]         put this binary on your PATH (a fresh download installs itself)
  jat install add-installer <name> --method <method> --command "<cmd>" [--docs <url>] [--force]
  jat install set-default <tool> <installer|method>
  jat install unset-default <tool>  |  remove-installer <name>  |  list-installers
                                     your own installers and per-tool defaults (~/.jat/config.json)
  jat list                           known tools and their default method
  jat migrate send [--transport file|vault] [--out <file>] [--all] [--include-secrets] [--show]
                                     pick config and bundle it for another machine
  jat migrate send --to-template <name|dir> [--all] [--show]
                                     save the chosen config as a template (never secrets)
  jat migrate receive [<bundle> | --transport vault [--key <key>] | --template <name|dir>] [--all] [--show]
                                     apply a bundle or template: absent / identical / differs per item
  jat migrate inspect <bundle> | --key <key> [--files]
                                     what a bundle holds, without applying it
  jat migrate cleanup [<key>] [--show] [--yes]
                                     tidy up backups here and migrations in the vault
  jat vault [set [--manager 1password|bitwarden] [<name or id>]]
                                     the private password-manager vault jat may use
  jat template [list]                templates: built in, and yours in ~/.jat/templates
  jat shell <tool> [--show]          make an installed shell the login shell (/etc/shells + chsh)
  jat mac dock-clean [--show]        take Apple's bundled apps out of the Dock, keep the rest
  jat mac default <domain> <key> -string|-bool|-int <value> [--show]
                                     defaults write, skipped when already set
  jat update [--check]             replace this binary with the latest release
  jat release [patch|minor|major]    tag and push a new release
  jat version

methods: `+strings.Join(allMethods, ", ")+`
`)
}

func cmdInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	profile := fs.String("profile", "", "profile to use (default: detected from this OS)")
	host := fs.String("host", "", "name for this machine (default: its hostname)")
	tpl := templateFlag(fs)
	all := fs.Bool("all", false, "with --template: apply everything it offers, skipping the picker")
	fs.Parse(args)

	p := *profile
	if p == "" {
		p = detectProfile()
	}
	if _, ok := profileOrder[p]; !ok {
		return fmt.Errorf("unknown profile %q (known: %s)", p, strings.Join(knownProfiles(), ", "))
	}

	h := slugHost(*host)
	if h == "" {
		if *host != "" {
			return fmt.Errorf("host %q has no usable characters (want letters, digits or -)", *host)
		}
		h = detectHost()
	}

	// Re-running init must not forget a vault that was chosen separately.
	cfg, _ := loadConfig()
	cfg.Profile, cfg.Host = p, h
	if err := saveConfig(cfg); err != nil {
		return err
	}
	fmt.Printf("profile %q, host %q saved to %s\n", p, h, configPath())

	// A bare init stays bare: a template is applied only when named, or
	// chosen from the prompt, whose first and default row is "None".
	name := *tpl
	if name == "" && stdinIsTerminal() {
		home, _ := os.UserHomeDir()
		var err error
		if name, err = initTemplatePrompt(home); err != nil {
			return err
		}
	}
	if name == "" {
		return nil
	}
	return receiveTemplate(name, *all, false)
}

func cmdInstall(args []string) error {
	if handled, err := cmdInstallConfig(args); handled {
		return err
	}
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	show := fs.Bool("show", false, "print what would run, and run nothing (same as --dry-run)")
	dryRun := fs.Bool("dry-run", false, "print the plan and run nothing")
	preview := fs.Bool("preview", false, "same as --dry-run")
	all := fs.Bool("all", false, "every tool not already installed, skipping the menu")
	self := fs.Bool("self", false, "install this binary onto your PATH (same as: jat install jat)")
	yes := fs.Bool("yes", false, "with --self: add to PATH without asking")
	var override string
	for _, m := range allMethods {
		fs.BoolFunc(m, "force install via "+m, func(string) error { override = m; return nil })
	}
	fs.Parse(flagsFirst(fs, args))
	plan := *show || *dryRun || *preview

	if *self || (fs.NArg() == 1 && fs.Arg(0) == "jat") {
		return selfInstall(plan, *yes)
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	// The user's installers and defaults apply to every path below: the
	// named tool, --all and the menu all read the same table.
	tbl := effectiveTools(cfg)

	// One named tool, the original form: resolve, show or run.
	if fs.NArg() == 1 && !*all {
		name := fs.Arg(0)
		tool, ok := tbl[name]
		if !ok {
			return fmt.Errorf("unknown tool %q (try: jat list, or jat install add-installer)", name)
		}
		method, cmd, err := resolve(tool, cfg.Profile, override)
		if err != nil {
			return err
		}
		if plan {
			fmt.Printf("tool:    %s\nprofile: %s\nmethod:  %s\n", name, cfg.Profile, method)
			if tool.Installer != "" && method == tool.Prefer {
				fmt.Printf("from:    your installer %q (%s)\n", tool.Installer, configPath())
			}
			if cmd == "" {
				fmt.Println("command: (not automatable)")
			} else {
				fmt.Printf("command: %s\n", cmd)
			}
			if tool.Docs != "" {
				fmt.Printf("verify:  %s\n", tool.Docs)
			}
			if deps := toolDeps(tbl, name, cfg.Profile); len(deps) > 0 {
				fmt.Printf("needs:   %s\n", strings.Join(deps, ", "))
			}
			return nil
		}
		if cmd == "" {
			fmt.Printf("%s has no automatable install on %s (%s).\n", name, cfg.Profile, method)
			if tool.Docs != "" {
				fmt.Printf("Get it here: %s\n", tool.Docs)
			}
			return nil
		}
		fmt.Printf("$ %s\n", cmd)
		c := exec.Command("sh", "-c", cmd)
		c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
		return c.Run()
	}
	if fs.NArg() > 1 {
		return errors.New("usage: jat install [<tool>] [--<method>] [--all] [--dry-run|--preview|--show]  |  jat install --self")
	}

	// No tool named: everything not yet installed (--all), or the menu.
	var chosen []string
	if *all {
		for _, n := range slices.Sorted(maps(tbl)) {
			if _, _, err := resolve(tbl[n], cfg.Profile, ""); err == nil && !installed(tbl, n) {
				chosen = append(chosen, n)
			}
		}
	} else {
		if !stdinIsTerminal() {
			return errors.New("usage: jat install <tool> — or --all to install everything not yet here (add --dry-run to see the plan)")
		}
		picked, ok, err := runPicker("What should be installed on this machine?",
			fmt.Sprintf("profile %s · host %s · ticking a tool ticks what it needs", cfg.Profile, cfg.Host),
			installRows(tbl, cfg.Profile))
		if err != nil {
			return err
		}
		if !ok || len(picked) == 0 {
			releasePickerScreen()
			fmt.Fprintln(os.Stderr, "Nothing chosen — nothing installed.")
			return nil
		}
		chosen = picked
	}
	releasePickerScreen()
	if len(chosen) == 0 {
		fmt.Println("everything jat knows is already installed")
		return nil
	}
	steps, err := buildPlan(tbl, chosen, cfg.Profile)
	if err != nil {
		return err
	}
	if plan {
		printPlan(steps, cfg.Profile)
		return nil
	}
	return runPlan(tbl, steps, cfg.Profile)
}

// installRows is the tool table as picker rows: the dependency tree with
// glyphs, one tab per group, installed tools dimmed, and a preview per
// tool showing the command, docs, and what it needs and is needed by.
func installRows(tbl map[string]Tool, profile string) []PickRow {
	tree := depTree(tbl, profile)
	// Glyphs: a row's own connector, preceded by the trunk lines of its
	// ancestors that still have siblings below.
	var rows []PickRow
	openAt := map[int]bool{} // depth → an ancestor at that depth has more siblings
	for _, tr := range tree {
		prefix := ""
		for d := 0; d < tr.Depth-1; d++ {
			if openAt[d] {
				prefix += "│  "
			} else {
				prefix += "   "
			}
		}
		if tr.Depth > 0 {
			if tr.Last {
				prefix += "└─ "
			} else {
				prefix += "├─ "
			}
		}
		openAt[tr.Depth-1] = !tr.Last
		if tr.Depth == 0 {
			openAt = map[int]bool{}
		}

		name := tr.Name
		t := tbl[name]
		method, cmd, err := resolve(t, profile, "")
		note, colour := "", ""
		switch {
		case err != nil:
			note, colour = "no install method on "+profile, "9"
		case cmd == "":
			where := t.Docs
			if where == "" && method == "appstore" {
				where = "App Store"
			}
			note, colour = fmt.Sprintf("%-9s %s", method, orUnknown(where)), "11"
		default:
			note = fmt.Sprintf("%-9s %s", method, cmd)
		}
		isIn := installed(tbl, name)
		if isIn {
			note, colour = "installed", "10"
		}
		rows = append(rows, PickRow{
			ID: name, Label: name, Note: note, ShortNote: method, NoteColor: colour,
			Group: t.Group, Tree: prefix, Needs: toolDeps(tbl, name, profile),
			// Installed, or uninstallable here: greyed and left out of
			// select-all, but still tickable on purpose.
			Dim:     isIn || err != nil,
			Preview: func() Preview { return installPreview(tbl, name, profile) },
		})
	}
	return rows
}

func installPreview(tbl map[string]Tool, name, profile string) Preview {
	t := tbl[name]
	p := Preview{Title: name}
	method, cmd, err := resolve(t, profile, "")
	switch {
	case err != nil:
		p.Lines = append(p.Lines, pl('!', err.Error()))
	case cmd == "":
		p.Lines = append(p.Lines, head("method  "+method), dim("not automatable — get it from:"), pl(' ', orUnknown(t.Docs)))
	default:
		p.Lines = append(p.Lines, head("method  "+method), pl(' ', cmd))
		if t.Docs != "" {
			p.Lines = append(p.Lines, dim(""), dim("verify: "+t.Docs))
		}
	}
	if installed(tbl, name) {
		p.Lines = append(p.Lines, dim(""), pl('+', "installed on this machine"))
	}
	var others []string
	for m, c := range t.Methods {
		if m != method && c != "" {
			others = append(others, m)
		}
	}
	slices.Sort(others)
	if len(others) > 0 {
		p.Lines = append(p.Lines, dim(""), dim("also available via --"+strings.Join(others, ", --")))
	}
	if deps := toolDeps(tbl, name, profile); len(deps) > 0 {
		p.Lines = append(p.Lines, dim(""), head("needs"), pl(' ', strings.Join(deps, ", ")))
	}
	var by []string
	for _, n := range slices.Sorted(maps(tbl)) {
		if slices.Contains(toolDeps(tbl, n, profile), name) {
			by = append(by, n)
		}
	}
	if len(by) > 0 {
		p.Lines = append(p.Lines, dim(""), head("needed by"), pl(' ', strings.Join(by, ", ")))
	}
	return p
}

func cmdList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	fs.Parse(args)

	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	all := effectiveTools(cfg)
	for _, name := range slices.Sorted(maps(all)) {
		method, _, err := resolve(all[name], cfg.Profile, "")
		if err != nil {
			fmt.Printf("%-20s %s\n", name, "—")
			continue
		}
		if all[name].Installer != "" {
			method += " (" + all[name].Installer + ")"
		}
		fmt.Printf("%-20s %s\n", name, method)
	}
	return nil
}

// resolve picks the install method for a tool: an explicit override if given,
// then the user's preferred method, otherwise the first method in the
// profile's preference order that the tool has.
func resolve(t Tool, profile, override string) (method, cmd string, err error) {
	if override != "" {
		cmd, ok := t.Methods[override]
		if !ok {
			return "", "", fmt.Errorf("no %s install method (has: %s)", override, strings.Join(slices.Sorted(maps(t.Methods)), ", "))
		}
		return override, cmd, nil
	}
	if cmd, ok := t.Methods[t.Prefer]; ok && t.Prefer != "" {
		return t.Prefer, cmd, nil
	}
	order, ok := profileOrder[profile]
	if !ok {
		return "", "", fmt.Errorf("unknown profile %q — run: jat init", profile)
	}
	for _, m := range order {
		if cmd, ok := t.Methods[m]; ok {
			return m, cmd, nil
		}
	}
	return "", "", fmt.Errorf("no install method for profile %q", profile)
}

// flagsFirst reorders args so stdlib flag sees every flag, which it otherwise
// stops doing at the first positional ("jat install gh --show"). A flag that
// takes a value keeps the argument after it: hoisting "--transport" away from
// "file" handed the next flag to it as a value instead.
func flagsFirst(fs *flag.FlagSet, args []string) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			// Kept, so a positional that starts with "-" stays one.
			positional = append(positional, args[i:]...)
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			positional = append(positional, a)
			continue
		}
		flags = append(flags, a)
		name, _, inline := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if inline || i+1 >= len(args) {
			continue
		}
		if f := fs.Lookup(name); f != nil && !isBoolFlag(f) {
			i++
			flags = append(flags, args[i])
		}
	}
	return append(flags, positional...)
}

func isBoolFlag(f *flag.Flag) bool {
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
}

// slugHost reduces a host name to [a-z0-9-]. The value ends up as a path
// segment when config is looked up per-machine, so it must not be able to
// carry a separator or traverse upwards.
func slugHost(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.' || r == ' ':
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// detectHost falls back to the machine's own hostname. Ugly but stable and
// unique, and `jat init --host` exists for when you want a name you chose.
func detectHost() string {
	name, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	// macOS hostnames arrive as "thing.local" / "thing.lan"; the suffix is
	// noise that changes with the network.
	name, _, _ = strings.Cut(name, ".")
	if h := slugHost(name); h != "" {
		return h
	}
	return "unknown"
}

func detectProfile() string {
	switch runtime.GOOS {
	case "darwin":
		return "macos"
	case "linux":
		// ponytail: debian-family only; add rhel/arch when a machine actually needs one
		return "debian"
	}
	return runtime.GOOS
}

func configPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".jat/config.json"
	}
	return filepath.Join(home, ".jat", "config.json")
}

// loadConfig falls back to the detected profile so every command works before
// `jat init` has ever run.
func loadConfig() (Config, error) {
	b, err := os.ReadFile(configPath())
	if errors.Is(err, fs.ErrNotExist) {
		return Config{Profile: detectProfile(), Host: detectHost()}, nil
	}
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return Config{}, fmt.Errorf("%s: %w", configPath(), err)
	}
	return c, nil
}

func saveConfig(c Config) error {
	p := configPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(b, '\n'), 0o644)
}

func knownProfiles() []string {
	return slices.Sorted(maps(profileOrder))
}

func maps[V any](m map[string]V) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}
