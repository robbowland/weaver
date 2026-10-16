package wvr

import "strings"

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
