package compress

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"

	"zpic/internal/aviftools"
)

const testAVIFXMP = "<x:xmpmeta xmlns:x='adobe:ns:meta/'/>"

func requireAVIF(t *testing.T) {
	t.Helper()
	if !SupportsAVIF() {
		t.Skip("bundled AVIF tools require macOS on Apple silicon")
	}
}

func avifTestProfile(t *testing.T) []byte {
	t.Helper()
	icc, err := os.ReadFile("/System/Library/ColorSync/Profiles/Display P3.icc")
	if err != nil {
		t.Skipf("system Display P3 profile unavailable: %v", err)
	}
	return icc
}

func avifTestPNG(t *testing.T, img image.Image, icc []byte, orientation uint16) []byte {
	t.Helper()
	chunks := [][]byte{
		pngTestChunk("eXIf", exifTIFF(binary.LittleEndian, orientation)),
		pngTestChunk("iTXt", []byte("XML:com.adobe.xmp\x00\x00\x00\x00\x00"+testAVIFXMP)),
	}
	if len(icc) > 0 {
		chunks = append(chunks, buildPNGICCPChunk(icc, 3))
	}
	return injectPNGChunksAfterIHDR(pngFixture(t, img, png.DefaultCompression), chunks)
}

func assertAVIFMetadata(t *testing.T, data []byte, exif, xmp, icc bool) aviftools.HeaderInfo {
	t.Helper()
	info, err := aviftools.InspectBytes(t.Context(), data)
	if err != nil {
		t.Fatal(err)
	}
	if info.HasEXIF != exif || info.HasXMP != xmp || info.HasICC != icc {
		t.Errorf("AVIF metadata = %+v, want EXIF=%v XMP=%v ICC=%v", info, exif, xmp, icc)
	}
	return info
}

