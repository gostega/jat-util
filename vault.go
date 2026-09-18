package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"
)

// jat is handed a vault that holds far more than its own data, so what it may
// do there is fixed in code rather than left to good intentions:
//
//   - it writes only to the configured vault, which was proven PERSONAL when
//     it was chosen — never a shared or team vault;
//   - everything it writes carries its own tag AND its own title pattern;
//   - it reads only items that carry both, found by tag and then checked by
//     title, with no fallback to a title search;
//   - it can run only the op subcommands listed in opAllowed, none of which
//     returns a field value of an item.

const (
	migrateTag    = "jat-migrate"
	migrateKeyTag = "jat-migrate-" // + key
)

// The title is where a listing gets host and user from, so nothing has to be
// opened to show them: jat/migrate/<key>/<host>/<user>.
var migrateTitle = regexp.MustCompile(`^jat/migrate/([0-9a-f]{4})/([a-z0-9-]+)/([a-z0-9._-]+)$`)

var migrateKeyRe = regexp.MustCompile(`^[0-9a-f]{4}$`)

// VaultRef is the private vault jat may use. The ID is what op is given; the
// name is only for showing a person which one that is.
type VaultRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// opAllowed is every op invocation jat can make. `item get` and `read` are
// absent on purpose: they are how a field value leaves a vault.
var opAllowed = [][2]string{
	{"vault", "get"},
	{"vault", "list"},
	{"item", "list"},
	{"document", "create"},
	{"document", "get"},
}

