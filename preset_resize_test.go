package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"zpic/internal/compress"
)

func TestPresetResizeSettingsRoundTrip(t *testing.T) {
	valid := []struct {
		name   string
		limits resizeLimits
		key    string
		value  string
	}{
		{"原尺寸", resizeLimits{}, "", ""},
		{"长边", resizeLimits{maxDimension: 1920}, "max_dimension", "1920"},
		{"宽度", resizeLimits{maxWidth: 64}, "max_width", "64"},
		{"高度", resizeLimits{maxHeight: 16384}, "max_height", "16384"},
		{"比例", resizeLimits{scalePercent: 1}, "scale_percent", "1"},
		{"100%", resizeLimits{scalePercent: 100}, "scale_percent", "100"},
	}
	presets := make([]compressionPreset, len(valid))
	for i, test := range valid {
		presets[i] = compressionPresets[0]
		presets[i].name = test.name
		presets[i].setResizeLimits(test.limits)
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := saveSettings(path, presets, "宽度"); err != nil {
		t.Fatal(err)
	}
	data := readFormatConvertFile(t, path)
	var stored struct {
		Version int
		Presets []map[string]json.RawMessage
	}
	if err := json.Unmarshal(data, &stored); err != nil || stored.Version != 1 || len(stored.Presets) != len(valid) {
		t.Fatalf("unexpected stored settings: %+v, %v", stored, err)
	}
	for i, test := range valid {
		for _, key := range []string{"max_dimension", "max_width", "max_height", "scale_percent"} {
			want := ""
			if test.key == key {
				want = test.value
			}
			if got := string(stored.Presets[i][key]); got != want {
				t.Fatalf("preset %q, field %s = %q, want %q", test.name, key, got, want)
			}
		}
	}
	loaded, selected, err := loadSettings(path)
	if err != nil || selected != "宽度" || !slices.Equal(loaded, presets) {
		t.Fatalf("resize settings did not round trip: selected=%q, presets=%+v, error=%v", selected, loaded, err)
	}

	for _, test := range []struct {
		name   string
		limits resizeLimits
		json   string
	}{
		{"width below minimum", resizeLimits{maxWidth: 63}, `"max_width": 63`},
		{"height above maximum", resizeLimits{maxHeight: 16385}, `"max_height": 16385`},
		{"invalid scale", resizeLimits{scalePercent: 101}, `"scale_percent": 101`},
		{"conflicting limits", resizeLimits{maxDimension: 1920, maxWidth: 800}, `"max_dimension": 1920, "max_width": 800`},
	} {
		t.Run(test.name, func(t *testing.T) {
			invalid := slices.Clone(presets)
			invalid[0].setResizeLimits(test.limits)
			var validation *presetValidationError
			if err := saveSettings(path, invalid, selected); !errors.As(err, &validation) || validation.index != 0 || validation.field != presetResizeModeField {
				t.Fatalf("invalid resize settings must fail validation: %v", err)
			}
			if !bytes.Equal(data, readFormatConvertFile(t, path)) {
				t.Fatal("validation failure changed the stored settings")
			}
			bad := bytes.Replace(data, []byte(`"name": "原尺寸"`), []byte(`"name": "原尺寸", `+test.json), 1)
			badPath := filepath.Join(t.TempDir(), "invalid.json")
			if err := os.WriteFile(badPath, bad, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := loadSettings(badPath); !errors.As(err, &validation) || validation.index != 0 || validation.field != presetResizeModeField {
				t.Fatalf("invalid resize settings must fail loading: %v", err)
			}
		})
	}
}

func TestPresetResizeEditor(t *testing.T) {
	for _, size := range [][2]int{{640, 600}, {724, 700}} {
		for _, scale := range []float32{1, 1.5} {
			t.Run(fmt.Sprintf("%dx%d/%.1f", size[0], size[1], scale), func(t *testing.T) {
				app := testSelectedApplication()
				app.settingsFile = filepath.Join(t.TempDir(), "settings.json")
				if err := saveSettings(app.settingsFile, app.presets, app.scheme); err != nil {
					t.Fatal(err)
				}
				before := readFormatConvertFile(t, app.settingsFile)
				app.result = &compress.Result{OriginalSize: 4096, OutputSize: 2048}
				app.preview = nil
				main := ui.NewTester(app.view, 760, 660)
				chooseTestResize(t, main, "编辑预设缩放…")
				if app.editor == nil || app.editor.section != settingsPresets {
					t.Fatal("resize menu must open the current preset editor")
				}
				tester := ui.NewTesterWith(app.settingsView, size[0], size[1], ui.TesterOptions{
					Preferences: ui.Preferences{TextScale: scale, ReduceMotion: true},
				})
				original, _ := tester.AccessByRole(ui.RoleRadio, "原尺寸")
				if !original.Checked || !original.Focused || original.Offscreen {
					t.Fatalf("the resize entry must reveal and focus the current mode: %+v", original)
				}
				tester.Key(0, ui.KeyRight)
				if app.editor.selectedPreset().maxDimension != 1920 || !tester.Focused("按长边") {
					t.Fatal("the focused resize modes must support the arrow keys")
				}
				clickResizeControls(t, tester, "原尺寸")
				tester.Scroll(float32(size[0])/2, float32(size[1])/2, 0, 150)
				clickResizeControls(t, tester, "1280")
				longEdge, ok := tester.AccessByRole(ui.RoleTextField, "长边上限（px）")
				if !ok || longEdge.Value != "1280" || app.editor.selectedPreset().maxDimension != 1280 {
					t.Fatalf("choosing the first shortcut must reveal its editable long edge: %+v", longEdge)
				}
				clickResizeControls(t, tester, "1920")
				for _, test := range []struct {
					mode, field, value string
					limits             resizeLimits
				}{
					{"按宽度", "宽度上限（px）", "16384", resizeLimits{maxWidth: 16384}},
					{"按高度", "高度上限（px）", "64", resizeLimits{maxHeight: 64}},
					{"按百分比", "缩放比例（%）", "100", resizeLimits{scalePercent: 100}},
				} {
					clickResizeControls(t, tester, test.mode, test.field)
					tester.Key(ui.Cmd, ui.KeyA)
					tester.Type(test.value)
					field, ok := tester.AccessByRole(ui.RoleTextField, test.field)
					save, _ := tester.AccessByRole(ui.RoleButton, "保存")
					mode, _ := tester.AccessByRole(ui.RoleRadio, test.mode)
					if !ok || field.Offscreen || field.Invalid || field.Value != test.value || !mode.Checked ||
						field.Bounds.X+field.Bounds.W > float32(size[0]) ||
						field.Bounds.Y < mode.Bounds.Y+mode.Bounds.H || field.Bounds.Y+field.Bounds.H >= save.Bounds.Y || save.Offscreen {
						t.Fatalf("resize controls must fit above the settings footer: mode=%+v, field=%+v, save=%+v", mode, field, save)
					}
					if got := app.editor.selectedPreset().resizeLimits(); got != test.limits {
						t.Fatalf("resize choice %q = %+v, want %+v", test.mode, got, test.limits)
					}
				}
				const field = "缩放比例（%）"
				for _, invalid := range []string{"", "12.5", "0", "101"} {
					tester.Key(ui.Cmd, ui.KeyA)
					tester.Key(0, ui.KeyBackspace)
					tester.Type(invalid)
					info, _ := tester.AccessByRole(ui.RoleTextField, field)
					if !info.Invalid || !strings.Contains(info.Description, "1–100") {
						t.Fatalf("invalid percentage %q must explain its range: %+v", invalid, info)
					}
				}
				clickResizeControls(t, tester, "编辑预设：画质优先", "通用", "保存")
				if app.editor == nil || app.editor.selected != 1 || app.editor.section != settingsPresets || !tester.Focused(field) ||
					app.result == nil || !bytes.Equal(before, readFormatConvertFile(t, app.settingsFile)) {
					t.Fatal("failed save must reveal the edited preset and preserve the current result and settings")
				}
				tester.Key(ui.Cmd, ui.KeyA)
				tester.Type("75")
				clickResizeControls(t, tester, "保存")
				if app.settingsOpen || app.result != nil || app.preview != app.file.preview || app.resizeLimits != (resizeLimits{scalePercent: 75}) {
					t.Fatalf("saving the corrected resize must apply it and reset the preview: resize=%+v", app.resizeLimits)
				}
				loaded, _, err := loadSettings(app.settingsFile)
				if err != nil || loaded[1].scalePercent != 75 {
					t.Fatalf("settings editor did not persist the new percentage: %v", err)
				}
			})
		}
	}
}

func TestPresetResizeEditorUndoStaysWithinField(t *testing.T) {
	app := testSelectedApplication()
	app.presets[2].scalePercent = 60
	app.openPresetSettings()
	app.editor.focusField = presetResizeModeField
	tester := ui.NewTester(app.settingsView, 724, 700)
	clickResizeControls(t, tester, "按宽度", "宽度上限（px）")
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Type("800")

	const percentage = "缩放比例（%）"
	clickResizeControls(t, tester, "按百分比", percentage)
	tester.Command("undo")
	field, _ := tester.AccessByRole(ui.RoleTextField, percentage)
	if field.Value != "50" || field.Invalid {
		t.Fatalf("undo must preserve the new mode's default percentage: %+v", field)
	}

	tester.Key(ui.Cmd, ui.KeyA)
	tester.Type("75")
	tester.Command("undo")
	if limits := app.editor.selectedPreset().resizeLimits(); limits != (resizeLimits{scalePercent: 50}) {
		t.Fatalf("undo must restore the previous percentage: %+v", limits)
	}
	clickResizeControls(t, tester, "编辑预设：画质优先", percentage)
	tester.Command("redo")
	field, _ = tester.AccessByRole(ui.RoleTextField, percentage)
	if field.Value != "60" || app.editor.selectedPreset().scalePercent != 60 {
		t.Fatalf("redo must preserve the other preset's percentage: %+v", field)
	}

	clickResizeControls(t, tester, "复制预设")
	app.editor.focusField = presetResizeModeField
	tester.Frame()
	clickResizeControls(t, tester, percentage)
	field, _ = tester.AccessByRole(ui.RoleTextField, percentage)
	if field.Value != "60" || app.editor.selectedPreset().scalePercent != 60 {
		t.Fatalf("duplicating a preset must copy its resize value: %+v", field)
	}
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Type("40")
	clickResizeControls(t, tester, "编辑预设：画质优先")
	field, _ = tester.AccessByRole(ui.RoleTextField, percentage)
	if field.Value != "60" || app.editor.selectedPreset().scalePercent != 60 {
		t.Fatalf("editing a duplicate must leave the source draft intact: %+v", field)
	}
}

func TestPresetResizeQuickAdjustments(t *testing.T) {
	for _, size := range [][2]int{{760, 660}, {980, 720}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			app := testSelectedApplication()
			app.presets[1].maxWidth = 16384
			app.applyScheme(app.scheme)
			app.settingsFile = filepath.Join(t.TempDir(), "settings.json")
			app.result = &compress.Result{OriginalSize: 4096, OutputSize: 2048}
			tester := ui.NewTester(app.view, size[0], size[1])
			format, _ := tester.AccessByRole(ui.RoleMenuButton, "快速切换输出格式")
			sizePicker, _ := tester.AccessByRole(ui.RoleMenuButton, "快速切换尺寸缩放")
			if sizePicker.Value != "宽 ≤ 16384" || format.Offscreen || sizePicker.Offscreen || format.Bounds.W < 78 ||
				format.Bounds.X+format.Bounds.W >= sizePicker.Bounds.X || sizePicker.Bounds.X+sizePicker.Bounds.W > float32(size[0]) {
				t.Fatalf("format and resize controls must fit side by side: format=%+v, resize=%+v", format, sizePicker)
			}
			for _, test := range []struct {
				item   string
				limits resizeLimits
			}{
				{"缩至 50%", resizeLimits{scalePercent: 50}},
				{"长边 ≤ 1920px", resizeLimits{maxDimension: 1920}},
				{"原尺寸", resizeLimits{}},
			} {
				chooseTestResize(t, tester, test.item)
				if app.resizeLimits != test.limits || app.result != nil || app.presets[1].maxWidth != 16384 {
					t.Fatalf("quick choice must replace all prior limits without editing its preset: %+v", app.resizeLimits)
				}
				clickResizeControls(t, tester, "快速切换尺寸缩放")
				item, ok := tester.AccessByRole(ui.RoleMenuItemRadio, test.item)
				if !ok || !item.Checked || item.Offscreen {
					t.Fatalf("quick choice must be visibly checked in the menu: %+v", item)
				}
				tester.Key(0, ui.KeyEscape)
			}
			clickResizeControls(t, tester, "恢复预设参数")
			if app.hasQuickOverrides() || app.resizeLimits != (resizeLimits{maxWidth: 16384}) {
				t.Fatalf("reset must restore the saved width cap: %+v", app.resizeLimits)
			}
			chooseTestResize(t, tester, "缩至 25%")
			clickResizeControls(t, tester, "将微调存为新预设")
			settings := ui.NewTester(app.settingsView, 724, 700)
			clickResizeControls(t, settings, "保存")
			loaded, selected, err := loadSettings(app.settingsFile)
			if err != nil || selected != "均衡 微调" || loaded[len(loaded)-1].resizeLimits() != (resizeLimits{scalePercent: 25}) || app.hasQuickOverrides() {
				t.Fatalf("quick preset must store and activate the chosen percentage: selected=%q, error=%v", selected, err)
			}
		})
	}
}

