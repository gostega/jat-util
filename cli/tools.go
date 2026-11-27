package cli

// Tool is one installable thing. Methods maps an install method to the shell
// command that performs it; an empty command means the method can't be
// automated (manual download, App Store) and jat just points at Docs.
//
// Group is the tab it appears under in `jat install`. Needs names other
// tools that must be installed first, on top of what the chosen method
// implies (brew/cask → homebrew, mise → mise, npm → node; see toolDeps).
// Bin is the executable whose presence on PATH means "installed" (default:
// the tool's own name); App is a macOS application bundle name checked in
// /Applications instead. Neither set means jat cannot tell. Check, when set,
// is a command run once Bin is found: the tool counts as installed only if it
// exits 0 — for tools where presence is not enough (macOS ships bash 3.2).
type Tool struct {
	Group   string
	Methods map[string]string
	Docs    string
	Needs   []string
	Bin     string
	App     string
	Check   []string

	// Prefer, set only from the user's config, is the method that wins over
	// the profile order; Installer names the user installer behind it.
	Prefer    string
	Installer string
}

// Group names, in the order the tabs appear.
var toolGroups = []string{"Apps", "Package managers", "CLI tools", "Runtimes", "Other"}

// Install method preference per profile: the first method a tool actually has
// wins, so a tool only needs the methods that genuinely exist for it.
var profileOrder = map[string][]string{
	"macos":  {"mise", "brew", "cask", "npm", "curlscript", "appstore", "manual"},
	"debian": {"mise", "apt", "npm", "curlscript", "manual"},
}

var allMethods = []string{"mise", "brew", "cask", "apt", "npm", "curlscript", "appstore", "manual"}

