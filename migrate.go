package main

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Transports carry the bundle; they do not change what is in it. One
// serialiser, one picker, one set of bugs.
var transports = []string{"file", "1password"}

func cmdMigrate(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: jat migrate <send|receive> [flags]")
	}
	switch args[0] {
	case "send":
		return migrateSend(args[1:])
	case "receive":
		return fmt.Errorf("jat migrate receive is not built yet — use `jat import <bundle>` for now")
	default:
		return fmt.Errorf("unknown migrate subcommand %q (want send or receive)", args[0])
	}
}

func migrateSend(args []string) error {
	fs := flag.NewFlagSet("migrate send", flag.ExitOnError)
	transport := fs.String("transport", "", "how the bundle travels: "+strings.Join(transports, ", "))
	out := fs.String("out", "", "for --transport file: where to write (default: ./jat-migrate-<key>.tar.gz)")
	withSecrets := fs.Bool("include-secrets", false, "offer keys, tokens and credentials as well")
	all := fs.Bool("all", false, "skip the picker and send everything available")
	fs.Parse(flagsFirst(args))

	if *transport == "" {
		*transport = "file"
	}
	if !slices.Contains(transports, *transport) {
		return fmt.Errorf("unknown transport %q (want %s)", *transport, strings.Join(transports, " or "))
	}
	if *transport == "1password" {
		return fmt.Errorf("the 1password transport is not built yet — use --transport file")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	available := availableConfigs(home, *withSecrets)
	if len(available) == 0 {
		return fmt.Errorf("none of the known config paths exist on this machine")
	}

	chosen := available
	if !*all {
		rows := make([]PickRow, 0, len(available))
		for _, name := range available {
			item := configs[name]
			note := item.Desc
			colour := ""
			if item.Secret {
				note = "SECRET — " + item.Desc
				colour = "9"
			}
			rows = append(rows, PickRow{
				ID: name, Label: name, Note: note, NoteColor: colour,
				// Secrets are never ticked by default, even when offered.
				Selected: !item.Secret,
			})
		}
		picked, ok, err := runPicker("What should leave this machine?",
			fmt.Sprintf("host %s · nothing selected means cancel", cfg.Host), rows)
		if err != nil {
			return err
		}
		if !ok || len(picked) == 0 {
			fmt.Fprintln(os.Stderr, "Nothing selected — no bundle written.")
			return nil
		}
		chosen = picked
	}

	key, err := migrationKey()
	if err != nil {
		return err
	}
	dest := *out
	if dest == "" {
		dest = fmt.Sprintf("jat-migrate-%s.tar.gz", key)
	}

	man := Manifest{
		Created: time.Now(),
		Host:    cfg.Host,
		Profile: cfg.Profile,
		Key:     key,
		User:    osUsername(),
	}
	if err := writeBundle(dest, home, chosen, &man); err != nil {
		return err
	}

	fmt.Printf("wrote %s\n", dest)
	fmt.Printf("  key:      %s\n", key)
	fmt.Printf("  from:     %s (%s) as %s\n", man.Host, man.Profile, man.User)
	fmt.Printf("  captured: %s\n", strings.Join(man.Captured, ", "))
	if !*withSecrets {
		fmt.Println("  secrets were not offered — pass --include-secrets to include them")
	}
	return nil
}

// availableConfigs is every known config with at least one path present here,
// in a stable order. Secrets are left out entirely unless asked for, so they
// cannot be selected by accident.
func availableConfigs(home string, withSecrets bool) []string {
	var out []string
	for _, name := range slices.Sorted(maps(configs)) {
		item := configs[name]
		if item.Secret && !withSecrets {
			continue
		}
		for _, rel := range item.Paths {
			if _, err := os.Lstat(filepath.Join(home, rel)); err == nil {
				out = append(out, name)
				break
			}
		}
	}
	return out
}

// migrationKey is a short identifier for telling two in-flight migrations
// apart. Not a cryptographic key and never used as one.
func migrationKey() (string, error) {
	b := make([]byte, 2)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func osUsername() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return "unknown"
}