func TestAVIFPNGConversionAndUtilities(t *testing.T) {
	requireAVIF(t)
	icc := avifTestProfile(t)
	source := image.NewNRGBA(image.Rect(0, 0, 120, 84))
	for y := range source.Bounds().Dy() {
		for x := range source.Bounds().Dx() {
			source.SetNRGBA(x, y, color.NRGBA{R: uint8(2 * x), G: uint8(3 * y), B: uint8(x + y), A: uint8(x % 3 * 127)})
		}
	}
	data := avifTestPNG(t, source, icc, 6)
	result, preview, err := BytesWithPreview(t.Context(), data, "source.png", Options{
		Quality: 90, OutputFormat: OutputFormatAVIF, Lossless: true, Threads: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	upright := clockwiseConversionImage(source)
	if result.Format != "avif" || result.Unchanged || image.Pt(result.Width, result.Height) != upright.Bounds().Size() || preview == nil {
		t.Fatalf("unexpected conversion result: %s %dx%d, unchanged=%v, preview=%v", result.Format, result.Width, result.Height, result.Unchanged, preview != nil)
	}
	assertImagesNear(t, upright, preview, 0)
	decodedPreview, err := DecodePreviewImage(t.Context(), result.Data)
	if err != nil {
		t.Fatal(err)
	}
	assertImagesNear(t, upright, decodedPreview, 0)
	assertAVIFMetadata(t, result.Data, true, true, true)
	path := writeFixture(t, "source.avif", result.Data)
	info, err := Inspect(t.Context(), path)
	if err != nil || info.Format != "avif" || image.Pt(info.Width, info.Height) != upright.Bounds().Size() || info.Size != result.OutputSize {
		t.Fatalf("Inspect = %+v, %v", info, err)
	}
	if format, err := VerifyImageData(t.Context(), result.Data); err != nil || format != "avif" {
		t.Fatalf("VerifyImageData = %q, %v", format, err)
	}
	verified, err := VerifyImageReader(t.Context(), bytes.NewReader(result.Data), result.OutputSize)
	if err != nil || verified.Format != "avif" || verified.Width != info.Width || verified.Height != info.Height {
		t.Fatalf("VerifyImageReader = %+v, %v", verified, err)
	}
	thumbnail, err := Thumbnail(t.Context(), path, 60)
	if err != nil || thumbnail.Bounds().Size() != image.Pt(42, 60) {
		t.Fatalf("Thumbnail has unexpected dimensions: %v", err)
	}
	clipboard, err := PNGBytes(t.Context(), result.Data)
	if err != nil {
		t.Fatal(err)
	}
	pixels, err := png.Decode(bytes.NewReader(clipboard))
	if err != nil {
		t.Fatal(err)
	}
	assertImagesNear(t, upright, pixels, 0)
	if pngOrientation(clipboard) != 1 || !bytes.Equal(extractPNGICCProfile(clipboard), icc) {
		t.Fatal("AVIF clipboard PNG did not retain the ICC profile with normalized orientation")
	}

	for _, format := range []OutputFormat{OutputFormatJPEG, OutputFormatPNG, OutputFormatWebP} {
		t.Run(string(format), func(t *testing.T) {
			switch format {
			case OutputFormatPNG:
				requireNativePNGTools(t)
			case OutputFormatWebP:
				requireWebPEncoder(t)
			}
			options := Options{Quality: 90, OutputFormat: format, Metadata: MetadataKeepICC, MaxDimension: 64, Lossless: true, Threads: 1}
			out, err := File(t.Context(), path, options)
			if err != nil {
				t.Fatal(err)
			}
			assertConversionResult(t, out, result.Data, string(format), image.Rect(0, 0, 45, 64))
			switch format {
			case OutputFormatJPEG:
				assertConvertedJPEGICC(t, out.Data, icc)
			case OutputFormatPNG:
				assertConvertedPNGICC(t, out.Data, icc)
			case OutputFormatWebP:
				if !bytes.Equal(extractWebPICCProfile(out.Data), icc) {
					t.Fatal("AVIF to WebP conversion lost the ICC profile")
				}
			}
		})
	}
}

func TestAVIFJPEGAndWebPInputMetadata(t *testing.T) {
	requireAVIF(t)
	icc := avifTestProfile(t)
	jpeg := makeJPEGWithICCAndOrient(t, samplePhotoImage(96, 64), icc, 6)
	xmp := []byte("http://ns.adobe.com/xap/1.0/\x00" + testAVIFXMP)
	app1 := binary.BigEndian.AppendUint16([]byte{0xff, 0xe1}, uint16(len(xmp)+2))
	jpeg = bytes.Join([][]byte{jpeg[:2], app1, xmp, jpeg[2:]}, nil)
	webp := extendedWebP(webpFixture(t, "alpha"), 0x1c, 96, 64)
	webp = injectWebPICC(webp, icc, 96, 64)
	webp = appendWebPChunk(webp, "EXIF", exifTIFF(binary.LittleEndian, 6))
	webp = appendWebPChunk(webp, "XMP ", []byte(testAVIFXMP))
	for format, data := range map[string][]byte{"jpeg": jpeg, "webp": webp} {
		t.Run(format, func(t *testing.T) {
			opts := Options{Quality: 90, OutputFormat: OutputFormatAVIF, Lossless: true, Threads: 1}
			result, err := Bytes(t.Context(), data, "source."+format, opts)
			if err != nil {
				t.Fatal(err)
			}
			assertAVIFMetadata(t, result.Data, true, true, true)
			decoded, _, err := image.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			pngData, err := aviftools.DecodePNG(t.Context(), result.Data, 1)
			if err != nil {
				t.Fatal(err)
			}
			got, err := png.Decode(bytes.NewReader(pngData))
			if err != nil {
				t.Fatal(err)
			}
			assertImagesNear(t, clockwiseConversionImage(decoded), got, 0)
			if pngOrientation(pngData) != 1 || !bytes.Equal(extractPNGICCProfile(pngData), icc) {
				t.Fatal("orientation or ICC profile changed during conversion")
			}
			opts.Metadata = MetadataStrip
			stripped, err := Bytes(t.Context(), data, "source."+format, opts)
			if err != nil {
				t.Fatal(err)
			}
			assertAVIFMetadata(t, stripped.Data, false, false, false)
		})
	}
}

func TestAVIFHEICInput(t *testing.T) {
	requireAVIF(t)
	path, _ := makeRealHEICFixture(t, samplePhotoImage(96, 72))
	result, err := File(t.Context(), path, Options{Quality: 75, OutputFormat: OutputFormatAVIF, Threads: 1})
	if err != nil {
		t.Fatal(err)
	}
	if result.Format != "avif" || result.Width != 96 || result.Height != 72 || result.Unchanged {
		t.Fatalf("unexpected result: %s %dx%d, unchanged=%v", result.Format, result.Width, result.Height, result.Unchanged)
	}
	if _, err := VerifyImageData(t.Context(), result.Data); err != nil {
		t.Fatal(err)
	}
}

func TestAVIFKeepsOriginalUnlessMetadataRequiresReencoding(t *testing.T) {
	requireAVIF(t)
	icc := avifTestProfile(t)
	pngData := avifTestPNG(t, targetSizeNoise(128, 96), nil, 1)
	data, err := aviftools.EncodePNG(t.Context(), pngData, aviftools.EncodeOptions{Quality: 10, ICC: icc, Threads: 1})
	if err != nil {
		t.Fatal(err)
	}
	opts := Options{Quality: 100, Lossless: true, Threads: 1}
	result, preview, err := BytesWithPreview(t.Context(), data, "source.avif", opts)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Unchanged || !sameBytesBacking(data, result.Data) || preview != nil {
		t.Fatalf("larger AVIF candidate took precedence: unchanged=%v, preview=%v", result.Unchanged, preview != nil)
	}
	for _, policy := range []MetadataPolicy{MetadataKeepICC, MetadataStrip} {
		t.Run(string(policy), func(t *testing.T) {
			opts := opts
			opts.Metadata = policy
			result, preview, err := BytesWithPreview(t.Context(), data, "source.avif", opts)
			if err != nil {
				t.Fatal(err)
			}
			if result.Unchanged || result.OutputSize <= int64(len(data)) || preview == nil {
				t.Fatalf("metadata stripping did not select the larger candidate: unchanged=%v, bytes=%d/%d, preview=%v", result.Unchanged, result.OutputSize, len(data), preview != nil)
			}
			assertAVIFMetadata(t, result.Data, false, false, policy == MetadataKeepICC)
		})
	}
}

func TestAVIFTargetSizeAndResize(t *testing.T) {
	requireAVIF(t)
	data := avifTestPNG(t, targetSizeNoise(256, 192), nil, 1)
	options := Options{Quality: 20, OutputFormat: OutputFormatAVIF, MaxDimension: 128, Threads: 1}
	small := compressTargetSizeFixture(t, data, "source.png", options)
	options.Quality = targetSizeTestQuality
	initial := compressTargetSizeFixture(t, data, "source.png", options)
	options.TargetSizeBytes = targetSizeBetween(t, small.OutputSize, initial.OutputSize)
	out, preview, err := BytesWithPreview(t.Context(), data, "source.png", options)
	if err != nil {
		t.Fatal(err)
	}
	assertTargetSizeLoweredQuality(t, out, options.Quality)
	if out.TargetSizeMissed || out.OutputSize > options.TargetSizeBytes || out.TargetSizeBytes != options.TargetSizeBytes {
		t.Fatalf("output size = %d, target = %d, missed = %v", out.OutputSize, out.TargetSizeBytes, out.TargetSizeMissed)
	}
	if preview == nil || preview.Bounds().Size() != image.Pt(128, 96) || out.Width != 128 || out.Height != 96 {
		t.Fatal("target-size output did not use the resized dimensions")
	}
	assertAVIFMetadata(t, out.Data, true, true, false)
}

func TestAVIFRecompressToTargetSize(t *testing.T) {
	requireAVIF(t)
	pngData := avifTestPNG(t, targetSizeNoise(144, 112), nil, 1)
	source := compressTargetSizeFixture(t, pngData, "source.png", Options{
		Quality: targetSizeTestQuality, OutputFormat: OutputFormatAVIF, Threads: 1,
	})
	options := Options{Quality: 20, Threads: 1}
	small := compressTargetSizeFixture(t, source.Data, "source.avif", options)
	options.Quality = targetSizeTestQuality
	options.TargetSizeBytes = targetSizeBetween(t, small.OutputSize, source.OutputSize)

	out, preview, err := BytesWithPreview(t.Context(), source.Data, "source.avif", options)
	if err != nil {
		t.Fatal(err)
	}
	assertTargetSizeLoweredQuality(t, out, options.Quality)
	if out.Unchanged || out.Format != "avif" || image.Pt(out.Width, out.Height) != image.Pt(144, 112) {
		t.Fatalf("recompressed AVIF = %s %dx%d, unchanged = %v", out.Format, out.Width, out.Height, out.Unchanged)
	}
	if out.TargetSizeMissed || out.OutputSize > options.TargetSizeBytes {
		t.Fatalf("recompressed size = %d, target = %d, missed = %v", out.OutputSize, options.TargetSizeBytes, out.TargetSizeMissed)
	}
	if preview == nil || preview.Bounds().Size() != image.Pt(144, 112) {
		t.Fatal("recompressed AVIF preview has unexpected dimensions")
	}
	assertAVIFMetadata(t, out.Data, true, true, false)
}

func TestAVIFAnimationRejectedBeforeNativeDecoding(t *testing.T) {
	data := []byte("\x00\x00\x00\x10ftypavis\x00\x00\x00\x00")
	if _, err := InspectBytes(t.Context(), data); !errors.Is(err, aviftools.ErrAnimatedAVIF) {
		t.Errorf("InspectBytes animation error = %v", err)
	}
	if _, err := VerifyImageData(t.Context(), data); !errors.Is(err, aviftools.ErrAnimatedAVIF) {
		t.Errorf("VerifyImageData animation error = %v", err)
	}
	if _, err := Bytes(t.Context(), data, "animated.avif", Options{Quality: 70}); !errors.Is(err, aviftools.ErrAnimatedAVIF) {
		t.Errorf("Bytes animation error = %v", err)
	}
	if _, err := DecodePreviewImage(t.Context(), data); !errors.Is(err, aviftools.ErrAnimatedAVIF) {
		t.Errorf("DecodePreviewImage animation error = %v", err)
	}
	if _, err := PNGBytes(t.Context(), data); !errors.Is(err, aviftools.ErrAnimatedAVIF) {
		t.Errorf("PNGBytes animation error = %v", err)
	}
}

func TestAVIFCorruptPayloadRejectedByPixelDecoders(t *testing.T) {
	requireAVIF(t)
	data, err := aviftools.EncodeImage(t.Context(), targetSizeNoise(64, 48), aviftools.EncodeOptions{Quality: 70, Threads: 1})
	if err != nil {
		t.Fatal(err)
	}
	mdat := bytes.Index(data, []byte("mdat"))
	if mdat < 0 || mdat+4 == len(data) {
		t.Fatal("AVIF fixture is missing image data")
	}
	clear(data[mdat+4:])
	if _, err := InspectBytes(t.Context(), data); err != nil {
		t.Fatalf("corrupt image payload changed the headers: %v", err)
	}
	if _, err := VerifyImageData(t.Context(), data); err == nil {
		t.Error("VerifyImageData accepted corrupt image data")
	}
	if _, err := VerifyImageReader(t.Context(), bytes.NewReader(data), int64(len(data))); err == nil {
		t.Error("VerifyImageReader accepted corrupt image data")
	}
	if _, _, err := BytesWithPreview(t.Context(), data, "corrupt.avif", Options{Quality: 70, Threads: 1}); err == nil {
		t.Error("BytesWithPreview accepted corrupt image data")
	}
	if _, err := DecodePreviewImage(t.Context(), data); err == nil {
		t.Error("DecodePreviewImage accepted corrupt image data")
	}
}

func TestAVIFWithEXIFOrientationNotDoubleRotated(t *testing.T) {
	requireAVIF(t)
	// Encode an asymmetric 96x64 image with EXIF metadata, then patch the raw
	// Exif item's TIFF Orientation tag from 1 to 6. Per AVIF/MIAF, avifdec
	// already applies container irot/imir transforms so its decoded PNG pixels
	// are upright while still carrying the raw Exif Orientation tag in eXIf.
	src := samplePhotoImage(96, 64)
	exifOrient1 := exifTIFF(binary.LittleEndian, 1)
	exifOrient6 := exifTIFF(binary.LittleEndian, 6)
	basePNG := avifTestPNG(t, src, nil, 1)
	avifData, err := aviftools.EncodePNG(t.Context(), basePNG, aviftools.EncodeOptions{
		Quality:  90,
		Lossless: true,
		Metadata: aviftools.MetadataAll,
		Threads:  1,
	})
	if err != nil {
		t.Fatalf("aviftools.EncodePNG failed: %v", err)
	}
	idx := bytes.Index(avifData, exifOrient1)
	if idx < 0 {
		t.Fatal("encoded AVIF did not embed expected raw EXIF TIFF bytes")
	}
	patchedAVIF := bytes.Clone(avifData)
	copy(patchedAVIF[idx:idx+len(exifOrient6)], exifOrient6)
	if !bytes.Contains(patchedAVIF, exifOrient6) {
		t.Fatal("patched AVIF missing EXIF Orientation 6 payload")
	}

	wantSize := image.Pt(96, 64)
	preview, err := DecodePreviewImage(t.Context(), patchedAVIF)
	if err != nil {
		t.Fatalf("DecodePreviewImage failed: %v", err)
	}
	if preview.Bounds().Size() != wantSize {
		t.Fatalf("expected DecodePreviewImage size %v, got %v", wantSize, preview.Bounds().Size())
	}

	// Compressing the AVIF must NOT apply EXIF Orientation 6 a second time.
	for _, outFmt := range []OutputFormat{OutputFormatJPEG, OutputFormatAVIF} {
		t.Run(string(outFmt), func(t *testing.T) {
			res, resPreview, err := BytesWithPreview(t.Context(), patchedAVIF, "oriented.avif", Options{
				Quality:      60,
				OutputFormat: outFmt,
				Threads:      1,
			})
			if err != nil {
				t.Fatalf("BytesWithPreview(%s) failed: %v", outFmt, err)
			}
			if image.Pt(res.Width, res.Height) != wantSize {
				t.Fatalf("AVIF -> %s double-rotated dimensions: got %dx%d, want %dx%d",
					outFmt, res.Width, res.Height, wantSize.X, wantSize.Y)
			}
			if resPreview == nil {
				t.Fatalf("AVIF -> %s expected non-nil preview (unchanged=%v)", outFmt, res.Unchanged)
			}
			if resPreview.Bounds().Size() != wantSize {
				t.Fatalf("AVIF -> %s preview dimensions mismatch: got %v, want %v",
					outFmt, resPreview.Bounds().Size(), wantSize)
			}
		})
	}
}
