package imageinput

import (
	"bytes"
	"context"
	"testing"
)

func BenchmarkReadSizedGrowing6MiB(b *testing.B) {
	data := make([]byte, 6<<20)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		got, err := readSized(context.Background(), bytes.NewReader(data), 512)
		if err != nil || !bytes.Equal(got, data) {
			b.Fatalf("read growing input: length=%d, error=%v", len(got), err)
		}
	}
}
