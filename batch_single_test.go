package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestBatchCompressSinglePendingItem(t *testing.T) {
	for _, outputMode := range []int{batchOutputFolder, batchOutputOverwrite} {
		name := "folder"
		if outputMode == batchOutputOverwrite {
			name = "overwrite"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			outputDir := filepath.Join(root, "out")
			donePath := filepath.Join(root, "done.jpg")
			targetPath := filepath.Join(root, "target.jpg")
			laterPath := filepath.Join(root, "later.jpg")
			failedPath := filepath.Join(root, "failed.jpg")
			invalidPath := filepath.Join(root, "broken.png")
			writeTestJPEG(t, donePath, 80, 60, 100)
			original := writeTestJPEG(t, targetPath, 160, 120, 100)
			writeTestJPEG(t, laterPath, 80, 60, 100)
			writeTestJPEG(t, failedPath, 80, 60, 100)
			if err := os.WriteFile(invalidPath, []byte("broken"), 0o600); err != nil {
				t.Fatal(err)
			}
			savedPath := filepath.Join(outputDir, "done.jpg")
			savedData := writeTestJPEG(t, savedPath, 80, 60, 60)
			scan, err := scanBatchPaths(context.Background(), []string{donePath, targetPath, laterPath, failedPath, invalidPath}, nil, 0)
			if err != nil {
				t.Fatal(err)
			}

			updates := make(chan func(), 8)
			app := newApplication()
			app.presets = testLegacyPresets()
			app.dispatchUpdate = func(update func()) { updates <- update }
			app.setScheme("体积优先")
			app.batch = newBatchState(scan.items)
			app.batch.outputMode, app.batch.outputDir = outputMode, outputDir
			app.batch.items[0].status = batchDone
			app.batch.items[0].savedPath = savedPath
			app.batch.items[0].outputSize = int64(len(savedData))
			app.batch.items[3].status = batchFailed
			app.batch.items[3].errMsg = "写入失败"
			app.batch.sort = batchSortName
			before := slices.Clone(app.batch.items)
			selected, err := readSelection(context.Background(), targetPath)
			if err != nil {
				t.Fatal(err)
			}
			app.completeBatchPreview(context.Background(), 1, app.batch.items[1], selected, nil, nil, nil)
			tester := ui.NewTester(app.view, 880, 660)
			button, ok := tester.AccessByRole(ui.RoleButton, "压缩此张")
			if !ok || button.Disabled || button.Offscreen || button.Bounds.H < 28 ||
				button.Bounds.X+button.Bounds.W > 880 || button.Bounds.Y+button.Bounds.H > 660 {
				t.Fatalf("single-item action must fit the minimum window: %+v", button)
			}
			if err := tester.Click("压缩此张"); err != nil {
				t.Fatal(err)
			}
			if outputMode == batchOutputOverwrite {
				if !app.confirmBatchOverwrite || app.busy || app.pendingBatchRun != batchRunSingle ||
					app.pendingBatchIndex != 1 || !tester.HasText("将压缩 1 张图片") {
					t.Fatalf("single-item overwrite must wait for confirmation: %+v", app)
				}
				tester.Key(0, ui.KeyEnter)
				if app.confirmBatchOverwrite || !app.batch.previewOpen || !slices.Equal(app.batch.items, before) {
					t.Fatal("default Enter must cancel without changing preview or queue")
				}
				if current, err := os.ReadFile(targetPath); err != nil || !bytes.Equal(current, original) {
					t.Fatalf("canceling must preserve source bytes: %v", err)
				}
				if err := tester.Click("压缩此张"); err != nil {
					t.Fatal(err)
				}
				if err := tester.Click("覆盖原图"); err != nil {
					t.Fatal(err)
				}
			}
			t.Cleanup(app.saves.Wait)
			if !app.busy || app.batch.total != 1 || app.batch.previewOpen {
				t.Fatal("single-item compression must queue one task and return to the queue")
			}
			deadline := time.NewTimer(5 * time.Second)
			defer deadline.Stop()
			for app.busy {
				select {
				case update := <-updates:
					update()
				case <-deadline.C:
					t.Fatal("single-item worker did not complete")
				}
			}
			tester.Frame()
			target := app.batch.items[1]
			if target.status != batchDone || target.outputSize >= int64(len(original)) ||
				app.batch.selected != 1 || app.batch.total != 1 || app.batch.done != 1 {
				t.Fatalf("only the requested item must complete and remain selected: %+v", app.batch)
			}
			for index, item := range app.batch.items {
				if index != 1 && item != before[index] {
					t.Fatalf("single-item compression changed another item %d: %+v", index, item)
				}
			}
			if current, err := os.ReadFile(savedPath); err != nil || !bytes.Equal(current, savedData) {
				t.Fatalf("single-item compression must preserve previous outputs: %v", err)
			}
			resultPath := target.savedPath
			if outputMode == batchOutputOverwrite {
				resultPath = targetPath
				if !target.overwritten {
					t.Fatal("confirmed single-item overwrite must record the replacement")
				}
			} else if current, err := os.ReadFile(targetPath); err != nil || !bytes.Equal(current, original) {
				t.Fatalf("folder export must preserve the original: %v", err)
			}
			if data, err := os.ReadFile(resultPath); err != nil || int64(len(data)) != target.outputSize {
				t.Fatalf("single-item result must be saved to its expected path: %v", err)
			}
		})
	}
}

