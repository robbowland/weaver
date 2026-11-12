module github.com/robbowland/weaver

go 1.26.0

require golang.org/x/sys v0.49.0

require (
	github.com/charmbracelet/x/ansi v0.11.9
	github.com/yuin/goldmark v1.7.16
	golang.org/x/term v0.47.0
)

require (
	github.com/clipperhouse/displaywidth v0.11.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/lucasb-eyer/go-colorful v1.4.1 // indirect
	// v0.0.31 at least: earlier ones build their width tables as they
	// load, ~20 ms at the start of every hook (docs/decisions.md).
	github.com/mattn/go-runewidth v0.0.31 // indirect
)
