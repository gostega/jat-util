package cli

import "testing"

func TestUserLayerOverBuiltins(t *testing.T) {
	cfg := Config{
		Profile: "macos",
		Installers: map[string]Installer{
			"claude-native": {Method: "curlscript", Command: "curl -fsSL https://claude.ai/install.sh | bash"},
			"my-tool":       {Method: "npm", Command: "npm install -g my-tool"},
		},
		Defaults: map[string]string{
			"claude":  "claude-native", // built-in tool, new method
			"my-tool": "my-tool",       // tool jat has never heard of
			"mise":    "curlscript",    // built-in method chosen over the profile's pick
		},
	}
	all := effectiveTools(cfg)
	cases := []struct {
		tool, override, want, wantCmd string
		wantErr                       bool
	}{
		{tool: "claude", want: "curlscript", wantCmd: "curl -fsSL https://claude.ai/install.sh | bash"},
		{tool: "claude", override: "manual", want: "manual"},                                                                    // --<method> still wins
		{tool: "claude", override: "curlscript", want: "curlscript", wantCmd: "curl -fsSL https://claude.ai/install.sh | bash"}, // the user's curlscript
		{tool: "my-tool", want: "npm", wantCmd: "npm install -g my-tool"},
		{tool: "mise", want: "curlscript", wantCmd: "curl https://mise.run | sh"},
		{tool: "mise", override: "brew", want: "brew", wantCmd: "brew install mise"},
		{tool: "gh", want: "brew", wantCmd: "brew install gh"}, // untouched
		{tool: "my-tool", override: "brew", wantErr: true},
	}
	for _, c := range cases {
		got, cmd, err := resolve(all[c.tool], cfg.Profile, c.override)
		if c.wantErr {
			if err == nil {
				t.Errorf("%s --%s: want error, got %q", c.tool, c.override, got)
			}
			continue
		}
		if err != nil || got != c.want || cmd != c.wantCmd {
			t.Errorf("%s --%s = %q, %q, %v; want %q, %q", c.tool, c.override, got, cmd, err, c.want, c.wantCmd)
		}
	}

	// The built-in table is copied, never written through.
	if _, ok := tools["claude"].Methods["curlscript"]; ok {
		t.Error("effectiveTools wrote into the built-in table")
	}
	if _, ok := tools["my-tool"]; ok {
		t.Error("effectiveTools added to the built-in table")
	}
}

func TestValidateInstaller(t *testing.T) {
	cases := []struct {
		name string
		inst Installer
		ok   bool
	}{
		{"claude-native", Installer{Method: "curlscript", Command: "curl x | sh"}, true},
		{"vendor-page", Installer{Method: "manual", Docs: "https://example.test"}, true},
		{"brew", Installer{Method: "brew", Command: "brew install x"}, false}, // shadows a method
		{"Bad Name", Installer{Method: "brew", Command: "brew install x"}, false},
		{"x", Installer{Method: "pip", Command: "pip install x"}, false}, // unknown method
		{"x", Installer{Method: "curlscript"}, false},                    // nothing to run
		{"x", Installer{Method: "manual", Command: "echo hi"}, false},    // manual never runs
	}
	for _, c := range cases {
		if err := validateInstaller(c.name, c.inst); (err == nil) != c.ok {
			t.Errorf("validateInstaller(%q, %+v) = %v, want ok=%v", c.name, c.inst, err, c.ok)
		}
	}
}
