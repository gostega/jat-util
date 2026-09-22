package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"
)

// `jat vault`: which password manager, and which vault in it, the vault
// transport may use. Everything here is written against Connector; the
// managers themselves are onepassword.go and bitwarden.go.

func cmdVault(args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if len(args) == 0 {
		if cfg.Vault == nil {
			fmt.Println("no vault chosen — run: jat vault set")
			return nil
		}
		c, err := connectorNamed(cfg.Vault.Manager)
		label := cfg.Vault.Manager
		if err == nil {
			label = c.Label()
		}
		fmt.Printf("%s · %s (%s)\n", label, cfg.Vault.Name, cfg.Vault.ID)
		return nil
	}
	if args[0] != "set" {
		return fmt.Errorf("usage: jat vault [set [--manager %s] [<name or id>]]", strings.Join(connectorNames(), "|"))
	}

	fs_ := flag.NewFlagSet("vault set", flag.ExitOnError)
	manager := fs_.String("manager", "", "which password manager: "+strings.Join(connectorNames(), ", "))
	fs_.Parse(flagsFirst(fs_, args[1:]))
	if fs_.NArg() > 1 {
		return fmt.Errorf("usage: jat vault set [--manager <name>] [<name or id>]")
	}

	c, err := chooseConnector(*manager)
	if err != nil || c == nil {
		return err
	}
	if err := c.Available(); err != nil {
		return err
	}

	want := fs_.Arg(0)
	if want == "" {
		if want, err = chooseVault(c); err != nil || want == "" {
			return err
		}
	}
	ref, err := c.Validate(want)
	if err != nil {
		return err
	}
	releasePickerScreen()
	cfg.Vault = &ref
	if err := saveConfig(cfg); err != nil {
		return err
	}
	fmt.Printf("%s vault %q (%s) saved to %s\n", c.Label(), ref.Name, ref.ID, configPath())
	return nil
}

// chooseConnector takes --manager, else the one manager installed, else
// asks. A nil connector with a nil error means the person cancelled.
func chooseConnector(name string) (Connector, error) {
	if name != "" {
		return connectorNamed(name)
	}
	installed := installedConnectors()
	switch len(installed) {
	case 0:
		return nil, fmt.Errorf("no password manager CLI found — install one (jat install op, jat install bw) or pass --manager")
	case 1:
		return installed[0], nil
	}
	if !stdinIsTerminal() {
		return nil, fmt.Errorf("several password managers installed — pass --manager %s", strings.Join(connectorNames(), "|"))
	}
	rows := make([]PickRow, 0, len(installed))
	for _, c := range installed {
		rows = append(rows, PickRow{ID: c.Name(), Label: c.Label(), Note: "via " + cliFor(c)})
	}
	id, ok, err := runPickerOne("Which password manager?", "", rows)
	if err != nil || !ok {
		return nil, err
	}
	return connectorNamed(id)
}

func chooseVault(c Connector) (string, error) {
	vaults, err := c.Vaults()
	if err != nil {
		return "", err
	}
	if len(vaults) == 1 {
		return vaults[0].ID, nil
	}
	rows := make([]PickRow, 0, len(vaults))
	for _, v := range vaults {
		rows = append(rows, PickRow{ID: v.ID, Label: v.Name})
	}
	id, ok, err := runPickerOne("Which vault is yours alone?",
		"usually Private or Employee · anything shared is refused", rows)
	if err != nil || !ok {
		return "", err
	}
	return id, nil
}

// configuredVault is what every vault-transport path starts from: the
// chosen vault and the connector that owns it, checked to be usable now.
func configuredVault() (Connector, VaultRef, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, VaultRef{}, err
	}
	if cfg.Vault == nil || cfg.Vault.ID == "" {
		return nil, VaultRef{}, errors.New("no vault chosen — run: jat vault set")
	}
	c, err := connectorNamed(cfg.Vault.Manager)
	if err != nil {
		return nil, VaultRef{}, fmt.Errorf("%w — run: jat vault set", err)
	}
	if err := c.Available(); err != nil {
		return nil, VaultRef{}, err
	}
	return c, *cfg.Vault, nil
}

