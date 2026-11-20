package compress

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolveOutputFormat(t *testing.T) {
	sources := []struct {
		format       string
		keepLossy    string
		keepLossless string
	}{
		{"jpeg", "jpeg", "jpeg"},
		{"png", "png", "png"},
		{"webp", "webp", "webp"},
		{"heic", "jpeg", "png"},
	}
	outputs := []struct {
		format OutputFormat
		want   string
	}{
		{OutputFormatKeep, ""},
		{OutputFormatJPEG, "jpeg"},
		{OutputFormatPNG, "png"},
		{OutputFormatWebP, "webp"},
	}
	for _, source := range sources {
		for _, output := range outputs {
			for _, lossless := range []bool{false, true} {
				want := output.want
				if output.format == OutputFormatKeep {
					want = source.keepLossy
					if lossless {
						want = source.keepLossless
					}
				}
				if got := ResolveOutputFormat(source.format, output.format, lossless); got != want {
					t.Errorf("ResolveOutputFormat(%q, %q, %v) = %q, want %q", source.format, output.format, lossless, got, want)
				}
			}
		}
	}
}

func TestFlattenAlphaForJPEG(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	src.SetNRGBA(0, 0, color.NRGBA{R: 0, G: 0, B: 0, A: 0})        // fully transparent -> white
	src.SetNRGBA(1, 0, color.NRGBA{R: 200, G: 100, B: 50, A: 255}) // opaque -> unchanged

	flat, err := flattenAlphaForJPEG(context.Background(), src)
	if err != nil {
		t.Fatalf("flattenAlphaForJPEG failed: %v", err)
	}
	r0, g0, b0, a0 := flat.At(0, 0).RGBA()
	if r0>>8 != 255 || g0>>8 != 255 || b0>>8 != 255 || a0>>8 != 255 {
		t.Fatalf("transparent pixel became (%d,%d,%d,%d), want white", r0>>8, g0>>8, b0>>8, a0>>8)
	}
	r1, g1, b1, a1 := flat.At(1, 0).RGBA()
	if r1>>8 != 200 || g1>>8 != 100 || b1>>8 != 50 || a1>>8 != 255 {
		t.Fatalf("opaque pixel became (%d,%d,%d,%d), want (200,100,50,255)", r1>>8, g1>>8, b1>>8, a1>>8)
	}
}

func makeRealHEICFixture(t *testing.T, img image.Image, extraArgs ...string) (string, []byte) {
	t.Helper()
	if runtime.GOOS != "darwin" || !SupportsHEIC() {
		t.Skip("macOS HEIC support unavailable")
	}
	dir := t.TempDir()
	inPath := filepath.Join(dir, "source.png")
	outPath := filepath.Join(dir, "sample.heic")
	f, err := os.Create(inPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()
	args := append([]string{"-s", "format", "heic"}, extraArgs...)
	args = append(args, "--out", outPath, inPath)
	if out, err := exec.Command("/usr/bin/sips", args...).CombinedOutput(); err != nil {
		t.Fatalf("sips encode heic failed: %v (%s)", err, out)
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	return outPath, data
}

func samplePhotoImage(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			r := uint8((x * 255) / max(1, w-1))
			g := uint8((y * 255) / max(1, h-1))
			b := uint8(((x + y) * 127) / max(1, w+h-2))
			img.SetRGBA(x, y, color.RGBA{R: r, G: g, B: b, A: 255})
		}
	}
	return img
}

