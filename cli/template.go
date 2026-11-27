package cli

import (
	"archive/tar"
	"compress/gzip"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// A template is a bundle nobody had to send: a directory of files laid out as
// they sit under $HOME, plus a template.json naming its actions. Receiving one
// goes through the same classify → pick → back up → write path as a bundle
// from another machine, so there is one set of diff screens and one set of
// bugs. A template is a selection of things jat already does elsewhere — its
// files are config items from configs.go, its actions are jat subcommands —
// never a second implementation of them.
//
//	<template>/template.json    see templateSpec
//	<template>/home/<rel>       files, at their $HOME-relative paths
//
// Built-in templates ship inside the binary (templates/ beside this file), so
// a rebuilt machine has them before anything else is cloned. Names committed
// there are generic: they describe what the template does, never whose it is.
// Personal ones live in ~/.jat/templates/<name>, which shadows a built-in of
// the same name, or anywhere, passed as a path.

//go:embed all:templates
var builtinTemplates embed.FS

type templateSpec struct {
	Description string `json:"description"`
	// Profiles, when set, are the only profiles this template applies to.
	Profiles []string `json:"profiles,omitempty"`
	// Extends names templates applied first; this one's files win.
	Extends []string `json:"extends,omitempty"`
	// When gates a config item's files: the item is left out, and says why,
	// unless its condition holds on this machine.
	When    map[string]when  `json:"when,omitempty"`
	Actions []templateAction `json:"actions,omitempty"`
}

// when is a condition on this machine. Every field set must hold.
type when struct {
	Profile string `json:"profile,omitempty"`
	App     string `json:"app,omitempty"` // macOS app bundle, as Tool.App
	Bin     string `json:"bin,omitempty"` // executable on PATH
}

// holds reports whether the condition is true here, and if not, why.
func (w *when) holds(profile string) (bool, string) {
	if w == nil {
		return true, ""
	}
	if w.Profile != "" && w.Profile != profile {
		return false, "only for " + w.Profile
	}
	if w.App != "" && !appInstalled(w.App) {
		return false, w.App + " is not installed"
	}
	if w.Bin != "" {
		if _, err := lookPath(w.Bin); err != nil {
			return false, w.Bin + " is not on PATH"
		}
	}
	return true, ""
}

// appInstalled is a variable so tests never depend on what the machine has.
var appInstalled = func(app string) bool {
	home, _ := os.UserHomeDir()
	for _, dir := range []string{"/Applications", filepath.Join(home, "Applications")} {
		if _, err := os.Stat(filepath.Join(dir, app+".app")); err == nil {
			return true
		}
	}
	return false
}

var templateNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func userTemplateDir(home string) string { return filepath.Join(home, ".jat", "templates") }

// isTemplatePath says whether a --template argument is a path rather than a
// name: anything with a separator or starting with a dot.
func isTemplatePath(arg string) bool {
	return strings.ContainsAny(arg, `/\`) || strings.HasPrefix(arg, ".")
}

type templateSource struct {
	Name   string
	Origin string // "built-in", or the directory it was read from
	FS     fs.FS
}

func findTemplate(arg, home string) (templateSource, error) {
	if isTemplatePath(arg) {
		dir, err := filepath.Abs(arg)
		if err != nil {
			return templateSource{}, err
		}
		if _, err := os.Stat(filepath.Join(dir, "template.json")); err != nil {
			return templateSource{}, fmt.Errorf("%s is not a template (no template.json)", arg)
		}
		return templateSource{Name: filepath.Base(dir), Origin: dir, FS: os.DirFS(dir)}, nil
	}
	if !templateNameRe.MatchString(arg) {
		return templateSource{}, fmt.Errorf("%q is not a template name (letters, digits, . _ -)", arg)
	}
	dir := filepath.Join(userTemplateDir(home), arg)
	if _, err := os.Stat(filepath.Join(dir, "template.json")); err == nil {
		return templateSource{Name: arg, Origin: dir, FS: os.DirFS(dir)}, nil
	}
	if sub, err := fs.Sub(builtinTemplates, "templates/"+arg); err == nil {
		if _, err := fs.Stat(sub, "template.json"); err == nil {
			return templateSource{Name: arg, Origin: "built-in", FS: sub}, nil
		}
	}
	var names []string
	for _, t := range listTemplates(home) {
		names = append(names, t.Name)
	}
	return templateSource{}, fmt.Errorf("no template %q (known: %s)", arg, strings.Join(names, ", "))
}

type templateInfo struct {
	Name, Origin, Description string
	Shadows                   bool // a user template hiding a built-in of the same name
}

// listTemplates is every template jat can find by name, built-ins first.
func listTemplates(home string) []templateInfo {
	var out []templateInfo
	builtin := map[string]bool{}
	entries, _ := fs.ReadDir(builtinTemplates, "templates")
	for _, e := range entries {
		if spec, err := readSpec(builtinTemplates, "templates/"+e.Name()+"/template.json"); err == nil && e.IsDir() {
			builtin[e.Name()] = true
			out = append(out, templateInfo{Name: e.Name(), Origin: "built-in", Description: spec.Description})
		}
	}
	dir := userTemplateDir(home)
	entries, _ = os.ReadDir(dir)
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if spec, err := readSpec(os.DirFS(p), "template.json"); err == nil && templateNameRe.MatchString(e.Name()) {
			out = append(out, templateInfo{Name: e.Name(), Origin: p, Description: spec.Description, Shadows: builtin[e.Name()]})
		}
	}
	return out
}

func readSpec(fsys fs.FS, name string) (templateSpec, error) {
	var spec templateSpec
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		return spec, err
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields() // a typo in a condition must not silently mean "always"
	if err := dec.Decode(&spec); err != nil {
		return spec, fmt.Errorf("template.json: %w", err)
	}
	return spec, nil
}

type templateFile struct {
	Rel  string
	Data []byte
	Mode fs.FileMode
	From string // template name, for messages
}

// loadedTemplate is a template with its extends applied and its conditions
// decided for this machine.
type loadedTemplate struct {
	Name     string
	Origin   string
	Chain    []string // every template applied, bases first
	Desc     string
	Files    []templateFile
	Captured []string // config items the files belong to
	Skipped  []string // "iterm2 — iTerm is not installed"
	Actions  []templateAction
}

func loadTemplate(arg, home, profile string, tbl map[string]Tool) (*loadedTemplate, error) {
	lt := &loadedTemplate{}
	files := map[string]templateFile{}
	var order []string
	gates := map[string]when{}
	var stack []string

	var visit func(arg string) error
	visit = func(arg string) error {
		src, err := findTemplate(arg, home)
		if err != nil {
			return err
		}
		if slices.Contains(stack, src.Name) {
			return fmt.Errorf("template %s extends itself (%s)", src.Name, strings.Join(append(stack, src.Name), " → "))
		}
		if slices.Contains(lt.Chain, src.Name) {
			return nil // reached twice through two extends: apply once
		}
		spec, err := readSpec(src.FS, "template.json")
		if err != nil {
			return fmt.Errorf("template %s: %w", src.Name, err)
		}
		if len(spec.Profiles) > 0 && !slices.Contains(spec.Profiles, profile) {
			return fmt.Errorf("template %s is for %s; this machine is %s", src.Name, strings.Join(spec.Profiles, ", "), profile)
		}
		stack = append(stack, src.Name)
		for _, e := range spec.Extends {
			if err := visit(e); err != nil {
				return err
			}
		}
		stack = stack[:len(stack)-1]

		err = fs.WalkDir(src.FS, "home", func(p string, d fs.DirEntry, err error) error {
			if errors.Is(err, fs.ErrNotExist) && p == "home" {
				return fs.SkipDir // a template of actions only
			}
			if err != nil || !d.Type().IsRegular() {
				return err
			}
			rel, err := bundleRel(bundlePrefix + strings.TrimPrefix(p, "home/"))
			if err != nil {
				return fmt.Errorf("template %s: %w", src.Name, err)
			}
			data, err := fs.ReadFile(src.FS, p)
			if err != nil {
				return err
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			// Embedded files are read-only; what lands in $HOME is the owner's.
			mode := info.Mode().Perm() | 0o200
			if _, ok := files[rel]; !ok {
				order = append(order, rel)
			}
			files[rel] = templateFile{Rel: rel, Data: data, Mode: mode, From: src.Name}
			return nil
		})
		if err != nil {
			return err
		}
		for item, w := range spec.When {
			if _, ok := configs[item]; !ok {
				return fmt.Errorf("template %s: condition on %q, which is not a config item", src.Name, item)
			}
			gates[item] = w
		}
		for _, a := range spec.Actions {
			if err := a.validate(tbl); err != nil {
				return fmt.Errorf("template %s: %w", src.Name, err)
			}
			lt.Actions = append(lt.Actions, a)
		}
		lt.Chain = append(lt.Chain, src.Name)
		if len(stack) == 0 {
			lt.Name, lt.Origin, lt.Desc = src.Name, src.Origin, spec.Description
		}
		return nil
	}
	if err := visit(arg); err != nil {
		return nil, err
	}

	skipped := map[string]bool{}
	for _, rel := range order {
		f := files[rel]
		items := coveringItems(rel)
		if len(items) == 0 {
			return nil, fmt.Errorf("template %s: %s belongs to no config item jat carries", f.From, rel)
		}
		keep := true
		for _, name := range items {
			if configs[name].Secret {
				return nil, fmt.Errorf("template %s: %s is in %s, a secret item — templates never carry secrets", f.From, rel, name)
			}
			if w, ok := gates[name]; ok {
				if ok, why := w.holds(profile); !ok {
					keep = false
					if !skipped[name] {
						skipped[name] = true
						lt.Skipped = append(lt.Skipped, name+" — "+why)
					}
				}
			}
		}
		if !keep {
			continue
		}
		lt.Files = append(lt.Files, f)
		for _, name := range items {
			if !slices.Contains(lt.Captured, name) {
				lt.Captured = append(lt.Captured, name)
			}
		}
	}

	var kept []templateAction
	seen := map[string]bool{}
	for _, a := range lt.Actions {
		if seen[a.id()] {
			continue
		}
		seen[a.id()] = true
		if ok, why := a.When.holds(profile); !ok {
			lt.Skipped = append(lt.Skipped, a.id()+" — "+why)
			continue
		}
		kept = append(kept, a)
	}
	lt.Actions = kept
	return lt, nil
}

// coveringItems is every config item whose paths cover rel, in name order.
func coveringItems(rel string) []string {
	var out []string
	for _, name := range slices.Sorted(maps(configs)) {
		if coveredBy(rel, configs[name].Paths) {
			out = append(out, name)
		}
	}
	return out
}

// writeTemplateBundle serialises a loaded template's files as a bundle, so
// receive can treat it exactly like one sent from another machine.
func writeTemplateBundle(lt *loadedTemplate, dest, profile, key string) (Manifest, error) {
	man := Manifest{
		Created:  time.Now(),
		Host:     "template " + lt.Name,
		Profile:  profile,
		Key:      key,
		User:     osUsername(),
		Captured: lt.Captured,
	}
	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return man, err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, tf := range lt.Files {
		hdr := &tar.Header{
			Name: bundlePrefix + tf.Rel, Mode: int64(tf.Mode), Size: int64(len(tf.Data)),
			ModTime: man.Created, Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return man, err
		}
		if _, err := tw.Write(tf.Data); err != nil {
			return man, err
		}
	}
	if err := writeManifest(tw, man); err != nil {
		return man, err
	}
	if err := tw.Close(); err != nil {
		return man, err
	}
	if err := gz.Close(); err != nil {
		return man, err
	}
	return man, f.Close()
}

// cmdTemplate is `jat template [list]`.
func cmdTemplate(args []string) error {
	if len(args) > 0 && args[0] != "list" {
		return fmt.Errorf("usage: jat template [list]   (apply one: jat init --template <name>, or jat migrate receive --template <name>)")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	ts := listTemplates(home)
	if len(ts) == 0 {
		fmt.Println("no templates")
		return nil
	}
	for _, t := range ts {
		origin := t.Origin
		if t.Shadows {
			origin += " (hides the built-in)"
		}
		fmt.Printf("  %-22s %s\n  %-22s %s\n", t.Name, t.Description, "", origin)
	}
	fmt.Printf("\nyour own go in %s/<name> — make one with: jat migrate send --to-template <name>\n", userTemplateDir(home))
	return nil
}

// writeToTemplate is `migrate send --to-template`: the chosen items' files,
// copied into a template's home/ tree. template.json is written only when
// missing, so a hand-edited one (actions, conditions) survives a refresh.
func writeToTemplate(arg, home string, cfg Config, chosen []string, show bool) error {
	dir := arg
	if !isTemplatePath(arg) {
		if !templateNameRe.MatchString(arg) {
			return fmt.Errorf("%q is not a template name (letters, digits, . _ -)", arg)
		}
		dir = filepath.Join(userTemplateDir(home), arg)
	}
	verb := "wrote"
	if show {
		verb = "would write"
	}
	n := 0
	for _, name := range chosen {
		if configs[name].Secret {
			return fmt.Errorf("%s is a secret item — templates never carry secrets", name)
		}
		for _, rel := range expandPaths(home, configs[name].Paths) {
			err := filepath.WalkDir(filepath.Join(home, rel), func(p string, d fs.DirEntry, err error) error {
				if err != nil || !d.Type().IsRegular() || isBackupName(p) {
					return err
				}
				r := relOf(home, p)
				fmt.Printf("  %-18s %s\n", name, r)
				n++
				if show {
					return nil
				}
				data, err := os.ReadFile(p)
				if err != nil {
					return err
				}
				dest := filepath.Join(dir, "home", filepath.FromSlash(r))
				if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
					return err
				}
				info, err := d.Info()
				if err != nil {
					return err
				}
				return os.WriteFile(dest, data, info.Mode().Perm())
			})
			if err != nil {
				return err
			}
		}
	}
	spec := filepath.Join(dir, "template.json")
	if _, err := os.Stat(spec); errors.Is(err, fs.ErrNotExist) {
		fmt.Printf("  %-18s %s\n", "(new)", "template.json")
		if !show {
			b, _ := json.MarshalIndent(templateSpec{
				Description: fmt.Sprintf("captured from %s on %s", cfg.Host, time.Now().Format("2 Jan 2006")),
				Profiles:    []string{cfg.Profile},
			}, "", "  ")
			if err := os.WriteFile(spec, append(b, '\n'), 0o644); err != nil {
				return err
			}
		}
	}
	fmt.Printf("%s %d file(s) to template %s\n", verb, n, dir)
	if !show {
		fmt.Println("  add actions or conditions by editing its template.json; apply with: jat migrate receive --template " + arg)
	}
	return nil
}

// receiveTemplate applies a template: its actions first, then its files.
// Actions go first because they can create files — the oh-my-bash installer
// writes ~/.bashrc — so files are classified again after the actions ran,
// and anything an action created is backed up before a template file
// overwrites it, exactly as if it had been there all along.
func receiveTemplate(arg string, all, show bool) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	cfg, _ := loadConfig()
	profile := cfg.Profile
	if profile == "" {
		profile = detectProfile()
	}
	tbl := effectiveTools(cfg)
	lt, err := loadTemplate(arg, home, profile, tbl)
	if err != nil {
		return err
	}

	tmp, err := os.MkdirTemp("", "jat-template-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	key, err := migrationKey()
	if err != nil {
		return err
	}
	bundlePath := filepath.Join(tmp, "template.tar.gz")
	man, err := writeTemplateBundle(lt, bundlePath, profile, key)
	if err != nil {
		return err
	}
	_, items, err := readBundle(bundlePath, home)
	if err != nil {
		return err
	}

	type actionState struct {
		a     templateAction
		done  bool
		check error
	}
	var states []actionState
	var rows []PickRow
	for _, a := range lt.Actions {
		done, cerr := a.done(tbl, profile)
		states = append(states, actionState{a, done, cerr})
		rows = append(rows, actionRow(a, done, cerr, tbl, profile))
	}
	rows = append(rows, receiveRows(items, bundlePath, home, cfg.Preview)...)
	if len(rows) == 0 {
		fmt.Printf("template %s has nothing for this machine\n", lt.Name)
		for _, s := range lt.Skipped {
			fmt.Printf("  skipped    %s\n", s)
		}
		return nil
	}

	var chosen []string
	if all || show && !stdinIsTerminal() {
		for _, r := range rows {
			if r.Selected {
				chosen = append(chosen, r.ID)
			}
		}
	} else {
		sub := strings.Join(lt.Chain, " + ")
		if len(lt.Skipped) > 0 {
			sub += fmt.Sprintf(" · %d skipped", len(lt.Skipped))
		}
		picked, ok, err := runPicker("Apply template "+lt.Name+"?", sub, rows)
		if err != nil {
			return err
		}
		if !ok {
			releasePickerScreen()
			fmt.Fprintln(os.Stderr, "Cancelled — nothing changed.")
			return nil
		}
		chosen = picked
	}

	releasePickerScreen()
	fmt.Printf("template %s (%s): %s\n", lt.Name, lt.Origin, strings.Join(lt.Chain, " + "))
	for _, s := range lt.Skipped {
		fmt.Printf("  %-10s %s\n", "skipped", s)
	}
	for _, s := range states {
		state := "to do"
		switch {
		case s.check != nil:
			state = "unchecked"
		case s.done:
			state = "done"
		}
		fmt.Printf("  %-10s %s\n", state, s.a.id())
	}
	for _, it := range items {
		fmt.Printf("  %-10s %-16s %s\n", it.State(), it.Name, describeCounts(it))
	}

	var failed []string
	for _, s := range states {
		if !slices.Contains(chosen, actionRowID(s.a)) {
			continue
		}
		fmt.Printf("\n%s %s\n", map[bool]string{true: "would run", false: "running"}[show], s.a.id())
		if err := s.a.run(tbl, profile, show); err != nil {
			fmt.Printf("  ✗ %s: %v\n", s.a.id(), err)
			failed = append(failed, s.a.id())
		}
	}

	var chosenItems []string
	for _, it := range items {
		if slices.Contains(chosen, it.Name) {
			chosenItems = append(chosenItems, it.Name)
		}
	}
	if len(chosenItems) > 0 {
		if !show {
			if _, items, err = readBundle(bundlePath, home); err != nil {
				return err
			}
		}
		want := map[string]string{}
		overwrites := false
		for _, it := range items {
			if !slices.Contains(chosenItems, it.Name) {
				continue
			}
			for _, f := range it.Files {
				if f.State != stateIdentical {
					want[f.Rel] = f.State
					overwrites = overwrites || f.State == stateDiffers
				}
			}
		}
		fmt.Println()
		if len(want) == 0 {
			fmt.Println("files: nothing to write — everything chosen is already identical")
		} else {
			n, err := applyBundle(bundlePath, home, man.Key, want, show, os.Stdout)
			if err != nil {
				return fmt.Errorf("stopped after %d file(s): %w", n, err)
			}
			verb := map[bool]string{true: "would write", false: "wrote"}[show]
			fmt.Printf("%s %d file(s) across %d item(s)\n", verb, n, len(chosenItems))
			if !show && overwrites {
				fmt.Printf("overwritten files are kept as *%s — when you are happy: jat migrate cleanup %s\n",
					backupSuffix(man.Key), backupKey(man.Key))
			}
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("%d action(s) failed: %s", len(failed), strings.Join(failed, ", "))
	}
	return nil
}

// initTemplatePrompt is init's optional last step: offer the templates, with
// "none" first and chosen, so a bare `jat init` stays a bare init.
func initTemplatePrompt(home string) (string, error) {
	ts := listTemplates(home)
	if len(ts) == 0 {
		return "", nil
	}
	rows := []PickRow{{ID: "", Label: "None", Note: "just save the profile and host"}}
	for _, t := range ts {
		rows = append(rows, PickRow{ID: t.Name, Label: t.Name, Note: t.Description})
	}
	choice, ok, err := runPickerOne("Apply a template now?", "settings and installs you can review item by item", rows)
	if err != nil || !ok {
		releasePickerScreen()
		return "", err
	}
	return choice, nil
}

// templateFlag adds --template to a FlagSet; shared by init and receive.
func templateFlag(fs *flag.FlagSet) *string {
	return fs.String("template", "", "apply a template: a name (jat template list) or a directory")
}
