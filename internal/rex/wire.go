package rex

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The Rex server's control connection, spoken directly (the server
// documents it at GET /llms.txt on its socket: "The Control Connection"):
// a WebSocket at /api/control on its Unix socket, subprotocol
// rex.control.v1, one JSON object per text message. A call over it takes
// a fraction of a millisecond; through the rex command, a process of a
// 42 MB binary each, 15 to 20 ms. One connection per wvr, opened at its
// first call and closed as it ends (Close); where it cannot be opened (no
// Unix socket, a server that refuses it), the rex command as before.

// wireRevision is the control API revision weaver's calls are written
// for, and wireMinRevision the oldest whose shapes they still read.
const (
	wireRevision    = 12
	wireMinRevision = 8
)

// wireTimeout bounds each call, as the rex command's --timeout does.
var wireTimeout = 10 * time.Second

// wireIdle is how long a connection may sit unused before the next call
// opens a new one: while idle it answers no pings and drains no events,
// so after minutes of it (a board or a panel waiting) the server may have
// closed it.
const wireIdle = 20 * time.Second

// wire is one control connection, to the server at Unix socket ep.
type wire struct {
	ep       string
	c        net.Conn
	r        *bufio.Reader
	next     int
	attached map[string]bool
	used     time.Time
	fresh    bool // opened for the call now being made
	sent     bool // a request has been sent on it during this batch
}

var (
	wireMu  sync.Mutex
	wireNow *wire
	// wireOff are the servers that refused the handshake (no WebSocket, no
	// rex.control.v1, no welcome): this process uses the rex command for
	// them. One that does not answer at all is tried again at each call
	// (it may be starting).
	wireOff = map[string]bool{}
)

// errNoWire: there is no control connection to call over; the rex
// command it is.
var errNoWire = errors.New("rex: no control connection")

// wireReq is one call: a method and its arguments.
type wireReq struct {
	method string
	in     map[string]any
}

