package setup

import (
	"bytes"
	"cmp"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	weaver "github.com/robbowland/weaver"
	"github.com/robbowland/weaver/internal/store"
)

// mark is in each file weaver brings to another program's configuration:
// while it is there the file is weaver's, to write again as weaver's text
// changes; without it (a file of yours, or one you took over by removing
// the line), weaver leaves the file alone.
var mark = []byte("weaver: this file is weaver's")

// A Brought is a file weaver brings to another program's configuration:
// what it is, as wvr names it, where it goes, and its text (mark in it).
type Brought struct {
	Label string
	Path  string
	Text  []byte
}

// Fate is what becomes of a Brought file: it is there as weaver's text, it
// is to be written (none there, or weaver's, out of date), or one there is
// yours.
type Fate int

const (
	Current Fate = iota
	ToWrite
	Yours
)

// Fate is what becomes of f.
func (f Brought) Fate() Fate {
	b, err := os.ReadFile(f.Path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return ToWrite
	case err != nil:
		return Yours
	case bytes.Equal(b, f.Text):
		return Current
	case bytes.Contains(b, mark):
		return ToWrite
	}
	return Yours
}

// Write writes f in its place.
func (f Brought) Write() error {
	if err := os.MkdirAll(filepath.Dir(f.Path), 0o755); err != nil {
		return err
	}
	return store.WriteAtomic(f.Path, f.Text)
}

// BroughtFiles are what weaver brings: tuicr's settings for its review
// panel (where you have none: diff_watch_interval_ms, which no flag sets)
// and its Micrographics theme (the colours, and the syntax theme beside
// them), in tuicr's configuration directory. Neither $XDG_CONFIG_HOME nor
// anything else is set for tuicr to find them: the nvim its `e` opens would
// inherit it.
func BroughtFiles() []Brought {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		dir = os.Getenv("HOME") + "/.config"
	}
	dir += "/tuicr"
	var out []Brought
	for _, f := range []struct{ label, src, dst string }{
		{"tuicr's settings", "config/tuicr/config.toml", "config.toml"},
		{"tuicr's Micrographics theme", "config/tuicr/themes/micrographics.toml", "themes/micrographics.toml"},
		{"tuicr's Micrographics syntax theme", "config/tuicr/themes/Micrographics.tmTheme", "themes/Micrographics.tmTheme"},
	} {
		b, err := weaver.Files.ReadFile(f.src)
		if err != nil {
			continue
		}
		out = append(out, Brought{Label: f.label, Path: filepath.Join(dir, f.dst), Text: b})
	}
	return out
}

// Bring writes each of bs that is to be written: those it wrote, and the
// first error.
func Bring(bs []Brought) ([]Brought, error) {
	var wrote []Brought
	var first error
	for _, f := range bs {
		if f.Fate() != ToWrite {
			continue
		}
		if err := f.Write(); err != nil {
			first = cmp.Or(first, err)
			continue
		}
		wrote = append(wrote, f)
	}
	return wrote, first
}
