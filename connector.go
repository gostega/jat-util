package main

import (
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"
)

// A connector is one password manager's CLI, wrapped so that everything above
// it — choosing the vault, listing migrations, sending and receiving a bundle
// — is written once. What a connector may do is fixed in code, not left to
// good intentions:
//
//   - it writes only to the vault chosen by `jat vault set`, which was proven
//     private to this user when it was chosen — never a shared or team vault;
//   - everything it writes carries jat's title pattern AND a manager-native
//     mark of jat's own (a tag, a folder);
//   - it reads only items carrying both, found by the mark and then checked
//     against the title by jatWrote, with no fallback to a title search;
//   - it runs only the CLI subcommands on its allowlist, none of which returns
//     a field value of an item jat did not write.
//
// Adding a manager means one file implementing Connector and one line in
// connectors. Nothing in migrate.go or receive.go knows which one is in use.
type Connector interface {
	// Name is the config value and the --manager spelling: "1password".
	Name() string
	// Label is what a person sees: "1Password".
	Label() string
	// Available reports whether the CLI is installed and signed in, with a
	// message saying what to do about it when not.
	Available() error
	// Vaults lists the vaults a person could choose from. May be one entry
	// for a manager with no vault concept.
	Vaults() ([]VaultRef, error)
	// Validate proves a vault is private to this user and fills in its id and
	// name. Called once, when the vault is chosen.
	Validate(nameOrID string) (VaultRef, error)
	// Items returns jat's candidate items in the vault, already narrowed by
	// the manager-native mark, for jatWrote to judge. key narrows further
	// where the manager can.
	Items(vault VaultRef, key string) ([]storedItem, error)
	// Fetch writes one item's bundle to dest. The item was vouched for by
	// jatWrote; a connector never fetches anything else.
	Fetch(vault VaultRef, it storedItem, dest string) error
	// Store files a bundle under title, carrying the manager-native mark.
	Store(vault VaultRef, bundlePath, title string, man Manifest) error
	// Delete removes one item. Like Fetch it is only ever handed an item
	// jatWrote vouched for, so a connector cannot be pointed at anything else.
	Delete(vault VaultRef, it storedItem) error
}

// storedItem is an item as a listing shows it, in manager-neutral terms.
// Every field comes from list metadata; nothing has been opened.
type storedItem struct {
	ID      string
	Title   string
	VaultID string
	// IsBundle says the item is the kind that can carry a file (an op
	// document, a bw note with one attachment). KeyMark is the key the
	// manager-native mark names (the jat-migrate-<key> tag, the attachment
	// file name); it must agree with the title.
	IsBundle bool
	KeyMark  string
	Created  time.Time
}

// VaultRef is the private vault jat may use, and which manager it lives in.
// The ID is what the CLI is given; the name is only for showing a person
// which one that is.
type VaultRef struct {
	Manager string `json:"manager"`
	ID      string `json:"id"`
	Name    string `json:"name"`
}

const (
	migrateTag    = "jat-migrate"
	migrateKeyTag = "jat-migrate-" // + key
)

// The title is where a listing gets host and user from, so nothing has to be
// opened to show them: jat/migrate/<key>/<host>/<user>.
var migrateTitle = regexp.MustCompile(`^jat/migrate/([0-9a-f]{4})/([a-z0-9-]+)/([a-z0-9._-]+)$`)

var migrateKeyRe = regexp.MustCompile(`^[0-9a-f]{4}$`)

func migrateTitleFor(man Manifest) string {
	return fmt.Sprintf("jat/migrate/%s/%s/%s", man.Key, slugHost(man.Host), slugUser(man.User))
}

func bundleFileName(key string) string { return "jat-migrate-" + key + ".tar.gz" }

// vaultBundle is a migration as receive lists it.
type vaultBundle struct {
	Item    storedItem
	Key     string
	Host    string
	User    string
	Created time.Time
}

// jatWrote is the single gate on reads, shared by every connector: in the
// chosen vault, able to carry a bundle, a title matching the pattern, and a
// manager-native mark naming the same key. All of it, not any of it.
func jatWrote(it storedItem, vault VaultRef) (vaultBundle, bool) {
	m := migrateTitle.FindStringSubmatch(it.Title)
	if m == nil || it.VaultID != vault.ID || !it.IsBundle || it.KeyMark != m[1] {
		return vaultBundle{}, false
	}
	return vaultBundle{Item: it, Key: m[1], Host: m[2], User: m[3], Created: it.Created}, true
}

var connectors = []Connector{onePassword{}, bitwarden{}}

func connectorNamed(name string) (Connector, error) {
	for _, c := range connectors {
		if c.Name() == name {
			return c, nil
		}
	}
	return nil, fmt.Errorf("unknown password manager %q (want %s)", name, strings.Join(connectorNames(), " or "))
}

func connectorNames() []string {
	var out []string
	for _, c := range connectors {
		out = append(out, c.Name())
	}
	return out
}

// installedConnectors is every manager whose CLI is on PATH, for choosing
// one without being asked when only one is there.
func installedConnectors() []Connector {
	var out []Connector
	for _, c := range connectors {
		if _, err := exec.LookPath(cliFor(c)); err == nil {
			out = append(out, c)
		}
	}
	return out
}

func cliFor(c Connector) string {
	return map[string]string{"1password": "op", "bitwarden": "bw"}[c.Name()]
}

// runAllowed is the one way a connector reaches its CLI. Every connector's
// allowlist is a set of two-word subcommands; anything else is refused before
// the binary is found, let alone run.
func runAllowed(exe string, allowed [][2]string, run func(args ...string) ([]byte, error), args ...string) ([]byte, error) {
	var key [2]string
	if len(args) > 0 {
		key[0] = args[0]
	}
	if len(args) > 1 {
		key[1] = args[1]
	}
	if len(args) == 0 || !slices.Contains(allowed, key) {
		return nil, fmt.Errorf("jat does not run `%s %s`", exe, strings.Join(args[:min(2, len(args))], " "))
	}
	return run(args...)
}

func notInstalled(exe, install string) error {
	return errors.New("the " + exe + " CLI is not installed — try: jat install " + install)
}
