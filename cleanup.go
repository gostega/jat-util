package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Receive keeps a copy of every file it overwrites, named after the migration
// that replaced it: .gitconfig.pre-jat-7f3a. The copies are deliberate litter,
// so each one is also written down in ~/.jat/backups/<key> — cleanup deletes
// from that list rather than sweeping $HOME for a suffix, which would be slow
// and would trust any file that happened to carry the name.

// backupKey stands in for bundles with no key (ones the old export wrote).
func backupKey(key string) string {
	if migrateKeyRe.MatchString(key) {
		return key
	}
	return "nokey"
}

func backupSuffix(key string) string { return ".pre-jat-" + backupKey(key) }

var backupNameRe = regexp.MustCompile(`\.pre-jat-([0-9a-f]{4}|nokey)$`)

func isBackupName(p string) bool { return backupNameRe.MatchString(p) }

func backupLog(home, key string) string {
	return filepath.Join(home, ".jat", "backups", backupKey(key))
}

// backupBeforeOverwrite copies home/rel beside itself and records it. An
// existing backup is left alone: receiving the same migration twice must not
// replace the true original with the first run's result.
func backupBeforeOverwrite(home, rel, key string) error {
	src := filepath.Join(home, rel)
	dst := src + backupSuffix(key)
	if _, err := os.Lstat(dst); err == nil {
		return nil
	}
	in, err := os.Open(src) // follows a symlink, like the comparison did
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(backupLog(home, key)), 0o700); err != nil {
		return err
	}
	log, err := os.OpenFile(backupLog(home, key), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer log.Close()
	_, err = fmt.Fprintln(log, rel+backupSuffix(key))
	return err
}

// recordedBackups returns the backups logged for key that still exist. Every
// line is checked again before it can become a deletion: it must end in this
// key's suffix and stay inside home, whatever the log file says.
func recordedBackups(home, key string) ([]string, error) {
	f, err := os.Open(backupLog(home, key))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		rel := sc.Text()
		if !strings.HasSuffix(rel, backupSuffix(key)) {
			continue
		}
		if _, err := bundleRel(bundlePrefix + rel); err != nil {
			continue
		}
		if info, err := os.Lstat(filepath.Join(home, rel)); err != nil || !info.Mode().IsRegular() {
			continue
		}
		if !slices.Contains(out, rel) {
			out = append(out, rel)
		}
	}
	return out, sc.Err()
}

// A cleanup target is either the backups one migration left on this
// machine or the migration's document in the vault. Both are named by key,
// and `cleanup <key>` takes both at once.
type cleanupTarget struct {
	ID    string // "local:<key>" or "vault:<key>"
	Key   string
	Label string
	Note  string
	files []string    // local: the backups
	vb    vaultBundle // vault: the item
	conn  Connector
	vault VaultRef
}

// cleanupTargets gathers everything that could be cleaned up: local backup
// sets always; vault migrations when a vault is configured and reachable.
// A vault that cannot be reached is reported, not fatal — the local half
// still works, and so does a machine with no vault at all.
func cleanupTargets(home, key string) (targets []cleanupTarget, vaultNote string) {
	entries, _ := os.ReadDir(filepath.Join(home, ".jat", "backups"))
	for _, e := range entries {
		if key != "" && e.Name() != key {
			continue
		}
		backups, err := recordedBackups(home, e.Name())
		if err != nil || len(backups) == 0 {
			continue
		}
		targets = append(targets, cleanupTarget{
			ID: "local:" + e.Name(), Key: e.Name(), Label: "backups " + e.Name(),
			Note:  fmt.Sprintf("%d file(s) on this machine", len(backups)),
			files: backups,
		})
	}

	cfg, err := loadConfig()
	if err != nil || cfg.Vault == nil {
		return targets, ""
	}
	c, vault, err := configuredVault()
	if err != nil {
		return targets, "vault not checked: " + err.Error()
	}
	found, err := listVaultBundles(c, vault, key)
	if err != nil {
		return targets, "vault not checked: " + err.Error()
	}
	for _, vb := range found {
		targets = append(targets, cleanupTarget{
			ID: "vault:" + vb.Key, Key: vb.Key, Label: "vault " + vb.Key,
			Note: fmt.Sprintf("%s · from %s as %s · %s", c.Label(), vb.Host, vb.User, vb.Created.Local().Format("2 Jan 2006 15:04")),
			vb:   vb, conn: c, vault: vault,
		})
	}
	return targets, ""
}

