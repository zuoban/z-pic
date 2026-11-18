package compress

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func exifTIFF(order binary.ByteOrder, orientation uint16) []byte {
	data := make([]byte, 26)
	if order == binary.LittleEndian {
		copy(data, "II")
	} else {
		copy(data, "MM")
	}
	order.PutUint16(data[2:4], 42)
	order.PutUint32(data[4:8], 8)
	order.PutUint16(data[8:10], 1)
	order.PutUint16(data[10:12], 0x0112)
	order.PutUint16(data[12:14], 3)
	order.PutUint32(data[14:18], 1)
	order.PutUint16(data[18:20], orientation)
	return data
}

func withEXIF(jpegData, tiff []byte) []byte {
	payload := append([]byte("Exif\x00\x00"), tiff...)
	out := append([]byte(nil), jpegData[:2]...)
	out = append(out, 0xff, 0xe1, byte((len(payload)+2)>>8), byte(len(payload)+2))
	out = append(out, payload...)
	return append(out, jpegData[2:]...)
}

func TestAllOrientationPixelMappings(t *testing.T) {
	img := image.NewGray(image.Rect(7, 9, 10, 11))
	for y := 0; y < 2; y++ {
		for x := 0; x < 3; x++ {
			img.SetGray(x+7, y+9, color.Gray{Y: uint8(y*3 + x + 1)})
		}
	}
	expected := []string{"123456", "321654", "654321", "456123",
		"142536", "415263", "635241", "362514"}
	for orientation := 1; orientation <= 8; orientation++ {
		t.Run(strconv.Itoa(orientation), func(t *testing.T) {
			normalized := normalizeOrientation(img, orientation)
			bounds := normalized.Bounds()
			var got []byte
			for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
				for x := bounds.Min.X; x < bounds.Max.X; x++ {
					value := color.GrayModel.Convert(normalized.At(x, y)).(color.Gray).Y
					got = append(got, '0'+value)
				}
			}
			if string(got) != expected[orientation-1] {
				t.Fatalf("got %s, want %s", got, expected[orientation-1])
			}
			if orientation >= 5 && (bounds.Dx() != 2 || bounds.Dy() != 3) {
				t.Fatalf("dimensions not swapped: %v", bounds)
			}
		})
	}
}

func TestJPEGExportNormalizesOrientation(t *testing.T) {
	// Four large gray blocks allow robust position assertions after lossy
	// encoding; EXIF padding ensures a smaller re-encoded output.
	img := image.NewGray(image.Rect(0, 0, 80, 48))
	levels := []uint8{30, 90, 150, 220}
	for y := 0; y < 48; y++ {
		for x := 0; x < 80; x++ {
			img.SetGray(x, y, color.Gray{Y: levels[(y/24)*2+x/40]})
		}
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, img, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		orientation   uint16
		width, height int
		corners       []uint8
	}{
		{6, 48, 80, []uint8{150, 30, 220, 90}},
		{2, 80, 48, []uint8{90, 30, 220, 150}},
	} {
		t.Run(strconv.Itoa(int(tc.orientation)), func(t *testing.T) {
			tiff := append(exifTIFF(binary.LittleEndian, tc.orientation), make([]byte, 8192)...)
			source := withEXIF(encoded.Bytes(), tiff)
			result, err := File(context.Background(), writeFixture(t, "photo.jpg", source), Options{Quality: 85})
			if err != nil {
				t.Fatal(err)
			}
			if result.Unchanged || result.Width != tc.width || result.Height != tc.height {
				t.Fatalf("unexpected dimensions or fallback: %dx%d unchanged=%v",
					result.Width, result.Height, result.Unchanged)
			}
			target := filepath.Join(t.TempDir(), "export.jpg")
			if err := result.Save(target); err != nil {
				t.Fatal(err)
			}
			saved, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := jpeg.Decode(bytes.NewReader(saved))
			if err != nil {
				t.Fatal(err)
			}
			if decoded.Bounds().Dx() != tc.width || decoded.Bounds().Dy() != tc.height {
				t.Fatalf("encoded dimensions: %v", decoded.Bounds())
			}
			points := []image.Point{{8, 8}, {tc.width - 9, 8},
				{8, tc.height - 9}, {tc.width - 9, tc.height - 9}}
			for i, point := range points {
				got := color.GrayModel.Convert(decoded.At(point.X, point.Y)).(color.Gray).Y
				delta := int(got) - int(tc.corners[i])
				if delta < -5 || delta > 5 {
					t.Fatalf("corner %d: got %d, want approximately %d", i, got, tc.corners[i])
				}
			}
			if bytes.Contains(saved, []byte("Exif\x00\x00")) {
				t.Fatal("old EXIF orientation remains in normalized output")
			}
		})
	}
}

