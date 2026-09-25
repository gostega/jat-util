package test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

// A terminal session against the binary. Keys are written raw; frames are
// whatever the program has drawn since the last key. A goroutine pumps the
// pty into a channel: read deadlines on a blocking pty master are a no-op,
// so a plain Read would wait forever on a quiet program.
type tui struct {
	t   *testing.T
	f   *os.File
	cmd *exec.Cmd
	all bytes.Buffer
	in  chan []byte
}

func startTUI(t *testing.T, home string, cols, rows int, args ...string) *tui {
	t.Helper()
	cmd := exec.Command(jat, args...)
	cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH"), "TERM=xterm-256color"}
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	s := &tui{t: t, f: f, cmd: cmd, in: make(chan []byte, 64)}
	go func() {
		buf := make([]byte, 65536)
		for {
			n, err := f.Read(buf)
			if n > 0 {
				s.in <- append([]byte(nil), buf[:n]...)
			}
			if err != nil {
				close(s.in)
				return
			}
		}
	}()
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait(); f.Close() })
	s.settle(1500 * time.Millisecond)
	return s
}

// settle collects output until the program has been quiet for a while,
// answering the terminal queries Bubble Tea sends on startup so it does not
// wait on them. d is the longest it waits for the first byte.
func (s *tui) settle(d time.Duration) string {
	var got bytes.Buffer
	wait := d
	for {
		select {
		case chunk, ok := <-s.in:
			if !ok {
				return got.String()
			}
			got.Write(chunk)
			s.all.Write(chunk)
			if bytes.Contains(chunk, []byte("\x1b]11;?")) {
				s.f.Write([]byte("\x1b]11;rgb:0000/0000/0000\x1b\\"))
			}
			if bytes.Contains(chunk, []byte("\x1b[6n")) {
				s.f.Write([]byte("\x1b[1;1R"))
			}
			wait = 400 * time.Millisecond
		case <-time.After(wait):
			return got.String()
		}
	}
}

func (s *tui) key(k string) string {
	s.f.Write([]byte(k))
	return s.settle(800 * time.Millisecond)
}

var ansi = regexp.MustCompile(`\x1b\][^\x07\x1b]*(\x07|\x1b\\)|\x1b\[[0-9;?]*[a-zA-Z]|\x1b[=>]`)

// plain strips escape codes and blank lines from a frame.
func plain(frame string) string {
	var lines []string
	for _, l := range strings.Split(ansi.ReplaceAllString(frame, ""), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, strings.TrimRight(l, " "))
		}
	}
	return strings.Join(lines, "\n")
}

const (
	up, down, left, right = "\x1b[A", "\x1b[B", "\x1b[D", "\x1b[C"
	enter, esc            = "\r", "\x1b"
)

func receiveScenario(t *testing.T) (home, bundle string) {
	old := oldHome(t)
	bundle = filepath.Join(t.TempDir(), "b.tar.gz")
	if out, err := run(t, old, nil, "migrate", "send", "--transport", "file", "--all", "--out", bundle); err != nil {
		t.Fatal(out)
	}
	home = t.TempDir()
	writeFiles(t, home, map[string]string{".gitconfig": "[user]\n  name = James\n  email = local@example.test\n", ".zshrc": "export A=1\n"})
	return home, bundle
}

// The menus are full screen, and the alternate screen is entered once for
// the whole run — toggling it per menu is what flickered (James, 2026-09-23).
func TestMenusTakeTheAlternateScreenOnce(t *testing.T) {
	home, bundle := receiveScenario(t)
	os.MkdirAll(filepath.Join(home, "Downloads"), 0o755)
	os.Rename(bundle, filepath.Join(home, "Downloads", "jat-migrate-7f3a.tar.gz")) // where the bundle chooser looks
	s := startTUI(t, home, 120, 30, "migrate")
	s.key(down) // Receive
	s.key(enter)
	s.key(enter) // File
	s.key(enter) // the one bundle
	frame := s.settle(500 * time.Millisecond)
	if got := bytes.Count(s.all.Bytes(), []byte("\x1b[?1049h")); got != 1 {
		t.Errorf("alternate screen entered %d times across four menus, want 1", got)
	}
	if !strings.Contains(plain(s.all.String()+frame), "What should land on this machine?") {
		t.Errorf("did not reach the picker:\n%s", plain(s.all.String()))
	}
	s.key(esc)
	if got := bytes.Count(s.all.Bytes(), []byte("\x1b[?1049l")); got != 1 {
		t.Errorf("alternate screen left %d times, want 1", got)
	}
	if !strings.Contains(plain(s.all.String()), "Cancelled") {
		t.Error("the cancel message did not survive leaving the alternate screen")
	}
}

func TestPreviewPaneWideAndNarrow(t *testing.T) {
	home, bundle := receiveScenario(t)

	// Wide: side pane with a real diff, list column still intact.
	s := startTUI(t, home, 120, 30, "migrate", "receive", bundle)
	s.key(down) // git
	frame := plain(s.key(right))
	for _, want := range []string{"what would change here", ".gitconfig  differs", "-   email = local@example.test", "+   email = old@example.test", "│"} {
		if !strings.Contains(frame, want) {
			t.Errorf("wide pane missing %q:\n%s", want, frame)
		}
	}
	for _, line := range strings.Split(frame, "\n") {
		if n := len([]rune(line)); n > 120 {
			t.Errorf("line wider than the terminal (%d): %q", n, line)
		}
	}
	if !strings.Contains(plain(s.key(left)), "→ preview") {
		t.Error("← did not close the pane")
	}

	// Narrow: full screen, list hidden, q returns.
	s = startTUI(t, home, 80, 24, "migrate", "receive", bundle)
	s.key(down)
	frame = plain(s.key(right))
	if !strings.Contains(frame, "what would change here") || strings.Contains(frame, "ghostty") {
		t.Errorf("80 columns should preview full screen with the list hidden:\n%s", frame)
	}
	if !strings.Contains(frame, "q or esc back to the list") {
		t.Error("full-screen help line missing")
	}
	frame = plain(s.key("q"))
	if !strings.Contains(frame, "ghostty") {
		t.Errorf("q did not return to the list:\n%s", frame)
	}
}

func TestSecretItemsNeverRenderContentsInTheTerminal(t *testing.T) {
	const secret = "BEGIN OPENSSH PRIVATE KEY"
	old := oldHome(t)
	writeFiles(t, old, map[string]string{".ssh/id_test": "-----" + secret + "-----\nAAAA\n"})
	bundle := filepath.Join(t.TempDir(), "b.tar.gz")
	if out, err := run(t, old, nil, "migrate", "send", "--transport", "file", "--all", "--include-secrets", "--out", bundle); err != nil {
		t.Fatal(out)
	}
	home := t.TempDir()
	s := startTUI(t, home, 120, 30, "migrate", "receive", bundle)
	// Walk every row with the pane open.
	s.key(right)
	for range 6 {
		s.key(down)
	}
	all := s.all.String()
	if strings.Contains(all, secret) || strings.Contains(all, "AAAA") {
		t.Fatal("secret file contents reached the terminal")
	}
	if !strings.Contains(plain(all), "hidden — this item holds credentials") {
		t.Error("secret item was never shown as hidden; did the walk reach it?")
	}
}