// wireCall calls method with in over the control connection, its result
// into out (when not nil). errNoWire when there is none to be had: the
// caller falls back on the rex command. A connection that fails part way
// is closed (the next call opens another), and its error returned: the
// call may have run, so it is not made again.
func wireCall(method string, in map[string]any, out any) error {
	res, errs, err := wireBatch([]wireReq{{method, in}})
	if err != nil {
		return err
	}
	if errs[0] != nil {
		return errs[0]
	}
	if makesBlocks[method] {
		disown(method, in, res[0])
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(res[0], out)
}

// makesBlocks are the methods that make blocks, which the connection that
// made them then owns: a terminal takes its size from its owner, and
// weaver's connection asks none (a message's terminal, owned by it, shows
// nothing). Through the rex command its connection ended with the call,
// giving them up; here, disown gives them up.
var makesBlocks = map[string]bool{"session.create": true, "session.new_window": true, "session.new_split": true,
	"session.new_layer": true, "session.new_block": true}

// disown gives up the blocks the call of method made (every block of its
// session the connection owns: Rex gives up all when none are named).
func disown(method string, in map[string]any, res json.RawMessage) {
	sid, _ := in["session_id"].(string)
	if method == "session.create" {
		var out struct {
			ID string `json:"session_id"`
		}
		_ = json.Unmarshal(res, &out)
		sid = out.ID
	}
	if sid != "" {
		_, _, _ = wireBatch([]wireReq{{"session.disown_block", map[string]any{"session_id": sid}}})
	}
}

// wireBatch makes calls all at once, over the control connection: their
// results in order, or each one's error (a Rex error as the rex command
// says one). The server runs calls side by side, but for the ones it runs
// in order (its doc's "Order of Requests"), so none may depend on another
// of the same batch. A session a call names is attached first. err is
// errNoWire, or the connection having failed (closed: the next call opens
// another).
func wireBatch(reqs []wireReq) ([]json.RawMessage, []error, error) {
	wireMu.Lock()
	defer wireMu.Unlock()
	res, errs, err := wireBatchOn(reqs)
	var gone *wireGone
	if errors.As(err, &gone) {
		// A connection kept from before, found closed as this batch was
		// sent (its server gone or restarted, or it dropped the idle
		// connection): nothing of the batch reached it. Once more, on a
		// new one.
		res, errs, err = wireBatchOn(reqs)
	}
	return res, errs, err
}

// wireGone: a kept connection was found closed as a batch was first sent
// on it; nothing reached the server.
type wireGone struct{ err error }

func (e *wireGone) Error() string { return "rex: " + e.err.Error() }

func wireBatchOn(reqs []wireReq) ([]json.RawMessage, []error, error) {
	w, err := wireGet()
	if err != nil {
		return nil, nil, err
	}
	// The sessions to attach to first.
	var att []wireReq
	seen := map[string]bool{}
	for _, r := range reqs {
		if sid, ok := r.in["session_id"].(string); ok && sid != "" && needsAttach(r.method) && !w.attached[sid] && !seen[sid] {
			seen[sid] = true
			att = append(att, wireReq{"session.attach", map[string]any{"session_id": sid}})
		}
	}
	failed := map[string]error{}
	if len(att) > 0 {
		_, aerrs, err := w.roundTrip(att)
		if err != nil {
			return nil, nil, wireBroken(w, err)
		}
		for i, a := range att {
			sid := a.in["session_id"].(string)
			if aerrs[i] != nil {
				failed[sid] = rexError("session.attach", aerrs[i])
			} else {
				w.attached[sid] = true
			}
		}
	}
	res := make([]json.RawMessage, len(reqs))
	errs := make([]error, len(reqs))
	var send []wireReq
	var at []int
	for i, r := range reqs {
		if sid, ok := r.in["session_id"].(string); ok && failed[sid] != nil && needsAttach(r.method) {
			errs[i] = failed[sid]
			continue
		}
		send, at = append(send, r), append(at, i)
	}
	if len(send) > 0 {
		got, gerrs, err := w.roundTrip(send)
		if err != nil {
			return nil, nil, wireBroken(w, err)
		}
		for j, i := range at {
			res[i] = got[j]
			if gerrs[j] != nil {
				errs[i] = rexError(reqs[i].method, gerrs[j])
			}
		}
	}
	return res, errs, nil
}

// needsAttach: does a call of method that names a session need the
// connection attached to it? All but those whose session_id names no
// session for the connection to act on, and attach itself.
func needsAttach(method string) bool {
	switch method {
	case "session.attach", "client.list", "client.perform":
		return false
	}
	return true
}

// rexError is a Rex error of a call of method, as the rex command says
// one: callers match on its code and words.
func rexError(method string, e *wireError) error {
	return fmt.Errorf("%s: rex: %s (status=%d code=%s)", method, e.Message, e.Status, e.Code)
}

// wireBroken closes a connection that failed, and says so.
func wireBroken(w *wire, err error) error {
	w.close()
	wireNow = nil
	var gone *wireGone
	if errors.As(err, &gone) {
		return gone
	}
	return fmt.Errorf("rex: %w", err)
}

// wireGet is the connection, opened if need be: a new one in place of one
// left idle past wireIdle. errNoWire when none can be opened.
func wireGet() (*wire, error) {
	ep := wireEndpoint()
	if os.Getenv("WEAVER_REX_WIRE") == "0" {
		ep = ""
	}
	if w := wireNow; w != nil {
		if w.ep == ep && time.Since(w.used) < wireIdle {
			w.used, w.fresh, w.sent = time.Now(), false, false
			return w, nil
		}
		w.close()
		wireNow = nil
	}
	if ep == "" || wireOff[ep] {
		return nil, errNoWire
	}
	w, err := wireDial(ep)
	if err != nil {
		var refused *wireRefused
		if errors.As(err, &refused) {
			wireOff[ep] = true
		}
		return nil, errNoWire
	}
	wireNow = w
	return w, nil
}

// wireRefused: a server answered, but not as the control connection.
type wireRefused struct{ why string }

func (e *wireRefused) Error() string { return "rex: " + e.why }

// Close ends wvr's control connection, if it has one, with a close
// message: the server forgets its client at once (one that goes without
// is kept a minute, attached to its sessions). Before wvr exits or execs.
func Close() {
	wireMu.Lock()
	defer wireMu.Unlock()
	if wireNow != nil {
		wireNow.close()
		wireNow = nil
	}
}

// wireEndpoint is the Unix socket of the server $REX_SERVER names (a
// unix:// URL), else the local server's (Rex's runtime directory's
// server.sock, when there); "" for any other (a host, a pipe).
func wireEndpoint() string {
	ep := os.Getenv("REX_SERVER")
	if ep == "" {
		p := filepath.Join(runtimeDir(), "server.sock")
		if st, err := os.Stat(p); err == nil && st.Mode()&os.ModeSocket != 0 {
			return p
		}
		return ""
	}
	u, err := url.Parse(ep)
	if err != nil || u.Scheme != "unix" {
		return ""
	}
	if u.Path != "" {
		return u.Path
	}
	return u.Opaque
}

// wireDial opens the control connection at Unix socket path: the
// WebSocket handshake, then hello and welcome.
func wireDial(path string) (*wire, error) {
	c, err := net.DialTimeout("unix", path, 2*time.Second)
	if err != nil {
		return nil, err
	}
	w := &wire{ep: path, c: c, r: bufio.NewReaderSize(c, 64<<10), attached: map[string]bool{}, used: time.Now(), fresh: true}
	if err := w.handshake(); err != nil {
		_ = c.Close()
		return nil, err
	}
	return w, nil
}

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

func (w *wire) handshake() error {
	_ = w.c.SetDeadline(time.Now().Add(5 * time.Second))
	var k [16]byte
	if _, err := rand.Read(k[:]); err != nil {
		return err
	}
	key := base64.StdEncoding.EncodeToString(k[:])
	req := "GET /api/control HTTP/1.1\r\nHost: localhost\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Protocol: rex.control.v1\r\n\r\n"
	if _, err := io.WriteString(w.c, req); err != nil {
		return err
	}
	status, err := w.r.ReadString('\n')
	if err != nil {
		return err
	}
	if f := strings.Fields(status); len(f) < 2 || f[1] != "101" {
		return &wireRefused{"no WebSocket: " + strings.TrimSpace(status)}
	}
	sum := sha1.Sum([]byte(key + wsGUID))
	accept, proto := "", ""
	for {
		l, err := w.r.ReadString('\n')
		if err != nil {
			return err
		}
		l = strings.TrimRight(l, "\r\n")
		if l == "" {
			break
		}
		k, v, _ := strings.Cut(l, ":")
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "sec-websocket-accept":
			accept = strings.TrimSpace(v)
		case "sec-websocket-protocol":
			proto = strings.TrimSpace(v)
		}
	}
	if accept != base64.StdEncoding.EncodeToString(sum[:]) || proto != "rex.control.v1" {
		return &wireRefused{"not a rex.control.v1 connection"}
	}
	hello := map[string]any{"type": "hello", "api_revision": wireRevision, "api_min_revision": wireMinRevision,
		"client": map[string]any{"kind": "cli", "name": "weaver"}}
	if err := w.send(hello); err != nil {
		return err
	}
	b, err := w.message()
	if err != nil {
		return err
	}
	var wel struct {
		Type     string `json:"type"`
		Protocol string `json:"protocol"`
	}
	if json.Unmarshal(b, &wel) != nil || wel.Type != "welcome" || wel.Protocol != "rex.control.v1" {
		return &wireRefused{"no welcome"}
	}
	return nil
}