func TestOrientationFallbackReportsDisplayDimensions(t *testing.T) {
	for _, orientation := range []uint16{2, 6} {
		data := withEXIF(jpegFixture(t, 1), exifTIFF(binary.BigEndian, orientation))
		result, err := File(context.Background(), writeFixture(t, "photo.jpg", data), Options{Quality: 100})
		if err != nil {
			t.Fatal(err)
		}
		width, height := 128, 96
		if orientation >= 5 {
			width, height = height, width
		}
		if !result.Unchanged || !bytes.Equal(result.Data, data) ||
			result.Width != width || result.Height != height {
			t.Fatalf("orientation %d fallback: %dx%d, unchanged=%v",
				orientation, result.Width, result.Height, result.Unchanged)
		}
	}
}

func TestEXIFReaderBoundsAndByteOrders(t *testing.T) {
	for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		for orientation := uint16(1); orientation <= 8; orientation++ {
			data := withEXIF([]byte{0xff, 0xd8, 0xff, 0xd9}, exifTIFF(order, orientation))
			if got := jpegOrientation(data); got != int(orientation) {
				t.Fatalf("orientation %d: got %d", orientation, got)
			}
			for n := 0; n < len(data)-2; n++ {
				if got := jpegOrientation(data[:n]); got != 1 {
					t.Fatalf("truncated APP1 accepted at length %d: %d", n, got)
				}
			}
		}
	}
	for name, mutate := range map[string]func([]byte){
		"byte order":           func(b []byte) { b[0] = 'X' },
		"magic":                func(b []byte) { b[2] = 0 },
		"IFD offset overflow":  func(b []byte) { binary.LittleEndian.PutUint32(b[4:8], 0xffffffff) },
		"IFD overlaps header":  func(b []byte) { binary.LittleEndian.PutUint32(b[4:8], 2) },
		"entry count overflow": func(b []byte) { binary.LittleEndian.PutUint16(b[8:10], 0xffff) },
		"wrong type":           func(b []byte) { binary.LittleEndian.PutUint16(b[12:14], 4) },
		"wrong count":          func(b []byte) { binary.LittleEndian.PutUint32(b[14:18], 2) },
		"zero orientation":     func(b []byte) { binary.LittleEndian.PutUint16(b[18:20], 0) },
		"invalid orientation":  func(b []byte) { binary.LittleEndian.PutUint16(b[18:20], 9) },
		"no orientation":       func(b []byte) { binary.LittleEndian.PutUint16(b[10:12], 0x0113) },
	} {
		t.Run(name, func(t *testing.T) {
			data := exifTIFF(binary.LittleEndian, 6)
			mutate(data)
			if got := jpegOrientation(withEXIF([]byte{0xff, 0xd8, 0xff, 0xd9}, data)); got != 1 {
				t.Fatalf("invalid EXIF returned %d", got)
			}
		})
	}
}

func TestMalformedEXIFDoesNotRejectJPEG(t *testing.T) {
	tiff := exifTIFF(binary.LittleEndian, 6)
	binary.LittleEndian.PutUint32(tiff[4:8], 0xffffffff)
	data := withEXIF(jpegFixture(t, 100), tiff)
	result, err := File(context.Background(), writeFixture(t, "photo.jpg", data), Options{Quality: 80})
	if err != nil {
		t.Fatal(err)
	}
	if result.Width != 128 || result.Height != 96 {
		t.Fatalf("invalid EXIF changed dimensions: %dx%d", result.Width, result.Height)
	}
}

type plainColorImage struct {
	bounds image.Rectangle
	pixels *image.NRGBA
}

func (plain plainColorImage) ColorModel() color.Model {
	return color.NRGBAModel
}

func (plain plainColorImage) Bounds() image.Rectangle {
	return plain.bounds
}

func (plain plainColorImage) At(column, row int) color.Color {
	return plain.pixels.NRGBAAt(column, row)
}

type opacityProbe struct {
	image.Image
	opacity    bool
	checks     int
	pixelReads int
}

