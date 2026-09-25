package cli

import (
	"path"
	"strings"
	"unicode"
)

// Syntax colour for the preview pane. Deliberately small (James, 2026-09-23:
// "we don't need strict correctness across many languages, just improved
// readability for general cases"): a handful of line-at-a-time tokenizers
// for the formats configs.go actually carries — INI-shaped, TOML, JSON, YAML,
// shell, ssh_config and plain key = value. No dependency, and no attempt at
// multi-line strings or heredocs; a preview pane is not an editor. Chroma is
// the upgrade path if the day comes.
//
// Kinds: 'c' comment · 's' section header · 'k' key · 'q' string ·
// 'n' number · 'w' keyword/boolean · 'v' variable · ' ' plain.
type span struct {
	kind byte
	text string
}

// langFor picks a tokenizer from a path relative to $HOME. Unknown files get
// "" and render plain.
func langFor(rel string) string {
	base := path.Base(rel)
	switch ext := path.Ext(base); ext {
	case ".json":
		return "json"
	case ".toml":
		return "toml"
	case ".yml", ".yaml":
		return "yaml"
	case ".ini", ".conf", ".cfg":
		return "ini"
	case ".sh", ".bash", ".zsh":
		return "shell"
	}
	switch {
	case strings.HasPrefix(base, ".gitconfig"), base == ".saml2aws", rel == ".aws/config", rel == ".aws/credentials":
		return "ini"
	case strings.HasPrefix(base, ".bash"), strings.HasPrefix(base, ".zsh"), base == ".zprofile", base == ".profile":
		return "shell"
	case rel == ".ssh/config":
		return "ssh"
	case strings.HasPrefix(rel, ".config/ghostty/"), strings.HasPrefix(rel, ".config/mise/"):
		return "kv"
	}
	return ""
}

func highlightLine(lang, line string) []span {
	switch lang {
	case "ini", "toml":
		return tokIni(line, lang == "toml")
	case "json":
		return tokJSON(line)
	case "yaml":
		return tokYAML(line)
	case "shell":
		return tokShell(line)
	case "ssh":
		return tokSSH(line)
	case "kv":
		return tokKV(line)
	}
	return nil
}

// splitComment cuts a trailing comment off at the first marker that is not
// inside quotes. It is the one bit of quote-awareness every format needs.
func splitComment(line string, markers string) (code, comment string) {
	var quote byte
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case strings.IndexByte(markers, c) >= 0 && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t'):
			return line[:i], line[i:]
		}
	}
	return line, ""
}

// tokValue colours the right-hand side of an assignment: strings, numbers,
// booleans, shell-style variables; everything else plain.
func tokValue(v string, vars bool) []span {
	var out []span
	flush := func(k byte, s string) {
		if s != "" {
			out = append(out, span{k, s})
		}
	}
	i := 0
	for i < len(v) {
		c := v[i]
		switch {
		case c == '"' || c == '\'':
			j := i + 1
			for j < len(v) && v[j] != c {
				if v[j] == '\\' {
					j++
				}
				j++
			}
			j = min(j+1, len(v))
			flush('q', v[i:j])
			i = j
		case vars && c == '$':
			j := i + 1
			if j < len(v) && v[j] == '{' {
				for j < len(v) && v[j] != '}' {
					j++
				}
				j = min(j+1, len(v))
			} else {
				for j < len(v) && (v[j] == '_' || unicode.IsLetter(rune(v[j])) || unicode.IsDigit(rune(v[j]))) {
					j++
				}
			}
			flush('v', v[i:j])
			i = j
		case unicode.IsDigit(rune(c)) && (i == 0 || !isWord(v[i-1])):
			j := i
			for j < len(v) && (unicode.IsDigit(rune(v[j])) || strings.IndexByte(".-:_xabcdefABCDEF", v[j]) >= 0) {
				j++
			}
			flush('n', v[i:j])
			i = j
		case isWord(c) && (i == 0 || !isWord(v[i-1])):
			j := i
			for j < len(v) && isWord(v[j]) {
				j++
			}
			word := v[i:j]
			switch strings.ToLower(word) {
			case "true", "false", "yes", "no", "on", "off", "null", "none":
				flush('w', word)
			default:
				flush(' ', word)
			}
			i = j
		default:
			j := i + 1
			for j < len(v) && !(v[j] == '"' || v[j] == '\'' || (vars && v[j] == '$') || isWord(v[j])) {
				j++
			}
			flush(' ', v[i:j])
			i = j
		}
	}
	return merge(out)
}

func isWord(c byte) bool {
	return c == '_' || c == '-' || unicode.IsLetter(rune(c)) || unicode.IsDigit(rune(c))
}

// merge joins neighbouring spans of the same kind so truncation and
// rendering have fewer pieces to handle.
func merge(in []span) []span {
	var out []span
	for _, s := range in {
		if n := len(out); n > 0 && out[n-1].kind == s.kind {
			out[n-1].text += s.text
			continue
		}
		out = append(out, s)
	}
	return out
}

func withComment(code []span, comment string) []span {
	if comment != "" {
		code = append(code, span{'c', comment})
	}
	return merge(code)
}

// tokIni covers .gitconfig, .aws/config, .saml2aws and TOML: [sections],
// key = value, ; or # comments.
func tokIni(line string, toml bool) []span {
	code, comment := splitComment(line, "#;")
	t := strings.TrimSpace(code)
	indent := code[:len(code)-len(strings.TrimLeft(code, " \t"))]
	switch {
	case t == "":
		return withComment([]span{{' ', code}}, comment)
	case strings.HasPrefix(t, "["):
		return withComment([]span{{' ', indent}, {'s', t}, {' ', code[len(indent)+len(t):]}}, comment)
	}
	if k, v, ok := strings.Cut(code, "="); ok {
		out := []span{{' ', indent}, {'k', strings.TrimSpace(k)}, {' ', k[len(indent)+len(strings.TrimSpace(k)):] + "="}}
		return withComment(append(out, tokValue(v, false)...), comment)
	}
	return withComment([]span{{' ', code}}, comment)
}

