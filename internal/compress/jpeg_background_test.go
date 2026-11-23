package compress

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"testing"

	"zpic/internal/webptools"
)

func TestParseValidateNormalizeJPEGBackground(t *testing.T) {
	white := color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	black := color.RGBA{R: 0x00, G: 0x00, B: 0x00, A: 0xff}
	custom := color.RGBA{R: 0x12, G: 0x34, B: 0x56, A: 0xff}

	for _, tc := range []struct {
		raw       string
		wantColor color.RGBA
		wantNorm  string
	}{
		{"", white, ""},
		{"   ", white, ""},
		{"white", white, ""},
		{"WHITE", white, ""},
		{" White ", white, ""},
		{"#ffffff", white, ""},
		{"#FFFFFF", white, ""},
		{" #FfFfFf ", white, ""},
		{"black", black, "#000000"},
		{"BLACK", black, "#000000"},
		{" Black ", black, "#000000"},
		{"#000000", black, "#000000"},
		{" #000000 ", black, "#000000"},
		{"#123456", custom, "#123456"},
		{" #123456 ", custom, "#123456"},
		{"#aBcDeF", color.RGBA{R: 0xab, G: 0xcd, B: 0xef, A: 0xff}, "#abcdef"},
	} {
		got, err := ParseJPEGBackground(tc.raw)
		if err != nil {
			t.Errorf("ParseJPEGBackground(%q) returned unexpected error: %v", tc.raw, err)
			continue
		}
		if got != tc.wantColor {
			t.Errorf("ParseJPEGBackground(%q) = %+v, want %+v", tc.raw, got, tc.wantColor)
		}
		if err := ValidateJPEGBackground(tc.raw); err != nil {
			t.Errorf("ValidateJPEGBackground(%q) returned unexpected error: %v", tc.raw, err)
		}
		if norm := NormalizeJPEGBackground(tc.raw); norm != tc.wantNorm {
			t.Errorf("NormalizeJPEGBackground(%q) = %q, want %q", tc.raw, norm, tc.wantNorm)
		}
	}

	for _, invalid := range []string{
		"#fff",
		"ffffff",
		"123456",
		"#12345",
		"#1234567",
		"#12345678",
		"#gggggg",
		"#12345g",
		"red",
		"transparent",
		"rgb(255,255,255)",
		"#12 345",
	} {
		if _, err := ParseJPEGBackground(invalid); err == nil {
			t.Errorf("ParseJPEGBackground(%q) succeeded, want error", invalid)
		}
		if err := ValidateJPEGBackground(invalid); err == nil {
			t.Errorf("ValidateJPEGBackground(%q) succeeded, want error", invalid)
		}
		if norm := NormalizeJPEGBackground(invalid); norm == "" {
			t.Errorf("NormalizeJPEGBackground(%q) silently normalized invalid input to empty string", invalid)
		}
	}
}

