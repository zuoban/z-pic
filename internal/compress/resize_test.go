package compress

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
	"testing"
)

func TestValidateResizeOptions(t *testing.T) {
	for _, test := range []struct {
		name    string
		values  [4]int
		wantErr bool
	}{
		{"disabled", [4]int{}, false},
		{"minimum long edge", [4]int{64, 0, 0, 0}, false},
		{"minimum width", [4]int{0, 64, 0, 0}, false},
		{"maximum height", [4]int{0, 0, 16384, 0}, false},
		{"minimum percentage", [4]int{0, 0, 0, 1}, false},
		{"original size", [4]int{0, 0, 0, 100}, false},
		{"short long edge", [4]int{63, 0, 0, 0}, true},
		{"short width", [4]int{0, 63, 0, 0}, true},
		{"large width", [4]int{0, 16385, 0, 0}, true},
		{"negative height", [4]int{0, 0, -1, 0}, true},
		{"large height", [4]int{0, 0, 16385, 0}, true},
		{"negative percentage", [4]int{0, 0, 0, -1}, true},
		{"large percentage", [4]int{0, 0, 0, 101}, true},
		{"long edge with width", [4]int{64, 64, 0, 0}, true},
		{"width with height", [4]int{0, 64, 64, 0}, true},
		{"height with original percentage", [4]int{0, 0, 64, 100}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			v := test.values
			if err := ValidateResizeOptions(v[0], v[1], v[2], v[3]); (err != nil) != test.wantErr {
				t.Fatalf("ValidateResizeOptions%v = %v, want error=%v", v, err, test.wantErr)
			}
		})
	}

	options := Options{Quality: 80, MaxWidth: 64, ScalePercent: 100}
	want := ValidateResizeOptions(0, options.MaxWidth, 0, options.ScalePercent)
	if want == nil {
		t.Fatal("a width cap and 100% scaling must be mutually exclusive")
	}
	if result, err := Bytes(t.Context(), nil, "input.jpg", options); result != nil || err == nil || err.Error() != want.Error() {
		t.Errorf("Bytes returned result=%v, error=%v, want %v", result != nil, err, want)
	}
	if result, err := CompressClipboardPNG(t.Context(), nil, options); result != nil || err == nil || err.Error() != want.Error() {
		t.Errorf("CompressClipboardPNG returned result=%v, error=%v, want %v", result != nil, err, want)
	}
}

func TestManualResizeJPEG(t *testing.T) {
	landscape := jpegFixture(t, 100)
	portrait := withEXIF(landscape, exifTIFF(binary.LittleEndian, 6))
	for _, test := range []struct {
		name    string
		data    []byte
		options Options
		want    image.Point
	}{
		{"landscape by width", landscape, Options{MaxWidth: 77}, image.Pt(77, 58)},
		{"landscape by height", landscape, Options{MaxHeight: 67}, image.Pt(89, 67)},
		{"portrait by displayed width", portrait, Options{MaxWidth: 67}, image.Pt(67, 89)},
		{"portrait by displayed height", portrait, Options{MaxHeight: 77}, image.Pt(58, 77)},
		{"portrait by percentage", portrait, Options{ScalePercent: 33}, image.Pt(32, 42)},
	} {
		t.Run(test.name, func(t *testing.T) {
			options := test.options
			options.Quality = 80
			result, preview, err := BytesWithPreview(t.Context(), test.data, "photo.jpg", options)
			if err != nil {
				t.Fatal(err)
			}
			if result.Format != "jpeg" || image.Pt(result.Width, result.Height) != test.want || result.TargetResized || jpegOrientation(result.Data) != 1 {
				t.Fatalf("got %s %dx%d, orientation=%d, automatic resize=%v, want upright %v",
					result.Format, result.Width, result.Height, jpegOrientation(result.Data), result.TargetResized, test.want)
			}
			assertPreviewMatchesOutput(t, result, preview)
		})
	}
}