// wireError is the error a response carries.
type wireError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Status  int    `json:"status"`
}

func (e *wireError) Error() string { return e.Message }

// roundTrip sends reqs at once and waits for their responses, skipping
// the events among them: each one's payload, or its error.
func (w *wire) roundTrip(reqs []wireReq) ([]json.RawMessage, []*wireError, error) {
	_ = w.c.SetDeadline(time.Now().Add(wireTimeout))
	pos := map[string]int{}
	for i, q := range reqs {
		w.next++
		id := strconv.Itoa(w.next)
		pos[id] = i
		in := q.in
		if in == nil {
			in = map[string]any{}
		}
		if err := w.send(map[string]any{"type": "request", "id": id, "method": q.method, "payload": in}); err != nil {
			if i == 0 && !w.fresh && !w.sent {
				return nil, nil, &wireGone{err}
			}
			return nil, nil, err
		}
		w.sent = true
	}
	res := make([]json.RawMessage, len(reqs))
	errs := make([]*wireError, len(reqs))
	for left := len(reqs); left > 0; {
		b, err := w.message()
		if err != nil {
			return nil, nil, err
		}
		var r struct {
			Type    string          `json:"type"`
			ID      string          `json:"id"`
			Payload json.RawMessage `json:"payload"`
			Error   *wireError      `json:"error"`
		}
		if json.Unmarshal(b, &r) != nil || r.Type != "response" {
			continue // an event, or a type newer than weaver
		}
		i, ok := pos[r.ID]
		if !ok {
			continue
		}
		delete(pos, r.ID)
		res[i], errs[i] = r.Payload, r.Error
		left--
	}
	w.used = time.Now()
	return res, errs, nil
}

