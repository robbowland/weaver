package wvr

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// A card's choices (close or keep, sign in or hide, ...), as buttons under
// it: ←/→ (or tab) move between them, enter takes the one you are on, a
// click takes the one clicked, and each still has its key (y, n, h, esc).
// What enter takes first is the card's default: the one that keeps things
// as they are, where the other would undo something.

// choice is one button: its key, and what it does.
type choice struct {
	key  string // "y", "n", "h", "⏎", "esc"
	what string
}

// keyByte is the byte that choice's key sends.
func (c choice) keyByte() byte {
	switch c.key {
	case "⏎":
		return '\r'
	case "esc":
		return 0x1b
	}
	return c.key[0]
}

var (
	reMouse  = regexp.MustCompile(`^\x1b\[<(\d+);(\d+);(\d+)([Mm])`)
	reCursor = regexp.MustCompile(`\x1b\[(\d+);\d+R`)
)

// choose shows cs on the line tty's cursor is on, indented by pad, def
// taken by enter until you move, and waits for one: its index, or -1 when
// the terminal went (or the card was left with a key no choice has, when
// cs has no esc).
func choose(t *os.File, pad string, cs []choice, def int) int {
	fd := int(t.Fd())
	old, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
	if err != nil {
		return -1
	}
	raw := *old
	raw.Lflag &^= unix.ECHO | unix.ICANON
	raw.Cc[unix.VMIN], raw.Cc[unix.VTIME] = 1, 0
	bestEffort(unix.IoctlSetTermios(fd, unix.TIOCSETA, &raw))
	// The buttons take clicks (SGR mouse reports), and the cursor hides.
	fmt.Fprint(t, "\033[?1000h\033[?1006h\033[?25l")
	defer func() {
		fmt.Fprint(t, "\033[?1000l\033[?1006l\033[?25h\r\n")
		bestEffort(unix.IoctlSetTermios(fd, unix.TIOCSETA, old))
	}()
	row := cursorRow(t)
	at := def
	// Where each button is on its line: [from, to) in columns, from 1.
	spans := make([][2]int, len(cs))
	draw := func() {
		var b strings.Builder
		// Each button's text where the card's values start (its own
		// padding a column before).
		b.WriteString("\r\033[2K" + pad + "       ")
		col := rlen(pad) + 7 + 1
		for i, c := range cs {
			if i > 0 {
				b.WriteString("   ")
				col += 3
			}
			label := " " + c.key + " " + c.what + " "
			spans[i] = [2]int{col, col + rlen(label)}
			col += rlen(label)
			if i == at {
				b.WriteString("\033[48;2;255;255;255m\033[38;2;0;0;0m" + label + COff)
			} else {
				b.WriteString(" " + CGrey + c.key + COff + " " + CDim + c.what + COff + " ")
			}
		}
		fmt.Fprint(t, b.String())
	}
	draw()
	buf := make([]byte, 64)
	pending := ""
read:
	for {
		n, err := t.Read(buf)
		if err != nil || n == 0 {
			return -1
		}
		pending += string(buf[:n])
		for pending != "" {
			s := pending
			switch {
			case strings.HasPrefix(s, "\033[<"):
				m := reMouse.FindStringSubmatch(s)
				if m == nil {
					if len(s) < 16 {
						continue read // the rest of the report is on its way
					}
					pending = s[1:]
					continue
				}
				pending = s[len(m[0]):]
				b, _ := strconv.Atoi(m[1])
				x, _ := strconv.Atoi(m[2])
				y, _ := strconv.Atoi(m[3])
				if m[4] == "M" && b&3 == 0 && b < 32 && (row == 0 || y == row) {
					for i, sp := range spans {
						if x >= sp[0] && x < sp[1] {
							at = i
							draw()
							return i
						}
					}
				}
			case strings.HasPrefix(s, "\033[C"), strings.HasPrefix(s, "\033[B"), s[0] == '\t':
				pending = strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(s, "\033[C"), "\033[B"), "\t")
				at = (at + 1) % len(cs)
				draw()
			case strings.HasPrefix(s, "\033[D"), strings.HasPrefix(s, "\033[A"), strings.HasPrefix(s, "\033[Z"):
				pending = s[3:]
				at = (at + len(cs) - 1) % len(cs)
				draw()
			case s[0] == '\r' || s[0] == '\n':
				return at
			case s == "\033":
				// esc alone (a sequence would have come with it).
				for i, c := range cs {
					if c.key == "esc" {
						return i
					}
				}
				return -1
			case s[0] == 0x1b:
				// Another sequence: passed over.
				pending = s[1:]
				for pending != "" && (pending[0] < 0x40 || pending[0] > 0x7e) {
					pending = pending[1:]
				}
				if pending != "" {
					pending = pending[1:]
				}
			default:
				pending = s[1:]
				for i, c := range cs {
					if k := c.keyByte(); k == s[0] || (k >= 'a' && k <= 'z' && k-32 == s[0]) {
						at = i
						draw()
						return i
					}
				}
			}
		}
	}
}

// cursorRow is the row tty's cursor is on, from 1 (0: the terminal did not
// say).
func cursorRow(t *os.File) int {
	fmt.Fprint(t, "\033[6n")
	fd := int(t.Fd())
	var got []byte
	b := make([]byte, 32)
	for len(got) < 32 {
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		if n, err := unix.Poll(fds, 300); err != nil || n == 0 {
			return 0
		}
		n, err := t.Read(b)
		if err != nil {
			return 0
		}
		got = append(got, b[:n]...)
		if i := strings.IndexByte(string(got), 'R'); i >= 0 {
			if m := reCursor.FindSubmatch(got[:i+1]); m != nil {
				r, _ := strconv.Atoi(string(m[1]))
				return r
			}
			return 0
		}
	}
	return 0
}
