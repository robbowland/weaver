package wvr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/robbowland/weaver/internal/session"
	"gopkg.in/yaml.v3"
)

// gh-dash's config for the view: weaver's whole, valid YAML, its keys run
// this binary, the fork's extras only for the fork; gh-dash's environment
// keeps your own config out and gh's sign-in in.
func TestGhDashConfig(t *testing.T) {
	bin := t.TempDir()
	for _, c := range []struct {
		version string
		fork    bool
	}{{"gh-dash version 4.26.1-rb1", true}, {"gh-dash version 4.26.1", false}} {
		if err := os.WriteFile(filepath.Join(bin, "gh-dash"), []byte("#!/bin/sh\necho '"+c.version+"'\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		e := &Env{&session.Env{StateDir: t.TempDir(), Self: "/opt/bin/wvr"}}
		t.Setenv("GH_CONFIG_DIR", "")
		t.Setenv("XDG_CONFIG_HOME", "/x")
		env, err := e.ghDashConfig([]string{filepath.Join(bin, "gh-dash")})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(env, " ") != "XDG_CONFIG_HOME="+e.StateDir+"/gh-dash GH_CONFIG_DIR=/x/gh GH_DASH_CONFIG=" {
			t.Errorf("env: %v", env)
		}
		b, err := os.ReadFile(e.StateDir + "/gh-dash/gh-dash/config.yml")
		if err != nil {
			t.Fatal(err)
		}
		var cfg struct {
			PrSections  []struct{ Title string } `yaml:"prSections"`
			Defaults    struct{ Views []string }
			Keybindings struct {
				Universal, Prs, Issues []struct{ Key, Command, Builtin string }
			}
		}
		if err := yaml.Unmarshal(b, &cfg); err != nil {
			t.Fatalf("%s: not YAML: %v\n%s", c.version, err, b)
		}
		keys := map[string]string{}
		for _, k := range cfg.Keybindings.Prs {
			keys[k.Key] = k.Command + k.Builtin
		}
		if keys["C"] != "/opt/bin/wvr _from github pr {{.RepoName}} {{.PrNumber}}" || keys["d"] != keys["C"]+" --review" || keys["alt+R"] != keys["d"] {
			t.Errorf("%s: pr keys: %v", c.version, keys)
		}
		fork := strings.Contains(string(b), "previewLineDown")
		if fork != c.fork || (len(cfg.Defaults.Views) == 2) != c.fork || len(cfg.PrSections) != 3 {
			t.Errorf("%s: fork extras %v, views %v, sections %d", c.version, fork, cfg.Defaults.Views, len(cfg.PrSections))
		}
	}
}

// LINEAR shows once linear-tui is signed in, asked at most once a minute.
func TestLinearReady(t *testing.T) {
	bin := t.TempDir()
	out := filepath.Join(bin, "status")
	if err := os.WriteFile(filepath.Join(bin, "linear-tui"), []byte("#!/bin/sh\ncat '"+out+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	e := &Env{&session.Env{StateDir: t.TempDir()}}
	if err := os.WriteFile(out, []byte("OAuth token  none\n\nNot authenticated. Run `linear-tui auth login` to sign in.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if e.linearReady() {
		t.Error("signed out: ready")
	}
	if err := os.WriteFile(out, []byte("OAuth token  acme (rob)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if e.linearReady() {
		t.Error("asked again within the minute")
	}
	old := time.Now().Add(-2 * time.Minute)
	if err := os.Chtimes(e.StateDir+"/linear-auth", old, old); err != nil {
		t.Fatal(err)
	}
	if !e.linearReady() {
		t.Error("signed in: not ready")
	}
	t.Setenv("PATH", "/usr/bin:/bin")
	if e.linearReady() {
		t.Error("not installed: ready")
	}
}
