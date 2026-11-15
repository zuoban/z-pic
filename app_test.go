package main

import (
	"bytes"
	"context"
	"errors"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"zpic/internal/compress"
)

func testSelectedApplication() *application {
	s := newApplication()
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
	tt := ui.NewTester(s.view, 980, 720)
	button, ok := tt.AccessByRole(ui.RoleButton, "开始压缩")
	if !ok || !button.Disabled {
		t.Fatalf("compression must be disabled without an image: %+v", button)
	}
	if !tt.HasText("选择图片") || !tt.HasText("选择文件夹") || !tt.HasText("选择或拖入图片") {
		t.Fatalf("the empty view must explain how to start: %q", tt.Texts())
	}
}

func TestChangingQualityInvalidatesSavedResult(t *testing.T) {
	s := testSelectedApplication()
	s.result = &compress.Result{OriginalSize: 4096, OutputSize: 2048}
	tt := ui.NewTester(s.view, 980, 720)
	if err := tt.Click("体积优先"); err != nil {
		t.Fatal(err)
	}
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
	for _, preset := range compressionPresets {
		scheme, ok := tt.AccessByRole(ui.RoleRadio, preset.name)
		if !ok || !scheme.Disabled {
			t.Fatalf("scheme must be disabled while processing: %+v", scheme)
		}
	}
	settings, ok := tt.AccessByRole(ui.RoleButton, "设置")
	if !ok || !settings.Disabled {
		t.Fatalf("settings navigation must be disabled while processing: %+v", settings)
	}
}

