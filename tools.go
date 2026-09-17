package main

// Tool is one installable thing. Methods maps an install method to the shell
// command that performs it; an empty command means the method can't be
// automated (manual download, App Store) and jat just points at Docs.
type Tool struct {
	Methods map[string]string
	Docs    string
}

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
		Methods: map[string]string{"cask": "brew install --cask visual-studio-code", "manual": ""},
		Docs:    "https://code.visualstudio.com/download",
	},
	"docker": {
		Methods: map[string]string{"manual": ""},
		Docs:    "https://www.docker.com/products/docker-desktop/",
	},
	"1password": {
		Methods: map[string]string{"manual": ""},
		Docs:    "https://1password.com/downloads/mac/",
	},
	"brave": {
		Methods: map[string]string{"manual": ""},
		Docs:    "https://brave.com/download/",
	},
	"ghostty": {
		Methods: map[string]string{"cask": "brew install --cask ghostty"},
		Docs:    "https://ghostty.org",
	},
	"obsidian": {
		Methods: map[string]string{"manual": ""},
		Docs:    "https://obsidian.md/download",
	},
	"magnet": {
		Methods: map[string]string{"appstore": ""},
	},
	"mos": {
		Methods: map[string]string{"cask": "brew install --cask mos", "manual": ""},
		Docs:    "https://github.com/Caldis/Mos",
	},
	"balenaetcher": {
		Methods: map[string]string{"manual": ""},
		Docs:    "https://etcher.balena.io",
	},
	"logi-options-plus": {
		Methods: map[string]string{"manual": ""},
		Docs:    "https://www.logitech.com/software/logi-options-plus.html",
	},
	"claude": {
		Methods: map[string]string{"manual": ""},
		Docs:    "https://claude.ai/download",
	},

	// Package managers & bootstrapping
	"homebrew": {
		Methods: map[string]string{"curlscript": `/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"`},
		Docs:    "https://brew.sh",
	},
	"mise": {
		Methods: map[string]string{"brew": "brew install mise", "curlscript": "curl https://mise.run | sh"},
		Docs:    "https://mise.jdx.dev",
	},
	"nvm": {
		Methods: map[string]string{"curlscript": "curl -o- https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.3/install.sh | bash"},
		Docs:    "https://github.com/nvm-sh/nvm",
	},

	// CLI tools
	"awscli": {
		Methods: map[string]string{"brew": "brew install awscli"},
		Docs:    "https://aws.amazon.com/cli/",
	},
	"gh": {
		Methods: map[string]string{"brew": "brew install gh"},
		Docs:    "https://cli.github.com",
	},
	"op": {
		Methods: map[string]string{"cask": "brew install --cask 1password-cli", "apt": "sudo apt-get install -y 1password-cli"},
		Docs:    "https://developer.1password.com/docs/cli",
	},
	"uv": {
		Methods: map[string]string{"brew": "brew install uv", "curlscript": "curl -LsSf https://astral.sh/uv/install.sh | sh"},
		Docs:    "https://docs.astral.sh/uv/",
	},
	"yq": {
		Methods: map[string]string{"brew": "brew install yq"},
		Docs:    "https://github.com/mikefarah/yq",
	},
	"nmap": {
		Methods: map[string]string{"brew": "brew install nmap", "apt": "sudo apt-get install -y nmap"},
		Docs:    "https://nmap.org",
	},
	"tree": {
		Methods: map[string]string{"brew": "brew install tree", "apt": "sudo apt-get install -y tree"},
	},
	"gnupg": {
		Methods: map[string]string{"brew": "brew install gnupg", "apt": "sudo apt-get install -y gnupg"},
		Docs:    "https://gnupg.org",
	},
	"act": {
		Methods: map[string]string{"brew": "brew install act"},
		Docs:    "https://github.com/nektos/act",
	},

	// mise-managed runtimes
	"node": {
		Methods: map[string]string{"mise": "mise use -g node@lts", "brew": "brew install node"},
		Docs:    "https://nodejs.org",
	},
	"pnpm": {
		Methods: map[string]string{"mise": "mise use -g pnpm@latest", "npm": "npm install -g pnpm"},
		Docs:    "https://pnpm.io",
	},
	"ruby": {
		Methods: map[string]string{"mise": "mise use -g ruby@3.4"},
		Docs:    "https://www.ruby-lang.org",
	},

	// Other
	"pulumi": {
		Methods: map[string]string{"curlscript": "curl -fsSL https://get.pulumi.com | sh"},
		Docs:    "https://www.pulumi.com/docs/install/",
	},
	"backlog.md": {
		Methods: map[string]string{"npm": "npm install -g backlog.md"},
		Docs:    "https://www.npmjs.com/package/backlog.md",
	},
	"oh-my-bash": {
		Methods: map[string]string{"curlscript": `bash -c "$(curl -fsSL https://raw.githubusercontent.com/ohmybash/oh-my-bash/master/tools/install.sh)"`},
		Docs:    "https://github.com/ohmybash/oh-my-bash",
	},
}
