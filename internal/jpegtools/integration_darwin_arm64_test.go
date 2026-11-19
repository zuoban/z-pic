//go:build darwin && arm64

package jpegtools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func testPhotoRGBA(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	fw, fh := float64(w), float64(h)
	for y := range h {
		fy := float64(y) / fh
		for x := range w {
			fx := float64(x) / fw
			dx, dy := fx-0.7, fy-0.3
			sun := math.Exp(-(dx*dx + dy*dy) * 18.0)
			wave := 12.0 * math.Sin(fx*25.0+math.Cos(fy*12.0))
			clamp := func(v float64) uint8 {
				if v < 0 {
					return 0
				}
				if v > 255 {
					return 255
				}
				return uint8(v)
			}
			img.SetRGBA(x, y, color.RGBA{
				R: clamp(40 + 180*fy + 140*sun + wave),
				G: clamp(80 + 110*(1-fy) + 120*sun + 0.5*wave),
				B: clamp(190 - 120*fy + 50*sun),
				A: 255,
			})
		}
	}
	return img
}

func jpegSOFInfo(data []byte) (sofMarker byte, components int) {
	if len(data) < 4 || data[0] != 0xff || data[1] != 0xd8 {
		return 0, 0
	}
	for offset := 2; offset < len(data); {
		if data[offset] != 0xff {
			return 0, 0
		}
		for offset < len(data) && data[offset] == 0xff {
			offset++
		}
		if offset >= len(data) {
			return 0, 0
		}
		marker := data[offset]
		offset++
		if marker == 0xda || marker == 0xd9 {
			return 0, 0
		}
		if marker == 0x01 || (marker >= 0xd0 && marker <= 0xd7) {
			continue
		}
		if len(data)-offset < 2 {
			return 0, 0
		}
		length := int(binary.BigEndian.Uint16(data[offset : offset+2]))
		if length < 2 || length > len(data)-offset {
			return 0, 0
		}
		if marker >= 0xc0 && marker <= 0xcf && marker != 0xc4 && marker != 0xc8 && marker != 0xcc {
			if length >= 8 {
				return marker, int(data[offset+7])
			}
			return marker, 0
		}
		offset += length
	}
	return 0, 0
}

