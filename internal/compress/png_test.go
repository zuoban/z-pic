package compress

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"zpic/internal/pngtools"
)

func assertPNGTransparency(t *testing.T, data []byte) {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	transparent, translucent, opaque := false, false, false
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			_, _, _, alpha := img.At(x, y).RGBA()
			transparent = transparent || alpha == 0
			translucent = translucent || (alpha > 0 && alpha < 0xffff)
			opaque = opaque || alpha == 0xffff
		}
	}
	if !transparent || !translucent || !opaque {
		t.Fatalf("alpha range: transparent=%v translucent=%v opaque=%v", transparent, translucent, opaque)
	}
}

func assertPNGResult(t *testing.T, result *Result, input []byte, width, height int) {
	t.Helper()
	if result.Format != "png" || result.Width != width || result.Height != height {
		t.Fatalf("result format/dimensions: %s %dx%d", result.Format, result.Width, result.Height)
	}
	if result.OriginalSize != int64(len(input)) || result.OutputSize != int64(len(result.Data)) ||
		result.OutputSize > result.OriginalSize {
		t.Fatalf("invalid sizes: input=%d original=%d output=%d data=%d",
			len(input), result.OriginalSize, result.OutputSize, len(result.Data))
	}
	unchanged := bytes.Equal(result.Data, input)
	if result.Unchanged != unchanged {
		t.Fatalf("Unchanged=%v, byte equality=%v", result.Unchanged, unchanged)
	}
	if err := validatePNGOutput(context.Background(), result.Data, image.Config{Width: width, Height: height}); err != nil {
		t.Fatalf("invalid result PNG: %v", err)
	}
}

func TestPNGOptionsDefaultsAndValidBoundaries(t *testing.T) {
	want := PNGOptions{MinQuality: 65, MaxQuality: 85, Colors: 256, Speed: 3}
	if got := DefaultPNGOptions(); got != want {
		t.Fatalf("default PNG options: got %+v, want %+v", got, want)
	}
	for _, test := range []struct {
		name    string
		options PNGOptions
	}{
		{"zero uses defaults", PNGOptions{}},
		{"explicit defaults", want},
		{"lower boundaries", PNGOptions{MinQuality: 0, MaxQuality: 1, Colors: 2, Speed: 1}},
		{"upper boundaries", PNGOptions{MinQuality: 100, MaxQuality: 100, Colors: 256, Speed: 11}},
		{"zero minimum quality is explicit", PNGOptions{MinQuality: 0, MaxQuality: 100, Colors: 16, Speed: 3}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.options.Validate(); err != nil {
				t.Fatalf("valid PNG options %+v rejected: %v", test.options, err)
			}
		})
	}
}

func TestPNGOptionsInvalidRejectedBeforeInput(t *testing.T) {
	for _, test := range []struct {
		name    string
		options PNGOptions
	}{
		{"minimum below zero", PNGOptions{MinQuality: -1, MaxQuality: 85, Colors: 256, Speed: 3}},
		{"minimum above 100", PNGOptions{MinQuality: 101, MaxQuality: 100, Colors: 256, Speed: 3}},
		{"maximum zero", PNGOptions{MinQuality: 0, MaxQuality: 0, Colors: 256, Speed: 3}},
		{"maximum below zero", PNGOptions{MinQuality: 0, MaxQuality: -1, Colors: 256, Speed: 3}},
		{"maximum above 100", PNGOptions{MinQuality: 65, MaxQuality: 101, Colors: 256, Speed: 3}},
		{"minimum exceeds maximum", PNGOptions{MinQuality: 86, MaxQuality: 85, Colors: 256, Speed: 3}},
		{"colors below two", PNGOptions{MinQuality: 65, MaxQuality: 85, Colors: 1, Speed: 3}},
		{"colors above 256", PNGOptions{MinQuality: 65, MaxQuality: 85, Colors: 257, Speed: 3}},
		{"speed below one", PNGOptions{MinQuality: 65, MaxQuality: 85, Colors: 256, Speed: -1}},
		{"speed above eleven", PNGOptions{MinQuality: 65, MaxQuality: 85, Colors: 256, Speed: 12}},
		{"missing colors", PNGOptions{MinQuality: 65, MaxQuality: 85, Speed: 3}},
		{"missing speed", PNGOptions{MinQuality: 65, MaxQuality: 85, Colors: 256}},
		{"only minimum", PNGOptions{MinQuality: 65}},
		{"only maximum", PNGOptions{MaxQuality: 85}},
		{"only colors", PNGOptions{Colors: 16}},
		{"only speed", PNGOptions{Speed: 3}},
	} {
		t.Run(test.name, func(t *testing.T) {
			validationErr := test.options.Validate()
			if validationErr == nil {
				t.Fatalf("invalid PNG options accepted: %+v", test.options)
			}
			// File cannot read this path; Bytes cannot decode nil. The options
			// error must win over either input failure, before any native work.
			path := filepath.Join(t.TempDir(), "missing.png")
			for _, method := range inputMethods(nil) {
				t.Run(method.name, func(t *testing.T) {
					result, err := method.run(context.Background(), path, Options{Quality: 80, PNG: test.options})
					if result != nil || err == nil || !strings.Contains(err.Error(), validationErr.Error()) {
						t.Fatalf("expected early options error %q, got result=%v err=%v", validationErr, result, err)
					}
				})
			}
		})
	}
}

