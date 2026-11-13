package rex

import (
	"bytes"
	"encoding/json"
	"math"
	"net/url"
	"strconv"
	"strings"

	"github.com/robbowland/weaver/internal/mux"
)

const terminal = "com.superlogical.terminal"

// snapshotLua is one look at the session, in one rex do: its view, and
// each tiled terminal's size (with full, its directory and processes too).
// A floating layer's terminals (popups, messages) are never panes.
const snapshotLua = `
local view, err = rex.session.view{}
if not view then error(err, 0) end
local term = "` + terminal + `"
local blocks = {}
for _, w in ipairs(view.windows or {}) do
  for _, layer in ipairs(w.layers or {}) do
    if layer.kind == "tiled" then
      for _, b in ipairs(layer.blocks or {}) do
        if b.creator_name == term then
          local info = { size = rex.block.call(term, "size", { block_id = b.block_id }) }
          if rex.args.full then
            info.pwd = rex.block.call(term, "pwd", { block_id = b.block_id })
            info.process = rex.block.call(term, "process", { block_id = b.block_id })
          end
          blocks[b.block_id] = info
        end
      end
    end
  end
end
return { view = view, blocks = blocks }
`

// fleetLua is one look at a fleet, in one rex do against its home: the
// sessions Rex runs, and a snapshot (as snapshotLua's) of home and of each
// session of rex.args.threads that runs. A script's connection reaches a
// session other than its own once it attaches to it; a terminal's methods
// are then called by session (block.method). A terminal's method that
// fails (its block gone since the view) answers nothing, as in
// snapshotLua; a session gone since the list is not looked at, as one not
// listed; any other Rex call that fails fails the look (error at level 0:
// no position, unlike a script's own error).
const fleetLua = `
local term = "` + terminal + `"
local list, err = rex.call("session.list", {})
if not list then error(err, 0) end
local live = {}
for _, s in ipairs(list.sessions or {}) do live[s.session_id] = true end
local function method(sid, b, m)
  return (rex.call("block.method", { session_id = sid, block_id = b, method = m }))
end
local function look(sid, view)
  local blocks = {}
  for _, w in ipairs(view.windows or {}) do
    for _, layer in ipairs(w.layers or {}) do
      if layer.kind == "tiled" then
        for _, b in ipairs(layer.blocks or {}) do
          if b.creator_name == term then
            local info = { size = method(sid, b.block_id, "size") }
            if rex.args.full then
              info.pwd = method(sid, b.block_id, "pwd")
              info.process = method(sid, b.block_id, "process")
            end
            blocks[b.block_id] = info
          end
        end
      end
    end
  end
  return { view = view, blocks = blocks }
end
local snaps = {}
local view, err = rex.session.view{}
if not view then error(err, 0) end
snaps[rex.session_id] = look(rex.session_id, view)
for _, sid in ipairs(rex.args.threads or {}) do
  if live[sid] and not snaps[sid] then
    local a, err = rex.session.attach{ session_id = sid }
    if a then
      snaps[sid] = look(sid, a.view)
    elseif tostring(err):find("code=session_not_found", 1, true) then
      live[sid] = nil
    else
      error(err, 0)
    end
  end
end
local ids = {}
for sid in pairs(live) do ids[#ids + 1] = sid end
return { live = ids, snaps = snaps }
`

// list is a JSON array that Lua may have written as {} when empty.
type list[T any] []T

func (l *list[T]) UnmarshalJSON(b []byte) error {
	if bytes.Equal(bytes.TrimSpace(b), []byte("{}")) {
		*l = nil
		return nil
	}
	var v []T
	err := json.Unmarshal(b, &v)
	*l = v
	return err
}

type node struct {
	Rect  []float64 `json:"rect"`
	Block string    `json:"block_id"`
	Split *struct {
		Direction string  `json:"direction"`
		Ratio     float64 `json:"ratio"`
		Before    *node   `json:"before"`
		After     *node   `json:"after"`
	} `json:"split"`
}

type block struct {
	ID      string `json:"block_id"`
	Label   string `json:"label"`
	Creator string `json:"creator_name"`
}

type layer struct {
	Kind   string      `json:"kind"`
	Layout *node       `json:"layout"`
	Blocks list[block] `json:"blocks"`
}

