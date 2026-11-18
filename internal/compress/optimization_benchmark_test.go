package compress

import (
	"bytes"
	"context"
	"image"
	"testing"
)

func BenchmarkThumbnailUnscaled(b *testing.B) {
	source := benchmarkSourceNRGBA(144, 96)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := thumbnailRaster(context.Background(), source, 144); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkUnchangedSnapshot6MiB(b *testing.B) {
	data := bytes.Repeat([]byte{0x37}, 6<<20)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		snapshot := captureOverwriteSnapshot(data, data)
		if snapshot.inputHash != snapshot.outputHash {
			b.Fatal("unchanged snapshot hashes differ")
		}
	}
}

func BenchmarkThumbnailScaledControl(b *testing.B) {
	source := image.NewRGBA(image.Rect(0, 0, 1600, 1200))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := thumbnailRaster(context.Background(), source, 144); err != nil {
			b.Fatal(err)
		}
	}
}
