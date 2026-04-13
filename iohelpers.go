package main

import (
	"encoding/json"
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

func writeJSONValue(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}
