package main

import (
	"fmt"
	"io"
)

func writeLine(w io.Writer, args ...any) {
	_, _ = fmt.Fprintln(w, args...)
}

func writef(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

func closeQuietly(c io.Closer) {
	if c != nil {
		_ = c.Close()
	}
}
