package cli

import (
	"strings"
	"testing"
)

func kinds(spans []span) string {
	var b strings.Builder
	for _, s := range spans {
		for range []rune(s.text) {
			b.WriteByte(s.kind)
		}
	}
	return b.String()
}

// Tokenizing must never lose or reorder a character: the pane shows the
// file, coloured, not a paraphrase of it.
func TestHighlightPreservesText(t *testing.T) {
	cases := map[string][]string{
		"ini":   {"[user]", "  name = James # who", `  email = "a@b"`, "; comment", "", "weird line"},
		"toml":  {"[[bin]]", "after-startup-command = ['exec-and-forget open -a Ghostty']", "gaps.inner.horizontal = 8"},
		"json":  {`{"a": [1, -2.5e3, true, null, "s\"q"], "k": {}}`, "  \"key\": \"value\","},
		"yaml":  {"editor: nvim", "  - name: x # c", "hosts:", "\"quoted key\": 1", "plain scalar"},
		"shell": {"export PATH=\"$HOME/.local/bin:$PATH\" # path", "alias ll='ls -l'", "if [ -f ~/.bashrc ]; then", "eval \"$(mise activate bash)\"", "FOO=${BAR:-x}"},
		"ssh":   {"Host github.com", "  IdentityAgent ~/.bitwarden-ssh-agent.sock # agent", "# top comment"},
		"kv":    {"font-family = JetBrains Mono", "theme = catppuccin-mocha # dark", "keybind = ctrl+shift+t=new_tab"},
	}
	for lang, lines := range cases {
		for _, line := range lines {
			var b strings.Builder
			for _, s := range highlightLine(lang, line) {
				b.WriteString(s.text)
			}
			if b.String() != line {
				t.Errorf("%s: %q became %q", lang, line, b.String())
			}
		}
	}
}

func TestHighlightKinds(t *testing.T) {
	check := func(lang, line, want string) {
		t.Helper()
		if got := kinds(highlightLine(lang, line)); got != want {
			t.Errorf("%s %q\n got %q\nwant %q", lang, line, got, want)
		}
	}
	check("ini", "[user]", "ssssss")
	check("ini", "name = J ; c", "kkkk     ccc")
	check("ini", `email = "x@y" # c`, "kkkkk   qqqqq ccc")
	check("toml", "gaps = 8", "kkkk   n")
	check("toml", "on = true", "kk   wwww")
	check("json", `{"k": "v", "n": 1, "b": false}`, " kkk  qqq  kkk  n  kkk  wwwww ")
	check("yaml", "editor: nvim # c", "kkkkkk       ccc")
	check("yaml", "  - name: x", "    kkkk   ")
	check("shell", "export PATH=\"$HOME/bin:$PATH\"", "wwwwww kkkk qqqqqqqqqqqqqqqqq")
	check("shell", "alias ll='ls -l' # c", "wwwww kk qqqqqqq ccc")
	check("shell", "echo $HOME ${X}", "     vvvvv vvvv")
	check("shell", "if [ -f x ]; then", "ww               ")
	check("shell", `PS1='$ '`, "kkk qqqq") // a $ inside quotes is not a variable
	check("ssh", "Host github.com", "ssss           ")
	check("ssh", "  IdentityAgent ~/x", "  kkkkkkkkkkkkk    ")
	check("kv", "theme = dark # c", "kkkkk        ccc")
	check("kv", "a comment-free line without equals", "                                  ")
}

func TestLangFor(t *testing.T) {
	for rel, want := range map[string]string{
		".gitconfig": "ini", ".gitconfig-personal": "ini", ".aws/config": "ini", ".saml2aws": "ini",
		".zshrc": "shell", ".bash_aliases": "shell", ".zprofile": "shell", "x/setup.sh": "shell",
		".config/karabiner/karabiner.json": "json", ".aerospace.toml": "toml", ".config/mise/config.toml": "toml",
		".config/gh/hosts.yml": "yaml", ".ssh/config": "ssh", ".config/ghostty/config": "kv",
		"README": "", ".ssh/id_ed25519": "",
	} {
		if got := langFor(rel); got != want {
			t.Errorf("langFor(%q) = %q, want %q", rel, got, want)
		}
	}
}

func TestHighlightedLinesStillTruncate(t *testing.T) {
	line := PreviewLine{Kind: ' ', Text: strings.Repeat("key = value ", 10), Spans: highlightLine("kv", strings.Repeat("key = value ", 10))}
	if got := visibleWidth(renderPreviewLine(line, 30)); got > 30 {
		t.Errorf("highlighted line rendered %d wide, want ≤ 30", got)
	}
	// Diff lines never get syntax colour.
	for _, l := range previewDiff([]byte("a = 1\n"), []byte("a = 2\n")) {
		if len(l.Spans) > 0 {
			t.Error("diff line carries syntax spans")
		}
	}
}
