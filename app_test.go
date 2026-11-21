package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"zpic/internal/compress"
)

func testLegacyPresets() []compressionPreset {
	return []compressionPreset{
		{name: "体积优先", quality: 60, png: compress.PNGOptions{MinQuality: 40, MaxQuality: 65, Colors: 128, Speed: 3}},
		{name: "均衡", quality: 80, png: compress.DefaultPNGOptions()},
		{name: "画质优先", quality: 92, png: compress.PNGOptions{MinQuality: 85, MaxQuality: 100, Colors: 256, Speed: 1}},
	}
}

func testSelectedApplication() *application {
	s := newApplication()
	s.presets = testLegacyPresets()
	preview := ui.NewBitmap(image.NewNRGBA(image.Rect(0, 0, 20, 12)))
	s.file = &selectedImage{
		path: "/photos/example.jpg", format: "jpeg", width: 20, height: 12,
		size: 4096, preview: preview,
	}
	s.preview = preview
	return s
}

func TestEmptyViewCannotCompress(t *testing.T) {
	s := newApplication()
	wantDefaults := defaultPresets()
	if !slices.Equal(s.presets, wantDefaults) || s.presets[0].name != "均衡" || s.quality != 80 || s.png != compress.DefaultPNGOptions() {
		t.Fatalf("initialization must offer the built-in default presets starting with balanced: %+v", s.presets)
	}
	tt := ui.NewTester(s.view, 980, 720)
	if _, ok := tt.AccessByRole(ui.RoleMenuButton, "选择预设"); ok || !tt.HasText("均衡") || tt.HasText("体积优先") || tt.HasText("画质优先") {
		t.Fatal("the empty view must display the balanced preset without a settings-only picker")
	}
	button, ok := tt.AccessByRole(ui.RoleButton, "开始压缩")
	if !ok || !button.Disabled {
		t.Fatalf("compression must be disabled without an image: %+v", button)
	}
	if !tt.HasText("选择图片") || !tt.HasText("选择文件夹") || !tt.HasText("选择或拖入图片") {
		t.Fatalf("the empty view must explain how to start: %q", tt.Texts())
	}
	settings := openTestSettings(t, s)
	if err := settings.Click("选择预设"); err != nil {
		t.Fatal(err)
	}
	if item, ok := settings.AccessByRole(ui.RoleMenuItemRadio, "均衡"); !ok || !item.Checked {
		t.Fatal("the default popup must contain the checked balanced preset")
	}
	for _, wantName := range []string{"网页 WebP", "无损优化", "≤ 500 KiB"} {
		if _, ok := settings.AccessByRole(ui.RoleMenuItemRadio, wantName); !ok {
			t.Fatalf("the default popup must include built-in preset %q", wantName)
		}
	}
}

func TestPresetPickerSelectionAndDismissal(t *testing.T) {
	s := testSelectedApplication()
	s.settingsFile = filepath.Join(t.TempDir(), "settings.json")
	result := &compress.Result{OriginalSize: 4096, OutputSize: 2048}
	s.result = result
	tt := openTestSettings(t, s)
	if err := tt.Click("选择预设"); err != nil {
		t.Fatal(err)
	}
	for _, preset := range testLegacyPresets() {
		item, ok := tt.AccessByRole(ui.RoleMenuItemRadio, preset.name)
		if !ok || item.Checked != (preset.name == "均衡") {
			t.Fatalf("menu must mark only the active preset: %+v", item)
		}
	}
	if err := tt.ChooseMenuItem("均衡"); err != nil {
		t.Fatal(err)
	}
	if s.result != result {
		t.Fatal("selecting the active preset must preserve the compression result")
	}
	if _, ok := tt.AccessByRole(ui.RoleMenuItemRadio, "均衡"); ok {
		t.Fatal("choosing a preset must close the menu")
	}
	if err := tt.Click("选择预设"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyEscape)
	if s.scheme != "均衡" || s.result != result || !tt.Focused("选择预设") {
		t.Fatal("Escape must preserve selection and result and return focus to the picker")
	}
	if _, ok := tt.AccessByRole(ui.RoleMenuItemRadio, "均衡"); ok {
		t.Fatal("Escape must close the preset menu")
	}
	if err := tt.Click("选择预设"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("画质优先"); err != nil {
		t.Fatal(err)
	}
	if s.editor.presets[s.editor.active].name != "画质优先" || s.scheme != "均衡" || s.result != result {
		t.Fatal("choosing a preset must update only the settings draft before saving")
	}
	if _, ok := tt.AccessByRole(ui.RoleMenuItemRadio, "画质优先"); ok {
		t.Fatal("applying a different preset must close the menu")
	}
	if err := tt.Click("选择预设"); err != nil {
		t.Fatal(err)
	}
	if item, ok := tt.AccessByRole(ui.RoleMenuItemRadio, "画质优先"); !ok || !item.Checked {
		t.Fatalf("reopening must show the newly selected preset checked: %+v", item)
	}
	tt.Key(0, ui.KeyEscape)
	if err := tt.Click("保存"); err != nil {
		t.Fatal(err)
	}
	if s.scheme != "画质优先" || s.quality != 92 || s.result != nil {
		t.Fatal("saving the selected preset must apply it and invalidate the old result")
	}
}

func TestMainToolbarFitsAcrossViews(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, floating := range []bool{false, true} {
			for _, state := range []string{"empty", "single", "result", "queue", "partial-queue", "preview"} {
				t.Run(map[bool]string{false: "light/", true: "dark/"}[dark]+map[bool]string{false: "floating-off/", true: "floating-on/"}[floating]+state, func(t *testing.T) {
					s := testSelectedApplication()
					s.floatingOpen = floating
					name := strings.Repeat("长名称预设", 20)
					s.presets = append(s.presets, compressionPreset{name: name, quality: 80, png: compress.DefaultPNGOptions()})
					s.setScheme(name)
					if state == "empty" {
						s.file, s.preview = nil, nil
					}
					if state == "result" {
						s.result = &compress.Result{OriginalSize: 4096, OutputSize: 2048}
					}
					if state == "queue" || state == "partial-queue" || state == "preview" {
						s.batch = newBatchState([]batchItem{{path: "/photos/example.jpg", absPath: "/photos/example.jpg", relPath: "example.jpg", format: "jpeg", status: batchPending}})
						s.batch.previewOpen = state == "preview"
						if s.batch.previewOpen {
							s.batch.previewIndex = 0
						}
						if state == "partial-queue" {
							s.batch.items = append(s.batch.items, batchItem{path: "/photos/done.jpg", absPath: "/photos/done.jpg", relPath: "done.jpg", format: "jpeg", status: batchDone, originalSize: 4096, outputSize: 2048})
						}
					}
					tt := ui.NewTester(func(c *ui.Context) {
						if dark {
							c.SetTheme(ui.DarkTheme())
						} else {
							c.SetTheme(ui.LightTheme())
						}
						s.view(c)
					}, 760, 660)
					toolbar, ok := tt.AccessByRole(ui.RoleToolbar, "操作栏")
					if !ok {
						t.Fatal("the top bar must identify its actions as a toolbar")
					}
					for _, label := range []string{"选择预设", "主题"} {
						if _, ok := tt.AccessByRole(ui.RoleMenuButton, label); ok {
							t.Fatalf("%s must only be available in settings", label)
						}
					}
					floatingLabel := "悬浮窗"
					if floating {
						floatingLabel = "收起悬浮窗"
					}
					labels := []string{"设置", floatingLabel, "开始压缩"}
					if state == "result" || state == "partial-queue" {
						labels[2] = "重新压缩"
					}
					if state == "partial-queue" {
						labels = append(labels, "继续压缩")
					}
					if state == "preview" {
						labels[2] = "压缩此张"
					}
					var controls []ui.AccessInfo
					for _, label := range labels {
						role := ui.RoleButton
						control, ok := tt.AccessByRole(role, label)
						b, bar := control.Bounds, toolbar.Bounds
						if !ok || control.Offscreen || b.W <= 0 || b.H < 28 || b.X < 0 || b.Y < 0 || b.X+b.W > 760 || b.Y+b.H > 80 || b.X < bar.X || b.Y < bar.Y || b.X+b.W > bar.X+bar.W || b.Y+b.H > bar.Y+bar.H {
							t.Fatalf("%s must fit within the top toolbar: %+v; toolbar: %+v", label, control, toolbar)
						}
						for _, other := range controls {
							o := other.Bounds
							if b.X < o.X+o.W && o.X < b.X+b.W && b.Y < o.Y+o.H && o.Y < b.Y+b.H {
								t.Fatalf("top toolbar controls overlap: %+v and %+v", control, other)
							}
						}
						controls = append(controls, control)
					}
					if _, ok := tt.AccessByRole(ui.RoleRadioGroup, "预设"); ok {
						t.Fatal("the main page must not keep a preset radio group")
					}
					if _, ok := tt.AccessByRole(ui.RoleRadio, name); ok {
						t.Fatal("preset options must only appear in the popup")
					}
				})
			}
		}
	}
}

