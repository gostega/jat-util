package cli

import "testing"

func TestRepoFromRemote(t *testing.T) {
	for url, want := range map[string]string{
		"git@github.com:gostega/screen-cam.git":       "gostega/screen-cam",
		"https://github.com/gostega/screen-cam.git":   "gostega/screen-cam",
		"https://github.com/gostega/screen-cam":       "gostega/screen-cam",
		"ssh://git@github.com/gostega/jat-util.git\n": "gostega/jat-util",
	} {
		if got, err := repoFromRemote(url); err != nil || got != want {
			t.Errorf("repoFromRemote(%q) = %q, %v; want %q", url, got, err, want)
		}
	}
	if _, err := repoFromRemote("git@gitlab.com:x/y.git"); err == nil {
		t.Error("a non-GitHub remote should be refused")
	}
}

func TestHighestTag(t *testing.T) {
	tags := []string{"v0.1.0", "v0.10.0-rc2", "v0.9.1", "v0.10.0-rc1", "junk", "v0.2.0", ""}
	if got := highestTag(tags); got != "v0.9.1" {
		t.Errorf("highestTag = %q, want v0.9.1 (rc tags never count)", got)
	}
	if got := highestTag(nil); got != "" {
		t.Errorf("no tags: %q", got)
	}
}

func TestCurrentVersion(t *testing.T) {
	cases := []struct {
		tag, released, want string
		fail                bool
	}{
		{"v0.5.0", "v0.5.0", "v0.5.0", false},
		{"v0.1.0", "", "v0.1.0", false}, // tagged by hand, no release yet
		{"", "", "", false},             // first release
		{"v0.4.0", "v0.5.0", "", true},  // tags here are behind GitHub
		{"", "v0.4.0", "", true},        // another repo's release, or no tags fetched
	}
	for _, c := range cases {
		got, err := currentVersion(c.tag, c.released, "o/r")
		if (err != nil) != c.fail || got != c.want {
			t.Errorf("currentVersion(%q, %q) = %q, %v", c.tag, c.released, got, err)
		}
	}
}
