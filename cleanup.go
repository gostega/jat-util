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

func migrateCleanup(args []string) error {
	fs_ := flag.NewFlagSet("migrate cleanup", flag.ExitOnError)
	show := fs_.Bool("show", false, "list the backups that would be deleted, and delete nothing")
	yes := fs_.Bool("yes", false, "delete without asking")
	fs_.Parse(flagsFirst(fs_, args))
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	if fs_.NArg() == 0 {
		return listBackupKeys(home)
	}
	key := fs_.Arg(0)
	if fs_.NArg() > 1 || (key != "nokey" && !migrateKeyRe.MatchString(key)) {
		return fmt.Errorf("usage: jat migrate cleanup [<key>] [--show] [--yes]")
	}

	backups, err := recordedBackups(home, key)
	if err != nil {
		return err
	}
	if len(backups) == 0 {
		fmt.Printf("no backups from migration %s on this machine\n", key)
		os.Remove(backupLog(home, key))
		return nil
	}

	verb := "would delete"
	if !*show {
		verb = "delete"
	}
	for _, rel := range backups {
		fmt.Printf("  %s  %s\n", verb, rel)
	}
	if *show {
		fmt.Printf("would delete %d backup(s) from migration %s\n", len(backups), key)
		return nil
	}
	if !*yes {
		if !stdinIsTerminal() {
			return fmt.Errorf("nothing deleted — pass --yes to delete these %d file(s) without a terminal", len(backups))
		}
		fmt.Printf("delete these %d file(s)? the originals they hold are gone for good [y/N] ", len(backups))
		answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
			fmt.Println("nothing deleted")
			return nil
		}
	}
	for _, rel := range backups {
		if err := os.Remove(filepath.Join(home, rel)); err != nil {
			return err
		}
	}
	fmt.Printf("deleted %d backup(s) from migration %s\n", len(backups), key)
	return os.Remove(backupLog(home, key))
}

func listBackupKeys(home string) error {
	entries, _ := os.ReadDir(filepath.Join(home, ".jat", "backups"))
	found := false
	for _, e := range entries {
		backups, err := recordedBackups(home, e.Name())
		if err != nil || len(backups) == 0 {
			continue
		}
		found = true
		fmt.Printf("  %s  %d backup(s)\n", e.Name(), len(backups))
	}
	if !found {
		fmt.Println("no backups on this machine")
		return nil
	}
	fmt.Println("jat migrate cleanup <key> --show   to see them")
	return nil
}