func TestSingleCompressionActionInTopToolbar(t *testing.T) {
	for _, state := range []string{"empty", "single", "result", "busy", "busy-result", "settings", "confirmation"} {
		t.Run(state, func(t *testing.T) {
			s := testSelectedApplication()
			label := "开始压缩"
			switch state {
			case "empty":
				s.file, s.preview = nil, nil
			case "result", "busy-result", "confirmation":
				s.result = &compress.Result{OriginalSize: 4096, OutputSize: 2048}
				label = "重新压缩"
			}
			if state == "busy" || state == "busy-result" {
				s.busy = true
			}
			if state == "settings" {
				s.setSettingsOpen(true)
			}
			if state == "confirmation" {
				s.requestOverwrite()
			}
			tt := ui.NewTester(s.view, 760, 660)
			button, ok := tt.AccessByRole(ui.RoleButton, label)
			if state == "confirmation" {
				if ok && !button.Disabled {
					t.Fatalf("confirmation must block compression: %+v", button)
				}
			} else {
				bar, hasBar := tt.AccessByRole(ui.RoleToolbar, "操作栏")
				b := button.Bounds
				if !ok || !hasBar || button.Offscreen || b.Y < 0 || b.Y+b.H > 80 || b.X < bar.Bounds.X || b.Y < bar.Bounds.Y || b.X+b.W > bar.Bounds.X+bar.Bounds.W || b.Y+b.H > bar.Bounds.Y+bar.Bounds.H {
					t.Fatalf("%s must remain in the top toolbar: %+v; toolbar: %+v", label, button, bar)
				}
				wantDisabled := state != "single" && state != "result"
				if button.Disabled != wantDisabled {
					t.Fatalf("compression disabled = %v, want %v in %s", button.Disabled, wantDisabled, state)
				}
			}
			if state != "single" && state != "result" {
				file, result, busy := s.file, s.result, s.busy
				_ = tt.Click(label)
				if s.file != file || s.result != result || s.busy != busy {
					t.Fatal("blocked compression must preserve the image, result and processing state")
				}
			}
			if state == "result" {
				for _, saveLabel := range []string{"另存副本", "覆盖原图…"} {
					if save, ok := tt.AccessByRole(ui.RoleButton, saveLabel); !ok || save.Disabled {
						t.Fatalf("moving recompression to the toolbar must preserve %s: %+v", saveLabel, save)
					}
				}
			}
		})
	}
}

func TestPresetPickerCannotOpenDuringModalState(t *testing.T) {
	for _, state := range []string{"processing", "overwrite", "batch-overwrite", "settings"} {
		t.Run(state, func(t *testing.T) {
			s := testSelectedApplication()
			switch state {
			case "processing":
				s.busy = true
			case "overwrite":
				s.result = &compress.Result{OriginalSize: 4096, OutputSize: 2048}
				s.requestOverwrite()
			case "batch-overwrite":
				s.batch = newBatchState([]batchItem{{path: "/photos/example.jpg", relPath: "example.jpg", status: batchPending}})
				s.confirmBatchOverwrite = true
			case "settings":
				s.setSettingsOpen(true)
			}
			tt := ui.NewTester(s.view, 880, 660)
			if state == "overwrite" || state == "batch-overwrite" {
				label := "确认覆盖原图？"
				if state == "batch-overwrite" {
					label = "确认批量覆盖原图？"
				}
				if _, ok := tt.AccessByRole(ui.RoleAlertDialog, label); !ok {
					t.Fatalf("%s must block the main window with its confirmation", state)
				}
				if _, ok := tt.AccessByRole(ui.RoleMenuButton, "选择预设"); ok {
					t.Fatal("confirmation must hide the underlying preset picker from interaction")
				}
			}
			if _, ok := tt.AccessByRole(ui.RoleMenuButton, "选择预设"); ok {
				t.Fatal("main view must not expose preset selection")
			}
			_ = tt.Click("选择预设")
			if _, ok := tt.AccessByRole(ui.RoleMenuItemRadio, "均衡"); ok {
				t.Fatal("disabled preset picker must not open a menu")
			}
		})
	}
}

func TestChangingQualityInvalidatesSavedResult(t *testing.T) {
	s := testSelectedApplication()
	s.result = &compress.Result{OriginalSize: 4096, OutputSize: 2048}
	chooseTestPreset(t, s, "体积优先")
	tt := ui.NewTester(s.view, 980, 720)
	if s.quality != 60 || s.result != nil || s.preview != s.file.preview {
		t.Fatal("changing quality must discard the old export and restore the original preview")
	}
	if tt.HasText("另存副本") {
		t.Fatal("a stale compression result must not be offered for saving")
	}
}

func TestBusyViewCannotChangeQuality(t *testing.T) {
	s := testSelectedApplication()
	s.busy, s.phase = true, "正在压缩图片"
	tt := ui.NewTester(s.view, 880, 660)
	for _, label := range []string{"更换图片", "开始压缩"} {
		button, ok := tt.AccessByRole(ui.RoleButton, label)
		if !ok || !button.Disabled {
			t.Errorf("%q must be disabled while processing: %+v", label, button)
		}
	}
	picker, ok := tt.AccessByRole(ui.RoleMenuButton, "选择预设")
	if ok {
		t.Fatalf("main view must not expose preset selection: %+v", picker)
	}
	settings, ok := tt.AccessByRole(ui.RoleButton, "设置")
	if !ok || !settings.Disabled {
		t.Fatalf("settings navigation must be disabled while processing: %+v", settings)
	}
}

func TestPresetQualityReplacesAdvancedParameters(t *testing.T) {
	for _, format := range []string{"jpeg", "png", "webp"} {
		t.Run(format, func(t *testing.T) {
			s := testSelectedApplication()
			s.file.format = format
			options := s.compressionOptions()
			tt := openTestSettings(t, s)
			if _, ok := tt.AccessByRole(ui.RoleDisclosure, "高级压缩参数"); ok {
				t.Fatal("presets must not expose independent advanced compression settings")
			}
			for _, label := range []string{"JPEG / WebP 质量", "目标质量", "最大颜色数", "最低质量", "处理速度"} {
				if _, ok := tt.AccessByRole(ui.RoleSlider, label); ok {
					t.Fatalf("presets must not expose independent parameter %q", label)
				}
			}
			if slider, ok := tt.AccessByRole(ui.RoleSlider, "图片质量"); !ok || slider.Disabled {
				t.Fatalf("image quality must control compression settings: %+v", slider)
			}
			if s.compressionOptions() != options {
				t.Fatal("opening settings must preserve active compression options")
			}
		})
	}
}

func TestPNGCompressionResultIsVisible(t *testing.T) {
	for _, unchanged := range []bool{false, true} {
		name := "smaller"
		if unchanged {
			name = "unchanged"
		}
		t.Run(name, func(t *testing.T) {
			s := testSelectedApplication()
			s.file.format = "png"
			result := &compress.Result{OriginalSize: 4096, OutputSize: 1024, Unchanged: unchanged}
			output, reduction := "1.0 KiB", "减少 75.0%"
			if unchanged {
				result.OutputSize = 4096
				output, reduction = "4.0 KiB", "未减小（0.0%）"
			}
			s.completeCompression(context.Background(), result, s.file.preview, nil)
			tt := ui.NewTester(s.view, 880, 660)
			for _, text := range []string{"压缩结果", "原始大小", "4.0 KiB", "输出大小", output, reduction, "另存副本"} {
				if !tt.HasText(text) {
					t.Errorf("PNG result must display %q; got %q", text, tt.Texts())
				}
			}
			saveLabel := "覆盖原图…"
			if unchanged {
				saveLabel = "无需覆盖（未减小）"
			}
			for _, label := range []string{"重新压缩", "另存副本", saveLabel} {
				button, ok := tt.AccessByRole(ui.RoleButton, label)
				if !ok || button.Bounds.H < 20 || button.Bounds.Y+button.Bounds.H > 660 {
					t.Fatalf("%q must remain reachable at minimum window size: %+v", label, button)
				}
			}
		})
	}
}

