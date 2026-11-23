package ycbcr

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"image"
	"image/draw"
	"testing"
)

func TestYCbCrConversionsMatchDraw(t *testing.T) {
	ratios := []image.YCbCrSubsampleRatio{
		image.YCbCrSubsampleRatio444,
		image.YCbCrSubsampleRatio422,
		image.YCbCrSubsampleRatio420,
		image.YCbCrSubsampleRatio440,
		image.YCbCrSubsampleRatio411,
		image.YCbCrSubsampleRatio410,
	}
	for _, ratio := range ratios {
		src := image.NewYCbCr(image.Rect(-3, -5, 34, 26), ratio)
		for i := range src.Y {
			src.Y[i] = uint8((i*17 + 23) % 256)
		}
		for i := range src.Cb {
			src.Cb[i] = uint8((i*31 + 11) % 256)
			src.Cr[i] = uint8((i*13 + 71) % 256)
		}
		for _, tc := range []struct {
			name string
			img  *image.YCbCr
		}{
			{"full", src},
			{"sub", src.SubImage(image.Rect(2, 3, 27, 21)).(*image.YCbCr)},
		} {
			b := tc.img.Bounds()
			want := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
			draw.Draw(want, want.Bounds(), tc.img, b.Min, draw.Src)

			got := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
			ToNRGBAInto(got, tc.img, b)
			if !bytes.Equal(got.Pix, want.Pix) {
				t.Fatalf("ToNRGBAInto mismatch for %s (%s)", ratio, tc.name)
			}

			var rgbBuf bytes.Buffer
			bw := bufio.NewWriter(&rgbBuf)
			rowBuf := make([]byte, b.Dx()*3)
			if err := WriteRGBScanlines(context.Background(), bw, tc.img, b, rowBuf); err != nil {
				t.Fatalf("WriteRGBScanlines failed for %s (%s): %v", ratio, tc.name, err)
			}
			if err := bw.Flush(); err != nil {
				t.Fatal(err)
			}
			gotRGB := rgbBuf.Bytes()
			if len(gotRGB) != b.Dx()*b.Dy()*3 {
				t.Fatalf("unexpected RGB length for %s (%s): got %d", ratio, tc.name, len(gotRGB))
			}
			for i := range b.Dx() * b.Dy() {
				if gotRGB[i*3] != want.Pix[i*4] || gotRGB[i*3+1] != want.Pix[i*4+1] || gotRGB[i*3+2] != want.Pix[i*4+2] {
					t.Fatalf("WriteRGBScanlines pixel %d mismatch for %s (%s)", i, ratio, tc.name)
				}
			}
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	src := image.NewYCbCr(image.Rect(0, 0, 8, 8), image.YCbCrSubsampleRatio420)
	var buf bytes.Buffer
	err := WriteRGBScanlines(ctx, bufio.NewWriter(&buf), src, src.Bounds(), make([]byte, 8*3))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}
