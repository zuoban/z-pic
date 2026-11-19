package compress

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func syntheticRGBICC() []byte {
	icc := make([]byte, 128)
	binary.BigEndian.PutUint32(icc[0:4], 128)
	copy(icc[16:20], "RGB ")
	copy(icc[36:40], "acsp")
	return icc
}

func buildPNGChunk(kind string, payload []byte) []byte {
	out := make([]byte, len(payload)+12)
	binary.BigEndian.PutUint32(out[0:4], uint32(len(payload)))
	copy(out[4:8], kind)
	copy(out[8:8+len(payload)], payload)
	binary.BigEndian.PutUint32(out[8+len(payload):], crc32.ChecksumIEEE(out[4:8+len(payload)]))
	return out
}

func buildPNGWithMetadata(t *testing.T, img image.Image, icc []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	var zbuf bytes.Buffer
	zw := zlib.NewWriter(&zbuf)
	if _, err := zw.Write(icc); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	chunks := [][]byte{
		buildPNGChunk("iCCP", append([]byte("DisplayP3\x00\x00"), zbuf.Bytes()...)),
		buildPNGChunk("tEXt", []byte("Software\x00SecretApp")),
		buildPNGChunk("eXIf", []byte{'I', 'I', 42, 0, 8, 0, 0, 0, 0, 0, 0, 0, 0, 0}),
	}
	return injectPNGChunksAfterIHDR(raw, chunks)
}

func webpHasChunk(data []byte, want string) bool {
	if len(data) < 20 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return false
	}
	for offset := 12; offset <= len(data)-8; {
		n := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		if n < 0 || n > len(data)-offset-8 {
			return false
		}
		if string(data[offset:offset+4]) == want {
			return true
		}
		offset += 8 + n + n%2
	}
	return false
}

func makeJPEGWithICCAndOrient(t *testing.T, img image.Image, icc []byte, orient int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	// Build APP2 ICC segment.
	iccPayload := append(append([]byte("ICC_PROFILE\x00"), 1, 1), icc...)
	app2 := make([]byte, 4+len(iccPayload))
	app2[0], app2[1] = 0xff, 0xe2
	binary.BigEndian.PutUint16(app2[2:4], uint16(len(iccPayload)+2))
	copy(app2[4:], iccPayload)

	var segments [][]byte
	if orient >= 1 && orient <= 8 {
		exif := make([]byte, 6+8+2+12+4)
		copy(exif[0:6], "Exif\x00\x00")
		copy(exif[6:8], "II")
		binary.LittleEndian.PutUint16(exif[8:10], 42)
		binary.LittleEndian.PutUint32(exif[10:14], 8)
		binary.LittleEndian.PutUint16(exif[14:16], 1)
		binary.LittleEndian.PutUint16(exif[16:18], 0x0112)
		binary.LittleEndian.PutUint16(exif[18:20], 3)
		binary.LittleEndian.PutUint32(exif[20:24], 1)
		binary.LittleEndian.PutUint16(exif[24:26], uint16(orient))
		app1 := make([]byte, 4+len(exif))
		app1[0], app1[1] = 0xff, 0xe1
		binary.BigEndian.PutUint16(app1[2:4], uint16(len(exif)+2))
		copy(app1[4:], exif)
		segments = append(segments, app1)
	}
	segments = append(segments, app2)
	return injectJPEGICCSegments(raw, segments, len(app2)+40)
}

func TestValidateNewOptions(t *testing.T) {
	if _, err := Bytes(context.Background(), []byte("x"), "/tmp/a.jpg", Options{Quality: 80, Metadata: "bad"}); err == nil {
		t.Fatal("invalid MetadataPolicy must fail")
	}
	if _, err := Bytes(context.Background(), []byte("x"), "/tmp/a.jpg", Options{Quality: 80, OutputFormat: "avif"}); err == nil {
		t.Fatal("invalid OutputFormat must fail")
	}
	for _, dim := range []int{-1, 1, 63, 16385} {
		if _, err := Bytes(context.Background(), []byte("x"), "/tmp/a.jpg", Options{Quality: 80, MaxDimension: dim}); err == nil {
			t.Fatalf("invalid MaxDimension %d must fail", dim)
		}
	}
}