func TestPresetResizeTargetHintUsesKnownLongEdge(t *testing.T) {
	for _, test := range []struct {
		name  string
		limit resizeLimits
		keeps bool
	}{
		{"long edge at floor", resizeLimits{maxDimension: 640}, true},
		{"long edge above floor", resizeLimits{maxDimension: 1920}, false},
		{"width", resizeLimits{maxWidth: 640}, false},
		{"height", resizeLimits{maxHeight: 640}, false},
		{"percentage", resizeLimits{scalePercent: 25}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			app := testSelectedApplication()
			preset := &app.presets[1]
			preset.setResizeLimits(test.limit)
			preset.targetSizeBytes, preset.targetResizeMinDimension = 500<<10, 640
			app.openPresetSettings()
			tester := ui.NewTester(app.settingsView, 640, 600)
			tester.Scroll(320, 300, 0, 600)
			keeps := tester.HasText("手动缩放后的长边已达到或低于下限，自动缩小将保留该尺寸。")
			generic := tester.HasText("手动缩放后，若体积仍超出目标且长边高于下限，会尝试继续等比缩小。")
			if keeps != test.keeps || generic == keeps {
				t.Fatalf("unexpected target resize hint: keeps=%v, generic=%v", keeps, generic)
			}
		})
	}
}

