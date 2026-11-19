package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"zpic/internal/compress"
)

func TestBatchRestartProcessesEverySourceWithQueuedUpdates(t *testing.T) {
	for _, outputMode := range []int{batchOutputFolder, batchOutputOverwrite} {
		name := "folder"
		if outputMode == batchOutputOverwrite {
			name = "overwrite"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			outputDir := filepath.Join(root, "out")
			if err := os.MkdirAll(outputDir, 0o755); err != nil {
				t.Fatal(err)
			}
			paths := []string{
				filepath.Join(root, "first.jpg"),
				filepath.Join(root, "second.jpg"),
				filepath.Join(root, "third.jpg"),
			}
			writeTestJPEG(t, paths[0], 80, 60, 25)
			writeTestJPEG(t, paths[1], 100, 80, 25)
			writeTestJPEG(t, paths[2], 160, 120, 100)
			scan, err := scanBatchPaths(context.Background(), paths, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			updates := make(chan func(), len(paths)*2+1)
			app := newApplication()
			app.presets = testLegacyPresets()
			app.dispatchUpdate = func(update func()) { updates <- update }
			app.setScheme("画质优先")
			app.batch = newBatchState(scan.items)
			app.batch.outputMode, app.batch.outputDir = outputMode, outputDir
			tester := ui.NewTester(app.view, 880, 660)
			previousOutputs := make(map[string][]byte)

			for run := range 3 {
				if run == 2 {
					writeTestJPEG(t, paths[0], 200, 140, 100)
					writeTestJPEG(t, paths[1], 220, 160, 100)
				}
				originals := make([][]byte, len(paths))
				expected := make([]*compress.Result, len(paths))
				for index, path := range paths {
					originals[index], err = os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					expected[index], err = compress.File(context.Background(), path, app.compressionOptions())
					if err != nil {
						t.Fatal(err)
					}
					if index < 2 && expected[index].Unchanged != (run < 2) {
						t.Fatalf("run %d fixture %d has unexpected savings: %+v", run, index, expected[index])
					}
				}

				action := "重新压缩"
				if run == 0 {
					action = "开始压缩"
				}
				button, ok := tester.AccessByRole(ui.RoleButton, action)
				if !ok || button.Disabled || button.Offscreen || button.Bounds.Y > 80 || button.Bounds.H != 32 {
					t.Fatalf("run %d must use the enabled top compression action: %+v", run, button)
				}
				if err := tester.Click(action); err != nil {
					t.Fatal(err)
				}
				if outputMode == batchOutputOverwrite {
					if !app.confirmBatchOverwrite || !tester.HasText("将压缩 3 张图片") {
						t.Fatal("each overwrite run must confirm all three sources")
					}
					if err := tester.Click("覆盖原图"); err != nil {
						t.Fatal(err)
					}
				}
				if !app.busy || app.batch.total != len(paths) || app.batch.done != 0 {
					t.Fatalf("run %d must queue all sources: %+v", run, app.batch)
				}
				finished := make(chan struct{})
				go func() {
					app.saves.Wait()
					close(finished)
				}()
				select {
				case <-finished:
				case <-time.After(5 * time.Second):
					t.Fatal("batch worker did not finish with queued updates")
				}
				for len(updates) > 0 {
					(<-updates)()
				}
				tester.Frame()
				if app.busy || app.failed || app.batch.done != len(paths) {
					t.Fatalf("run %d did not complete every source: %q %+v", run, app.message, app.batch)
				}

				for index, item := range app.batch.items {
					result := expected[index]
					if item.status != batchDone || item.unchanged != result.Unchanged ||
						item.width != result.Width || item.height != result.Height ||
						item.originalSize != result.OriginalSize || item.outputSize != result.OutputSize {
						t.Fatalf("run %d item %d differs from independent compression: %+v", run, index, item)
					}
					resultPath := item.savedPath
					sourceData := originals[index]
					if outputMode == batchOutputOverwrite {
						resultPath = paths[index]
						sourceData = result.Data
						if item.overwritten == result.Unchanged || item.savedPath != "" {
							t.Fatalf("run %d item %d has incorrect overwrite state: %+v", run, index, item)
						}
					}
					output, err := os.ReadFile(resultPath)
					if err != nil || !bytes.Equal(output, result.Data) {
						t.Fatalf("run %d item %d saved incorrect data: %v", run, index, err)
					}
					source, err := os.ReadFile(paths[index])
					if err != nil || !bytes.Equal(source, sourceData) {
						t.Fatalf("run %d source %d has incorrect data: %v", run, index, err)
					}
					if outputMode == batchOutputFolder {
						if _, exists := previousOutputs[resultPath]; exists {
							t.Fatalf("run %d reused an earlier output path: %q", run, resultPath)
						}
						previousOutputs[resultPath] = output
					}
				}
				for path, before := range previousOutputs {
					after, err := os.ReadFile(path)
					if err != nil || !bytes.Equal(after, before) {
						t.Fatalf("run %d modified an earlier output: %q: %v", run, path, err)
					}
				}
			}
		})
	}
}

