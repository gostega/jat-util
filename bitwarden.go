package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// bitwarden drives the `bw` CLI. Bitwarden has no vault-within-an-account
// concept and no tags, so the shape differs from 1Password in two ways that
// the shared rules absorb:
//
//   - "the private vault" is the account's own vault, as opposed to any
//     organization it belongs to. An item with an organizationId is not
//     jat's to touch, and jatWrote refuses it because its VaultID will not
//     match the account's user id;
//   - the manager-native mark is a folder named jat-migrate, plus the bundle
//     riding as the one attachment on a secure note, named after its key.
//
// One thing to know: `bw list items` returns whole item bodies, notes
// included, for every item it lists — there is no metadata-only listing.
// jat therefore only ever lists inside its own folder, and reads nothing but
// id, name, type, folder, organization, dates and attachment names from what
// comes back. Anything a person files in that folder themselves is listed by
// bw but ignored by jat.
//
// Attachments need a Bitwarden Premium account. Without one, Store fails at
// the attachment step and says so. Not yet run against a live bw
// (2026-09-22): JSON shapes and flags are from its documentation.
type bitwarden struct{}

func (bitwarden) Name() string  { return "bitwarden" }
func (bitwarden) Label() string { return "Bitwarden" }

const bwFolder = migrateTag

// bwAllowed is every bw invocation jat can make. `get item`, `get notes`,
// `get password` and friends are absent on purpose; `list items` is only
// ever run with --folderid (enforced in Items, its one caller).
var bwAllowed = [][2]string{
	{"status", ""},
	{"sync", ""},
	{"list", "folders"},
	{"create", "folder"},
	{"list", "items"},
	{"create", "item"},
	{"create", "attachment"},
	{"get", "attachment"},
}

var bwExec = func(args ...string) ([]byte, error) {
	if _, err := exec.LookPath("bw"); err != nil {
		return nil, notInstalled("bw", "bw")
	}
	cmd := exec.Command("bw", append(args, "--nointeraction")...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("bw %s: %s", strings.Join(args[:min(2, len(args))], " "), msg)
	}
	return out, nil
}

func bwRun(args ...string) ([]byte, error) { return runAllowed("bw", bwAllowed, bwExec, args...) }

type bwStatus struct {
	UserEmail string `json:"userEmail"`
	UserID    string `json:"userId"`
	Status    string `json:"status"` // unauthenticated, locked, unlocked
}

func bwState() (bwStatus, error) {
	out, err := bwRun("status")
	if err != nil {
		return bwStatus{}, err
	}
	var st bwStatus
	if err := json.Unmarshal(out, &st); err != nil {
		return bwStatus{}, fmt.Errorf("unreadable reply from bw status: %w", err)
	}
	return st, nil
}

// Available needs an unlocked session: bw reads BW_SESSION from the
// environment, and jat inherits it rather than asking for a password itself.
func (bitwarden) Available() error {
	if _, err := exec.LookPath("bw"); err != nil {
		return notInstalled("bw", "bw")
	}
	st, err := bwState()
	if err != nil {
		return err
	}
	switch st.Status {
	case "unlocked":
		return nil
	case "locked":
		return errors.New("Bitwarden is locked — run: export BW_SESSION=$(bw unlock --raw)")
	default:
		return errors.New("not signed in to Bitwarden — run: bw login, then export BW_SESSION=$(bw unlock --raw)")
	}
}

// Vaults is the one vault an account has. Its id is the user id, which is
// what organization-owned items will fail to match.
func (b bitwarden) Vaults() ([]VaultRef, error) {
	ref, err := b.Validate("")
	if err != nil {
		return nil, err
	}
	return []VaultRef{ref}, nil
}

func (b bitwarden) Validate(nameOrID string) (VaultRef, error) {
	if err := b.Available(); err != nil {
		return VaultRef{}, err
	}
	st, err := bwState()
	if err != nil {
		return VaultRef{}, err
	}
	if nameOrID != "" && nameOrID != st.UserID && nameOrID != st.UserEmail {
		return VaultRef{}, fmt.Errorf("Bitwarden has one personal vault per account, and %q is not this one (%s)", nameOrID, st.UserEmail)
	}
	if st.UserID == "" {
		return VaultRef{}, errors.New("bw status returned no user id")
	}
	return VaultRef{Manager: "bitwarden", ID: st.UserID, Name: st.UserEmail}, nil
}

type bwFolderRec struct {
	ID   *string `json:"id"`
	Name string  `json:"name"`
}

