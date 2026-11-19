package wvr

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/mux"
	"github.com/robbowland/weaver/internal/session"
	"github.com/robbowland/weaver/internal/store"
)

// started is a process start at t as weaver records it (process.Starts):
// ps's lstart in UTC, a day under ten padded.
func started(t time.Time) string { return t.UTC().Format("Mon Jan _2 15:04:05 2006") }

// installClaude makes a claude that agents start ($WEAVER_CLAUDE), last
// changed at at; its path.
func installClaude(t *testing.T, at time.Time) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, at, at); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WEAVER_CLAUDE", p)
	return p
}

// An agent runs an older Claude Code when its process started before the
// installed claude last changed, to the second its start is recorded to;
// one whose start is not recorded, or that exited, does not say.
func TestOutdated(t *testing.T) {
	installed := time.Date(2026, 11, 5, 22, 35, 29, 600_000_000, time.UTC)
	for _, c := range []struct {
		r    session.Row
		want bool
	}{
		{session.Row{Pstart: started(installed.Add(-time.Hour))}, true},
		{session.Row{Pstart: "Thu Nov  5 22:35:28 2026"}, true},
		{session.Row{Pstart: "Thu Nov  5 22:35:29 2026"}, false}, // the same second
		{session.Row{Pstart: started(installed.Add(time.Minute))}, false},
		{session.Row{Pstart: ""}, false},
		{session.Row{Pstart: "garbled"}, false},
		{session.Row{Pstart: started(installed.Add(-time.Hour)), Exited: true}, false},
	} {
		if got := outdated(c.r, installed); got != c.want {
			t.Errorf("outdated(%q, exited %v) = %v, want %v", c.r.Pstart, c.r.Exited, got, c.want)
		}
	}
	if outdated(session.Row{Pstart: started(installed.Add(-time.Hour))}, time.Time{}) {
		t.Error("no claude installed: outdated")
	}
}