type window struct {
	ID      string      `json:"window_id"`
	Label   string      `json:"label"`
	Focused string      `json:"focused_block_id"`
	Zoomed  string      `json:"zoomed_block_id"` // the block filling it, while one does
	Layers  list[layer] `json:"layers"`
}

type view struct {
	ID      string       `json:"session_id"`
	Label   string       `json:"label"`
	Active  string       `json:"active_window_id"`
	Windows list[window] `json:"windows"`
}

type proc struct {
	Pid int    `json:"pid"`
	Cwd string `json:"cwd"`
}

type info struct {
	Size *struct {
		Columns int `json:"columns"`
		Rows    int `json:"rows"`
		// The sizes clients (an app) ask for: none until an app has drawn
		// the terminal, which is Rex's 80 by 24 till then.
		Desired list[struct {
			Columns int `json:"columns"`
		}] `json:"desired"`
	} `json:"size"`
	Pwd *struct {
		Pwd string `json:"pwd"`
	} `json:"pwd"`
	Process *struct {
		Child      *proc           `json:"child"`
		Foreground *proc           `json:"foreground"`
		LastExit   json.RawMessage `json:"last_exit"`
	} `json:"process"`
}

type snapshot struct {
	View   view            `json:"view"`
	Blocks map[string]info `json:"blocks"`
}

func (s Session) snapshot(full bool) (snapshot, error) {
	var sn snapshot
	err := do(s.ID, snapshotLua, map[string]any{"full": full}, &sn)
	return sn, err
}

// windowOf is the window block b is in (its tiled layout or a floating
// layer); "" when none.
func (v view) windowOf(b string) string {
	for _, w := range v.Windows {
		for _, l := range w.Layers {
			for _, x := range l.Blocks {
				if x.ID == b {
					return w.ID
				}
			}
		}
	}
	return ""
}

// rects are the blocks of a layout, by id: where each is, as fractions of
// the window.
func rects(n *node, into map[string][]float64) {
	switch {
	case n == nil:
	case n.Split != nil:
		rects(n.Split.Before, into)
		rects(n.Split.After, into)
	case n.Block != "" && len(n.Rect) == 4:
		into[n.Block] = n.Rect
	}
}

// tiled are a window's tiled blocks (not its floating layers', which are
// popups), with where each is.
func (w window) tiled() ([]block, map[string][]float64) {
	var bs []block
	at := map[string][]float64{}
	for _, l := range w.Layers {
		if l.Kind != "tiled" {
			continue
		}
		rects(l.Layout, at)
		for _, b := range l.Blocks {
			if b.Creator == terminal {
				bs = append(bs, b)
			}
		}
	}
	return bs, at
}

// size is a window's columns and lines, from a block's size and its share
// of the window. A window no app has drawn yet (made behind you: its
// terminals still Rex's 80 by 24) is as big as the active window, when an
// app has drawn that: the app draws a session's windows at one size.
func (sn snapshot) size(w window) (int, int) {
	if w.ID != sn.View.Active && !sn.drawn(w) {
		for _, a := range sn.View.Windows {
			if a.ID == sn.View.Active && sn.drawn(a) {
				return sn.ownSize(a)
			}
		}
	}
	return sn.ownSize(w)
}

// drawn: has an app drawn one of window w's terminals (asked for a size)?
func (sn snapshot) drawn(w window) bool {
	bs, _ := w.tiled()
	for _, b := range bs {
		if i := sn.Blocks[b.ID]; i.Size != nil && len(i.Size.Desired) > 0 {
			return true
		}
	}
	return false
}

// ownSize is size from window w's own terminals.
func (sn snapshot) ownSize(w window) (int, int) {
	// A zoomed block fills the window, whatever its share of the layout
	// says (and the others keep the sizes they had): its own size, or none.
	if w.Zoomed != "" {
		if i := sn.Blocks[w.Zoomed]; i.Size != nil {
			return i.Size.Columns, i.Size.Rows
		}
		return 0, 0
	}
	// A terminal an app has drawn first: one just split in is Rex's 80 by
	// 24 till the app draws it.
	bs, at := w.tiled()
	for _, drawn := range []bool{true, false} {
		for _, b := range bs {
			i, r := sn.Blocks[b.ID], at[b.ID]
			if i.Size == nil || len(r) != 4 || r[2] <= 0 || r[3] <= 0 || drawn && len(i.Size.Desired) == 0 {
				continue
			}
			return int(math.Round(float64(i.Size.Columns) / r[2])), int(math.Round(float64(i.Size.Rows) / r[3]))
		}
	}
	return 0, 0
}