func TestPNGCustomOptionsReachQuantizer(t *testing.T) {
	// Match the native integration suite's supported bundle policy. A missing
	// or broken binary on the supported platform must fail this test.
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("native assets currently bundled only for darwin/arm64")
	}
	data := pngScreenshotFixture(t)
	before := append([]byte(nil), data...)
	if count := pngColorCount(t, data); count <= 256 {
		t.Fatalf("parameter fixture must have more than 256 colors, got %d", count)
	}
	path := writeFixture(t, "configurable.png", data)
	defaultResult, err := Bytes(context.Background(), data, path, Options{Quality: 80})
	if err != nil {
		t.Fatal(err)
	}
	assertPNGResult(t, defaultResult, before, 320, 192)
	defaultColors := pngColorCount(t, defaultResult.Data)
	if defaultColors <= 16 {
		t.Fatalf("default fixture cannot distinguish a 16-color limit: %d colors", defaultColors)
	}
	custom := PNGOptions{MinQuality: 0, MaxQuality: 100, Colors: 16, Speed: 3}
	for _, method := range inputMethods(data) {
		t.Run(method.name, func(t *testing.T) {
			result, err := method.run(context.Background(), path, Options{Quality: 80, PNG: custom})
			if err != nil {
				t.Fatal(err)
			}
			assertPNGResult(t, result, before, 320, 192)
			count := pngColorCount(t, result.Data)
			if count > 16 {
				t.Fatalf("configured 16-color limit was not applied: %d colors", count)
			}
			if bytes.Equal(result.Data, defaultResult.Data) {
				t.Fatal("custom options produced the default output")
			}
			current, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(current, before) || !bytes.Equal(data, before) {
				t.Fatalf("configured compression mutated source file or snapshot: %v", err)
			}
			t.Logf("synthetic screenshot: default=%d colors/%d bytes custom=%d colors/%d bytes",
				defaultColors, len(defaultResult.Data), count, len(result.Data))
		})
	}
}

func pngColorCount(t *testing.T, data []byte) int {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	colors := make(map[color.NRGBA64]struct{})
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			colors[color.NRGBA64Model.Convert(img.At(x, y)).(color.NRGBA64)] = struct{}{}
		}
	}
	return len(colors)
}

func TestPNGSupportsPaletteTransparency(t *testing.T) {
	palette := color.Palette{
		color.NRGBA{R: 233, G: 17, B: 91, A: 0},
		color.NRGBA{R: 13, G: 129, B: 241, A: 1},
		color.NRGBA{R: 57, G: 171, B: 23, A: 128},
		color.NRGBA{R: 251, G: 199, B: 37, A: 255},
	}
	img := image.NewPaletted(image.Rect(0, 0, 64, 48), palette)
	for y := 0; y < 48; y++ {
		for x := 0; x < 64; x++ {
			img.SetColorIndex(x, y, uint8((x/4+y/3)%len(palette)))
		}
	}
	data := pngFixture(t, img, png.NoCompression)
	before := append([]byte(nil), data...)
	path := writeFixture(t, "palette.png", data)
	for _, method := range inputMethods(data) {
		t.Run(method.name, func(t *testing.T) {
			result, err := method.run(context.Background(), path, Options{Quality: 80})
			if err != nil {
				t.Fatal(err)
			}
			assertPNGResult(t, result, data, 64, 48)
			assertPNGTransparency(t, result.Data)
			current, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(current, before) || !bytes.Equal(data, before) {
				t.Fatalf("palette source file or snapshot mutated: %v", err)
			}
		})
	}
}

