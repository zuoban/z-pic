package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"zpic/internal/compress"
)

func TestDefaultPresetsValidAndExposedInCLI(t *testing.T) {
	presets := defaultPresets()
	if err := validatePresets(presets, "均衡"); err != nil {
		t.Fatalf("default presets must pass validation: %v", err)
	}
	if len(presets) < 4 || presets[0].name != "均衡" {
		t.Fatalf("unexpected default presets: %+v", presets)
	}
	byName := make(map[string]compressionPreset, len(presets))
	for _, p := range presets {
		byName[p.name] = p
	}
	if p, ok := byName["网页 WebP"]; !ok || p.outputFormat != compress.OutputFormatWebP || p.metadata != compress.MetadataKeepICC {
		t.Fatalf("expected 网页 WebP built-in preset, got %+v", p)
	}
	if p, ok := byName["无损优化"]; !ok || !p.lossless {
		t.Fatalf("expected 无损优化 built-in preset, got %+v", p)
	}
	if p, ok := byName["≤ 500 KiB"]; !ok || p.targetSizeBytes != 500<<10 {
		t.Fatalf("expected ≤ 500 KiB built-in preset, got %+v", p)
	}
	_, hasAVIF := byName["极致 AVIF"]
	if hasAVIF != compress.SupportsAVIF() {
		t.Fatalf("极致 AVIF default presence (%v) must match SupportsAVIF (%v)", hasAVIF, compress.SupportsAVIF())
	}

	// CLI presets list must expose the same defaults when no settings file exists.
	t.Setenv("ZPIC_CONFIG_FILE", filepath.Join(t.TempDir(), "missing", "settings.json"))
	var stdout, stderr bytes.Buffer
	if code := runCLIContext(context.Background(), []string{"presets", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("z-pic presets --json failed: code=%d stderr=%q", code, stderr.String())
	}
	var report struct {
		Selected string `json:"selected"`
		Presets  []struct {
			Name string `json:"name"`
		} `json:"presets"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode presets json: %v", err)
	}
	if report.Selected != "均衡" || len(report.Presets) != len(presets) {
		t.Fatalf("CLI presets mismatch: %+v", report)
	}
}

func TestQuickTweaksDoNotMutatePresetAndCanResetOrSave(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	app := testSelectedApplication()
	app.presets = defaultPresets()
	app.settingsFile = settingsPath
	if err := saveSettings(settingsPath, app.presets, app.scheme); err != nil {
		t.Fatal(err)
	}
	beforeJSON, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	originalPresets := slices.Clone(app.presets)
	app.result = &compress.Result{OriginalSize: 4096, OutputSize: 2048}

	tester := ui.NewTester(app.view, 980, 720)
	if app.hasQuickOverrides() || tester.HasText("已微调") {
		t.Fatal("fresh preset must not show quick-override state")
	}

	// Quick-tweak quality level.
	if err := tester.Click("快速调整质量"); err != nil {
		t.Fatalf("open quality menu: %v", err)
	}
	if err := tester.ChooseMenuItem("1 · 体积优先"); err != nil {
		t.Fatalf("choose quality level: %v", err)
	}
	if app.quality != imageQualityLevels[1].quality || app.png != imageQualityLevels[1].png || app.lossless {
		t.Fatalf("quick quality change not applied: q=%v png=%+v lossless=%v", app.quality, app.png, app.lossless)
	}
	if app.result != nil {
		t.Fatal("quick tweak must invalidate existing result")
	}
	if !app.hasQuickOverrides() || !tester.HasText("已微调") || !tester.HasText("临时生效，未改动预设") {
		t.Fatal("quick tweak must display override indicator and actions")
	}

	// Quick-tweak output format & max dimension.
	if err := tester.Click("快速切换输出格式"); err != nil {
		t.Fatalf("open format menu: %v", err)
	}
	if err := tester.ChooseMenuItem("转为 WebP"); err != nil {
		t.Fatalf("choose WebP: %v", err)
	}
	if err := tester.Click("快速切换尺寸缩放"); err != nil {
		t.Fatalf("open dimension menu: %v", err)
	}
	if err := tester.ChooseMenuItem("长边 ≤ 1920px"); err != nil {
		t.Fatalf("choose 1920px: %v", err)
	}
	if app.outputFormat != compress.OutputFormatWebP || app.maxDimension != 1920 {
		t.Fatalf("quick format/dimension not applied: fmt=%q dim=%d", app.outputFormat, app.maxDimension)
	}
	if !reflect.DeepEqual(app.presets, originalPresets) {
		t.Fatal("quick tweaks must never mutate stored preset definitions")
	}
	afterJSON, err := os.ReadFile(settingsPath)
	if err != nil || !bytes.Equal(beforeJSON, afterJSON) {
		t.Fatal("quick tweaks must never write settings.json")
	}

	// Reset restores saved preset parameters and clears override badge.
	if err := tester.Click("恢复预设参数"); err != nil {
		t.Fatalf("click reset overrides: %v", err)
	}
	if app.hasQuickOverrides() || app.quality != 80 || app.outputFormat != compress.OutputFormatKeep || app.maxDimension != 0 {
		t.Fatalf("reset must restore balanced preset parameters: q=%v fmt=%q dim=%d", app.quality, app.outputFormat, app.maxDimension)
	}
	tester.Frame()
	if tester.HasText("已微调") {
		t.Fatal("resetting overrides must clear override badge")
	}

	// Tweak to lossless, then save as a new preset via Settings editor.
	if err := tester.Click("快速调整质量"); err != nil {
		t.Fatal(err)
	}
	if err := tester.ChooseMenuItem("无损压缩"); err != nil {
		t.Fatal(err)
	}
	if !app.lossless || !app.hasQuickOverrides() {
		t.Fatal("quick lossless toggle must activate lossless override")
	}
	if err := tester.Click("将微调存为新预设"); err != nil {
		t.Fatalf("click save quick preset: %v", err)
	}
	if !app.settingsOpen || app.editor == nil || app.editor.section != settingsPresets {
		t.Fatal("save quick preset must open settings on presets tab")
	}
	draft := app.editor.selectedPreset()
	if draft == nil || !draft.lossless || draft.name != "均衡 微调" || app.editor.active != app.editor.selected {
		t.Fatalf("save quick preset draft mismatch: %+v active=%d selected=%d", draft, app.editor.active, app.editor.selected)
	}
	settingsTester := ui.NewTester(app.settingsView, 724, 700)
	if !settingsTester.Focused("预设名称") {
		t.Fatal("save quick preset must focus preset name field")
	}
	if err := settingsTester.Click("保存"); err != nil {
		t.Fatalf("save new preset: %v", err)
	}
	if app.settingsOpen || app.scheme != "均衡 微调" || !app.lossless || app.hasQuickOverrides() {
		t.Fatalf("saving tweaked preset must activate it and clear overrides: open=%v scheme=%q lossless=%v overridden=%v",
			app.settingsOpen, app.scheme, app.lossless, app.hasQuickOverrides())
	}
	loaded, selected, err := loadSettings(settingsPath)
	if err != nil || selected != "均衡 微调" || len(loaded) != len(originalPresets)+1 {
		t.Fatalf("saved tweaked preset must persist: len=%d selected=%q err=%v", len(loaded), selected, err)
	}
}

func TestQuickTweakAutoCompressAndSamePresetReset(t *testing.T) {
	dir := t.TempDir()
	jpgPath := filepath.Join(dir, "sample.jpg")
	writeTestJPEG(t, jpgPath, 80, 60, 90)

	updates := make(chan func(), 16)
	app := newApplication()
	app.autoCompress = true
	app.dispatchUpdate = func(fn func()) { updates <- fn }
	app.load(jpgPath)
	(<-updates)() // load done -> starts auto-compress
	(<-updates)() // initial compression done
	if app.busy || app.result == nil {
		t.Fatalf("initial auto-compress failed: busy=%v result=%v", app.busy, app.result)
	}

	// Quick format tweak triggers auto-compress when autoCompress is enabled.
	app.quickSetOutputFormat(compress.OutputFormatWebP)
	if !app.busy || app.phase != "正在压缩图片" {
		t.Fatalf("quick tweak with autoCompress must trigger compression: busy=%v phase=%q", app.busy, app.phase)
	}
	(<-updates)()
	if app.busy || app.result == nil || app.result.Format != "webp" {
		t.Fatalf("quick tweaked compression must output webp: busy=%v result=%+v", app.busy, app.result)
	}

	// Re-selecting the same preset resets the quick override cleanly.
	app.setScheme("均衡")
	if app.hasQuickOverrides() || app.outputFormat != compress.OutputFormatKeep {
		t.Fatalf("re-selecting current preset must clear temporary override: fmt=%q", app.outputFormat)
	}
	if !app.busy {
		t.Fatal("resetting via setScheme with autoCompress must re-compress")
	}
	(<-updates)()
}

func TestSizeGrowthWarningAndHintsSingleAndBatch(t *testing.T) {
	// Single-image PNG conversion growth shows warning hint and preserves metadata note.
	app := styleReviewApplication(t, "result")
	app.file.format = "avif"
	app.outputFormat = compress.OutputFormatPNG
	app.result = &compress.Result{
		SourcePath:   app.file.path,
		Format:       "png",
		Width:        1920,
		Height:       1080,
		OriginalSize: 270_000,
		OutputSize:   1_930_000,
	}
	for _, size := range [][2]int{{760, 660}, {980, 720}} {
		tester := ui.NewTester(app.view, size[0], size[1])
		if !tester.HasText("增大 614.8%") || !tester.HasText("转为 PNG") {
			t.Fatalf("single growth card must show growth percentage and format note at %v", size)
		}
		if !tester.HasText("转为 PNG 易增大复杂图体积，可改用 WebP、JPEG 或保持原格式") {
			t.Fatalf("PNG growth card must show actionable hint at %v", size)
		}
		// Ensure bottom save actions stay visible even with quick-override bar + growth hint at 760x660.
		for _, label := range []string{"保存到原文件夹", "另存副本", "复制图片"} {
			btn, ok := tester.AccessByRole(ui.RoleButton, label)
			if !ok || btn.Offscreen || btn.Bounds.Y+btn.Bounds.H > float32(size[1]) {
				t.Fatalf("save button %q clipped at %v: %+v", label, size, btn)
			}
		}
	}

	// Batch item status color and aggregate summary growth warning.
	theme := ui.DarkTheme()
	statusText, statusColor := batchItemStatus(batchItem{
		status:       batchDone,
		originalSize: 1000,
		outputSize:   2500,
	}, theme)
	if statusText != "+150.0%" || statusColor != theme.Warning {
		t.Fatalf("grown batch item must use Warning color: text=%q color=%v want=%v", statusText, statusColor, theme.Warning)
	}

	batchApp := newApplication()
	batchApp.outputFormat = compress.OutputFormatPNG
	batchApp.batch = newBatchState([]batchItem{
		{path: "/a.avif", absPath: "/a.avif", relPath: "a.avif", format: "avif", originalSize: 1000, outputSize: 3000, status: batchDone},
		{path: "/b.avif", absPath: "/b.avif", relPath: "b.avif", format: "avif", originalSize: 1000, outputSize: 2000, status: batchDone},
	})
	batchTester := ui.NewTester(batchApp.view, 880, 660)
	if !batchTester.HasText("增大 150.0%") || !batchTester.HasText("共增大 ") ||
		!batchTester.HasText("批量转为 PNG 易增大体积，可改用 WebP、JPEG 或保持原格式") {
		t.Fatalf("batch growth summary must show growth percent, total bytes grown, and hint: %q", strings.Join(batchTester.Texts(), " | "))
	}
}