func TestHEICInspectCompressAndUtilities(t *testing.T) {
	path, data := makeRealHEICFixture(t, samplePhotoImage(160, 120))
	ctx := context.Background()

	info, err := Inspect(ctx, path)
	if err != nil {
		t.Fatalf("Inspect(heic) failed: %v", err)
	}
	if info.Format != "heic" || info.Width != 160 || info.Height != 120 || info.Size != int64(len(data)) {
		t.Fatalf("Inspect(heic) = %+v, want heic 160x120 size=%d", info, len(data))
	}

	if fmtName, err := VerifyImageData(ctx, data); err != nil || fmtName != "heic" {
		t.Fatalf("VerifyImageData = (%q, %v), want (heic, nil)", fmtName, err)
	}

	// Default lossy compression -> JPEG
	resJPEG, err := Bytes(ctx, data, path, Options{Quality: 80})
	if err != nil {
		t.Fatalf("Bytes(heic -> jpeg) failed: %v", err)
	}
	if resJPEG.Format != "jpeg" || resJPEG.Width != 160 || resJPEG.Height != 120 || resJPEG.Unchanged {
		t.Fatalf("unexpected JPEG result: format=%q %dx%d unchanged=%v", resJPEG.Format, resJPEG.Width, resJPEG.Height, resJPEG.Unchanged)
	}
	if _, decFmt, err := image.Decode(bytes.NewReader(resJPEG.Data)); err != nil || decFmt != "jpeg" {
		t.Fatalf("JPEG output decode failed: fmt=%q err=%v", decFmt, err)
	}

	// Overwrite must be refused because source is HEIC and output is JPEG.
	resFromFile, err := File(ctx, path, Options{Quality: 80})
	if err != nil {
		t.Fatalf("File(heic) failed: %v", err)
	}
	if err := resFromFile.Overwrite(); err == nil {
		t.Fatalf("expected Overwrite() on HEIC->JPEG result to fail")
	}

	// Lossless compression -> PNG
	resPNG, err := Bytes(ctx, data, path, Options{Quality: 80, Lossless: true})
	if err != nil {
		t.Fatalf("Bytes(heic -> png) failed: %v", err)
	}
	if resPNG.Format != "png" || resPNG.Width != 160 || resPNG.Height != 120 {
		t.Fatalf("unexpected PNG result: format=%q %dx%d", resPNG.Format, resPNG.Width, resPNG.Height)
	}
	if _, decFmt, err := image.Decode(bytes.NewReader(resPNG.Data)); err != nil || decFmt != "png" {
		t.Fatalf("PNG output decode failed: fmt=%q err=%v", decFmt, err)
	}

	// WebP conversion + resize
	resWebP, previewImg, err := BytesWithPreview(ctx, data, path, Options{
		Quality:      80,
		OutputFormat: OutputFormatWebP,
		MaxDimension: 80,
	})
	if err != nil {
		t.Fatalf("BytesWithPreview(heic -> webp) failed: %v", err)
	}
	if resWebP.Format != "webp" || resWebP.Width != 80 || resWebP.Height != 60 {
		t.Fatalf("unexpected WebP result: format=%q %dx%d", resWebP.Format, resWebP.Width, resWebP.Height)
	}
	if previewImg == nil || previewImg.Bounds().Dx() != 80 || previewImg.Bounds().Dy() != 60 {
		t.Fatalf("unexpected WebP preview image: %v", previewImg)
	}

	// Resize on default JPEG output
	resResizedJPEG, err := Bytes(ctx, data, path, Options{Quality: 80, MaxDimension: 80})
	if err != nil {
		t.Fatalf("Bytes(heic -> resized jpeg) failed: %v", err)
	}
	if resResizedJPEG.Format != "jpeg" || resResizedJPEG.Width != 80 || resResizedJPEG.Height != 60 {
		t.Fatalf("unexpected resized JPEG result: format=%q %dx%d", resResizedJPEG.Format, resResizedJPEG.Width, resResizedJPEG.Height)
	}

	// Thumbnail
	thumb, err := Thumbnail(ctx, path, 48)
	if err != nil {
		t.Fatalf("Thumbnail(heic) failed: %v", err)
	}
	if thumb.Bounds().Dx() != 48 || thumb.Bounds().Dy() != 36 {
		t.Fatalf("Thumbnail(heic) bounds = %v, want 48x36", thumb.Bounds())
	}

	// Clipboard PNGBytes
	clipPNG, err := PNGBytes(ctx, data)
	if err != nil {
		t.Fatalf("PNGBytes(heic) failed: %v", err)
	}
	if cfg, decFmt, err := image.DecodeConfig(bytes.NewReader(clipPNG)); err != nil || decFmt != "png" || cfg.Width != 160 || cfg.Height != 120 {
		t.Fatalf("PNGBytes(heic) output invalid: fmt=%q cfg=%+v err=%v", decFmt, cfg, err)
	}
}

func TestHEICMetadataStripAndKeepICC(t *testing.T) {
	if runtime.GOOS != "darwin" || !SupportsHEIC() {
		t.Skip("macOS HEIC support unavailable")
	}
	p3Profile := "/System/Library/ColorSync/Profiles/Display P3.icc"
	if _, err := os.Stat(p3Profile); err != nil {
		t.Skip("Display P3 ICC profile not found")
	}
	path, data := makeRealHEICFixture(t, samplePhotoImage(96, 72), "-m", p3Profile)
	ctx := context.Background()

	resKeep, err := Bytes(ctx, data, path, Options{Quality: 80, Metadata: MetadataKeepICC})
	if err != nil {
		t.Fatalf("HEIC -> JPEG keep-icc failed: %v", err)
	}
	if len(extractJPEGICCProfile(resKeep.Data, 3)) == 0 {
		t.Fatalf("expected JPEG output from Display P3 HEIC to retain ICC profile")
	}

	resStrip, err := Bytes(ctx, data, path, Options{Quality: 80, Metadata: MetadataStrip})
	if err != nil {
		t.Fatalf("HEIC -> JPEG strip failed: %v", err)
	}
	if len(extractJPEGICCProfile(resStrip.Data, 3)) != 0 {
		t.Fatalf("expected JPEG output with MetadataStrip to drop ICC profile")
	}

	resWebPKeep, err := Bytes(ctx, data, path, Options{Quality: 80, OutputFormat: OutputFormatWebP, Metadata: MetadataKeepICC})
	if err != nil {
		t.Fatalf("HEIC -> WebP keep-icc failed: %v", err)
	}
	if len(extractWebPICCProfile(resWebPKeep.Data)) == 0 {
		t.Fatalf("expected WebP output from Display P3 HEIC to retain ICC profile")
	}
}