func TestBundledManifestAndVersionWithoutPATH(t *testing.T) {
	if !Available() {
		t.Fatal("MozJPEG must be bundled on darwin/arm64")
	}
	t.Setenv("PATH", "")

	manifestBytes, err := os.ReadFile(filepath.Join("assets", "darwin_arm64", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Version  string `json:"version"`
		Binaries map[string]struct {
			SHA256 string `json:"sha256"`
		} `json:"binaries"`
	}
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Version != "4.1.5" {
		t.Fatalf("manifest version = %q", manifest.Version)
	}
	for _, name := range []string{"cjpeg", "jpegtran"} {
		raw := bundledTool(name)
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != manifest.Binaries[name].SHA256 {
			t.Fatalf("%s checksum mismatch", name)
		}
		path, cleanup, err := extractTool(name)
		if err != nil {
			t.Fatal(err)
		}
		dir := filepath.Dir(path)
		cmd := exec.Command(path, "-version")
		cmd.Env = []string{"LC_ALL=C"}
		out, _ := cmd.CombinedOutput()
		cleanup()
		if !strings.Contains(string(out), "mozjpeg version 4.1.5") {
			t.Fatalf("%s -version output = %q", name, string(out))
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatalf("temp dir %s not cleaned up: %v", dir, err)
		}
	}
}

func TestMozJPEGEncodeProgressiveGrayscaleAndSavings(t *testing.T) {
	t.Setenv("PATH", "")
	img := testPhotoRGBA(320, 240)
	var stdBuf bytes.Buffer
	if err := jpeg.Encode(&stdBuf, img, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	mozBytes, err := EncodeImage(context.Background(), img, EncodeOptions{Quality: 80})
	if err != nil {
		t.Fatalf("EncodeImage failed: %v", err)
	}
	sof, comps := jpegSOFInfo(mozBytes)
	if sof != 0xc2 || comps != 3 {
		t.Fatalf("expected progressive 3-component JPEG (SOF2=0xc2), got marker=0x%x comps=%d", sof, comps)
	}
	decoded, err := jpeg.Decode(bytes.NewReader(mozBytes))
	if err != nil || decoded.Bounds().Dx() != 320 || decoded.Bounds().Dy() != 240 {
		t.Fatalf("decoded output invalid: %v %v", decoded, err)
	}
	if len(mozBytes) >= stdBuf.Len() {
		t.Fatalf("expected MozJPEG (%d) smaller than stdlib JPEG (%d)", len(mozBytes), stdBuf.Len())
	}

	// Grayscale input produces 1-component progressive JPEG.
	gray := image.NewGray(image.Rect(0, 0, 128, 96))
	for y := range 96 {
		for x := range 128 {
			gray.SetGray(x, y, color.Gray{Y: uint8((x*5 + y*7) % 256)})
		}
	}
	grayOut, err := EncodeImage(context.Background(), gray, EncodeOptions{Quality: 80})
	if err != nil {
		t.Fatalf("grayscale EncodeImage failed: %v", err)
	}
	sof, comps = jpegSOFInfo(grayOut)
	if sof != 0xc2 || comps != 1 {
		t.Fatalf("expected progressive 1-component grayscale JPEG, got marker=0x%x comps=%d", sof, comps)
	}

	// MaxOutputBytes triggers ErrNoSavings when exceeded.
	if _, err := EncodeImage(context.Background(), img, EncodeOptions{Quality: 80, MaxOutputBytes: len(mozBytes)}); !errors.Is(err, ErrNoSavings) {
		t.Fatalf("expected ErrNoSavings at exact boundary, got %v", err)
	}
}

func TestMozJPEGLosslessOptimizationAndTransforms(t *testing.T) {
	t.Setenv("PATH", "")
	img := testPhotoRGBA(160, 96) // Multiple of 16 for perfect lossless transforms.
	var baseBuf bytes.Buffer
	if err := jpeg.Encode(&baseBuf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	base := baseBuf.Bytes()
	origDecoded, err := jpeg.Decode(bytes.NewReader(base))
	if err != nil {
		t.Fatal(err)
	}

	opt, err := OptimizeLossless(context.Background(), base, OptimizeOptions{Copy: CopyAll, SmallerOnly: true})
	if err != nil {
		t.Fatalf("OptimizeLossless failed: %v", err)
	}
	if len(opt) >= len(base) {
		t.Fatalf("expected jpegtran (%d) to shrink baseline stdlib JPEG (%d)", len(opt), len(base))
	}
	sof, _ := jpegSOFInfo(opt)
	if sof != 0xc2 {
		t.Fatalf("expected progressive SOF2 output from jpegtran, got 0x%x", sof)
	}
	optDecoded, err := jpeg.Decode(bytes.NewReader(opt))
	if err != nil {
		t.Fatal(err)
	}
	// Verify 100% bit-for-bit identical decoded pixels!
	if optDecoded.Bounds() != origDecoded.Bounds() {
		t.Fatalf("bounds changed: %v vs %v", optDecoded.Bounds(), origDecoded.Bounds())
	}
	for y := 0; y < 96; y++ {
		for x := 0; x < 160; x++ {
			r1, g1, b1, a1 := origDecoded.At(x, y).RGBA()
			r2, g2, b2, a2 := optDecoded.At(x, y).RGBA()
			if r1 != r2 || g1 != g2 || b1 != b2 || a1 != a2 {
				t.Fatalf("lossless pixel mismatch at (%d,%d): got (%d,%d,%d,%d) want (%d,%d,%d,%d)",
					x, y, r2, g2, b2, a2, r1, g1, b1, a1)
			}
		}
	}

	// Re-optimizing already-optimized JPEG with SmallerOnly returns original bytes unchanged.
	again, err := OptimizeLossless(context.Background(), opt, OptimizeOptions{Copy: CopyAll, SmallerOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(again, opt) {
		t.Fatalf("expected no further change when already optimal: %d vs %d", len(again), len(opt))
	}

	// Perfect rotation (Orientation 6: 90 deg CW) on MCU-aligned 160x96 -> 96x160.
	rot, err := OptimizeLossless(context.Background(), base, OptimizeOptions{
		Copy:        CopyNone,
		Orientation: 6,
		Perfect:     true,
	})
	if err != nil {
		t.Fatalf("perfect orientation 6 failed: %v", err)
	}
	rotCfg, err := jpeg.DecodeConfig(bytes.NewReader(rot))
	if err != nil || rotCfg.Width != 96 || rotCfg.Height != 160 {
		t.Fatalf("rotated config = %+v err=%v", rotCfg, err)
	}

	// Non-MCU-aligned 163x99 with Perfect=true and Orientation=6 must return ErrImperfectTransform.
	odd := testPhotoRGBA(163, 99)
	var oddBuf bytes.Buffer
	if err := jpeg.Encode(&oddBuf, odd, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	if _, err := OptimizeLossless(context.Background(), oddBuf.Bytes(), OptimizeOptions{
		Copy:        CopyNone,
		Orientation: 6,
		Perfect:     true,
	}); !errors.Is(err, ErrImperfectTransform) {
		t.Fatalf("expected ErrImperfectTransform on non-MCU-aligned JPEG, got %v", err)
	}
}
