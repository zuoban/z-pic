package compress

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"path/filepath"
	"testing"
)

func TestResolveOutputFormatMatrix(t *testing.T) {
	for _, source := range []struct {
		format string
		keep   [2]string // lossy, lossless
	}{
		{"jpeg", [2]string{"jpeg", "jpeg"}},
		{"png", [2]string{"png", "png"}},
		{"webp", [2]string{"webp", "webp"}},
		{"heic", [2]string{"jpeg", "png"}},
		{"avif", [2]string{"avif", "avif"}},
	} {
		for _, target := range []struct {
			name   string
			option OutputFormat
			want   [2]string
		}{
			{"keep", OutputFormatKeep, source.keep},
			{"jpeg", OutputFormatJPEG, [2]string{"jpeg", "jpeg"}},
			{"png", OutputFormatPNG, [2]string{"png", "png"}},
			{"webp", OutputFormatWebP, [2]string{"webp", "webp"}},
			{"avif", OutputFormatAVIF, [2]string{"avif", "avif"}},
		} {
			for mode, want := range target.want {
				lossless := mode == 1
				t.Run(fmt.Sprintf("%s/%s/lossless=%t", source.format, target.name, lossless), func(t *testing.T) {
					if got := ResolveOutputFormat(source.format, target.option, lossless); got != want {
						t.Fatalf("resolved format = %q, want %q", got, want)
					}
				})
			}
		}
	}
}

func TestConvertPNGToJPEG(t *testing.T) {
	const tile = 16
	source := image.NewNRGBA(image.Rect(0, 0, 3*tile, 2*tile))
	colors := [2][3]color.NRGBA{
		{{R: 180}, {R: 32, G: 84, B: 140, A: 128}, {R: 179, G: 57, B: 38, A: 255}},
		{{G: 228}, {R: 122, G: 20, B: 44, A: 128}, {R: 35, G: 118, B: 186, A: 255}},
	}
	var centers []image.Point
	for row, line := range colors {
		for column, c := range line {
			x, y := column*tile, row*tile
			draw.Draw(source, image.Rect(x, y, x+tile, y+tile), image.NewUniform(c), image.Point{}, draw.Src)
			centers = append(centers, image.Pt(x+tile/2, y+tile/2))
		}
	}
	icc := syntheticRGBICC()
	compact := buildPNGWithMetadata(t, source, icc)
	// The cross-format overwrite checks need a source larger than the JPEG.
	padded := insertPNGChunk(compact, "tEXt", append([]byte("Comment\x00"), bytes.Repeat([]byte{'x'}, 16<<10)...))

	for _, tc := range []struct {
		name     string
		data     []byte
		metadata MetadataPolicy
		lossless bool
		wantICC  []byte
	}{
		{"keep-icc", padded, MetadataKeepICC, false, icc},
		{"strip", padded, MetadataStrip, false, nil},
		{"lossless-option-with-larger-output", compact, MetadataKeepICC, true, icc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeFixture(t, "source.png", tc.data)
			result, err := File(t.Context(), path, Options{
				Quality: 95, Lossless: tc.lossless, OutputFormat: OutputFormatJPEG, Metadata: tc.metadata,
			})
			if err != nil {
				t.Fatal(err)
			}
			decoded := assertConversionResult(t, result, tc.data, "jpeg", source.Bounds())
			assertConversionSamples(t, decoded, conversionOverWhite(source), centers, color.RGBAModel, 12)
			assertConvertedJPEGICC(t, result.Data, tc.wantICC)
			if tc.lossless {
				if len(result.Data) <= len(tc.data) {
					t.Fatalf("fixture must produce a larger JPEG: %d <= %d", len(result.Data), len(tc.data))
				}
			} else {
				assertConversionSavings(t, result, tc.data)
			}
			assertConversionOverwriteRejected(t, result, path, tc.data)
		})
	}

	t.Run("exif-orientation", func(t *testing.T) {
		data := insertPNGChunk(pngFixture(t, source, png.DefaultCompression), "eXIf", exifTIFF(binary.LittleEndian, 6))
		path := writeFixture(t, "oriented.png", data)
		result, err := File(t.Context(), path, Options{Quality: 95, OutputFormat: OutputFormatJPEG})
		if err != nil {
			t.Fatal(err)
		}
		upright := clockwiseConversionImage(source)
		decoded := assertConversionResult(t, result, data, "jpeg", upright.Bounds())
		var rotatedCenters []image.Point
		for _, p := range centers {
			rotatedCenters = append(rotatedCenters, image.Pt(source.Bounds().Dy()-1-p.Y, p.X))
		}
		assertConversionSamples(t, decoded, conversionOverWhite(upright), rotatedCenters, color.RGBAModel, 12)
		if got := jpegOrientation(result.Data); got != 1 {
			t.Errorf("JPEG orientation tag = %d after baking, want 1", got)
		}
	})
}