func TestOverwriteRequiresConfirmationAndDefaultsToCancel(t *testing.T) {
	for _, key := range []ui.Key{ui.KeyEnter, ui.KeyEscape} {
		s := testSelectedApplication()
		path := filepath.Join(t.TempDir(), "photo.jpg")
		original := []byte("original source bytes")
		if err := os.WriteFile(path, original, 0o600); err != nil {
			t.Fatal(err)
		}
		s.file.path = path
		s.result = &compress.Result{OriginalSize: 4096, OutputSize: 2048}
		result := s.result
		tt := ui.NewTester(s.view, 880, 660)
		if err := tt.Click("覆盖原图…"); err != nil {
			t.Fatal(err)
		}
		dialog, ok := tt.AccessByRole(ui.RoleAlertDialog, "确认覆盖原图？")
		if !ok || dialog.Bounds.H < 50 || !s.confirmOverwrite || s.busy {
			t.Fatalf("overwrite must open a confirmation before disk I/O: %+v", dialog)
		}
		if !tt.Focused("取消") {
			t.Fatal("cancellation must be the default confirmation action")
		}
		tt.Key(0, key)
		if s.confirmOverwrite || s.result != result || s.overwritten || s.busy {
			t.Fatal("canceling must preserve result and application state")
		}
		data, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(data, original) {
			t.Fatalf("canceling confirmation changed the source: %v", err)
		}
	}
}

func TestOverwriteUnavailableWithoutSmallerCurrentResult(t *testing.T) {
	for _, state := range []string{"empty", "unchanged", "busy", "overwritten", "not-smaller"} {
		t.Run(state, func(t *testing.T) {
			s := testSelectedApplication()
			s.result = &compress.Result{OriginalSize: 4096, OutputSize: 2048}
			switch state {
			case "empty":
				s.result = nil
			case "unchanged":
				s.result.Unchanged = true
			case "busy":
				s.busy = true
			case "overwritten":
				s.overwritten = true
			case "not-smaller":
				s.result.OutputSize = s.result.OriginalSize
			}
			s.requestOverwrite()
			if s.canOverwrite() || s.confirmOverwrite {
				t.Fatalf("overwrite must not be offered in state %q", state)
			}
			tt := ui.NewTester(s.view, 880, 660)
			label := "覆盖原图…"
			if state == "unchanged" {
				label = "无需覆盖（未减小）"
			} else if state == "overwritten" {
				label = "已覆盖原图"
			}
			if button, exists := tt.AccessByRole(ui.RoleButton, label); exists && !button.Disabled {
				t.Fatalf("unavailable overwrite must be disabled: %+v", button)
			}
		})
	}
}

func TestSuccessfulOverwriteRefreshesSelectedSnapshot(t *testing.T) {
	s := testSelectedApplication()
	original := s.file
	original.data = []byte("original snapshot")
	result := &compress.Result{
		Format: "jpeg", Width: 20, Height: 12, OriginalSize: 4096,
		OutputSize: 10, Data: []byte("compressed"),
	}
	s.result = result
	preview := ui.NewBitmap(image.NewNRGBA(image.Rect(0, 0, 20, 12)))
	s.preview, s.busy, s.phase = preview, true, "正在覆盖原图"
	s.completeOverwrite(result, nil)
	if s.busy || s.phase != "" || !s.overwritten || s.result != result || s.canOverwrite() {
		t.Fatal("completed overwrite must keep comparison visible and prevent repeated writes")
	}
	if s.file.path != original.path || s.file.size != result.OutputSize ||
		!bytes.Equal(s.file.data, result.Data) || s.file.preview != preview ||
		string(original.data) != "original snapshot" {
		t.Fatal("selected snapshot must advance to the saved output without mutating the old snapshot")
	}
	s.setScheme("体积优先")
	if s.result != nil || s.preview != preview || s.overwritten {
		t.Fatal("future parameter changes must restore the new on-disk image")
	}
}

func TestFailedOverwriteRetainsSelectionAndResult(t *testing.T) {
	s := testSelectedApplication()
	result := &compress.Result{OriginalSize: 4096, OutputSize: 2048}
	file, preview := s.file, s.preview
	s.result, s.busy, s.phase = result, true, "正在覆盖原图"
	s.completeOverwrite(result, errors.New("原图已被修改，请重新选择"))
	if s.busy || s.phase != "" || s.overwritten || s.result != result ||
		s.file != file || s.preview != preview || !s.failed ||
		!strings.Contains(s.message, "覆盖失败") {
		t.Fatal("failed overwrite must retain the valid preview/result for saving a copy")
	}
}

func TestChangingParametersDismissesStaleOverwriteConfirmation(t *testing.T) {
	s := testSelectedApplication()
	s.result = &compress.Result{OriginalSize: 4096, OutputSize: 2048}
	s.requestOverwrite()
	s.setScheme("体积优先")
	if s.confirmOverwrite || s.result != nil {
		t.Fatal("an invalidated result must not retain an overwrite confirmation")
	}
}

func TestPNGSaveActionFitsMinimumWindow(t *testing.T) {
	s := testSelectedApplication()
	s.file.format = "png"
	s.result = &compress.Result{OriginalSize: 4096, OutputSize: 2048}
	s.message = "已保存：" + strings.Repeat("long-folder/", 20) + "photo-compressed.png"
	tt := ui.NewTester(s.view, 880, 660)
	button, ok := tt.AccessByRole(ui.RoleButton, "另存副本")
	if !ok || button.Bounds.H < 20 || button.Bounds.Y+button.Bounds.H > 660 {
		t.Fatalf("saving must remain reachable at minimum window size: %+v", button)
	}
}

func TestPresetsConfigureBothFormats(t *testing.T) {
	for _, test := range []struct {
		name    string
		quality float64
		png     compress.PNGOptions
	}{
		{"体积优先", 60, compress.PNGOptions{MinQuality: 40, MaxQuality: 65, Colors: 128, Speed: 3}},
		{"均衡", 80, compress.PNGOptions{MinQuality: 65, MaxQuality: 85, Colors: 256, Speed: 3}},
		{"画质优先", 92, compress.PNGOptions{MinQuality: 85, MaxQuality: 100, Colors: 256, Speed: 1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := testSelectedApplication()
			s.quality = 30
			s.result = &compress.Result{OriginalSize: 4096, OutputSize: 2048}
			s.setScheme(test.name)
			if s.scheme != test.name || s.quality != test.quality || s.png != test.png || s.result != nil {
				t.Fatalf("preset did not apply and invalidate: scheme=%s quality=%v png=%+v", s.scheme, s.quality, s.png)
			}
		})
	}
}

func TestImageQualityDraftInvalidatesOnlyAfterSaving(t *testing.T) {
	s := testSelectedApplication()
	s.file.format = "png"
	s.presets = append(s.presets, compressionPreset{name: "自定义", quality: 80, png: compress.DefaultPNGOptions()})
	s.setScheme("自定义")
	result := &compress.Result{OriginalSize: 4096, OutputSize: 2048}
	preview := ui.NewBitmap(image.NewNRGBA(image.Rect(0, 0, 20, 12)))
	s.result, s.preview = result, preview
	tt := openTestSettings(t, s)
	slider, ok := tt.AccessByRole(ui.RoleSlider, "图片质量")
	if !ok {
		t.Fatal("image quality must be adjustable")
	}
	tt.ClickAt(slider.Bounds.X+slider.Bounds.W/2, slider.Bounds.Y+slider.Bounds.H/2)
	tt.Key(0, ui.KeyHome)
	draft := s.editor.presets[s.editor.selected]
	expectedPNG := compress.PNGOptions{MinQuality: 20, MaxQuality: 40, Colors: 64, Speed: 5}
	if draft.quality != 40 || draft.png != expectedPNG {
		t.Fatalf("lowering image quality must update all draft compression parameters: %+v", draft)
	}
	if s.result != result || s.preview != preview || s.png != compress.DefaultPNGOptions() {
		t.Fatal("editing a draft must preserve the active parameters, export and preview until saving")
	}
	if err := tt.Click("保存"); err != nil {
		t.Fatal(err)
	}
	if s.settingsOpen || s.compressionOptions() != (compress.Options{Quality: 40, PNG: expectedPNG}) ||
		s.result != nil || s.preview != s.file.preview {
		t.Fatal("saving changed active parameters must discard the old export and restore the source")
	}
	main := ui.NewTester(s.view, 880, 660)
	if _, ok := main.AccessByRole(ui.RoleMenuButton, "选择预设"); ok || !main.HasText("自定义") {
		t.Fatal("editing a named scheme must keep that scheme selected")
	}
}

