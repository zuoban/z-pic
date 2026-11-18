// Package toolio provides bounded I/O for the bundled native encoders.
package toolio

import "bytes"

const MaxDiagnosticBytes = 32 << 10

// Diagnostic retains a prefix while draining every write, so verbose stderr
// cannot block a child process or grow memory without bound. Do not embed a
// bytes.Buffer: its ReadFrom would let io.Copy bypass the limit in Write.
type Diagnostic struct {
	buffer bytes.Buffer
	limit  int
}

func NewDiagnostic(limit int) *Diagnostic {
	return &Diagnostic{limit: max(0, limit)}
}

func (w *Diagnostic) Write(data []byte) (int, error) {
	_, _ = w.buffer.Write(data[:min(len(data), w.limit-w.buffer.Len())])
	return len(data), nil
}

func (w *Diagnostic) String() string {
	return w.buffer.String()
}
