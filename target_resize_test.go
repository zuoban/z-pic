package main

import (
	"bytes"
	"errors"
	"fmt"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"zpic/internal/compress"
)

func TestPresetTargetResizeEditor(t *testing.T) {
	const checkbox, field = "超出目标时自动缩小", "长边下限（px）"
	for _, size := range [][2]int{{640, 600}, {724, 700}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			app := testSelectedApplication()
			app.presets[1].targetSizeBytes = 500 << 10
			app.settingsFile = filepath.Join(t.TempDir(), "settings.json")
			if err := saveSettings(app.settingsFile, app.presets, app.scheme); err != nil {
				t.Fatal(err)
			}
			before := readFormatConvertFile(t, app.settingsFile)
			t.Setenv("ZPIC_CONFIG_FILE", app.settingsFile)
			app.restoreSettings()
			app.result = &compress.Result{OriginalSize: 4096, OutputSize: 2048}
			app.openPresetSettings()
			tester := ui.NewTester(app.settingsView, size[0], size[1])
			tester.Scroll(float32(size[0])/2, float32(size[1])/2, 0, 560)
			check, ok := tester.AccessByRole(ui.RoleCheckBox, checkbox)
			if !ok || check.Offscreen || check.Checked || app.targetResizeMinDimension != 0 || bytes.Contains(before, []byte("target_resize_min_dimension")) {
				t.Fatalf("legacy settings should show an unchecked opt-in: %+v", check)
			}
			if err := tester.Click(checkbox); err != nil {
				t.Fatal(err)
			}
			check, _ = tester.AccessByRole(ui.RoleCheckBox, checkbox)
			floor, ok := tester.AccessByRole(ui.RoleTextField, field)
			if !check.Checked || !ok || floor.Offscreen || floor.Value != "640" || !app.settingsHaveChanges() {
				t.Fatalf("enabling autoshrink must reveal the default floor: checkbox=%+v field=%+v", check, floor)
			}
			if err := tester.Click(field); err != nil {
				t.Fatal(err)
			}
			tester.Key(ui.Cmd, ui.KeyA)
			tester.Type("63")
			floor, _ = tester.AccessByRole(ui.RoleTextField, field)
			if !floor.Invalid || !strings.Contains(floor.Description, "64–16384") {
				t.Fatalf("invalid resize floor should expose the allowed range: %+v", floor)
			}
			if err := tester.Click("通用"); err != nil {
				t.Fatal(err)
			}
			if err := tester.Click("保存"); err != nil {
				t.Fatal(err)
			}
			if !app.settingsOpen || app.editor.section != settingsPresets || !tester.Focused(field) || app.result == nil ||
				!bytes.Equal(before, readFormatConvertFile(t, app.settingsFile)) {
				t.Fatal("failed save must bring the field into focus and preserve the applied settings")
			}
			tester.Key(ui.Cmd, ui.KeyA)
			tester.Type("768")
			if err := tester.Click("保存"); err != nil {
				t.Fatal(err)
			}
			if app.settingsOpen || app.result != nil || app.compressionOptions().TargetResizeMinDimension != 768 {
				t.Fatal("saving a new effective floor must apply it and invalidate the result")
			}
			after := readFormatConvertFile(t, app.settingsFile)
			loaded, selected, err := loadSettings(app.settingsFile)
			if err != nil || selected != app.scheme || loaded[1].targetResizeMinDimension != 768 ||
				!bytes.Contains(after, []byte(`"target_resize_min_dimension": 768`)) {
				t.Fatalf("resize floor did not round trip: selected=%q presets=%+v err=%v", selected, loaded, err)
			}
		})
	}
}

