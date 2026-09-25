package cli

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
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
//
// Unlocking. bw has no desktop-app integration, so a session is a key that
// `bw unlock` prints and every later call needs. James (2026-09-23) accepted
// jat doing that step itself: when the vault is locked and there is a
// terminal, jat runs `bw unlock --raw` with the terminal attached — bw asks
// for the master password, not jat — and keeps the key in memory for the
// rest of the run, passing it as --session. It is never written, printed or
// logged, and BW_SESSION set by the shell is used as-is when present.
type bitwarden struct{}

// bwSession is the key for this run only. Empty means "whatever the
// environment has", which is how a shell-exported BW_SESSION keeps working.
var bwSession string

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
	{"delete", "item"},
}

var bwExec = func(args ...string) ([]byte, error) {
	if _, err := exec.LookPath("bw"); err != nil {
		return nil, notInstalled("bw", "bw")
	}
	full := append(slices.Clone(args), "--nointeraction")
	if bwSession != "" {
		full = append(full, "--session", bwSession)
	}
	cmd := exec.Command("bw", full...)
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

// Available needs an unlocked session. A shell-exported BW_SESSION is used
// as found; otherwise, on a terminal, jat unlocks for this run (see the type
// comment). Signing in is still the person's job: it may need 2FA, and it
// is done once per machine, not once per run.
func (bitwarden) Available() error {
	// No LookPath here: bwExec does that, and a test with a stand-in bwExec
	// must not depend on whether the machine running it has bw installed
	// (it did, and passed locally while failing in CI — 2026-09-23).
	st, err := bwState()
	if err != nil {
		return err
	}
	switch st.Status {
	case "unlocked":
		return nil
	case "locked":
		if !stdinIsTerminal() {
			return errors.New("Bitwarden is locked — run: export BW_SESSION=$(bw unlock --raw)")
		}
		return bwUnlockForThisRun()
	default:
		return errors.New("not signed in to Bitwarden — run: bw login, then try again")
	}
}

// bwUnlockForThisRun runs `bw unlock --raw` with the terminal attached, so
// bw does its own prompting, and keeps only the key it prints.
func bwUnlockForThisRun() error {
	fmt.Fprintln(os.Stderr, "Bitwarden is locked — bw will ask for your master password (held for this run only).")
	out, err := bwUnlock()
	if err != nil {
		return err
	}
	key := strings.TrimSpace(string(out))
	if key == "" {
		return errors.New("bw unlock returned no session key")
	}
	bwSession = key
	st, err := bwState()
	if err != nil {
		return err
	}
	if st.Status != "unlocked" {
		bwSession = ""
		return errors.New("bw unlock did not leave the vault unlocked")
	}
	return nil
}

// bwUnlock is the one bw call that is not through bwExec: the prompt needs
// stdin and stderr to be the terminal, and --nointeraction would refuse it.
var bwUnlock = func() ([]byte, error) {
	cmd := exec.Command("bw", "unlock", "--raw")
	cmd.Stdin, cmd.Stderr = os.Stdin, os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("bw unlock: %w", err)
	}
	return out, nil
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

// Delete moves the note (and its attachment) to Bitwarden's trash, which is
// what `bw delete item` does without --permanent; the trash empties itself
// after 30 days and gives a mistake a way back.
func (bitwarden) Delete(vault VaultRef, it storedItem) error {
	_, err := bwRun("delete", "item", it.ID)
	return err
}
