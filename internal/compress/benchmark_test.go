package compress

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func benchmarkJPEGFile12MP(bench *testing.B) (string, int64) {
	bench.Helper()
	source := benchmarkSourceNRGBA(4000, 3000)
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, source, &jpeg.Options{Quality: 90}); err != nil {
		bench.Fatal(err)
	}
	path := filepath.Join(bench.TempDir(), "photo.jpg")
	if err := os.WriteFile(path, encoded.Bytes(), 0o600); err != nil {
		bench.Fatal(err)
	}
	return path, int64(encoded.Len())
}

func BenchmarkReadInputJPEG12MP(bench *testing.B) {
	path, size := benchmarkJPEGFile12MP(bench)
	ctx := context.Background()
	bench.SetBytes(size)
	bench.ReportAllocs()
	bench.ResetTimer()
	for iter := 0; iter < bench.N; iter++ {
		if _, err := readInput(ctx, path); err != nil {
			bench.Fatal(err)
		}
	}
}

func BenchmarkThumbnailFileJPEG12MP(bench *testing.B) {
	path, size := benchmarkJPEGFile12MP(bench)
	ctx := context.Background()
	bench.SetBytes(size)
	bench.ReportAllocs()
	bench.ResetTimer()
	for iter := 0; iter < bench.N; iter++ {
		if _, err := Thumbnail(ctx, path, 144); err != nil {
			bench.Fatal(err)
		}
	}
}

func BenchmarkValidatedPNGPreview12MP(bench *testing.B) {
	source := benchmarkSourceNRGBA(4000, 3000)
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		bench.Fatal(err)
	}
	data := encoded.Bytes()
	config := image.Config{Width: 4000, Height: 3000}
	ctx := context.Background()
	bench.Run("DecodeTwice", func(sub *testing.B) {
		sub.ReportAllocs()
		for iter := 0; iter < sub.N; iter++ {
			if err := validatePNGOutput(ctx, data, config); err != nil {
				sub.Fatal(err)
			}
			if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
				sub.Fatal(err)
			}
		}
	})
	bench.Run("ReuseDecoded", func(sub *testing.B) {
		sub.ReportAllocs()
		for iter := 0; iter < sub.N; iter++ {
			if _, err := decodePNGOutput(ctx, data, config); err != nil {
				sub.Fatal(err)
			}
		}
	})
}

func benchmarkSourceNRGBA(width, height int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for row := 0; row < height; row++ {
		for column := 0; column < width; column++ {
			img.SetNRGBA(column, row, color.NRGBA{
				R: uint8((column * 5) & 0xff),
				G: uint8((row * 7) & 0xff),
				B: uint8(((column + row) * 3) & 0xff),
				A: 255,
			})
		}
	}
	return img
}

func BenchmarkOrientedJPEGEncode(bench *testing.B) {
	source := benchmarkSourceNRGBA(800, 600)
	var rawJPEG bytes.Buffer
	if err := jpeg.Encode(&rawJPEG, source, &jpeg.Options{Quality: 90}); err != nil {
		bench.Fatal(err)
	}
	decodedYCbCr, err := jpeg.Decode(&rawJPEG)
	if err != nil {
		bench.Fatal(err)
	}
	options := &jpeg.Options{Quality: 80}

	bench.Run("PlanarYCbCrFastPath", func(sub *testing.B) {
		var output bytes.Buffer
		sub.ReportAllocs()
		sub.ResetTimer()
		for iter := 0; iter < sub.N; iter++ {
			output.Reset()
			oriented := orientForJPEGEncode(decodedYCbCr, 6)
			if err := jpeg.Encode(&output, oriented, options); err != nil {
				sub.Fatal(err)
			}
		}
	})

	bench.Run("LazyInterfaceFallback", func(sub *testing.B) {
		var output bytes.Buffer
		sub.ReportAllocs()
		sub.ResetTimer()
		for iter := 0; iter < sub.N; iter++ {
			output.Reset()
			oriented := normalizeOrientation(decodedYCbCr, 6)
			if err := jpeg.Encode(&output, oriented, options); err != nil {
				sub.Fatal(err)
			}
		}
	})
}

func BenchmarkThumbnailRaster(bench *testing.B) {
	source := benchmarkSourceNRGBA(1600, 1200)
	fallbackSource := plainColorImage{bounds: source.Bounds(), pixels: source}
	ctx := context.Background()

	bench.Run("RGBA64FastPath", func(sub *testing.B) {
		sub.ReportAllocs()
		sub.ResetTimer()
		for iter := 0; iter < sub.N; iter++ {
			if _, err := thumbnailRaster(ctx, source, 96); err != nil {
				sub.Fatal(err)
			}
		}
	})

	bench.Run("ColorInterfaceFallback", func(sub *testing.B) {
		sub.ReportAllocs()
		sub.ResetTimer()
		for iter := 0; iter < sub.N; iter++ {
			if _, err := thumbnailRaster(ctx, fallbackSource, 96); err != nil {
				sub.Fatal(err)
			}
		}
	})
}

func BenchmarkNativePNGCompressCachedBinary(bench *testing.B) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		bench.Skip("native assets bundled for darwin/arm64")
	}
	source := benchmarkSourceNRGBA(512, 512)
	var raw bytes.Buffer
	if err := png.Encode(&raw, source); err != nil {
		bench.Fatal(err)
	}
	payload := raw.Bytes()
	ctx := context.Background()
	options := Options{Quality: 80, PNG: DefaultPNGOptions()}
	if _, err := Bytes(ctx, payload, "bench.png", options); err != nil {
		bench.Fatal(err)
	}
	bench.ReportAllocs()
	bench.ResetTimer()
	for iter := 0; iter < bench.N; iter++ {
		if _, err := Bytes(ctx, payload, "bench.png", options); err != nil {
			bench.Fatal(err)
		}
	}
}