func TestConvertWebPToJPEGAndPNG(t *testing.T) {
	base := webpFixture(t, "alpha") // 96x64 with transparent, translucent and opaque columns.
	icc := syntheticRGBICC()
	data := extendedWebP(base, 0x38, 96, 64) // ICC, alpha and EXIF flags.
	// ICCP comes before the existing ALPH and VP8 chunks.
	data = append(append(bytes.Clone(data[:30]), webpChunk("ICCP", icc)...), data[30:]...)
	data = appendWebPChunk(data, "EXIF", exifTIFF(binary.LittleEndian, 6))
	data = appendWebPChunk(data, "JUNK", make([]byte, 32<<10))
	source, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	upright := clockwiseConversionImage(source)
	for _, format := range []OutputFormat{OutputFormatJPEG, OutputFormatPNG} {
		t.Run(string(format), func(t *testing.T) {
			if format == OutputFormatPNG {
				requireNativePNGTools(t)
			}
			for _, metadata := range []MetadataPolicy{MetadataKeepICC, MetadataStrip} {
				t.Run(string(metadata), func(t *testing.T) {
					path := writeFixture(t, "oriented.webp", data)
					result, err := File(t.Context(), path, Options{
						Quality: 100, Lossless: format == OutputFormatPNG, OutputFormat: format, Metadata: metadata, Threads: 1,
					})
					if err != nil {
						t.Fatal(err)
					}
					decoded := assertConversionResult(t, result, data, string(format), upright.Bounds())
					var wantICC []byte
					if metadata == MetadataKeepICC {
						wantICC = icc
					}
					if format == OutputFormatPNG {
						assertImagesNear(t, upright, decoded, 2)
						for y := range upright.Bounds().Dy() {
							for x := range upright.Bounds().Dx() {
								_, _, _, got := decoded.At(x, y).RGBA()
								if want := upright.NRGBAAt(x, y).A; got != uint32(want)*0x101 {
									t.Fatalf("PNG alpha at (%d,%d) = %d, want %d", x, y, got>>8, want)
								}
							}
						}
						assertConvertedPNGICC(t, result.Data, wantICC)
					} else {
						white := conversionOverWhite(upright)
						// Transparency becomes rows after rotation. Check luma at all
						// sampled opacities, allowing for JPEG chroma subsampling.
						transparent := []image.Point{{10, 12}, {20, 24}, {40, 48}, {51, 72}}
						assertConversionSamples(t, decoded, white, transparent, color.RGBAModel, 40)
						mixed := append(transparent, image.Pt(26, 88), image.Pt(43, 92))
						assertConversionSamples(t, decoded, white, mixed, color.GrayModel, 10)
						if got := jpegOrientation(result.Data); got != 1 {
							t.Errorf("JPEG orientation tag = %d after baking, want 1", got)
						}
						assertConvertedJPEGICC(t, result.Data, wantICC)
					}
					assertConversionSavings(t, result, data)
					assertConversionOverwriteRejected(t, result, path, data)
				})
			}
		})
	}
}

func TestConvertJPEGToPNG(t *testing.T) {
	requireNativePNGTools(t)
	gray := image.NewGray(image.Rect(0, 0, 48, 32))
	for y := range gray.Bounds().Dy() {
		for x := range gray.Bounds().Dx() {
			gray.SetGray(x, y, color.Gray{Y: uint8(20 + 3*x + 2*y)})
		}
	}
	for _, tc := range []struct {
		name  string
		space string
		img   image.Image
	}{
		{"rgb", "RGB ", samplePhotoImage(48, 32)},
		{"gray", "GRAY", gray},
	} {
		t.Run(tc.name, func(t *testing.T) {
			icc := makeICCHeaderPayload(tc.space)
			data := makeJPEGWithICCAndOrient(t, tc.img, icc, 0)
			data = withEXIF(data, append(exifTIFF(binary.BigEndian, 6), make([]byte, 16<<10)...))
			source, _, err := image.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			upright := clockwiseConversionImage(source)
			for _, metadata := range []MetadataPolicy{MetadataKeepICC, MetadataStrip} {
				t.Run(string(metadata), func(t *testing.T) {
					path := writeFixture(t, "oriented.jpg", data)
					result, err := Bytes(t.Context(), data, path, Options{
						Quality: 90, Lossless: true, OutputFormat: OutputFormatPNG, Metadata: metadata, Threads: 1,
					})
					if err != nil {
						t.Fatal(err)
					}
					decoded := assertConversionResult(t, result, data, "png", upright.Bounds())
					assertImagesNear(t, upright, decoded, 1)
					var wantICC []byte
					if metadata == MetadataKeepICC {
						wantICC = icc
						if tc.space == "GRAY" {
							// IHDR color types 0 and 4 can carry a GRAY profile.
							if colorType := result.Data[25]; colorType != 0 && colorType != 4 {
								t.Errorf("PNG color type = %d with a GRAY ICC profile", colorType)
							}
						}
					}
					assertConvertedPNGICC(t, result.Data, wantICC)
					assertConversionSavings(t, result, data)
					assertConversionOverwriteRejected(t, result, path, data)
				})
			}
		})
	}
}

