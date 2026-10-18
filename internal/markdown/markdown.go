// Package markdown renders markdown (a pull request's description, a Linear
// issue's) for a terminal card: wrapped to a width, in the colours it is
// given, its structure kept (headings, lists and task lists, quotes and
// GitHub's alerts, code, tables) and its noise left out (HTML, link
// targets). Parsed by goldmark, with GitHub's extensions.
//
// Set as Micrographics sets a note (the vault's theme), in a terminal's
// terms: a section (h2) under a faint rule with its index (01) before it in
// capitals; links and images as [ SRC: text ] and [ IMG: alt ]; code in
// crop corners with its language at the top right; an alert under an ink
// stripe with its kind as a [ LABEL ]; task boxes as weaver's cells; tables
// with a faint capitals header and no rules between columns.
//
// Not glamour: it renders more, but its highlighter registers every lexer
// and style as it loads, and weaver loads on every agent hook.
package markdown

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

// Style is the colours text is set in, as SGR sequences: Text for prose,
// Strong for emphasis, headings and code, Faint for what frames it (bullets,
// quotes' bars, rules), Good for a ticked task, Bad for an alert to heed;
// Off ends any.
type Style struct {
	Text, Strong, Faint, Good, Bad, Off string
}

const (
	bold   = "\x1b[1m"
	italic = "\x1b[3m"
	struck = "\x1b[9m"
	// nbsp joins a link's brackets to its words, so a line never breaks
	// inside them.
	nbsp = "\u00a0"
)

var md = goldmark.New(goldmark.WithExtensions(extension.Table, extension.Strikethrough, extension.TaskList, extension.Linkify))

// Render is src rendered to width columns.
func Render(src string, width int, st Style) string {
	b := []byte(strings.ReplaceAll(src, "\r", ""))
	r := renderer{src: b, st: st, h2: new(int)}
	ls := r.blocks(md.Parser().Parse(text.NewReader(b)), max(width, 10))
	return strings.Join(ls, "\n")
}

type renderer struct {
	src []byte
	st  Style
	h2  *int // the sections so far, for their index
}

// span is a run of inline text in one style.
type span struct {
	text, style string
	brk         bool // a hard line break
}

// blocks renders n's children, a blank line between each (none in a tight
// list).
func (r *renderer) blocks(n ast.Node, w int) []string {
	var out []string
	tight := false
	if l, ok := n.(*ast.ListItem); ok {
		if p, ok := l.Parent().(*ast.List); ok {
			tight = p.IsTight
		}
	}
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		ls := r.block(c, w)
		if len(ls) == 0 {
			continue
		}
		if len(out) > 0 && !tight {
			out = append(out, "")
		}
		out = append(out, ls...)
	}
	return out
}

func (r *renderer) block(n ast.Node, w int) []string {
	st := r.st
	switch n := n.(type) {
	case *ast.Paragraph, *ast.TextBlock:
		return wrap(r.inline(n, st.Text), w, st)
	case *ast.Heading:
		return r.heading(n, w)
	case *ast.List:
		return r.list(n, w)
	case *ast.Blockquote:
		if kind, ok := alertKind(n, r.src); ok {
			return r.alert(n, kind, w)
		}
		// Its own voice: a rail, and italics.
		q := renderer{src: r.src, h2: r.h2, st: Style{Text: italic + st.Text, Strong: italic + st.Strong, Faint: st.Faint, Good: st.Good, Bad: st.Bad, Off: st.Off}}
		var out []string
		for _, l := range q.blocks(n, w-2) {
			out = append(out, st.Text+"│"+st.Off+" "+l)
		}
		return out
	case *ast.FencedCodeBlock, *ast.CodeBlock:
		return r.code(n, w)
	case *ast.ThematicBreak:
		return []string{st.Faint + strings.Repeat("─", w) + st.Off}
	case *ast.HTMLBlock:
		// Its text, without its tags: a <details>'s summary, say.
		var b strings.Builder
		lines := n.Lines()
		for i := 0; i < lines.Len(); i++ {
			s := lines.At(i)
			b.Write(s.Value(r.src))
		}
		t := strings.Join(strings.Fields(reTag.ReplaceAllString(b.String(), " ")), " ")
		if t == "" {
			return nil
		}
		return wrap([]span{{text: t, style: st.Faint}}, w, st)
	case *east.Table:
		return r.table(n, w)
	}
	return r.blocks(n, w)
}

