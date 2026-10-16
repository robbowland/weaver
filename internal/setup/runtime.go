package setup

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	weaver "github.com/robbowland/weaver"
	"github.com/robbowland/weaver/internal/files"
)

// Runtime is the directory holding what the binary carries (nvim/ and
// config/) as files, for what needs them on disk: Neovim and tmux. Unpacked
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
	if _, err := os.Stat(dir + "/config/tmux.conf"); err == nil {
		syncNvim(data+"/weaver", id)
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
		if _, err2 := os.Stat(dir + "/config/tmux.conf"); err2 != nil {
			return "", err
		}
	}
	syncNvim(data+"/weaver", id)
	pruneRuntimes(data+"/weaver/runtime", id)
	return dir, nil
}

// syncNvim keeps base/nvim, the Neovim plugin at a path that stays put for
// Neovim configs to load (lazy.nvim's `dir`), this build's: a copy, marked
// with the build's id, swapped in whole when the build changes.
func syncNvim(base, id string) {
	stable := base + "/nvim"
	if l, err := files.FirstLine(stable + "/.weaver-runtime"); err == nil && l == id {
		return
	}
	pid := strconv.Itoa(os.Getpid())
	tmp := stable + ".tmp." + pid
	err := fs.WalkDir(weaver.Files, "nvim", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		to := filepath.Join(tmp, strings.TrimPrefix(p, "nvim"))
		if d.IsDir() {
			return os.MkdirAll(to, 0o755)
		}
		b, err := weaver.Files.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(to, b, 0o644)
	})
	if err == nil {
		err = os.WriteFile(tmp+"/.weaver-runtime", []byte(id+"\n"), 0o644)
	}
	if err != nil {
		os.RemoveAll(tmp)
		return
	}
	old := stable + ".old." + pid
	_ = os.Rename(stable, old) // none there yet is fine
	if os.Rename(tmp, stable) != nil {
		os.RemoveAll(tmp)
	}
	os.RemoveAll(old)
}

// pruneRuntimes removes the unpacked files of builds other than this one
// that nothing has unpacked in a month (an nvim started from one keeps
// loading its modules from there, so a recent one stays).
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
