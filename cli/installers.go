package cli

import (
	"errors"
	"flag"
	"fmt"
	stdmaps "maps"
	"regexp"
	"slices"
	"strings"
)

// Installer is a user-defined install method: one of the known method kinds,
// with the command the user wants run for it. It lives in ~/.jat/config.json,
// so a new upstream installer needs no jat release.
type Installer struct {
	Method  string `json:"method"`
	Command string `json:"command,omitempty"`
	Docs    string `json:"docs,omitempty"`
}

// The layers, lowest first: the built-in table in tools.go, then the user's
// config (installers, and a default per tool), then --<method> on the command
// line. A tool's default names either one of the user's installers or a
// method the tool already has.
//
// Choosing an installer as a tool's default also makes it that tool's entry
// for the installer's method, so `jat install claude --curlscript` runs the
// user's curlscript, not a built-in one (or an error, if there was none).

var installerName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// effectiveTools is the built-in table with the user's layer applied. The
// built-in maps are copied, never written through.
func effectiveTools(cfg Config) map[string]Tool {
	out := make(map[string]Tool, len(tools)+len(cfg.Defaults))
	for name, t := range tools {
		out[name] = t
	}
	for name, choice := range cfg.Defaults {
		t := out[name]
		t.Methods = stdmaps.Clone(t.Methods)
		if t.Methods == nil {
			t.Methods = map[string]string{}
		}
		if inst, ok := cfg.Installers[choice]; ok {
			t.Methods[inst.Method] = inst.Command
			t.Prefer = inst.Method
			t.Installer = choice
			if inst.Docs != "" {
				t.Docs = inst.Docs
			}
		} else {
			t.Prefer = choice
		}
		out[name] = t
	}
	return out
}

func validateInstaller(name string, inst Installer) error {
	if !installerName.MatchString(name) {
		return fmt.Errorf("installer name %q: want lowercase letters, digits, '.', '_' or '-'", name)
	}
	if slices.Contains(allMethods, name) {
		return fmt.Errorf("installer name %q is a method name; pick another so defaults stay unambiguous", name)
	}
	if !slices.Contains(allMethods, inst.Method) {
		return fmt.Errorf("unknown method %q (known: %s)", inst.Method, strings.Join(allMethods, ", "))
	}
	automatable := inst.Method != "manual" && inst.Method != "appstore"
	if automatable && strings.TrimSpace(inst.Command) == "" {
		return fmt.Errorf("method %s needs --command", inst.Method)
	}
	if !automatable && inst.Command != "" {
		return fmt.Errorf("method %s is not automated; give --docs instead of --command", inst.Method)
	}
	return nil
}

// cmdInstallConfig handles `jat install <subcommand>`; handled is false when
// args[0] is not one, so the caller treats it as a tool name.
func cmdInstallConfig(args []string) (handled bool, err error) {
	if len(args) == 0 {
		return false, nil
	}
	switch args[0] {
	case "add-installer":
		return true, cmdAddInstaller(args[1:])
	case "remove-installer":
		return true, cmdRemoveInstaller(args[1:])
	case "set-default":
		return true, cmdSetDefault(args[1:])
	case "unset-default":
		return true, cmdUnsetDefault(args[1:])
	case "list-installers":
		return true, cmdListInstallers(args[1:])
	}
	return false, nil
}

func cmdAddInstaller(args []string) error {
	fs := flag.NewFlagSet("install add-installer", flag.ExitOnError)
	method := fs.String("method", "", "method kind: "+strings.Join(allMethods, ", "))
	command := fs.String("command", "", "shell command that performs the install")
	docs := fs.String("docs", "", "page to verify the command against")
	force := fs.Bool("force", false, "replace an installer of the same name")
	fs.Parse(flagsFirst(fs, args))
	if fs.NArg() != 1 {
		return errors.New(`usage: jat install add-installer <name> --method <method> --command "<cmd>" [--docs <url>] [--force]`)
	}
	name := fs.Arg(0)
	inst := Installer{Method: *method, Command: *command, Docs: *docs}
	if err := validateInstaller(name, inst); err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if _, ok := cfg.Installers[name]; ok && !*force {
		return fmt.Errorf("installer %q already exists (use --force to replace it)", name)
	}
	if cfg.Installers == nil {
		cfg.Installers = map[string]Installer{}
	}
	cfg.Installers[name] = inst
	if err := saveConfig(cfg); err != nil {
		return err
	}
	fmt.Printf("installer %q saved to %s\n", name, configPath())
	return nil
}

func cmdRemoveInstaller(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: jat install remove-installer <name>")
	}
	name := args[0]
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if _, ok := cfg.Installers[name]; !ok {
		return fmt.Errorf("no installer %q", name)
	}
	var users []string
	for tool, choice := range cfg.Defaults {
		if choice == name {
			users = append(users, tool)
		}
	}
	if len(users) > 0 {
		slices.Sort(users)
		return fmt.Errorf("installer %q is the default for %s; unset-default first", name, strings.Join(users, ", "))
	}
	delete(cfg.Installers, name)
	if err := saveConfig(cfg); err != nil {
		return err
	}
	fmt.Printf("installer %q removed\n", name)
	return nil
}

func cmdSetDefault(args []string) error {
	if len(args) != 2 {
		return errors.New("usage: jat install set-default <tool> <installer|method>")
	}
	tool, choice := args[0], args[1]
	if !installerName.MatchString(tool) {
		return fmt.Errorf("tool name %q: want lowercase letters, digits, '.', '_' or '-'", tool)
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if _, ok := cfg.Installers[choice]; !ok {
		builtin, known := tools[tool]
		if !slices.Contains(allMethods, choice) {
			return fmt.Errorf("%q is neither one of your installers nor a method (see: jat install list-installers)", choice)
		}
		if _, has := builtin.Methods[choice]; !known || !has {
			return fmt.Errorf("%s has no built-in %s method; add an installer for it first", tool, choice)
		}
	}
	if cfg.Defaults == nil {
		cfg.Defaults = map[string]string{}
	}
	cfg.Defaults[tool] = choice
	if err := saveConfig(cfg); err != nil {
		return err
	}
	fmt.Printf("jat install %s now uses %s\n", tool, choice)
	return nil
}

func cmdUnsetDefault(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: jat install unset-default <tool>")
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if _, ok := cfg.Defaults[args[0]]; !ok {
		return fmt.Errorf("no default set for %q", args[0])
	}
	delete(cfg.Defaults, args[0])
	if err := saveConfig(cfg); err != nil {
		return err
	}
	fmt.Printf("%s is back to its built-in default\n", args[0])
	return nil
}

func cmdListInstallers(args []string) error {
	if len(args) != 0 {
		return errors.New("usage: jat install list-installers")
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if len(cfg.Installers) == 0 && len(cfg.Defaults) == 0 {
		fmt.Println("no installers or defaults configured (see: jat install add-installer)")
		return nil
	}
	if len(cfg.Installers) > 0 {
		fmt.Println("installers:")
		for _, name := range slices.Sorted(maps(cfg.Installers)) {
			inst := cfg.Installers[name]
			cmd := inst.Command
			if cmd == "" {
				cmd = "(not automatable)"
			}
			fmt.Printf("  %-20s %-11s %s\n", name, inst.Method, cmd)
		}
	}
	if len(cfg.Defaults) > 0 {
		fmt.Println("defaults:")
		for _, tool := range slices.Sorted(maps(cfg.Defaults)) {
			fmt.Printf("  %-20s %s\n", tool, cfg.Defaults[tool])
		}
	}
	return nil
}
