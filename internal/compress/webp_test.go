package compress

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func webpFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name+".webp"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func webpChunk(kind string, payload []byte) []byte {
	chunk := make([]byte, 8+len(payload)+len(payload)%2)
	copy(chunk, kind)
	binary.LittleEndian.PutUint32(chunk[4:], uint32(len(payload)))
	copy(chunk[8:], payload)
	return chunk
}

func appendWebPChunk(data []byte, kind string, payload []byte) []byte {
	result := append(bytes.Clone(data), webpChunk(kind, payload)...)
	binary.LittleEndian.PutUint32(result[4:], uint32(len(result)-8))
	return result
}

func extendedWebP(data []byte, flags byte, width, height int) []byte {
	header := make([]byte, 10)
	header[0] = flags
	for index := range 3 {
		header[4+index] = byte((width - 1) >> (8 * index))
		header[7+index] = byte((height - 1) >> (8 * index))
	}
	payload := data[12:]
	if string(data[12:16]) == "VP8X" {
		payload = data[30:]
	}
	result := append(bytes.Clone(data[:12]), webpChunk("VP8X", header)...)
	result = append(result, payload...)
	binary.LittleEndian.PutUint32(result[4:], uint32(len(result)-8))
	return result
}

func TestValidateStaticWebPContainer(t *testing.T) {
	data := webpFixture(t, "opaque")
	truncated := bytes.Clone(data[:len(data)-1])
	binary.LittleEndian.PutUint32(truncated[4:], uint32(len(truncated)-8))
	overlong := bytes.Clone(data)
	binary.LittleEndian.PutUint32(overlong[16:], uint32(len(overlong)))
	wrongLength := bytes.Clone(data)
	binary.LittleEndian.PutUint32(wrongLength[4:], 0)
	extraImage := append(bytes.Clone(data), data[12:]...)
	binary.LittleEndian.PutUint32(extraImage[4:], uint32(len(extraImage)-8))
	for _, test := range []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"truncated RIFF", data[:8]},
		{"wrong RIFF size", wrongLength},
		{"truncated payload", truncated},
		{"oversized chunk", overlong},
		{"extra image", extraImage},
		{"animation flag", extendedWebP(data, 2, 256, 192)},
		{"animation header", appendWebPChunk(data, "ANIM", make([]byte, 6))},
		{"animation frame", appendWebPChunk(data, "ANMF", make([]byte, 16))},
		{"trailing partial chunk", appendWebPChunk(data, "XMP ", []byte("a"))[:len(data)+9]},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateWebP(context.Background(), test.data); err == nil {
				t.Fatal("invalid or animated WebP accepted")
			}
		})
	}
	for _, name := range []string{"opaque", "alpha", "tiny"} {
		if err := ValidateWebP(context.Background(), webpFixture(t, name)); err != nil {
			t.Fatalf("valid %s WebP rejected: %v", name, err)
		}
	}
	if err := ValidateWebP(context.Background(), appendWebPChunk(data, "XMP ", []byte("ANIM ANMF"))); err != nil {
		t.Fatalf("chunk names inside metadata must not be treated as animation: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ValidateWebP(ctx, data); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation must be preserved: %v", err)
	}
}

func TestWebPInspectionAndThumbnail(t *testing.T) {
	for _, name := range []string{"opaque", "alpha", "tiny"} {
		t.Run(name, func(t *testing.T) {
			data := webpFixture(t, name)
			path := writeFixture(t, "image.WEBP", data)
			expected, _, err := image.DecodeConfig(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			info, err := Inspect(context.Background(), path)
			if err != nil || info.Format != "webp" || info.Width != expected.Width ||
				info.Height != expected.Height || info.Size != int64(len(data)) {
				t.Fatalf("incorrect WebP inspection: %+v %v", info, err)
			}
			thumbnail, err := Thumbnail(context.Background(), path, 64)
			if err != nil || thumbnail.Bounds().Dx() > 64 || thumbnail.Bounds().Dy() > 64 {
				t.Fatalf("invalid WebP thumbnail: %v", err)
			}
			if name == "alpha" {
				_, _, _, alpha := thumbnail.At(0, 0).RGBA()
				if alpha == 65535 {
					t.Fatal("transparent thumbnail was flattened")
				}
			}
		})
	}
}

func TestWebPRejectsAnimationDamageAndOversizedCanvas(t *testing.T) {
	data := webpFixture(t, "opaque")
	for _, test := range []struct {
		name string
		data []byte
	}{
		{"animated", extendedWebP(data, 2, 256, 192)},
		{"late animation", appendWebPChunk(data, "ANMF", make([]byte, 16))},
		{"oversized", extendedWebP(data, 0, 8000, 8000)},
		{"damaged", data[:30]},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := writeFixture(t, "invalid.webp", test.data)
			if _, err := Inspect(context.Background(), path); err == nil {
				t.Fatal("invalid WebP accepted by header scan")
			}
			if _, err := Thumbnail(context.Background(), path, 64); err == nil {
				t.Fatal("invalid WebP accepted by thumbnail decoder")
			}
			for _, method := range inputMethods(test.data) {
				if _, err := method.run(context.Background(), path, Options{Quality: 75}); err == nil {
					t.Fatalf("invalid WebP accepted by %s", method.name)
				}
			}
			actual, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(actual, test.data) {
				t.Fatal("rejecting WebP must preserve the source")
			}
		})
	}
}