func (probe *opacityProbe) Opaque() bool {
	probe.checks++
	return probe.opacity
}

func (probe *opacityProbe) At(column, row int) color.Color {
	probe.pixelReads++
	return probe.Image.At(column, row)
}

func TestOrientedImageOpacityDelegatesWithoutReadingPixels(test *testing.T) {
	for _, opaque := range []bool{true, false} {
		for orientation := 2; orientation <= 8; orientation++ {
			pixels := image.NewNRGBA(image.Rect(3, 5, 8, 9))
			if opaque {
				for offset := 3; offset < len(pixels.Pix); offset += 4 {
					pixels.Pix[offset] = 255
				}
			}
			source := &opacityProbe{Image: pixels, opacity: opaque}
			oriented := normalizeOrientation(source, orientation).(interface{ Opaque() bool })
			if got := oriented.Opaque(); got != opaque || source.checks != 1 || source.pixelReads != 0 {
				test.Fatalf("orientation %d: opaque=%v want=%v checks=%d pixel reads=%d",
					orientation, got, opaque, source.checks, source.pixelReads)
			}
		}
	}
}

func TestOrientedImageOpacityFallbackPreservesAlpha(test *testing.T) {
	bounds := image.Rect(-3, 5, 4, 9)
	for _, alpha := range []uint8{255, 254, 128, 0} {
		source := image.NewNRGBA(bounds)
		for row := bounds.Min.Y; row < bounds.Max.Y; row++ {
			for column := bounds.Min.X; column < bounds.Max.X; column++ {
				source.SetNRGBA(column, row, color.NRGBA{R: 170, G: 80, B: 30, A: 255})
			}
		}
		source.SetNRGBA(bounds.Max.X-1, bounds.Max.Y-1, color.NRGBA{R: 170, G: 80, B: 30, A: alpha})
		for _, wrapper := range []image.Image{
			plainColorImage{bounds: bounds, pixels: source},
			struct{ image.RGBA64Image }{source},
		} {
			for orientation := 2; orientation <= 8; orientation++ {
				oriented := normalizeOrientation(wrapper, orientation).(interface{ Opaque() bool })
				if got := oriented.Opaque(); got != source.Opaque() {
					test.Fatalf("%T orientation %d: opaque=%v alpha=%d", wrapper, orientation, got, alpha)
				}
			}
		}
	}
	empty := normalizeOrientation(struct{ image.Image }{image.NewNRGBA(image.Rectangle{})}, 6)
	if !empty.(interface{ Opaque() bool }).Opaque() {
		test.Fatal("empty image must be opaque")
	}
}

func TestOrientedImageOpacityFallbackPreserves16BitAlpha(test *testing.T) {
	source := image.NewNRGBA64(image.Rect(3, 5, 4, 6))
	for _, alpha := range []uint16{0xffff, 0xfffe, 0xff00, 0x8000, 0} {
		source.SetNRGBA64(3, 5, color.NRGBA64{R: 45000, G: 20000, B: 10000, A: alpha})
		for _, wrapper := range []image.Image{
			struct{ image.Image }{source},
			struct{ image.RGBA64Image }{source},
		} {
			for orientation := 2; orientation <= 8; orientation++ {
				oriented := normalizeOrientation(wrapper, orientation).(interface{ Opaque() bool })
				if got := oriented.Opaque(); got != (alpha == 0xffff) {
					test.Fatalf("%T orientation %d: opaque=%v alpha=%d", wrapper, orientation, got, alpha)
				}
			}
		}
	}
}