func TestManualResizeSmallImages(t *testing.T) {
	data := makeJPEGWithICCAndOrient(t, samplePhotoImage(5, 3), syntheticRGBICC(), 0)
	baseline := compressTargetSizeFixture(t, data, "tiny.jpg", Options{Quality: 80})
	for _, options := range []Options{{MaxWidth: 64}, {MaxHeight: 64}, {ScalePercent: 100}} {
		options.Quality = 80
		result := compressTargetSizeFixture(t, data, "tiny.jpg", options)
		if result.Width != 5 || result.Height != 3 || !bytes.Equal(result.Data, baseline.Data) {
			t.Fatalf("resize settings %+v changed a 5x3 image to %dx%d or altered compression", options, result.Width, result.Height)
		}
	}

	result := compressTargetSizeFixture(t, data, "tiny.jpg", Options{Quality: 80, ScalePercent: 1})
	assertConversionResult(t, result, data, "jpeg", image.Rect(0, 0, 1, 1))
}

func TestManualResizeFileEntries(t *testing.T) {
	data := jpegFixture(t, 100)
	path := writeFixture(t, "photo.jpg", data)
	options := Options{Quality: 80, MaxHeight: 72}
	want := image.Rect(0, 0, 96, 72)
	for _, method := range inputMethods(data) {
		t.Run(method.name, func(t *testing.T) {
			result, err := method.run(t.Context(), path, options)
			if err != nil {
				t.Fatal(err)
			}
			assertConversionResult(t, result, data, "jpeg", want)
			if result.snapshot == nil || *result.snapshot != *captureOverwriteSnapshot(data, result.Data) {
				t.Fatal("overwrite snapshot must describe the source and the resized output")
			}
		})
	}
	result, _, err := FileWithAcquire(t.Context(), path, options, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertConversionResult(t, result, data, "jpeg", want)
}

func TestManualResizeSameFormatPNGEXIF(t *testing.T) {
	requireNativePNGTools(t)
	source := samplePhotoImage(160, 120)
	data := insertPNGChunk(pngFixture(t, source, png.DefaultCompression), "eXIf", exifTIFF(binary.LittleEndian, 6))
	for _, test := range []struct {
		name    string
		options Options
		want    image.Rectangle
	}{
		{"legacy long edge", Options{MaxDimension: 100}, image.Rect(0, 0, 100, 75)},
		{"displayed width", Options{MaxWidth: 96}, image.Rect(0, 0, 96, 128)},
		{"displayed height", Options{MaxHeight: 100}, image.Rect(0, 0, 75, 100)},
		{"percentage", Options{ScalePercent: 50}, image.Rect(0, 0, 60, 80)},
		{"width already fits", Options{MaxWidth: 144}, image.Rect(0, 0, 120, 160)},
		{"original displayed size", Options{ScalePercent: 100}, image.Rect(0, 0, 120, 160)},
	} {
		t.Run(test.name, func(t *testing.T) {
			options := test.options
			options.Quality, options.Lossless, options.Threads = 80, true, 1
			result, preview, err := BytesWithPreview(t.Context(), data, "photo.png", options)
			if err != nil {
				t.Fatal(err)
			}
			assertPreviewMatchesOutput(t, result, preview)
			if preview.Bounds() != test.want || pngOrientation(result.Data) != 1 {
				t.Fatalf("PNG output bounds=%v, EXIF orientation=%d, want %v and orientation 1",
					preview.Bounds(), pngOrientation(result.Data), test.want)
			}
			if test.want.Size() == image.Pt(120, 160) {
				assertImagesEqual(t, clockwiseConversionImage(source), preview)
			}

			options.OutputFormat = OutputFormatJPEG
			clipboard := compressClipboardFixture(t, data, options)
			if clipboard.Format != "png" || !bytes.Equal(clipboard.Data, result.Data) {
				t.Fatal("clipboard compression must use the same PNG resize path")
			}
		})
	}
}

func TestManualResizeWithTargetFallback(t *testing.T) {
	// ICC alone exceeds the target, so the search can exercise its size floor.
	icc := append(syntheticRGBICC(), make([]byte, MinTargetSizeBytes)...)
	binary.BigEndian.PutUint32(icc, uint32(len(icc)))
	data := makeJPEGWithICCAndOrient(t, targetSizeNoise(512, 352), icc, 1)
	direct := compressTargetSizeFixture(t, data, "noise.jpg", Options{
		Quality: targetSizeTestQuality, Metadata: MetadataKeepICC,
		MaxDimension: 320, TargetSizeBytes: MinTargetSizeBytes,
	})
	for _, test := range []struct {
		name    string
		options Options
	}{
		{"width", Options{MaxWidth: 384}},
		{"height", Options{MaxHeight: 264}},
		{"percentage", Options{ScalePercent: 75}},
	} {
		t.Run(test.name, func(t *testing.T) {
			options := test.options
			options.Quality, options.Metadata = targetSizeTestQuality, MetadataKeepICC
			options.TargetSizeBytes = MinTargetSizeBytes
			baseline := compressTargetSizeFixture(t, data, "noise.jpg", options)
			assertTargetSizeResult(t, baseline, MinTargetSizeBytes, true, "jpeg", image.Pt(384, 264))
			if baseline.TargetResized {
				t.Fatal("manual resize must not be reported as automatic")
			}

			options.TargetResizeMinDimension = 400
			belowFloor := compressTargetSizeFixture(t, data, "noise.jpg", options)
			if belowFloor.TargetResized || !bytes.Equal(belowFloor.Data, baseline.Data) {
				t.Fatal("fallback changed a baseline below its floor")
			}

			options.TargetResizeMinDimension = 320
			result, preview, err := BytesWithPreview(t.Context(), data, "noise.jpg", options)
			if err != nil {
				t.Fatal(err)
			}
			assertTargetSizeResult(t, result, MinTargetSizeBytes, true, "jpeg", image.Pt(320, 220))
			assertPreviewMatchesOutput(t, result, preview)
			if !result.TargetResized || result.OutputSize >= baseline.OutputSize {
				t.Fatalf("fallback selected %d bytes from %d, resized=%v", result.OutputSize, baseline.OutputSize, result.TargetResized)
			}
			if !bytes.Equal(result.Data, direct.Data) {
				t.Fatal("fallback must resize directly from the original pixels")
			}
		})
	}
}

func TestManualResizeNativeFormats(t *testing.T) {
	t.Run("WebP", func(t *testing.T) {
		requireWebPEncoder(t)
		data := extendedWebP(webpFixture(t, "alpha"), 0x18, 96, 64)
		data = appendWebPChunk(data, "EXIF", exifTIFF(binary.LittleEndian, 6))
		result := compressTargetSizeFixture(t, data, "photo.webp", Options{Quality: 80, MaxHeight: 72, Threads: 1})
		assertConversionResult(t, result, data, "webp", image.Rect(0, 0, 48, 72))
	})
	t.Run("AVIF", func(t *testing.T) {
		requireAVIF(t)
		data := avifTestPNG(t, samplePhotoImage(160, 120), nil, 6)
		avif := compressTargetSizeFixture(t, data, "photo.png", Options{Quality: 80, OutputFormat: OutputFormatAVIF, MaxWidth: 96, Threads: 1})
		result, preview, err := BytesWithPreview(t.Context(), avif.Data, "photo.avif", Options{Quality: 80, ScalePercent: 50, Threads: 1})
		if err != nil {
			t.Fatal(err)
		}
		want := image.Pt(48, 64)
		if result.Format != "avif" || image.Pt(result.Width, result.Height) != want || preview == nil || preview.Bounds().Size() != want {
			t.Fatalf("AVIF output is %s %dx%d with preview %T, want AVIF %v", result.Format, result.Width, result.Height, preview, want)
		}
	})
	t.Run("HEIC", func(t *testing.T) {
		path, data := makeRealHEICFixture(t, samplePhotoImage(160, 120))
		result, err := File(t.Context(), path, Options{Quality: 80, MaxWidth: 80})
		if err != nil {
			t.Fatal(err)
		}
		assertConversionResult(t, result, data, "jpeg", image.Rect(0, 0, 80, 60))
	})
}