func TestPresetTargetResizeDormancyAndQuickTweaks(t *testing.T) {
	app := testSelectedApplication()
	app.settingsFile = filepath.Join(t.TempDir(), "settings.json")
	preset := &app.presets[1]
	preset.maxDimension, preset.targetResizeMinDimension = 1920, 640
	app.applyScheme(app.scheme)
	result := &compress.Result{OriginalSize: 4096, OutputSize: 2048}
	app.result = result
	preset.targetResizeMinDimension = 800
	if app.applyScheme(app.scheme) || app.result != result || app.compressionOptions().TargetResizeMinDimension != 0 {
		t.Fatal("changing a dormant floor should preserve the current result")
	}
	preset.targetSizeBytes = 500 << 10
	if !app.applyScheme(app.scheme) || app.result != nil || app.compressionOptions().TargetResizeMinDimension != 800 {
		t.Fatal("enabling a size target must activate the saved floor")
	}
	app.quickSetLossless()
	if options := app.compressionOptions(); options.TargetResizeMinDimension != 0 || options.MaxDimension != 1920 {
		t.Fatalf("lossless should pause autoshrink while retaining the manual cap: %+v", options)
	}
	app.resetQuickOverrides()
	if app.hasQuickOverrides() || app.compressionOptions().TargetResizeMinDimension != 800 {
		t.Fatal("reset should restore the active autoshrink option")
	}
	app.quickSetLossless()
	app.openSaveQuickPreset()
	draft := app.editor.selectedPreset()
	if !draft.lossless || draft.targetResizeMinDimension != 800 {
		t.Fatalf("save as must retain the configured floor in a lossless draft: %+v", draft)
	}
	app.saveSettingsEdits()
	presets, selected, err := loadSettings(app.settingsFile)
	if err != nil || selected != app.scheme || presets[len(presets)-1].targetResizeMinDimension != 800 {
		t.Fatalf("dormant resize floor did not round trip: presets=%+v err=%v", presets, err)
	}

	app.openPresetSettings()
	app.duplicatePreset()
	if app.editor.selectedPreset().targetResizeMinDimension != 800 || app.editor.targets[app.editor.selected].resizeMinText != "800" {
		t.Fatal("duplicating a preset should copy the resize floor and its draft text")
	}
	app.requestDeletePreset()
	app.deletePreset()
	app.addPreset()
	if app.editor.selectedPreset().targetResizeMinDimension != 0 || app.editor.targets[app.editor.selected].resizeMinText != "640" {
		t.Fatal("new presets should start with autoshrink off and the default draft floor")
	}

	invalid := compress.MaxResizeDimension + 1
	presets[1].targetResizeMinDimension = invalid
	var problem *presetValidationError
	if err := saveSettings(app.settingsFile, presets, selected); !errors.As(err, &problem) || problem.field != presetTargetResizeMinField {
		t.Fatalf("saving an invalid floor should identify its field: %v", err)
	}
	bad := bytes.Replace(readFormatConvertFile(t, app.settingsFile), []byte(`"target_resize_min_dimension": 800`),
		[]byte(fmt.Sprintf(`"target_resize_min_dimension": %d`, invalid)), 1)
	badPath := filepath.Join(t.TempDir(), "invalid.json")
	if err := os.WriteFile(badPath, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadSettings(badPath); !errors.As(err, &problem) || problem.field != presetTargetResizeMinField {
		t.Fatalf("loading an invalid floor should identify its field: %v", err)
	}
}

func TestTargetResizeDesktopResults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "photo.jpg")
	original := writeTestJPEG(t, path, 1024, 768, 100)
	app := newApplication()
	preset := &app.presets[0]
	preset.targetSizeBytes, preset.targetResizeMinDimension = compress.MinTargetSizeBytes, 640
	preset.output = presetSaveOptions{Mode: presetSaveFixed, Directory: t.TempDir()}
	app.applyScheme(app.scheme)
	updates := make(chan func(), 8)
	app.dispatchUpdate = func(fn func()) { updates <- fn }

	app.load(path)
	awaitTargetResizeUpdate(t, updates)
	app.startCompression()
	awaitTargetResizeUpdate(t, updates)
	result := app.result
	if result == nil {
		t.Fatalf("compression failed: %s", app.message)
	}
	decoded, err := jpeg.DecodeConfig(bytes.NewReader(result.Data))
	if err != nil || !result.TargetResized || result.Width < 640 || result.Width >= 1024 ||
		result.Width != decoded.Width || result.Height != decoded.Height || result.OutputSize != int64(len(result.Data)) || !result.TargetSizeMissed {
		t.Fatalf("unexpected single-image result: %d×%d, %d bytes, resized=%v, missed=%v, decoded=%+v, err=%v",
			result.Width, result.Height, result.OutputSize, result.TargetResized, result.TargetSizeMissed, decoded, err)
	}
	resultDimensions := fmt.Sprintf("%d × %d", result.Width, result.Height)
	resizeNote := "已自动缩小至 " + resultDimensions
	tester := ui.NewTester(app.view, 760, 660)
	for _, text := range []string{resizeNote, "未达目标 ≤ 1 KiB", formatSize(result.OutputSize)} {
		if !tester.HasText(text) {
			t.Fatalf("single-image result is missing %q", text)
		}
	}
	singlePreviewMetadata := fmt.Sprintf("输出 JPEG · %s · %s", resultDimensions, formatSize(result.OutputSize))
	if _, ok := tester.Find(singlePreviewMetadata); !ok {
		t.Fatalf("single-image preview footer should describe the resized output: %q", singlePreviewMetadata)
	}
	if err := tester.Click("原图"); err != nil {
		t.Fatal(err)
	}
	sourceMetadata := "JPEG  ·  1024 × 768  ·  " + formatSize(int64(len(original)))
	if _, ok := tester.Find(sourceMetadata); !ok || tester.HasText(singlePreviewMetadata) {
		t.Fatal("original preview should show the source dimensions and bytes")
	}
	if err := tester.Click("结果"); err != nil {
		t.Fatal(err)
	}
	if _, ok := tester.Find(singlePreviewMetadata); !ok {
		t.Fatal("result preview should restore the resized metadata")
	}

	app.applyPresetOutput(presetSaveOptions{Mode: presetSaveOriginal})
	tester.Frame()
	if err := tester.Click("覆盖原图…"); err != nil {
		t.Fatal(err)
	}
	if !tester.HasText("本次自动缩小为 "+resultDimensions+" px。") || !tester.Focused("取消") {
		t.Fatal("single overwrite should show the new dimensions and default to Cancel")
	}
	tester.Key(0, ui.KeyEnter)
	if app.confirmOverwrite || app.result != result || !bytes.Equal(original, readFormatConvertFile(t, path)) {
		t.Fatal("canceling an autoshrunk overwrite must preserve the source and preview")
	}
	app.applyPresetOutput(preset.output)

	app.loadBatch([]string{path}, false)
	awaitTargetResizeUpdate(t, updates)
	app.startBatchCompression()
	for app.busy {
		awaitTargetResizeUpdate(t, updates)
	}
	item := app.batch.items[0]
	if item.status != batchDone || !item.targetResized || !item.targetSizeMissed || item.savedPath == "" {
		t.Fatalf("batch result lost its autoshrink state: %+v", item)
	}
	saved, err := compress.Inspect(t.Context(), item.savedPath)
	if err != nil || saved.Width != item.width || saved.Height != item.height || saved.Size != item.outputSize ||
		!bytes.Equal(original, readFormatConvertFile(t, path)) {
		t.Fatalf("batch row should describe the saved copy while preserving the original: saved=%+v item=%+v err=%v", saved, item, err)
	}
	tester.Frame()
	batchDimensions := fmt.Sprintf("%d × %d", saved.Width, saved.Height)
	for _, text := range []string{"JPEG · " + batchDimensions + " · 自动缩小", formatSize(saved.Size), "1 张自动缩小", "1 张未达目标体积"} {
		if !tester.HasText(text) {
			t.Fatalf("batch result is missing %q", text)
		}
	}
	app.openBatchPreview(0)
	awaitTargetResizeUpdate(t, updates)
	tester.Frame()
	if app.result == nil || !app.result.TargetResized || !app.result.TargetSizeMissed ||
		!tester.HasText("已自动缩小至 "+batchDimensions) || !tester.HasText("未达目标 ≤ 1 KiB") {
		t.Fatal("batch preview should display the saved autoshrink result and target miss")
	}
	batchPreviewMetadata := fmt.Sprintf("输出 JPEG · %s · %s", batchDimensions, formatSize(saved.Size))
	if _, ok := tester.Find(batchPreviewMetadata); !ok {
		t.Fatalf("batch preview footer should describe the saved copy: %q", batchPreviewMetadata)
	}
	app.resetBatchRunState()
	item = app.batch.items[0]
	if item.width != 1024 || item.height != 768 || item.targetResized || item.targetSizeMissed || !bytes.Equal(original, readFormatConvertFile(t, path)) {
		t.Fatalf("resetting a batch copy should restore source dimensions and clear the result flags: %+v", item)
	}
}

