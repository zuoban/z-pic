package compress

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math/rand/v2"
	"testing"
)

const (
	targetSizeTestMin     int64 = 1 << 10
	targetSizeTestMax     int64 = 64 << 20
	targetSizeTestQuality       = 92
)

func TestValidateTargetSizeBytes(t *testing.T) {
	for _, size := range []int64{0, targetSizeTestMin, targetSizeTestMin + 1, targetSizeTestMax - 1, targetSizeTestMax} {
		if err := ValidateTargetSizeBytes(size); err != nil {
			t.Errorf("ValidateTargetSizeBytes(%d) rejected a valid target: %v", size, err)
		}
	}
	for _, size := range []int64{-1, 1, targetSizeTestMin - 1, targetSizeTestMax + 1, 1<<63 - 1} {
		if err := ValidateTargetSizeBytes(size); err == nil {
			t.Errorf("ValidateTargetSizeBytes(%d) accepted an invalid target", size)
		}
	}
}

func TestParseTargetSize(t *testing.T) {
	for _, test := range []struct {
		input string
		want  int64
	}{
		{"", 0},
		{" \t\n", 0},
		{" 0 ", 0},
		{"1024", 1024},
		{"2048 B", 2048},
		{"2048.00b", 2048},
		{"2KB", 2000},
		{"2 kIb", 2048},
		{"2K", 2048},
		{"3.25 MB", 3_250_000},
		{"1.5 mIb", 1_572_864},
		{".5 MiB", 524_288},
		{"\t 2 m \n", 2_097_152},
		{"64 MiB", 64 << 20},
		{"1.2344 KB", 1234},
		{"1.2345 KB", 1235},
		{"2.0035 KB", 2004},
		{"0.0019595 MB", 1960},
		{"1.0006 KiB", 1025},
		{"1.0236 KB", 1024},
	} {
		if got, err := ParseTargetSize(test.input); err != nil || got != test.want {
			t.Errorf("ParseTargetSize(%q) = (%d, %v), want (%d, nil)", test.input, got, err, test.want)
		}
	}

	for _, input := range []string{
		"garbage", "KB", "2 XB", "1 GiB", "2 K B", "1.2.3 MB", "NaN KB", "Inf", "+Inf MiB",
		"-2 KiB", "1023 B", "1.0234 KB", "64.1 MiB", "9223372036854775808 B", "9223372036854776 KB",
		"2048.5 B", "2048.5", "2048.0000000000000001 B", "0 KiB", "0.0001 KiB",
	} {
		if got, err := ParseTargetSize(input); err == nil {
			t.Errorf("ParseTargetSize(%q) = %d, want an error", input, got)
		}
	}
}

func TestFormatTargetSize(t *testing.T) {
	for _, test := range []struct {
		size int64
		want string
	}{
		{0, ""},
		{2 << 20, "2 MiB"},
		{2048, "2 KiB"},
		{16_000_000, "15625 KiB"}, // Also an exact MB multiple.
		{3_000_000, "3 MB"},
		{1_234_000, "1234 KB"},
		{1_234_567, "1.18 MiB"},
		{1<<20 + 1, "1.00 MiB"},
		{1537, "1.5 KiB"},
		{1025, "1.0 KiB"},
		{1023, "1023 B"},
	} {
		if got := FormatTargetSize(test.size); got != test.want {
			t.Errorf("FormatTargetSize(%d) = %q, want %q", test.size, got, test.want)
		}
	}
}

func TestTargetSizeInvalidOptions(t *testing.T) {
	data := jpegFixture(t, 100)
	path := writeFixture(t, "photo.jpg", data)
	for _, method := range inputMethods(data) {
		t.Run(method.name, func(t *testing.T) {
			result, err := method.run(t.Context(), path, Options{Quality: 80, TargetSizeBytes: targetSizeTestMin - 1})
			if result != nil || err == nil {
				t.Fatalf("invalid target gave result=%v, error=%v", result != nil, err)
			}
		})
	}
}

