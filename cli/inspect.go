package cli

import (
	"flag"
	"fmt"
	"os"
	"time"
)

// migrateInspect prints what a bundle holds. Plain stdout so it pipes and
// greps, and never file contents: a bundle may carry an --include-secrets
// payload, and this is the command that gets pasted into a chat.
func migrateInspect(args []string) error {
	fs_ := flag.NewFlagSet("migrate inspect", flag.ExitOnError)
	listFiles := fs_.Bool("files", false, "list every path rather than summarising per item")
	key := fs_.String("key", "", "inspect a migration waiting in the vault instead of a file")
	fs_.Parse(flagsFirst(fs_, args))
	if (fs_.NArg() == 1) == (*key != "") {
		return fmt.Errorf("usage: jat migrate inspect <bundle.tar.gz> | --key <key> [--files]")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	bundlePath := fs_.Arg(0)
	if *key != "" {
		path, cleanup, err := bundleFromVault(*key)
		if err != nil || path == "" {
			return err
		}
		defer cleanup()
		bundlePath = path
	}
	man, items, err := readBundle(bundlePath, home)
	if err != nil {
		return err
	}

	fmt.Printf("key:      %s\n", orUnknown(man.Key))
	fmt.Printf("created:  %s\n", man.Created.Format(time.RFC3339))
	fmt.Printf("from:     %s (%s) as %s\n", orUnknown(man.Host), orUnknown(man.Profile), orUnknown(man.User))
	for _, it := range items {
		secret := ""
		if it.Secret {
			secret = "  SECRET"
		}
		fmt.Printf("  %-16s %3d file(s)  %8s%s\n", it.Name, len(it.Files), humanSize(it.size()), secret)
		if *listFiles {
			for _, f := range it.Files {
				fmt.Printf("    %8s  %s\n", humanSize(f.Size), f.Rel)
			}
		}
	}
	for _, s := range man.Skipped {
		fmt.Printf("  skipped on send: %s\n", s)
	}
	return nil
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