// This deterministic synthetic screenshot uses flat panels and smooth color
// ramps with subtle texture, over 256 colors, and a transparent border. Starting
// from BestCompression ensures lossy savings cannot rely on an uncompressed IDAT.
func pngScreenshotFixture(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 320, 192))
	noise := uint32(0x12345678)
	for y := 0; y < 192; y++ {
		for x := 0; x < 320; x++ {
			noise ^= noise << 13
			noise ^= noise >> 17
			noise ^= noise << 5
			c := color.NRGBA{
				R: uint8(30+x/2) + uint8(noise&7),
				G: uint8(40+y) + uint8((noise>>3)&7),
				B: uint8(80+(x+y)/4) + uint8((noise>>6)&7), A: 255,
			}
			if x < 8 || y < 8 {
				c = color.NRGBA{}
			} else if x > 24 && x < 120 && y > 32 && y < 150 {
				c = color.NRGBA{R: 235, G: 239, B: 245, A: 255}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	return pngFixture(t, img, png.BestCompression)
}

func TestPNGDefaultQuantizesColorsPreservesDimensionsAndSource(t *testing.T) {
	data := pngScreenshotFixture(t)
	before := append([]byte(nil), data...)
	original, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	colors := make(map[color.NRGBA64]bool)
	for y := 0; y < 192; y++ {
		for x := 0; x < 320; x++ {
			colors[color.NRGBA64Model.Convert(original.At(x, y)).(color.NRGBA64)] = true
		}
	}
	if len(colors) <= 256 {
		t.Fatalf("fixture has only %d colors", len(colors))
	}
	path := writeFixture(t, "screenshot.png", data)
	for _, method := range inputMethods(data) {
		t.Run(method.name, func(t *testing.T) {
			result, err := method.run(context.Background(), path, Options{Quality: 80})
			if err != nil {
				t.Fatal(err)
			}
			assertPNGResult(t, result, data, 320, 192)
			if result.OutputSize >= result.OriginalSize {
				t.Fatal("lossy synthetic screenshot did not shrink")
			}
			decoded, err := png.Decode(bytes.NewReader(result.Data))
			if err != nil {
				t.Fatal(err)
			}
			changed, transparent, opaque := false, false, false
			outputColors := make(map[color.NRGBA64]bool)
			for y := 0; y < 192; y++ {
				for x := 0; x < 320; x++ {
					got := color.NRGBA64Model.Convert(decoded.At(x, y)).(color.NRGBA64)
					outputColors[got] = true
					want := color.NRGBA64Model.Convert(original.At(x, y)).(color.NRGBA64)
					if want.A == 0xffff && (got.R != want.R || got.G != want.G || got.B != want.B) {
						changed = true
					}
					transparent = transparent || got.A == 0
					opaque = opaque || got.A == 0xffff
				}
			}
			if !changed || !transparent || !opaque {
				t.Fatalf("changed opaque colors=%v transparent=%v opaque=%v", changed, transparent, opaque)
			}
			if len(outputColors) > 256 {
				t.Fatalf("default PNG path did not quantize: %d output colors", len(outputColors))
			}
			current, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(current, before) || !bytes.Equal(data, before) {
				t.Fatalf("source file or snapshot mutated: %v", err)
			}
			t.Logf("synthetic screenshot: std BestCompression=%d lossy=%d bytes", len(data), len(result.Data))
		})
	}
}

func TestPNGQualityGateRefusalRemainsUsable(t *testing.T) {
	// Independent random RGB samples cannot meet the fixed quantization
	// quality gate with only 256 colors.
	img := image.NewNRGBA(image.Rect(0, 0, 96, 64))
	state := uint32(0x87654321)
	for y := 0; y < 64; y++ {
		for x := 0; x < 96; x++ {
			state ^= state << 13
			state ^= state >> 17
			state ^= state << 5
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(state), G: uint8(state >> 8), B: uint8(state >> 16), A: 255})
		}
	}
	data := pngFixture(t, img, png.NoCompression)
	before := append([]byte(nil), data...)
	quantized, err := pngtools.Quantize(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(quantized, before) {
		t.Fatal("quality-gate fixture unexpectedly accepted quantization")
	}
	path := writeFixture(t, "quality-gate.png", before)
	for _, method := range inputMethods(data) {
		t.Run(method.name, func(t *testing.T) {
			result, err := method.run(context.Background(), path, Options{Quality: 80})
			if err != nil {
				t.Fatalf("quantization refusal should still produce a result: %v", err)
			}
			assertPNGResult(t, result, before, 96, 64)
			current, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(current, before) || !bytes.Equal(data, before) {
				t.Fatalf("quality-gate source file or snapshot mutated: %v", err)
			}
		})
	}
}

func pngHasChunk(data []byte, target string) bool {
	for offset := 8; offset <= len(data)-12; {
		chunkSize := int(binary.BigEndian.Uint32(data[offset : offset+4]))
		if chunkSize < 0 || chunkSize > len(data)-offset-12 {
			return false
		}
		if string(data[offset+4:offset+8]) == target {
			return true
		}
		offset += chunkSize + 12
	}
	return false
}

func TestPNGCompressesWithCaBXAndIDOT(t *testing.T) {
	base := pngScreenshotFixture(t)
	for _, chunkName := range []string{"caBX", "iDOT"} {
		t.Run(chunkName, func(t *testing.T) {
			input := insertPNGChunk(base, chunkName, bytes.Repeat([]byte("m"), 2048))
			result, err := Bytes(context.Background(), input, "sample.png", Options{
				Quality: 92,
				PNG:     PNGOptions{MinQuality: 85, MaxQuality: 100, Colors: 256, Speed: 1},
			})
			if err != nil {
				t.Fatalf("Bytes failed with %s: %v", chunkName, err)
			}
			assertPNGResult(t, result, input, 320, 192)
			if result.Unchanged || result.OutputSize >= result.OriginalSize {
				t.Fatalf("PNG with %s did not shrink: %d -> %d", chunkName, result.OriginalSize, result.OutputSize)
			}
			if pngHasChunk(result.Data, chunkName) {
				t.Fatalf("compressed PNG retained invalidated %s chunk", chunkName)
			}
			losslessFallback, err := Bytes(context.Background(), input, "sample.png", Options{
				Quality: 100,
				PNG:     PNGOptions{MinQuality: 100, MaxQuality: 100, Colors: 256, Speed: 1},
			})
			if err != nil {
				t.Fatalf("Bytes lossless fallback failed with %s: %v", chunkName, err)
			}
			assertPNGResult(t, losslessFallback, input, 320, 192)
			if losslessFallback.Unchanged || losslessFallback.OutputSize >= losslessFallback.OriginalSize {
				t.Fatalf("PNG with %s did not shrink on Oxipng fallback: %d -> %d", chunkName, losslessFallback.OriginalSize, losslessFallback.OutputSize)
			}
			if pngHasChunk(losslessFallback.Data, chunkName) {
				t.Fatalf("Oxipng fallback retained invalidated %s chunk", chunkName)
			}
		})
	}
}

func TestPNGNoSavingsReturnsOptimizedOriginal(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	img.SetNRGBA(0, 0, color.NRGBA{R: 17, G: 29, B: 71, A: 128})
	raw := pngFixture(t, img, png.BestCompression)
	optimized, err := Bytes(context.Background(), raw, "tiny.png", Options{Quality: 80})
	if err != nil {
		t.Fatal(err)
	}
	// Use the native optimizer's own output; Go's BestCompression alone is
	// not a reliable no-savings fixture for Oxipng.
	data := optimized.Data
	path := writeFixture(t, "optimized.png", data)
	for _, method := range inputMethods(data) {
		t.Run(method.name, func(t *testing.T) {
			result, err := method.run(context.Background(), path, Options{Quality: 80})
			if err != nil {
				t.Fatal(err)
			}
			assertPNGResult(t, result, data, 1, 1)
			if !result.Unchanged || !bytes.Equal(result.Data, data) {
				t.Fatal("expected byte-exact original for optimized tiny PNG")
			}
		})
	}
}

func TestPNGBytesUsesSnapshotAfterReplacementAndDeletion(t *testing.T) {
	data := pngScreenshotFixture(t)
	source := writeFixture(t, "source.png", data)
	options := Options{Quality: 80}
	expected, err := File(context.Background(), source, options)
	if err != nil {
		t.Fatal(err)
	}
	replacement := pngFixture(t, image.NewGray(image.Rect(0, 0, 2, 3)), png.BestCompression)
	if err := os.WriteFile(source, replacement, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"replaced", "deleted"} {
		t.Run(state, func(t *testing.T) {
			if state == "deleted" {
				if err := os.Remove(source); err != nil {
					t.Fatal(err)
				}
			}
			result, err := Bytes(context.Background(), data, source, options)
			if err != nil {
				t.Fatal(err)
			}
			assertPNGResult(t, result, data, 320, 192)
			if !bytes.Equal(result.Data, expected.Data) {
				t.Fatal("snapshot result depends on current source")
			}
			if err := result.Save(source); err == nil {
				t.Fatal("snapshot result allowed saving over source")
			}
			if state == "replaced" {
				current, err := os.ReadFile(source)
				if err != nil || !bytes.Equal(current, replacement) {
					t.Fatalf("source replacement changed: %v", err)
				}
			} else if _, err := os.Stat(source); !os.IsNotExist(err) {
				t.Fatalf("deleted source was recreated: %v", err)
			}
			target := filepath.Join(t.TempDir(), "export.png")
			if err := result.Save(target); err != nil {
				t.Fatal(err)
			}
			saved, err := os.ReadFile(target)
			if err != nil || !bytes.Equal(saved, result.Data) {
				t.Fatalf("snapshot export: %v", err)
			}
		})
	}
}

func TestPNGRetainsInputGuards(t *testing.T) {
	data := pngFixture(t, image.NewGray(image.Rect(0, 0, 2, 3)), png.BestCompression)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, test := range []struct {
		name    string
		data    []byte
		ctx     context.Context
		quality int
	}{
		{"damaged", data[:len(data)-15], context.Background(), 80},
		{"APNG", insertPNGChunk(data, "acTL", make([]byte, 8)), context.Background(), 80},
		{"canceled", data, canceled, 80},
		{"invalid quality", data, context.Background(), 0},
	} {
		path := writeFixture(t, "guard.png", test.data)
		for _, method := range inputMethods(test.data) {
			t.Run(test.name+"/"+method.name, func(t *testing.T) {
				result, err := method.run(test.ctx, path, Options{Quality: test.quality})
				if result != nil || err == nil {
					t.Fatal("PNG input guard did not reject request")
				}
				if test.name == "canceled" && !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation identity lost: %v", err)
				}
			})
		}
	}
}

