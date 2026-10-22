package wvr

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/robbowland/weaver/internal/cli"
	"golang.org/x/sys/unix"
)

// framed runs fn with what it writes to stderr, and what the commands it
// starts write there (Worktrunk's, git's), shown inside a frame drawn as
// fzf draws the form's border: the form's popup has no border of tmux's,
// so what follows the form (the thread being made) showed bare, unlike the
// form before it and the cards. fn gets the frame, to ask in it
// (frame.choose); off a terminal it gets none, and stderr is as it was.
func framed(label string, fn func(*frame) error) error {
	rows, cols, ok := ttySize()
	if !ok || !cli.IsTTY(2) {
		return fn(nil)
	}
	kept, err := unix.Dup(2)
	if err != nil {
		return fn(nil)
	}
	r, w, err := os.Pipe()
	if err != nil {
		bestEffort(unix.Close(kept))
		return fn(nil)
	}
	if err := unix.Dup2(int(w.Fd()), 2); err != nil {
		bestEffort(unix.Close(kept))
		bestEffort(r.Close())
		bestEffort(w.Close())
		return fn(nil)
	}
	f := &frame{out: os.NewFile(uintptr(kept), "tty"), label: label, w: cols, h: rows, marks: make(chan struct{}, 1)}
	f.paint()
	done := make(chan struct{})
	go func() {
		f.read(r)
		close(done)
	}()
	defer func() {
		bestEffort(unix.Dup2(kept, 2))
		bestEffort(w.Close())
		// A command it left running keeps the pipe open: what that writes
		// later goes unshown rather than holding this up.
		select {
		case <-done:
		case <-time.After(300 * time.Millisecond):
		}
		f.mu.Lock()
		f.ended = true
		fmt.Fprint(f.out, "\033[?25h")
		bestEffort(f.out.Close())
		f.mu.Unlock()
	}()
	return fn(f)
}

// frame is what framed shows, w x h: the lines written (the last, the one
// being written) inside the border.
type frame struct {
	out   *os.File
	label string
	w, h  int

	mu      sync.Mutex
	lines   []string
	pending string // the start of an escape or a character a read cut off
	ended   bool
	marks   chan struct{}
}

var (
	// reEsc is a terminal escape: CSI, OSC, or one of two characters.
	reEsc = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(?:\x07|\x1b\\)|[@-Z\\-_])`)
	// frameMark, written to stderr, comes back once all before it is in
	// the frame (flush).
	frameMark = "\x1b]weaver-frame\x07"
)

// read takes what comes on r into the frame, shown as it comes.
func (f *frame) read(r io.Reader) {
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			f.mu.Lock()
			f.take(string(buf[:n]))
			if !f.ended {
				f.paint()
			}
			f.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

// take adds text written: a line ends at \n and \r starts it again; of the
// escapes, colours stay; a tab is spaces.
func (f *frame) take(s string) {
	s, f.pending = f.pending+s, ""
	if i := strings.LastIndexByte(s, 0x1b); i >= 0 && len(s)-i < 256 {
		if m := reEsc.FindStringIndex(s[i:]); m == nil || m[0] != 0 {
			s, f.pending = s[:i], s[i:]
		}
	}
	for j := len(s) - 1; j >= 0 && j >= len(s)-utf8.UTFMax; j-- {
		if utf8.RuneStart(s[j]) {
			if !utf8.FullRuneInString(s[j:]) {
				s, f.pending = s[:j], s[j:]+f.pending
			}
			break
		}
	}
	if strings.Contains(s, frameMark) {
		s = strings.ReplaceAll(s, frameMark, "")
		select {
		case f.marks <- struct{}{}:
		default:
		}
	}
	s = reEsc.ReplaceAllStringFunc(s, func(e string) string {
		if strings.HasPrefix(e, "\x1b[") && strings.HasSuffix(e, "m") {
			return e
		}
		return ""
	})
	s = strings.ReplaceAll(s, "\t", "    ")
	if len(f.lines) == 0 {
		f.lines = []string{""}
	}
	parts := strings.Split(s, "\n")
	for i, p := range parts {
		if i > 0 {
			f.lines = append(f.lines, "")
		}
		if i < len(parts)-1 {
			p = strings.TrimSuffix(p, "\r")
		}
		if j := strings.LastIndexByte(p, '\r'); j >= 0 {
			f.lines[len(f.lines)-1], p = "", p[j+1:]
		}
		f.lines[len(f.lines)-1] += p
	}
}

// screen is the frame's rows, each w wide: fzf's sharp border, the label
// from its third column as fzf puts it, round the last of the lines that
// fit (each cut onto the next as it must); and how many body rows the
// lines take, the last on the one being written.
func (f *frame) screen() ([]string, int) {
	in, n := max(f.w-4, 1), max(f.h-2, 1)
	var body []string
	for _, l := range f.lines {
		body = append(body, cutAnsi(l, in)...)
	}
	if len(body) > n {
		body = body[len(body)-n:]
	}
	label := f.label
	if visible(label) > f.w-4 {
		label = ""
	}
	edge := CDim + "│" + COff
	rows := []string{CDim + "┌─" + COff + label + CDim + strings.Repeat("─", max(f.w-3-visible(label), 0)) + "┐" + COff}
	for i := 0; i < n; i++ {
		l := ""
		if i < len(body) {
			l = body[i]
		}
		rows = append(rows, edge+" "+l+COff+strings.Repeat(" ", in-visible(l))+" "+edge)
	}
	return append(rows, CDim+"└"+strings.Repeat("─", max(f.w-2, 0))+"┘"+COff), len(body)
}

// paint draws the frame, a row at a time.
func (f *frame) paint() {
	rows, _ := f.screen()
	var b strings.Builder
	b.WriteString("\033[?25l")
	for i, r := range rows {
		fmt.Fprintf(&b, "\033[%d;1H%s", i+1, r)
	}
	fmt.Fprint(f.out, b.String())
}

// flush waits till all written so far is in the frame.
func (f *frame) flush() {
	fmt.Fprint(os.Stderr, frameMark)
	select {
	case <-f.marks:
	case <-time.After(time.Second):
	}
}

// choose asks cs (choose's buttons) on a line of their own after what the
// frame shows, inside it; with no frame, as choose does.
func (f *frame) choose(t *os.File, cs []choice, def int) int {
	if f == nil {
		return choose(t, "", cs, def)
	}
	f.flush()
	f.mu.Lock()
	if len(f.lines) == 0 || f.lines[len(f.lines)-1] != "" {
		f.take("\n")
	}
	f.paint()
	_, n := f.screen()
	f.mu.Unlock()
	fmt.Fprintf(t, "\033[%d;1H", n+1)
	return chooseIn(t, CDim+"│"+COff+" ", fmt.Sprintf("\033[%dG", f.w)+CDim+"│"+COff, cs, def)
}

// cutAnsi is s in lines of at most n characters, its colours where they
// fall.
func cutAnsi(s string, n int) []string {
	var ls []string
	var b strings.Builder
	k := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			if m := reAnsi.FindStringIndex(s[i:]); m != nil && m[0] == 0 {
				b.WriteString(s[i : i+m[1]])
				i += m[1]
				continue
			}
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if k == n {
			ls = append(ls, b.String())
			b.Reset()
			k = 0
		}
		b.WriteRune(r)
		k++
		i += size
	}
	return append(ls, b.String())
}
