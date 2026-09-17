package main

import "testing"

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