func TestFlattenAlphaForJPEGBackgrounds(t *testing.T) {
	// Include a non-zero origin rectangle to exercise bounds offset handling.
	src := image.NewNRGBA(image.Rect(3, 5, 8, 6))
	pixels := []color.NRGBA{
		{R: 0, G: 0, B: 0, A: 0},        // fully transparent
		{R: 255, G: 0, B: 0, A: 0},      // fully transparent with nonzero RGB
		{R: 200, G: 100, B: 50, A: 128}, // ~50% alpha
		{R: 60, G: 180, B: 240, A: 64},  // ~25% alpha
		{R: 200, G: 100, B: 50, A: 255}, // fully opaque
	}
	for i, c := range pixels {
		src.SetNRGBA(3+i, 5, c)
	}

	backgrounds := []struct {
		name string
		bg   color.RGBA
	}{
		{"white", color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}},
		{"black", color.RGBA{R: 0x00, G: 0x00, B: 0x00, A: 0xff}},
		{"custom", color.RGBA{R: 0x12, G: 0x34, B: 0x56, A: 0xff}},
	}

	for _, tc := range backgrounds {
		t.Run(tc.name, func(t *testing.T) {
			flat, err := flattenAlphaForJPEG(t.Context(), src, tc.bg)
			if err != nil {
				t.Fatalf("flattenAlphaForJPEG failed: %v", err)
			}
			rgba, ok := flat.(*image.RGBA)
			if !ok {
				t.Fatalf("expected *image.RGBA output, got %T", flat)
			}
			if rgba.Bounds() != image.Rect(0, 0, len(pixels), 1) {
				t.Fatalf("unexpected bounds %v", rgba.Bounds())
			}

			bgR := uint32(tc.bg.R) * 0x101
			bgG := uint32(tc.bg.G) * 0x101
			bgB := uint32(tc.bg.B) * 0x101
			for i, c := range pixels {
				got := rgba.RGBAAt(i, 0)
				r, g, b, a := c.RGBA()
				a = min(a, 0xffff)
				invA := 0xffff - a
				want := color.RGBA{
					R: uint8((min(r, a) + (invA*bgR+0x7fff)/0xffff) >> 8),
					G: uint8((min(g, a) + (invA*bgG+0x7fff)/0xffff) >> 8),
					B: uint8((min(b, a) + (invA*bgB+0x7fff)/0xffff) >> 8),
					A: 0xff,
				}
				if got != want {
					t.Errorf("pixel %d (%+v) over %s: got %+v, want %+v", i, c, tc.name, got, want)
				}
			}

			// Fully transparent pixels must exactly equal the background color.
			if got := rgba.RGBAAt(0, 0); got != tc.bg {
				t.Errorf("transparent pixel 0 = %+v, want %+v", got, tc.bg)
			}
			if got := rgba.RGBAAt(1, 0); got != tc.bg {
				t.Errorf("transparent pixel 1 = %+v, want %+v", got, tc.bg)
			}
			// Opaque pixel must remain unchanged.
			wantOpaque := color.RGBA{R: 200, G: 100, B: 50, A: 255}
			if got := rgba.RGBAAt(4, 0); got != wantOpaque {
				t.Errorf("opaque pixel = %+v, want %+v", got, wantOpaque)
			}
		})
	}
}

func TestFlattenAlphaForJPEGOpaqueFastPathAndCancel(t *testing.T) {
	opaqueNRGBA := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	draw.Draw(opaqueNRGBA, opaqueNRGBA.Bounds(), image.NewUniform(color.NRGBA{R: 10, G: 20, B: 30, A: 255}), image.Point{}, draw.Src)

	opaqueRGBA := image.NewRGBA(image.Rect(0, 0, 4, 4))
	draw.Draw(opaqueRGBA, opaqueRGBA.Bounds(), image.NewUniform(color.RGBA{R: 10, G: 20, B: 30, A: 255}), image.Point{}, draw.Src)

	bg := color.RGBA{R: 0x12, G: 0x34, B: 0x56, A: 0xff}
	for _, tc := range []struct {
		name string
		img  image.Image
	}{
		{"opaque NRGBA", opaqueNRGBA},
		{"opaque RGBA", opaqueRGBA},
		{"YCbCr", image.NewYCbCr(image.Rect(0, 0, 4, 4), image.YCbCrSubsampleRatio420)},
		{"Gray", image.NewGray(image.Rect(0, 0, 4, 4))},
		{"Gray16", image.NewGray16(image.Rect(0, 0, 4, 4))},
		{"CMYK", image.NewCMYK(image.Rect(0, 0, 4, 4))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := flattenAlphaForJPEG(t.Context(), tc.img, bg)
			if err != nil {
				t.Fatalf("flattenAlphaForJPEG(%s) failed: %v", tc.name, err)
			}
			if got != tc.img {
				t.Fatalf("flattenAlphaForJPEG(%s) allocated a new image instead of taking the opaque fast path", tc.name)
			}
		})
	}

	t.Run("canceled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		alphaImg := image.NewNRGBA(image.Rect(0, 0, 4, 4))
		got, err := flattenAlphaForJPEG(ctx, alphaImg, bg)
		if got != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got img=%v err=%v", got, err)
		}
	})
}

