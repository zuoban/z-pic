package pngtools

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"testing"
)

func requireBundle(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("native assets currently bundled only for darwin/arm64")
	}
}

func chunk(name string, data []byte) []byte {
	b := make([]byte, len(data)+12)
	binary.BigEndian.PutUint32(b, uint32(len(data)))
	copy(b[4:], name)
	copy(b[8:], data)
	binary.BigEndian.PutUint32(b[len(b)-4:], crc32.ChecksumIEEE(b[4:len(b)-4]))
	return b
}

func withChunks(data []byte, chunks ...[]byte) []byte {
	out := bytes.Clone(data[:33]) // after IHDR
	for _, c := range chunks {
		out = append(out, c...)
	}
	return append(out, data[33:]...)
}

func metadata(data []byte) [][]byte {
	var out [][]byte
	for offset := 8; offset < len(data); {
		size := int(binary.BigEndian.Uint32(data[offset:]))
		name := string(data[offset+4 : offset+8])
		if name != "IDAT" && name != "IHDR" && name != "PLTE" && name != "tRNS" {
			out = append(out, data[offset:offset+size+12])
		}
		offset += size + 12
	}
	sort.Slice(out, func(i, j int) bool { return bytes.Compare(out[i], out[j]) < 0 })
	return out
}

func TestEmbeddedLosslessSamplesAndMetadata(t *testing.T) {
	requireBundle(t)
	t.Setenv("PATH", "")
	temp := t.TempDir()
	t.Setenv("TMPDIR", temp)
	nrgba := image.NewNRGBA(image.Rect(0, 0, 40, 30))
	deep := image.NewNRGBA64(nrgba.Bounds())
	gray := image.NewGray16(nrgba.Bounds())
	palette := image.NewPaletted(nrgba.Bounds(), color.Palette{
		color.NRGBA{R: 43, G: 62, B: 93, A: 0},
		color.NRGBA{R: 8, G: 20, B: 59, A: 255},
	})
	for y := 0; y < 30; y++ {
		for x := 0; x < 40; x++ {
			nrgba.SetNRGBA(x, y, color.NRGBA{R: byte(x), G: byte(y), B: 99, A: byte((x % 3) * 127)})
			deep.SetNRGBA64(x, y, color.NRGBA64{R: uint16(x*513 + 1), G: uint16(y*1025 + 3), B: 65533, A: uint16((x % 3) * 32767)})
			gray.SetGray16(x, y, color.Gray16{Y: uint16(x*1023 + y)})
			palette.SetColorIndex(x, y, uint8(x%2))
		}
	}
	for name, img := range map[string]image.Image{
		"rgba": nrgba, "rgba16": deep, "gray16": gray, "palette": palette,
		"rgba-normal-reductions": nrgba, "rgba16-normal-reductions": deep,
	} {
		t.Run(name, func(t *testing.T) {
			data := withChunks(encodePNG(t, img),
				chunk("tEXt", []byte("Comment\x00keep me")),
				chunk("pHYs", []byte{0, 0, 14, 196, 0, 0, 14, 196, 1}),
				chunk("eXIf", []byte{'I', 'I', 42, 0, 8, 0, 0, 0, 0, 0, 0, 0, 0, 0}),
				chunk("vpAg", []byte{1, 2, 3, 4}),
			)
			if name == "rgba" || name == "rgba16" {
				bits := data[24]
				data = withChunks(data, chunk("sBIT", []byte{bits, bits, bits, bits}), chunk("bKGD", make([]byte, 6)))
			}
			out, err := Optimize(context.Background(), data)
			if err != nil {
				t.Fatal(err)
			}
			if len(out) >= len(data) {
				t.Fatalf("expected real compression: %d -> %d", len(data), len(out))
			}
			if !reflect.DeepEqual(metadata(data), metadata(out)) {
				t.Fatal("metadata chunks changed")
			}
			before, err := png.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			after, err := png.Decode(bytes.NewReader(out))
			if err != nil {
				t.Fatal(err)
			}
			for y := 0; y < 30; y++ {
				for x := 0; x < 40; x++ {
					a := color.NRGBA64Model.Convert(before.At(x, y))
					b := color.NRGBA64Model.Convert(after.At(x, y))
					if a != b {
						t.Fatalf("sample changed at (%d,%d): %v -> %v", x, y, a, b)
					}
				}
			}
		})
	}
	for _, name := range []string{"caBX", "iDOT"} {
		data := withChunks(encodePNG(t, nrgba), chunk(name, []byte("immutable")))
		out, err := Optimize(context.Background(), data)
		if err != nil || !bytes.Equal(out, data) {
			t.Fatalf("%s was changed: %v", name, err)
		}
	}
	if _, err := Optimize(context.Background(), []byte("broken PNG")); err == nil {
		t.Fatal("invalid PNG accepted")
	}
	entries, err := os.ReadDir(temp)
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary tool directories leaked: %v %v", entries, err)
	}
}

