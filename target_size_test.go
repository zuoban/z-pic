package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"zpic/internal/compress"
)

func TestTargetSizePresetSettingsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	presets := testLegacyPresets()
	const activeIndex, target = 1, 1_234_567
	presets[activeIndex].targetSizeBytes = target
	name := presets[activeIndex].name
	if err := saveSettings(path, presets, name); err != nil {
		t.Fatal(err)
	}
	original := readFormatConvertFile(t, path)
	var stored struct {
		Presets []map[string]json.RawMessage `json:"presets"`
	}
	if err := json.Unmarshal(original, &stored); err != nil {
		t.Fatal(err)
	}
	if got := string(stored.Presets[activeIndex]["target_size_bytes"]); got != "1234567" {
		t.Fatalf("target_size_bytes = %q, want 1234567", got)
	}
	if _, ok := stored.Presets[0]["target_size_bytes"]; ok {
		t.Fatal("settings must omit disabled targets")
	}

	t.Setenv("ZPIC_CONFIG_FILE", path)
	app := newApplication()
	app.restoreSettings()
	if opts := app.compressionOptions(); app.settingsError != "" || app.scheme != name || opts.TargetSizeBytes != target {
		t.Fatalf("restored preset=%q target=%d error=%q", app.scheme, opts.TargetSizeBytes, app.settingsError)
	}
	tester := openTestSettings(t, app)
	replaceTestPresetName(t, tester, "分享")
	if err := tester.Click("保存"); err != nil {
		t.Fatal(err)
	}
	loaded, selected, err := loadSettings(path)
	if err != nil || selected != "分享" || loaded[activeIndex].targetSizeBytes != target || loaded[0].targetSizeBytes != 0 {
		t.Fatalf("saving another field changed the target: presets=%+v selected=%q err=%v", loaded, selected, err)
	}

	for _, invalid := range []int64{-1, 1023, 64<<20 + 1} {
		presets[activeIndex].targetSizeBytes = invalid
		var problem *presetValidationError
		if err := saveSettings(path, presets, name); !errors.As(err, &problem) || problem.field != "preset-target-size" {
			t.Errorf("saving invalid target %d returned %v", invalid, err)
		}
		bad := bytes.Replace(original, []byte(`"target_size_bytes": 1234567`), []byte(fmt.Sprintf(`"target_size_bytes": %d`, invalid)), 1)
		badPath := filepath.Join(t.TempDir(), "invalid.json")
		if err := os.WriteFile(badPath, bad, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := loadSettings(badPath); !errors.As(err, &problem) || problem.field != "preset-target-size" {
			t.Errorf("loading invalid target %d returned %v", invalid, err)
		}
	}
}

func TestTargetSizePresetEditor(t *testing.T) {
	for _, window := range []struct {
		name          string
		width, height int
	}{
		{"minimum", 640, 600},
		{"default", 724, 700},
	} {
		t.Run(window.name, func(t *testing.T) {
			app := newApplication()
			app.settingsFile = filepath.Join(t.TempDir(), "settings.json")
			app.openPresetSettings()
			tester := ui.NewTester(app.settingsView, window.width, window.height)
			tester.Scroll(320, 300, 0, 450)
			if err := tester.Click("200 KiB"); err != nil {
				t.Fatal(err)
			}
			const label = "自定义上限"
			field, ok := tester.AccessByRole(ui.RoleTextField, label)
			if !ok || field.Offscreen || field.Value != "200 KiB" || app.editor.selectedPreset().targetSizeBytes != 200<<10 {
				t.Fatalf("quick choice must update the visible custom field and draft: %+v", field)
			}
			if err := tester.Click("不限"); err != nil {
				t.Fatal(err)
			}
			if app.settingsHaveChanges() {
				t.Fatal("restoring the saved target must clear the draft change")
			}
			if err := tester.Click(label); err != nil {
				t.Fatal(err)
			}
			tester.Type("500 B")
			if field, _ := tester.AccessByRole(ui.RoleTextField, label); !field.Invalid || field.Description == "" || !app.settingsHaveChanges() {
				t.Fatalf("invalid target must be marked as an unsaved edit with an associated error: %+v", field)
			}
			if err := tester.Click("保存"); err != nil {
				t.Fatal(err)
			}
			if !app.settingsOpen || !strings.Contains(app.editor.message, "目标体积") || !tester.Focused(label) {
				t.Fatalf("invalid target must block save and focus the field: %+v", app.editor)
			}
			if _, err := os.Stat(app.settingsFile); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid draft must leave settings absent: %v", err)
			}
			tester.Key(ui.Cmd, ui.KeyA)
			tester.Type("300KB")
			if field, _ := tester.AccessByRole(ui.RoleTextField, label); field.Invalid || app.editor.selectedPreset().targetSizeBytes != 300_000 || app.targetSizeBytes != 0 {
				t.Fatalf("valid decimal limit must update only the draft: field=%+v draft=%d active=%d", field, app.editor.selectedPreset().targetSizeBytes, app.targetSizeBytes)
			}
			if err := tester.Click("保存"); err != nil {
				t.Fatal(err)
			}
			if app.settingsOpen || app.targetSizeBytes != 300_000 || app.compressionOptions().TargetSizeBytes != 300_000 {
				t.Fatalf("save did not activate the target: open=%v target=%d", app.settingsOpen, app.targetSizeBytes)
			}
			if summary := presetProcessingSummary(app.activePresetSnapshot()); summary != "体积 ≤ 300 KB" {
				t.Fatalf("preset summary = %q, want 体积 ≤ 300 KB", summary)
			}
		})
	}
}