func requireWebPEncoder(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("native WebP bundle currently ships for darwin/arm64")
	}
}

func TestWebPCompressionPreservesFormatDimensionsAndAlpha(t *testing.T) {
	requireWebPEncoder(t)
	for _, name := range []string{"opaque", "alpha"} {
		data := webpFixture(t, name)
		for _, method := range inputMethods(data) {
			t.Run(name+"/"+method.name, func(t *testing.T) {
				path := writeFixture(t, "actually-webp.jpg", data)
				result, err := method.run(context.Background(), path, Options{Quality: 35})
				if err != nil {
					t.Fatal(err)
				}
				input, _, err := image.Decode(bytes.NewReader(data))
				if err != nil {
					t.Fatal(err)
				}
				if result.Format != "webp" || result.Width != input.Bounds().Dx() ||
					result.Height != input.Bounds().Dy() || result.Unchanged ||
					result.OutputSize >= int64(len(data)) || result.OutputSize != int64(len(result.Data)) {
					t.Fatalf("incorrect WebP result: %+v", result)
				}
				output, format, err := image.Decode(bytes.NewReader(result.Data))
				if err != nil || format != "webp" || output.Bounds() != input.Bounds() {
					t.Fatalf("invalid output: %s %v", format, err)
				}
				for row := 0; row < input.Bounds().Dy(); row++ {
					for column := 0; column < input.Bounds().Dx(); column++ {
						_, _, _, before := input.At(column, row).RGBA()
						_, _, _, after := output.At(column, row).RGBA()
						if before != after {
							t.Fatalf("alpha changed at %d,%d: %d -> %d", column, row, before, after)
						}
					}
				}
				if err := result.Save(path); err == nil {
					t.Fatal("save-copy must reject the original WebP path")
				}
				saved := path + ".webp"
				if err := result.Save(saved); err != nil {
					t.Fatal(err)
				}
				if err := result.Save(saved); err == nil {
					t.Fatal("save-copy must reject an existing WebP output")
				}
				original, _ := os.ReadFile(path)
				if !bytes.Equal(original, data) {
					t.Fatal("WebP compression changed the source")
				}
				if err := result.Overwrite(); err != nil {
					t.Fatal(err)
				}
				overwritten, _ := os.ReadFile(path)
				if !bytes.Equal(overwritten, result.Data) {
					t.Fatal("WebP overwrite did not publish the validated result")
				}
			})
		}
	}
}

func TestWebPQualityAndUnchangedFallback(t *testing.T) {
	requireWebPEncoder(t)
	path := writeFixture(t, "photo.webp", webpFixture(t, "opaque"))
	small, err := File(context.Background(), path, Options{Quality: 20})
	if err != nil {
		t.Fatal(err)
	}
	large, err := File(context.Background(), path, Options{Quality: 90})
	if err != nil {
		t.Fatal(err)
	}
	if small.OutputSize >= large.OutputSize {
		t.Fatal("WebP must use the selected scheme quality")
	}
	tiny := webpFixture(t, "tiny")
	result, err := File(context.Background(), writeFixture(t, "tiny.webp", tiny), Options{Quality: 80})
	if err != nil || !result.Unchanged || !bytes.Equal(result.Data, tiny) ||
		result.OutputSize != int64(len(tiny)) {
		t.Fatalf("larger WebP output must retain the original: %+v %v", result, err)
	}
}

func TestWebPOutputValidation(t *testing.T) {
	data := webpFixture(t, "opaque")
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateWebPOutput(context.Background(), data, config); err != nil {
		t.Fatal(err)
	}
	config.Width++
	if err := validateWebPOutput(context.Background(), data, config); err == nil {
		t.Fatal("WebP dimension changes must be rejected")
	}
	animated := extendedWebP(data, 2, 256, 192)
	if err := validateWebPOutput(context.Background(), animated, config); err == nil || !strings.Contains(err.Error(), "动画") {
		t.Fatal("animated WebP output must be rejected")
	}
}

func TestWebPOutputPreviewRejectsDamageAndCancellation(t *testing.T) {
	data := webpFixture(t, "opaque")
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	corrupt := appendWebPChunk(data[:12], "VP8 ", data[20:40])
	if _, _, err := image.DecodeConfig(bytes.NewReader(corrupt)); err != nil {
		t.Fatalf("corruption fixture must retain a readable image header: %v", err)
	}
	for name, output := range map[string][]byte{
		"empty":          nil,
		"truncated":      data[:len(data)/2],
		"animated":       extendedWebP(data, 2, config.Width, config.Height),
		"dimensions":     webpFixture(t, "tiny"),
		"corrupt pixels": corrupt,
	} {
		t.Run(name, func(t *testing.T) {
			if preview, err := decodeWebPOutput(context.Background(), output, config); err == nil || preview != nil {
				t.Fatal("invalid WebP output returned a preview")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if preview, err := decodeWebPOutput(ctx, data, config); preview != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled WebP validation returned preview=%T err=%v", preview, err)
	}
}
