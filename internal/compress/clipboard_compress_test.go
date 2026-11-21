package compress

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/png"
	"strings"
	"testing"
)

func TestCompressClipboardPNGTargetSize(t *testing.T) {
	requireNativePNGTools(t)
	data := pngScreenshotFixture(t)
	original := bytes.Clone(data)
	options := Options{OutputFormat: OutputFormatJPEG, Threads: 1}
	baseline := compressClipboardFixture(t, data, options)
	fewerColors := options
	fewerColors.PNG = PNGOptions{MinQuality: 0, MaxQuality: 40, Colors: 64, Speed: 4}
	small := compressClipboardFixture(t, data, fewerColors)
	if got := pngColorCount(t, small.Data); got > fewerColors.PNG.Colors {
		t.Fatalf("PNG with a %d-color limit contains %d colors", fewerColors.PNG.Colors, got)
	}

	for _, test := range []struct {
		name   string
		target int64
		missed bool
	}{
		{"hit", targetSizeBetween(t, small.OutputSize, baseline.OutputSize), false},
		{"miss", MinTargetSizeBytes, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			withTarget := options
			withTarget.TargetSizeBytes = test.target
			result := compressClipboardFixture(t, data, withTarget)
			assertTargetSizeResult(t, result, test.target, test.missed, "png", image.Pt(320, 192))
			if result.Unchanged || result.TargetResized || result.OutputSize >= baseline.OutputSize {
				t.Fatalf("target search selected %d bytes, baseline %d, unchanged=%v, resized=%v",
					result.OutputSize, baseline.OutputSize, result.Unchanged, result.TargetResized)
			}
		})
	}
	if !bytes.Equal(data, original) {
		t.Fatal("compression mutated the clipboard snapshot")
	}
}

func TestCompressClipboardPNGTargetResize(t *testing.T) {
	requireNativePNGTools(t)
	data := insertPNGChunk(pngScreenshotFixture(t), "eXIf", exifTIFF(binary.LittleEndian, 6))
	options := Options{
		OutputFormat: OutputFormatAVIF, Metadata: MetadataStrip, Threads: 1,
		MaxDimension: 256, TargetSizeBytes: MinTargetSizeBytes,
	}
	baseline := compressClipboardFixture(t, data, options)
	assertTargetSizeResult(t, baseline, MinTargetSizeBytes, true, "png", image.Pt(256, 154))
	if baseline.TargetResized {
		t.Fatal("manual cap reported as target resizing")
	}

	options.TargetResizeMinDimension = 128
	result := compressClipboardFixture(t, data, options)
	if !result.TargetResized || result.Height < options.TargetResizeMinDimension || result.Height >= options.MaxDimension || result.OutputSize >= baseline.OutputSize {
		t.Fatalf("target resize selected %dx%d, %d bytes, resized=%v (baseline %d bytes)",
			result.Width, result.Height, result.OutputSize, result.TargetResized, baseline.OutputSize)
	}
	width, height, _ := resizedDimensions(192, 320, result.Height)
	assertTargetSizeResult(t, result, MinTargetSizeBytes, int64(len(result.Data)) > MinTargetSizeBytes, "png", image.Pt(width, height))
	if pngOrientation(result.Data) != 1 {
		t.Fatal("expected target resize to bake the EXIF orientation into the PNG raster")
	}
}

func TestCompressClipboardPNGLosslessTransparency(t *testing.T) {
	requireNativePNGTools(t)
	img := targetSizeNoise(128, 96)
	img.Pix[3] = 0
	img.Pix[7] = 128
	data := insertPNGChunk(pngFixture(t, img, png.NoCompression), "eXIf", exifTIFF(binary.LittleEndian, 6))
	result := compressClipboardFixture(t, data, Options{
		OutputFormat: OutputFormatWebP, Lossless: true, Metadata: MetadataStrip, Threads: 1,
		TargetSizeBytes: MinTargetSizeBytes, TargetResizeMinDimension: MinResizeDimension,
	})
	assertTargetSizeResult(t, result, MinTargetSizeBytes, true, "png", img.Bounds().Size())
	if result.Unchanged || result.TargetResized || result.OutputSize >= result.OriginalSize || pngHasChunk(result.Data, "eXIf") {
		t.Fatalf("lossless strip selected %d bytes from %d, unchanged=%v, resized=%v, EXIF=%v",
			result.OutputSize, result.OriginalSize, result.Unchanged, result.TargetResized, pngHasChunk(result.Data, "eXIf"))
	}
	decoded, err := png.Decode(bytes.NewReader(result.Data))
	if err != nil {
		t.Fatal(err)
	}
	assertImagesEqual(t, img, decoded)
}

func TestCompressClipboardPNGNoBenefit(t *testing.T) {
	requireNativePNGTools(t)
	data := pngFixture(t, image.NewGray(image.Rect(0, 0, 1, 1)), png.BestCompression)
	options := Options{Lossless: true, Threads: 1, TargetSizeBytes: MinTargetSizeBytes}
	optimized := compressClipboardFixture(t, data, options)
	result := compressClipboardFixture(t, optimized.Data, options)
	assertTargetSizeResult(t, result, MinTargetSizeBytes, false, "png", image.Pt(1, 1))
	if !result.Unchanged || !bytes.Equal(result.Data, optimized.Data) {
		t.Fatal("expected already optimized clipboard PNG to keep its original bytes")
	}
}

func TestCompressClipboardPNGInputGuards(t *testing.T) {
	data := pngFixture(t, image.NewGray(image.Rect(0, 0, 2, 3)), png.BestCompression)
	oversizedBytes := make([]byte, maxInputSize+1)
	copy(oversizedBytes, data)
	ihdr := bytes.Clone(data[16:29])
	binary.BigEndian.PutUint32(ihdr[:4], maxPixels)
	oversizedPixels := append(bytes.Clone(data[:8]), pngTestChunk("IHDR", ihdr)...)
	for _, test := range []struct {
		name string
		data []byte
		want string
	}{
		{"non-PNG", jpegFixture(t, 80), "静态 PNG"},
		{"animated PNG", insertPNGChunk(data, "acTL", make([]byte, 8)), "动画 PNG"},
		{"truncated PNG", data[:len(data)-1], "缺少 IEND"},
		{"oversized bytes", oversizedBytes, "64 MiB"},
		{"oversized dimensions", oversizedPixels, "像素"},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := CompressClipboardPNG(t.Context(), test.data, Options{})
			if result != nil || err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("result=%v, error=%v, want %q", result != nil, err, test.want)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if result, err := CompressClipboardPNG(ctx, data, Options{}); result != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("result=%v, error=%v, want cancellation", result != nil, err)
	}
}

func compressClipboardFixture(t *testing.T, data []byte, options Options) *Result {
	t.Helper()
	result, err := CompressClipboardPNG(t.Context(), data, options)
	if err != nil {
		t.Fatal(err)
	}
	if result.SourcePath != "" || result.sourcePath != "" || result.snapshot != nil {
		t.Fatal("clipboard result carries file overwrite state")
	}
	if result.OriginalSize != int64(len(data)) || result.OutputSize != int64(len(result.Data)) || result.OutputSize > maxInputSize {
		t.Fatalf("invalid clipboard byte counts: input=%d, original=%d, output=%d, data=%d",
			len(data), result.OriginalSize, result.OutputSize, len(result.Data))
	}
	return result
}