func migrateCleanup(args []string) error {
	fs_ := flag.NewFlagSet("migrate cleanup", flag.ExitOnError)
	show := fs_.Bool("show", false, "list what would be deleted, and delete nothing")
	yes := fs_.Bool("yes", false, "delete without asking")
	fs_.Parse(flagsFirst(fs_, args))
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	key := fs_.Arg(0)
	if fs_.NArg() > 1 || (key != "" && key != "nokey" && !migrateKeyRe.MatchString(key)) {
		return fmt.Errorf("usage: jat migrate cleanup [<key>] [--show] [--yes]")
	}

	targets, vaultNote := cleanupTargets(home, key)
	if len(targets) == 0 {
		releasePickerScreen()
		if key != "" {
			fmt.Printf("nothing from migration %s to clean up\n", key)
			os.Remove(backupLog(home, key))
		} else {
			fmt.Println("nothing to clean up")
		}
		if vaultNote != "" {
			fmt.Println(vaultNote)
		}
		return nil
	}

	// No key: choose. Interactive when there is a terminal; otherwise list
	// and stop, since deleting by default is the wrong default.
	chosen := targets
	if key == "" && !*show {
		if !stdinIsTerminal() {
			releasePickerScreen()
			for _, t := range targets {
				fmt.Printf("  %-14s %s\n", t.Label, t.Note)
			}
			if vaultNote != "" {
				fmt.Println(vaultNote)
			}
			fmt.Println("pass a key to clean one migration up: jat migrate cleanup <key> [--show]")
			return nil
		}
		rows := make([]PickRow, 0, len(targets))
		for _, t := range targets {
			rows = append(rows, PickRow{ID: t.ID, Label: t.Label, Note: t.Note})
		}
		sub := "nothing is ticked until you tick it"
		if vaultNote != "" {
			sub = vaultNote
		}
		picked, ok, err := runPicker("What should be cleaned up?", sub, rows)
		if err != nil {
			return err
		}
		if !ok || len(picked) == 0 {
			releasePickerScreen()
			fmt.Fprintln(os.Stderr, "Nothing chosen — nothing deleted.")
			return nil
		}
		chosen = chosen[:0]
		for _, t := range targets {
			if slices.Contains(picked, t.ID) {
				chosen = append(chosen, t)
			}
		}
	}

	releasePickerScreen()
	verb := "delete"
	if *show {
		verb = "would delete"
	}
	n := 0
	for _, t := range chosen {
		if t.conn != nil {
			fmt.Printf("  %s  %s in %s vault %q  (%s)\n", verb, t.vb.Item.Title, t.conn.Label(), t.vault.Name, t.Note)
			n++
			continue
		}
		for _, rel := range t.files {
			fmt.Printf("  %s  %s\n", verb, rel)
			n++
		}
	}
	if *show {
		fmt.Printf("would delete %d thing(s)\n", n)
		if vaultNote != "" {
			fmt.Println(vaultNote)
		}
		return nil
	}
	if !*yes {
		if !stdinIsTerminal() {
			return fmt.Errorf("nothing deleted — pass --yes to delete these %d thing(s) without a terminal", n)
		}
		fmt.Printf("delete these %d thing(s)? backups hold the originals they replaced [y/N] ", n)
		answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
			fmt.Println("nothing deleted")
			return nil
		}
	}
	for _, t := range chosen {
		if t.conn != nil {
			if err := deleteVaultBundle(t.conn, t.vault, t.vb); err != nil {
				return fmt.Errorf("vault %s: %w", t.Key, err)
			}
			fmt.Printf("deleted migration %s from the vault\n", t.Key)
			continue
		}
		for _, rel := range t.files {
			if err := os.Remove(filepath.Join(home, rel)); err != nil {
				return err
			}
		}
		os.Remove(backupLog(home, t.Key))
		fmt.Printf("deleted %d backup(s) from migration %s\n", len(t.files), t.Key)
	}
	return nil
}
