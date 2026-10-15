package wv

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

// jqStr is s as a JSON string, escaped as jq's tojson escapes it: \" \\ and
// \b \f \n \r \t by name, other control characters and DEL as \u00XX, and
// everything else as it is (no HTML escaping).
func jqStr(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// canon is raw JSON as jq's tojson prints it: compact, keys in their order,
// numbers as written. "" when raw is not JSON.
func canon(raw []byte) string {
	if len(bytes.TrimSpace(raw)) == 0 {
		return ""
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var b strings.Builder
	if canonValue(d, &b) != nil {
		return ""
	}
	return b.String()
}

func canonValue(d *json.Decoder, b *strings.Builder) error {
	t, err := d.Token()
	if err != nil {
		return err
	}
	switch x := t.(type) {
	case json.Delim:
		switch x {
		case '{':
			b.WriteByte('{')
			first := true
			for d.More() {
				k, err := d.Token()
				if err != nil {
					return err
				}
				if !first {
					b.WriteByte(',')
				}
				first = false
				b.WriteString(jqStr(k.(string)))
				b.WriteByte(':')
				if err := canonValue(d, b); err != nil {
					return err
				}
			}
			if _, err := d.Token(); err != nil {
				return err
			}
			b.WriteByte('}')
		case '[':
			b.WriteByte('[')
			first := true
			for d.More() {
				if !first {
					b.WriteByte(',')
				}
				first = false
				if err := canonValue(d, b); err != nil {
					return err
				}
			}
			if _, err := d.Token(); err != nil {
				return err
			}
			b.WriteByte(']')
		}
	case string:
		b.WriteString(jqStr(x))
	case json.Number:
		b.WriteString(x.String())
	case bool:
		if x {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case nil:
		b.WriteString("null")
	}
	return nil
}

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
		b.WriteString(jqStr(m.k))
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

// one is jq's one($n): runs of white space and control characters as one
// space, trimmed, the first n characters.
func one(v any, n int) string {
	s := tostring(v)
	var b strings.Builder
	sp := false
	for _, r := range s {
		if unicode.IsSpace(r) || r < 0x20 || r == 0x7f {
			if !sp {
				b.WriteByte(' ')
			}
			sp = true
			continue
		}
		sp = false
		b.WriteRune(r)
	}
	t := strings.TrimPrefix(b.String(), " ")
	t = strings.TrimSuffix(t, " ")
	return runeSlice(t, n)
}

func runeSlice(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}

// line is jq's line: runs of control characters (and DEL) as one space.
func line(v any) string {
	s := tostring(v)
	var b strings.Builder
	c := false
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			if !c {
				b.WriteByte(' ')
			}
			c = true
			continue
		}
		c = false
		b.WriteRune(r)
	}
	return b.String()
}

// ctl is each control character but RS and US (and DEL) as a space, as the
// hook's parsed line was (gsub("[\u0000-\u001d\u007f]"; " ")).
func ctl(s string) string {
	if strings.IndexFunc(s, func(r rune) bool { return r < 0x1e || r == 0x7f }) < 0 {
		return s
	}
	return strings.Map(func(r rune) rune {
		if r < 0x1e || r == 0x7f {
			return ' '
		}
		return r
	}, s)
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