func TestEmbeddedNormalReductions(t *testing.T) {
	requireBundle(t)
	// All channels have exactly representable 8-bit values, so lossless bit
	// depth / palette reduction should remain enabled for ordinary inputs.
	img := image.NewNRGBA64(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.SetNRGBA64(x, y, color.NRGBA64{R: uint16(x%2) * 65535, G: 80 * 257, B: 40 * 257, A: 65535})
		}
	}
	data := encodePNG(t, img)
	out, err := Optimize(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if out[24] == data[24] && out[25] == data[25] {
		t.Fatal("ordinary PNG representation was not reduced")
	}
	decoded, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			if color.NRGBA64Model.Convert(decoded.At(x, y)) != img.At(x, y) {
				t.Fatalf("lossless reduction changed pixel (%d,%d)", x, y)
			}
		}
	}
}

func TestEmbeddedQuantizeThenOptimize(t *testing.T) {
	requireBundle(t)
	t.Setenv("PATH", "")
	temp := t.TempDir()
	t.Setenv("TMPDIR", temp)
	img := image.NewNRGBA(image.Rect(0, 0, 128, 128))
	for y := 0; y < 128; y++ {
		for x := 0; x < 128; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: byte(x * 2), G: byte(y * 2), B: byte(x + y), A: 255})
		}
	}
	data := encodePNG(t, img)
	quantized, err := Quantize(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(quantized, data) || quantized[25] != 3 {
		t.Fatal("expected genuinely quantized indexed PNG")
	}
	out, err := Optimize(context.Background(), quantized)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := png.Decode(bytes.NewReader(out))
	if err != nil || decoded.Bounds() != img.Bounds() || len(out) >= len(data) {
		t.Fatalf("invalid pipeline result: err=%v size=%d -> %d", err, len(data), len(out))
	}
	tiny := encodePNG(t, image.NewNRGBA(image.Rect(0, 0, 1, 1)))
	out, err = Quantize(context.Background(), tiny)
	if err != nil || !bytes.Equal(out, tiny) {
		t.Fatalf("tiny no-saving fallback failed: %v", err)
	}
	entries, err := os.ReadDir(temp)
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary tool directories leaked: %v %v", entries, err)
	}
}

func TestEmbeddedExecutableVersionsAndPermissions(t *testing.T) {
	requireBundle(t)
	for name, version := range map[string]string{"oxipng": "10.2.1", "pngquant": "3.0.3"} {
		path, cleanup, err := extractTool(name)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer cleanup()
			for target, mode := range map[string]os.FileMode{path: 0700, filepath.Dir(path): 0700} {
				info, err := os.Stat(target)
				if err != nil || info.Mode().Perm() != mode {
					t.Fatalf("permissions for %s: %v %v", target, info, err)
				}
			}
			cmd := exec.Command(path, "--version")
			cmd.Env = []string{"PATH="}
			out, err := cmd.CombinedOutput()
			if err != nil || !bytes.Contains(out, []byte(version)) {
				t.Fatalf("%s version: %q %v", name, out, err)
			}
		}()
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("tool was not removed: %v", err)
		}
	}
}