func makeAlphaTilesFixture() (*image.NRGBA, []image.Point) {
	const tile = 16
	src := image.NewNRGBA(image.Rect(0, 0, 3*tile, 2*tile))
	tiles := [2][3]color.NRGBA{
		{{R: 0, G: 0, B: 0, A: 0}, {R: 200, G: 80, B: 40, A: 128}, {R: 180, G: 60, B: 40, A: 255}},
		{{R: 255, G: 255, B: 255, A: 0}, {R: 40, G: 180, B: 220, A: 128}, {R: 30, G: 120, B: 190, A: 255}},
	}
	var centers []image.Point
	for row, line := range tiles {
		for col, c := range line {
			x, y := col*tile, row*tile
			draw.Draw(src, image.Rect(x, y, x+tile, y+tile), image.NewUniform(c), image.Point{}, draw.Src)
			centers = append(centers, image.Pt(x+tile/2, y+tile/2))
		}
	}
	return src, centers
}

func assertJPEGFlattenedColors(t *testing.T, jpegData []byte, preview image.Image, src *image.NRGBA, centers []image.Point, bg color.RGBA) {
	t.Helper()
	expected, err := flattenAlphaForJPEG(t.Context(), src, bg)
	if err != nil {
		t.Fatal(err)
	}
	decoded, format, err := image.Decode(bytes.NewReader(jpegData))
	if err != nil || format != "jpeg" {
		t.Fatalf("decode JPEG failed: format=%q err=%v", format, err)
	}
	assertConversionSamples(t, decoded, expected, centers, color.RGBAModel, 12)
	if preview != nil {
		assertConversionSamples(t, preview, expected, centers, color.RGBAModel, 12)
	}
}

func TestConvertAlphaPNGToJPEGBackground(t *testing.T) {
	src, centers := makeAlphaTilesFixture()
	pngData := pngFixture(t, src, png.DefaultCompression)
	path := writeFixture(t, "alpha.png", pngData)

	cases := []struct {
		name string
		opt  string
		bg   color.RGBA
	}{
		{"default-white", "", color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}},
		{"named-white", "white", color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}},
		{"named-black", "black", color.RGBA{R: 0x00, G: 0x00, B: 0x00, A: 0xff}},
		{"custom-hex", "#123456", color.RGBA{R: 0x12, G: 0x34, B: 0x56, A: 0xff}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := Options{
				Quality:        95,
				OutputFormat:   OutputFormatJPEG,
				JPEGBackground: tc.opt,
			}
			resFile, err := File(t.Context(), path, opts)
			if err != nil {
				t.Fatalf("File failed: %v", err)
			}
			assertJPEGFlattenedColors(t, resFile.Data, nil, src, centers, tc.bg)

			resBytes, err := Bytes(t.Context(), pngData, path, opts)
			if err != nil {
				t.Fatalf("Bytes failed: %v", err)
			}
			assertJPEGFlattenedColors(t, resBytes.Data, nil, src, centers, tc.bg)

			resPreview, preview, err := BytesWithPreview(t.Context(), pngData, path, opts)
			if err != nil {
				t.Fatalf("BytesWithPreview failed: %v", err)
			}
			if preview == nil {
				t.Fatal("expected non-nil preview for PNG->JPEG conversion")
			}
			assertJPEGFlattenedColors(t, resPreview.Data, preview, src, centers, tc.bg)
		})
	}
}

func TestConvertAlphaWebPToJPEGBackground(t *testing.T) {
	requireWebPEncoder(t)
	src, centers := makeAlphaTilesFixture()
	webpData, err := webptools.EncodeImage(t.Context(), src, webptools.EncodeOptions{
		Quality:  100,
		Lossless: true,
		Threads:  1,
	})
	if err != nil {
		t.Fatalf("encode WebP alpha fixture failed: %v", err)
	}
	path := writeFixture(t, "alpha.webp", webpData)

	cases := []struct {
		name string
		opt  string
		bg   color.RGBA
	}{
		{"default-white", "", color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}},
		{"named-black", "black", color.RGBA{R: 0x00, G: 0x00, B: 0x00, A: 0xff}},
		{"custom-hex", "#123456", color.RGBA{R: 0x12, G: 0x34, B: 0x56, A: 0xff}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := Options{
				Quality:        95,
				OutputFormat:   OutputFormatJPEG,
				JPEGBackground: tc.opt,
			}
			res, preview, err := BytesWithPreview(t.Context(), webpData, path, opts)
			if err != nil {
				t.Fatalf("BytesWithPreview(webp->jpeg) failed: %v", err)
			}
			assertJPEGFlattenedColors(t, res.Data, preview, src, centers, tc.bg)
		})
	}
}

