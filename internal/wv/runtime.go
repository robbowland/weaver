package wv

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	weaver "github.com/robbowland/weaver"
)

// Runtime is the directory holding what the binary carries (the script,
// nvim/, config/) as files, for what needs them on disk: bash, Neovim and
// tmux. Unpacked once per build, under a name made of its content, into
// $XDG_DATA_HOME/weaver/runtime/; $WEAVER_SH (a checkout's script, its
// nvim/ and config/ beside it) stands in for it.
func Runtime() (string, error) {
	if sh := os.Getenv("WEAVER_SH"); sh != "" {
		return filepath.Dir(sh), nil
	}
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		data = os.Getenv("HOME") + "/.local/share"
	}
	h := sha256.New()
	fs.WalkDir(weaver.Files, ".", func(p string, d fs.DirEntry, err error) error {
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
	dir := data + "/weaver/runtime/" + hex.EncodeToString(h.Sum(nil))[:16]
	if _, err := os.Stat(dir + "/weaver"); err == nil {
		return dir, nil
	}
	tmp := dir + ".tmp." + strconv.Itoa(os.Getpid())
	err := fs.WalkDir(weaver.Files, ".", func(p string, d fs.DirEntry, err error) error {
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
		mode := os.FileMode(0o644)
		if p == "weaver" {
			mode = 0o755
		}
		return os.WriteFile(filepath.Join(tmp, p), b, mode)
	})
	if err != nil {
		os.RemoveAll(tmp)
		return "", err
	}
	if err := os.Rename(tmp, dir); err != nil {
		// Another weaver unpacked it meanwhile.
		os.RemoveAll(tmp)
		if _, err2 := os.Stat(dir + "/weaver"); err2 != nil {
			return "", err
		}
	}
	return dir, nil
}

// Script runs the shell script in place of this process for what is not
// compiled yet: by bash, told which binary it is and where its files are.
func (e *Env) Script(args []string) error {
	dir, err := Runtime()
	if err != nil {
		return err
	}
	script := dir + "/weaver"
	if sh := os.Getenv("WEAVER_SH"); sh != "" {
		script = sh
	}
	bash := "/bin/bash"
	if p, err := lookPath("bash"); err == nil {
		bash = p
	}
	env := append(os.Environ(), "WEAVER_SELF="+e.Self, "WEAVER_RUNTIME="+dir)
	return syscall.Exec(bash, append([]string{bash, script}, args...), env)
}