var reTag = regexp.MustCompile(`<[^>]*>`)

// heading is a heading as Micrographics sets one: a section (h2) under a
// faint rule, its index (01, 02 ...) before it, in bold capitals; the title
// (h1) in bold capitals; h3 in bold; h4 to h6 as grey capitals, labels.
func (r *renderer) heading(n *ast.Heading, w int) []string {
	st := r.st
	style, upper := bold+st.Strong, true
	switch n.Level {
	case 3:
		upper = false
	case 4, 5, 6:
		style = st.Text
	}
	sp := r.inline(n, style)
	for i := range sp {
		if upper {
			sp[i].text = strings.ToUpper(sp[i].text)
		}
		sp[i].style = style
	}
	if n.Level != 2 {
		return wrap(sp, w, st)
	}
	*r.h2++
	idx := fmt.Sprintf("%02d", *r.h2)
	out := []string{st.Faint + strings.Repeat("─", w) + st.Off}
	for i, l := range wrap(sp, w-len(idx)-2, st) {
		if i == 0 {
			out = append(out, st.Faint+idx+st.Off+"  "+l)
		} else {
			out = append(out, strings.Repeat(" ", len(idx)+2)+l)
		}
	}
	return out
}

// code is a code block in crop corners, its language at the top right.
func (r *renderer) code(n ast.Node, w int) []string {
	st := r.st
	lang := ""
	if f, ok := n.(*ast.FencedCodeBlock); ok {
		lang = strings.ToUpper(string(f.Language(r.src)))
	}
	top := st.Faint + "┌" + st.Off
	if lang != "" && utf8.RuneCountInString(lang)+3 <= w {
		top += strings.Repeat(" ", w-1-utf8.RuneCountInString(lang)) + st.Faint + lang + st.Off
	}
	out := []string{top}
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		s := lines.At(i)
		l := strings.TrimRight(strings.ReplaceAll(string(s.Value(r.src)), "\t", "  "), "\n")
		out = append(out, "  "+st.Strong+cut(l, w-4)+st.Off)
	}
	return append(out, strings.Repeat(" ", max(w-1, 0))+st.Faint+"┘"+st.Off)
}

var reAlert = regexp.MustCompile(`^\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]`)

// alertKind is a GitHub alert's kind (> [!NOTE] ...), if the quote is one.
func alertKind(q *ast.Blockquote, src []byte) (string, bool) {
	p := q.FirstChild()
	if p == nil || p.Lines().Len() == 0 {
		return "", false
	}
	l := p.Lines().At(0)
	if m := reAlert.FindStringSubmatch(strings.TrimSpace(string(l.Value(src)))); m != nil {
		return m[1], true
	}
	return "", false
}

// alert is a GitHub alert as a Micrographics callout: an ink stripe over
// it, its kind as a label in brackets (one to heed in red), its text.
func (r *renderer) alert(q *ast.Blockquote, kind string, w int) []string {
	st := r.st
	label := st.Faint
	if kind == "WARNING" || kind == "CAUTION" || kind == "IMPORTANT" {
		label = st.Bad
	}
	out := []string{st.Strong + strings.Repeat("━", w) + st.Off, label + "[ " + kind + " ]" + st.Off}
	first := true
	for c := q.FirstChild(); c != nil; c = c.NextSibling() {
		var ls []string
		if first {
			// The marker goes; what follows it on its line is the text.
			ls = wrap(dropMarker(r.inline(c, st.Text), "[!"+kind+"]"), w, st)
			first = false
		} else {
			ls = r.block(c, w)
		}
		if len(ls) > 0 && len(out) > 2 {
			out = append(out, "")
		}
		out = append(out, ls...)
	}
	return out
}

// dropMarker is sp without its first len(marker) characters, and the break
// or spaces after them.
func dropMarker(sp []span, marker string) []span {
	n := utf8.RuneCountInString(marker)
	for len(sp) > 0 && n > 0 {
		rs := []rune(sp[0].text)
		if len(rs) <= n {
			n -= len(rs)
			sp = sp[1:]
			continue
		}
		sp[0].text = string(rs[n:])
		n = 0
	}
	for len(sp) > 0 && (sp[0].brk || strings.TrimSpace(sp[0].text) == "") {
		sp = sp[1:]
	}
	if len(sp) > 0 {
		sp[0].text = strings.TrimLeft(sp[0].text, " ")
	}
	return sp
}