func TestJPEGMetadataResizeLosslessAndSkipSnapshot(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 320, 160))
	for y := range 160 {
		for x := range 320 {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x % 256), G: uint8((y * 2) % 256), B: 90, A: 255})
		}
	}
	icc := syntheticRGBICC()
	// Orientation 6 rotates raw 320x160 to upright 160x320.
	data := makeJPEGWithICCAndOrient(t, img, icc, 6)

	// 1. Lossless JPEG without resize retains exact original bytes.
	losslessRes, err := Bytes(context.Background(), data, "/tmp/photo.jpg", Options{Quality: 80, Lossless: true})
	if err != nil {
		t.Fatalf("lossless JPEG failed: %v", err)
	}
	if !losslessRes.Unchanged || !bytes.Equal(losslessRes.Data, data) || losslessRes.Width != 160 || losslessRes.Height != 320 {
		t.Fatalf("lossless JPEG must retain exact bytes and upright dimensions: unchanged=%v %dx%d", losslessRes.Unchanged, losslessRes.Width, losslessRes.Height)
	}

	// 2. MetadataKeepICC keeps ICC after APP0 JFIF; MetadataStrip drops ICC.
	iccRes, err := Bytes(context.Background(), data, "/tmp/photo.jpg", Options{Quality: 75, Metadata: MetadataKeepICC})
	if err != nil {
		t.Fatalf("JPEG MetadataKeepICC failed: %v", err)
	}
	if segs, _ := jpegICCSegments(iccRes.Data); len(segs) == 0 {
		t.Fatal("MetadataKeepICC must retain JPEG ICC profile")
	}
	// Verify JFIF ordering when APP0 is present right after SOI.
	jfifSample := []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x04, 'J', 'F', 0xff, 0xd9}
	fakeICC := []byte{0xff, 0xe2, 0x00, 0x04, 'I', 'C'}
	injected := injectJPEGICCSegments(jfifSample, [][]byte{fakeICC}, len(fakeICC))
	if len(injected) < 10 || injected[2] != 0xff || injected[3] != 0xe0 || injected[8] != 0xff || injected[9] != 0xe2 {
		t.Fatalf("APP2 ICC must follow APP0 JFIF when present: %x", injected)
	}
	stripRes, err := Bytes(context.Background(), data, "/tmp/photo.jpg", Options{Quality: 75, Metadata: MetadataStrip})
	if err != nil {
		t.Fatalf("JPEG MetadataStrip failed: %v", err)
	}
	if segs, _ := jpegICCSegments(stripRes.Data); len(segs) != 0 {
		t.Fatal("MetadataStrip must remove JPEG ICC profile")
	}
	if jpegOrientation(stripRes.Data) != 1 {
		t.Fatal("MetadataStrip must strip EXIF orientation after baking pixels")
	}

	// 3. Resize respects upright orientation (160x320 -> 80x160 for MaxDimension=160) and never upscales.
	resizedRes, err := Bytes(context.Background(), data, "/tmp/photo.jpg", Options{Quality: 80, MaxDimension: 160})
	if err != nil {
		t.Fatalf("JPEG resize failed: %v", err)
	}
	if resizedRes.Width != 80 || resizedRes.Height != 160 || resizedRes.Unchanged {
		t.Fatalf("expected upright resized 80x160, got %dx%d unchanged=%v", resizedRes.Width, resizedRes.Height, resizedRes.Unchanged)
	}
	noUpscale, err := Bytes(context.Background(), data, "/tmp/photo.jpg", Options{Quality: 80, MaxDimension: 1000})
	if err != nil {
		t.Fatalf("JPEG no-upscale failed: %v", err)
	}
	if noUpscale.Width != 160 || noUpscale.Height != 320 {
		t.Fatalf("must not upscale smaller image, got %dx%d", noUpscale.Width, noUpscale.Height)
	}

	// 4. SkipOverwriteSnapshot allows Save() and cleanly rejects Overwrite().
	skipRes, err := Bytes(context.Background(), data, "/tmp/photo.jpg", Options{Quality: 70, SkipOverwriteSnapshot: true})
	if err != nil {
		t.Fatalf("SkipOverwriteSnapshot failed: %v", err)
	}
	if err := skipRes.Overwrite(); err == nil {
		t.Fatal("Overwrite must be refused when SkipOverwriteSnapshot is set")
	}
	dest := filepath.Join(t.TempDir(), "copy.jpg")
	if err := skipRes.Save(dest); err != nil {
		t.Fatalf("Save must still work with SkipOverwriteSnapshot: %v", err)
	}
}

