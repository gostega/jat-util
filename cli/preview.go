package cli

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// What the picker's preview pane shows for a row. Built by send and receive,
// rendered by the picker, which knows nothing about bundles.
//
// The one rule here that is not a nicety: an item marked Secret never has its
// contents read into a Preview. Not its diff, not its first line. Previewing
// ~/.ssh/id_ed25519 would put a private key on screen, in scrollback, and in
// any screen share. previewSecret is the only builder such an item reaches.
type Preview struct {
	Title string
	Lines []PreviewLine
}

type PreviewLine struct {
	Kind byte // ' ' plain · '+' added · '-' removed · '@' hunk · '.' dim · '!' warning · 'h' heading
	Text string
	// Spans is optional syntax colour for a plain line (highlight.go). Diff
	// lines never carry it: red/green already spends the colour channel.
	Spans []span
}

const (
	previewMaxBytes = 256 << 10 // above this, say the size instead of showing the file
	previewMaxLines = 400       // per file; the pane scrolls, but not forever
	diffMaxLines    = 2000      // above this the LCS table is too big; fall back to a summary
)

func pl(kind byte, text string) PreviewLine { return PreviewLine{Kind: kind, Text: text} }

func dim(text string) PreviewLine  { return pl('.', text) }
func head(text string) PreviewLine { return pl('h', text) }

// previewSecret is the whole preview for a Secret item: names and per-file
// state so a person can see what they would be adding (James, 2026-09-22),
// never contents. With hideNames set, counts and total only.
func previewSecret(it *bundleItem, hideNames bool) Preview {
	p := Preview{Title: it.Name, Lines: []PreviewLine{pl('!', "hidden — this item holds credentials"), dim("")}}
	if hideNames {
		p.Lines = append(p.Lines,
			dim(fmt.Sprintf("%d file(s), %s. Names and contents are not shown.", len(it.Files), humanSize(it.size()))),
			dim("Use `jat migrate inspect --files` to list paths."))
		return p
	}
	for _, f := range it.Files {
		state := ""
		if f.State != "" {
			state = "  " + stateWord(f.State)
		}
		p.Lines = append(p.Lines, pl(' ', fmt.Sprintf("%-9s %s%s", humanSize(f.Size), f.Rel, state)))
	}
	p.Lines = append(p.Lines, dim(""), dim("contents and diffs of credentials are never shown"))
	return p
}

func stateWord(s string) string {
	switch s {
	case stateDiffers:
		return "would be overwritten"
	case stateAbsent:
		return "new"
	case stateIdentical:
		return "identical"
	}
	return s
}

// previewContents shows an item as it is: one file's head, or a tree for
// several. Used where there is no change to show but the person still
// wants to see what the item holds.
func previewContents(bundlePath string, it *bundleItem, subtitle string) Preview {
	if len(it.Files) != 1 {
		return previewTree(it, fmt.Sprintf("%d files, %s · %s", len(it.Files), humanSize(it.size()), subtitle))
	}
	f := it.Files[0]
	bodies, err := bundleBodies(bundlePath, []string{f.Rel})
	p := Preview{Title: fmt.Sprintf("%s  · %s · %s", it.Name, f.Rel, subtitle)}
	if err != nil {
		p.Lines = []PreviewLine{pl('!', "could not read the bundle: "+err.Error())}
		return p
	}
	body := bodies[f.Rel]
	p.Lines = previewFile(f.Rel, body.data, body.size, body.truncated)
	return p
}

// previewTree draws a directory-shaped item as its file tree with sizes, not
// a concatenation. Used when nothing in the item differs.
func previewTree(it *bundleItem, subtitle string) Preview {
	p := Preview{Title: it.Name + "  · " + subtitle}
	rels := make([]string, 0, len(it.Files))
	sizes := map[string]int64{}
	for _, f := range it.Files {
		rels = append(rels, f.Rel)
		sizes[f.Rel] = f.Size
	}
	sort.Strings(rels)
	// Common root, so .config/karabiner/… reads as one tree rather than
	// repeating the prefix on every line.
	root := commonDir(rels)
	if root != "" {
		p.Lines = append(p.Lines, pl(' ', root+"/"))
	}
	lastDir := ""
	for _, rel := range rels {
		sub := strings.TrimPrefix(strings.TrimPrefix(rel, root), "/")
		dir, base := path.Split(sub)
		if dir != lastDir && dir != "" {
			depth := strings.Count(strings.TrimSuffix(dir, "/"), "/")
			p.Lines = append(p.Lines, pl(' ', strings.Repeat("  ", depth+1)+path.Base(strings.TrimSuffix(dir, "/"))+"/"))
			lastDir = dir
		}
		depth := strings.Count(dir, "/")
		if root != "" {
			depth++
		}
		p.Lines = append(p.Lines, pl(' ', fmt.Sprintf("%s%-*s %8s", strings.Repeat("  ", depth), max(1, 40-2*depth), base, humanSize(sizes[rel]))))
	}
	return p
}