// folderID finds jat's folder, creating it when create is set. The folder
// is the fence every listing runs inside.
func (bitwarden) folderID(create bool) (string, error) {
	out, err := bwRun("list", "folders")
	if err != nil {
		return "", err
	}
	var folders []bwFolderRec
	if err := json.Unmarshal(out, &folders); err != nil {
		return "", fmt.Errorf("unreadable reply from bw list folders: %w", err)
	}
	for _, f := range folders {
		if f.Name == bwFolder && f.ID != nil {
			return *f.ID, nil
		}
	}
	if !create {
		return "", nil
	}
	out, err = bwRun("create", "folder", bwEncode(map[string]string{"name": bwFolder}))
	if err != nil {
		return "", err
	}
	var f bwFolderRec
	if err := json.Unmarshal(out, &f); err != nil || f.ID == nil {
		return "", fmt.Errorf("bw create folder returned no id")
	}
	return *f.ID, nil
}

// bw takes its JSON input base64-encoded, the way `bw encode` produces it.
func bwEncode(v any) string {
	b, _ := json.Marshal(v)
	return base64.StdEncoding.EncodeToString(b)
}

// bwItem is the slice of an item jat reads. The struct deliberately has no
// notes, login or fields member, so the rest of what bw returns is dropped
// at decode time and never reaches a variable.
type bwItem struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Type           int     `json:"type"` // 2 = secure note
	FolderID       *string `json:"folderId"`
	OrganizationID *string `json:"organizationId"`
	CreationDate   time.Time
	Attachments    []struct {
		ID       string `json:"id"`
		FileName string `json:"fileName"`
	} `json:"attachments"`
}

func (b bitwarden) Items(vault VaultRef, key string) ([]storedItem, error) {
	if _, err := bwRun("sync"); err != nil {
		return nil, err
	}
	folder, err := b.folderID(false)
	if err != nil || folder == "" {
		return nil, err
	}
	out, err := bwRun("list", "items", "--folderid", folder)
	if err != nil {
		return nil, err
	}
	var items []struct {
		bwItem
		CreationDate time.Time `json:"creationDate"`
	}
	if err := json.Unmarshal(out, &items); err != nil {
		return nil, fmt.Errorf("unreadable reply from bw list items: %w", err)
	}
	var found []storedItem
	for _, it := range items {
		s := storedItem{ID: it.ID, Title: it.Name, VaultID: vault.ID, Created: it.CreationDate}
		if it.OrganizationID != nil && *it.OrganizationID != "" {
			s.VaultID = *it.OrganizationID // not the personal vault: jatWrote refuses it
		}
		if it.Type == 2 && len(it.Attachments) == 1 {
			if k, ok := strings.CutPrefix(strings.TrimSuffix(it.Attachments[0].FileName, ".tar.gz"), "jat-migrate-"); ok && migrateKeyRe.MatchString(k) {
				s.IsBundle, s.KeyMark = true, k
			}
		}
		if key == "" || s.KeyMark == key {
			found = append(found, s)
		}
	}
	return found, nil
}

func (bitwarden) Fetch(vault VaultRef, it storedItem, dest string) error {
	_, err := bwRun("get", "attachment", bundleFileName(it.KeyMark), "--itemid", it.ID, "--output", dest)
	return err
}

func (b bitwarden) Store(vault VaultRef, bundlePath, title string, man Manifest) error {
	folder, err := b.folderID(true)
	if err != nil {
		return err
	}
	item := map[string]any{
		"type":       2,
		"secureNote": map[string]int{"type": 0},
		"name":       title,
		"folderId":   folder,
		"notes": fmt.Sprintf("jat migration %s from %s as %s. The bundle is the attachment; `jat migrate receive --transport vault --key %s` applies it.",
			man.Key, man.Host, man.User, man.Key),
	}
	out, err := bwRun("create", "item", bwEncode(item))
	if err != nil {
		return err
	}
	var created struct{ ID string }
	if err := json.Unmarshal(out, &created); err != nil || created.ID == "" {
		return errors.New("bw create item returned no id")
	}
	// bw names the attachment after the file, so the bundle is copied under
	// the name the key mark requires.
	dir, err := os.MkdirTemp("", "jat-bw-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	named := dir + "/" + bundleFileName(man.Key)
	if err := copyFile(bundlePath, named); err != nil {
		return err
	}
	if _, err := bwRun("create", "attachment", "--file", named, "--itemid", created.ID); err != nil {
		return fmt.Errorf("%w (attachments need Bitwarden Premium; the empty note %q was created and can be deleted)", err, title)
	}
	return nil
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o600)
}
