package cli

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

// fakeBw stands in for the bw binary: an in-memory folder list and item
// list, plus a record of every call.
type fakeBw struct {
	calls   [][]string
	status  string
	folders []map[string]any
	items   []map[string]any
	stored  map[string][]byte // attachment bytes by item id
	premium bool
}

func decode(t *testing.T, b64 string) map[string]any {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("bw was handed something that is not base64: %v", err)
	}
	var m map[string]any
	json.Unmarshal(raw, &m)
	return m
}

func (f *fakeBw) install(t *testing.T) {
	t.Helper()
	prev := bwExec
	f.stored = map[string][]byte{}
	bwExec = func(args ...string) ([]byte, error) {
		f.calls = append(f.calls, args)
		switch strings.Join(args[:min(2, len(args))], " ") {
		case "status":
			st := f.status
			// The fake replaces bwExec, which is what appends --session, so
			// it reads the run's key directly. A good key unlocks, as bw would.
			if bwSession == "unlocked-key" {
				st = "unlocked"
			}
			return json.Marshal(map[string]string{"status": st, "userId": "u1", "userEmail": "james@example.test"})
		case "sync":
			return []byte("Syncing complete."), nil
		case "list folders":
			return json.Marshal(f.folders)
		case "create folder":
			m := decode(t, args[2])
			m["id"] = "f-new"
			f.folders = append(f.folders, m)
			return json.Marshal(m)
		case "list items":
			var out []map[string]any
			for _, it := range f.items {
				if fid, _ := it["folderId"].(string); fid == args[3] {
					out = append(out, it)
				}
			}
			return json.Marshal(out)
		case "create item":
			m := decode(t, args[2])
			m["id"] = fmt.Sprintf("i-%d", len(f.items))
			m["attachments"] = []any{}
			f.items = append(f.items, m)
			return json.Marshal(m)
		case "create attachment":
			if !f.premium {
				return nil, fmt.Errorf("Premium status is required to use this feature.")
			}
			var file, id string
			for i := range args {
				if args[i] == "--file" {
					file = args[i+1]
				}
				if args[i] == "--itemid" {
					id = args[i+1]
				}
			}
			b, _ := os.ReadFile(file)
			f.stored[id] = b
			for _, it := range f.items {
				if it["id"] == id {
					it["attachments"] = []map[string]any{{"id": "a1", "fileName": file[strings.LastIndex(file, "/")+1:]}}
					return json.Marshal(it)
				}
			}
			return nil, fmt.Errorf("no item %s", id)
		case "get attachment":
			var id, out string
			for i := range args {
				if args[i] == "--itemid" {
					id = args[i+1]
				}
				if args[i] == "--output" {
					out = args[i+1]
				}
			}
			return nil, os.WriteFile(out, f.stored[id], 0o600)
		}
		return nil, fmt.Errorf("fake bw: unexpected %v", args)
	}
	t.Cleanup(func() { bwExec = prev })
}

func TestBwRunRefusesAnythingThatReadsAValue(t *testing.T) {
	f := &fakeBw{status: "unlocked"}
	f.install(t)
	for _, args := range [][]string{
		{"get", "item", "x"}, {"get", "password", "x"}, {"get", "notes", "x"}, {"get", "totp", "x"},
		{"list", "items"}, // only ever with --folderid, but the allowlist alone cannot say that
		{"export"}, {"unlock"},
	} {
		if _, err := bwRun(args...); err == nil && !(args[0] == "list" && args[1] == "items") {
			t.Errorf("bwRun(%v) was allowed", args)
		}
	}
}

func TestBitwardenNeedsAnUnlockedSession(t *testing.T) {
	f := &fakeBw{status: "locked"}
	f.install(t)
	if err := (bitwarden{}).Available(); err == nil || !strings.Contains(err.Error(), "BW_SESSION") {
		t.Errorf("locked session gave %v, want the unlock hint", err)
	}
	f.status = "unauthenticated"
	if err := (bitwarden{}).Available(); err == nil || !strings.Contains(err.Error(), "bw login") {
		t.Errorf("signed-out gave %v, want the login hint", err)
	}
	f.status = "unlocked"
	ref, err := (bitwarden{}).Validate("")
	if err != nil || ref.ID != "u1" || ref.Manager != "bitwarden" {
		t.Errorf("Validate = %+v, %v", ref, err)
	}
	if _, err := (bitwarden{}).Validate("someone-else"); err == nil {
		t.Error("a vault name that is not this account was accepted")
	}
}