func TestTargetSizeJPEGICC(t *testing.T) {
	base := jpegFixture(t, 100)
	profile := append(makeICCHeaderPayload("RGB "), make([]byte, 1792)...)
	binary.BigEndian.PutUint32(profile, uint32(len(profile)))
	data := bytes.Join([][]byte{base[:2], makeICCAPP2Segment(1, 1, profile), base[2:]}, nil)
	const path = "photo.jpg"

	// Both policies must search at this limit, even with the ICC bytes included.
	small := compressTargetSizeFixture(t, data, path, Options{Quality: 25, Metadata: MetadataKeepICC})
	initial := compressTargetSizeFixture(t, data, path, Options{Quality: targetSizeTestQuality, Metadata: MetadataStrip})
	target := targetSizeBetween(t, small.OutputSize, initial.OutputSize)
	for _, test := range []struct {
		name     string
		metadata MetadataPolicy
		wantICC  []byte
	}{
		{"keep-icc", MetadataKeepICC, profile},
		{"strip", MetadataStrip, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := compressTargetSizeFixture(t, data, path, Options{
				Quality: targetSizeTestQuality, Metadata: test.metadata, TargetSizeBytes: target,
				TargetResizeMinDimension: MinResizeDimension,
			})
			assertTargetSizeResult(t, result, target, false, "jpeg", image.Pt(128, 96))
			assertTargetSizeLoweredQuality(t, result, targetSizeTestQuality)
			if result.TargetResized {
				t.Fatal("quality search met the target at the original dimensions")
			}
			assertConvertedJPEGICC(t, result.Data, test.wantICC)
		})
	}
}

func TestTargetSizeJPEGAlreadyFits(t *testing.T) {
	for _, test := range []struct {
		name         string
		inputQuality int
		quality      int
		unchanged    bool
	}{
		{"initial-encoding", 100, 65, false},
		{"original-fallback", 1, 100, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := jpegFixture(t, test.inputQuality)
			options := Options{Quality: test.quality}
			initial := compressTargetSizeFixture(t, data, "photo.jpg", options)
			options.TargetSizeBytes = max(targetSizeTestMin, initial.OutputSize)
			options.TargetResizeMinDimension = MinResizeDimension
			result := compressTargetSizeFixture(t, data, "photo.jpg", options)
			assertTargetSizeResult(t, result, options.TargetSizeBytes, false, "jpeg", image.Pt(128, 96))
			if result.TargetResized || result.AppliedQuality != 0 || result.Unchanged != test.unchanged || !bytes.Equal(result.Data, initial.Data) {
				t.Fatalf("initial output met target: resized=%v, applied quality=%d, unchanged=%v, same bytes=%v",
					result.TargetResized, result.AppliedQuality, result.Unchanged, bytes.Equal(result.Data, initial.Data))
			}
		})
	}
}

func TestTargetSizeSkipsSearchWhenFirstCandidateFits(t *testing.T) {
	options := Options{Quality: targetSizeTestQuality, TargetSizeBytes: targetSizeTestMin}
	var qualities []int
	_, _, err := encodeWithTargetSize(t.Context(), options, func(quality int) ([]byte, error) {
		qualities = append(qualities, quality)
		return make([]byte, targetSizeTestMin), nil
	})
	if err != nil || len(qualities) != 1 || qualities[0] != options.Quality {
		t.Fatalf("qualities=%v, error=%v, want one attempt at quality %d", qualities, err, options.Quality)
	}
}

func TestTargetSizeJPEGUnreachable(t *testing.T) {
	source := targetSizeNoise(512, 352)
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, source, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatal(err)
	}
	options := Options{Quality: targetSizeTestQuality}
	initial := compressTargetSizeFixture(t, encoded.Bytes(), "noise.jpg", options)
	options.TargetSizeBytes = targetSizeTestMin
	result := compressTargetSizeFixture(t, encoded.Bytes(), "noise.jpg", options)
	assertTargetSizeResult(t, result, targetSizeTestMin, true, "jpeg", source.Bounds().Size())
	assertTargetSizeLoweredQuality(t, result, options.Quality)
	if result.OutputSize >= initial.OutputSize {
		t.Fatalf("best effort produced %d bytes, initial encoding was %d bytes", result.OutputSize, initial.OutputSize)
	}
}