func TestTargetSizeSingleResultSummary(t *testing.T) {
	app := testSelectedApplication()
	app.result = &compress.Result{Format: "jpeg", OriginalSize: 4096, OutputSize: 2048, TargetSizeBytes: 2048, AppliedQuality: 55}
	tester := ui.NewTester(app.view, 980, 720)
	if !tester.HasText("自动质量 55") {
		t.Fatal("successful search must show the selected quality")
	}
	app.result.TargetSizeBytes = 1024
	app.result.TargetSizeMissed = true
	tester.Frame()
	if !tester.HasText("未达目标 ≤ 1 KiB") {
		t.Fatal("unmet result must show the size limit")
	}
}

func TestTargetSizeBatchReportsRetainedFiles(t *testing.T) {
	app := newApplication()
	app.batch = newBatchState([]batchItem{
		{path: "/small.jpg", absPath: "/small.jpg", relPath: "small.jpg", format: "jpeg", status: batchPending},
		{path: "/big.jpg", absPath: "/big.jpg", relPath: "big.jpg", format: "jpeg", status: batchPending},
	})
	app.batch.outputMode = batchOutputOverwrite
	tester := ui.NewTester(app.view, 980, 720)
	const target = 2 << 10
	for index, original := range []int64{1500, 4000} {
		result := &compress.Result{
			Format: "jpeg", OriginalSize: original, OutputSize: 4500,
			TargetSizeBytes: target, TargetSizeMissed: true,
		}
		update := saveBatchOutcome(batchTask{index: index}, batchCompressionOutcome{result: result}, batchOutputOverwrite, "")
		app.applyBatchItemUpdate(update)
		item := app.batch.items[index]
		if item.status != batchDone || !item.unchanged || item.outputSize != original || item.targetSizeBytes != target || item.targetSizeMissed != (original > target) {
			t.Fatalf("batch result must reflect the retained original: %+v", item)
		}
	}
	tester.Frame()
	if app.batch.cachedStats.targetSizeMissed != 1 || !tester.HasText("未达目标") || !tester.HasText("跳过 2 张未减小 · 1 张未达目标体积") {
		t.Fatalf("batch must count and mark the single unmet item: %+v", tester.Texts())
	}
	resetBatchItemState(&app.batch.items[1])
	tester.Frame()
	if app.batch.items[1].targetSizeBytes != 0 || app.batch.items[1].targetSizeMissed || app.batch.cachedStats.targetSizeMissed != 0 || tester.HasText("未达目标") {
		t.Fatal("restarting an item must clear stale target results")
	}
}