// close ends the connection with a close message (normal closure).
func (w *wire) close() {
	_ = w.c.SetDeadline(time.Now().Add(200 * time.Millisecond))
	_ = w.frame(0x8, []byte{0x03, 0xe8})
	_ = w.c.Close()
}

// send writes v as one text message.
func (w *wire) send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return w.frame(0x1, b)
}

// frame writes one final frame of opcode op, masked, as a client's must be.
func (w *wire) frame(op byte, p []byte) error {
	var h [14]byte
	h[0] = 0x80 | op
	n := 2
	switch l := len(p); {
	case l < 126:
		h[1] = 0x80 | byte(l)
	case l < 1<<16:
		h[1] = 0x80 | 126
		binary.BigEndian.PutUint16(h[2:], uint16(l))
		n = 4
	default:
		h[1] = 0x80 | 127
		binary.BigEndian.PutUint64(h[2:], uint64(l))
		n = 10
	}
	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return err
	}
	copy(h[n:], mask[:])
	n += 4
	buf := make([]byte, n+len(p))
	copy(buf, h[:n])
	for i, c := range p {
		buf[n+i] = c ^ mask[i%4]
	}
	_, err := w.c.Write(buf)
	return err
}

// wireMaxMessage bounds a message weaver reads: the server's own limit
// is 1 MiB (its welcome's max_message_bytes); more is no Rex.
const wireMaxMessage = 16 << 20

// message reads the next whole text message: fragments joined, pings
// answered, a close an error.
func (w *wire) message() ([]byte, error) {
	var msg bytes.Buffer
	for {
		var h [2]byte
		if _, err := io.ReadFull(w.r, h[:]); err != nil {
			return nil, err
		}
		fin, op := h[0]&0x80 != 0, h[0]&0x0f
		masked, l := h[1]&0x80 != 0, uint64(h[1]&0x7f)
		switch l {
		case 126:
			var x [2]byte
			if _, err := io.ReadFull(w.r, x[:]); err != nil {
				return nil, err
			}
			l = uint64(binary.BigEndian.Uint16(x[:]))
		case 127:
			var x [8]byte
			if _, err := io.ReadFull(w.r, x[:]); err != nil {
				return nil, err
			}
			l = binary.BigEndian.Uint64(x[:])
		}
		if l > wireMaxMessage || uint64(msg.Len())+l > wireMaxMessage {
			return nil, errors.New("rex: message too large")
		}
		var mask [4]byte
		if masked {
			if _, err := io.ReadFull(w.r, mask[:]); err != nil {
				return nil, err
			}
		}
		p := make([]byte, l)
		if _, err := io.ReadFull(w.r, p); err != nil {
			return nil, err
		}
		if masked {
			for i := range p {
				p[i] ^= mask[i%4]
			}
		}
		switch op {
		case 0x8:
			return nil, errors.New("rex: connection closed by the server")
		case 0x9:
			if err := w.frame(0xa, p); err != nil {
				return nil, err
			}
			continue
		case 0xa:
			continue
		}
		msg.Write(p)
		if fin {
			return msg.Bytes(), nil
		}
	}
}