var bwVault = VaultRef{Manager: "bitwarden", ID: "u1", Name: "james@example.test"}

func bwNote(id, name, folder string, org any, attachments ...string) map[string]any {
	var atts []map[string]any
	for _, a := range attachments {
		atts = append(atts, map[string]any{"id": "a-" + a, "fileName": a})
	}
	return map[string]any{"id": id, "name": name, "type": 2, "folderId": folder, "organizationId": org,
		"notes": "SHOULD NEVER BE READ", "creationDate": "2026-09-22T01:00:00Z", "attachments": atts}
}

func TestBitwardenOnlyItemsJatWroteAreEverFetched(t *testing.T) {
	f := &fakeBw{status: "unlocked",
		folders: []map[string]any{{"id": nil, "name": "No Folder"}, {"id": "f1", "name": "jat-migrate"}, {"id": "f2", "name": "Work"}},
		items: []map[string]any{
			bwNote("good", "jat/migrate/7f3a/old-mac/james", "f1", nil, "jat-migrate-7f3a.tar.gz"),
			bwNote("wrong-folder", "jat/migrate/7f3a/old-mac/james", "f2", nil, "jat-migrate-7f3a.tar.gz"),
			bwNote("org-owned", "jat/migrate/7f3a/old-mac/james", "f1", "org9", "jat-migrate-7f3a.tar.gz"),
			bwNote("no-attachment", "jat/migrate/7f3a/old-mac/james", "f1", nil),
			bwNote("two-attachments", "jat/migrate/7f3a/old-mac/james", "f1", nil, "jat-migrate-7f3a.tar.gz", "extra.txt"),
			bwNote("key-mismatch", "jat/migrate/7f3a/old-mac/james", "f1", nil, "jat-migrate-0000.tar.gz"),
			bwNote("bad-title", "My bank", "f1", nil, "jat-migrate-7f3a.tar.gz"),
			{"id": "login", "name": "jat/migrate/7f3a/old-mac/james", "type": 1, "folderId": "f1", "attachments": []map[string]any{{"id": "x", "fileName": "jat-migrate-7f3a.tar.gz"}}},
		}}
	f.install(t)
	f.stored["good"] = []byte("bundle bytes")

	found, err := listVaultBundles(bitwarden{}, bwVault, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Item.ID != "good" {
		var ids []string
		for _, vb := range found {
			ids = append(ids, vb.Item.ID)
		}
		t.Fatalf("accepted %v, want only the item carrying every mark", ids)
	}
	for _, c := range f.calls {
		if c[0] == "list" && c[1] == "items" && (len(c) < 4 || c[2] != "--folderid" || c[3] != "f1") {
			t.Errorf("list items ran outside jat's folder: %v", c)
		}
	}

	path, cleanup, err := fetchVaultBundle(bitwarden{}, bwVault, found[0])
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if got, _ := os.ReadFile(path); string(got) != "bundle bytes" {
		t.Errorf("fetched %q", got)
	}
	for _, c := range f.calls {
		if c[0] == "get" && !strings.Contains(strings.Join(c, " "), "--itemid good") {
			t.Errorf("fetched an item jat did not write: %v", c)
		}
	}
}

func TestBitwardenNoFolderMeansNothingWaiting(t *testing.T) {
	f := &fakeBw{status: "unlocked", folders: []map[string]any{{"id": nil, "name": "No Folder"}}}
	f.install(t)
	found, err := listVaultBundles(bitwarden{}, bwVault, "")
	if err != nil || len(found) != 0 {
		t.Errorf("got %v, %v", found, err)
	}
	for _, c := range f.calls {
		if c[0] == "list" && c[1] == "items" {
			t.Error("listed items with no folder to fence them")
		}
		if c[0] == "create" {
			t.Error("a listing created something")
		}
	}
}

// The vault is a courier: what goes in through Store comes out of Fetch
// byte for byte, is recognised by the shared gate, and reads as a bundle.
func TestBitwardenRoundTrip(t *testing.T) {
	f := &fakeBw{status: "unlocked", premium: true, folders: []map[string]any{{"id": nil, "name": "No Folder"}}}
	f.install(t)
	home := t.TempDir()
	bundle := testBundle(t, []string{"zsh"}, map[string]string{"home/.zshrc": "x"})
	raw, _ := os.ReadFile(bundle)

	title, err := sendVaultBundle(bitwarden{}, bwVault, bundle, Manifest{Key: "7f3a", Host: "Old Mac", User: "James T"})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.folders) != 2 || f.folders[1]["name"] != "jat-migrate" {
		t.Errorf("folder not created: %v", f.folders)
	}
	if f.items[0]["name"] != title || f.items[0]["folderId"] != "f-new" || f.items[0]["type"] != float64(2) {
		t.Errorf("item filed wrongly: %v", f.items[0])
	}
	if _, has := f.items[0]["organizationId"]; has {
		t.Error("item was given an organizationId; it must stay in the personal vault")
	}

	found, err := listVaultBundles(bitwarden{}, bwVault, "7f3a")
	if err != nil || len(found) != 1 {
		t.Fatalf("receive would refuse what send just filed: %v %v", found, err)
	}
	path, cleanup, err := fetchVaultBundle(bitwarden{}, bwVault, found[0])
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if got, _ := os.ReadFile(path); string(got) != string(raw) {
		t.Error("bundle changed in transit")
	}
	if _, items, err := readBundle(path, home); err != nil || len(items) != 1 || items[0].Name != "zsh" {
		t.Errorf("readBundle on the fetched copy: %v %v", items, err)
	}
}