// list is a list's items, each under its marker: a bullet, a number, or a
// task's box.
func (r *renderer) list(n *ast.List, w int) []string {
	st := r.st
	var out []string
	num := n.Start
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		mark := st.Faint + "·" + st.Off + " "
		if n.IsOrdered() {
			mark = st.Faint + strconv.Itoa(num) + "." + st.Off + " "
			num++
		}
		// A task's box is a cell, as weaver's states are: [x] done.
		if box := taskBox(c); box != nil {
			mark = st.Faint + "[ ]" + st.Off + " "
			if box.IsChecked {
				mark = st.Faint + "[" + st.Off + st.Good + "x" + st.Off + st.Faint + "]" + st.Off + " "
			}
		}
		mw := visible(mark)
		ls := r.blocks(c, w-mw)
		if len(ls) == 0 {
			ls = []string{""}
		}
		if len(out) > 0 && !n.IsTight {
			out = append(out, "")
		}
		for i, l := range ls {
			if i == 0 {
				out = append(out, mark+l)
			} else {
				out = append(out, strings.Repeat(" ", mw)+l)
			}
		}
	}
	return out
}

// taskBox is a task list item's box, if it is one.
func taskBox(item ast.Node) *east.TaskCheckBox {
	if p := item.FirstChild(); p != nil {
		if b, ok := p.FirstChild().(*east.TaskCheckBox); ok {
			return b
		}
	}
	return nil
}

// table is a table, its columns as wide as their widest cell, or narrowed
// to fit, apart by space alone; its header in faint capitals over a rule.
func (r *renderer) table(n *east.Table, w int) []string {
	st := r.st
	var rows [][]string
	head := 0
	for row := n.FirstChild(); row != nil; row = row.NextSibling() {
		var cells []string
		for c := row.FirstChild(); c != nil; c = c.NextSibling() {
			var b strings.Builder
			for _, s := range r.inline(c, "") {
				b.WriteString(s.text)
			}
			cells = append(cells, strings.TrimSpace(b.String()))
		}
		if _, ok := row.(*east.TableHeader); ok {
			head++
		}
		rows = append(rows, cells)
	}
	if len(rows) == 0 {
		return nil
	}
	cols := 0
	for _, cs := range rows {
		cols = max(cols, len(cs))
	}
	ws := make([]int, cols)
	for _, cs := range rows {
		for i, c := range cs {
			ws[i] = max(ws[i], utf8.RuneCountInString(c))
		}
	}
	// Narrowed, widest first, till the row fits (" │ " between columns).
	for total(ws)+3*(cols-1) > w {
		widest := 0
		for i := range ws {
			if ws[i] > ws[widest] {
				widest = i
			}
		}
		if ws[widest] <= 3 {
			break
		}
		ws[widest]--
	}
	var out []string
	for i, cs := range rows {
		var parts []string
		for j := range cols {
			c := ""
			if j < len(cs) {
				c = cs[j]
			}
			c = cut(c, ws[j])
			c += strings.Repeat(" ", ws[j]-utf8.RuneCountInString(c))
			if i < head {
				c = st.Faint + strings.ToUpper(c) + st.Off
			} else {
				c = st.Text + c + st.Off
			}
			parts = append(parts, c)
		}
		out = append(out, strings.TrimRight(strings.Join(parts, "   "), " "))
		if i == head-1 {
			out = append(out, st.Faint+strings.Repeat("─", total(ws)+3*(cols-1))+st.Off)
		}
	}
	return out
}

func total(ws []int) int {
	t := 0
	for _, w := range ws {
		t += w
	}
	return t
}

