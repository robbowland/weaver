package wvr

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"

	"github.com/robbowland/weaver/internal/jsonx"
)

// kv is one member of a JSON object read in order.
type kv struct {
	k string
	v json.RawMessage
}

// oobj is a JSON object with its members in their order, each value as it
// was written: changed member by member and written back as it was.
type oobj []kv

func parseOobj(raw []byte) (oobj, bool) {
	d := json.NewDecoder(bytes.NewReader(raw))
	t, err := d.Token()
	if err != nil || t != json.Delim('{') {
		return nil, false
	}
	var o oobj
	for d.More() {
		k, err := d.Token()
		if err != nil {
			return nil, false
		}
		var v json.RawMessage
		if d.Decode(&v) != nil {
			return nil, false
		}
		o = append(o, kv{k.(string), v})
	}
	if _, err := d.Token(); err != nil {
		return nil, false
	}
	if _, err := d.Token(); err != io.EOF {
		return o, true
	}
	return o, true
}

func (o oobj) get(k string) (json.RawMessage, bool) {
	for _, m := range o {
		if m.k == k {
			return m.v, true
		}
	}
	return nil, false
}

// set replaces member k's value in place, or adds it at the end.
func (o oobj) set(k string, v string) oobj {
	for i := range o {
		if o[i].k == k {
			o[i].v = json.RawMessage(v)
			return o
		}
	}
	return append(o, kv{k, json.RawMessage(v)})
}

func (o oobj) String() string {
	var b strings.Builder
	b.WriteByte('{')
	for i, m := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(jsonx.Quote(m.k))
		b.WriteByte(':')
		var c bytes.Buffer
		if json.Compact(&c, m.v) == nil {
			b.Write(c.Bytes())
		} else {
			b.Write(m.v)
		}
	}
	b.WriteByte('}')
	return b.String()
}

// rawArray is a JSON array's elements as written; nil when raw is not one.
func rawArray(raw []byte) ([]json.RawMessage, bool) {
	var a []json.RawMessage
	if json.Unmarshal(raw, &a) != nil {
		return nil, false
	}
	return a, true
}

func joinRaw(a []json.RawMessage) string {
	parts := make([]string, len(a))
	for i, e := range a {
		var c bytes.Buffer
		if json.Compact(&c, e) == nil {
			parts[i] = c.String()
		} else {
			parts[i] = string(e)
		}
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// jsonStr is s as a JSON string, \ and " escaped, tab
// and newline by name, any other control character a space.
func jsonStr(s string) string {
	if !strings.ContainsAny(s, "\\\"") && strings.IndexFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f }) < 0 {
		return `"` + s + `"`
	}
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\':
			b.WriteString(`\\`)
		case c == '"':
			b.WriteString(`\"`)
		case c == '\t':
			b.WriteString(`\t`)
		case c == '\n':
			b.WriteString(`\n`)
		case c < 0x20 || c == 0x7f:
			b.WriteByte(' ')
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}
