package compress

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

// Cancel on a read checkpoint rather than relying on CPU speed or timers.
type cancelAtCheckpoint struct {
	context.Context
	cancel context.CancelFunc
	checks int
	limit  int
}

func (ctx *cancelAtCheckpoint) Err() error {
	ctx.checks++
	if ctx.checks >= ctx.limit {
		ctx.cancel()
	}
	return ctx.Context.Err()
}

func TestJPEGDecodeObservesCancellationDuringReads(t *testing.T) {
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, benchmarkSourceNRGBA(1024, 768), &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &cancelAtCheckpoint{Context: base, cancel: cancel, limit: 12}
	result, err := Bytes(ctx, encoded.Bytes(), "photo.jpg", Options{Quality: 80})
	if result != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("decode ignored cancellation: result=%v, error=%v, checks=%d", result != nil, err, ctx.checks)
	}
}

func TestJPEGOrientationObservesCancellationDuringConversion(t *testing.T) {
	bounds := image.Rect(0, 0, 96, 80)
	for _, source := range []image.Image{
		image.NewYCbCr(bounds, image.YCbCrSubsampleRatio420),
		image.NewYCbCr(bounds, image.YCbCrSubsampleRatio444),
		image.NewYCbCr(bounds, image.YCbCrSubsampleRatio422),
		image.NewGray(bounds),
		image.NewNRGBA(bounds),
	} {
		for orientation := 2; orientation <= 8; orientation++ {
			base, cancel := context.WithCancel(context.Background())
			ctx := &cancelAtCheckpoint{Context: base, cancel: cancel, limit: 3}
			output, err := orientForJPEGEncode(ctx, source, orientation)
			cancel()
			if output != nil || !errors.Is(err, context.Canceled) || ctx.checks != 3 {
				t.Fatalf("%T orientation %d: canceled conversion returned %T, %v after %d checks",
					source, orientation, output, err, ctx.checks)
			}
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if output, err := orientForJPEGEncode(ctx, image.NewGray(bounds), 1); output != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("already canceled orientation returned %T, %v", output, err)
	}
}

func TestOrientedPlaneStopsBeforeCopyCompletes(t *testing.T) {
	source := image.NewGray(image.Rect(0, 0, 96, 80))
	for index := range source.Pix {
		source.Pix[index] = 255
	}
	for _, orientation := range []int{2, 6} {
		oriented := normalizeOrientation(source, orientation).(orientedImage)
		output := image.NewGray(oriented.Bounds())
		base, cancel := context.WithCancel(context.Background())
		ctx := &cancelAtCheckpoint{Context: base, cancel: cancel, limit: 2}
		err := copyOrientedPlane(ctx, output.Pix, output.Stride, source.Pix, source.Stride, oriented, 0)
		cancel()
		copied := bytes.Count(output.Pix, []byte{255})
		if !errors.Is(err, context.Canceled) || copied == 0 || copied == len(output.Pix) {
			t.Fatalf("orientation %d copied %d/%d pixels before %v", orientation, copied, len(output.Pix), err)
		}
	}
}

func TestJPEGHeadersObserveCancellationDuringReads(t *testing.T) {
	original := jpegFixture(t, 90)
	data := bytes.Clone(original[:2])
	// Valid APP2 segments before SOF force multiple reads while finding config.
	for range 8 {
		data = append(data, 0xff, 0xe2, 0xff, 0xff)
		data = append(data, make([]byte, 65533)...)
	}
	data = append(data, original[2:]...)
	for _, convert := range []struct {
		name string
		run  func(context.Context) error
	}{
		{"compression", func(ctx context.Context) error {
			_, err := Bytes(ctx, data, "photo.jpg", Options{Quality: 80})
			return err
		}},
		{"clipboard", func(ctx context.Context) error {
			_, err := PNGBytes(ctx, data)
			return err
		}},
	} {
		t.Run(convert.name, func(t *testing.T) {
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx := &cancelAtCheckpoint{Context: base, cancel: cancel, limit: 12}
			if err := convert.run(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("header read ignored cancellation: %v, checks=%d", err, ctx.checks)
			}
		})
	}
}

type clipboardCancelImage struct {
	image.Image
	cancel context.CancelFunc
	reads  int
}

func (source *clipboardCancelImage) ColorModel() color.Model {
	source.cancel()
	return source.Image.ColorModel()
}

func (source *clipboardCancelImage) Opaque() bool { return true }

func (source *clipboardCancelImage) At(x, y int) color.Color {
	source.reads++
	return source.Image.At(x, y)
}

func TestClipboardPNGStopsWritingWhenEncodingCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := &clipboardCancelImage{Image: benchmarkSourceNRGBA(64, 48), cancel: cancel}
	data, err := encodeClipboardPNG(ctx, source, maxInputSize)
	if data != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled encode returned %d bytes, %v", len(data), err)
	}
	if source.reads != 0 {
		t.Fatalf("encoder continued reading %d pixels after cancellation", source.reads)
	}
}