// inline is n's inline content as spans, in style unless it says
// otherwise.
func (r *renderer) inline(n ast.Node, style string) []span {
	st := r.st
	var out []span
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch c := c.(type) {
		case *ast.Text:
			out = append(out, span{text: string(c.Segment.Value(r.src)), style: style})
			switch {
			case c.HardLineBreak():
				out = append(out, span{brk: true})
			case c.SoftLineBreak():
				out = append(out, span{text: " ", style: style})
			}
		case *ast.String:
			out = append(out, span{text: string(c.Value), style: style})
		case *ast.CodeSpan:
			var b strings.Builder
			for t := c.FirstChild(); t != nil; t = t.NextSibling() {
				if s, ok := t.(*ast.Text); ok {
					b.Write(s.Segment.Value(r.src))
				}
			}
			out = append(out, span{text: b.String(), style: st.Strong})
		case *ast.Emphasis:
			s := italic + style
			if c.Level >= 2 {
				s = bold + st.Strong
			}
			out = append(out, r.inline(c, s)...)
		case *east.Strikethrough:
			out = append(out, r.inline(c, struck+style)...)
		case *ast.Link:
			// [ SRC: its text ]
			out = append(out, span{text: "[" + nbsp + "SRC:" + nbsp, style: st.Faint})
			out = append(out, r.inline(c, st.Strong)...)
			out = append(out, span{text: nbsp + "]", style: st.Faint})
		case *ast.AutoLink:
			u := strings.TrimPrefix(strings.TrimPrefix(string(c.Label(r.src)), "https://"), "http://")
			out = append(out, span{text: "[" + nbsp + "SRC:" + nbsp, style: st.Faint}, span{text: cut(u, 48), style: st.Text},
				span{text: nbsp + "]", style: st.Faint})
		case *ast.Image:
			alt := "image"
			if t := r.inline(c, ""); len(t) > 0 && strings.TrimSpace(t[0].text) != "" {
				alt = t[0].text
			}
			out = append(out, span{text: "[" + nbsp + "IMG:" + nbsp, style: st.Faint}, span{text: alt, style: st.Text},
				span{text: nbsp + "]", style: st.Faint})
		case *ast.RawHTML:
			var b strings.Builder
			for i := 0; i < c.Segments.Len(); i++ {
				s := c.Segments.At(i)
				b.Write(s.Value(r.src))
			}
			if strings.HasPrefix(strings.ToLower(b.String()), "<br") {
				out = append(out, span{brk: true})
			}
		case *east.TaskCheckBox:
			// The list's marker shows it.
		default:
			out = append(out, r.inline(c, style)...)
		}
	}
	return out
}

// part is a word's text in one style (a word may change style midway:
// **bold**ing).
type part struct{ style, text string }

// wrap is spans set in lines of w columns, each line in the style's text
// colour, each span's style where it is.
func wrap(sp []span, w int, st Style) []string {
	var lines []string
	var line strings.Builder
	n := 0
	var pend []part // the word being built
	pendN := 0
	space := false
	flush := func() {
		lines = append(lines, strings.TrimRight(line.String(), " "))
		line.Reset()
		n = 0
	}
	put := func() {
		if pendN == 0 {
			return
		}
		need := pendN
		if n > 0 && space {
			need++
		}
		if n > 0 && n+need > w {
			flush()
		}
		if n > 0 && space {
			line.WriteString(" ")
			n++
		}
		for _, p := range pend {
			rs := []rune(p.text)
			// A word longer than the line, cut across lines.
			for n+len(rs) > w {
				if n >= w {
					flush()
					continue
				}
				k := w - n
				line.WriteString(p.style + string(rs[:k]) + st.Off)
				n += k
				rs = rs[k:]
				flush()
			}
			line.WriteString(p.style + string(rs) + st.Off)
			n += len(rs)
		}
		pend, pendN, space = nil, 0, false
	}
	for _, s := range sp {
		if s.brk {
			put()
			flush()
			continue
		}
		style := s.style
		if style == "" {
			style = st.Text
		}
		for i, f := range strings.Split(s.text, " ") {
			if i > 0 {
				put()
				space = true
			}
			if f == "" {
				continue
			}
			pend = append(pend, part{style, f})
			pendN += utf8.RuneCountInString(f)
		}
	}
	put()
	if n > 0 || len(lines) == 0 {
		flush()
	}
	// Trailing empties (a break at the end) go.
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

var reSGR = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func visible(s string) int { return utf8.RuneCountInString(reSGR.ReplaceAllString(s, "")) }

// cut is s at most n runes, an ellipsis ending one cut short.
func cut(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}
