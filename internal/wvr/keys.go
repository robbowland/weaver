package wvr

import "strings"

// Keys are written one way wherever weaver shows them: lowercase modifiers
// joined with +, then the key as typed, so a capital is Shift's (alt+r,
// alt+R, ctrl+q, shift+tab; ⏎, esc and tab as they are, alt+⏎ too). fzf
// names the same keys alt-r, ctrl-q, btab and alt-enter.

// keyLabel is fzf's key k as weaver writes it.
func keyLabel(k string) string {
	switch {
	case k == "btab":
		return "shift+tab"
	case strings.HasPrefix(k, "alt-"), strings.HasPrefix(k, "ctrl-"):
		m, key, _ := strings.Cut(k, "-")
		if key == "enter" {
			key = "⏎"
		}
		return m + "+" + key
	}
	return k
}

// fzfKey is fzf's name for the key weaver writes as label l.
func fzfKey(l string) string {
	switch {
	case l == "⏎":
		return "enter"
	case l == "shift+tab":
		return "btab"
	case strings.HasPrefix(l, "alt+"), strings.HasPrefix(l, "ctrl+"):
		m, key, _ := strings.Cut(l, "+")
		if key == "⏎" {
			key = "enter"
		}
		return m + "-" + key
	}
	return l
}