// listVaultBundles asks the connector for candidates and keeps only what
// jatWrote accepts. key is validated first because connectors put it into
// arguments.
func listVaultBundles(c Connector, vault VaultRef, key string) ([]vaultBundle, error) {
	if key != "" && !migrateKeyRe.MatchString(key) {
		return nil, fmt.Errorf("%q is not a migration key (four hex characters, like 7f3a)", key)
	}
	items, err := c.Items(vault, key)
	if err != nil {
		return nil, err
	}
	var found []vaultBundle
	for _, it := range items {
		if vb, ok := jatWrote(it, vault); ok && (key == "" || vb.Key == key) {
			found = append(found, vb)
		}
	}
	slices.SortFunc(found, func(a, b vaultBundle) int { return b.Created.Compare(a.Created) })
	return found, nil
}

// fetchVaultBundle downloads one migration to a private temp file and
// returns its path with a cleanup. It only accepts something
// listVaultBundles vouched for.
func fetchVaultBundle(c Connector, vault VaultRef, vb vaultBundle) (string, func(), error) {
	dir, err := os.MkdirTemp("", "jat-receive-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { os.RemoveAll(dir) }
	dest := dir + "/" + bundleFileName(vb.Key)
	if err := c.Fetch(vault, vb.Item, dest); err != nil {
		cleanup()
		return "", nil, err
	}
	if _, err := os.Stat(dest); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("%s did not produce the bundle file: %w", c.Label(), err)
	}
	return dest, cleanup, nil
}

// pickVaultBundle resolves --key (or its absence) to one migration.
func pickVaultBundle(c Connector, vault VaultRef, key string) (vaultBundle, bool, error) {
	found, err := listVaultBundles(c, vault, key)
	if err != nil {
		return vaultBundle{}, false, err
	}
	switch {
	case len(found) == 0 && key != "":
		return vaultBundle{}, false, fmt.Errorf("no migration %s in %s vault %q", key, c.Label(), vault.Name)
	case len(found) == 0:
		return vaultBundle{}, false, fmt.Errorf("no migrations waiting in %s vault %q", c.Label(), vault.Name)
	case len(found) == 1:
		// The key exists to tell two migrations apart. With one, there is
		// nothing to ask.
		return found[0], true, nil
	}
	if !stdinIsTerminal() {
		var keys []string
		for _, vb := range found {
			keys = append(keys, vb.Key)
		}
		return vaultBundle{}, false, fmt.Errorf("%d migrations waiting (%s) — pass --key", len(found), strings.Join(keys, ", "))
	}
	rows := make([]PickRow, 0, len(found))
	for i, vb := range found {
		rows = append(rows, PickRow{
			ID: fmt.Sprint(i), Label: vb.Key,
			Note: fmt.Sprintf("%s · from %s as %s", vb.Created.Local().Format("2 Jan 2006 15:04"), vb.Host, vb.User),
		})
	}
	choice, ok, err := runPickerOne("Which migration?", c.Label()+" vault "+vault.Name, rows)
	if err != nil || !ok {
		return vaultBundle{}, false, err
	}
	var i int
	fmt.Sscan(choice, &i)
	return found[i], true, nil
}

// sendVaultBundle files a finished bundle under the title jat later insists
// on before it will read it back.
func sendVaultBundle(c Connector, vault VaultRef, bundlePath string, man Manifest) (string, error) {
	title := migrateTitleFor(man)
	if !migrateTitle.MatchString(title) {
		return "", fmt.Errorf("refusing to file %q: it would not match jat's own title pattern", title)
	}
	return title, c.Store(vault, bundlePath, title, man)
}

func slugUser(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		}
		return '-'
	}, s)
	if s == "" {
		return "unknown"
	}
	return s
}