func TestJPEGBackgroundValidationRejected(t *testing.T) {
	src, _ := makeAlphaTilesFixture()
	pngData := pngFixture(t, src, png.DefaultCompression)
	path := writeFixture(t, "alpha.png", pngData)

	invalidValues := []string{"#fff", "red", "123456", "#12345g", "#12345678"}
	for _, invalid := range invalidValues {
		opts := Options{
			Quality:        85,
			OutputFormat:   OutputFormatJPEG,
			JPEGBackground: invalid,
		}
		for _, method := range inputMethods(pngData) {
			res, err := method.run(t.Context(), path, opts)
			if res != nil || err == nil {
				t.Errorf("%s accepted invalid JPEGBackground %q: res=%v err=%v", method.name, invalid, res != nil, err)
			}
		}
		if res, err := CompressClipboardPNG(t.Context(), pngData, opts); res != nil || err == nil {
			t.Errorf("CompressClipboardPNG accepted invalid JPEGBackground %q: res=%v err=%v", invalid, res != nil, err)
		}
	}
}

func TestFlattenAlphaForJPEGFastPathsMatchGeneric(t *testing.T) {
	parent := image.Rect(-2, -3, 26, 20)
	sub := image.Rect(3, 2, 21, 16)

	nrgba := image.NewNRGBA(parent)
	rgba := image.NewRGBA(parent)
	nrgba64 := image.NewNRGBA64(parent)
	pal := color.Palette{
		color.NRGBA{R: 0, G: 0, B: 0, A: 0},
		color.NRGBA{R: 220, G: 60, B: 40, A: 64},
		color.NRGBA{R: 40, G: 180, B: 230, A: 128},
		color.NRGBA{R: 120, G: 200, B: 80, A: 200},
		color.NRGBA{R: 15, G: 90, B: 240, A: 255},
	}
	paletted := image.NewPaletted(parent, pal)
	for y := parent.Min.Y; y < parent.Max.Y; y++ {
		for x := parent.Min.X; x < parent.Max.X; x++ {
			a := uint8(((x-parent.Min.X)*37 + (y-parent.Min.Y)*19) % 256)
			if (x+y)%5 == 0 {
				a = 0
			} else if (x+y)%7 == 0 {
				a = 255
			}
			r := uint8((x * 13) & 0xff)
			g := uint8((y * 29) & 0xff)
			b := uint8(((x ^ y) * 17) & 0xff)
			c := color.NRGBA{R: r, G: g, B: b, A: a}
			nrgba.SetNRGBA(x, y, c)
			if x == sub.Min.X+1 && y == sub.Min.Y+1 {
				// Out-of-gamut premultiplied RGBA pixel (channel > alpha) to verify clamping.
				rgba.SetRGBA(x, y, color.RGBA{R: 250, G: 200, B: 180, A: 100})
			} else {
				rgba.Set(x, y, c)
			}
			nrgba64.SetNRGBA64(x, y, color.NRGBA64{
				R: uint16(r)*257 + 13,
				G: uint16(g)*257 + 29,
				B: uint16(b)*257 + 41,
				A: uint16(a) * 257,
			})
			paletted.SetColorIndex(x, y, uint8(((x-parent.Min.X)+(y-parent.Min.Y))%len(pal)))
		}
	}
	ny := image.NewNYCbCrA(parent, image.YCbCrSubsampleRatio420)
	for i := range ny.Y {
		ny.Y[i] = uint8((i*23 + 11) % 256)
		switch i % 6 {
		case 0:
			ny.A[i] = 0
		case 1:
			ny.A[i] = 255
		default:
			ny.A[i] = uint8((i*41 + 17) % 256)
		}
	}
	for i := range ny.Cb {
		ny.Cb[i] = uint8((i*17 + 63) % 256)
		ny.Cr[i] = uint8((i*31 + 97) % 256)
	}

	bgs := []color.RGBA{
		{R: 0xff, G: 0xff, B: 0xff, A: 0xff},
		{R: 0x00, G: 0x00, B: 0x00, A: 0xff},
		{R: 0x34, G: 0x78, B: 0xbc, A: 0xff},
	}
	cases := []struct {
		name string
		img  image.Image
	}{
		{"nrgba", nrgba},
		{"nrgba-sub", nrgba.SubImage(sub)},
		{"rgba", rgba},
		{"rgba-sub", rgba.SubImage(sub)},
		{"paletted", paletted},
		{"paletted-sub", paletted.SubImage(sub)},
		{"nycbcra", ny},
		{"nycbcra-sub", ny.SubImage(sub)},
		{"nrgba64-sub", nrgba64.SubImage(sub)},
	}
	for _, bg := range bgs {
		bgR := uint32(bg.R) * 0x101
		bgG := uint32(bg.G) * 0x101
		bgB := uint32(bg.B) * 0x101
		for _, tc := range cases {
			gotImg, err := flattenAlphaForJPEG(t.Context(), tc.img, bg)
			if err != nil {
				t.Fatalf("flattenAlphaForJPEG(%s) failed: %v", tc.name, err)
			}
			got, ok := gotImg.(*image.RGBA)
			if !ok {
				t.Fatalf("flattenAlphaForJPEG(%s) returned %T, want *image.RGBA", tc.name, gotImg)
			}
			b := tc.img.Bounds()
			if got.Bounds() != image.Rect(0, 0, b.Dx(), b.Dy()) {
				t.Fatalf("flattenAlphaForJPEG(%s) bounds = %v, want 0..%dx%d", tc.name, got.Bounds(), b.Dx(), b.Dy())
			}
			for y := range b.Dy() {
				for x := range b.Dx() {
					r, g, bl, a := tc.img.At(b.Min.X+x, b.Min.Y+y).RGBA()
					a = min(a, 0xffff)
					invA := 0xffff - a
					want := color.RGBA{
						R: uint8((min(r, a) + (invA*bgR+0x7fff)/0xffff) >> 8),
						G: uint8((min(g, a) + (invA*bgG+0x7fff)/0xffff) >> 8),
						B: uint8((min(bl, a) + (invA*bgB+0x7fff)/0xffff) >> 8),
						A: 0xff,
					}
					if actual := got.RGBAAt(x, y); actual != want {
						t.Fatalf("flattenAlphaForJPEG(%s, bg=%+v) at (%d,%d): got %+v, want %+v",
							tc.name, bg, x, y, actual, want)
					}
				}
			}
		}
	}
}