func TestConvertSameFormatOverwrite(t *testing.T) {
	for _, format := range []OutputFormat{OutputFormatJPEG, OutputFormatPNG} {
		t.Run(string(format), func(t *testing.T) {
			var data []byte
			var bounds image.Rectangle
			options := Options{Quality: 35, OutputFormat: format, Threads: 1}
			if format == OutputFormatPNG {
				requireNativePNGTools(t)
				img := samplePhotoImage(48, 32)
				bounds = img.Bounds()
				data = pngFixture(t, img, png.NoCompression)
				options.Lossless = true
			} else {
				data = jpegFixture(t, 100)
				bounds = image.Rect(0, 0, 128, 96)
			}
			path := writeFixture(t, "source."+string(format), data)
			result, err := File(t.Context(), path, options)
			if err != nil {
				t.Fatal(err)
			}
			assertConversionResult(t, result, data, string(format), bounds)
			assertConversionSavings(t, result, data)
			assertOverwriteFile(t, path, data)
			if err := result.Overwrite(); err != nil {
				t.Fatal(err)
			}
			assertOverwriteFile(t, path, result.Data)
			assertNoOverwriteTemps(t, filepath.Dir(path))
		})
	}
}

func TestConvertHEICExplicitFormats(t *testing.T) {
	source := samplePhotoImage(96, 72)
	path, data := makeRealHEICFixture(t, source)
	for _, format := range []OutputFormat{OutputFormatJPEG, OutputFormatPNG, OutputFormatWebP} {
		t.Run(string(format), func(t *testing.T) {
			switch format {
			case OutputFormatPNG:
				requireNativePNGTools(t)
			case OutputFormatWebP:
				requireWebPEncoder(t)
			}
			// For JPEG and PNG choose the lossless setting that would select
			// the other format if the explicit target were ignored.
			result, err := File(t.Context(), path, Options{
				Quality: 85, Lossless: format == OutputFormatJPEG, OutputFormat: format, Threads: 1,
			})
			if err != nil {
				t.Fatal(err)
			}
			assertConversionResult(t, result, data, string(format), source.Bounds())
			assertConversionOverwriteRejected(t, result, path, data)
		})
	}
}

func assertConversionResult(t *testing.T, result *Result, source []byte, format string, bounds image.Rectangle) image.Image {
	t.Helper()
	if result.Format != format || result.Width != bounds.Dx() || result.Height != bounds.Dy() {
		t.Fatalf("result = %s %dx%d, want %s %v", result.Format, result.Width, result.Height, format, bounds.Size())
	}
	if result.Unchanged || bytes.Equal(result.Data, source) {
		t.Fatal("conversion returned the original image")
	}
	if result.OriginalSize != int64(len(source)) || result.OutputSize != int64(len(result.Data)) {
		t.Errorf("result sizes = (%d, %d), want (%d, %d)", result.OriginalSize, result.OutputSize, len(source), len(result.Data))
	}
	decoded, actualFormat, err := image.Decode(bytes.NewReader(result.Data))
	if err != nil {
		t.Fatalf("decode %s output: %v", format, err)
	}
	if actualFormat != format || decoded.Bounds() != bounds {
		t.Fatalf("decoded output = %s %v, want %s %v", actualFormat, decoded.Bounds(), format, bounds)
	}
	return decoded
}

func assertConversionSavings(t *testing.T, result *Result, source []byte) {
	t.Helper()
	if len(result.Data) >= len(source) {
		t.Fatalf("overwrite fixture must save bytes: output %d, source %d", len(result.Data), len(source))
	}
}

