package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// firstRelease is what a repo with no published releases starts at.
const firstRelease = "v0.1.0"

func cmdRelease(args []string) error {
	fs := flag.NewFlagSet("release", flag.ExitOnError)
	remote := fs.String("remote", "origin", "git remote to push the tag to")
	yes := fs.Bool("yes", false, "skip the confirmation prompt")
	fs.Parse(flagsFirst(args))

	bump := "patch"
	if fs.NArg() > 1 {
		return fmt.Errorf("usage: jat release [patch|minor|major] [--remote <name>] [--yes]")
	}
	if fs.NArg() == 1 {
		bump = fs.Arg(0)
	}

	// Refuse anything but a clean, pushed main: a tag is public the moment it
	// lands, and pointing one at a commit nobody else has is not undoable.
	branch, err := git("branch", "--show-current")
	if err != nil {
		return err
	}
	if branch != "main" {
		return fmt.Errorf("releases are cut from main (currently on %q)", branch)
	}
	dirty, err := git("status", "--porcelain")
	if err != nil {
		return err
	}
	if dirty != "" {
		return fmt.Errorf("working tree has uncommitted changes — commit or stash first")
	}
	if err := headMatchesRemote(*remote, branch); err != nil {
		return err
	}

	current, err := latestReleaseTag()
	if err != nil {
		return err
	}

	var next string
	if current == "" {
		next = firstRelease
		fmt.Printf("No published releases yet — starting at %s\n", next)
	} else {
		if next, err = bumpVersion(current, bump); err != nil {
			return err
		}
		fmt.Printf("Release %s -> %s (%s)\n", current, next, bump)
	}
	fmt.Printf("Repo:   %s\nRemote: %s\n\n", repo, *remote)

	commits, err := commitsSince(current)
	if err != nil {
		return err
	}
	if len(commits) == 0 {
		fmt.Println("No commits since the last release — this will tag the same commit again.")
	} else {
		fmt.Printf("Commits to be released (%d):\n", len(commits))
		for _, c := range commits {
			fmt.Printf("  %s\n", c)
		}
	}
	fmt.Println()

	if !*yes {
		ok, err := confirm(fmt.Sprintf("Tag HEAD as %s and push to %s?", next, *remote))
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("aborted")
		}
	}

	// Belt and braces: whatever led here, never try to move an existing tag.
	if _, err := git("rev-parse", "-q", "--verify", "refs/tags/"+next); err == nil {
		return fmt.Errorf("tag %s already exists locally — the release list and your tags disagree; check `gh release list --repo %s`", next, repo)
	}
	if _, err := git("tag", "-a", next, "-m", "Release "+next); err != nil {
		return err
	}
	if _, err := git("push", *remote, next); err != nil {
		// Roll the local tag back so a retry isn't blocked by a tag that exists
		// here and nowhere else.
		if _, delErr := git("tag", "-d", next); delErr != nil {
			return fmt.Errorf("push failed (%w) and the local tag %s could not be removed: %v", err, next, delErr)
		}
		return fmt.Errorf("push failed, local tag rolled back: %w", err)
	}

	fmt.Printf("Released %s — the workflow builds and publishes the binaries.\n", next)
	return nil
}

// headMatchesRemote fetches, then requires HEAD to be exactly the remote tip,
// so a release can't tag a commit that hasn't been pushed.
func headMatchesRemote(remote, branch string) error {
	if _, err := git("fetch", "--quiet", remote, branch); err != nil {
		return err
	}
	local, err := git("rev-parse", "HEAD")
	if err != nil {
		return err
	}
	upstream, err := git("rev-parse", remote+"/"+branch)
	if err != nil {
		return err
	}
	if local != upstream {
		return fmt.Errorf("HEAD (%.7s) does not match %s/%s (%.7s) — push or pull first",
			local, remote, branch, upstream)
	}
	return nil
}

var semverRe = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)`)

func bumpVersion(current, bump string) (string, error) {
	m := semverRe.FindStringSubmatch(current)
	if m == nil {
		return "", fmt.Errorf("latest release %q is not semver (vX.Y.Z)", current)
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	patch, _ := strconv.Atoi(m[3])

	switch bump {
	case "major":
		major, minor, patch = major+1, 0, 0
	case "minor":
		minor, patch = minor+1, 0
	case "patch":
		patch++
	default:
		return "", fmt.Errorf("unknown bump %q (want patch, minor or major)", bump)
	}
	return fmt.Sprintf("v%d.%d.%d", major, minor, patch), nil
}

func commitsSince(tag string) ([]string, error) {
	spec := "HEAD"
	if tag != "" {
		spec = tag + "..HEAD"
	}
	out, err := git("log", "--oneline", spec)
	if err != nil || out == "" {
		return nil, err
	}
	return strings.Split(out, "\n"), nil
}

func confirm(prompt string) (bool, error) {
	fmt.Printf("%s [y/N] ", prompt)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

func git(args ...string) (string, error) {
	out, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}