func TestPreviewComparisonKeepsCompressionResult(t *testing.T) {
	s := testSelectedApplication()
	result := &compress.Result{OriginalSize: 4096, OutputSize: 2048}
	compressed := ui.NewBitmap(image.NewNRGBA(image.Rect(0, 0, 20, 12)))
	s.completeCompression(context.Background(), result, compressed, nil)
	tt := ui.NewTester(s.view, 880, 660)
	for _, label := range []string{"原图", "结果", "原图"} {
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
		imageLabel := "原图预览"
		if label == "结果" {
			imageLabel = "压缩后预览"
		}
		if _, ok := tt.AccessByRole(ui.RoleImage, imageLabel); !ok {
			t.Fatalf("comparison must display %s", imageLabel)
		}
		if s.result != result || s.preview != compressed || !tt.HasText("另存副本") {
			t.Fatal("comparison must preserve the result bitmap and export")
		}
	}
	s.setScheme("体积优先")
	tt.Frame()
	if _, ok := tt.AccessByRole(ui.RoleRadioGroup, "预览模式"); ok {
		t.Fatal("invalidating a result must remove comparison")
	}
	if s.preview != s.file.preview || s.result != nil {
		t.Fatal("invalidating comparison must restore the source")
	}
	s.completeCompression(context.Background(), result, compressed, nil)
	tt.Frame()
	after, ok := tt.AccessByRole(ui.RoleRadio, "对比")
	if !ok || !after.Checked {
		t.Fatal("a new result must default to the split comparison")
	}
}

func TestComparisonUnavailableAfterOverwrite(t *testing.T) {
	s := testSelectedApplication()
	result := &compress.Result{
		OriginalSize: 4096, OutputSize: 2048, Format: "jpeg", Width: 20, Height: 12,
	}
	compressed := ui.NewBitmap(image.NewNRGBA(image.Rect(0, 0, 20, 12)))
	s.completeCompression(context.Background(), result, compressed, nil)
	tt := ui.NewTester(s.view, 880, 660)
	if err := tt.Click("原图"); err != nil {
		t.Fatal(err)
	}
	s.completeOverwrite(result, nil)
	tt.Frame()
	if _, ok := tt.AccessByRole(ui.RoleRadioGroup, "预览模式"); ok {
		t.Fatal("comparison must not present an overwritten file as the old original")
	}
	if s.file.preview != compressed || s.preview != compressed {
		t.Fatal("overwrite must retain the compressed snapshot even when viewing the original")
	}
}

func TestPresetSelectionSupportsArrowKeys(t *testing.T) {
	s := testSelectedApplication()
	tt := openTestSettings(t, s)
	if err := tt.Click("选择预设"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyHome)
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyDown)
	if s.scheme != "均衡" || s.quality != 80 {
		t.Fatal("moving through the menu must not apply a preset before confirmation")
	}
	tt.Key(0, ui.KeyEnter)
	if s.editor.presets[s.editor.active].name != "画质优先" || s.scheme != "均衡" {
		t.Fatal("Enter must select the highlighted preset as a draft")
	}
	if err := tt.Click("保存"); err != nil {
		t.Fatal(err)
	}
	if s.scheme != "画质优先" || s.quality != 92 {
		t.Fatal("arrow keys and Enter must select and apply the highlighted preset")
	}
}

func TestWindowShortcutsRespectUnavailableActions(t *testing.T) {
	for _, state := range []string{"empty", "busy", "confirmation", "settings"} {
		t.Run(state, func(t *testing.T) {
			s := newApplication()
			if state != "empty" {
				s = testSelectedApplication()
			}
			if state == "busy" {
				s.busy = true
			}
			if state == "confirmation" {
				s.result = &compress.Result{OriginalSize: 4096, OutputSize: 2048}
				s.confirmOverwrite = true
			}
			if state == "settings" {
				s.setSettingsOpen(true)
				s.result = &compress.Result{OriginalSize: 4096, OutputSize: 2048}
			}
			tt := ui.NewTester(s.view, 880, 660)
			keys := []ui.Key{ui.KeyEnter, ui.KeyS}
			if state != "empty" {
				keys = append(keys, ui.KeyO)
			}
			for _, key := range keys {
				tt.Key(ui.Cmd, key)
			}
			if s.busy != (state == "busy") || s.confirmOverwrite != (state == "confirmation") {
				t.Fatal("unavailable shortcuts must not start work or dismiss confirmation")
			}
		})
	}
}

func TestSettingsShortcutOpensWithoutChangingCompressionState(t *testing.T) {
	for _, state := range []string{"empty", "result"} {
		t.Run(state, func(t *testing.T) {
			s := newApplication()
			if state == "result" {
				s = testSelectedApplication()
				s.result = &compress.Result{OriginalSize: 4096, OutputSize: 2048}
				s.comparison.mode = previewOriginal
			}
			file, result, preview, options := s.file, s.result, s.preview, s.compressionOptions()
			tt := ui.NewTester(s.view, 880, 660)
			settings, ok := tt.AccessByRole(ui.RoleButton, "设置")
			if !ok || !strings.Contains(settings.KeyShortcuts, ",") || !tt.HasText(shortcutLabel(",")) {
				t.Fatal("settings must expose its shortcut to users and assistive technology")
			}
			tt.Key(0, ui.KeyComma)
			if s.settingsOpen {
				t.Fatal("a comma without the command modifier must not open settings")
			}
			tt.Key(ui.Cmd, ui.KeyComma)
			if !s.settingsOpen || s.editor == nil {
				t.Fatal("the settings shortcut must open the editor with or without an image")
			}
			s.addPreset()
			s.editQualityLevel(0)
			editor := s.editor
			tt.Key(ui.Cmd, ui.KeyComma)
			if s.editor != editor || editor.presets[editor.selected].quality != 40 {
				t.Fatal("repeating the shortcut must retain the open settings draft")
			}
			if s.file != file || s.result != result || s.preview != preview ||
				s.compressionOptions() != options || (s.comparison.mode == previewOriginal) != (state == "result") || s.busy {
				t.Fatal("opening settings by shortcut must preserve the image and compression state")
			}
		})
	}
}

func TestResultActionsFitBothThemesAtMinimumSize(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, format := range []string{"jpeg", "png"} {
			name := format
			if dark {
				name += "-dark"
			}
			t.Run(name, func(t *testing.T) {
				s := testSelectedApplication()
				s.file.format = format
				s.file.path = "/photos/" + strings.Repeat("很长的图片文件名", 12) + "." + format
				s.result = &compress.Result{OriginalSize: 4096, OutputSize: 2048}
				s.message = "已保存：" + strings.Repeat("very-long-folder/", 20) + "compressed." + format
				tt := ui.NewTester(func(c *ui.Context) {
					theme := ui.LightTheme()
					if dark {
						theme = ui.DarkTheme()
					}
					c.SetTheme(theme)
					s.view(c)
				}, 880, 660)
				for _, label := range []string{"更换图片", "重新压缩", "另存副本", "覆盖原图…"} {
					button, ok := tt.AccessByRole(ui.RoleButton, label)
					if !ok || button.Offscreen || button.Bounds.H < 28 ||
						button.Bounds.Y+button.Bounds.H > 660 || button.Bounds.X+button.Bounds.W > 880 {
						t.Fatalf("%s must fit in the minimum window: %+v", label, button)
					}
				}
				compare, ok := tt.AccessByRole(ui.RoleRadioGroup, "预览模式")
				if !ok || compare.Bounds.W > 200 || compare.Bounds.X+compare.Bounds.W > 880 {
					t.Fatalf("comparison must remain bounded in its dedicated mode row: %+v", compare)
				}
			})
		}
	}
}