func TestOrientedPNGPreservesPixelSamples(test *testing.T) {
	bounds := image.Rect(3, 5, 6, 7)
	source8 := image.NewNRGBA(bounds)
	source16 := image.NewNRGBA64(bounds)
	alphas := []uint16{0xffff, 0xfffe, 0x8000, 0x0101, 1, 0}
	for index, alpha := range alphas {
		column, row := bounds.Min.X+index%3, bounds.Min.Y+index/3
		source8.SetNRGBA(column, row, color.NRGBA{R: 240, G: 110, B: 30, A: uint8(alpha >> 8)})
		source16.SetNRGBA64(column, row, color.NRGBA64{R: 61001, G: 28003, B: 7007, A: alpha})
	}
	for _, source := range []image.Image{source8, source16} {
		for _, wrapper := range []image.Image{source, struct{ image.Image }{source}} {
			for orientation := 2; orientation <= 8; orientation++ {
				oriented := normalizeOrientation(wrapper, orientation)
				var encoded bytes.Buffer
				if err := png.Encode(&encoded, oriented); err != nil {
					test.Fatal(err)
				}
				decoded, err := png.Decode(&encoded)
				if err != nil {
					test.Fatal(err)
				}
				if decoded.Bounds() != oriented.Bounds() {
					test.Fatalf("%T orientation %d: bounds=%v want=%v", wrapper, orientation, decoded.Bounds(), oriented.Bounds())
				}
				for row := 0; row < decoded.Bounds().Dy(); row++ {
					for column := 0; column < decoded.Bounds().Dx(); column++ {
						got := color.NRGBA64Model.Convert(decoded.At(column, row))
						want := color.NRGBA64Model.Convert(oriented.At(column, row))
						if got != want {
							test.Fatalf("%T orientation %d pixel (%d,%d)=%v want=%v", wrapper, orientation, column, row, got, want)
						}
					}
				}
			}
		}
	}
}

func TestOrientedImageRGBA64AtParity(test *testing.T) {
	sourceBounds := image.Rect(3, 5, 8, 9)
	source := image.NewNRGBA(sourceBounds)
	for row := sourceBounds.Min.Y; row < sourceBounds.Max.Y; row++ {
		for column := sourceBounds.Min.X; column < sourceBounds.Max.X; column++ {
			source.SetNRGBA(column, row, color.NRGBA{
				R: uint8(column * 29),
				G: uint8(row * 37),
				B: uint8((column + row) * 19),
				A: uint8(120 + (column*row)%135),
			})
		}
	}
	fallbackSource := plainColorImage{bounds: sourceBounds, pixels: source}
	for orientation := 2; orientation <= 8; orientation++ {
		fastOriented, ok := normalizeOrientation(source, orientation).(image.RGBA64Image)
		if !ok {
			test.Fatalf("orientation %d did not implement image.RGBA64Image", orientation)
		}
		slowOriented, ok := normalizeOrientation(fallbackSource, orientation).(image.RGBA64Image)
		if !ok {
			test.Fatalf("fallback orientation %d did not implement image.RGBA64Image", orientation)
		}
		bounds := fastOriented.Bounds()
		for row := bounds.Min.Y; row < bounds.Max.Y; row++ {
			for column := bounds.Min.X; column < bounds.Max.X; column++ {
				red, green, blue, alpha := fastOriented.At(column, row).RGBA()
				want := color.RGBA64{
					R: uint16(red),
					G: uint16(green),
					B: uint16(blue),
					A: uint16(alpha),
				}
				if got := fastOriented.RGBA64At(column, row); got != want {
					test.Fatalf("orientation %d fast RGBA64At(%d,%d) = %+v, want %+v", orientation, column, row, got, want)
				}
				if got := slowOriented.RGBA64At(column, row); got != want {
					test.Fatalf("orientation %d fallback RGBA64At(%d,%d) = %+v, want %+v", orientation, column, row, got, want)
				}
			}
		}
		allocs := testing.AllocsPerRun(100, func() {
			_ = fastOriented.RGBA64At(0, 0)
		})
		if allocs != 0 {
			test.Fatalf("orientation %d RGBA64At allocated %.1f objects per call", orientation, allocs)
		}
	}
}