func TestPresetResizeDesktopOutputDimensions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "photo.jpg")
	original := writeTestJPEG(t, path, 384, 256, 95)
	app := newApplication()
	preset := &app.presets[0]
	preset.maxWidth = 120
	preset.output = presetSaveOptions{Mode: presetSaveFixed, Directory: t.TempDir()}
	app.applyScheme(app.scheme)
	updates := make(chan func(), 8)
	app.dispatchUpdate = func(fn func()) { updates <- fn }
	app.load(path)
	awaitBatchUpdate(t, updates)()
	tester := ui.NewTester(app.view, 760, 660)
	clickResizeControls(t, tester, "开始压缩")
	awaitBatchUpdate(t, updates)()
	if app.result == nil {
		t.Fatalf("single-image resize failed: %s", app.message)
	}
	decoded, err := jpeg.DecodeConfig(bytes.NewReader(app.result.Data))
	if err != nil || decoded.Width != 120 || decoded.Height != 80 || app.result.Width != decoded.Width || app.result.Height != decoded.Height {
		t.Fatalf("width preset must produce a 120×80 JPEG: decoded=%+v, error=%v", decoded, err)
	}

	preset.setResizeLimits(resizeLimits{maxHeight: 128})
	app.applyScheme(app.scheme)
	app.loadBatch([]string{path}, false)
	awaitBatchUpdate(t, updates)()
	app.startBatchCompression()
	for app.busy {
		awaitBatchUpdate(t, updates)()
	}
	item := app.batch.items[0]
	saved, err := compress.Inspect(t.Context(), item.savedPath)
	if err != nil || item.status != batchDone || saved.Width != 192 || saved.Height != 128 ||
		item.width != saved.Width || item.height != saved.Height || !bytes.Equal(original, readFormatConvertFile(t, path)) {
		t.Fatalf("height preset must produce a 192×128 copy: item=%+v, saved=%+v, error=%v", item, saved, err)
	}
	app.quickSetResize(resizeLimits{scalePercent: 50})
	item = app.batch.items[0]
	if item.status != batchPending || item.savedPath != "" || item.width != 384 || item.height != 256 {
		t.Fatalf("resize changes must invalidate completed batch items: %+v", item)
	}
}