func TestImageQualityBusyState(t *testing.T) {
	s := testSelectedApplication()
	s.file.format = "png"
	tt := openTestSettings(t, s)
	if slider, ok := tt.AccessByRole(ui.RoleSlider, "图片质量"); !ok || slider.Disabled {
		t.Fatalf("image quality must be editable before processing: %+v", slider)
	}
	s.busy = true
	tt.Frame()
	if slider, ok := tt.AccessByRole(ui.RoleSlider, "图片质量"); !ok || !slider.Disabled {
		t.Fatalf("image quality must be disabled while processing: %+v", slider)
	}
}

func TestQualityKeyboardFocusKeepsSettingsActionsVisible(t *testing.T) {
	s := testSelectedApplication()
	s.file.format = "png"
	s.presets = append(s.presets, compressionPreset{name: "自定义", quality: 80, png: compress.DefaultPNGOptions()})
	s.setScheme("自定义")
	result := &compress.Result{OriginalSize: 4096, OutputSize: 2048}
	s.result = result
	tt := openTestSettings(t, s)
	for i := 0; i < 40 && !tt.Focused("图片质量"); i++ {
		tt.Key(0, ui.KeyTab)
	}
	slider, ok := tt.AccessByRole(ui.RoleSlider, "图片质量")
	if !ok || !slider.Focused || slider.Offscreen || slider.Bounds.H < 10 {
		t.Fatalf("keyboard focus must reach image quality: %+v", slider)
	}
	save, ok := tt.AccessByRole(ui.RoleButton, "保存")
	if !ok || save.Offscreen || save.Bounds.H < 28 || save.Bounds.Y+save.Bounds.H > 600 {
		t.Fatalf("editing image quality must keep saving reachable: %+v", save)
	}
	tt.Key(0, ui.KeyRight)
	expectedPNG := compress.PNGOptions{MinQuality: 85, MaxQuality: 100, Colors: 256, Speed: 1}
	draft := s.editor.presets[s.editor.selected]
	if draft.quality != 92 || draft.png != expectedPNG || s.quality != 80 || s.png.Speed != 3 || s.result != result {
		t.Fatal("image quality must update all draft parameters before saving")
	}
	if err := tt.Click("保存"); err != nil {
		t.Fatal(err)
	}
	main := ui.NewTester(s.view, 880, 660)
	if s.settingsOpen || s.quality != 92 || s.png != expectedPNG || s.result != nil || !main.HasText("开始压缩") {
		t.Fatal("saving changed settings must close the dialog and require recompression")
	}
}

func TestMainPresetsOnlyEditThroughDialog(t *testing.T) {
	for _, format := range []string{"jpeg", "png"} {
		t.Run(format, func(t *testing.T) {
			s := testSelectedApplication()
			s.file.format = format
			s.settingsFile = filepath.Join(t.TempDir(), "settings.json")
			result := &compress.Result{OriginalSize: 4096, OutputSize: 2048}
			s.result = result
			preset := s.presets[1]
			tt := ui.NewTester(s.view, 880, 660)
			if _, ok := tt.AccessByRole(ui.RoleMenuButton, "选择预设"); ok {
				t.Fatal("preset selection must only be available in settings")
			}
			if _, ok := tt.AccessByRole(ui.RoleSlider, "图片质量"); ok {
				t.Fatal("quality must only be editable in the preset dialog")
			}
			if _, ok := tt.AccessByRole(ui.RoleRadioGroup, "保存到"); ok {
				t.Fatal("output preferences must only be editable in the preset dialog")
			}
			if _, ok := tt.AccessByRole(ui.RoleButton, "新增预设"); ok {
				t.Fatal("presets must only be created in the preset dialog")
			}
			if err := tt.Click("设置"); err != nil {
				t.Fatal(err)
			}
			settings := ui.NewTester(s.settingsView, 724, 700)
			if err := settings.Click("管理预设"); err != nil {
				t.Fatal(err)
			}
			slider, ok := settings.AccessByRole(ui.RoleSlider, "图片质量")
			if !s.settingsOpen || !ok || slider.Disabled || slider.Offscreen {
				t.Fatalf("the edit entry must open an editable preset dialog: %+v", slider)
			}
			settings.ClickAt(slider.Bounds.X+slider.Bounds.W/2, slider.Bounds.Y+slider.Bounds.H/2)
			settings.Key(0, ui.KeyHome)
			if err := settings.Click("每次询问"); err != nil {
				t.Fatal(err)
			}
			draft := s.editor.selectedPreset()
			if draft.quality != 40 || draft.output.Mode != presetSaveAsk {
				t.Fatalf("the dialog must edit the selected draft: %+v", draft)
			}
			if s.presets[1] != preset || s.quality != preset.quality || s.png != preset.png || s.output != preset.output || s.result != result {
				t.Fatal("dialog edits must leave the active preset and result unchanged until saved")
			}
			if err := settings.Click("保存"); err != nil {
				t.Fatal(err)
			}
			if s.settingsOpen || s.quality != 40 || s.output.Mode != presetSaveAsk || s.result != nil {
				t.Fatal("saving the dialog must apply the preset and invalidate the old compression result")
			}
		})
	}
}

