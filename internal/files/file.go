// Package files reads and writes the small files weaver keeps: a line, a
// value, whole or not at all.
package files

import (
	"os"
	"strconv"
	"strings"
)

// FirstLine is a file's first line (as `read -r`: without its newline).
func FirstLine(f string) (string, error) {
	b, err := os.ReadFile(f)
	if err != nil {
		return "", err
	}
	if i := strings.IndexByte(string(b), '\n'); i >= 0 {
		b = b[:i]
	}
	return string(b), nil
}

// ReadTrim is a file's content without its trailing newlines, as $(<file).
func ReadTrim(f string) (string, error) {
	b, err := os.ReadFile(f)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(b), "\n"), nil
}

// Put leaves file f holding text and a newline, for readers that may read
// it meanwhile (fzf's keys, its transforms): alone when it holds that
// already, else replaced whole (a rename); a new file is just written.
func Put(f, text string) error {
	text = strings.TrimRight(text, "\n")
	cur, err := ReadTrim(f)
	if err != nil {
		return os.WriteFile(f, []byte(text+"\n"), 0o644)
	}
	if cur == text {
		return nil
	}
	tmp := f + "." + strconv.Itoa(os.Getpid())
	if err := os.WriteFile(tmp, []byte(text+"\n"), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, f)
}

// Exists is whether f is there (a broken link counts).
func Exists(f string) bool {
	_, err := os.Lstat(f)
	return err == nil
}
