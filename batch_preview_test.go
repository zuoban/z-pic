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

	"github.com/egoist/mygo/ui"
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

func drainBatchUpdates(t *testing.T, updates <-chan func(), cond func() bool) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for cond() {
		select {
		case fn := <-updates:
			fn()
		case <-deadline.C:
			t.Fatal("timed out waiting for async batch update")
		}
	}
}

func TestBatchPreviewRecompressDoneAndReturnToPreview(t *testing.T) {
	root := t.TempDir()
	outputDir := filepath.Join(root, "out")
	donePath := filepath.Join(root, "done.jpg")
	pendingPath := filepath.Join(root, "pending.jpg")
	failedPath := filepath.Join(root, "failed.jpg")
	overwrittenPath := filepath.Join(root, "overwritten.jpg")
	writeTestJPEG(t, donePath, 160, 120, 100)
	writeTestJPEG(t, pendingPath, 160, 120, 100)
	writeTestJPEG(t, failedPath, 160, 120, 100)
	writeTestJPEG(t, overwrittenPath, 120, 90, 75)
	prevSavedPath := filepath.Join(outputDir, "done.jpg")
	prevSavedData := writeTestJPEG(t, prevSavedPath, 160, 120, 95)

	scan, err := scanBatchPaths(context.Background(), []string{donePath, pendingPath, failedPath, overwrittenPath}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	updates := make(chan func(), 16)
	app := newApplication()
	app.presets = testLegacyPresets()
	app.dispatchUpdate = func(fn func()) { updates <- fn }
	app.setScheme("体积优先")
	app.batch = newBatchState(scan.items)
	app.batch.outputMode = batchOutputFolder
	app.batch.outputDir = outputDir
	app.batch.items[0].status = batchDone
	app.batch.items[0].savedPath = prevSavedPath
	app.batch.items[0].outputSize = int64(len(prevSavedData))
	app.batch.items[2].status = batchFailed
	app.batch.items[2].errMsg = "上次失败"
	app.batch.items[3].status = batchDone
	app.batch.items[3].overwritten = true
	app.batch.items[3].outputSize = app.batch.items[3].originalSize
	t.Cleanup(app.saves.Wait)

	// 1) Open preview on non-overwritten batchDone item: shows "重新压缩此张", recompresses only it, and returns to preview.
	app.openBatchPreview(0)
	drainBatchUpdates(t, updates, func() bool { return app.busy })
	if !app.batch.previewOpen || app.batch.previewIndex != 0 {
		t.Fatalf("expected preview open on index 0, got open=%v idx=%d", app.batch.previewOpen, app.batch.previewIndex)
	}
	tt := ui.NewTester(app.view, 980, 680)
	if _, ok := tt.AccessByRole(ui.RoleButton, "重新压缩此张"); !ok {
		t.Fatal("preview header must show 重新压缩此张 for non-overwritten batchDone item")
	}
	if err := tt.Click("重新压缩此张"); err != nil {
		t.Fatal(err)
	}
	if app.batch.returnPreviewPath != app.batch.items[0].absPath {
		t.Fatalf("expected returnPreviewPath=%q, got %q", app.batch.items[0].absPath, app.batch.returnPreviewPath)
	}
	drainBatchUpdates(t, updates, func() bool { return app.busy })
	tt.Frame()
	if !app.batch.previewOpen || app.batch.previewIndex != 0 || app.result == nil {
		t.Fatalf("expected single recompress from preview to return to updated preview: open=%v idx=%d result=%v",
			app.batch.previewOpen, app.batch.previewIndex, app.result != nil)
	}
	if app.batch.returnPreviewPath != "" {
		t.Fatalf("returnPreviewPath must be cleared after returning to preview, got %q", app.batch.returnPreviewPath)
	}
	if app.batch.items[0].status != batchDone || app.batch.items[0].savedPath == prevSavedPath {
		t.Fatalf("expected recompressed done item to write new output, prev=%q item=%+v", prevSavedPath, app.batch.items[0])
	}
	if app.batch.items[1].status != batchPending || app.batch.items[2].status != batchFailed {
		t.Fatalf("single recompress must leave sibling items untouched: pending=%v failed=%v",
			app.batch.items[1].status, app.batch.items[2].status)
	}

	// 2) Pending item compressed from preview ("压缩此张") also returns to preview.
	app.openBatchPreview(1)
	drainBatchUpdates(t, updates, func() bool { return app.busy })
	tt.Frame()
	if err := tt.Click("压缩此张"); err != nil {
		t.Fatal(err)
	}
	drainBatchUpdates(t, updates, func() bool { return app.busy })
	if !app.batch.previewOpen || app.batch.previewIndex != 1 || app.batch.items[1].status != batchDone || app.result == nil {
		t.Fatalf("single pending compress from preview must return to comparison preview: open=%v idx=%d status=%v result=%v",
			app.batch.previewOpen, app.batch.previewIndex, app.batch.items[1].status, app.result != nil)
	}

	// 3) Failed item retried from preview ("重试") also returns to preview.
	app.openBatchPreview(2)
	drainBatchUpdates(t, updates, func() bool { return app.busy })
	tt.Frame()
	if err := tt.Click("重试"); err != nil {
		t.Fatal(err)
	}
	drainBatchUpdates(t, updates, func() bool { return app.busy })
	if !app.batch.previewOpen || app.batch.previewIndex != 2 || app.batch.items[2].status != batchDone || app.result == nil {
		t.Fatalf("single failed retry from preview must return to comparison preview: open=%v idx=%d status=%v result=%v",
			app.batch.previewOpen, app.batch.previewIndex, app.batch.items[2].status, app.result != nil)
	}

	// 4) Overwritten batchDone item in preview must NOT show "重新压缩此张" and must not be runnable.
	app.openBatchPreview(3)
	drainBatchUpdates(t, updates, func() bool { return app.busy })
	tt.Frame()
	if _, runnable := app.batchPreviewTarget(); runnable {
		t.Fatal("overwritten batchDone item must not be runnable via batchPreviewTarget")
	}
	for _, label := range []string{"重新压缩此张", "压缩此张", "重试"} {
		if _, ok := tt.AccessByRole(ui.RoleButton, label); ok {
			t.Fatalf("overwritten batchDone preview must not show %q", label)
		}
	}
	app.compressBatchItem(3)
	if app.busy || app.batch.returnPreviewPath != "" {
		t.Fatalf("compressBatchItem on overwritten item must be a no-op: busy=%v returnPath=%q", app.busy, app.batch.returnPreviewPath)
	}

	// 5) Canceled single-item run clears returnPreviewPath and does not reopen preview.
	app.closeBatchPreview()
	app.batch.returnPreviewPath = app.batch.items[0].absPath
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	app.completeBatchRun(canceledCtx)
	if app.batch.previewOpen || app.busy || app.batch.returnPreviewPath != "" {
		t.Fatalf("canceled batch run must clear returnPreviewPath and stay out of preview: open=%v busy=%v returnPath=%q",
			app.batch.previewOpen, app.busy, app.batch.returnPreviewPath)
	}

	// 6) Non-single batch run clears returnPreviewPath and does not reopen preview.
	app.batch.returnPreviewPath = app.batch.items[0].absPath
	app.restartBatchCompression()
	if app.batch.returnPreviewPath != "" {
		t.Fatalf("non-single batch run must clear returnPreviewPath immediately, got %q", app.batch.returnPreviewPath)
	}
	drainBatchUpdates(t, updates, func() bool { return app.busy })
	if app.batch.previewOpen {
		t.Fatal("non-single batch run must not reopen preview on completion")
	}
}

func TestBatchPreviewRemoveAdvancesToAdjacentVisibleItem(t *testing.T) {
	root := t.TempDir()
	aPath := filepath.Join(root, "a.jpg")
	bPath := filepath.Join(root, "b.jpg")
	cPath := filepath.Join(root, "c.jpg")
	badPath := filepath.Join(root, "bad.png")
	dPath := filepath.Join(root, "d.jpg")
	writeTestJPEG(t, aPath, 80, 60, 90)
	writeTestJPEG(t, bPath, 80, 60, 90)
	writeTestJPEG(t, cPath, 80, 60, 90)
	if err := os.WriteFile(badPath, []byte("not-a-png"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTestJPEG(t, dPath, 80, 60, 90)

	scan, err := scanBatchPaths(context.Background(), []string{aPath, bPath, cPath, badPath, dPath}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	updates := make(chan func(), 16)
	app := newApplication()
	app.dispatchUpdate = func(fn func()) { updates <- fn }
	app.batch = newBatchState(scan.items)

	// 1) Open preview on b.jpg (index 1); removing it advances to next visible previewable item c.jpg.
	app.openBatchPreview(1)
	drainBatchUpdates(t, updates, func() bool { return app.busy })
	if !app.batch.previewOpen || app.batch.items[app.batch.previewIndex].relPath != "b.jpg" {
		t.Fatalf("expected preview on b.jpg, got open=%v idx=%d", app.batch.previewOpen, app.batch.previewIndex)
	}
	app.removeBatchItem(app.batch.previewIndex)
	drainBatchUpdates(t, updates, func() bool { return app.busy })
	if !app.batch.previewOpen || app.batch.items[app.batch.previewIndex].relPath != "c.jpg" {
		t.Fatalf("removing b.jpg inside preview must advance to c.jpg: open=%v idx=%d items=%+v",
			app.batch.previewOpen, app.batch.previewIndex, app.batch.items)
	}

	// 2) Removing c.jpg skips invalid bad.png and advances to d.jpg.
	app.removeBatchItem(app.batch.previewIndex)
	drainBatchUpdates(t, updates, func() bool { return app.busy })
	if !app.batch.previewOpen || app.batch.items[app.batch.previewIndex].relPath != "d.jpg" {
		t.Fatalf("removing c.jpg inside preview must skip invalid bad.png and advance to d.jpg: open=%v idx=%d items=%+v",
			app.batch.previewOpen, app.batch.previewIndex, app.batch.items)
	}

	// 3) Removing last visible previewable item d.jpg falls back to nearest previous previewable item a.jpg.
	app.removeBatchItem(app.batch.previewIndex)
	drainBatchUpdates(t, updates, func() bool { return app.busy })
	if !app.batch.previewOpen || app.batch.items[app.batch.previewIndex].relPath != "a.jpg" {
		t.Fatalf("removing last previewable item d.jpg must fall back to a.jpg: open=%v idx=%d items=%+v",
			app.batch.previewOpen, app.batch.previewIndex, app.batch.items)
	}

	// 4) Removing a.jpg leaves only invalid bad.png -> closes preview cleanly.
	app.removeBatchItem(app.batch.previewIndex)
	if app.busy || app.batch.previewOpen || len(app.batch.items) != 1 || !app.batch.items[0].invalidInput {
		t.Fatalf("removing last previewable item when only invalid items remain must close preview: busy=%v open=%v items=%+v",
			app.busy, app.batch.previewOpen, app.batch.items)
	}

	// 5) Filtered + sorted view: removing inside preview respects filteredBatchIndices() order.
	app.batch = newBatchState([]batchItem{
		{path: cPath, absPath: cPath, relPath: "c.jpg", format: "jpeg", status: batchPending},
		{path: aPath, absPath: aPath, relPath: "a.jpg", format: "jpeg", status: batchDone, savedPath: aPath},
		{path: bPath, absPath: bPath, relPath: "b.jpg", format: "jpeg", status: batchPending},
	})
	app.batch.filter = batchFilterPending
	app.batch.sort = batchSortName // visible order: [b.jpg (2), c.jpg (0)]
	app.openBatchPreview(2)
	drainBatchUpdates(t, updates, func() bool { return app.busy })
	app.removeBatchItem(app.batch.previewIndex)
	drainBatchUpdates(t, updates, func() bool { return app.busy })
	if !app.batch.previewOpen || app.batch.items[app.batch.previewIndex].relPath != "c.jpg" {
		t.Fatalf("removing inside filtered+sorted preview must advance to c.jpg: open=%v idx=%d items=%+v",
			app.batch.previewOpen, app.batch.previewIndex, app.batch.items)
	}
	// Removing c.jpg leaves no pending items in the filtered view -> closes preview even though a.jpg exists.
	app.removeBatchItem(app.batch.previewIndex)
	if app.busy || app.batch.previewOpen || len(app.batch.items) != 1 || app.batch.items[0].relPath != "a.jpg" {
		t.Fatalf("removing last item in filtered view must close preview: busy=%v open=%v items=%+v",
			app.busy, app.batch.previewOpen, app.batch.items)
	}
}
