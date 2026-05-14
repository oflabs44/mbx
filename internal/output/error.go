package output

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Code is part of the stable JSON contract; Message is human-facing and may
// evolve between releases.
type Failure struct {
	Code    Code           `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

func (f *Failure) Error() string { return f.Message }

func Errorf(code Code, msg string, args ...any) *Failure {
	return &Failure{Code: code, Message: fmt.Sprintf(msg, args...)}
}

func (f *Failure) WithDetails(k string, v any) *Failure {
	c := *f
	if c.Details == nil {
		c.Details = map[string]any{}
	}
	c.Details[k] = v
	return &c
}

type errorEnvelope struct {
	V     int     `json:"v"`
	Error Failure `json:"error"`
}

func (w *Writer) Failure(f *Failure) error {
	env := errorEnvelope{V: Version, Error: *f}
	enc := json.NewEncoder(w.stderr)
	enc.SetEscapeHTML(false)
	return enc.Encode(env)
}

func AsFailure(err error) *Failure {
	var f *Failure
	if errors.As(err, &f) {
		return f
	}
	return &Failure{Code: CodeGeneric, Message: err.Error()}
}
