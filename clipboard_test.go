package main

import (
	"bytes"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"zpic/internal/compress"
)

func TestCopyResultLifecycle(t *testing.T) {
	for _, scenario := range []string{"success", "write failure", "invalid image", "cancel", "closed", "batch preview"} {
		t.Run(scenario, func(t *testing.T) {
			app := testSelectedApplication()
			data := webPAppFixture(t, "alpha.webp")
			app.result = &compress.Result{Data: data, Format: "webp", OriginalSize: 4096, OutputSize: int64(len(data))}
			result, preview, source := app.result, app.preview, app.file
			app.lastSavedPath = "/photos/saved.webp"
			if scenario == "invalid image" {
				app.result.Data = []byte("broken")
			}
			if scenario == "batch preview" {
				app.batch = newBatchState([]batchItem{{path: source.path, status: batchDone}})
				app.batch.previewOpen = true
			}
			updates := make(chan func(), 1)
			app.dispatchUpdate = func(update func()) { updates <- update }
			writes := 0
			app.writeClipboardImage = func(pngData []byte) error {
				writes++
				if _, err := png.Decode(bytes.NewReader(pngData)); err != nil {
					t.Fatalf("clipboard must receive a PNG image: %v", err)
				}
				if scenario == "write failure" {
					return errors.New("clipboard unavailable")
				}
				return nil
			}
			app.copyResult()
			if !app.busy || app.cancel == nil || writes != 0 {
				t.Fatal("copy must prepare the image asynchronously before publishing")
			}
			if scenario == "cancel" {
				app.cancelWork()
			}
			if scenario == "closed" {
				app.closed = true
			}
			select {
			case update := <-updates:
				update()
			case <-time.After(5 * time.Second):
				t.Fatal("clipboard preparation timed out")
			}
			wantWrites := 0
			if scenario == "success" || scenario == "write failure" || scenario == "batch preview" {
				wantWrites = 1
			}
			if writes != wantWrites || app.result != result || app.preview != preview || app.file != source ||
				app.lastSavedPath != "/photos/saved.webp" || !bytes.Equal(data, webPAppFixture(t, "alpha.webp")) {
				t.Fatal("copy must publish only valid, current work and preserve the result and save state")
			}
			if scenario != "closed" && (app.busy || app.cancel != nil || app.canceling) {
				t.Fatal("copy must finish its busy state")
			}
			switch scenario {
			case "success", "batch preview":
				if app.failed || !strings.Contains(app.message, "已复制") {
					t.Fatal("successful copying must report completion")
				}
				assertClipboardToast(t, app, "图片已复制到剪贴板")
			case "write failure", "invalid image":
				if !app.failed || !strings.Contains(app.message, "复制图片失败") {
					t.Fatal("failed copying must report an error")
				}
				assertClipboardToast(t, app, "复制图片失败")
			case "cancel":
				if app.failed || !strings.Contains(app.message, "取消") {
					t.Fatal("cancellation must be neutral")
				}
				if app.clipboardToast != nil {
					t.Fatal("cancellation must not show a completion toast")
				}
			}
		})
	}
}

func assertClipboardToast(t *testing.T, app *application, title string) {
	t.Helper()
	if app.clipboardToast == nil || app.clipboardToast.Title != title {
		t.Fatalf("copy must queue a visible notification: %+v", app.clipboardToast)
	}
	tester := ui.NewTester(app.view, 760, 660)
	tester.Frame()
	if _, ok := tester.Find(title); !ok {
		t.Fatalf("copy notification must be rendered: %s", title)
	}
	if app.clipboardToast != nil {
		t.Fatal("the notification must be consumed once rather than renewed every frame")
	}
	tester.Advance(5 * time.Second)
	tester.Frame()
	if _, ok := tester.Find(title); ok {
		t.Fatal("the notification must disappear automatically")
	}
}