func TestOrientForJPEGEncodePlanes(test *testing.T) {
	nrgba := image.NewNRGBA(image.Rect(0, 0, 4, 2))
	nrgba.SetNRGBA(0, 0, color.NRGBA{R: 240, G: 20, B: 30, A: 255})
	nrgba.SetNRGBA(3, 1, color.NRGBA{R: 20, G: 210, B: 230, A: 255})

	var rawJPEG bytes.Buffer
	if err := jpeg.Encode(&rawJPEG, nrgba, &jpeg.Options{Quality: 100}); err != nil {
		test.Fatal(err)
	}
	ycbcrSource, err := jpeg.Decode(&rawJPEG)
	if err != nil {
		test.Fatal(err)
	}
	graySource := image.NewGray(image.Rect(0, 0, 4, 2))
	graySource.SetGray(0, 0, color.Gray{Y: 200})
	graySource.SetGray(3, 1, color.Gray{Y: 40})
	grayParent := image.NewGray(image.Rect(-9, -7, 91, 101))
	for index := range grayParent.Pix {
		grayParent.Pix[index] = uint8(index*37 + 11)
	}
	paddedGray := grayParent.SubImage(image.Rect(-3, -1, 16, 8))
	narrowGray := grayParent.SubImage(image.Rect(3, 2, 4, 9))
	shortGray := grayParent.SubImage(image.Rect(2, 3, 9, 4))
	tiledGray := grayParent.SubImage(image.Rect(-3, -1, 64, 98))
	tallGray := grayParent.SubImage(image.Rect(3, 2, 4, 69))
	wideGray := grayParent.SubImage(image.Rect(2, 3, 69, 4))

	for _, source := range []image.Image{ycbcrSource, graySource, paddedGray, narrowGray, shortGray, tiledGray, tallGray, wideGray, nrgba} {
		if unchanged, err := orientForJPEGEncode(context.Background(), source, 1); err != nil || unchanged != source {
			test.Fatalf("orientation 1 must return original %T", source)
		}
		for orientation := 2; orientation <= 8; orientation++ {
			got, err := orientForJPEGEncode(context.Background(), source, orientation)
			if err != nil {
				test.Fatal(err)
			}
			want := normalizeOrientation(source, orientation)
			if got.Bounds() != want.Bounds() {
				test.Fatalf("%T orientation %d bounds = %v, want %v", source, orientation, got.Bounds(), want.Bounds())
			}
			bounds := want.Bounds()
			for row := bounds.Min.Y; row < bounds.Max.Y; row++ {
				for column := bounds.Min.X; column < bounds.Max.X; column++ {
					gotR, gotG, gotB, _ := got.At(column, row).RGBA()
					wantR, wantG, wantB, _ := want.At(column, row).RGBA()
					if gotR>>8 != wantR>>8 || gotG>>8 != wantG>>8 || gotB>>8 != wantB>>8 {
						test.Fatalf("%T orientation %d pixel (%d,%d) = (%d,%d,%d), want (%d,%d,%d)",
							source, orientation, column, row,
							gotR>>8, gotG>>8, gotB>>8, wantR>>8, wantG>>8, wantB>>8)
					}
				}
			}
		}
	}
}

