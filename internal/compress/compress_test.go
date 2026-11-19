package compress

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"zpic/internal/contextio"
)

func writeFixture(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func jpegFixture(t *testing.T, quality int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 128, 96))
	for y := 0; y < 96; y++ {
		for x := 0; x < 128; x++ {
			img.SetRGBA(x, y, color.RGBA{
				R: uint8(x*37 + y*19), G: uint8(x*11 + y*43),
				B: uint8(x*71 + y*7), A: 255,
			})
		}
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: quality}); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func pngFixture(t *testing.T, img image.Image, level png.CompressionLevel) []byte {
	t.Helper()
	var out bytes.Buffer
	encoder := png.Encoder{CompressionLevel: level}
	if err := encoder.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func inputMethods(data []byte) []struct {
	name string
	run  func(context.Context, string, Options) (*Result, error)
} {
	return []struct {
		name string
		run  func(context.Context, string, Options) (*Result, error)
	}{
		{"File", File},
		{"Bytes", func(ctx context.Context, path string, options Options) (*Result, error) {
			return Bytes(ctx, data, path, options)
		}},
		{"BytesWithPreview", func(ctx context.Context, path string, options Options) (*Result, error) {
			result, _, err := BytesWithPreview(ctx, data, path, options)
			return result, err
		}},
	}
}

func TestJPEGCompressionPreservesDimensions(t *testing.T) {
	data := jpegFixture(t, 100)
	// The real format, rather than the extension, determines the encoder.
	path := writeFixture(t, "photo.png", data)
	result, err := File(context.Background(), path, Options{Quality: 35})
	if err != nil {
		t.Fatal(err)
	}
	if result.Format != "jpeg" || result.Width != 128 || result.Height != 96 ||
		result.SourcePath != path || result.Unchanged {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.OriginalSize != int64(len(data)) ||
		result.OutputSize != int64(len(result.Data)) || result.OutputSize >= result.OriginalSize {
		t.Fatal("JPEG did not compress or size metadata is incorrect")
	}
	config, err := jpeg.DecodeConfig(bytes.NewReader(result.Data))
	if err != nil || config.Width != 128 || config.Height != 96 {
		t.Fatalf("output dimensions: %+v, %v", config, err)
	}
}

func TestPNGSupports8And16BitTransparency(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("bundled native encoders require darwin/arm64")
	}
	for _, depth := range []int{8, 16} {
		t.Run(strconv.Itoa(depth)+"bit", func(t *testing.T) {
			img := image.NewNRGBA64(image.Rect(0, 0, 32, 24))
			for y := 0; y < 24; y++ {
				for x := 0; x < 32; x++ {
					c := color.NRGBA64{R: 0x1234, G: 0xabcd, B: 0x5678,
						A: []uint16{0, 1, 0x8080, 0xffff}[x%4]}
					if depth == 8 {
						c.R, c.G, c.B, c.A = 0x1212, 0xabab, 0x5656, uint16(x%4)*0x5555
					}
					img.SetNRGBA64(x, y, c)
				}
			}
			var input image.Image = img
			if depth == 8 {
				nrgba := image.NewNRGBA(img.Bounds())
				for y := 0; y < 24; y++ {
					for x := 0; x < 32; x++ {
						nrgba.Set(x, y, img.At(x, y))
					}
				}
				input = nrgba
			}
			data := pngFixture(t, input, png.NoCompression)
			path := writeFixture(t, "alpha.jpg", data)
			for _, method := range inputMethods(data) {
				t.Run(method.name, func(t *testing.T) {
					result, err := method.run(context.Background(), path, Options{Quality: 80})
					if err != nil {
						t.Fatal(err)
					}
					assertPNGResult(t, result, data, 32, 24)
					if result.Unchanged || result.OutputSize >= result.OriginalSize {
						t.Fatal("uncompressed alpha fixture did not shrink")
					}
					assertPNGTransparency(t, result.Data)
				})
			}
		})
	}
}

func TestNoSavingsReturnsOriginalBytes(t *testing.T) {
	for name, data := range map[string][]byte{
		"jpeg": jpegFixture(t, 1),
	} {
		t.Run(name, func(t *testing.T) {
			result, err := File(context.Background(), writeFixture(t, name, data), Options{Quality: 100})
			if err != nil {
				t.Fatal(err)
			}
			if !result.Unchanged || !bytes.Equal(result.Data, data) ||
				result.OutputSize != result.OriginalSize || result.OutputSize != int64(len(data)) {
				t.Fatal("expected byte-exact original data when encoding has no benefit")
			}
		})
	}
}

func TestRejectUnsupportedAndDamagedInput(t *testing.T) {
	var gifData bytes.Buffer
	if err := gif.Encode(&gifData, image.NewGray(image.Rect(0, 0, 2, 2)), nil); err != nil {
		t.Fatal(err)
	}
	jpegData := jpegFixture(t, 80)
	pngData := pngFixture(t, image.NewGray(image.Rect(0, 0, 2, 2)), png.BestCompression)
	for name, data := range map[string][]byte{
		"empty": nil, "text": []byte("not an image"), "gif": gifData.Bytes(),
		"broken jpeg": jpegData[:len(jpegData)/2],
		"broken png":  pngData[:len(pngData)-15],
		"apng":        insertPNGChunk(pngData, "acTL", make([]byte, 8)),
	} {
		t.Run(name, func(t *testing.T) {
			path := writeFixture(t, "input", data)
			for _, method := range inputMethods(data) {
				t.Run(method.name, func(t *testing.T) {
					result, err := method.run(context.Background(), path, Options{Quality: 80})
					if err == nil || result != nil {
						t.Fatal("expected rejected input")
					}
				})
			}
		})
	}
	for _, method := range inputMethods(jpegData) {
		for _, path := range []string{"", "invalid\x00path"} {
			if _, err := method.run(context.Background(), path, Options{Quality: 80}); err == nil {
				t.Fatalf("%s accepted invalid path %q", method.name, path)
			}
		}
	}
	if _, err := File(context.Background(), filepath.Join(t.TempDir(), "missing"), Options{Quality: 80}); err == nil {
		t.Fatal("missing file was accepted")
	}
}

func TestQualityBoundaries(t *testing.T) {
	data := jpegFixture(t, 80)
	path := writeFixture(t, "input", data)
	for _, method := range inputMethods(data) {
		t.Run(method.name, func(t *testing.T) {
			for _, quality := range []int{-1, 0, 1, 100, 101} {
				_, err := method.run(context.Background(), path, Options{Quality: quality})
				valid := quality >= 1 && quality <= 100
				if (err == nil) != valid {
					t.Fatalf("quality %d: %v", quality, err)
				}
			}
		})
	}
}

func TestCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, method := range inputMethods(nil) {
		result, err := method.run(ctx, "does-not-need-to-exist", Options{Quality: 80})
		if result != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("%s: expected cancellation, got %v", method.name, err)
		}
	}
	reader := contextio.NewReader(ctx, strings.NewReader("data"))
	if _, err := reader.Read(make([]byte, 4)); !errors.Is(err, context.Canceled) {
		t.Fatalf("read cancellation: %v", err)
	}
}