func TestCopyResultGuards(t *testing.T) {
	for _, guard := range []string{"empty", "busy", "settings", "overwrite", "batch overwrite", "batch list", "closed"} {
		t.Run(guard, func(t *testing.T) {
			app := testSelectedApplication()
			app.result = &compress.Result{Data: []byte("unused")}
			switch guard {
			case "empty":
				app.result = nil
			case "busy":
				app.busy = true
			case "settings":
				app.settingsOpen = true
			case "overwrite":
				app.confirmOverwrite = true
			case "batch overwrite":
				app.confirmBatchOverwrite = true
			case "batch list":
				app.batch = newBatchState(nil)
			case "closed":
				app.closed = true
			}
			app.copyResult()
			if app.cancel != nil || app.canCopyResult() {
				t.Fatal("blocked copying must not start background work")
			}
		})
	}
}

func TestCopyResultActionFitsWindow(t *testing.T) {
	for _, width := range []int{760, 980} {
		for _, batch := range []bool{false, true} {
			app := testSelectedApplication()
			app.result = &compress.Result{Data: []byte("unused"), OriginalSize: 4096, OutputSize: 2048}
			if batch {
				app.batch = newBatchState([]batchItem{{path: app.file.path, status: batchDone}})
				app.batch.previewOpen = true
			}
			tester := ui.NewTester(app.view, width, 660)
			button, ok := tester.AccessByRole(ui.RoleButton, "复制图片")
			if !ok || button.Disabled || button.Offscreen || button.Bounds.X < 0 ||
				button.Bounds.X+button.Bounds.W > float32(width) ||
				button.Bounds.Y+button.Bounds.H > 660 {
				t.Fatalf("copy action must fit %d-point window (batch %t): %+v", width, batch, button)
			}
			app.busy = true
			tester.Frame()
			button, ok = tester.AccessByRole(ui.RoleButton, "复制图片")
			if !ok || !button.Disabled {
				t.Fatal("copy action must be disabled during work")
			}
		}
	}
}

func TestBatchPreviewRetainsClipboardResultSnapshot(t *testing.T) {
	for _, scenario := range []string{"saved", "unchanged", "overwritten"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			sourcePath, savedPath := filepath.Join(root, "source.webp"), filepath.Join(root, "saved.webp")
			source := writeWebPAppFixture(t, sourcePath, "alpha.webp")
			output := writeWebPAppFixture(t, savedPath, "opaque.webp")
			if scenario == "unchanged" {
				output = source
				if err := os.WriteFile(savedPath, output, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "overwritten" {
				output = source
			}
			item := batchItem{
				path: sourcePath, savedPath: savedPath, format: "webp", status: batchDone,
				originalSize: int64(len(source)), outputSize: int64(len(output)),
				unchanged: scenario == "unchanged", overwritten: scenario == "overwritten",
			}
			app := newApplication()
			app.batch = newBatchState([]batchItem{item})
			updates := make(chan func(), 1)
			app.dispatchUpdate = func(update func()) { updates <- update }
			app.openBatchPreview(0)
			select {
			case update := <-updates:
				update()
			case <-time.After(5 * time.Second):
				t.Fatal("batch preview timed out")
			}
			if app.failed || !app.canCopyResult() || !bytes.Equal(app.result.Data, output) {
				t.Fatal("batch previews must retain the exported image snapshot for copying")
			}
			if err := os.Remove(sourcePath); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(savedPath); err != nil {
				t.Fatal(err)
			}
			app.writeClipboardImage = func(data []byte) error {
				_, err := png.Decode(bytes.NewReader(data))
				return err
			}
			app.copyResult()
			select {
			case update := <-updates:
				update()
			case <-time.After(5 * time.Second):
				t.Fatal("copying batch result timed out")
			}
			if app.failed || !strings.Contains(app.message, "已复制") {
				t.Fatal("copying a loaded preview must work without reopening files")
			}
		})
	}
}
