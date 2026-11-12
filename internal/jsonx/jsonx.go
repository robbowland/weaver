// Package jsonx reads the JSON weaver keeps and is given leniently: a
// member of an unexpected type reads as its zero value, a number may come
// as a string, any value may stand for text.
package jsonx

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

// Int is a number that older files may hold as a string ("12"), or
// not at all; anything else ("terminal_3") reads as 0.
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

// Text is a member that is text, or another JSON value as its JSON,
// compact; null or false is none.
type Text string

func (t *Text) UnmarshalJSON(b []byte) error {
	var s string
	switch v := strings.TrimSpace(string(b)); {
	case json.Unmarshal(b, &s) == nil:
		*t = Text(s)
	case v == "null" || v == "false":
		*t = ""
	default:
		// b is JSON: the decoder that gave it read it.
		var c bytes.Buffer
		_ = json.Compact(&c, b)
		*t = Text(c.String())
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

// NumberOr is n as a float, or d when it is not a number.
func NumberOr(n json.Number, d float64) float64 {
	if f, err := n.Float64(); err == nil {
		return f
	}
	return d
}