func TestRejectOversizedHeadersBeforeDecode(t *testing.T) {
	original := pngFixture(t, image.NewGray(image.Rect(0, 0, 1, 1)), png.BestCompression)
	for name, dims := range map[string][2]uint32{
		"pixel limit": {8001, 4000},
		"huge":        {0x7fffffff, 0x7fffffff},
	} {
		t.Run(name, func(t *testing.T) {
			// Only retain IHDR: rejection must occur before decoding pixels.
			data := append([]byte(nil), original[:33]...)
			binary.BigEndian.PutUint32(data[16:20], dims[0])
			binary.BigEndian.PutUint32(data[20:24], dims[1])
			binary.BigEndian.PutUint32(data[29:33], crc32.ChecksumIEEE(data[12:29]))
			path := writeFixture(t, "header.png", data)
			for _, method := range inputMethods(data) {
				t.Run(method.name, func(t *testing.T) {
					_, err := method.run(context.Background(), path, Options{Quality: 80})
					if err == nil || (!strings.Contains(err.Error(), "像素") &&
						!strings.Contains(err.Error(), "dimension overflow")) {
						t.Fatalf("expected pixel limit rejection, got %v", err)
					}
				})
			}
		})
	}
}

func TestBytesRejectOversizedData(t *testing.T) {
	data := make([]byte, maxInputSize+1)
	if _, err := Bytes(context.Background(), data, "snapshot.jpg", Options{Quality: 80}); err == nil ||
		!strings.Contains(err.Error(), "64 MiB") {
		t.Fatalf("expected byte limit rejection, got %v", err)
	}
}

func TestFileRejectNonRegularInput(t *testing.T) {
	// Directory coverage is portable and exercises the pre-open IsRegular
	// rejection. The post-open f.Stat check handles observable replacements;
	// this test does not inject a replacement race or create a platform FIFO.
	result, err := File(context.Background(), t.TempDir(), Options{Quality: 80})
	if result != nil || err == nil || !strings.Contains(err.Error(), "普通文件") {
		t.Fatalf("expected non-regular input rejection, got %v", err)
	}
}