func TestBatchRetainedSourceClearsTargetResize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "photo.jpg")
	original := writeTestJPEG(t, path, 1024, 768, 100)
	source, err := compress.Inspect(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	result := &compress.Result{
		SourcePath: path, Format: source.Format, Width: 640, Height: 480,
		OriginalSize: source.Size, OutputSize: source.Size + 1,
		TargetSizeBytes: source.Size, TargetSizeMissed: true, TargetResized: true,
	}
	update := saveBatchOutcomeWithBackup(batchTask{path: path}, batchCompressionOutcome{source: source, result: result}, batchOutputOverwrite, "", func(string) error {
		t.Fatal("keeping an original must not attempt to back it up or overwrite it")
		return nil
	})
	if update.status != batchDone || !update.unchanged || update.overwritten || update.width != source.Width ||
		update.height != source.Height || update.format != source.Format || update.outputSize != source.Size ||
		update.targetResized || update.targetSizeMissed || !bytes.Equal(original, readFormatConvertFile(t, path)) {
		t.Fatalf("skipping the larger result should report the retained source: %+v", update)
	}
}

func TestBatchTargetResizeOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "photo.jpg")
	original := writeTestJPEG(t, path, 1024, 768, 100)
	app := newApplication()
	preset := &app.presets[0]
	preset.targetSizeBytes, preset.targetResizeMinDimension = compress.MinTargetSizeBytes, 640
	preset.output = presetSaveOptions{Mode: presetSaveOriginal}
	app.applyScheme(app.scheme)
	updates := make(chan func(), 8)
	app.dispatchUpdate = func(fn func()) { updates <- fn }
	app.loadBatch([]string{path}, false)
	awaitTargetResizeUpdate(t, updates)

	tester := ui.NewTester(app.view, 760, 660)
	app.startBatchCompression()
	tester.Frame()
	if !app.confirmBatchOverwrite || !tester.HasText("部分图片的分辨率可能降低") || !tester.Focused("取消") {
		t.Fatal("batch overwrite should warn about resolution and default to Cancel")
	}
	tester.Key(0, ui.KeyEnter)
	if app.confirmBatchOverwrite || app.busy || app.batch.items[0].status != batchPending ||
		!bytes.Equal(original, readFormatConvertFile(t, path)) {
		t.Fatal("canceling batch overwrite must preserve the source and pending item")
	}

	app.startBatchCompression()
	tester.Frame()
	if err := tester.Click("覆盖原图"); err != nil {
		t.Fatal(err)
	}
	for app.busy {
		awaitTargetResizeUpdate(t, updates)
	}
	item := app.batch.items[0]
	saved, err := compress.Inspect(t.Context(), path)
	if err != nil || !item.overwritten || !item.targetResized || saved.Width >= 1024 || saved.Width < 640 ||
		saved.Width != item.width || saved.Height != item.height || saved.Size != item.outputSize {
		t.Fatalf("confirmed overwrite should record the resized file on disk: saved=%+v item=%+v err=%v", saved, item, err)
	}

	preset.targetResizeMinDimension = 0
	app.applyScheme(app.scheme)
	item = app.batch.items[0]
	if item.status != batchPending || item.targetResized || item.targetSizeMissed ||
		item.width != saved.Width || item.height != saved.Height || item.originalSize != saved.Size {
		t.Fatalf("changing the floor must reset the results and use the current overwritten file: %+v", item)
	}
}

// A resize fallback can run several full quality searches before it posts an update.
func awaitTargetResizeUpdate(t *testing.T, updates <-chan func()) {
	t.Helper()
	select {
	case update := <-updates:
		update()
	case <-time.After(30 * time.Second):
		t.Fatal("target resize did not deliver an update")
	}
}
