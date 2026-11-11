package setup

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"time"

	weaver "github.com/robbowland/weaver"
)

// Runtime is the directory holding what the binary carries (config/) as
// files, for what needs them on disk: Rex's keys. Unpacked
// once per build, under a name made of its content, into
// $XDG_DATA_HOME/weaver/runtime/.
func Runtime() (string, error) {
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		data = os.Getenv("HOME") + "/.local/share"
	}
	h := sha256.New()
	err := fs.WalkDir(weaver.Files, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := weaver.Files.ReadFile(p)
		if err != nil {
			return err
		}
		h.Write([]byte(p))
		h.Write([]byte{0})
		h.Write(b)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("reading the files weaver carries: %w", err)
	}
	id := hex.EncodeToString(h.Sum(nil))[:16]
	dir := data + "/weaver/runtime/" + id
	if _, err := os.Stat(dir + "/config/rex.lua"); err == nil {
		removeOldCopies(data + "/weaver")
		return dir, nil
	}
	tmp := dir + ".tmp." + strconv.Itoa(os.Getpid())
	err = fs.WalkDir(weaver.Files, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(tmp, p), 0o755)
		}
		b, err := weaver.Files.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(tmp, p), b, 0o644)
	})
	if err != nil {
		os.RemoveAll(tmp)
		return "", err
	}
	if err := os.Rename(tmp, dir); err != nil {
		// Another weaver unpacked it meanwhile.
		os.RemoveAll(tmp)
		if _, err2 := os.Stat(dir + "/config/rex.lua"); err2 != nil {
			return "", err
		}
	}
	removeOldCopies(data + "/weaver")
	pruneRuntimes(data+"/weaver/runtime", id)
	return dir, nil
}

// removeOldCopies removes, from base, what weaver no longer keeps there:
// its Neovim plugin's copy (nvim, which Neovim configs loaded) and the
// plugins it fetched for it (plugins).
func removeOldCopies(base string) {
	for _, d := range []string{"nvim", "plugins"} {
		if _, err := os.Lstat(base + "/" + d); err == nil {
			os.RemoveAll(base + "/" + d)
		}
	}
}

// pruneRuntimes removes the unpacked files of builds other than this one
// that nothing has unpacked in a month (a recent one may still be in use).
func pruneRuntimes(dir, id string) {
	es, _ := os.ReadDir(dir)
	for _, e := range es {
		if e.Name() == id || !e.IsDir() {
			continue
		}
		if st, err := e.Info(); err == nil && time.Since(st.ModTime()) > 31*24*time.Hour {
			os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
}