func writeCLITargetSizeJPEG(t *testing.T, path string) []byte {
	t.Helper()
	const width, height, block = 1024, 768, 8
	img := image.NewGray(image.Rect(0, 0, width, height))
	rng := rand.New(rand.NewPCG(42, 73))
	// High-contrast blocks stay distinct even at the lowest JPEG quality.
	for y := 0; y < height; y += block {
		for x := 0; x < width; x += block {
			level := rng.IntN(2) * 224
			for row := y; row < y+block; row++ {
				pixels := img.Pix[img.PixOffset(x, row):]
				for column := range block {
					pixels[column] = uint8(level + rng.IntN(32))
				}
			}
		}
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, img, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func TestCLITargetSize(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "photo.jpg")
	original := writeCLITargetSizeJPEG(t, source)
	path := filepath.Join(dir, "settings.json")
	presets := testLegacyPresets()
	preset := &presets[2]
	preset.targetSizeBytes = 2 << 10
	if err := saveSettings(path, presets, preset.name); err != nil {
		t.Fatal(err)
	}
	presetArgs := []string{"--settings", path, "--preset", preset.name}
	for _, tc := range []struct {
		name, limit     string
		usePreset, save bool
		target          int64
		missed          bool
	}{
		{name: "explicit", limit: "25KB", save: true, target: 25_000},
		{name: "unreachable", limit: "2KiB", target: 2048, missed: true},
		{name: "inherit", usePreset: true, target: 2048, missed: true},
		{name: "override", usePreset: true, limit: "25KB", target: 25_000},
		{name: "disable", usePreset: true, limit: "0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{source}
			if tc.usePreset {
				args = append(args, presetArgs...)
			} else {
				args = append(args, "-q", "92")
			}
			if tc.limit != "" {
				args = append(args, "--max-size", tc.limit)
			}
			if !tc.save {
				args = append(args, "--dry-run")
			}
			report := runFormatConvertCompress(t, 1, args...)
			file := report.Files[0]
			if report.TargetSizeBytes != tc.target || file.TargetSizeMissed != tc.missed || (report.TargetSizeMissed == 1) != tc.missed {
				t.Fatalf("incorrect target report: %+v", report)
			}
			if tc.target == 0 {
				if file.AppliedQuality != 0 || file.OutputBytes <= 25_000 {
					t.Fatalf("disabled target changed encoding quality: %+v", file)
				}
			} else if (file.OutputBytes > tc.target) != tc.missed || file.AppliedQuality <= 0 || file.AppliedQuality >= 92 {
				t.Fatalf("incorrect output size or automatic quality: %+v", file)
			}
			if tc.save {
				if output := decodeFormatConvertFile(t, file.Output, "jpeg"); int64(len(output)) != file.OutputBytes {
					t.Fatalf("saved image has %d bytes, report says %d", len(output), file.OutputBytes)
				}
				if !bytes.Equal(readFormatConvertFile(t, source), original) {
					t.Fatal("saving a copy changed the source")
				}
			}
		})
	}

	var stdout, stderr bytes.Buffer
	args := []string{"compress", "--dry-run", "--max-size", "2KiB", source}
	if code := runCLIContext(t.Context(), args, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "[未达目标]") || !strings.Contains(stdout.String(), "目标体积 ≤ 2 KiB，1 未达目标") {
		t.Fatalf("text target report: exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	for _, invalid := range []string{"invalid", "1023", "65MiB", "-2KiB"} {
		stdout.Reset()
		stderr.Reset()
		args := []string{"compress", "--dry-run", "--max-size", invalid, source}
		if code := runCLIContext(t.Context(), args, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "目标体积") {
			t.Errorf("invalid --max-size %q: exit=%d stdout=%q stderr=%q", invalid, code, stdout.String(), stderr.String())
		}
	}
}

func TestCLIPresetsTargetSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	presets := testLegacyPresets()
	presets[1].targetSizeBytes = 300_000
	presets[1].targetResizeMinDimension = 640
	if err := saveSettings(path, presets, presets[1].name); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	args := []string{"presets", "--settings", path}
	if code := runCLIContext(t.Context(), args, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "体积 ≤ 300 KB") ||
		!strings.Contains(stdout.String(), "长边下限 640 px") {
		t.Fatalf("presets text: exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := runCLIContext(t.Context(), append(args, "--json"), &stdout, &stderr); code != 0 {
		t.Fatalf("presets JSON: exit=%d stderr=%q", code, stderr.String())
	}
	var report cliPresetsReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil || len(report.Presets) != len(presets) || report.Presets[1].TargetSizeBytes != 300_000 || report.Presets[0].TargetSizeBytes != 0 ||
		report.Presets[1].TargetResizeMinDimension != 640 || report.Presets[0].TargetResizeMinDimension != 0 {
		t.Fatalf("invalid presets JSON: %v\n%s", err, stdout.String())
	}
}

func TestCLITargetResize(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "photo.jpg")
	original := writeCLITargetSizeJPEG(t, source)
	preset := compressionPreset{
		name: "上传", quality: 92, png: compress.DefaultPNGOptions(),
		targetSizeBytes: 2 << 10, targetResizeMinDimension: 64,
	}
	denseSource := filepath.Join(dir, "dense.jpg")
	writeTestJPEG(t, denseSource, 1024, 768, 100)
	settings := filepath.Join(dir, "settings.json")
	if err := saveSettings(settings, []compressionPreset{preset}, preset.name); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name            string
		flags           []string
		resized, missed bool
		target          int64
		minWidth        int
	}{
		{"off by default", []string{"--max-size", "2KiB"}, false, true, 2048, 1024},
		{"explicit opt-in", []string{"--max-size", "2KiB", "--target-resize-min", "64"}, true, false, 2048, 64},
		{"preset inheritance", nil, true, false, 2048, 64},
		{"floor override", []string{"--max-size", "1KiB", "--target-resize-min", "640"}, true, true, 1024, 640},
		{"disable via flag", []string{"--target-resize-min", "0"}, false, true, 2048, 1024},
		{"disable target", []string{"--max-size", "0"}, false, false, 0, 1024},
		{"lossless", []string{"--lossless"}, false, true, 2048, 1024},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "result.jpg")
			input := source
			if tc.name == "floor override" {
				input = denseSource
			}
			args := []string{input, "-o", output}
			if tc.name != "explicit opt-in" && tc.name != "off by default" {
				args = append(args, "--settings", settings, "--preset", preset.name)
			}
			report := runFormatConvertCompress(t, 1, append(args, tc.flags...)...)
			result := report.Files[0]
			data := readFormatConvertFile(t, output)
			cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
			if err != nil || result.OutputBytes != int64(len(data)) || result.Width != cfg.Width || result.Height != cfg.Height {
				t.Fatalf("report does not match saved JPEG: %+v, config=%+v, error=%v", result, cfg, err)
			}
			if report.TargetSizeBytes != tc.target || result.TargetResized != tc.resized || result.TargetSizeMissed != tc.missed ||
				(tc.target > 0 && int64(len(data)) > tc.target) != tc.missed ||
				cfg.Width < tc.minWidth || (cfg.Width < 1024) != tc.resized || cfg.Height != (cfg.Width*3+2)/4 {
				t.Fatalf("unexpected target resize result: %+v", result)
			}
			if tc.resized && (result.OriginalWidth != 1024 || result.OriginalHeight != 768) {
				t.Fatalf("missing original dimensions: %+v", result)
			}
		})
	}
	if !bytes.Equal(readFormatConvertFile(t, source), original) {
		t.Fatal("target resize changed the source while saving a copy")
	}

	var stdout, stderr bytes.Buffer
	args := []string{"compress", denseSource, "--dry-run", "--max-size", "1KiB", "--target-resize-min", "640"}
	if code := runCLIContext(t.Context(), args, &stdout, &stderr); code != 0 ||
		!strings.Contains(stdout.String(), "[自动缩小 1024 × 768 → ") || !strings.Contains(stdout.String(), "[未达目标]") {
		t.Fatalf("text resize report: exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	for _, invalid := range []string{"-1", "63", "16385"} {
		stderr.Reset()
		if code := runCLIContext(t.Context(), []string{"compress", source, "--target-resize-min", invalid}, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "--target-resize-min") {
			t.Errorf("invalid resize floor %q: exit=%d stderr=%q", invalid, code, stderr.String())
		}
	}
}