func TestOrientedYCbCrPreservesSamplesAndJPEG(test *testing.T) {
	for _, scenario := range []struct {
		name      string
		bounds    image.Rectangle
		ratio     image.YCbCrSubsampleRatio
		wantRatio image.YCbCrSubsampleRatio
		padded    bool
	}{
		{"aligned-420", image.Rect(0, 0, 18, 10), image.YCbCrSubsampleRatio420, image.YCbCrSubsampleRatio420, true},
		{"minimal-420", image.Rect(0, 0, 2, 2), image.YCbCrSubsampleRatio420, image.YCbCrSubsampleRatio420, false},
		{"single-pixel-420", image.Rect(0, 0, 1, 1), image.YCbCrSubsampleRatio420, image.YCbCrSubsampleRatio444, false},
		{"single-column-420", image.Rect(0, 0, 1, 7), image.YCbCrSubsampleRatio420, image.YCbCrSubsampleRatio444, true},
		{"single-row-420", image.Rect(0, 0, 7, 1), image.YCbCrSubsampleRatio420, image.YCbCrSubsampleRatio444, true},
		{"odd-width-420", image.Rect(0, 0, 19, 10), image.YCbCrSubsampleRatio420, image.YCbCrSubsampleRatio444, true},
		{"odd-height-420", image.Rect(0, 0, 18, 11), image.YCbCrSubsampleRatio420, image.YCbCrSubsampleRatio444, true},
		{"odd-size-420", image.Rect(0, 0, 19, 11), image.YCbCrSubsampleRatio420, image.YCbCrSubsampleRatio444, false},
		{"translated-420", image.Rect(2, 2, 20, 12), image.YCbCrSubsampleRatio420, image.YCbCrSubsampleRatio444, true},
		{"negative-origin-420", image.Rect(-4, -2, 14, 8), image.YCbCrSubsampleRatio420, image.YCbCrSubsampleRatio444, false},
		{"tile-aligned-420", image.Rect(0, 0, 66, 98), image.YCbCrSubsampleRatio420, image.YCbCrSubsampleRatio420, false},
		{"tile-padded-420", image.Rect(0, 0, 66, 98), image.YCbCrSubsampleRatio420, image.YCbCrSubsampleRatio420, true},
		{"tile-odd-420", image.Rect(0, 0, 67, 99), image.YCbCrSubsampleRatio420, image.YCbCrSubsampleRatio444, true},
		{"tile-translated-420", image.Rect(3, 5, 70, 104), image.YCbCrSubsampleRatio420, image.YCbCrSubsampleRatio444, true},
		{"tile-negative-origin-420", image.Rect(-5, -7, 62, 92), image.YCbCrSubsampleRatio420, image.YCbCrSubsampleRatio444, false},
		{"tile-444", image.Rect(-3, 1, 64, 100), image.YCbCrSubsampleRatio444, image.YCbCrSubsampleRatio444, false},
		{"444", image.Rect(0, 0, 18, 10), image.YCbCrSubsampleRatio444, image.YCbCrSubsampleRatio444, false},
		{"422", image.Rect(0, 0, 18, 10), image.YCbCrSubsampleRatio422, image.YCbCrSubsampleRatio444, false},
		{"440", image.Rect(0, 0, 18, 10), image.YCbCrSubsampleRatio440, image.YCbCrSubsampleRatio444, false},
		{"411", image.Rect(0, 0, 18, 10), image.YCbCrSubsampleRatio411, image.YCbCrSubsampleRatio444, false},
		{"410", image.Rect(0, 0, 18, 10), image.YCbCrSubsampleRatio410, image.YCbCrSubsampleRatio444, false},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			source := image.NewYCbCr(scenario.bounds, scenario.ratio)
			if scenario.padded {
				parent := image.NewYCbCr(image.Rect(0, 0, max(32, scenario.bounds.Max.X+5), max(32, scenario.bounds.Max.Y+3)), scenario.ratio)
				source = parent.SubImage(scenario.bounds).(*image.YCbCr)
			}
			for index := range source.Y {
				source.Y[index] = uint8(index*37 + 11)
			}
			for index := range source.Cb {
				source.Cb[index] = uint8(index*17 + 57)
				source.Cr[index] = uint8(index*29 + 91)
			}
			beforeY, beforeCb, beforeCr := bytes.Clone(source.Y), bytes.Clone(source.Cb), bytes.Clone(source.Cr)
			for orientation := 2; orientation <= 8; orientation++ {
				test.Run(strconv.Itoa(orientation), func(test *testing.T) {
					oriented, err := orientForJPEGEncode(context.Background(), source, orientation)
					if err != nil {
						test.Fatal(err)
					}
					output := oriented.(*image.YCbCr)
					expected := normalizeOrientation(source, orientation)
					if output.Bounds() != expected.Bounds() || output.SubsampleRatio != scenario.wantRatio {
						test.Fatalf("bounds=%v ratio=%v, want %v %v", output.Bounds(), output.SubsampleRatio, expected.Bounds(), scenario.wantRatio)
					}
					reference := image.NewYCbCr(expected.Bounds(), image.YCbCrSubsampleRatio444)
					for row := 0; row < output.Bounds().Dy(); row++ {
						for column := 0; column < output.Bounds().Dx(); column++ {
							want := expected.At(column, row).(color.YCbCr)
							if got := output.YCbCrAt(column, row); got != want {
								test.Fatalf("pixel (%d,%d)=%v, want %v", column, row, got, want)
							}
							reference.Y[reference.YOffset(column, row)] = want.Y
							chromaIndex := reference.COffset(column, row)
							reference.Cb[chromaIndex], reference.Cr[chromaIndex] = want.Cb, want.Cr
						}
					}
					var actualJPEG, referenceJPEG bytes.Buffer
					options := &jpeg.Options{Quality: 80}
					if err := jpeg.Encode(&actualJPEG, output, options); err != nil {
						test.Fatal(err)
					}
					if err := jpeg.Encode(&referenceJPEG, reference, options); err != nil {
						test.Fatal(err)
					}
					if !bytes.Equal(actualJPEG.Bytes(), referenceJPEG.Bytes()) {
						test.Fatal("JPEG output differs from full-resolution chroma reference")
					}
				})
			}
			if !bytes.Equal(source.Y, beforeY) || !bytes.Equal(source.Cb, beforeCb) || !bytes.Equal(source.Cr, beforeCr) {
				test.Fatal("source planes changed")
			}
		})
	}
}
