// Package contextio provides cooperative cancellation for synchronous reads.
package contextio

import (
	"context"
	"io"
)

// NewReader checks ctx before each read. It cannot interrupt a Read that has
// already blocked; network callers must also use request contexts/deadlines.
// Keep the source unembedded so io.Copy cannot bypass checks via WriterTo.
func NewReader(ctx context.Context, source io.Reader) io.Reader {
	return reader{ctx: ctx, source: source}
}

type reader struct {
	ctx    context.Context
	source io.Reader
}

func (r reader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.source.Read(buffer)
}
