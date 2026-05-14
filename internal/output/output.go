package output

import (
	"encoding/json"
	"fmt"
	"io"
)

// Bump only on breaking changes to the envelope shape; additive fields under
// data/meta/error.details are backward-compatible.
const Version = 1

type Format int

const (
	FormatJSON Format = iota
	FormatTable
)

type Writer struct {
	stdout io.Writer
	stderr io.Writer
	format Format
}

func NewWriter(stdout, stderr io.Writer, format Format) *Writer {
	return &Writer{stdout: stdout, stderr: stderr, format: format}
}

type successEnvelope struct {
	V    int `json:"v"`
	Data any `json:"data,omitempty"`
	Meta any `json:"meta,omitempty"`
}

func (w *Writer) Success(data, meta any) error {
	if w.format == FormatTable {
		return fmt.Errorf("table output not implemented")
	}

	env := successEnvelope{V: Version, Data: data, Meta: meta}
	enc := json.NewEncoder(w.stdout)
	enc.SetEscapeHTML(false)
	return enc.Encode(env)
}