func assertConversionOverwriteRejected(t *testing.T, result *Result, path string, source []byte) {
	t.Helper()
	if err := result.Overwrite(); err == nil {
		t.Error("format conversion allowed overwriting the source")
	}
	assertOverwriteFile(t, path, source)
	assertNoOverwriteTemps(t, filepath.Dir(path))
}

func clockwiseConversionImage(source image.Image) *image.NRGBA {
	bounds := source.Bounds()
	upright := image.NewNRGBA(image.Rect(0, 0, bounds.Dy(), bounds.Dx()))
	for y := range bounds.Dy() {
		for x := range bounds.Dx() {
			c := source.At(bounds.Min.X+x, bounds.Min.Y+y)
			if ny, ok := c.(color.NYCbCrA); ok {
				if ny.A == 0 {
					upright.SetNRGBA(bounds.Dy()-1-y, x, color.NRGBA{})
				} else {
					r, g, b := color.YCbCrToRGB(ny.Y, ny.Cb, ny.Cr)
					upright.SetNRGBA(bounds.Dy()-1-y, x, color.NRGBA{R: r, G: g, B: b, A: ny.A})
				}
				continue
			}
			upright.Set(bounds.Dy()-1-y, x, c)
		}
	}
	return upright
}

func conversionOverWhite(source image.Image) *image.RGBA {
	out := image.NewRGBA(source.Bounds())
	draw.Draw(out, out.Bounds(), image.White, image.Point{}, draw.Src)
	draw.Draw(out, out.Bounds(), source, source.Bounds().Min, draw.Over)
	return out
}

func assertConversionSamples(t *testing.T, got, want image.Image, points []image.Point, model color.Model, tolerance int) {
	t.Helper()
	for _, point := range points {
		gr, gg, gb, ga := model.Convert(got.At(point.X, point.Y)).RGBA()
		wr, wg, wb, wa := model.Convert(want.At(point.X, point.Y)).RGBA()
		for index, channel := range [3]uint32{gr, gg, gb} {
			got8, want8 := int(channel>>8), int([3]uint32{wr, wg, wb}[index]>>8)
			if ga != wa || got8 < want8-tolerance || got8 > want8+tolerance {
				t.Fatalf("pixel %v = (%d,%d,%d,%d), want (%d,%d,%d,%d) with color tolerance %d",
					point, gr>>8, gg>>8, gb>>8, ga>>8, wr>>8, wg>>8, wb>>8, wa>>8, tolerance)
			}
		}
	}
}

func assertConvertedJPEGICC(t *testing.T, data, want []byte) {
	t.Helper()
	segments, _ := jpegICCSegments(data)
	var profile []byte
	for _, segment := range segments {
		profile = append(profile, segment[18:]...)
	}
	if !bytes.Equal(profile, want) {
		t.Fatalf("JPEG ICC differs from expected profile: got %d bytes, want %d", len(profile), len(want))
	}
}

func assertConvertedPNGICC(t *testing.T, data, want []byte) {
	t.Helper()
	if pngHasChunk(data, "eXIf") {
		t.Error("PNG contains EXIF after baking the source orientation")
	}
	if want == nil {
		if pngHasChunk(data, "iCCP") {
			t.Error("PNG still contains iCCP after stripping metadata")
		}
		return
	}
	for offset := 8; offset <= len(data)-12; {
		length := int(binary.BigEndian.Uint32(data[offset : offset+4]))
		if length < 0 || length > len(data)-offset-12 {
			t.Fatal("invalid PNG chunk length")
		}
		if string(data[offset+4:offset+8]) == "iCCP" {
			payload := data[offset+8 : offset+8+length]
			nameEnd := bytes.IndexByte(payload, 0)
			if nameEnd < 1 || nameEnd+2 >= len(payload) || payload[nameEnd+1] != 0 {
				t.Fatal("invalid iCCP header")
			}
			reader, err := zlib.NewReader(bytes.NewReader(payload[nameEnd+2:]))
			if err != nil {
				t.Fatalf("open iCCP profile: %v", err)
			}
			defer reader.Close()
			profile, err := io.ReadAll(io.LimitReader(reader, int64(len(want))+1))
			if err != nil || !bytes.Equal(profile, want) {
				t.Fatalf("PNG ICC differs from expected profile: got %d bytes, want %d, read error %v", len(profile), len(want), err)
			}
			return
		}
		offset += 12 + length
	}
	t.Fatal("PNG output is missing iCCP")
}
