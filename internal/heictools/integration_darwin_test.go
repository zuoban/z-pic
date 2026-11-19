//go:build darwin

package heictools

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func createTestHEICFromPNG(t *testing.T, img image.Image, extraArgs ...string) []byte {
	t.Helper()
	dir := t.TempDir()
	inPath := filepath.Join(dir, "in.png")
	outPath := filepath.Join(dir, "out.heic")
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
	return data
}

func createOrientedTestHEIC(t *testing.T, w, h, orient int) []byte {
	t.Helper()
	dir := t.TempDir()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			switch {
			case y < h/2 && x < w/2:
				img.SetRGBA(x, y, color.RGBA{R: 220, G: 30, B: 30, A: 255})
			case y < h/2:
				img.SetRGBA(x, y, color.RGBA{R: 30, G: 200, B: 30, A: 255})
			case x < w/2:
				img.SetRGBA(x, y, color.RGBA{R: 30, G: 30, B: 220, A: 255})
			default:
				img.SetRGBA(x, y, color.RGBA{R: 230, G: 230, B: 230, A: 255})
			}
		}
	}
	var jpgBuf bytes.Buffer
	if err := jpeg.Encode(&jpgBuf, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	jb := jpgBuf.Bytes()
	var tiff bytes.Buffer
	tiff.WriteString("MM")
	_ = binary.Write(&tiff, binary.BigEndian, uint16(42))
	_ = binary.Write(&tiff, binary.BigEndian, uint32(8))
	_ = binary.Write(&tiff, binary.BigEndian, uint16(1))
	_ = binary.Write(&tiff, binary.BigEndian, uint16(0x0112))
	_ = binary.Write(&tiff, binary.BigEndian, uint16(3))
	_ = binary.Write(&tiff, binary.BigEndian, uint32(1))
	_ = binary.Write(&tiff, binary.BigEndian, uint16(orient))
	_ = binary.Write(&tiff, binary.BigEndian, uint16(0))
	_ = binary.Write(&tiff, binary.BigEndian, uint32(0))

	var app1 bytes.Buffer
	app1.Write([]byte{0xff, 0xe1})
	_ = binary.Write(&app1, binary.BigEndian, uint16(2+6+tiff.Len()))
	app1.WriteString("Exif\x00\x00")
	app1.Write(tiff.Bytes())

	rotJPG := make([]byte, 0, len(jb)+app1.Len())
	rotJPG = append(rotJPG, jb[:2]...)
	rotJPG = append(rotJPG, app1.Bytes()...)
	rotJPG = append(rotJPG, jb[2:]...)

	inPath := filepath.Join(dir, "rot.jpg")
	outPath := filepath.Join(dir, "rot.heic")
	if err := os.WriteFile(inPath, rotJPG, 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("/usr/bin/sips", "-s", "format", "heic", "--out", outPath, inPath).CombinedOutput(); err != nil {
		t.Fatalf("sips encode oriented heic failed: %v (%s)", err, out)
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestDarwinInspectAndDecodeRealHEIC(t *testing.T) {
	if !Available() {
		t.Skip("sips not available")
	}
	img := image.NewNRGBA(image.Rect(0, 0, 96, 64))
	for y := range 64 {
		for x := range 96 {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 2), G: uint8(y * 3), B: 180, A: 200})
		}
	}
	heic := createTestHEICFromPNG(t, img)

	info, err := InspectBytes(context.Background(), heic)
	if err != nil {
		t.Fatalf("InspectBytes failed: %v", err)
	}
	if info.Width != 96 || info.Height != 64 {
		t.Fatalf("got %dx%d, want 96x64", info.Width, info.Height)
	}

	pngBytes, err := DecodePNG(context.Background(), heic)
	if err != nil {
		t.Fatalf("DecodePNG failed: %v", err)
	}
	decoded, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatalf("png.Decode failed: %v", err)
	}
	if decoded.Bounds().Dx() != 96 || decoded.Bounds().Dy() != 64 {
		t.Fatalf("decoded %dx%d, want 96x64", decoded.Bounds().Dx(), decoded.Bounds().Dy())
	}

	// Thumbnail downscale must not exceed maxEdge and must not upscale when maxEdge > source.
	thumbSmall, err := DecodeThumbnailPNG(context.Background(), heic, 32)
	if err != nil {
		t.Fatalf("DecodeThumbnailPNG(32) failed: %v", err)
	}
	cfgSmall, err := png.DecodeConfig(bytes.NewReader(thumbSmall))
	if err != nil {
		t.Fatal(err)
	}
	if max(cfgSmall.Width, cfgSmall.Height) != 32 {
		t.Fatalf("thumb(32) got %dx%d, want maxEdge 32", cfgSmall.Width, cfgSmall.Height)
	}

	thumbNoUpscale, err := DecodeThumbnailPNG(context.Background(), heic, 256)
	if err != nil {
		t.Fatalf("DecodeThumbnailPNG(256) failed: %v", err)
	}
	cfgNoUpscale, err := png.DecodeConfig(bytes.NewReader(thumbNoUpscale))
	if err != nil {
		t.Fatal(err)
	}
	if cfgNoUpscale.Width != 96 || cfgNoUpscale.Height != 64 {
		t.Fatalf("thumb(256) upscaled to %dx%d, want 96x64", cfgNoUpscale.Width, cfgNoUpscale.Height)
	}
}

func TestDarwinInspectRotatedHEICMatchesVisualDimensions(t *testing.T) {
	if !Available() {
		t.Skip("sips not available")
	}
	for orient := 1; orient <= 8; orient++ {
		heic := createOrientedTestHEIC(t, 80, 40, orient)
		info, err := InspectBytes(context.Background(), heic)
		if err != nil {
			t.Fatalf("orient=%d InspectBytes failed: %v", orient, err)
		}
		wantW, wantH := 80, 40
		if orient >= 5 && orient <= 8 {
			wantW, wantH = 40, 80
		}
		if info.Width != wantW || info.Height != wantH {
			t.Fatalf("orient=%d Inspect got %dx%d, want %dx%d", orient, info.Width, info.Height, wantW, wantH)
		}
	}
}

func TestDarwinDecodeCanceled(t *testing.T) {
	if !Available() {
		t.Skip("sips not available")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := DecodePNG(ctx, []byte("not-even-read")); err == nil {
		t.Fatal("expected error on canceled context")
	}
}
