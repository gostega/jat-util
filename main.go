package main

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
}

func main() {
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
	case "export":
		err = cmdExport(os.Args[2:])
	case "import":
		err = cmdImport(os.Args[2:])
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

	if err != nil {
		fmt.Fprintln(os.Stderr, "jat:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage:
  jat init [--profile <name>] [--host <name>]
                                     save the OS profile and this machine's name
  jat install <tool> [--<method>] [--show]
  jat list                           known tools and their default method
  jat migrate send [--transport file] [--out <file>] [--all] [--include-secrets]
                                     pick config and bundle it for another machine
  jat export [--out <file>] [--include-secrets] [--show]
                                     bundle known config for another machine
  jat import <bundle> [--show] [--force]
  jat update [--check]               replace this binary with the latest release
  jat release [patch|minor|major]    tag and push a new release
  jat version

methods: `+strings.Join(allMethods, ", ")+`
`)
}

func cmdInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	profile := fs.String("profile", "", "profile to use (default: detected from this OS)")
	host := fs.String("host", "", "name for this machine (default: its hostname)")
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

	if err := saveConfig(Config{Profile: p, Host: h}); err != nil {
		return err
	}
	fmt.Printf("profile %q, host %q saved to %s\n", p, h, configPath())
	return nil
}

func cmdInstall(args []string) error {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	show := fs.Bool("show", false, "print the command that would run, and where to verify it")
	var override string
	for _, m := range allMethods {
		fs.BoolFunc(m, "force install via "+m, func(string) error { override = m; return nil })
	}
	fs.Parse(flagsFirst(args))

	if fs.NArg() != 1 {
		return errors.New("usage: jat install <tool> [--<method>] [--show]")
	}
	name := fs.Arg(0)
	tool, ok := tools[name]
	if !ok {
		return fmt.Errorf("unknown tool %q (try: jat list)", name)
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	method, cmd, err := resolve(tool, cfg.Profile, override)
	if err != nil {
		return err
	}

	if *show {
		fmt.Printf("tool:    %s\nprofile: %s\nmethod:  %s\n", name, cfg.Profile, method)
		if cmd == "" {
			fmt.Println("command: (not automatable)")
		} else {
			fmt.Printf("command: %s\n", cmd)
		}
		if tool.Docs != "" {
			fmt.Printf("verify:  %s\n", tool.Docs)
		}
		return nil
	}

	if cmd == "" {
		if method == "appstore" {
			fmt.Printf("%s: install from the Mac App Store\n", name)
			return nil
		}
		fmt.Printf("%s: manual install — download from %s\n", name, tool.Docs)
		return nil
	}

	fmt.Printf("==> [%s] %s\n", method, cmd)
	c := exec.Command("sh", "-c", cmd)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return c.Run()
}

func cmdList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	fs.Parse(args)

	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	for _, name := range slices.Sorted(maps(tools)) {
		method, _, err := resolve(tools[name], cfg.Profile, "")
		if err != nil {
			fmt.Printf("%-20s %s\n", name, "—")
			continue
		}
		fmt.Printf("%-20s %s\n", name, method)
	}
	return nil
}

// resolve picks the install method for a tool: an explicit override if given,
// otherwise the first method in the profile's preference order that the tool has.
func resolve(t Tool, profile, override string) (method, cmd string, err error) {
	if override != "" {
		cmd, ok := t.Methods[override]
		if !ok {
			return "", "", fmt.Errorf("no %s install method (has: %s)", override, strings.Join(slices.Sorted(maps(t.Methods)), ", "))
		}
		return override, cmd, nil
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
// stops doing at the first positional ("jat install gh --show").
// ponytail: safe only because every install flag is boolean; a flag taking a
// separate value would need pflag instead.
func flagsFirst(args []string) []string {
	var flags, positional []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
		} else {
			positional = append(positional, a)
		}
	}
	return append(flags, positional...)
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
