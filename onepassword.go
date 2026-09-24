package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"
)

// onePassword drives the `op` CLI. A migration is a document in the private
// vault, tagged jat-migrate and jat-migrate-<key>.
//
// Verified against op 2.39.0 on a 1Password Business account: the private
// vault reports type PERSONAL (named Employee there), team vaults
// USER_CREATED, and documents list as category DOCUMENT.
type onePassword struct{}

func (onePassword) Name() string  { return "1password" }
func (onePassword) Label() string { return "1Password" }

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
		return nil, notInstalled("op", "op")
	}
	cmd := exec.Command("op", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	// op prompts for biometric approval on the terminal it was started from.
	cmd.Stdin = os.Stdin
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("op %s: %s", strings.Join(args[:2], " "), strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

func opRun(args ...string) ([]byte, error) { return runAllowed("op", opAllowed, opExec, args...) }

// Available only checks the binary is there: op signs in through its
// desktop app on first use, so there is no session state to test up front.
func (onePassword) Available() error {
	_, err := opRun("vault", "list", "--format", "json")
	return err
}

func (onePassword) Vaults() ([]VaultRef, error) {
	out, err := opRun("vault", "list", "--format", "json")
	if err != nil {
		return nil, err
	}
	var vaults []struct{ ID, Name string }
	if err := json.Unmarshal(out, &vaults); err != nil {
		return nil, fmt.Errorf("unreadable reply from op vault list: %w", err)
	}
	var refs []VaultRef
	for _, v := range vaults {
		refs = append(refs, VaultRef{Manager: "1password", ID: v.ID, Name: v.Name})
	}
	return refs, nil
}

// Validate is the one moment a vault's type is checked: a single
// `op vault get`. `op vault list` has no type field, so finding the private
// vault by scanning would cost a call per vault.
func (onePassword) Validate(nameOrID string) (VaultRef, error) {
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
	return VaultRef{Manager: "1password", ID: v.ID, Name: v.Name}, nil
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

func (onePassword) Items(vault VaultRef, key string) ([]storedItem, error) {
	tag := migrateTag
	if key != "" {
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
	var out2 []storedItem
	for _, it := range items {
		s := storedItem{ID: it.ID, Title: it.Title, VaultID: it.Vault.ID, Created: it.CreatedAt,
			IsBundle: it.Category == "DOCUMENT" && slices.Contains(it.Tags, migrateTag)}
		for _, t := range it.Tags {
			if k, ok := strings.CutPrefix(t, migrateKeyTag); ok && migrateKeyRe.MatchString(k) {
				s.KeyMark = k
			}
		}
		out2 = append(out2, s)
	}
	return out2, nil
}

func (onePassword) Fetch(vault VaultRef, it storedItem, dest string) error {
	_, err := opRun("document", "get", it.ID, "--vault", vault.ID, "--out-file", dest, "--force")
	return err
}

func (onePassword) Store(vault VaultRef, bundlePath, title string, man Manifest) error {
	_, err := opRun("document", "create", bundlePath,
		"--vault", vault.ID,
		"--title", title,
		"--file-name", bundleFileName(man.Key),
		"--tags", migrateTag+","+migrateKeyTag+man.Key,
		"--format", "json")
	return err
}
