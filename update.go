package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// repo is where releases are published. Both update and release read it.
const repo = "gostega/jat-util"

// assetName must match what the release workflow uploads.
func assetName() string {
	return fmt.Sprintf("jat-%s-%s", runtime.GOOS, runtime.GOARCH)
}

func cmdUpdate(args []string) error {
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	check := fs.Bool("check", false, "report the latest version without installing it")
	fs.Parse(args)

	latest, err := latestReleaseTag()
	if err != nil {
		return err
	}

	if *check {
		fmt.Printf("installed: %s\n", Version)
		if latest == "" {
			fmt.Printf("latest:    (no releases published for %s yet)\n", repo)
			return nil
		}
		fmt.Printf("latest:    %s\n", latest)
		if sameVersion(Version, latest) {
			fmt.Println("up to date")
		}
		return nil
	}

	if latest == "" {
		return fmt.Errorf("%s has no published releases to update to — cut one with `jat release`", repo)
	}
	// Anything that isn't a plain semver came from a local build: the Makefile
	// stamps in `git describe`, so an untagged tree yields a bare sha and a
	// modified one a -dirty suffix. None of those should be silently replaced
	// by a release binary.
	if !isReleaseVersion(Version) {
		return fmt.Errorf("this is a local build (version %q) — refusing to replace it with release %s; use `make install` instead", Version, latest)
	}
	if sameVersion(Version, latest) {
		fmt.Printf("already on %s\n", latest)
		return nil
	}

	dest, err := os.Executable()
	if err != nil {
		return fmt.Errorf("could not locate the running binary: %w", err)
	}
	// Replace what the symlink points at, not the symlink itself.
	if resolved, err := filepath.EvalSymlinks(dest); err == nil {
		dest = resolved
	}

	fmt.Printf("updating %s: %s -> %s\n", dest, Version, latest)
	if err := downloadRelease(latest, dest); err != nil {
		return err
	}
	fmt.Printf("updated to %s\n", latest)
	return nil
}

// downloadRelease fetches this platform's asset and moves it over dest.
//
// Staged alongside dest and renamed in, never downloaded over it: writing in
// place keeps dest's inode, and rewriting the inode of a binary that a process
// still has mapped as running code can wedge it. A rename swaps the directory
// entry, leaving the running process on its old inode.
func downloadRelease(tag, dest string) error {
	staged := fmt.Sprintf("%s.new-%d", dest, os.Getpid())
	defer os.Remove(staged)

	out, err := exec.Command("gh", "release", "download", tag,
		"--repo", repo, "--pattern", assetName(), "--output", staged).CombinedOutput()
	if err != nil {
		return fmt.Errorf("could not download %s from release %s: %w: %s",
			assetName(), tag, err, strings.TrimSpace(string(out)))
	}
	if err := os.Chmod(staged, 0o755); err != nil {
		return err
	}
	return os.Rename(staged, dest)
}

// latestReleaseTag asks gh rather than reading git tags, so it reflects what
// was actually published. Returns "" when the repo has no releases yet.
func latestReleaseTag() (string, error) {
	out, err := exec.Command("gh", "release", "view",
		"--repo", repo, "--json", "tagName", "-q", ".tagName").CombinedOutput()
	if err == nil {
		return strings.TrimSpace(string(out)), nil
	}
	if !strings.Contains(string(out), "release not found") {
		return "", fmt.Errorf("could not read the latest release for %s: %w: %s",
			repo, err, strings.TrimSpace(string(out)))
	}
	// "release not found" is also what gh says when it cannot see the repo at
	// all — a switched `gh auth` account, say. Treating that as "no releases
	// yet" would silently restart versioning from the first tag, so confirm
	// the repo really is visible before believing it.
	if vErr := exec.Command("gh", "repo", "view", repo, "--json", "name").Run(); vErr != nil {
		return "", fmt.Errorf("cannot see %s — check `gh auth status`, the active account may not have access", repo)
	}
	return "", nil
}

// sameVersion compares ignoring a leading "v", since tags carry it and the
// ldflags-injected version doesn't.
func sameVersion(a, b string) bool {
	return strings.TrimPrefix(a, "v") == strings.TrimPrefix(b, "v")
}

// isReleaseVersion reports whether v looks like it came from a release build
// rather than a local one. "1.2.3" yes; "dev", "b9bdf3f", "1.2.3-dirty" no.
func isReleaseVersion(v string) bool {
	return semverRe.MatchString(v) && !strings.HasSuffix(v, "-dirty")
}