func TestAskModePickedOutputDirHandlesFirstSingleAndDirectorySwitch(t *testing.T) {
	root := t.TempDir()
	outA := filepath.Join(root, "out-a")
	outB := filepath.Join(root, "out-b")
	for _, dir := range []string{outA, outB} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	p0 := filepath.Join(root, "first.jpg")
	p1 := filepath.Join(root, "second.jpg")
	writeTestJPEG(t, p0, 120, 90, 95)
	writeTestJPEG(t, p1, 120, 90, 95)
	scan, err := scanBatchPaths(context.Background(), []string{p0, p1}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	updates := make(chan func(), 16)
	app := newApplication()
	app.dispatchUpdate = func(update func()) { updates <- update }
	app.applyPresetOutput(presetSaveOptions{Mode: presetSaveAsk, UseSuffix: true, Suffix: "-zpic"})
	app.batch = newBatchState(scan.items)
	app.batch.outputMode = batchOutputFolder
	app.batch.items[0].status = batchFailed
	app.batch.items[0].errMsg = "上次失败"

	drainWorker := func() {
		t.Helper()
		done := make(chan struct{})
		go func() {
			app.saves.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("batch worker timed out")
		}
		for len(updates) > 0 {
			(<-updates)()
		}
	}

	// First single-item run when outputDir is still empty must preserve sibling failure state.
	app.startBatchWithPickedOutputDir(outA, batchRunSingle, 1)
	drainWorker()
	if app.batch.items[0].status != batchFailed || app.batch.items[1].status != batchDone ||
		app.batch.items[1].savedPath != filepath.Join(outA, "second-zpic.jpg") {
		t.Fatalf("first single run in ask mode corrupted sibling state: %+v", app.batch.items)
	}
	firstExport, err := os.ReadFile(app.batch.items[1].savedPath)
	if err != nil {
		t.Fatal(err)
	}

	// Retrying/resuming into a different directory must reset and export all valid items to the new directory.
	app.startBatchWithPickedOutputDir(outB, batchRunRetryFailed, -1)
	drainWorker()
	for index, wantName := range []string{"first-zpic.jpg", "second-zpic.jpg"} {
		want := filepath.Join(outB, wantName)
		if app.batch.items[index].status != batchDone || app.batch.items[index].savedPath != want {
			t.Fatalf("directory switch left item %d incomplete: %+v (want %q)", index, app.batch.items[index], want)
		}
		if _, err := os.Stat(want); err != nil {
			t.Fatalf("expected export %q in new directory: %v", want, err)
		}
	}
	if after, err := os.ReadFile(filepath.Join(outA, "second-zpic.jpg")); err != nil || !bytes.Equal(after, firstExport) {
		t.Fatalf("previous directory export modified: %v", err)
	}
}