func TestBitwardenWithoutPremiumSaysSo(t *testing.T) {
	f := &fakeBw{status: "unlocked", folders: []map[string]any{{"id": "f1", "name": "jat-migrate"}}}
	f.install(t)
	bundle := testBundle(t, []string{"zsh"}, map[string]string{"home/.zshrc": "x"})
	_, err := sendVaultBundle(bitwarden{}, bwVault, bundle, Manifest{Key: "7f3a", Host: "h", User: "u"})
	if err == nil || !strings.Contains(err.Error(), "Premium") {
		t.Errorf("got %v, want the Premium explanation", err)
	}
}

// James, 2026-09-23: jat may unlock for the run. The key stays in memory,
// goes to bw as --session, and never appears in anything jat prints.
func TestBitwardenUnlocksForTheRunOnATerminal(t *testing.T) {
	f := &fakeBw{status: "locked", folders: []map[string]any{{"id": "f1", "name": "jat-migrate"}}}
	f.install(t)
	prevTTY, prevUnlock, prevSession := stdinIsTerminal, bwUnlock, bwSession
	t.Cleanup(func() { stdinIsTerminal, bwUnlock, bwSession = prevTTY, prevUnlock, prevSession })
	unlocks := 0
	bwUnlock = func() ([]byte, error) { unlocks++; return []byte("unlocked-key\n"), nil }

	stdinIsTerminal = func() bool { return false }
	if err := (bitwarden{}).Available(); err == nil || !strings.Contains(err.Error(), "BW_SESSION") || unlocks != 0 {
		t.Fatalf("without a terminal: err=%v unlocks=%d; want the hint and no unlock attempt", err, unlocks)
	}

	stdinIsTerminal = func() bool { return true }
	if err := (bitwarden{}).Available(); err != nil {
		t.Fatalf("with a terminal: %v", err)
	}
	if unlocks != 1 || bwSession != "unlocked-key" {
		t.Fatalf("unlocks=%d session=%q", unlocks, bwSession)
	}
	// Every later call carries the key, and nothing else in the run asks again.
	if _, err := listVaultBundles(bitwarden{}, bwVault, ""); err != nil {
		t.Fatal(err)
	}
	if err := (bitwarden{}).Available(); err != nil || unlocks != 1 {
		t.Errorf("second Available: err=%v unlocks=%d, want no second prompt", err, unlocks)
	}

	// A failed unlock leaves no half-state behind.
	bwSession = ""
	f.status = "locked"
	bwUnlock = func() ([]byte, error) { return []byte("wrong-key\n"), nil }
	if err := (bitwarden{}).Available(); err == nil || bwSession != "" {
		t.Errorf("bad unlock: err=%v session=%q, want an error and an empty session", err, bwSession)
	}
}