// The installed claude is the file its command resolves to, links
// followed, and when that changed.
func TestClaudeInstalled(t *testing.T) {
	at := time.Now().Add(-time.Hour).Truncate(time.Second)
	p := installClaude(t, at)
	link := filepath.Join(t.TempDir(), "claude")
	if err := os.Symlink(p, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WEAVER_CLAUDE", link+" --verbose")
	got, when := claudeInstalled()
	if file, _ := filepath.EvalSymlinks(p); got != file || !when.Equal(at) {
		t.Errorf("claudeInstalled() = %s %v, want %s %v", got, when, file, at)
	}
	t.Setenv("WEAVER_CLAUDE", "/nonexistent/claude")
	if _, when := claudeInstalled(); !when.IsZero() {
		t.Errorf("no claude: changed at %v", when)
	}
}

// A restart's message names the version installed: an npm package's, read
// from its package.json, or a native install's, its file's name.
func TestClaudeName(t *testing.T) {
	d := t.TempDir()
	pkg := filepath.Join(d, "node_modules", "@anthropic-ai", "claude-code")
	if err := os.MkdirAll(pkg+"/bin", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pkg+"/package.json", []byte("{\n  \"name\": \"@anthropic-ai/claude-code\",\n  \"version\": \"2.1.330\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		pkg + "/bin/claude.exe":                     "Claude Code 2.1.330",
		d + "/.local/share/claude/versions/2.1.331": "Claude Code 2.1.331",
		d + "/bin/claude":                           "the new Claude Code",
		"":                                          "the new Claude Code",
	} {
		if got := claudeName(path); got != want {
			t.Errorf("claudeName(%s) = %q, want %q", path, got, want)
		}
	}
}

// Of the threads' agents behind an update, those that restart by
// themselves rest (no turn, nothing asked of you, nothing in the
// background), have for ten minutes, have no helper at work, and are not
// in the session you look at.
func TestRestartable(t *testing.T) {
	now := int64(2_000_000_000)
	installed := time.Unix(now-3600, 0)
	old := started(installed.Add(-time.Hour))
	rest := func(pane mux.PaneID, sess string, f func(*session.Row)) session.Row {
		r := session.Row{Pane: pane, Session: sess, Tnum: 1, State: agent.StateIdle, Since: now - restIdle, Pstart: old, Turn: 1}
		if f != nil {
			f(&r)
		}
		return r
	}
	rows := []session.Row{
		rest("1", "s1", nil),
		rest("2", "s2", func(r *session.Row) { r.Since = now - restIdle + 1 }),
		rest("3", "s3", func(r *session.Row) { r.State = agent.StateWorking }),
		rest("4", "s4", func(r *session.Row) { r.State = agent.StateNeedsYou }),
		rest("5", "s5", func(r *session.Row) { r.State = agent.StateDone }),
		rest("6", "s6", func(r *session.Row) { r.State, r.Seen = agent.StateDone, true }),
		rest("7", "s7", func(r *session.Row) { r.Bg = 1 }),
		rest("8", "s8", func(r *session.Row) { r.Pstart = started(installed.Add(time.Minute)) }),
		rest("9", "shown", nil),
		rest("10", "s10", nil),
		// s10's helper at work.
		rest("11", "s10", func(r *session.Row) { r.Tnum, r.Thread, r.State = 0, 1, agent.StateWorking }),
		rest("12", "s12", func(r *session.Row) { r.Exited = true }),
		rest("13", "s13", func(r *session.Row) { r.State = agent.StateInterrupted }),
		// A helper itself, behind and resting: not a thread's agent.
		rest("14", "s1", func(r *session.Row) { r.Tnum, r.Thread = 0, 1 }),
	}
	got := restartable(rows, now, installed, "shown")
	if want := []mux.PaneID{"1", "6", "13"}; strings.Join(panes(got), " ") != strings.Join(panes(want), " ") {
		t.Errorf("restartable = %v, want %v", got, want)
	}
}

func panes(ps []mux.PaneID) []string {
	var out []string
	for _, p := range ps {
		out = append(out, string(p))
	}
	return out
}

// The session the Rex app shows is its preference, as `defaults export`
// writes it: base64 of a JSON object naming it.
func TestLastFocused(t *testing.T) {
	data := base64.StdEncoding.EncodeToString([]byte(`{"sessionID":"session:01a1"}`))
	prefs := "<plist><dict>\n\t<key>Workspace.lastFocusedSession</key>\n\t<data>\n\t" + data[:10] + "\n\t" + data[10:] + "\n\t</data>\n</dict></plist>"
	if got, ok := lastFocused([]byte(prefs)); got != "session:01a1" || !ok {
		t.Errorf("lastFocused = %q %v, want session:01a1", got, ok)
	}
	if _, ok := lastFocused([]byte("<plist><dict></dict></plist>")); ok {
		t.Error("no preference: a session")
	}
}

// A row says its agent is behind an update (update · R) when R would
// restart it now: not mid-turn, not exited (whose fact already says R).
func TestRowSignalsUpdate(t *testing.T) {
	now := int64(2_000_000_000)
	for _, c := range []struct {
		r    session.Row
		fact string
		want string
	}{
		{session.Row{State: agent.StateIdle, Turn: 1}, "", "update · R"},
		{session.Row{State: agent.StateDone, Seen: true}, "Tidied.", "update · R · Tidied."},
		{session.Row{State: agent.StateDone}, "Tidied.", "update · R · Tidied."},
		{session.Row{State: agent.StateWorking}, "thinking", "thinking"},
		{session.Row{State: agent.StateNeedsYou}, "input", "input"},
		{session.Row{State: agent.StateIdle, Exited: true}, "exited · R starts it again, x closes it", "exited · R starts it again, x closes it"},
	} {
		if got := rowSignals(c.fact, c.r, now, threadPR{}, true); got != c.want {
			t.Errorf("rowSignals(%q, %s) = %q, want %q", c.fact, c.r.State, got, c.want)
		}
	}
	if got := rowSignals("", session.Row{State: agent.StateIdle}, now, threadPR{}, false); got != "" {
		t.Errorf("not behind: %q", got)
	}
}

// The board says which agents run an older Claude Code than the one
// installed.
func TestBoardUpdateMarker(t *testing.T) {
	t.Setenv("COLUMNS", "120")
	t.Setenv("COLOR", "")
	e, agents, lost, now := boardScene(t)
	installed := time.Unix(now-1800, 0)
	installClaude(t, installed)
	for i := range agents {
		switch agents[i].Title {
		case "Board sections", "Background start":
			agents[i].Pstart = started(installed.Add(-time.Hour))
		case "Oldest":
			agents[i].Pstart = started(installed.Add(time.Minute))
		}
	}
	got := e.board(true, agents, lost, now)
	for title, want := range map[string]string{
		"Board sections":   "[ ] | Board sections | update · R | board | 40m",
		"Background start": "[/] | Background start | running tests | main | 2m",
		"Oldest":           "[ ] | Oldest | oldest | 3d",
	} {
		for _, l := range strings.Split(got, "\n") {
			if strings.Contains(l, title) {
				if l = spaced(l); l != want {
					t.Errorf("%s's row: %q, want %q", title, l, want)
				}
			}
		}
	}
}

// An idle thread's agent behind an update restarts by itself onto the new
// one, as R restarts it, saying so where you are; not while you look at
// its session, and not when weaver cannot tell where you look.
func TestRestartUpdated(t *testing.T) {
	e, p, sid, _ := closing(t)
	home := e.RexHere
	installClaude(t, time.Now().Add(-time.Minute))
	long := time.Now().Add(-time.Hour)
	// (No pid: the test's own process is not the one that started then.)
	store.UpdateState(e.StateFile(p), func(r *agent.Record) {
		r.State, r.Since, r.ProcStart, r.Pid = agent.StateIdle, json.Number(strconv.FormatInt(long.Unix(), 10)), started(long), ""
	})
	restarted := func() bool { st, _ := store.ReadState(e.StateFile(p)); return st.ProcStart == "" }
	t.Cleanup(func() { shownSession = appSession })

	// You look at it: left.
	shownSession = func() (string, bool) { return sid, true }
	e.restartUpdated()
	if restarted() {
		t.Fatal("restarted the agent of the session you look at")
	}
	// Where you look is not known: left.
	shownSession = func() (string, bool) { return "", false }
	e.restartUpdated()
	if restarted() {
		t.Fatal("restarted not knowing where you look")
	}
	// You look at home: restarted, and said there.
	shownSession = func() (string, bool) { return home, true }
	e.restartUpdated()
	if !restarted() {
		t.Fatal("an idle agent behind an update was not restarted")
	}
	if got, want := messages(t, home), "weaver: restarted 01 onto the new Claude Code"; got != want {
		t.Errorf("home said %q, want %q", got, want)
	}
}