// Seeded from accepted-tools.md.
var tools = map[string]Tool{
	// Apps
	"vscode": {
		Group:   "Apps",
		App:     "Visual Studio Code",
		Methods: map[string]string{"cask": "brew install --cask visual-studio-code", "manual": ""},
		Docs:    "https://code.visualstudio.com/download",
	},
	"docker": {
		Group:   "Apps",
		App:     "Docker",
		Methods: map[string]string{"manual": ""},
		Docs:    "https://www.docker.com/products/docker-desktop/",
	},
	"orbstack": {
		Group:   "Apps",
		App:     "OrbStack",
		Methods: map[string]string{"cask": "brew install --cask orbstack", "manual": ""},
		Docs:    "https://orbstack.dev/download",
	},
	"protonvpn": {
		Group:   "Apps",
		App:     "ProtonVPN",
		Methods: map[string]string{"cask": "brew install --cask protonvpn", "manual": ""},
		Docs:    "https://protonvpn.com/download-macos",
	},
	"iterm2": {
		Group:   "Apps",
		App:     "iTerm", // the cask installs iTerm.app, not iTerm2.app
		Methods: map[string]string{"cask": "brew install --cask iterm2", "manual": ""},
		Docs:    "https://iterm2.com/downloads.html",
	},
	"bitwarden": {
		Group:   "Apps",
		App:     "Bitwarden",
		Methods: map[string]string{"cask": "brew install --cask bitwarden", "manual": ""},
		Docs:    "https://bitwarden.com/download/",
	},
	"discord": {
		Group:   "Apps",
		App:     "Discord",
		Methods: map[string]string{"cask": "brew install --cask discord", "manual": ""},
		Docs:    "https://discord.com/download",
	},
	"1password": {
		Group:   "Apps",
		App:     "1Password",
		Methods: map[string]string{"manual": ""},
		Docs:    "https://1password.com/downloads/mac/",
	},
	"brave": {
		Group:   "Apps",
		App:     "Brave Browser",
		Methods: map[string]string{"manual": ""},
		Docs:    "https://brave.com/download/",
	},
	"ghostty": {
		Group:   "Apps",
		App:     "Ghostty",
		Methods: map[string]string{"cask": "brew install --cask ghostty"},
		Docs:    "https://ghostty.org",
	},
	"obsidian": {
		Group:   "Apps",
		App:     "Obsidian",
		Methods: map[string]string{"manual": ""},
		Docs:    "https://obsidian.md/download",
	},
	"magnet": {
		Group:   "Apps",
		App:     "Magnet",
		Methods: map[string]string{"appstore": ""},
	},
	"mos": {
		Group:   "Apps",
		App:     "Mos",
		Methods: map[string]string{"cask": "brew install --cask mos", "manual": ""},
		Docs:    "https://github.com/Caldis/Mos",
	},
	"balenaetcher": {
		Group:   "Apps",
		App:     "balenaEtcher",
		Methods: map[string]string{"manual": ""},
		Docs:    "https://etcher.balena.io",
	},
	"logi-options-plus": {
		Group:   "Apps",
		App:     "logioptionsplus",
		Methods: map[string]string{"manual": ""},
		Docs:    "https://www.logitech.com/software/logi-options-plus.html",
	},
	"claude": {
		Group:   "Apps",
		App:     "Claude",
		Methods: map[string]string{"manual": ""},
		Docs:    "https://claude.ai/download",
	},

	// Package managers & bootstrapping
	"homebrew": {
		Group:   "Package managers",
		Bin:     "brew",
		Methods: map[string]string{"curlscript": `/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"`},
		Docs:    "https://brew.sh",
	},
	"mise": {
		Group:   "Package managers",
		Methods: map[string]string{"brew": "brew install mise", "curlscript": "curl https://mise.run | sh"},
		Docs:    "https://mise.jdx.dev",
	},
	"nvm": {
		Group:   "Package managers",
		Methods: map[string]string{"curlscript": "curl -o- https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.3/install.sh | bash"},
		Docs:    "https://github.com/nvm-sh/nvm",
	},

	// CLI tools
	"awscli": {
		Group:   "CLI tools",
		Bin:     "aws",
		Methods: map[string]string{"brew": "brew install awscli"},
		Docs:    "https://aws.amazon.com/cli/",
	},
	"gh": {
		Group:   "CLI tools",
		Methods: map[string]string{"brew": "brew install gh"},
		Docs:    "https://cli.github.com",
	},
	"op": {
		Group:   "CLI tools",
		Methods: map[string]string{"cask": "brew install --cask 1password-cli", "apt": "sudo apt-get install -y 1password-cli"},
		Docs:    "https://developer.1password.com/docs/cli",
	},
	"uv": {
		Group:   "CLI tools",
		Methods: map[string]string{"brew": "brew install uv", "curlscript": "curl -LsSf https://astral.sh/uv/install.sh | sh"},
		Docs:    "https://docs.astral.sh/uv/",
	},
	"yq": {
		Group:   "CLI tools",
		Methods: map[string]string{"brew": "brew install yq"},
		Docs:    "https://github.com/mikefarah/yq",
	},
	"bash": {
		Group: "CLI tools",
		// /bin/bash on macOS is 3.2 forever (GPLv3); "installed" means the bash
		// you get from PATH is 5 or later, which is what Homebrew's provides.
		Check:   []string{"bash", "-c", "(( BASH_VERSINFO[0] >= 5 ))"},
		Methods: map[string]string{"brew": "brew install bash", "apt": "sudo apt-get install -y bash"},
		Docs:    "https://www.gnu.org/software/bash/",
	},
	"nmap": {
		Group:   "CLI tools",
		Methods: map[string]string{"brew": "brew install nmap", "apt": "sudo apt-get install -y nmap"},
		Docs:    "https://nmap.org",
	},
	"tree": {
		Group:   "CLI tools",
		Methods: map[string]string{"brew": "brew install tree", "apt": "sudo apt-get install -y tree"},
	},
	"gnupg": {
		Group:   "CLI tools",
		Bin:     "gpg",
		Methods: map[string]string{"brew": "brew install gnupg", "apt": "sudo apt-get install -y gnupg"},
		Docs:    "https://gnupg.org",
	},
	"act": {
		Group:   "CLI tools",
		Methods: map[string]string{"brew": "brew install act"},
		Docs:    "https://github.com/nektos/act",
	},

	// mise-managed runtimes
	"node": {
		Group:   "Runtimes",
		Methods: map[string]string{"mise": "mise use -g node@lts", "brew": "brew install node"},
		Docs:    "https://nodejs.org",
	},
	"pnpm": {
		Group:   "Runtimes",
		Methods: map[string]string{"mise": "mise use -g pnpm@latest", "npm": "npm install -g pnpm"},
		Docs:    "https://pnpm.io",
	},
	"ruby": {
		Group:   "Runtimes",
		Methods: map[string]string{"mise": "mise use -g ruby@3.4"},
		Docs:    "https://www.ruby-lang.org",
	},

	// Other
	"pulumi": {
		Group:   "Other",
		Methods: map[string]string{"curlscript": "curl -fsSL https://get.pulumi.com | sh"},
		Docs:    "https://www.pulumi.com/docs/install/",
	},
	"backlog.md": {
		Group:   "Other",
		Bin:     "backlog",
		Methods: map[string]string{"npm": "npm install -g backlog.md"},
		Docs:    "https://www.npmjs.com/package/backlog.md",
	},
	"oh-my-bash": {
		Group: "Other",
		// It is a directory, not an executable: installed means its install
		// dir exists ($OSH, which its installer defaults to ~/.oh-my-bash).
		Bin:   "bash",
		Check: []string{"bash", "-c", `[ -d "${OSH:-$HOME/.oh-my-bash}" ]`},
		// "bash" fills $0 for bash -c; --unattended stops the installer
		// exec-ing a new interactive bash, which would hold jat's terminal.
		Methods: map[string]string{"curlscript": `bash -c "$(curl -fsSL https://raw.githubusercontent.com/ohmybash/oh-my-bash/master/tools/install.sh)" bash --unattended`},
		Docs:    "https://github.com/ohmybash/oh-my-bash",
	},
}
