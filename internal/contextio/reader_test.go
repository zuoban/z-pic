package contextio

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestCopyCannotBypassCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// strings.Reader implements WriterTo, which must not be exposed by the
	// wrapper or io.Copy would copy everything without calling Read.
	var output bytes.Buffer
	n, err := io.Copy(&output, NewReader(ctx, strings.NewReader("private")))
	if n != 0 || output.Len() != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled copy wrote %d bytes: %q, %v", n, output.String(), err)
	}
}
