package main

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBatchPreviewReadsActualOutput(t *testing.T) {
	var original, changed bytes.Buffer
	for _, fixture := range []struct {
		buffer *bytes.Buffer
		color  color.RGBA
	}{
		{&original, color.RGBA{R: 255, A: 255}},
		{&changed, color.RGBA{B: 255, A: 255}},
	} {
		pixels := image.NewRGBA(image.Rect(0, 0, 16, 16))
		for row := 0; row < 16; row++ {
			for column := 0; column < 16; column++ {
				pixels.SetRGBA(column, row, fixture.color)
			}
		}
		if err := jpeg.Encode(fixture.buffer, pixels, &jpeg.Options{Quality: 50}); err != nil {
			t.Fatal(err)
		}
	}
	size := max(original.Len(), changed.Len()) + 16
	sourceBytes := append(original.Bytes(), make([]byte, size-original.Len())...)
	changedBytes := append(changed.Bytes(), make([]byte, size-changed.Len())...)
	for _, scenario := range []string{"same", "source-changed", "output-changed", "corrupt-output", "missing-output"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			sourcePath, savedPath := filepath.Join(root, "source.jpg"), filepath.Join(root, "saved.jpg")
			source, output := sourceBytes, sourceBytes
			if scenario == "source-changed" {
				source = changedBytes
			} else if scenario == "output-changed" {
				output = changedBytes
			} else if scenario == "corrupt-output" {
				output = make([]byte, size)
			}
			if err := os.WriteFile(sourcePath, source, 0o600); err != nil {
				t.Fatal(err)
			}
			if scenario != "missing-output" {
				if err := os.WriteFile(savedPath, output, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			app := newApplication()
			app.batch = newBatchState([]batchItem{{
				path: sourcePath, savedPath: savedPath, format: "jpeg", status: batchDone,
				originalSize: int64(size), outputSize: int64(size), width: 16, height: 16, unchanged: true,
			}})
			updates := make(chan func(), 1)
			app.dispatchUpdate = func(update func()) { updates <- update }
			app.openBatchPreview(0)
			awaitBatchUpdate(t, updates)()
			if scenario == "corrupt-output" || scenario == "missing-output" {
				if !app.failed || app.batch.previewOpen || app.result != nil {
					t.Fatal("invalid output must report a preview error")
				}
				return
			}
			if app.result == nil || !bytes.Equal(app.result.Data, output) {
				t.Fatal("preview must retain the actual saved output")
			}
			if (app.preview == app.file.preview) != (scenario == "same") {
				t.Fatal("source bitmap reuse requires identical file contents")
			}
			var copied []byte
			app.writeClipboardImage = func(data []byte) error { copied = data; return nil }
			app.copyResult()
			awaitBatchUpdate(t, updates)()
			actual, _, err := image.Decode(bytes.NewReader(copied))
			if err != nil {
				t.Fatal(err)
			}
			expected, _, err := image.Decode(bytes.NewReader(output))
			if err != nil {
				t.Fatal(err)
			}
			if color.NRGBAModel.Convert(actual.At(0, 0)) != color.NRGBAModel.Convert(expected.At(0, 0)) {
				t.Fatal("clipboard must contain the saved output pixels")
			}
		})
	}
}

func awaitBatchUpdate(t *testing.T, updates <-chan func()) func() {
	t.Helper()
	select {
	case update := <-updates:
		return update
	case <-time.After(5 * time.Second):
		t.Fatal("background work did not deliver an update")
		return nil
	}
}

func TestSelectionReuseHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readSelectionWithPreview(ctx, "unused", &selectedImage{}); err != context.Canceled {
		t.Fatalf("canceled selection returned %v", err)
	}
}

func TestBatchPreviewUnchangedInPlacePopulatesDiffData(t *testing.T) {
	pixels := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			pixels.SetRGBA(x, y, color.RGBA{R: uint8(x * 16), G: uint8(y * 16), B: 120, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, pixels, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(t.TempDir(), "unchanged.jpg")
	if err := os.WriteFile(sourcePath, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	app := newApplication()
	app.batch = newBatchState([]batchItem{{
		path: sourcePath, format: "jpeg", status: batchDone,
		originalSize: int64(buf.Len()), outputSize: int64(buf.Len()),
		width: 16, height: 16, unchanged: true,
	}})
	updates := make(chan func(), 4)
	app.dispatchUpdate = func(update func()) { updates <- update }
	app.openBatchPreview(0)
	awaitBatchUpdate(t, updates)()

	if app.result == nil || !bytes.Equal(app.result.Data, buf.Bytes()) {
		t.Fatalf("unchanged in-place batch preview must populate result.Data for diff heatmap")
	}
	app.comparison.mode = previewDiff
	app.ensureComparisonDiff()
	awaitBatchUpdate(t, updates)()
	if !app.comparison.diffReady || app.comparison.diffBitmap == nil || !app.comparison.diffMetrics.Identical {
		t.Fatalf("expected identical diff heatmap for unchanged in-place batch preview: ready=%v bitmap=%v metrics=%+v err=%q",
			app.comparison.diffReady, app.comparison.diffBitmap != nil, app.comparison.diffMetrics, app.comparison.diffError)
	}
}