func commonDir(rels []string) string {
	if len(rels) == 0 {
		return ""
	}
	root := path.Dir(rels[0])
	for _, r := range rels[1:] {
		for root != "." && root != "/" && !strings.HasPrefix(r, root+"/") {
			root = path.Dir(root)
		}
	}
	if root == "." || root == "/" {
		return ""
	}
	return root
}

// previewFile shows one file's first lines, or says why it will not.
func previewFile(rel string, data []byte, size int64, truncated bool) []PreviewLine {
	if isBinary(data) {
		return []PreviewLine{dim(fmt.Sprintf("binary file, %s — not shown", humanSize(size)))}
	}
	if truncated {
		return []PreviewLine{dim(fmt.Sprintf("large text file, %s — not shown", humanSize(size)))}
	}
	lines := splitLines(data)
	lang := langFor(rel)
	out := make([]PreviewLine, 0, min(len(lines), previewMaxLines)+1)
	for i, l := range lines {
		if i == previewMaxLines {
			out = append(out, dim(fmt.Sprintf("… %d more lines", len(lines)-i)))
			break
		}
		out = append(out, PreviewLine{Kind: ' ', Text: l, Spans: highlightLine(lang, l)})
	}
	return out
}

func isBinary(data []byte) bool {
	return bytes.IndexByte(data[:min(len(data), 8<<10)], 0) >= 0
}

