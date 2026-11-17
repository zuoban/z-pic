package compress

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestClipboardPNGPreservesPixelsAndOrientation(t *testing.T) {
	transparent := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	transparent.SetNRGBA(0, 0, color.NRGBA{R: 210, G: 80, B: 20, A: 100})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, transparent); err != nil {
		t.Fatal(err)
	}
	cases := map[string][]byte{
		"png":  encoded.Bytes(),
		"webp": webpFixture(t, "alpha"),
	}
	for orientation := uint16(1); orientation <= 8; orientation++ {
		data := withEXIF(jpegFixture(t, 90), exifTIFF(binary.LittleEndian, orientation))
		cases[string(rune('0'+orientation))] = data
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			before := bytes.Clone(input)
			want, format, err := image.Decode(bytes.NewReader(input))
			if err != nil {
				t.Fatal(err)
			}
			if format == "jpeg" {
				want = normalizeOrientation(want, jpegOrientation(input))
			}
			data, err := PNGBytes(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			got, err := png.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			if got.Bounds() != want.Bounds() || !bytes.Equal(input, before) {
				t.Fatal("clipboard conversion must preserve dimensions and source bytes")
			}
			bounds := want.Bounds()
			for row := bounds.Min.Y; row < bounds.Max.Y; row++ {
				for column := bounds.Min.X; column < bounds.Max.X; column++ {
					wantColor := color.NRGBA64Model.Convert(want.At(column, row))
					gotColor := color.NRGBA64Model.Convert(got.At(column, row))
					if wantColor != gotColor {
						t.Fatalf("pixel (%d,%d) = %v, want %v", column, row, gotColor, wantColor)
					}
				}
			}
			if format == "png" && !bytes.Equal(data, input) {
				t.Fatal("PNG clipboard data must preserve the original encoding")
			}
		})
	}
}

func TestClipboardPNGRejectsInvalidOrCanceledInput(t *testing.T) {
	for _, data := range [][]byte{nil, []byte("broken image"), jpegFixture(t, 80)[:40]} {
		if _, err := PNGBytes(context.Background(), data); err == nil {
			t.Fatal("invalid data must fail before clipboard publication")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := PNGBytes(ctx, jpegFixture(t, 80)); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled conversion = %v", err)
	}
}
