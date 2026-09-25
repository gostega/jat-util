package cli

import (
	"flag"
	"slices"
	"strings"
	"testing"
)

func TestResolve(t *testing.T) {
	cases := []struct {
		tool     string
		profile  string
		override string
		want     string
		wantErr  bool
	}{
		{tool: "gh", profile: "macos", want: "brew"},
		{tool: "node", profile: "macos", want: "mise"},         // mise outranks the brew formula
		{tool: "vscode", profile: "macos", want: "cask"},       // vetoed from manual to cask
		{tool: "pulumi", profile: "macos", want: "curlscript"}, // no brew entry, so the script wins
		{tool: "nmap", profile: "debian", want: "apt"},
		{tool: "mise", profile: "macos", override: "curlscript", want: "curlscript"},
		{tool: "gh", profile: "macos", override: "npm", wantErr: true}, // method the tool lacks
		{tool: "gh", profile: "freebsd", wantErr: true},                // unknown profile
		{tool: "ghostty", profile: "debian", wantErr: true},            // cask-only on a linux profile
	}

	for _, c := range cases {
		got, _, err := resolve(tools[c.tool], c.profile, c.override)
		if c.wantErr {
			if err == nil {
				t.Errorf("resolve(%s, %s, %q): want error, got %q", c.tool, c.profile, c.override, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("resolve(%s, %s, %q): %v", c.tool, c.profile, c.override, err)
			continue
		}
		if got != c.want {
			t.Errorf("resolve(%s, %s, %q) = %q, want %q", c.tool, c.profile, c.override, got, c.want)
		}
	}
}

// Every seeded tool must resolve on at least one profile, or it's dead data.
func TestEveryToolResolvesSomewhere(t *testing.T) {
	for name, tool := range tools {
		ok := false
		for profile := range profileOrder {
			if _, _, err := resolve(tool, profile, ""); err == nil {
				ok = true
			}
		}
		if !ok {
			t.Errorf("%s: resolves on no profile", name)
		}
	}
}

func TestIsReleaseVersion(t *testing.T) {
	for v, want := range map[string]bool{
		"1.2.3":       true,
		"v1.2.3":      true,
		"dev":         false,
		"b9bdf3f":     false, // untagged local build
		"1.2.3-dirty": false, // built from a modified tree
		"":            false,
	} {
		if got := isReleaseVersion(v); got != want {
			t.Errorf("isReleaseVersion(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestBumpVersion(t *testing.T) {
	for _, c := range []struct{ cur, bump, want string }{
		{"v1.2.3", "patch", "v1.2.4"},
		{"v1.2.3", "minor", "v1.3.0"},
		{"v1.2.3", "major", "v2.0.0"},
		{"1.2.3", "patch", "v1.2.4"},
	} {
		got, err := bumpVersion(c.cur, c.bump)
		if err != nil || got != c.want {
			t.Errorf("bumpVersion(%q, %q) = %q, %v; want %q", c.cur, c.bump, got, err, c.want)
		}
	}
	if _, err := bumpVersion("not-a-version", "patch"); err == nil {
		t.Error("bumpVersion: want error on non-semver")
	}
	if _, err := bumpVersion("v1.2.3", "sideways"); err == nil {
		t.Error("bumpVersion: want error on unknown bump")
	}
}

func TestSlugHost(t *testing.T) {
	// Becomes a path segment when config is looked up per-machine, so nothing
	// that could carry a separator or traverse upwards may survive.
	for in, want := range map[string]string{
		"work-mac":         "work-mac",
		"Work Mac":         "work-mac",
		"MAC-WT2XY0206L":   "mac-wt2xy0206l",
		"thing.local":      "thing-local",
		"../../etc/passwd": "etcpasswd",
		"a/b":              "ab",
		"///":              "",
		"":                 "",
	} {
		if got := slugHost(in); got != want {
			t.Errorf("slugHost(%q) = %q, want %q", in, got, want)
		}
	}
	for _, in := range []string{"../../etc/passwd", "a/b", "x\\y", "a b/../c"} {
		if got := slugHost(in); strings.ContainsAny(got, `/\.`) {
			t.Errorf("slugHost(%q) = %q, still contains a path character", in, got)
		}
	}
}

func TestBundleRel(t *testing.T) {
	// A bundle is untrusted input: these must never resolve to a path outside
	// $HOME, however the entry is spelled.
	for _, bad := range []string{
		"home/../../.ssh/authorized_keys",
		"home/../.bashrc",
		"home/../../../etc/passwd",
		"/etc/passwd",
		"etc/passwd", // missing the prefix entirely
		"home/..",
		"manifest.json.evil",
	} {
		if got, err := bundleRel(bad); err == nil {
			t.Errorf("bundleRel(%q) = %q, want error", bad, got)
		}
	}

	for in, want := range map[string]string{
		"home/.bashrc":                ".bashrc",
		"home/.config/ghostty/config": ".config/ghostty/config",
		"home/.aws/config":            ".aws/config",
		"home/a/../b":                 "b", // cleaned, still inside
	} {
		got, err := bundleRel(in)
		if err != nil || got != want {
			t.Errorf("bundleRel(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestConfigsSecretsAreMarked(t *testing.T) {
	// Anything holding keys or tokens must be Secret, or it lands in a bundle
	// by default. Guards against a future path being added to the wrong entry.
	mustBeSecret := []string{".ssh", ".gnupg", ".aws/credentials", ".aws/sso", ".config/gh", ".claude.json"}
	for name, item := range configs {
		if item.Secret {
			continue
		}
		for _, p := range item.Paths {
			for _, s := range mustBeSecret {
				if p == s {
					t.Errorf("configs[%q] contains %q but is not marked Secret", name, p)
				}
			}
		}
	}
}

func TestFlagsFirstKeepsValuesWithTheirFlags(t *testing.T) {
	newFS := func() *flag.FlagSet {
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		fs.String("transport", "", "")
		fs.Bool("all", false, "")
		fs.Bool("show", false, "")
		return fs
	}
	for _, tc := range []struct{ in, want []string }{
		// The bug: --all was hoisted next to --transport and became its value.
		{[]string{"--transport", "file", "--all"}, []string{"--transport", "file", "--all"}},
		{[]string{"b.tar.gz", "--transport", "file", "--show"}, []string{"--transport", "file", "--show", "b.tar.gz"}},
		{[]string{"b.tar.gz", "--transport=file", "--show"}, []string{"--transport=file", "--show", "b.tar.gz"}},
		{[]string{"gh", "--show"}, []string{"--show", "gh"}},
		{[]string{"--show", "--", "-odd-name"}, []string{"--show", "--", "-odd-name"}},
	} {
		if got := flagsFirst(newFS(), tc.in); !slices.Equal(got, tc.want) {
			t.Errorf("flagsFirst(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