func TestTargetSizeWebP(t *testing.T) {
	requireWebPEncoder(t)
	const width, height = 256, 192
	for _, test := range []struct {
		name   string
		path   string
		format OutputFormat
		data   func(*testing.T) []byte
	}{
		{"same-format", "photo.webp", OutputFormatKeep, func(t *testing.T) []byte { return webpFixture(t, "opaque") }},
		{"conversion", "photo.png", OutputFormatWebP, func(t *testing.T) []byte {
			return pngFixture(t, benchmarkSourceNRGBA(width, height), png.BestSpeed)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := test.data(t)
			options := Options{Quality: 25, OutputFormat: test.format, Threads: 1}
			small := compressTargetSizeFixture(t, data, test.path, options)
			options.Quality = targetSizeTestQuality
			initial := compressTargetSizeFixture(t, data, test.path, options)

			t.Run("fit-with-preview", func(t *testing.T) {
				withTarget := options
				withTarget.TargetSizeBytes = targetSizeBetween(t, small.OutputSize, initial.OutputSize)
				result, preview, err := BytesWithPreview(t.Context(), data, test.path, withTarget)
				if err != nil {
					t.Fatal(err)
				}
				assertTargetSizeResult(t, result, withTarget.TargetSizeBytes, false, "webp", image.Pt(width, height))
				assertTargetSizeLoweredQuality(t, result, options.Quality)
				assertPreviewMatchesOutput(t, result, preview)
			})
			t.Run("miss", func(t *testing.T) {
				withTarget := options
				withTarget.TargetSizeBytes = targetSizeTestMin
				result := compressTargetSizeFixture(t, data, test.path, withTarget)
				assertTargetSizeResult(t, result, targetSizeTestMin, true, "webp", image.Pt(width, height))
				assertTargetSizeLoweredQuality(t, result, options.Quality)
				if result.OutputSize >= initial.OutputSize {
					t.Fatalf("best effort produced %d bytes, initial encoding was %d bytes", result.OutputSize, initial.OutputSize)
				}
			})
		})
	}
}

func TestTargetSizeLossyPNG(t *testing.T) {
	requireNativePNGTools(t)
	data := pngScreenshotFixture(t)
	const path = "screenshot.png"
	options := Options{Quality: 80, Threads: 1}
	initial := compressTargetSizeFixture(t, data, path, options)
	smallerOptions := options
	smallerOptions.PNG = PNGOptions{MinQuality: 0, MaxQuality: 40, Colors: 64, Speed: 4}
	small := compressTargetSizeFixture(t, data, path, smallerOptions)
	fitTarget := targetSizeBetween(t, small.OutputSize, initial.OutputSize)
	initialColors := pngColorCount(t, initial.Data)
	for _, test := range []struct {
		name   string
		target int64
		missed bool
	}{
		{"fit", fitTarget, false},
		{"miss", targetSizeTestMin, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			withTarget := options
			withTarget.TargetSizeBytes = test.target
			result, preview, err := BytesWithPreview(t.Context(), data, path, withTarget)
			if err != nil {
				t.Fatal(err)
			}
			assertTargetSizeResult(t, result, test.target, test.missed, "png", image.Pt(320, 192))
			assertPreviewMatchesOutput(t, result, preview)
			if got := pngColorCount(t, result.Data); got >= initialColors || result.OutputSize >= initial.OutputSize || result.AppliedQuality != 0 {
				t.Fatalf("PNG ladder produced %d colors and %d bytes (initial %d colors and %d bytes), applied quality=%d",
					got, result.OutputSize, initialColors, initial.OutputSize, result.AppliedQuality)
			}
		})
	}
}

func TestTargetSizeLossless(t *testing.T) {
	for _, format := range []string{"jpeg", "png", "webp"} {
		t.Run(format, func(t *testing.T) {
			var data []byte
			var size image.Point
			switch format {
			case "jpeg":
				data, size = jpegFixture(t, 100), image.Pt(128, 96)
			case "png":
				requireNativePNGTools(t)
				data, size = pngScreenshotFixture(t), image.Pt(320, 192)
			case "webp":
				requireWebPEncoder(t)
				data, size = webpFixture(t, "opaque"), image.Pt(256, 192)
			}
			path := "lossless." + format
			options := Options{Quality: targetSizeTestQuality, Lossless: true, Threads: 1}
			initial := compressTargetSizeFixture(t, data, path, options)
			if initial.OutputSize <= targetSizeTestMin {
				t.Fatalf("lossless fixture encodes to %d bytes, need more than %d", initial.OutputSize, targetSizeTestMin)
			}
			for _, test := range []struct {
				name   string
				target int64
				missed bool
			}{
				{"fit", initial.OutputSize, false},
				{"miss", targetSizeTestMin, true},
			} {
				t.Run(test.name, func(t *testing.T) {
					withTarget := options
					withTarget.TargetSizeBytes = test.target
					result := compressTargetSizeFixture(t, data, path, withTarget)
					assertTargetSizeResult(t, result, test.target, test.missed, format, size)
					if result.AppliedQuality != 0 || result.Unchanged != initial.Unchanged || !bytes.Equal(result.Data, initial.Data) {
						t.Fatalf("target changed lossless encoding: applied quality=%d, unchanged=%v (initial %v), same bytes=%v",
							result.AppliedQuality, result.Unchanged, initial.Unchanged, bytes.Equal(result.Data, initial.Data))
					}
				})
			}
		})
	}
}

func TestSearchTargetSizeCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := 0
	_, _, err := searchTargetSize(ctx, make([]byte, 2*targetSizeTestMin), targetSizeTestQuality, targetSizeTestMin,
		func(data []byte) int { return len(data) },
		func(int) ([]byte, error) {
			calls++
			cancel()
			return make([]byte, targetSizeTestMin), nil
		})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("canceled search returned %v after %d probes, want context.Canceled after one probe", err, calls)
	}
}

func TestTargetSizeHEICConversion(t *testing.T) {
	source := benchmarkSourceNRGBA(256, 192)
	path, data := makeRealHEICFixture(t, source)
	for _, format := range []OutputFormat{OutputFormatJPEG, OutputFormatWebP} {
		t.Run(string(format), func(t *testing.T) {
			if format == OutputFormatWebP {
				requireWebPEncoder(t)
			}
			options := Options{Quality: 25, OutputFormat: format, Threads: 1}
			small := compressTargetSizeFixture(t, data, path, options)
			options.Quality = targetSizeTestQuality
			initial := compressTargetSizeFixture(t, data, path, options)
			options.TargetSizeBytes = targetSizeBetween(t, small.OutputSize, initial.OutputSize)
			result, err := File(t.Context(), path, options)
			if err != nil {
				t.Fatal(err)
			}
			assertTargetSizeResult(t, result, options.TargetSizeBytes, false, string(format), source.Bounds().Size())
			assertTargetSizeLoweredQuality(t, result, options.Quality)
		})
	}
}

func compressTargetSizeFixture(t *testing.T, data []byte, path string, options Options) *Result {
	t.Helper()
	result, err := Bytes(t.Context(), data, path, options)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func targetSizeBetween(t *testing.T, smaller, initial int64) int64 {
	t.Helper()
	lower := max(targetSizeTestMin, smaller)
	if lower >= initial {
		t.Fatalf("unable to place target between %d-byte alternate and %d-byte initial output", lower, initial)
	}
	return lower + (initial-lower)/2
}

func assertTargetSizeResult(t *testing.T, result *Result, target int64, missed bool, format string, size image.Point) {
	t.Helper()
	if result == nil {
		t.Fatal("compression returned no result")
	}
	if result.TargetSizeBytes != target || result.TargetSizeMissed != missed {
		t.Fatalf("target=(%d, missed=%v) at %d output bytes, want (%d, missed=%v)",
			result.TargetSizeBytes, result.TargetSizeMissed, result.OutputSize, target, missed)
	}
	if result.OutputSize != int64(len(result.Data)) || (result.OutputSize > target) != missed {
		t.Fatalf("output size=%d, data size=%d, target=%d, missed=%v", result.OutputSize, len(result.Data), target, missed)
	}
	if result.Format != format || image.Pt(result.Width, result.Height) != size {
		t.Fatalf("result is %s %dx%d, want %s %v", result.Format, result.Width, result.Height, format, size)
	}
	decoded, decodedFormat, err := image.Decode(bytes.NewReader(result.Data))
	if err != nil {
		t.Fatalf("decode %s output: %v", format, err)
	}
	if decodedFormat != format || decoded.Bounds().Size() != size {
		t.Fatalf("decoded output is %s %v, want %s %v", decodedFormat, decoded.Bounds().Size(), format, size)
	}
}

func assertTargetSizeLoweredQuality(t *testing.T, result *Result, requested int) {
	t.Helper()
	if result.Unchanged || result.AppliedQuality < 1 || result.AppliedQuality >= requested {
		t.Fatalf("applied quality=%d, requested=%d, unchanged=%v, want a smaller quality on changed output",
			result.AppliedQuality, requested, result.Unchanged)
	}
}

// Keep a random light/dark pattern at JPEG's block scale so that the fixture
// retains high-contrast detail even at the encoder's lowest quality.
func targetSizeNoise(width, height int) *image.NRGBA {
	const block = 8
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	rng := rand.New(rand.NewPCG(42, 73))
	levels := make([]uint8, (width+block-1)/block)
	for y := range height {
		if y%block == 0 {
			for column := range levels {
				levels[column] = uint8(rng.IntN(2) * 224)
			}
		}
		for x := range width {
			level := levels[x/block]
			noise := rng.Uint32()
			img.SetNRGBA(x, y, color.NRGBA{
				R: level + uint8(noise&31), G: level + uint8(noise>>5&31), B: level + uint8(noise>>10&31), A: 255,
			})
		}
	}
	return img
}