func TestValidatePNGOutput(t *testing.T) {
	data := pngFixture(t, image.NewGray(image.Rect(0, 0, 2, 3)), png.BestCompression)
	input := image.Config{Width: 2, Height: 3}
	if err := validatePNGOutput(context.Background(), data, input); err != nil {
		t.Fatalf("valid PNG rejected: %v", err)
	}
	corrupt := append([]byte(nil), data...)
	// Keep IHDR intact so DecodeConfig succeeds, but invalidate IDAT's CRC.
	for offset := 8; offset+12 <= len(corrupt); {
		size := int(binary.BigEndian.Uint32(corrupt[offset : offset+4]))
		if string(corrupt[offset+4:offset+8]) == "IDAT" {
			corrupt[offset+8+size] ^= 0xff
			break
		}
		offset += size + 12
	}
	for name, output := range map[string][]byte{
		"empty": nil, "corrupt IDAT": corrupt, "truncated": data[:len(data)-15],
		"wrong dimensions": pngFixture(t, image.NewGray(image.Rect(0, 0, 3, 2)), png.BestCompression),
		"not PNG":          jpegFixture(t, 80),
		"APNG":             insertPNGChunk(data, "acTL", make([]byte, 8)),
	} {
		t.Run(name, func(t *testing.T) {
			if err := validatePNGOutput(context.Background(), output, input); err == nil {
				t.Fatal("invalid native output was accepted")
			}
			if preview, err := decodePNGOutput(context.Background(), output, input); err == nil || preview != nil {
				t.Fatal("invalid native output returned a preview")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := validatePNGOutput(ctx, data, input); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled output validation, got %v", err)
	}
	if preview, err := decodePNGOutput(ctx, data, input); preview != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled PNG validation returned preview=%T err=%v", preview, err)
	}
}

func TestIsAnimatedPNGAndSameBytesBacking(t *testing.T) {
	data := pngFixture(t, image.NewGray(image.Rect(0, 0, 2, 2)), png.BestCompression)
	if IsAnimatedPNG(data) {
		t.Fatal("static PNG reported as animated")
	}
	if !IsAnimatedPNG(insertPNGChunk(data, "acTL", make([]byte, 8))) {
		t.Fatal("animated PNG was not detected")
	}
	if IsAnimatedPNG(jpegFixture(t, 80)) {
		t.Fatal("JPEG bytes mistaken for animated PNG")
	}
	copied := append([]byte(nil), data...)
	if !sameBytesBacking(data, data) || sameBytesBacking(data, copied) {
		t.Fatal("sameBytesBacking must distinguish identical slice backing from copies")
	}
}
