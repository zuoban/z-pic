package main

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/egoist/mygo/ui"

	"zpic/internal/compress"
)

func TestSingleCompressionPreviewMatchesOutput(t *testing.T) {
	for _, format := range []string{"jpeg", "png", "webp"} {
		t.Run(format, func(t *testing.T) {
			if format != "jpeg" && (runtime.GOOS != "darwin" || runtime.GOARCH != "arm64") {
				t.Skip("native encoders currently ship for darwin/arm64")
			}
			source := image.NewNRGBA(image.Rect(0, 0, 128, 96))
			for row := 0; row < 96; row++ {
				for column := 0; column < 128; column++ {
					source.SetNRGBA(column, row, color.NRGBA{
						R: uint8(column * 37), G: uint8(row * 43),
						B: uint8(column*71 + row*7), A: 255,
					})
				}
			}
			var encoded bytes.Buffer
			var data []byte
			var err error
			switch format {
			case "jpeg":
				err = jpeg.Encode(&encoded, source, &jpeg.Options{Quality: 100})
				data = encoded.Bytes()
			case "png":
				encoder := png.Encoder{CompressionLevel: png.NoCompression}
				err = encoder.Encode(&encoded, source)
				data = encoded.Bytes()
			case "webp":
				data, err = os.ReadFile(filepath.Join("internal", "compress", "testdata", "alpha.webp"))
			}
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "image."+format)
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			selected, err := readSelection(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			app := newApplication()
			updates := make(chan func(), 1)
			app.dispatchUpdate = func(update func()) { updates <- update }
			app.file, app.preview, app.quality = selected, selected.preview, 20
			app.startCompression()
			awaitBatchUpdate(t, updates)()
			if app.failed || app.result == nil || app.result.Unchanged || app.preview == nil ||
				app.preview == selected.preview || app.busy {
				t.Fatalf("compression did not publish a changed preview: %q result=%+v", app.message, app.result)
			}
			actual := ui.NewTester(app.view, 880, 660).Image()
			expected, err := ui.DecodeBitmap(app.result.Data)
			if err != nil {
				t.Fatal(err)
			}
			app.preview = expected
			want := ui.NewTester(app.view, 880, 660).Image()
			if !bytes.Equal(actual.Pix, want.Pix) {
				t.Fatal("rendered preview differs from decoding the actual output")
			}
			if !bytes.Equal(selected.data, data) {
				t.Fatal("compression changed the selected input snapshot")
			}
		})
	}
}

func TestUnchangedCompressionRebuildsMissingSourcePreview(t *testing.T) {
	img := image.NewGray(image.Rect(0, 0, 16, 24))
	for y := range 24 {
		for x := range 16 {
			img.SetGray(x, y, color.Gray{Y: uint8((x*23 + y*31) % 256)})
		}
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, img, &jpeg.Options{Quality: 10}); err != nil {
		t.Fatal(err)
	}
	minimal, err := compress.Bytes(context.Background(), encoded.Bytes(), "tiny.jpg", compress.Options{Quality: 10})
	if err != nil {
		t.Fatal(err)
	}
	app := newApplication()
	app.file = &selectedImage{path: "tiny.jpg", data: minimal.Data, width: 16, height: 24}
	app.quality = 100
	updates := make(chan func(), 1)
	app.dispatchUpdate = func(update func()) { updates <- update }
	app.startCompression()
	awaitBatchUpdate(t, updates)()
	if app.failed || app.result == nil || !app.result.Unchanged || app.preview == nil {
		t.Fatalf("unchanged result without a source bitmap needs a decoded fallback: %q", app.message)
	}
	if width, height := app.preview.Size(); width != 16 || height != 24 {
		t.Fatalf("fallback preview dimensions: %dx%d", width, height)
	}
}