func TestSettingsCanBeEditedBeforeChoosingImage(t *testing.T) {
	s := newApplication()
	s.presets = testLegacyPresets()
	chooseTestPreset(t, s, "画质优先")
	tt := ui.NewTester(s.view, 880, 660)
	if s.scheme != "画质优先" || s.quality != 92 {
		t.Fatal("scheme selection must work before choosing an image")
	}
	if err := tt.Click("设置"); err != nil {
		t.Fatal(err)
	}
	s.settingsFile = filepath.Join(t.TempDir(), "settings.json")
	settings := ui.NewTester(s.settingsView, 760, 560)
	if err := settings.Click("预设"); err != nil {
		t.Fatal(err)
	}
	if err := settings.Click("新增预设"); err != nil {
		t.Fatal(err)
	}
	slider, ok := settings.AccessByRole(ui.RoleSlider, "图片质量")
	if !ok || slider.Disabled {
		t.Fatalf("image quality must be editable without a selected image: %+v", slider)
	}
	settings.ClickAt(slider.Bounds.X+slider.Bounds.W/2, slider.Bounds.Y+slider.Bounds.H/2)
	settings.Key(0, ui.KeyEnd)
	if s.editor.presets[s.editor.selected].quality != 100 || s.quality != 92 {
		t.Fatal("editing settings without an image must update only the draft")
	}
	if err := settings.Click("保存"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if s.settingsOpen || s.scheme != "画质优先" || s.quality != 92 || !tt.HasText("选择图片") {
		t.Fatal("saving a new scheme must keep the active scheme until selected and return to the unchanged main view")
	}
	chooseTestPreset(t, s, "新预设")
	tt.Frame()
	if s.quality != 100 || s.scheme != "新预设" {
		t.Fatal("saving settings must preserve the named scheme and return to the unchanged main view")
	}
	button, ok := tt.AccessByRole(ui.RoleButton, "开始压缩")
	if !ok || !button.Disabled {
		t.Fatal("editing settings must not enable compression without an image")
	}
}

func TestSettingsDialogPreservesCompressionStateWithoutEdits(t *testing.T) {
	for _, exit := range []string{"取消", "保存", "Escape"} {
		t.Run(exit, func(t *testing.T) {
			s := testSelectedApplication()
			result := &compress.Result{OriginalSize: 4096, OutputSize: 2048}
			preview := ui.NewBitmap(image.NewNRGBA(image.Rect(0, 0, 20, 12)))
			s.completeCompression(context.Background(), result, preview, nil)
			s.comparison.mode = previewOriginal
			file, options, message := s.file, s.compressionOptions(), s.message
			tt := ui.NewTester(s.view, 880, 660)
			if err := tt.Click("设置"); err != nil {
				t.Fatal(err)
			}
			if !s.settingsOpen || !tt.HasText("图片预览") || !tt.HasText("另存副本") {
				t.Fatal("opening a separate settings window must keep the main view intact")
			}
			s.settingsFile = filepath.Join(t.TempDir(), "settings.json")
			settings := ui.NewTester(s.settingsView, 760, 560)
			if _, ok := settings.AccessByRole(ui.RoleDialog, "应用设置"); !ok {
				t.Fatal("settings content must identify itself as a dialog")
			}
			if exit == "Escape" {
				settings.Key(0, ui.KeyEscape)
			} else if err := settings.Click(exit); err != nil {
				t.Fatal(err)
			}
			if s.settingsOpen || s.file != file || s.result != result || s.preview != preview ||
				s.comparison.mode != previewOriginal || s.compressionOptions() != options || s.message != message {
				t.Fatal("opening and closing settings without edits must preserve image, result, preview, settings and message")
			}
			tt.Frame()
			save, ok := tt.AccessByRole(ui.RoleButton, "另存副本")
			if !ok || save.Disabled || save.Offscreen || save.Bounds.Y+save.Bounds.H > 660 {
				t.Fatalf("returning to the main page must keep saving reachable: %+v", save)
			}
		})
	}
}

func TestSettingsNavigationRespectsBusyAndConfirmation(t *testing.T) {
	for _, state := range []string{"busy", "confirmation"} {
		t.Run(state, func(t *testing.T) {
			s := testSelectedApplication()
			s.busy = state == "busy"
			s.confirmOverwrite = state == "confirmation"
			if s.confirmOverwrite {
				s.result = &compress.Result{OriginalSize: 4096, OutputSize: 2048}
			}
			tt := ui.NewTester(s.view, 880, 660)
			tt.Key(ui.Cmd, ui.KeyComma)
			if s.settingsOpen || s.editor != nil {
				t.Fatal("the settings shortcut must not open during work or overwrite confirmation")
			}
			s.setSettingsOpen(true)
			if s.settingsOpen {
				t.Fatal("settings must not open during work or overwrite confirmation")
			}
		})
	}
}

func TestFileDropWhileSettingsAreOpenPreservesDraft(t *testing.T) {
	s := testSelectedApplication()
	result := &compress.Result{OriginalSize: 4096, OutputSize: 2048}
	s.result = result
	s.setSettingsOpen(true)
	editor := s.editor
	s.addPreset()
	s.editQualityLevel(2)
	s.dropFiles([]string{"/photos/first.jpg", "/photos/second.png"})
	tt := ui.NewTester(s.view, 880, 660)
	if !s.settingsOpen || s.editor != editor || s.editor.presets[s.editor.selected].quality != 70 ||
		s.failed || s.message != "" {
		t.Fatal("file drops while a modal settings editor is open must not close it or discard its draft")
	}
	if s.result != result || !tt.HasText("另存副本") {
		t.Fatal("an invalid drop must preserve the selected image and valid compression result")
	}
}

func TestFileDropPreservesBusyAndConfirmationState(t *testing.T) {
	for _, state := range []string{"busy", "confirmation"} {
		t.Run(state, func(t *testing.T) {
			s := testSelectedApplication()
			s.setSettingsOpen(true)
			s.busy, s.confirmOverwrite = state == "busy", state == "confirmation"
			s.dropFiles([]string{"/photos/first.jpg", "/photos/second.png"})
			if !s.settingsOpen || s.message != "" || s.failed {
				t.Fatal("a blocked file drop must not navigate or change status")
			}
		})
	}
}

func TestSettingsFitBothThemesAtMinimumSize(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(map[bool]string{false: "light", true: "dark"}[dark], func(t *testing.T) {
			s := newApplication()
			s.openPresetSettings()
			tt := ui.NewTester(func(c *ui.Context) {
				theme := ui.LightTheme()
				if dark {
					theme = ui.DarkTheme()
				}
				c.SetTheme(theme)
				s.settingsView(c)
			}, 760, 560)
			for _, label := range []string{"取消", "保存", "新增预设", "复制预设", "删除预设"} {
				button, ok := tt.AccessByRole(ui.RoleButton, label)
				if !ok || button.Disabled || button.Bounds.H < 28 || button.Offscreen ||
					button.Bounds.Y+button.Bounds.H > 560 || button.Bounds.X+button.Bounds.W > 760 {
					t.Fatalf("%s must fit in the minimum window: %+v", label, button)
				}
			}
			slider, ok := tt.AccessByRole(ui.RoleSlider, "图片质量")
			if !ok || slider.Disabled || slider.Bounds.W < 100 || slider.Bounds.X < 20 ||
				slider.Bounds.X+slider.Bounds.W > 740 {
				t.Fatalf("image quality must fit the settings content width: %+v", slider)
			}
			s.addPreset()
			tt.Frame()
			for _, label := range []string{"取消", "保存", "新增预设", "复制预设", "删除预设"} {
				button, ok := tt.AccessByRole(ui.RoleButton, label)
				if !ok || button.Disabled || button.Bounds.H < 28 || button.Offscreen ||
					button.Bounds.Y+button.Bounds.H > 560 || button.Bounds.X+button.Bounds.W > 760 {
					t.Fatalf("%s must fit and be enabled for custom schemes: %+v", label, button)
				}
			}
			slider, ok = tt.AccessByRole(ui.RoleSlider, "图片质量")
			if !ok || slider.Disabled || slider.Bounds.W < 100 || slider.Bounds.X < 20 ||
				slider.Bounds.X+slider.Bounds.W > 740 {
				t.Fatalf("image quality must fit and be enabled for custom schemes: %+v", slider)
			}
		})
	}
}

func TestSettingsNoOpAndBusyKeepResult(t *testing.T) {
	for _, busy := range []bool{false, true} {
		s := testSelectedApplication()
		s.presets = append(s.presets, compressionPreset{name: "自定义", quality: 80, png: compress.DefaultPNGOptions()})
		s.setScheme("自定义")
		s.setSettingsOpen(true)
		draft := s.editor.presets[s.editor.selected]
		s.busy = busy
		result := &compress.Result{OriginalSize: 4096, OutputSize: 2048}
		s.result = result
		original := s.compressionOptions()
		if busy {
			s.setScheme("体积优先")
			s.editQualityLevel(0)
		} else {
			s.editQualityLevel(3)
			s.editQualityLevel(-1)
			s.editQualityLevel(len(imageQualityLevels))
			s.setScheme("unknown")
		}
		if s.result != result || s.compressionOptions() != original || s.editor.presets[s.editor.selected] != draft {
			t.Fatalf("no-op, invalid, or busy edits changed draft/options/result (busy=%v)", busy)
		}
	}
}

func TestCompressionSettingsSnapshot(t *testing.T) {
	s := testSelectedApplication()
	s.presets = append(s.presets, compressionPreset{name: "自定义", quality: 60, png: compress.DefaultPNGOptions()})
	captured := s.compressionOptions()
	s.setScheme("自定义")
	if captured.Quality != 80 || captured.PNG != compress.DefaultPNGOptions() {
		t.Fatal("captured options must not change with subsequent UI edits")
	}
	s.result = &compress.Result{OriginalSize: 4096, OutputSize: 2048}
	openTestSettings(t, s)
	s.editQualityLevel(0)
	if s.quality != 60 || s.png != compress.DefaultPNGOptions() || s.result == nil {
		t.Fatal("draft changes must not change captured options or valid results")
	}
	s.saveSettingsEdits()
	if s.result != nil || s.quality != 40 || s.png != imageQualityLevels[0].png || s.scheme != "自定义" {
		t.Fatal("saving image quality must update all named scheme parameters and invalidate the old result")
	}
}

func TestSaveActionFitsMinimumWindow(t *testing.T) {
	s := testSelectedApplication()
	s.result = &compress.Result{OriginalSize: 4096, OutputSize: 2048}
	s.message = "已保存：/Users/example/" + strings.Repeat("very-long-folder/", 10) + "photo-compressed.jpg"
	tt := ui.NewTester(s.view, 880, 660)
	button, ok := tt.AccessByRole(ui.RoleButton, "另存副本")
	if !ok || button.Bounds.H < 20 || button.Bounds.Y+button.Bounds.H > 660 {
		t.Fatalf("saving must remain reachable at the minimum window size: %+v", button)
	}
}