func TestNativeLosslessMetadataConvertAndResize(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("bundled native PNG/WebP tools currently ship only for darwin/arm64")
	}
	// Multi-color transparent gradient (>1000 unique colors so pngquant would alter pixels).
	srcImg := image.NewNRGBA(image.Rect(0, 0, 200, 100))
	for y := range 100 {
		for x := range 200 {
			srcImg.SetNRGBA(x, y, color.NRGBA{
				R: uint8((x * 3) % 256),
				G: uint8((y * 5) % 256),
				B: uint8((x + y) % 256),
				A: uint8(120 + (x % 136)),
			})
		}
	}
	icc := syntheticRGBICC()
	pngData := buildPNGWithMetadata(t, srcImg, icc)

	// 1. Lossless PNG keeps bit-exact pixels and strips tEXt/eXIf under MetadataKeepICC.
	pngLossless, err := Bytes(context.Background(), pngData, "/tmp/grad.png", Options{
		Quality:  80,
		Lossless: true,
		Metadata: MetadataKeepICC,
	})
	if err != nil {
		t.Fatalf("lossless PNG failed: %v", err)
	}
	if pngHasChunk(pngLossless.Data, "tEXt") || pngHasChunk(pngLossless.Data, "eXIf") {
		t.Fatal("PNG MetadataKeepICC must strip tEXt and eXIf")
	}
	if !pngHasChunk(pngLossless.Data, "iCCP") {
		t.Fatal("PNG MetadataKeepICC must retain iCCP")
	}
	decodedPNG, _, err := image.Decode(bytes.NewReader(pngLossless.Data))
	if err != nil {
		t.Fatalf("decode lossless PNG failed: %v", err)
	}
	for y := range 100 {
		for x := range 200 {
			if color.NRGBAModel.Convert(decodedPNG.At(x, y)) != srcImg.NRGBAAt(x, y) {
				t.Fatalf("lossless PNG pixel changed at (%d,%d)", x, y)
			}
		}
	}

	// 2. PNG MetadataStrip removes iCCP too.
	pngStrip, err := Bytes(context.Background(), pngData, "/tmp/grad.png", Options{
		Quality:  80,
		Lossless: true,
		Metadata: MetadataStrip,
	})
	if err != nil {
		t.Fatalf("PNG MetadataStrip failed: %v", err)
	}
	if pngHasChunk(pngStrip.Data, "iCCP") {
		t.Fatal("PNG MetadataStrip must remove iCCP")
	}

	// 3. PNG resize + convert to WebP (with ICC preserved and Overwrite refused).
	webpFromPNG, err := Bytes(context.Background(), pngData, "/tmp/grad.png", Options{
		Quality:      80,
		OutputFormat: OutputFormatWebP,
		MaxDimension: 100,
		Metadata:     MetadataKeepICC,
	})
	if err != nil {
		t.Fatalf("PNG->WebP resize failed: %v", err)
	}
	if webpFromPNG.Format != "webp" || webpFromPNG.Width != 100 || webpFromPNG.Height != 50 {
		t.Fatalf("unexpected PNG->WebP output: format=%s %dx%d", webpFromPNG.Format, webpFromPNG.Width, webpFromPNG.Height)
	}
	if !webpHasChunk(webpFromPNG.Data, "ICCP") || webpHasChunk(webpFromPNG.Data, "EXIF") {
		t.Fatal("PNG->WebP MetadataKeepICC must inject ICCP and have no EXIF")
	}
	if err := webpFromPNG.Overwrite(); err == nil {
		t.Fatal("converted WebP result must never overwrite original PNG")
	}

	// 4. Oriented WebP with EXIF orientation 6 + ICCP: MetadataKeepICC strips EXIF, bakes upright pixels, keeps ICCP.
	fixture, err := os.ReadFile(filepath.Join("..", "webptools", "testdata", "alpha-metadata.webp"))
	if err != nil {
		t.Fatal(err)
	}
	// Fixture raw is 19x13 with EXIF orientation 6 => upright 13x19.
	webpRes, err := Bytes(context.Background(), fixture, "/tmp/oriented.webp", Options{
		Quality:  85,
		Lossless: true,
		Metadata: MetadataKeepICC,
	})
	if err != nil {
		t.Fatalf("oriented WebP MetadataKeepICC failed: %v", err)
	}
	if webpRes.Width != 13 || webpRes.Height != 19 {
		t.Fatalf("expected upright 13x19 after baking orientation 6, got %dx%d", webpRes.Width, webpRes.Height)
	}
	if webpHasChunk(webpRes.Data, "EXIF") || webpHasChunk(webpRes.Data, "XMP ") {
		t.Fatal("WebP MetadataKeepICC must strip EXIF and XMP")
	}
	if !webpHasChunk(webpRes.Data, "ICCP") {
		t.Fatal("WebP MetadataKeepICC must retain ICCP after baking orientation")
	}
	if WebPOrientation(webpRes.Data) != 1 {
		t.Fatal("baked WebP should have normal orientation 1")
	}

	// 5. PNG resize (keeping PNG format) downscales accurately.
	pngResized, err := Bytes(context.Background(), pngData, "/tmp/grad.png", Options{
		Quality:      80,
		Lossless:     true,
		MaxDimension: 100,
		Metadata:     MetadataKeepICC,
	})
	if err != nil {
		t.Fatalf("PNG resize failed: %v", err)
	}
	if pngResized.Format != "png" || pngResized.Width != 100 || pngResized.Height != 50 {
		t.Fatalf("expected resized PNG 100x50, got %s %dx%d", pngResized.Format, pngResized.Width, pngResized.Height)
	}
	if !pngHasChunk(pngResized.Data, "iCCP") {
		t.Fatal("resized PNG with MetadataKeepICC must preserve iCCP")
	}
}
