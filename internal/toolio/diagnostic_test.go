package toolio

import (
	"bytes"
	"io"
	"testing"
)

func TestDiagnosticBoundsRetainedPrefixAndDrainsStream(t *testing.T) {
	d := NewDiagnostic(10)
	chunks := []string{"hello", " ", "world", " - trailing verbose stderr"}
	total := 0
	for _, chunk := range chunks {
		n, err := d.Write([]byte(chunk))
		if err != nil || n != len(chunk) {
			t.Fatalf("Write(%q) = %d, %v", chunk, n, err)
		}
		total += n
	}
	if got := d.String(); got != "hello worl" {
		t.Fatalf("String() = %q, want %q", got, "hello worl")
	}

	// Ensure io.Copy does not bypass Write via an embedded ReadFrom.
	zeroLimit := NewDiagnostic(-5)
	copied, err := io.Copy(zeroLimit, bytes.NewReader(bytes.Repeat([]byte("a"), 4096)))
	if err != nil || copied != 4096 || zeroLimit.String() != "" {
		t.Fatalf("zero-limit copy = (%d, %v, %q)", copied, err, zeroLimit.String())
	}
}

func TestBoundedBufferClampsCapacityAtMaxCap(t *testing.T) {
	var buf BoundedBuffer
	maxCap := 5000
	chunk := bytes.Repeat([]byte("a"), 2000)
	buf.AppendBounded([]byte("pre:"), chunk, maxCap)
	buf.AppendBounded(nil, chunk, maxCap)
	if buf.Len() != 4004 || buf.Cap() > maxCap {
		t.Fatalf("Len=%d Cap=%d exceeded maxCap=%d", buf.Len(), buf.Cap(), maxCap)
	}
	if !bytes.HasPrefix(buf.Bytes(), []byte("pre:aaaa")) || len(buf.String()) != 4004 {
		t.Fatal("BoundedBuffer corrupted retained bytes")
	}
	buf.Reset()
	if buf.Len() != 0 || buf.Cap() != 0 {
		t.Fatalf("Reset kept storage: Len=%d Cap=%d", buf.Len(), buf.Cap())
	}
}