func TestRevealSavedFileLifecycleAndMinimumWindow(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "photo.png")
	savedPath := filepath.Join(dir, strings.Repeat("nested-dir-", 4)+"photo-compressed.png")
	if err := os.WriteFile(srcPath, []byte("src"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(savedPath, []byte("out"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, dark := range []bool{false, true} {
		name := "light"
		if dark {
			name = "dark"
		}
		t.Run(name, func(t *testing.T) {
			var revealed []string
			s := newApplication()
			s.dispatchUpdate = func(fn func()) { fn() }
			s.showInFolder = func(path string) { revealed = append(revealed, path) }
			preview := ui.NewBitmap(image.NewNRGBA(image.Rect(0, 0, 20, 12)))
			s.file = &selectedImage{
				path: srcPath, format: "png", width: 20, height: 12,
				size: 4096, preview: preview,
			}
			s.preview = preview
			s.result = &compress.Result{
				SourcePath: srcPath, Format: "png", Width: 20, Height: 12,
				OriginalSize: 4096, OutputSize: 2048, Data: []byte("out"),
			}
			s.lastSavedPath = savedPath
			s.setMessage("已保存："+savedPath, false)

			tt := ui.NewTester(func(c *ui.Context) {
				theme := ui.LightTheme()
				if dark {
					theme = ui.DarkTheme()
				}
				c.SetTheme(theme)
				s.view(c)
			}, 880, 660)

			for _, label := range []string{"定位文件", "另存副本", "覆盖原图…"} {
				button, ok := tt.AccessByRole(ui.RoleButton, label)
				if !ok || button.Offscreen || button.Bounds.H < 24 ||
					button.Bounds.X+button.Bounds.W > 880 || button.Bounds.Y+button.Bounds.H > 660 {
					t.Fatalf("%q must fit inside minimum window: %+v", label, button)
				}
			}

			if err := tt.Click("定位文件"); err != nil {
				t.Fatal(err)
			}
			if len(revealed) != 1 || revealed[0] != savedPath {
				t.Fatalf("clicking 定位文件 must reveal savedPath %q, got %v", savedPath, revealed)
			}

			s.settingsOpen = true
			s.revealFileInFolder(savedPath)
			if len(revealed) != 1 {
				t.Fatalf("modal guard must prevent revealFileInFolder while settings open: %v", revealed)
			}
			s.settingsOpen = false
		})
	}

	var revealed []string
	s := newApplication()
	s.dispatchUpdate = func(fn func()) { fn() }
	s.showInFolder = func(path string) { revealed = append(revealed, path) }
	preview := ui.NewBitmap(image.NewNRGBA(image.Rect(0, 0, 20, 12)))
	s.file = &selectedImage{path: srcPath, format: "png", width: 20, height: 12, size: 4096, preview: preview}
	s.preview = preview
	s.result = &compress.Result{
		SourcePath: srcPath, Format: "png", Width: 20, Height: 12,
		OriginalSize: 4096, OutputSize: 2048, Data: []byte("out"),
	}

	s.writeCopy(s.result, filepath.Join(dir, "copy.png"))
	s.saves.Wait()
	if s.lastSavedPath != filepath.Join(dir, "copy.png") || s.failed {
		t.Fatalf("writeCopy must track lastSavedPath cleanly: last=%q failed=%v msg=%q", s.lastSavedPath, s.failed, s.message)
	}

	if err := os.Remove(s.lastSavedPath); err != nil {
		t.Fatal(err)
	}
	s.revealFileInFolder(s.lastSavedPath)
	if !s.failed || !strings.Contains(s.message, "找不到输出文件") || len(revealed) != 0 {
		t.Fatalf("missing saved file must report error instead of revealing: failed=%v msg=%q revealed=%v",
			s.failed, s.message, revealed)
	}

	s.completeOverwrite(s.result, nil)
	if s.lastSavedPath != srcPath || s.failed {
		t.Fatalf("completeOverwrite must track source path for reveal: last=%q failed=%v", s.lastSavedPath, s.failed)
	}
	s.revealFileInFolder(s.lastSavedPath)
	if len(revealed) != 1 || revealed[0] != srcPath {
		t.Fatalf("reveal after overwrite must target source path %q, got %v", srcPath, revealed)
	}

	s.invalidateResult()
	if s.lastSavedPath != "" {
		t.Fatalf("invalidateResult must clear lastSavedPath, got %q", s.lastSavedPath)
	}
	s.lastSavedPath = srcPath
	s.completeSelection(context.Background(), &selectedImage{path: srcPath, preview: preview}, nil)
	if s.lastSavedPath != "" {
		t.Fatalf("completeSelection must clear lastSavedPath, got %q", s.lastSavedPath)
	}
}

func TestReadSelectionCanceledBeforeOpening(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readSelection(ctx, "/does/not/exist"); err != context.Canceled {
		t.Fatalf("canceled work should stop before file I/O: %v", err)
	}
}

func TestCanceledSelectionDoesNotReplaceCurrentImage(t *testing.T) {
	s := testSelectedApplication()
	original := s.file
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.busy, s.canceling = true, true
	s.completeSelection(ctx, &selectedImage{path: "new.jpg"}, nil)
	if s.file != original || s.busy || s.failed || s.message != "操作已取消。" {
		t.Fatal("a completed load canceled before delivery must preserve the current image")
	}
}

func TestCanceledCompressionDoesNotPublishCompletedResult(t *testing.T) {
	s := testSelectedApplication()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.busy, s.canceling = true, true
	s.completeCompression(ctx, &compress.Result{OriginalSize: 4096, OutputSize: 2048}, nil, nil)
	if s.result != nil || s.preview != s.file.preview || s.busy || s.failed {
		t.Fatal("cancellation before delivery must discard a completed compression result")
	}
}

func TestReadSelectionRejectsNonFile(t *testing.T) {
	if _, err := readSelection(context.Background(), t.TempDir()); err == nil {
		t.Fatal("a directory must be rejected before attempting to read image data")
	}
}

func TestReadSelectionRejectsAnimatedPNGAndPreservesSelection(t *testing.T) {
	apngPath := filepath.Join(t.TempDir(), "anim.png")
	writeTestAnimatedPNG(t, apngPath)

	if _, err := readSelection(context.Background(), apngPath); err == nil || !strings.Contains(err.Error(), "动画 PNG") {
		t.Fatalf("expected animated PNG rejection in readSelection, got %v", err)
	}

	s := testSelectedApplication()
	existing := s.file
	_, err := readSelection(context.Background(), apngPath)
	s.completeSelection(context.Background(), nil, err)
	if s.file != existing || !s.failed || !strings.Contains(s.message, "动画 PNG") {
		t.Fatalf("rejecting animated PNG must preserve existing selection and report error: file=%v failed=%v msg=%q",
			s.file, s.failed, s.message)
	}
}

func TestSuggestedPathAvoidsExistingCopies(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "photo.jpg")
	existing := filepath.Join(dir, "photo-compressed.jpg")
	if err := os.WriteFile(existing, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	if path := suggestedPath(source, ".jpg"); path != filepath.Join(dir, "photo-compressed-2.jpg") {
		t.Fatalf("suggested path would overwrite an existing copy: %q", path)
	}
}

func TestSuggestedPathBoundsCollisionSearch(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "photo.jpg")
	base := filepath.Join(dir, "photo-compressed")
	for number := 1; number <= maxSuggestedPathChecks+1; number++ {
		path := base + ".jpg"
		if number > 1 {
			path = fmt.Sprintf("%s-%d.jpg", base, number)
		}
		if err := os.WriteFile(path, []byte("existing"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// The final suggestion is deliberately occupied. Returning the next free
	// name would mean the filesystem search continued beyond its budget.
	path := suggestedPath(source, ".jpg")
	want := fmt.Sprintf("%s-%d.jpg", base, maxSuggestedPathChecks+1)
	if path != want {
		t.Fatalf("unbounded collision search: got %q, want %q", path, want)
	}
	result := &compress.Result{SourcePath: source, Data: []byte("new")}
	if err := result.Save(path); err == nil {
		t.Fatal("an advisory suggestion bypassed exclusive saving")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "existing" {
		t.Fatal("suggestion collision changed an existing file")
	}
}

func TestUnchangedPreviewReusesSourceBitmap(test *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := range 16 {
		for x := range 16 {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x * 17), G: uint8(y * 17), B: uint8((x ^ y) * 16), A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, img, &jpeg.Options{Quality: 10}); err != nil {
		test.Fatal(err)
	}
	dir := test.TempDir()
	sourcePath := filepath.Join(dir, "tiny.jpg")
	savedPath := filepath.Join(dir, "tiny-compressed.jpg")
	minimal, err := compress.Bytes(context.Background(), encoded.Bytes(), sourcePath, compress.Options{Quality: 10})
	if err != nil {
		test.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, minimal.Data, 0o600); err != nil {
		test.Fatal(err)
	}
	if err := os.WriteFile(savedPath, minimal.Data, 0o600); err != nil {
		test.Fatal(err)
	}

	selected, err := readSelection(context.Background(), sourcePath)
	if err != nil {
		test.Fatal(err)
	}
	appState := newApplication()
	updates := make(chan func(), 2)
	appState.dispatchUpdate = func(callback func()) {
		updates <- callback
	}
	appState.file = selected
	appState.preview = selected.preview
	appState.quality = 98

	appState.startCompression()
	callback := <-updates
	callback()
	if appState.result == nil || !appState.result.Unchanged {
		test.Fatalf("expected unchanged compression result, got %+v", appState.result)
	}
	if appState.preview != selected.preview {
		test.Fatal("unchanged single-image compression must reuse source preview bitmap")
	}

	appState.batch = newBatchState([]batchItem{{
		path:         sourcePath,
		absPath:      sourcePath,
		relPath:      "tiny.jpg",
		format:       "jpeg",
		width:        16,
		height:       16,
		originalSize: int64(len(minimal.Data)),
		outputSize:   int64(len(minimal.Data)),
		unchanged:    true,
		savedPath:    savedPath,
		status:       batchDone,
	}})
	appState.openBatchPreview(0)
	batchCallback := <-updates
	batchCallback()
	if !appState.batch.previewOpen || appState.preview != appState.file.preview {
		test.Fatal("unchanged batch preview must reuse loaded source preview bitmap")
	}
}

func TestHiddenTitleBarKeepsToolbarClearOfWindowControls(t *testing.T) {
	cases := []struct {
		name  string
		bar   ui.TitleBar
		width int
	}{
		{name: "macos-default", bar: ui.TitleBar{Height: 52, Left: 84, Bottom: 33}, width: 980},
		{name: "macos-min-width", bar: ui.TitleBar{Height: 52, Left: 84, Bottom: 33}, width: 760},
		{name: "windows-min-width", bar: ui.TitleBar{Height: 52, Right: 138, Bottom: 52}, width: 760},
		{name: "fullscreen", bar: ui.TitleBar{}, width: 980},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := testSelectedApplication()
			s.floatingOpen = true
			s.batch = newBatchState([]batchItem{
				{path: "/photos/pending.jpg", absPath: "/photos/pending.jpg", relPath: "pending.jpg", format: "jpeg", status: batchPending},
				{path: "/photos/done.jpg", absPath: "/photos/done.jpg", relPath: "done.jpg", format: "jpeg", status: batchDone, originalSize: 4096, outputSize: 2048},
			})
			tt := ui.NewTester(s.view, tc.width, 660)
			tt.SetTitleBar(tc.bar)
			toolbar, ok := tt.AccessByRole(ui.RoleToolbar, "操作栏")
			if !ok || toolbar.Bounds.Y != 0 || toolbar.Bounds.H < max(tc.bar.Height, mainTitleBarHeight) {
				t.Fatalf("toolbar must span the hidden title bar band: %+v", toolbar)
			}
			var items []ui.AccessInfo
			for _, item := range tt.AccessAll() {
				if item.Name == "Z Pic" {
					items = append(items, item)
				}
			}
			for _, label := range []string{"重新压缩", "继续压缩", "粘贴图片", "收起悬浮窗", "设置"} {
				role := ui.RoleButton
				control, found := tt.AccessByRole(role, label)
				if !found {
					t.Fatalf("missing %s in toolbar", label)
				}
				items = append(items, control)
			}
			rightLimit := float32(tc.width) - tc.bar.Right
			for index, item := range items {
				b := item.Bounds
				if item.Offscreen || b.X < tc.bar.Left || b.X+b.W > rightLimit || b.Y < 0 || b.Y+b.H > toolbar.Bounds.H {
					t.Fatalf("%s (%+v) must stay clear of window controls %+v in %dpx width", item.Name, b, tc.bar, tc.width)
				}
				for _, other := range items[:index] {
					o := other.Bounds
					if b.X < o.X+o.W && o.X < b.X+b.W && b.Y < o.Y+o.H && o.Y < b.Y+b.H {
						t.Fatalf("toolbar elements overlap: %+v and %+v", item, other)
					}
				}
			}
		})
	}
}

func TestMainWindowBusyDropsQueueWhileDialogsBlockAndDisableActions(t *testing.T) {
	root := t.TempDir()
	selectedPath := filepath.Join(root, "selected.jpg")
	droppedPath := filepath.Join(root, "dropped.jpg")
	savedPath := filepath.Join(root, "saved.jpg")
	writeTestJPEG(t, selectedPath, 80, 60, 95)
	writeTestJPEG(t, droppedPath, 100, 80, 95)
	writeTestJPEG(t, savedPath, 80, 60, 75)

	selected, err := readSelection(context.Background(), selectedPath)
	if err != nil {
		t.Fatal(err)
	}
	updates := make(chan func(), 16)
	app := newApplication()
	app.dispatchUpdate = func(update func()) { updates <- update }
	app.downloadDir = filepath.Join(root, "downloads")
	app.completeSelection(context.Background(), selected, nil)
	app.result = &compress.Result{SourcePath: selectedPath, Format: "jpeg", Width: 80, Height: 60, OriginalSize: 4096, OutputSize: 2048, Data: []byte("out")}
	app.lastSavedPath = savedPath
	app.setMessage("已保存："+savedPath, false)

	for _, guard := range []string{"settings", "overwrite", "batch-overwrite"} {
		app.settingsOpen = guard == "settings"
		app.confirmOverwrite = guard == "overwrite"
		app.confirmBatchOverwrite = guard == "batch-overwrite"
		app.dropFiles([]string{droppedPath})
		if len(app.dropQueue) != 0 || app.batch != nil || app.file != selected {
			t.Fatalf("%s dialog must ignore main-window file drops without queueing", guard)
		}
		if guard == "settings" {
			tt := ui.NewTester(app.view, 880, 660)
			for _, label := range []string{"更换图片", "定位文件", "另存副本"} {
				btn, ok := tt.AccessByRole(ui.RoleButton, label)
				if !ok || !btn.Disabled {
					t.Fatalf("%q must be disabled while settings dialog is open: %+v", label, btn)
				}
			}
		} else if guard == "overwrite" {
			tt := ui.NewTester(app.view, 880, 660)
			if _, ok := tt.AccessByRole(ui.RoleAlertDialog, "确认覆盖原图？"); !ok {
				t.Fatal("overwrite dialog must remain open after ignored file drop")
			}
		}
	}
	app.settingsOpen, app.confirmOverwrite, app.confirmBatchOverwrite = false, false, false

	app.busy, app.phase = true, "正在压缩图片"
	app.dropFiles([]string{droppedPath})
	if len(app.dropQueue) != 1 || app.dropImporting || app.file != selected || app.batch != nil {
		t.Fatalf("busy main-window drop must queue without discarding active selection: queue=%d batch=%+v", len(app.dropQueue), app.batch)
	}
	tt := ui.NewTester(app.view, 880, 660)
	if !tt.HasText("已暂存 1 次拖入，当前操作结束后入队") {
		t.Fatal("main window must surface pending drop count during busy work")
	}

	app.busy, app.phase = false, ""
	app.startNextDropImport()
	waitForDropImports(t, app, updates)
	if app.batch == nil || len(app.batch.items) != 2 ||
		app.batch.items[0].path != selectedPath || app.batch.items[1].path != droppedPath {
		t.Fatalf("queued main-window drop must promote both sources into batch queue: %+v", app.batch)
	}
}
