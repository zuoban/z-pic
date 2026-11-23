package ycbcr

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
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
			{"sub-even", src.SubImage(image.Rect(2, 3, 27, 21)).(*image.YCbCr)},
			{"sub-odd", src.SubImage(image.Rect(3, 4, 28, 22)).(*image.YCbCr)},
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

			for row := range b.Dy() {
				FillRGBRow(rowBuf, tc.img, b, row)
				wantRow := gotRGB[row*b.Dx()*3 : (row+1)*b.Dx()*3]
				if !bytes.Equal(rowBuf, wantRow) {
					t.Fatalf("FillRGBRow row %d mismatch for %s (%s)", row, ratio, tc.name)
				}
			}
		}

		// Also verify non-negative even-origin 4:2:0 fast path explicitly.
		pos := image.NewYCbCr(image.Rect(0, 0, 29, 21), ratio)
		for i := range pos.Y {
			pos.Y[i] = uint8((i*23 + 7) % 256)
		}
		for i := range pos.Cb {
			pos.Cb[i] = uint8((i*37 + 13) % 256)
			pos.Cr[i] = uint8((i*19 + 59) % 256)
		}
		posWant := image.NewNRGBA(pos.Bounds())
		draw.Draw(posWant, posWant.Bounds(), pos, pos.Bounds().Min, draw.Src)
		posRow := make([]byte, pos.Bounds().Dx()*3)
		for row := range pos.Bounds().Dy() {
			FillRGBRow(posRow, pos, pos.Bounds(), row)
			for x := range pos.Bounds().Dx() {
				dOff := row*posWant.Stride + x*4
				if posRow[x*3] != posWant.Pix[dOff] || posRow[x*3+1] != posWant.Pix[dOff+1] || posRow[x*3+2] != posWant.Pix[dOff+2] {
					t.Fatalf("FillRGBRow positive-origin mismatch at (%d,%d) for %s", x, row, ratio)
				}
			}
		}

		ny := image.NewNYCbCrA(image.Rect(-2, -3, 27, 22), ratio)
		for i := range ny.Y {
			ny.Y[i] = uint8((i*19 + 5) % 256)
			if i%7 == 0 {
				ny.A[i] = 0
			} else {
				ny.A[i] = uint8((i*29 + 3) % 256)
			}
		}
		for i := range ny.Cb {
			ny.Cb[i] = uint8((i*11 + 47) % 256)
			ny.Cr[i] = uint8((i*7 + 89) % 256)
		}
		for _, tc := range []struct {
			name string
			img  *image.NYCbCrA
		}{
			{"full", ny},
			{"sub", ny.SubImage(image.Rect(3, 4, 23, 18)).(*image.NYCbCrA)},
		} {
			b := tc.img.Bounds()
			dst := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
			// Pre-fill dst with non-zero bytes to verify A==0 clears pixels.
			for i := range dst.Pix {
				dst.Pix[i] = 0xaa
			}
			NYCbCrAToNRGBAInto(dst, tc.img, b)
			for y := range b.Dy() {
				for x := range b.Dx() {
					c := tc.img.NYCbCrAAt(b.Min.X+x, b.Min.Y+y)
					var want color.NRGBA
					if c.A != 0 {
						r, g, bl := color.YCbCrToRGB(c.Y, c.Cb, c.Cr)
						want = color.NRGBA{R: r, G: g, B: bl, A: c.A}
					}
					if got := dst.NRGBAAt(x, y); got != want {
						t.Fatalf("NYCbCrAToNRGBAInto mismatch for %s (%s) at (%d,%d): got %+v want %+v", ratio, tc.name, x, y, got, want)
					}
				}
			}
		}
	}

	// Non-canonical empty bounds (Dx() < 0, Dy() > 0) must be a no-op, not panic.
	emptyNY := image.NewNYCbCrA(image.Rect(0, 0, 4, 4), image.YCbCrSubsampleRatio420)
	emptyDst := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	NYCbCrAToNRGBAInto(emptyDst, emptyNY, image.Rectangle{Min: image.Pt(5, 0), Max: image.Pt(2, 4)})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	src := image.NewYCbCr(image.Rect(0, 0, 8, 8), image.YCbCrSubsampleRatio420)
	var buf bytes.Buffer
	err := WriteRGBScanlines(ctx, bufio.NewWriter(&buf), src, src.Bounds(), make([]byte, 8*3))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}