func BenchmarkFlattenAlphaForJPEG(b *testing.B) {
	const w, h = 1920, 1080
	nrgba := image.NewNRGBA(image.Rect(0, 0, w, h))
	rgba := image.NewRGBA(image.Rect(0, 0, w, h))
	pal := color.Palette{
		color.NRGBA{R: 0, G: 0, B: 0, A: 0},
		color.NRGBA{R: 200, G: 100, B: 50, A: 128},
		color.NRGBA{R: 40, G: 180, B: 220, A: 255},
	}
	paletted := image.NewPaletted(image.Rect(0, 0, w, h), pal)
	for i := 0; i < w*h; i++ {
		a := uint8(i % 256)
		nrgba.Pix[i*4] = uint8(i)
		nrgba.Pix[i*4+1] = uint8(i >> 2)
		nrgba.Pix[i*4+2] = uint8(i >> 4)
		nrgba.Pix[i*4+3] = a
		rgba.Pix[i*4] = uint8(uint16(nrgba.Pix[i*4]) * uint16(a) / 255)
		rgba.Pix[i*4+1] = uint8(uint16(nrgba.Pix[i*4+1]) * uint16(a) / 255)
		rgba.Pix[i*4+2] = uint8(uint16(nrgba.Pix[i*4+2]) * uint16(a) / 255)
		rgba.Pix[i*4+3] = a
		paletted.Pix[i] = uint8(i % len(pal))
	}
	bg := color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		img  image.Image
	}{
		{"NRGBA", nrgba},
		{"RGBA", rgba},
		{"Paletted", paletted},
	} {
		b.Run(tc.name, func(sub *testing.B) {
			sub.ReportAllocs()
			sub.ResetTimer()
			for range sub.N {
				if _, err := flattenAlphaForJPEG(ctx, tc.img, bg); err != nil {
					sub.Fatal(err)
				}
			}
		})
	}
}