func TestBytesUsesSnapshotAfterSourceReplacement(t *testing.T) {
	snapshot := jpegFixture(t, 100)
	source := writeFixture(t, "source.jpg", snapshot)
	expected, err := File(context.Background(), source, Options{Quality: 35})
	if err != nil {
		t.Fatal(err)
	}
	replacement := pngFixture(t, image.NewGray(image.Rect(0, 0, 2, 3)), png.BestCompression)
	if err := os.WriteFile(source, replacement, 0o600); err != nil {
		t.Fatal(err)
	}
	// Use a relative source path to verify that Bytes captures an absolute
	// identity for Save while retaining the caller's SourcePath value.
	t.Chdir(filepath.Dir(source))
	relative := filepath.Base(source)
	result, err := Bytes(context.Background(), snapshot, relative, Options{Quality: 35})
	if err != nil {
		t.Fatal(err)
	}
	if result.Format != "jpeg" || result.Width != 128 || result.Height != 96 ||
		result.OriginalSize != int64(len(snapshot)) || !bytes.Equal(result.Data, expected.Data) {
		t.Fatal("Bytes did not compress the original snapshot")
	}
	if result.SourcePath != relative || result.sourcePath != source {
		t.Fatalf("source identity: %q / %q", result.SourcePath, result.sourcePath)
	}
	if err := result.Save(source); err == nil {
		t.Fatal("Bytes result allowed overwriting its source")
	}
	current, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(current, replacement) {
		t.Fatalf("replacement was changed: %v", err)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	// A cached snapshot remains usable after the source disappears.
	if _, err := Bytes(context.Background(), snapshot, relative, Options{Quality: 35}); err != nil {
		t.Fatal(err)
	}
	if err := result.Save(source); err == nil {
		t.Fatal("Bytes result accepted its source path after source removal")
	}
}

func TestRejectOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxInputSize + 1); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := File(context.Background(), path, Options{Quality: 80}); err == nil ||
		!strings.Contains(err.Error(), "64 MiB") {
		t.Fatalf("expected file size rejection, got %v", err)
	}
}

func TestSaveRejectsEmptyResultWithoutCreatingFile(t *testing.T) {
	for _, result := range []*Result{nil, {}, {Data: []byte{}}} {
		path := filepath.Join(t.TempDir(), "output.png")
		if err := result.Save(path); err == nil {
			t.Fatal("empty compression result saved successfully")
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("empty output artifact created: %v", err)
		}
	}
}

func TestSaveNeverOverwrites(t *testing.T) {
	data := jpegFixture(t, 100)
	source := writeFixture(t, "source.jpg", data)
	result, err := File(context.Background(), source, Options{Quality: 35})
	if err != nil {
		t.Fatal(err)
	}
	existing := writeFixture(t, "existing", []byte("keep me"))
	alias := filepath.Join(t.TempDir(), "source-link")
	if err := os.Symlink(source, alias); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"", source, filepath.Join(filepath.Dir(source), ".", "source.jpg"), existing, alias} {
		if err := result.Save(path); err == nil {
			t.Fatalf("Save(%q) overwrote a protected path", path)
		}
	}
	for path, want := range map[string][]byte{source: data, existing: []byte("keep me")} {
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("protected file changed: %s, %v", path, err)
		}
	}
	target := filepath.Join(t.TempDir(), "output.jpg")
	if err := result.Save(target); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil || !bytes.Equal(got, result.Data) {
		t.Fatalf("saved content differs: %v", err)
	}
	// Removing the source still must not permit saving back to its path.
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if err := result.Save(source); err == nil {
		t.Fatal("source path was accepted after source removal")
	}
	if err := result.Save(filepath.Join(t.TempDir(), "missing", "out")); err == nil {
		t.Fatal("expected creation error")
	}
}

func pngTestChunk(kind string, payload []byte) []byte {
	chunk := make([]byte, len(payload)+12)
	binary.BigEndian.PutUint32(chunk, uint32(len(payload)))
	copy(chunk[4:8], kind)
	copy(chunk[8:], payload)
	binary.BigEndian.PutUint32(chunk[len(chunk)-4:], crc32.ChecksumIEEE(chunk[4:len(chunk)-4]))
	return chunk
}

func insertPNGChunk(data []byte, kind string, payload []byte) []byte {
	out := append([]byte(nil), data[:33]...)
	out = append(out, pngTestChunk(kind, payload)...)
	return append(out, data[33:]...)
}

func TestPNGChunkScanUsesBoundaries(t *testing.T) {
	data := pngFixture(t, image.NewGray(image.Rect(0, 0, 2, 2)), png.BestCompression)
	withText := insertPNGChunk(data, "tEXt", []byte("note\x00acTL"))
	if animatedPNG(withText) {
		t.Fatal("text payload was mistaken for animation")
	}
	if !animatedPNG(insertPNGChunk(data, "acTL", make([]byte, 8))) {
		t.Fatal("animation control chunk was missed")
	}
	if animatedPNG([]byte("short")) {
		t.Fatal("short input was mistaken for animation")
	}
}

func TestContextReaderPropagatesReadErrors(t *testing.T) {
	reader := contextio.NewReader(context.Background(), strings.NewReader(""))
	if _, err := reader.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}
}