func TestBatchSinglePendingActionGuardsAndLayout(t *testing.T) {
	for _, dark := range []bool{false, true} {
		name := "light"
		if dark {
			name = "dark"
		}
		t.Run(name, func(t *testing.T) {
			app := testSelectedApplication()
			app.batch = newBatchState([]batchItem{
				{path: app.file.path, absPath: app.file.path, relPath: strings.Repeat("long-name-", 15) + ".jpg", status: batchPending},
			})
			app.batch.outputMode = batchOutputOverwrite
			app.completeBatchPreview(context.Background(), 0, app.batch.items[0], app.file, nil, nil, nil)
			tester := ui.NewTester(func(context *ui.Context) {
				theme := ui.LightTheme()
				if dark {
					theme = ui.DarkTheme()
				}
				context.SetTheme(theme)
				app.view(context)
			}, 880, 660)
			button, ok := tester.AccessByRole(ui.RoleButton, "压缩此张")
			remove, _ := tester.AccessByRole(ui.RoleButton, "移除")
			toolbar, _ := tester.AccessByRole(ui.RoleToolbar, "操作栏")
			if !ok || button.Offscreen || button.Bounds.H < 28 || button.Bounds.X < 0 ||
				button.Bounds.Y+button.Bounds.H > toolbar.Bounds.Y+toolbar.Bounds.H ||
				button.Bounds.X+button.Bounds.W > 880 || remove.Bounds.X+remove.Bounds.W > 880 {
				t.Fatalf("single-item compression must be in toolbar and removal remain bounded: action=%+v remove=%+v", button, remove)
			}
			for _, status := range []batchStatus{batchDone, batchProcessing, batchFailed} {
				app.batch.items[0].status = status
				app.batch.items[0].overwritten = false
				tester.Frame()
				if _, ok := tester.AccessByRole(ui.RoleButton, "压缩此张"); ok {
					t.Fatalf("pending-item action must be hidden for status %v", status)
				}
				if status == batchDone {
					if _, ok := tester.AccessByRole(ui.RoleButton, "重新压缩此张"); !ok {
						t.Fatal("non-overwritten done item must show 重新压缩此张 in preview")
					}
					if app.batchRunTargetCount(batchRunSingle, 0) != 1 {
						t.Fatal("non-overwritten done item must be eligible for single-item recompression")
					}
					app.batch.items[0].overwritten = true
					tester.Frame()
					if _, ok := tester.AccessByRole(ui.RoleButton, "重新压缩此张"); ok {
						t.Fatal("overwritten done item must hide 重新压缩此张")
					}
					if app.batchRunTargetCount(batchRunSingle, 0) != 0 {
						t.Fatal("overwritten done item must be rejected by single-item mode")
					}
					app.batch.items[0].overwritten = false
					continue
				}
				if status != batchFailed && app.batchRunTargetCount(batchRunSingle, 0) != 0 {
					t.Fatalf("single-item mode must reject status %v", status)
				}
			}
			app.batch.items[0].status = batchPending
			app.batch.items[0].invalidInput = true
			tester.Frame()
			if _, ok := tester.AccessByRole(ui.RoleButton, "压缩此张"); ok ||
				app.batchRunTargetCount(batchRunSingle, 0) != 0 {
				t.Fatal("invalid items must not be eligible for single-item compression")
			}
			app.batch.items[0].invalidInput = false
			app.busy = true
			tester.Frame()
			button, ok = tester.AccessByRole(ui.RoleButton, "压缩此张")
			if !ok || !button.Disabled {
				t.Fatal("single-item action must be disabled while busy")
			}
			for _, state := range []string{"busy", "settings", "confirmation"} {
				app.busy = state == "busy"
				app.settingsOpen = state == "settings"
				app.confirmOverwrite = state == "confirmation"
				app.beginBatchRunMode(batchRunSingle, 0)
				if app.confirmBatchOverwrite || app.batch.total != 0 || !app.batch.previewOpen {
					t.Fatalf("single-item request must respect the %s guard", state)
				}
			}
			if app.batchRunTargetCount(batchRunSingle, -1) != 0 || app.batchRunTargetCount(batchRunSingle, 1) != 0 {
				t.Fatal("single-item mode must reject out-of-range indices")
			}
		})
	}
}

func TestBatchSingleRecompressDoesNotReserveOwnSavedPath(t *testing.T) {
	dir := t.TempDir()
	aPath := filepath.Join(dir, "a.jpg")
	bPath := filepath.Join(dir, "b.jpg")
	writeTestJPEG(t, aPath, 100, 80, 95)
	writeTestJPEG(t, bPath, 100, 80, 95)

	aOut := filepath.Join(dir, "a-zpic.jpg")
	bOut := filepath.Join(dir, "b-zpic.jpg")

	app := newApplication()
	app.batch = newBatchState([]batchItem{
		{path: aPath, absPath: aPath, relPath: "a.jpg", format: "jpeg", status: batchDone, savedPath: aOut},
		{path: bPath, absPath: bPath, relPath: "b.jpg", format: "jpeg", status: batchDone, savedPath: bOut},
	})

	tasks, usedPaths := app.prepareBatchTasks(batchRunSingle, 0)
	if len(tasks) != 1 || tasks[0].index != 0 {
		t.Fatalf("expected single task for index 0, got %+v", tasks)
	}
	if usedPaths[aOut] {
		t.Fatalf("recompressing item 0 must not reserve its own old savedPath %q in usedPaths", aOut)
	}
	if !usedPaths[bOut] {
		t.Fatalf("other untouched item savedPath %q must remain reserved in usedPaths", bOut)
	}
}
