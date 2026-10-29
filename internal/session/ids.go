package session

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/robbowland/weaver/internal/jsonx"
	"github.com/robbowland/weaver/internal/mux"
)

// paneOfNum is the pane a record keeps as a number.
func paneOfNum(n jsonx.Int) mux.PaneID { return mux.PaneID(strconv.Itoa(int(n))) }

// PaneOfFile is the pane of one of weaver's per-agent files
// ($STATE_DIR/<skey>_<pane>.<ext>).
func PaneOfFile(f string) mux.PaneID {
	b := f[:len(f)-len(filepath.Ext(f))]
	return mux.PaneID(b[strings.LastIndexByte(b, '_')+1:])
}