// tokKV is tokIni without sections, for ghostty and mise style files.
func tokKV(line string) []span {
	code, comment := splitComment(line, "#")
	if k, v, ok := strings.Cut(code, "="); ok && strings.TrimSpace(k) != "" && !strings.ContainsAny(strings.TrimSpace(k), " \t") {
		indent := k[:len(k)-len(strings.TrimLeft(k, " \t"))]
		key := strings.TrimSpace(k)
		out := []span{{' ', indent}, {'k', key}, {' ', k[len(indent)+len(key):] + "="}}
		return withComment(append(out, tokValue(v, false)...), comment)
	}
	return withComment([]span{{' ', code}}, comment)
}

// tokJSON: a string followed by ':' is a key, other strings are strings,
// numbers and true/false/null get their colours, punctuation stays plain.
func tokJSON(line string) []span {
	var out []span
	i := 0
	for i < len(line) {
		c := line[i]
		switch {
		case c == '"':
			j := i + 1
			for j < len(line) && line[j] != '"' {
				if line[j] == '\\' {
					j++
				}
				j++
			}
			j = min(j+1, len(line))
			kind := byte('q')
			if rest := strings.TrimLeft(line[j:], " \t"); strings.HasPrefix(rest, ":") {
				kind = 'k'
			}
			out = append(out, span{kind, line[i:j]})
			i = j
		case unicode.IsDigit(rune(c)) || (c == '-' && i+1 < len(line) && unicode.IsDigit(rune(line[i+1]))):
			j := i + 1
			for j < len(line) && strings.IndexByte("0123456789.eE+-", line[j]) >= 0 {
				j++
			}
			out = append(out, span{'n', line[i:j]})
			i = j
		case unicode.IsLetter(rune(c)):
			j := i
			for j < len(line) && unicode.IsLetter(rune(line[j])) {
				j++
			}
			kind := byte(' ')
			if w := line[i:j]; w == "true" || w == "false" || w == "null" {
				kind = 'w'
			}
			out = append(out, span{kind, line[i:j]})
			i = j
		default:
			out = append(out, span{' ', line[i : i+1]})
			i++
		}
	}
	return merge(out)
}

// tokYAML: comments, "key:" at the start (after indent or "- "), values.
func tokYAML(line string) []span {
	code, comment := splitComment(line, "#")
	t := strings.TrimLeft(code, " \t-")
	prefix := code[:len(code)-len(t)]
	if t == "" {
		return withComment([]span{{' ', code}}, comment)
	}
	if strings.HasPrefix(t, "\"") || strings.HasPrefix(t, "'") {
		return withComment(append([]span{{' ', prefix}}, tokValue(t, false)...), comment)
	}
	if k, v, ok := strings.Cut(t, ":"); ok && (v == "" || v[0] == ' ') && !strings.ContainsAny(k, "\"'") {
		out := []span{{' ', prefix}, {'k', k}, {' ', ":"}}
		return withComment(append(out, tokValue(v, false)...), comment)
	}
	return withComment(append([]span{{' ', prefix}}, tokValue(t, false)...), comment)
}

var shellKeywords = map[string]bool{
	"if": true, "then": true, "else": true, "elif": true, "fi": true, "for": true, "in": true, "do": true, "done": true,
	"while": true, "until": true, "case": true, "esac": true, "function": true, "return": true, "local": true,
	"export": true, "alias": true, "source": true, "unset": true, "eval": true, "exec": true, "set": true, "shopt": true,
	"setopt": true, "autoload": true, "bindkey": true, "unalias": true, "readonly": true, "declare": true, "typeset": true,
}

// tokShell: comments, keywords and builtins, strings, $variables, and the
// name in NAME=value or alias x=.
func tokShell(line string) []span {
	code, comment := splitComment(line, "#")
	t := strings.TrimLeft(code, " \t")
	indent := code[:len(code)-len(t)]
	var out []span
	if indent != "" {
		out = append(out, span{' ', indent})
	}
	rest := t
	// Leading keywords: "export FOO=bar", "alias ll='ls -l'", "if [ ... ]".
	for {
		w, after, _ := strings.Cut(rest, " ")
		if !shellKeywords[w] {
			break
		}
		out = append(out, span{'w', w})
		if after == "" {
			return withComment(out, comment)
		}
		out = append(out, span{' ', " "})
		rest = after
	}
	// NAME=value at the start of what is left.
	if k, v, ok := strings.Cut(rest, "="); ok && k != "" && !strings.ContainsAny(k, " \t\"'$()") {
		out = append(out, span{'k', k}, span{' ', "="})
		return withComment(append(out, tokValue(v, true)...), comment)
	}
	return withComment(append(out, tokValue(rest, true)...), comment)
}

// tokSSH: Host/Match lines are sections; otherwise the first word is the
// keyword and the rest is its value.
func tokSSH(line string) []span {
	code, comment := splitComment(line, "#")
	t := strings.TrimLeft(code, " \t")
	indent := code[:len(code)-len(t)]
	if t == "" {
		return withComment([]span{{' ', code}}, comment)
	}
	w, after, _ := strings.Cut(t, " ")
	kind := byte('k')
	if strings.EqualFold(w, "Host") || strings.EqualFold(w, "Match") {
		kind = 's'
	}
	out := []span{{' ', indent}, {kind, w}}
	if after != "" {
		out = append(out, span{' ', " "})
		out = append(out, tokValue(after, false)...)
	}
	return withComment(out, comment)
}