// activeSize is the active window's columns and lines; 160 by 48 when
// they are not known.
func (sn snapshot) activeSize() (int, int) {
	for _, w := range sn.View.Windows {
		if c, l := sn.size(w); w.ID == sn.View.Active && c > 0 && l > 0 {
			return c, l
		}
	}
	return 160, 48
}

// splitBeside is the ratio of the side-by-side split block b is one half
// of, in any window.
func (v view) splitBeside(b string) (float64, bool) {
	for _, w := range v.Windows {
		for _, l := range w.Layers {
			sp := l.Layout
			if l.Kind != "tiled" || sp == nil || sp.Split == nil || sp.Split.Direction != "horizontal" ||
				sp.Split.Before == nil || sp.Split.After == nil {
				continue
			}
			if sp.Split.Before.Block == b || sp.Split.After.Block == b {
				return sp.Split.Ratio, true
			}
		}
	}
	return 0, false
}

// border is the border to move for block b to be cols wide, when b and
// another block split a window side by side: the blocks either side of it
// and its ratio. The window holding b alone counts.
func (sn snapshot) border(b string, cols int) (before, after string, ratio float64, ok bool) {
	for _, w := range sn.View.Windows {
		for _, l := range w.Layers {
			sp := l.Layout
			if l.Kind != "tiled" || sp == nil || sp.Split == nil || sp.Split.Direction != "horizontal" ||
				sp.Split.Before == nil || sp.Split.After == nil || sp.Split.Before.Block == "" || sp.Split.After.Block == "" {
				continue
			}
			before, after = sp.Split.Before.Block, sp.Split.After.Block
			if b != before && b != after {
				continue
			}
			total, _ := sn.size(w)
			if total <= 0 {
				return "", "", 0, false
			}
			ratio = float64(cols) / float64(total)
			if b == after {
				ratio = float64(total-cols) / float64(total)
			}
			return before, after, min(max(ratio, 0.05), 0.95), true
		}
	}
	return "", "", 0, false
}

// place is where weaver put window number n of home's (home 0, a view 900
// and on); -1 when weaver put it nowhere.
func (s Session) place(n string) int {
	if v, err := strconv.Atoi(s.ids().noted("want", n)); err == nil {
		return v
	}
	return -1
}

func (s Session) windowsOf(sn snapshot) []mux.Window {
	var ws []mux.Window
	for _, w := range sn.View.Windows {
		n, err := s.num(w.ID)
		if err != nil {
			continue
		}
		cols, _ := sn.size(w)
		ws = append(ws, mux.Window{ID: mux.WindowID(n), Index: s.place(n), Name: w.Label,
			View: s.ids().noted("view", n), Width: cols, Active: w.ID == sn.View.Active})
	}
	return ws
}

func (s Session) panesOf(sn snapshot) []mux.Pane {
	var ps []mux.Pane
	for _, w := range sn.View.Windows {
		wn, err := s.num(w.ID)
		if err != nil {
			continue
		}
		bs, _ := w.tiled()
		for _, b := range bs {
			n, err := s.num(b.ID)
			if err != nil {
				continue
			}
			i := sn.Blocks[b.ID]
			p := mux.Pane{ID: mux.PaneID(n), Session: s.ID, Window: mux.WindowID(wn), WindowName: w.Label,
				Cmd: s.ids().noted("cmd", n), Title: b.Label, Cwd: cwdOf(i)}
			if pr := i.Process; pr != nil {
				p.Exited = pr.Child == nil && len(pr.LastExit) > 0 && string(pr.LastExit) != "null"
			}
			ps = append(ps, p)
		}
	}
	return ps
}

// cwdOf is where a terminal is: the directory its shell last reported (a
// file:// URL), else its foreground program's.
func cwdOf(i info) string {
	if i.Pwd != nil && i.Pwd.Pwd != "" {
		if u, err := url.Parse(i.Pwd.Pwd); err == nil && u.Scheme == "file" {
			return u.Path
		}
		if !strings.Contains(i.Pwd.Pwd, "://") {
			return i.Pwd.Pwd
		}
	}
	if i.Process != nil && i.Process.Foreground != nil {
		return i.Process.Foreground.Cwd
	}
	return ""
}