func TestPresetResizeClipboardOutputDimensions(t *testing.T) {
	if !supportsClipboardPNGCompression() {
		t.Skip("requires bundled PNG tools")
	}
	var original bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.NoCompression}
	if err := encoder.Encode(&original, image.NewNRGBA(image.Rect(0, 0, 160, 120))); err != nil {
		t.Fatal(err)
	}
	app := newApplication()
	app.presets[0].scalePercent = 25
	app.applyScheme(app.scheme)
	updates := make(chan func(), 1)
	app.dispatchUpdate = func(fn func()) { updates <- fn }
	const revision = 42
	app.readClipboardPNG = func() ([]byte, int64, error) { return original.Bytes(), revision, nil }
	var result []byte
	app.replaceClipboardPNG = func(data []byte, gotRevision int64) error {
		if gotRevision != revision {
			t.Fatalf("clipboard revision = %d, want %d", gotRevision, revision)
		}
		result = data
		return nil
	}
	tester := ui.NewTester(app.view, 760, 660)
	clickResizeControls(t, tester, "压缩剪贴板图片")
	awaitBatchUpdate(t, updates)()
	decoded, err := png.DecodeConfig(bytes.NewReader(result))
	if app.failed || err != nil || decoded.Width != 40 || decoded.Height != 30 || len(result) >= original.Len() {
		t.Fatalf("clipboard percentage must write a smaller 40×30 PNG: decoded=%+v, message=%q, error=%v", decoded, app.message, err)
	}
}

func clickResizeControls(t *testing.T, tester *ui.Tester, labels ...string) {
	t.Helper()
	for _, label := range labels {
		if err := tester.Click(label); err != nil {
			t.Fatal(err)
		}
	}
}

func chooseTestResize(t *testing.T, tester *ui.Tester, item string) {
	t.Helper()
	clickResizeControls(t, tester, "快速切换尺寸缩放")
	if err := tester.ChooseMenuItem(item); err != nil {
		t.Fatal(err)
	}
}
