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

func TestWebPEXIFOrientationAcrossClipboardThumbnailAndPreview(t *testing.T) {
	base := webpFixture(t, "alpha") // 96x64 NYCbCrA
	for _, withExifPrefix := range []bool{false, true} {
		payload := exifTIFF(binary.LittleEndian, 6)
		if withExifPrefix {
			payload = append([]byte("Exif\x00\x00"), payload...)
		}
		orientedWebP := appendWebPChunk(extendedWebP(base, 0x18, 96, 64), "EXIF", payload)

		rawDecoded, _, err := image.Decode(bytes.NewReader(orientedWebP))
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := rawDecoded.(*image.NYCbCrA); !ok {
			t.Fatalf("expected *image.NYCbCrA from alpha WebP decode, got %T", rawDecoded)
		}
		want := normalizeOrientation(rawDecoded, 6)

		pngOut, err := PNGBytes(context.Background(), orientedWebP)
		if err != nil {
			t.Fatalf("PNGBytes failed: %v", err)
		}
		gotPNG, err := png.Decode(bytes.NewReader(pngOut))
		if err != nil {
			t.Fatal(err)
		}
		if gotPNG.Bounds() != image.Rect(0, 0, 64, 96) {
			t.Fatalf("clipboard PNG bounds = %v, want 64x96", gotPNG.Bounds())
		}
		for y := 0; y < 96; y += 7 {
			for x := 0; x < 64; x += 7 {
				wantC := color.NRGBA64Model.Convert(want.At(x, y))
				gotC := color.NRGBA64Model.Convert(gotPNG.At(x, y))
				if wantC != gotC {
					t.Fatalf("clipboard pixel (%d,%d) = %v, want %v", x, y, gotC, wantC)
				}
			}
		}

		thumb, err := Thumbnail(context.Background(), writeFixture(t, "oriented.webp", orientedWebP), 48)
		if err != nil {
			t.Fatalf("Thumbnail failed: %v", err)
		}
		if thumb.Bounds() != image.Rect(0, 0, 32, 48) {
			t.Fatalf("thumbnail bounds = %v, want 32x48", thumb.Bounds())
		}
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

func TestClipboardPNGRejectsAnimatedContainers(t *testing.T) {
	pngData := pngFixture(t, image.NewNRGBA(image.Rect(0, 0, 3, 2)), png.DefaultCompression)
	webpData := webpFixture(t, "opaque")
	for name, data := range map[string][]byte{
		"APNG":           insertPNGChunk(pngData, "acTL", make([]byte, 8)),
		"WebP flag":      extendedWebP(webpData, 2, 256, 192),
		"late WebP ANIM": appendWebPChunk(webpData, "ANIM", make([]byte, 6)),
		"late WebP ANMF": appendWebPChunk(webpData, "ANMF", make([]byte, 16)),
		"bad WebP RIFF":  append(bytes.Clone(webpData), 0),
	} {
		t.Run(name, func(t *testing.T) {
			if data, err := PNGBytes(context.Background(), data); err == nil || data != nil {
				t.Fatal("animated or malformed container reached clipboard output")
			}
		})
	}
}

func TestStreamYCbCrAndNYCbCrAClipboardPNGMatches16BitModelAndHonorsCancel(t *testing.T) {
	for _, ratio := range []image.YCbCrSubsampleRatio{
		image.YCbCrSubsampleRatio420,
		image.YCbCrSubsampleRatio422,
		image.YCbCrSubsampleRatio444,
	} {
		for _, bounds := range []image.Rectangle{
			image.Rect(0, 0, 17, 13),
			image.Rect(3, 5, 20, 18),
		} {
			ycbcr := image.NewYCbCr(bounds, ratio)
			for i := range ycbcr.Y {
				ycbcr.Y[i] = uint8(i*37 + 19)
			}
			for i := range ycbcr.Cb {
				ycbcr.Cb[i] = uint8(i*53 + 80)
				ycbcr.Cr[i] = uint8(i*29 + 110)
			}
			encoded, err := encodeClipboardPNG(context.Background(), ycbcr, maxInputSize)
			if err != nil {
				t.Fatal(err)
			}
			got, err := png.Decode(bytes.NewReader(encoded))
			if err != nil {
				t.Fatal(err)
			}
			for y := 0; y < bounds.Dy(); y++ {
				for x := 0; x < bounds.Dx(); x++ {
					wantC := color.NRGBA64Model.Convert(ycbcr.At(bounds.Min.X+x, bounds.Min.Y+y))
					gotC := color.NRGBA64Model.Convert(got.At(x, y))
					if wantC != gotC {
						t.Fatalf("YCbCr (%v, %v) mismatch at (%d,%d): got %v, want %v", ratio, bounds, x, y, gotC, wantC)
					}
				}
			}
		}
	}

	ny := image.NewNYCbCrA(image.Rect(2, 3, 11, 9), image.YCbCrSubsampleRatio420)
	for i := range ny.Y {
		ny.Y[i] = uint8(i*41 + 30)
		ny.A[i] = []uint8{0, 1, 73, 180, 255}[i%5]
	}
	for i := range ny.Cb {
		ny.Cb[i] = uint8(i*23 + 90)
		ny.Cr[i] = uint8(i*61 + 40)
	}
	encodedNY, err := encodeClipboardPNG(context.Background(), ny, maxInputSize)
	if err != nil {
		t.Fatal(err)
	}
	gotNY, err := png.Decode(bytes.NewReader(encodedNY))
	if err != nil {
		t.Fatal(err)
	}
	for y := 0; y < ny.Bounds().Dy(); y++ {
		for x := 0; x < ny.Bounds().Dx(); x++ {
			want := color.NRGBA64Model.Convert(ny.At(ny.Bounds().Min.X+x, ny.Bounds().Min.Y+y))
			got := color.NRGBA64Model.Convert(gotNY.At(x, y))
			if want != got {
				t.Fatalf("NYCbCrA mismatch at (%d,%d): got %v, want %v", x, y, got, want)
			}
		}
	}

	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	cCtx := &cancelAtCheckpoint{Context: base, cancel: cancel, limit: 4}
	if _, err := encodeClipboardPNG(cCtx, image.NewYCbCr(image.Rect(0, 0, 16, 16), image.YCbCrSubsampleRatio420), maxInputSize); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected row cancellation, got %v", err)
	}
}