func TestClipboardPNGOutputLimitAndExactBoundary(t *testing.T) {
	source := benchmarkSourceNRGBA(128, 96)
	expected, err := encodeClipboardPNG(context.Background(), source, maxInputSize)
	if err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int{0, 32, len(expected) - 1, len(expected)} {
		data, err := encodeClipboardPNG(context.Background(), source, limit)
		if limit < len(expected) {
			if data != nil || !errors.Is(err, errClipboardLimit) {
				t.Fatalf("limit %d returned %d bytes, %v", limit, len(data), err)
			}
		} else if err != nil || !bytes.Equal(data, expected) {
			t.Fatalf("exact limit rejected/changed valid PNG: %v", err)
		}
	}
}

type colorOnlyImage struct{ image.Image }

func TestUnscaledThumbnailMatchesBilinearSamples(t *testing.T) {
	parent := image.Rect(0, 0, 23, 19)
	bounds := image.Rect(4, 6, 19, 15)
	for _, source := range []interface {
		image.Image
		Set(int, int, color.Color)
	}{
		image.NewRGBA(parent), image.NewNRGBA(parent),
		image.NewRGBA64(parent), image.NewNRGBA64(parent),
		image.NewGray16(parent),
	} {
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			for x := bounds.Min.X; x < bounds.Max.X; x++ {
				source.Set(x, y, color.NRGBA64{
					R: uint16(x*1917 + y*157), G: uint16(y*3219 + x*139),
					B: uint16(x*y*337 + 128), A: uint16(x*1193 + y*777),
				})
			}
		}
		// SubImage preserves padded strides and moves the coordinate origin.
		sub := source.(interface {
			SubImage(image.Rectangle) image.Image
		}).SubImage(bounds)
		for orientation := 1; orientation <= 8; orientation++ {
			oriented := normalizeOrientation(sub, orientation)
			for _, candidate := range []image.Image{
				oriented, colorOnlyImage{oriented},
			} {
				got, err := thumbnailRaster(context.Background(), candidate, 64)
				if err != nil {
					t.Fatal(err)
				}
				for y := 0; y < got.Rect.Dy(); y++ {
					for x := 0; x < got.Rect.Dx(); x++ {
						r, g, b, a := candidate.At(candidate.Bounds().Min.X+x, candidate.Bounds().Min.Y+y).RGBA()
						want := color.RGBA{
							R: thumbnailChannel(r, r, r, r, 0, 0),
							G: thumbnailChannel(g, g, g, g, 0, 0),
							B: thumbnailChannel(b, b, b, b, 0, 0),
							A: thumbnailChannel(a, a, a, a, 0, 0),
						}
						if got.RGBAAt(x, y) != want {
							t.Fatalf("%T direction=%d pixel=(%d,%d): got %v, want %v",
								source, orientation, x, y, got.RGBAAt(x, y), want)
						}
					}
				}
			}
		}
	}
}

func TestSnapshotHashesBindActualInputAndOutput(t *testing.T) {
	input := []byte("original image bytes")
	for _, output := range [][]byte{input, input[:8], bytes.Clone(input), []byte("smaller result")} {
		snapshot := captureOverwriteSnapshot(input, output)
		if snapshot.inputHash != sha256.Sum256(input) || snapshot.outputHash != sha256.Sum256(output) ||
			snapshot.inputSize != int64(len(input)) || snapshot.outputSize != int64(len(output)) {
			t.Fatal("snapshot lost content/length identity")
		}
	}
}
