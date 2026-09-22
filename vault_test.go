package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeOp stands in for the op binary. It records every call and answers from
// canned replies keyed by the first two arguments.
type fakeOp struct {
	calls   [][]string
	replies map[string]string
}

func (f *fakeOp) install(t *testing.T) {
	t.Helper()
	prev := opExec
	opExec = func(args ...string) ([]byte, error) {
		f.calls = append(f.calls, args)
		k := args[0] + " " + args[1]
		if k == "document get" {
			// Behave like op: write the requested file.
			out := args[slices.Index(args, "--out-file")+1]
			return nil, os.WriteFile(out, []byte("bundle bytes"), 0o600)
		}
		reply, ok := f.replies[k]
		if !ok {
			return nil, fmt.Errorf("fake op: no reply for %q", k)
		}
		return []byte(reply), nil
	}
	t.Cleanup(func() { opExec = prev })
}

func (f *fakeOp) count(sub string) (n int) {
	for _, c := range f.calls {
		if c[0]+" "+c[1] == sub {
			n++
		}
	}
	return
}

func TestOpRunRefusesAnythingThatReadsAValue(t *testing.T) {
	f := &fakeOp{}
	f.install(t)
	for _, args := range [][]string{
		{"item", "get", "x"},
		{"read", "op://Private/x/password"},
		{"item", "edit", "x"},
		{"inject"},
	} {
		if _, err := opRun(args...); err == nil {
			t.Errorf("opRun(%v) was allowed", args)
		}
	}
	if len(f.calls) != 0 {
		t.Errorf("a refused command still reached op: %v", f.calls)
	}
}

func TestValidateVaultAcceptsOnlyPersonal(t *testing.T) {
	f := &fakeOp{replies: map[string]string{"vault get": `{"id":"abc123","name":"Employee","type":"PERSONAL"}`}}
	f.install(t)
	ref, err := op.Validate("Employee")
	if err != nil || ref.ID != "abc123" || ref.Name != "Employee" || ref.Manager != "1password" {
		t.Fatalf("got %+v, %v", ref, err)
	}
	if len(f.calls) != 1 {
		t.Errorf("validation cost %d op calls, want exactly one", len(f.calls))
	}

	for _, typ := range []string{"USER_CREATED", "EVERYONE", "TRANSFER", ""} {
		f.replies["vault get"] = fmt.Sprintf(`{"id":"zzz","name":"Team","type":%q}`, typ)
		if _, err := op.Validate("Team"); err == nil {
			t.Errorf("vault of type %q was accepted", typ)
		}
	}
}

func TestInitKeepsTheChosenVault(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := saveConfig(Config{Profile: "debian", Host: "a", Vault: &VaultRef{Manager: "1password", ID: "abc123", Name: "Private"}}); err != nil {
		t.Fatal(err)
	}
	if err := cmdInit([]string{"--host", "b"}); err != nil {
		t.Fatal(err)
	}
	cfg, _ := loadConfig()
	if cfg.Host != "b" || cfg.Vault == nil || cfg.Vault.ID != "abc123" {
		t.Errorf("after re-init: %+v", cfg)
	}
}

var testVault = VaultRef{Manager: "1password", ID: "v1", Name: "Private"}

var op = onePassword{}

func item(id, title, category, vaultID string, tags ...string) opItem {
	it := opItem{ID: id, Title: title, Tags: tags, Category: category}
	it.Vault.ID = vaultID
	return it
}

