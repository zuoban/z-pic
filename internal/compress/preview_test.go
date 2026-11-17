package compress

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/png"
	"runtime"
	"strconv"
	"testing"
)

func assertPreviewMatchesOutput(t *testing.T, result *Result, preview image.Image) {
	t.Helper()
	if result == nil || result.Unchanged || preview == nil {
		t.Fatalf("expected changed output and decoded preview: result=%+v preview=%T", result, preview)
	}
	expected, _, err := image.Decode(bytes.NewReader(result.Data))
	if err != nil {
		t.Fatal(err)
	}
	if preview.Bounds() != expected.Bounds() ||
		preview.Bounds().Dx() != result.Width || preview.Bounds().Dy() != result.Height {
		t.Fatalf("preview dimensions %v differ from output %v or result %dx%d",
			preview.Bounds(), expected.Bounds(), result.Width, result.Height)
	}
	for row := preview.Bounds().Min.Y; row < preview.Bounds().Max.Y; row++ {
		for column := preview.Bounds().Min.X; column < preview.Bounds().Max.X; column++ {
			actualPixel := color.NRGBA64Model.Convert(preview.At(column, row))
			expectedPixel := color.NRGBA64Model.Convert(expected.At(column, row))
			if actualPixel != expectedPixel {
				t.Fatalf("preview differs from output at (%d, %d): %v != %v",
					column, row, actualPixel, expectedPixel)
			}
		}
	}
}

func TestBytesWithPreviewJPEGOrientation(t *testing.T) {
	for orientation := uint16(1); orientation <= 8; orientation++ {
		t.Run(strconv.Itoa(int(orientation)), func(t *testing.T) {
			data := withEXIF(jpegFixture(t, 100), append(exifTIFF(binary.LittleEndian, orientation), make([]byte, 8192)...))
			original := bytes.Clone(data)
			result, preview, err := BytesWithPreview(context.Background(), data, "photo.jpg", Options{Quality: 35})
			if err != nil {
				t.Fatal(err)
			}
			assertPreviewMatchesOutput(t, result, preview)
			width, height := 128, 96
			if orientation >= 5 {
				width, height = height, width
			}
			if result.Width != width || result.Height != height || jpegOrientation(result.Data) != 1 {
				t.Fatal("changed JPEG preview must use normalized output orientation")
			}
			if !bytes.Equal(data, original) {
				t.Fatal("preview generation mutated the input snapshot")
			}
		})
	}
}

func TestBytesWithPreviewPNGTransparency(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("native PNG bundle currently ships for darwin/arm64")
	}
	for _, depth := range []int{8, 16} {
		t.Run(strconv.Itoa(depth), func(t *testing.T) {
			source := image.NewNRGBA64(image.Rect(0, 0, 32, 24))
			for row := 0; row < 24; row++ {
				for column := 0; column < 32; column++ {
					source.SetNRGBA64(column, row, color.NRGBA64{
						R: 0x1212, G: 0xabab, B: 0x5656, A: uint16(column%4) * 0x5555,
					})
				}
			}
			var input image.Image = source
			if depth == 8 {
				input8 := image.NewNRGBA(source.Bounds())
				for row := 0; row < 24; row++ {
					for column := 0; column < 32; column++ {
						input8.Set(column, row, source.At(column, row))
					}
				}
				input = input8
			}
			data := pngFixture(t, input, png.NoCompression)
			original := bytes.Clone(data)
			result, preview, err := BytesWithPreview(context.Background(), data, "alpha.png", Options{Quality: 80})
			if err != nil {
				t.Fatal(err)
			}
			assertPreviewMatchesOutput(t, result, preview)
			assertPNGTransparency(t, result.Data)
			if !bytes.Equal(data, original) {
				t.Fatal("preview generation mutated the PNG snapshot")
			}
		})
	}
}

func TestBytesWithPreviewWebP(t *testing.T) {
	requireWebPEncoder(t)
	for _, name := range []string{"opaque", "alpha"} {
		t.Run(name, func(t *testing.T) {
			data := webpFixture(t, name)
			original := bytes.Clone(data)
			result, preview, err := BytesWithPreview(context.Background(), data, "image.webp", Options{Quality: 20})
			if err != nil {
				t.Fatal(err)
			}
			assertPreviewMatchesOutput(t, result, preview)
			if !bytes.Equal(data, original) {
				t.Fatal("preview generation mutated the WebP snapshot")
			}
		})
	}
}

func TestBytesWithPreviewUnchanged(t *testing.T) {
	fixtures := map[string][]byte{
		"jpeg": withEXIF(jpegFixture(t, 1), exifTIFF(binary.BigEndian, 6)),
	}
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		fixtures["webp"] = webpFixture(t, "tiny")
		source := image.NewNRGBA(image.Rect(0, 0, 1, 1))
		source.SetNRGBA(0, 0, color.NRGBA{R: 17, G: 29, B: 71, A: 128})
		result, err := Bytes(context.Background(), pngFixture(t, source, png.BestCompression), "tiny.png", Options{Quality: 80})
		if err != nil {
			t.Fatal(err)
		}
		fixtures["png"] = result.Data
	}
	for format, data := range fixtures {
		t.Run(format, func(t *testing.T) {
			result, preview, err := BytesWithPreview(context.Background(), data, "tiny."+format, Options{Quality: 100})
			if err != nil {
				t.Fatal(err)
			}
			if !result.Unchanged || preview != nil || !sameBytesBacking(result.Data, data) {
				t.Fatal("unchanged results must preserve bytes and let callers reuse the source preview")
			}
		})
	}
}

func TestBytesWithPreviewRejectsInvalidAndCanceledRequests(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, expire := context.WithTimeout(context.Background(), 0)
	defer expire()
	valid := jpegFixture(t, 100)
	for _, scenario := range []struct {
		name    string
		ctx     context.Context
		data    []byte
		path    string
		options Options
		cause   error
	}{
		{"canceled", canceled, valid, "photo.jpg", Options{Quality: 80}, context.Canceled},
		{"expired", expired, valid, "photo.jpg", Options{Quality: 80}, context.DeadlineExceeded},
		{"quality", context.Background(), valid, "photo.jpg", Options{}, nil},
		{"path", context.Background(), valid, "", Options{Quality: 80}, nil},
		{"empty", context.Background(), nil, "photo.jpg", Options{Quality: 80}, nil},
		{"damaged", context.Background(), valid[:len(valid)/2], "photo.jpg", Options{Quality: 80}, nil},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			result, preview, err := BytesWithPreview(scenario.ctx, scenario.data, scenario.path, scenario.options)
			if result != nil || preview != nil || err == nil {
				t.Fatalf("invalid request returned result=%+v preview=%T err=%v", result, preview, err)
			}
			if scenario.cause != nil && !errors.Is(err, scenario.cause) {
				t.Fatalf("cancellation identity lost: %v", err)
			}
		})
	}
}
