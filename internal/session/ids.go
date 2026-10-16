package session

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/robbowland/weaver/internal/jsonx"
	"github.com/robbowland/weaver/internal/tmux"
)

// paneOfNum is the pane a record keeps as a number.
func paneOfNum(n jsonx.Int) tmux.PaneID { return tmux.PaneID(strconv.Itoa(int(n))) }

// PaneOfFile is the pane of one of weaver's per-agent files
// ($STATE_DIR/<skey>_<pane>.<ext>).
func PaneOfFile(f string) tmux.PaneID {
	b := f[:len(f)-len(filepath.Ext(f))]
	return tmux.PaneID(b[strings.LastIndexByte(b, '_')+1:])
}
