// Package jsonx reads the JSON weaver keeps and is given leniently, and
// writes it as jq did: a member of an unexpected type reads as its zero
// value, a number may come as a string, any value may stand for text.
package jsonx

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Int is a number that older files may hold as a string ("12"), or
// not at all; anything else (a zellij pane's "terminal_3") reads as 0.
type Int int

func (n *Int) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*n = 0
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		f = 0
	}
	*n = Int(f)
	return nil
}

// Text is a member that is text, or another JSON value as its JSON
// (jq's tostring); null or false is none.
type Text string

func (t *Text) UnmarshalJSON(b []byte) error {
	var s string
	switch v := strings.TrimSpace(string(b)); {
	case json.Unmarshal(b, &s) == nil:
		*t = Text(s)
	case v == "null" || v == "false":
		*t = ""
	default:
		*t = Text(Canon(b))
	}
	return nil
}

// Decode decodes the JSON object b into v. A member of another type
// than v gives it (an older or newer weaver's) reads as its zero value, and
// the rest still reads; b not an object is an error.
func Decode(b []byte, v any) error {
	err := json.Unmarshal(b, v)
	var te *json.UnmarshalTypeError
	if errors.As(err, &te) && te.Field != "" {
		return nil
	}
	return err
}

// First is the first JSON value of a file (jq --slurpfile's [0]).
func First(b []byte) []byte {
	d := json.NewDecoder(bytes.NewReader(b))
	var raw json.RawMessage
	if d.Decode(&raw) != nil {
		return nil
	}
	return raw
}

// Canon is raw JSON as jq's tojson prints it: compact, keys in their order,
// numbers as written. "" when raw is not JSON.
func Canon(raw []byte) string {
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
				b.WriteString(Quote(k.(string)))
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
		b.WriteString(Quote(x))
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

// Quote is s as a JSON string, escaped as jq's tojson escapes it: \" \\ and
// \b \f \n \r \t by name, other control characters and DEL as \u00XX, and
// everything else as it is (no HTML escaping).
func Quote(s string) string {
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

// NumberOr is n as a float, or d when it is not a number.
func NumberOr(n json.Number, d float64) float64 {
	if f, err := n.Float64(); err == nil {
		return f
	}
	return d
}
