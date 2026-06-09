package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/charmbracelet/glamour"
	"github.com/mattn/go-isatty"
	"golang.org/x/term"
)

// isTTY is true when stdout is an interactive terminal.
var isTTY = isatty.IsTerminal(os.Stdout.Fd())

// renderMarkdown outputs md to stdout.
//   - TTY: rendered via glamour (ANSI colours, word-wrap).
//   - Piped/redirected: raw markdown — AI agents and scripts get clean structure.
//
// Returns 0 on success, 1 on error (which should not happen in practice).
func renderMarkdown(md string) int {
	if !isTTY {
		fmt.Print(md)
		return 0
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(termWidth()),
	)
	if err != nil {
		// Graceful fallback: raw output if glamour fails to initialise.
		fmt.Print(md)
		return 0
	}
	out, err := r.Render(md)
	if err != nil {
		fmt.Print(md)
		return 0
	}
	fmt.Print(out)
	return 0
}

// printJSON marshals v as indented JSON to stdout.
func printJSON(v any) int {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return die(err)
	}
	return 0
}

// termWidth returns the current terminal column width, falling back to 80.
func termWidth() int {
	w, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w <= 0 {
		return 80
	}
	return w
}

// die prints err to stderr and returns exit code 1.
func die(err error) int {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	return 1
}