func TestPNGViewExplainsLossyCompressionWithoutModeSelection(t *testing.T) {
	s := testSelectedApplication()
	s.file.format = "png"
	tt := openTestSettings(t, s)
	if !tt.HasText("PNG 有损压缩") ||
		!tt.HasText("质量与颜色数越低，文件通常越小。支持透明度，但渐变、阴影与半透明边缘可能变化。") {
		t.Fatal("PNG must explain its default lossy compression")
	}
	if tt.HasText("无损优化") || tt.HasText("PNG 压缩模式") || tt.HasText("更小体积（有损）") {
		t.Fatal("PNG must not offer the removed mode selection")
	}
	for _, label := range []string{"JPEG 质量", "目标质量", "最大颜色数"} {
		if _, ok := tt.AccessByRole(ui.RoleSlider, label); !ok {
			t.Fatalf("settings must expose both formats independently of the selected image: %s", label)
		}
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
			for _, label := range []string{"重新压缩", "另存副本", "覆盖原图"} {
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
		if err := tt.Click("覆盖原图"); err != nil {
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
			label := "覆盖原图"
			if s.overwritten {
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
	s.advanced = true
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

func TestPNGControlsClampDraftAndInvalidateOnlyAfterSaving(t *testing.T) {
	s := testSelectedApplication()
	s.file.format = "png"
	result := &compress.Result{OriginalSize: 4096, OutputSize: 2048}
	preview := ui.NewBitmap(image.NewNRGBA(image.Rect(0, 0, 20, 12)))
	s.result, s.preview = result, preview
	tt := openTestSettings(t, s)
	slider, ok := tt.AccessByRole(ui.RoleSlider, "目标质量")
	if !ok {
		t.Fatal("PNG target quality must be adjustable")
	}
	tt.ClickAt(slider.Bounds.X+slider.Bounds.W/2, slider.Bounds.Y+slider.Bounds.H/2)
	tt.Key(0, ui.KeyHome)
	draft := s.editor.presets[s.editor.selected]
	if draft.png.MaxQuality != 1 || draft.png.MinQuality != 1 {
		t.Fatalf("lowering target must clamp the draft minimum: %+v", draft.png)
	}
	if s.result != result || s.preview != preview || s.png != compress.DefaultPNGOptions() {
		t.Fatal("editing a draft must preserve the active parameters, export and preview until saving")
	}
	if err := tt.Click("保存"); err != nil {
		t.Fatal(err)
	}
	if s.settingsOpen || s.png.MaxQuality != 1 || s.png.MinQuality != 1 ||
		s.result != nil || s.preview != s.file.preview {
		t.Fatal("saving changed active parameters must discard the old export and restore the source")
	}
	main := ui.NewTester(s.view, 880, 660)
	scheme, ok := main.AccessByRole(ui.RoleRadio, "均衡")
	if !ok || !scheme.Checked {
		t.Fatalf("editing a named scheme must keep that scheme selected: %+v", scheme)
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
	tt := ui.NewTester(s.view, 880, 660)
	if err := tt.Click("均衡"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyRight)
	if s.scheme != "画质优先" || s.quality != 92 {
		t.Fatal("arrow keys must select and apply the adjacent preset")
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
			s.editQuality(35)
			editor := s.editor
			tt.Key(ui.Cmd, ui.KeyComma)
			if s.editor != editor || editor.presets[editor.selected].quality != 35 {
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
				s.file.format, s.advanced = format, true
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
				for _, label := range []string{"更换图片", "重新压缩", "另存副本", "覆盖原图"} {
					button, ok := tt.AccessByRole(ui.RoleButton, label)
					if !ok || button.Offscreen || button.Bounds.H < 28 ||
						button.Bounds.Y+button.Bounds.H > 660 || button.Bounds.X+button.Bounds.W > 880 {
						t.Fatalf("%s must fit in the minimum window: %+v", label, button)
					}
				}
				compare, ok := tt.AccessByRole(ui.RoleRadioGroup, "预览模式")
				if !ok || compare.Bounds.W > 200 || compare.Bounds.X+compare.Bounds.W > 880 {
					t.Fatalf("comparison must share the toolbar with replace: %+v", compare)
				}
			})
		}
	}
}

func TestPNGAdvancedParametersAndBusyState(t *testing.T) {
	s := testSelectedApplication()
	s.file.format, s.advanced = "png", true
	tt := openTestSettings(t, s)
	for _, label := range []string{"目标质量", "最大颜色数", "最低质量", "处理速度"} {
		if _, ok := tt.AccessByRole(ui.RoleSlider, label); !ok {
			t.Errorf("missing PNG parameter %q", label)
		}
	}
	s.busy = true
	tt.Frame()
	for _, label := range []string{"目标质量", "最大颜色数", "最低质量", "处理速度"} {
		slider, ok := tt.AccessByRole(ui.RoleSlider, label)
		if !ok || !slider.Disabled {
			t.Errorf("%q must be disabled while busy: %+v", label, slider)
		}
	}
}

func TestAdvancedKeyboardFocusScrollsWithoutHidingSettingsActions(t *testing.T) {
	s := testSelectedApplication()
	s.file.format, s.advanced = "png", true
	result := &compress.Result{OriginalSize: 4096, OutputSize: 2048}
	s.result = result
	tt := openTestSettings(t, s)
	for i := 0; i < 20 && !tt.Focused("处理速度"); i++ {
		tt.Key(0, ui.KeyTab)
	}
	slider, ok := tt.AccessByRole(ui.RoleSlider, "处理速度")
	if !ok || !slider.Focused || slider.Offscreen || slider.Bounds.H < 10 {
		t.Fatalf("keyboard focus must scroll to advanced parameters: %+v", slider)
	}
	save, ok := tt.AccessByRole(ui.RoleButton, "保存")
	if !ok || save.Offscreen || save.Bounds.H < 28 || save.Bounds.Y+save.Bounds.H > 560 {
		t.Fatalf("scrolling advanced settings must keep saving reachable: %+v", save)
	}
	tt.Key(0, ui.KeyRight)
	if s.editor.presets[s.editor.selected].png.Speed != 4 || s.png.Speed != 3 || s.result != result {
		t.Fatal("advanced speed control must update only the draft before saving")
	}
	if err := tt.Click("保存"); err != nil {
		t.Fatal(err)
	}
	main := ui.NewTester(s.view, 880, 660)
	if s.settingsOpen || s.png.Speed != 4 || s.result != nil || !main.HasText("开始压缩") {
		t.Fatal("saving changed settings must close the dialog and require recompression")
	}
}

func TestMainViewOnlyOffersSchemes(t *testing.T) {
	for _, format := range []string{"jpeg", "png"} {
		t.Run(format, func(t *testing.T) {
			s := testSelectedApplication()
			s.file.format, s.advanced = format, true
			tt := ui.NewTester(s.view, 880, 660)
			if _, ok := tt.AccessByRole(ui.RoleRadioGroup, "压缩方案"); !ok {
				t.Fatal("the main page must offer scheme selection")
			}
			for _, label := range []string{"JPEG 质量", "目标质量", "最大颜色数", "最低质量", "处理速度"} {
				if _, ok := tt.AccessByRole(ui.RoleSlider, label); ok {
					t.Fatalf("compression parameter %q must only appear in the settings window", label)
				}
			}
			if tt.HasText("高级设置") {
				t.Fatal("advanced settings must only appear in the settings window")
			}
		})
	}
}

func TestSettingsCanBeEditedBeforeChoosingImage(t *testing.T) {
	s := newApplication()
	tt := ui.NewTester(s.view, 880, 660)
	if err := tt.Click("画质优先"); err != nil {
		t.Fatal(err)
	}
	if s.scheme != "画质优先" || s.quality != 92 {
		t.Fatal("scheme selection must work before choosing an image")
	}
	if err := tt.Click("设置"); err != nil {
		t.Fatal(err)
	}
	s.settingsFile = filepath.Join(t.TempDir(), "settings.json")
	settings := ui.NewTester(s.settingsView, 760, 560)
	for _, label := range []string{"JPEG 质量", "目标质量", "最大颜色数"} {
		slider, ok := settings.AccessByRole(ui.RoleSlider, label)
		if !ok || slider.Disabled {
			t.Fatalf("%s must be editable without a selected image: %+v", label, slider)
		}
	}
	slider, _ := settings.AccessByRole(ui.RoleSlider, "JPEG 质量")
	settings.ClickAt(slider.Bounds.X+slider.Bounds.W/2, slider.Bounds.Y+slider.Bounds.H/2)
	settings.Key(0, ui.KeyEnd)
	if s.editor.presets[s.editor.selected].quality != 100 || s.quality != 92 {
		t.Fatal("editing settings without an image must update only the draft")
	}
	if err := settings.Click("保存"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if s.settingsOpen || s.quality != 100 || s.scheme != "画质优先" || !tt.HasText("选择图片") {
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
			if _, ok := settings.AccessByRole(ui.RoleDialog, "设置"); !ok {
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
	s.editQuality(70)
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
			s.setSettingsOpen(true)
			s.advanced = true
			tt := ui.NewTester(func(c *ui.Context) {
				theme := ui.LightTheme()
				if dark {
					theme = ui.DarkTheme()
				}
				c.SetTheme(theme)
				s.settingsView(c)
			}, 760, 560)
			for _, label := range []string{"取消", "保存", "新增方案", "删除方案"} {
				button, ok := tt.AccessByRole(ui.RoleButton, label)
				if !ok || button.Disabled || button.Offscreen || button.Bounds.H < 28 ||
					button.Bounds.Y+button.Bounds.H > 560 || button.Bounds.X+button.Bounds.W > 760 {
					t.Fatalf("%s must fit in the minimum window: %+v", label, button)
				}
			}
			for _, label := range []string{"JPEG 质量", "目标质量", "最大颜色数", "最低质量", "处理速度"} {
				slider, ok := tt.AccessByRole(ui.RoleSlider, label)
				if !ok || slider.Bounds.W < 100 || slider.Bounds.X < 20 ||
					slider.Bounds.X+slider.Bounds.W > 740 {
					t.Fatalf("%s must fit the settings content width: %+v", label, slider)
				}
			}
		})
	}
}

func TestSettingsNoOpAndBusyKeepResult(t *testing.T) {
	for _, busy := range []bool{false, true} {
		s := testSelectedApplication()
		s.setSettingsOpen(true)
		draft := s.editor.presets[s.editor.selected]
		s.busy = busy
		result := &compress.Result{OriginalSize: 4096, OutputSize: 2048}
		s.result = result
		original := s.compressionOptions()
		if busy {
			s.setScheme("体积优先")
			s.editQuality(25)
			s.editPNGOptions(compress.PNGOptions{MinQuality: 0, MaxQuality: 60, Colors: 16, Speed: 4})
		} else {
			s.editQuality(80)
			s.editPNGOptions(s.png)
			s.editPNGOptions(compress.PNGOptions{MaxQuality: 101})
			s.setScheme("unknown")
		}
		if s.result != result || s.compressionOptions() != original || s.editor.presets[s.editor.selected] != draft {
			t.Fatalf("no-op, invalid, or busy edits changed draft/options/result (busy=%v)", busy)
		}
	}
}

func TestCompressionSettingsSnapshot(t *testing.T) {
	s := testSelectedApplication()
	captured := s.compressionOptions()
	s.setScheme("体积优先")
	if captured.Quality != 80 || captured.PNG != compress.DefaultPNGOptions() {
		t.Fatal("captured options must not change with subsequent UI edits")
	}
	options := s.png
	options.Colors = 32
	s.result = &compress.Result{OriginalSize: 4096, OutputSize: 2048}
	openTestSettings(t, s)
	s.editPNGOptions(options)
	if s.png.Colors == 32 || s.result == nil {
		t.Fatal("draft changes must not change captured options or valid results")
	}
	s.savePresetEdits()
	if s.result != nil || s.png.Colors != 32 || s.scheme != "体积优先" {
		t.Fatal("saved custom color cap must update the named scheme and invalidate the old result")
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
