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
// each terminal's size (with full, its directory and processes too).
const snapshotLua = `
local view, err = rex.session.view{}
if not view then error(err, 0) end
local term = "` + terminal + `"
local blocks = {}
for _, w in ipairs(view.windows or {}) do
  for _, layer in ipairs(w.layers or {}) do
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
return { view = view, blocks = blocks }
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
// of the window.
func (sn snapshot) size(w window) (int, int) {
	bs, at := w.tiled()
	for _, b := range bs {
		i, r := sn.Blocks[b.ID], at[b.ID]
		if i.Size == nil || len(r) != 4 || r[2] <= 0 || r[3] <= 0 {
			continue
		}
		return int(math.Round(float64(i.Size.Columns) / r[2])), int(math.Round(float64(i.Size.Rows) / r[3]))
	}
	return 0, 0
}

// place is where weaver put window number n (home 0, a thread its number,
// a view 900 and on); -1 when weaver put it nowhere.
func (s Session) place(n string) int {
	if v, err := strconv.Atoi(s.ids().noted("want", n)); err == nil {
		return v
	}
	return -1
}

func (s Session) windowsOf(sn snapshot) []mux.Window {
	var ws []mux.Window
	for _, w := range sn.View.Windows {
		n, err := s.ids().num(w.ID)
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
		wn, err := s.ids().num(w.ID)
		if err != nil {
			continue
		}
		cols, lines := sn.size(w)
		bs, at := w.tiled()
		for _, b := range bs {
			n, err := s.ids().num(b.ID)
			if err != nil {
				continue
			}
			i := sn.Blocks[b.ID]
			p := mux.Pane{ID: mux.PaneID(n), Tab: mux.WindowID(wn), TabName: w.Label, TabIndex: s.place(wn),
				Cmd: s.ids().noted("cmd", n), Title: b.Label, Cwd: cwdOf(i),
				Focused: w.ID == sn.View.Active && w.Focused == b.ID}
			if i.Size != nil {
				p.W, p.H = i.Size.Columns, i.Size.Rows
			}
			if r := at[b.ID]; len(r) == 4 {
				p.X, p.Y = int(math.Round(r[0]*float64(cols))), int(math.Round(r[1]*float64(lines)))
			}
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