// opExec is swapped out in tests. Everything goes through opRun first.
var opExec = func(args ...string) ([]byte, error) {
	if _, err := exec.LookPath("op"); err != nil {
		return nil, errors.New("the 1Password CLI (op) is not installed — try: jat install op")
	}
	cmd := exec.Command("op", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	// op prompts for biometric approval on the terminal it was started from.
	cmd.Stdin = os.Stdin
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("op %s: %s", strings.Join(args[:min(2, len(args))], " "), strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

func opRun(args ...string) ([]byte, error) {
	if len(args) < 2 || !slices.Contains(opAllowed, [2]string{args[0], args[1]}) {
		return nil, fmt.Errorf("jat does not run `op %s`", strings.Join(args[:min(2, len(args))], " "))
	}
	return opExec(args...)
}

func cmdVault(args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if len(args) == 0 {
		if cfg.Vault == nil {
			fmt.Println("no vault chosen — run: jat vault set [<name or id>]")
			return nil
		}
		fmt.Printf("%s (%s)\n", cfg.Vault.Name, cfg.Vault.ID)
		return nil
	}
	if args[0] != "set" || len(args) > 2 {
		return fmt.Errorf("usage: jat vault [set [<name or id>]]")
	}

	want := ""
	if len(args) == 2 {
		want = args[1]
	} else {
		if want, err = chooseVault(); err != nil || want == "" {
			return err
		}
	}
	ref, err := validateVault(want)
	if err != nil {
		return err
	}
	cfg.Vault = &ref
	if err := saveConfig(cfg); err != nil {
		return err
	}
	fmt.Printf("vault %q (%s) saved to %s\n", ref.Name, ref.ID, configPath())
	return nil
}

// validateVault is the one moment a vault's type is checked: a single
// `op vault get`. `op vault list` has no type field, so finding the private
// vault by scanning would cost a call per vault.
func validateVault(nameOrID string) (VaultRef, error) {
	out, err := opRun("vault", "get", nameOrID, "--format", "json")
	if err != nil {
		return VaultRef{}, err
	}
	var v struct{ ID, Name, Type string }
	if err := json.Unmarshal(out, &v); err != nil {
		return VaultRef{}, fmt.Errorf("unreadable reply from op vault get: %w", err)
	}
	if v.Type != "PERSONAL" {
		return VaultRef{}, fmt.Errorf("vault %q is %s, not your private vault — jat only writes where nobody else can read (usually named Private or Employee)",
			v.Name, orUnknown(v.Type))
	}
	if v.ID == "" {
		return VaultRef{}, errors.New("op vault get returned no id")
	}
	return VaultRef{ID: v.ID, Name: v.Name}, nil
}

func chooseVault() (string, error) {
	out, err := opRun("vault", "list", "--format", "json")
	if err != nil {
		return "", err
	}
	var vaults []struct{ ID, Name string }
	if err := json.Unmarshal(out, &vaults); err != nil {
		return "", fmt.Errorf("unreadable reply from op vault list: %w", err)
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

func configuredVault() (VaultRef, error) {
	cfg, err := loadConfig()
	if err != nil {
		return VaultRef{}, err
	}
	if cfg.Vault == nil || cfg.Vault.ID == "" {
		return VaultRef{}, errors.New("no vault chosen — run: jat vault set")
	}
	return *cfg.Vault, nil
}

// vaultBundle is a migrate document as a listing shows it. Every field comes
// from list metadata; nothing has been opened.
type vaultBundle struct {
	ItemID  string
	Key     string
	Host    string
	User    string
	Created time.Time
}

type opItem struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Tags     []string `json:"tags"`
	Category string   `json:"category"`
	Vault    struct {
		ID string `json:"id"`
	} `json:"vault"`
	CreatedAt time.Time `json:"created_at"`
}

// jatWrote reports whether an item is one jat may read: in the configured
// vault, a document, carrying the migrate tag, a key tag, and a title that
// matches the pattern and agrees with the key tag. All of it, not any of it.
func jatWrote(it opItem, vault VaultRef) (vaultBundle, bool) {
	m := migrateTitle.FindStringSubmatch(it.Title)
	if m == nil || it.Vault.ID != vault.ID || it.Category != "DOCUMENT" {
		return vaultBundle{}, false
	}
	if !slices.Contains(it.Tags, migrateTag) || !slices.Contains(it.Tags, migrateKeyTag+m[1]) {
		return vaultBundle{}, false
	}
	return vaultBundle{ItemID: it.ID, Key: m[1], Host: m[2], User: m[3], Created: it.CreatedAt}, true
}

// listVaultBundles finds migrate documents by tag, then keeps only the ones
// jatWrote accepts. key narrows the tag; it is validated first because it
// becomes part of an argument.
func listVaultBundles(vault VaultRef, key string) ([]vaultBundle, error) {
	tag := migrateTag
	if key != "" {
		if !migrateKeyRe.MatchString(key) {
			return nil, fmt.Errorf("%q is not a migration key (four hex characters, like 7f3a)", key)
		}
		tag = migrateKeyTag + key
	}
	out, err := opRun("item", "list", "--tags", tag, "--vault", vault.ID, "--format", "json")
	if err != nil {
		return nil, err
	}
	var items []opItem
	if err := json.Unmarshal(out, &items); err != nil {
		return nil, fmt.Errorf("unreadable reply from op item list: %w", err)
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

// fetchVaultBundle downloads one document to a private temp file and returns
// its path with a cleanup. It only accepts something listVaultBundles vouched
// for, so there is no way to hand it a title or an id from anywhere else.
func fetchVaultBundle(vault VaultRef, vb vaultBundle) (string, func(), error) {
	dir, err := os.MkdirTemp("", "jat-receive-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { os.RemoveAll(dir) }
	dest := dir + "/bundle.tar.gz"
	if _, err := opRun("document", "get", vb.ItemID, "--vault", vault.ID, "--out-file", dest, "--force"); err != nil {
		cleanup()
		return "", nil, err
	}
	return dest, cleanup, nil
}

// pickVaultBundle resolves --key (or its absence) to one document.
func pickVaultBundle(vault VaultRef, key string) (vaultBundle, bool, error) {
	found, err := listVaultBundles(vault, key)
	if err != nil {
		return vaultBundle{}, false, err
	}
	switch {
	case len(found) == 0 && key != "":
		return vaultBundle{}, false, fmt.Errorf("no migration %s in vault %q", key, vault.Name)
	case len(found) == 0:
		return vaultBundle{}, false, fmt.Errorf("no migrations waiting in vault %q", vault.Name)
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
	choice, ok, err := runPickerOne("Which migration?", "vault "+vault.Name, rows)
	if err != nil || !ok {
		return vaultBundle{}, false, err
	}
	var i int
	fmt.Sscan(choice, &i)
	return found[i], true, nil
}

// sendVaultBundle files a finished bundle as a document carrying both marks
// jat later insists on before it will read it back.
func sendVaultBundle(vault VaultRef, bundlePath string, man Manifest) (string, error) {
	title := fmt.Sprintf("jat/migrate/%s/%s/%s", man.Key, slugHost(man.Host), slugUser(man.User))
	if !migrateTitle.MatchString(title) {
		return "", fmt.Errorf("refusing to file %q: it would not match jat's own title pattern", title)
	}
	_, err := opRun("document", "create", bundlePath,
		"--vault", vault.ID,
		"--title", title,
		"--file-name", fmt.Sprintf("jat-migrate-%s.tar.gz", man.Key),
		"--tags", migrateTag+","+migrateKeyTag+man.Key,
		"--format", "json")
	return title, err
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
