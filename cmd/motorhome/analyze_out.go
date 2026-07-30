package main

// Output sink for the analyze subcommand.
//
// Every table the analyze command prints goes through aprintf/aprintln/aprint
// rather than fmt.Print* directly, so the whole rendering stage can be pointed
// somewhere else: at io.Discard for -json (which emits a machine-readable
// document on stdout instead of tables), or at a buffer in tests.
//
// Warnings and errors deliberately do NOT go through this sink — they are
// written straight to stderr, so they still reach the user in -json mode
// without corrupting the JSON document on stdout.

import (
	"fmt"
	"io"
	"os"
)

// analyzeOut is where the analyze subcommand's tables are written.
//
// It is bound to os.Stdout at the start of RunAnalyze rather than here: main.go
// swaps os.Stdout for a pipe before calling RunAnalyze so the output can be
// copied to the clipboard, and a package-level initialiser would capture the
// pre-swap descriptor and bypass that.
var analyzeOut io.Writer = os.Stdout

func aprintf(format string, a ...any) { fmt.Fprintf(analyzeOut, format, a...) }
func aprintln(a ...any)               { fmt.Fprintln(analyzeOut, a...) }
func aprint(a ...any)                 { fmt.Fprint(analyzeOut, a...) }