func splitLines(b []byte) []string {
	s := strings.TrimSuffix(string(b), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// previewDiff is a unified diff of the copy on disk (old) against the one in
// the bundle (new): red is what this machine loses, green is what arrives.
// Computed here, not by an external diff.
func previewDiff(old, new []byte) []PreviewLine {
	if isBinary(old) || isBinary(new) {
		return []PreviewLine{dim(fmt.Sprintf("binary file changes, %s → %s", humanSize(int64(len(old))), humanSize(int64(len(new)))))}
	}
	a, b := splitLines(old), splitLines(new)
	if len(a) > diffMaxLines || len(b) > diffMaxLines {
		return []PreviewLine{dim(fmt.Sprintf("too large to diff here: %d → %d lines", len(a), len(b)))}
	}
	ops := lineDiff(a, b)
	var out []PreviewLine
	// Hunks with 3 lines of context, the way diff -u draws them.
	const ctx = 3
	i := 0
	for i < len(ops) {
		if ops[i].kind == ' ' {
			i++
			continue
		}
		start := max(0, i-ctx)
		end := i
		for end < len(ops) {
			if ops[end].kind != ' ' {
				end++
				continue
			}
			// Run of context: does another change follow within 2*ctx?
			j := end
			for j < len(ops) && ops[j].kind == ' ' && j-end <= 2*ctx {
				j++
			}
			if j < len(ops) && ops[j].kind != ' ' {
				end = j
				continue
			}
			break
		}
		end = min(len(ops), end+ctx)
		oa, ob, na, nb := 0, 0, 0, 0
		for _, op := range ops[:start] {
			if op.kind != '+' {
				oa++
			}
			if op.kind != '-' {
				ob++
			}
		}
		for _, op := range ops[start:end] {
			if op.kind != '+' {
				na++
			}
			if op.kind != '-' {
				nb++
			}
		}
		out = append(out, pl('@', fmt.Sprintf("@@ -%d,%d +%d,%d @@", oa+1, na, ob+1, nb)))
		for _, op := range ops[start:end] {
			out = append(out, pl(op.kind, string(op.kind)+" "+op.text))
		}
		i = end
	}
	if len(out) == 0 {
		return []PreviewLine{dim("no line changes (whitespace or line endings only)")}
	}
	return out
}

type diffOp struct {
	kind byte
	text string
}

// lineDiff is a plain LCS over lines: files here are dotfiles, so an O(n·m)
// table is fine and gives the same answer as diff for the sizes involved.
func lineDiff(a, b []string) []diffOp {
	n, m := len(a), len(b)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var ops []diffOp
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{' ', a[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			ops = append(ops, diffOp{'-', a[i]})
			i++
		default:
			ops = append(ops, diffOp{'+', b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{'-', a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{'+', b[j]})
	}
	return ops
}

// fileBody is a file's bytes for previewing, capped so a stray large file
// cannot be pulled into memory just to be refused.
type fileBody struct {
	data      []byte
	size      int64
	truncated bool
}

func readCapped(r io.Reader, size int64) fileBody {
	if size > previewMaxBytes {
		return fileBody{size: size, truncated: true}
	}
	data, _ := io.ReadAll(io.LimitReader(r, previewMaxBytes+1))
	return fileBody{data: data, size: size, truncated: int64(len(data)) > previewMaxBytes}
}

func readDiskCapped(p string) (fileBody, error) {
	f, err := os.Open(p)
	if err != nil {
		return fileBody{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return fileBody{}, err
	}
	return readCapped(f, info.Size()), nil
}

// bundleBodies pulls the named files' bytes out of a bundle in one pass.
// Called lazily, the first time a row is previewed; the picker caches it.
func bundleBodies(bundlePath string, rels []string) (map[string]fileBody, error) {
	want := map[string]bool{}
	for _, r := range rels {
		want[r] = true
	}
	out := map[string]fileBody{}
	_, err := walkBundle(bundlePath, func(rel string, hdr *tar.Header, r io.Reader) error {
		if want[rel] {
			out[rel] = readCapped(r, hdr.Size)
		}
		return nil
	})
	return out, err
}

// receivePreview builds the pane content for one receive row. Secret and
// identical items never reach the bundle; a differs item shows what would
// change here, an all-new item shows what arrives.
func receivePreview(bundlePath, home string, it *bundleItem, hideSecretNames bool) Preview {
	switch {
	case it.Secret:
		return previewSecret(it, hideSecretNames)
	case it.State() == stateIdentical:
		// Nothing would change, but the contents are still worth a look
		// (James, 2026-09-24): the bundle copy is byte-for-byte what is here.
		return previewContents(bundlePath, it, "identical · already on this machine")
	}
	changed, added, _ := it.counts()
	if changed == 0 && len(it.Files) > 1 {
		return previewTree(it, fmt.Sprintf("%d files, %s · all new here", added, humanSize(it.size())))
	}

	// Differing files first, then new ones; identical ones have nothing to say.
	files := slices.Clone(it.Files)
	slices.SortStableFunc(files, func(a, b *bundleFile) int {
		rank := map[string]int{stateDiffers: 0, stateAbsent: 1, stateIdentical: 2}
		return rank[a.State] - rank[b.State]
	})
	var rels []string
	for _, f := range files {
		if f.State != stateIdentical {
			rels = append(rels, f.Rel)
		}
	}
	bodies, err := bundleBodies(bundlePath, rels)
	p := Preview{Title: it.Name + "  · what would change here"}
	if err != nil {
		p.Lines = append(p.Lines, pl('!', "could not read the bundle: "+err.Error()))
		return p
	}
	for _, f := range files {
		if f.State == stateIdentical {
			continue
		}
		if len(p.Lines) > 0 {
			p.Lines = append(p.Lines, dim(""))
		}
		body := bodies[f.Rel]
		switch f.State {
		case stateDiffers:
			p.Lines = append(p.Lines, head(f.Rel+"  differs"))
			disk, derr := readDiskCapped(filepath.Join(home, f.Rel))
			switch {
			case derr != nil:
				p.Lines = append(p.Lines, dim("could not read the copy on disk: "+derr.Error()))
			case body.truncated || disk.truncated:
				p.Lines = append(p.Lines, dim(fmt.Sprintf("large file, %s → %s — not diffed", humanSize(disk.size), humanSize(body.size))))
			default:
				p.Lines = append(p.Lines, previewDiff(disk.data, body.data)...)
			}
		default:
			p.Lines = append(p.Lines, head(f.Rel+"  new"))
			p.Lines = append(p.Lines, dim(fmt.Sprintf("new file, %d lines, %s", len(splitLines(body.data)), humanSize(body.size))))
			p.Lines = append(p.Lines, previewFile(f.Rel, body.data, body.size, body.truncated)...)
		}
	}
	return p
}

// sendPreview shows what is on this machine for one send row: the file as
// it is, a tree for a directory, and nothing for a secret.
func sendPreview(home, name string) Preview {
	item := configs[name]
	rels := expandFiles(home, item.Paths)
	bi := &bundleItem{Name: name, Desc: item.Desc, Secret: item.Secret}
	for _, rel := range rels {
		var size int64
		if info, err := os.Stat(filepath.Join(home, rel)); err == nil {
			size = info.Size()
		}
		bi.Files = append(bi.Files, &bundleFile{Rel: rel, Size: size})
	}
	if item.Secret {
		return previewSecret(bi, false)
	}
	if len(rels) == 0 {
		return Preview{Title: name, Lines: []PreviewLine{dim("nothing present on this machine")}}
	}
	if len(rels) > 1 {
		return previewTree(bi, fmt.Sprintf("%d files, %s", len(rels), humanSize(bi.size())))
	}
	body, err := readDiskCapped(filepath.Join(home, rels[0]))
	if err != nil {
		return Preview{Title: name, Lines: []PreviewLine{dim(err.Error())}}
	}
	p := Preview{Title: fmt.Sprintf("%s  · %s · %d lines, %s", name, rels[0], len(splitLines(body.data)), humanSize(body.size))}
	p.Lines = previewFile(rels[0], body.data, body.size, body.truncated)
	return p
}

// expandFiles is expandPaths followed by a walk, so a directory pattern
// yields the regular files under it, relative to home.
func expandFiles(home string, patterns []string) []string {
	var out []string
	for _, rel := range expandPaths(home, patterns) {
		filepath.WalkDir(filepath.Join(home, rel), func(p string, d os.DirEntry, err error) error {
			if err == nil && d.Type().IsRegular() && !isBackupName(p) {
				out = append(out, relOf(home, p))
			}
			return nil
		})
	}
	sort.Strings(out)
	return out
}