// jat must never read an item it did not write, even one it can plainly see.
// Tag AND title AND vault AND category, or it is not jat's.
func TestOnlyItemsJatWroteAreEverFetched(t *testing.T) {
	items := []opItem{
		item("good", "jat/migrate/7f3a/old-mac/james", "DOCUMENT", "v1", "jat-migrate", "jat-migrate-7f3a"),
		item("no-title", "My bank login", "DOCUMENT", "v1", "jat-migrate", "jat-migrate-7f3a"),
		item("no-tag", "jat/migrate/7f3a/old-mac/james", "DOCUMENT", "v1"),
		item("half-tag", "jat/migrate/7f3a/old-mac/james", "DOCUMENT", "v1", "jat-migrate"),
		item("key-mismatch", "jat/migrate/7f3a/old-mac/james", "DOCUMENT", "v1", "jat-migrate", "jat-migrate-0000"),
		item("login", "jat/migrate/7f3a/old-mac/james", "LOGIN", "v1", "jat-migrate", "jat-migrate-7f3a"),
		item("other-vault", "jat/migrate/7f3a/old-mac/james", "DOCUMENT", "shared", "jat-migrate", "jat-migrate-7f3a"),
		item("title-suffix", "jat/migrate/7f3a/old-mac/james/../x", "DOCUMENT", "v1", "jat-migrate", "jat-migrate-7f3a"),
	}
	b, _ := json.Marshal(items)
	f := &fakeOp{replies: map[string]string{"item list": string(b)}}
	f.install(t)

	found, err := listVaultBundles(op, testVault, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Item.ID != "good" {
		t.Fatalf("accepted %+v, want only the item carrying every mark", found)
	}
	if found[0].Key != "7f3a" || found[0].Host != "old-mac" || found[0].User != "james" {
		t.Errorf("listing fields wrong: %+v", found[0])
	}

	path, cleanup, err := fetchVaultBundle(op, testVault, found[0])
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if got, _ := os.ReadFile(path); string(got) != "bundle bytes" {
		t.Errorf("fetched %q", got)
	}
	for _, c := range f.calls {
		if c[0] == "document" && c[2] != "good" {
			t.Errorf("fetched an item jat did not write: %v", c)
		}
		if c[0] == "document" && !slices.Contains(c, "v1") {
			t.Errorf("fetch was not pinned to the configured vault: %v", c)
		}
	}
	if f.count("document get") != 1 {
		t.Errorf("document get ran %d times", f.count("document get"))
	}
}

func TestKeyIsValidatedBeforeItReachesOp(t *testing.T) {
	f := &fakeOp{replies: map[string]string{"item list": `[]`}}
	f.install(t)
	for _, bad := range []string{"7f3a,other-tag", "../x", "7F3A", "7f3", "--vault"} {
		if _, err := listVaultBundles(op, testVault, bad); err == nil {
			t.Errorf("key %q was accepted", bad)
		}
	}
	if len(f.calls) != 0 {
		t.Errorf("a bad key still reached op: %v", f.calls)
	}
}

func TestSendFilesWithBothMarksInThePrivateVault(t *testing.T) {
	f := &fakeOp{replies: map[string]string{"document create": `{"uuid":"new"}`}}
	f.install(t)
	title, err := sendVaultBundle(op, testVault, "/tmp/x.tar.gz", Manifest{Key: "7f3a", Host: "Old Mac!", User: "James T"})
	if err != nil {
		t.Fatal(err)
	}
	if !migrateTitle.MatchString(title) {
		t.Errorf("title %q does not match the pattern receive insists on", title)
	}
	got := strings.Join(f.calls[0], " ")
	for _, want := range []string{"--vault v1", "--tags jat-migrate,jat-migrate-7f3a", "--title " + title} {
		if !strings.Contains(got, want) {
			t.Errorf("document create missing %q: %s", want, got)
		}
	}
	// What send writes, receive must recognise.
	b, _ := json.Marshal([]opItem{item("new", title, "DOCUMENT", "v1", "jat-migrate", "jat-migrate-7f3a")})
	f.replies["item list"] = string(b)
	if found, err := listVaultBundles(op, testVault, ""); err != nil || len(found) != 1 {
		t.Errorf("receive would refuse what send just filed: %v %v", found, err)
	}
}

// The vault is a courier: what comes out is byte-for-byte a file-transport
// bundle and goes through the same readBundle.
func TestVaultTransportCarriesTheSameBundle(t *testing.T) {
	home := t.TempDir()
	bundle := testBundle(t, []string{"zsh"}, map[string]string{"home/.zshrc": "x"})
	raw, _ := os.ReadFile(bundle)

	prev := opExec
	t.Cleanup(func() { opExec = prev })
	var stored []byte
	opExec = func(args ...string) ([]byte, error) {
		switch args[0] + " " + args[1] {
		case "document create":
			stored, _ = os.ReadFile(args[2])
			return []byte(`{}`), nil
		case "item list":
			b, _ := json.Marshal([]opItem{item("d1", "jat/migrate/7f3a/old/u", "DOCUMENT", "v1", "jat-migrate", "jat-migrate-7f3a")})
			return b, nil
		case "document get":
			return nil, os.WriteFile(args[slices.Index(args, "--out-file")+1], stored, 0o600)
		}
		return nil, fmt.Errorf("unexpected %v", args)
	}

	if _, err := sendVaultBundle(op, testVault, bundle, Manifest{Key: "7f3a", Host: "old", User: "u"}); err != nil {
		t.Fatal(err)
	}
	found, err := listVaultBundles(op, testVault, "7f3a")
	if err != nil || len(found) != 1 {
		t.Fatalf("found %v, %v", found, err)
	}
	path, cleanup, err := fetchVaultBundle(op, testVault, found[0])
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(raw) {
		t.Error("bundle changed in transit")
	}
	if _, items, err := readBundle(path, home); err != nil || len(items) != 1 || items[0].Name != "zsh" {
		t.Errorf("readBundle on the fetched copy: %v %v", items, err)
	}
	cleanup()
	if _, err := os.Stat(filepath.Dir(path)); err == nil {
		t.Error("temp bundle left on disk after cleanup")
	}
}
