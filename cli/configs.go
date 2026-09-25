package cli

// Config items jat knows how to carry between machines. Paths are relative to
// $HOME. This is a curated list, not a sweep of the home directory — if it
// isn't named here, migrate won't touch it.
//
// Secret items hold credentials, tokens or private keys. They are excluded
// unless --include-secrets is passed, so the safe thing happens by default and
// a bundle left sitting in Downloads isn't a key leak.
type ConfigItem struct {
	Paths  []string
	Secret bool
	Desc   string
}

var configs = map[string]ConfigItem{
	"bash": {
		Paths: []string{".bashrc", ".bash_profile", ".bash_aliases"},
		Desc:  "bash startup files (oh-my-bash is wired up in .bashrc)",
	},
	"zsh": {
		Paths: []string{".zshrc", ".zprofile"},
		Desc:  "zsh startup files",
	},
	"git": {
		// Glob rather than a list: includeIf overlays are named per identity,
		// so the set differs on every machine. ".gitconfig-*" catches them all
		// while leaving ".gitconfig.bak.*" and friends alone.
		Paths: []string{".gitconfig", ".gitconfig-*"},
		Desc:  "git config and its per-directory includeIf overlays",
	},
	"ghostty": {
		Paths: []string{".config/ghostty"},
		Desc:  "Ghostty terminal config",
	},
	"karabiner": {
		Paths: []string{".config/karabiner"},
		Desc:  "Karabiner-Elements key remapping",
	},
	"aerospace": {
		Paths: []string{".aerospace.toml"},
		Desc:  "AeroSpace tiling window manager",
	},
	"mise": {
		Paths: []string{".config/mise"},
		Desc:  "mise runtime versions",
	},
	"aws": {
		Paths: []string{".aws/config"},
		Desc:  "AWS CLI profiles (not credentials)",
	},
	"saml2aws": {
		Paths: []string{".saml2aws"},
		Desc:  "saml2aws IdP settings (no password stored)",
	},
	"ssh-config": {
		Paths: []string{".ssh/config"},
		Desc:  "SSH host aliases, without any keys",
	},

	// --- secret-bearing ---
	"ssh-keys": {
		Paths:  []string{".ssh"},
		Secret: true,
		Desc:   "the whole ~/.ssh, private keys included",
	},
	"gnupg": {
		Paths:  []string{".gnupg"},
		Secret: true,
		Desc:   "GnuPG keyring, private keys included",
	},
	"aws-credentials": {
		Paths:  []string{".aws/credentials", ".aws/sso"},
		Secret: true,
		Desc:   "AWS static credentials and cached SSO tokens",
	},
	"gh": {
		Paths:  []string{".config/gh"},
		Secret: true,
		Desc:   "GitHub CLI config — hosts.yml can hold an OAuth token",
	},
	"claude": {
		Paths:  []string{".claude.json", ".claude"},
		Secret: true,
		Desc:   "Claude Code config, MCP server settings and history",
	},
}
