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
	if !tt.HasText("选择图片") || !tt.HasText("从一张图片开始") {
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
	slider, ok := tt.AccessByRole(ui.RoleSlider, "JPEG 质量")
	if !ok || !slider.Disabled {
		t.Fatalf("quality slider must be disabled while processing: %+v", slider)
	}
}

func TestPNGViewExplainsLossyCompressionWithoutModeSelection(t *testing.T) {
	s := testSelectedApplication()
	s.file.format = "png"
	tt := ui.NewTester(s.view, 980, 720)
	if !tt.HasText("PNG 有损压缩") ||
		!tt.HasText("质量与颜色数越低，文件通常越小。支持透明度，但渐变、阴影与半透明边缘可能变化。") {
		t.Fatal("PNG must explain its default lossy compression")
	}
	if tt.HasText("无损优化") || tt.HasText("PNG 压缩模式") || tt.HasText("更小体积（有损）") {
		t.Fatal("PNG must not offer the removed mode selection")
	}
	if _, ok := tt.AccessByRole(ui.RoleSlider, "JPEG 质量"); ok {
		t.Fatal("JPEG quality must not be offered for PNG")
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
	s.setQuality(60)
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
	s.setQuality(60)
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
			s.setQuality(30)
			s.result = &compress.Result{OriginalSize: 4096, OutputSize: 2048}
			s.setScheme(test.name)
			if s.scheme != test.name || s.quality != test.quality || s.png != test.png || s.result != nil {
				t.Fatalf("preset did not apply and invalidate: scheme=%s quality=%v png=%+v", s.scheme, s.quality, s.png)
			}
		})
	}
}

func TestPNGControlsInvalidateAndClampQualityRange(t *testing.T) {
	s := testSelectedApplication()
	s.file.format = "png"
	s.result = &compress.Result{OriginalSize: 4096, OutputSize: 2048}
	s.preview = ui.NewBitmap(image.NewNRGBA(image.Rect(0, 0, 20, 12)))
	tt := ui.NewTester(s.view, 980, 720)
	slider, ok := tt.AccessByRole(ui.RoleSlider, "目标质量")
	if !ok {
		t.Fatal("PNG target quality must be adjustable")
	}
	tt.ClickAt(slider.Bounds.X+slider.Bounds.W/2, slider.Bounds.Y+slider.Bounds.H/2)
	tt.Key(0, ui.KeyHome)
	if s.png.MaxQuality != 1 || s.png.MinQuality != 1 || s.scheme != "自定义" {
		t.Fatalf("lowering target must clamp minimum and mark custom: %+v %s", s.png, s.scheme)
	}
	if s.result != nil || s.preview != s.file.preview || tt.HasText("另存副本") {
		t.Fatal("PNG parameter changes must discard the old export and preview")
	}
	if !tt.HasText("自定义") {
		t.Fatal("scheme must reflect custom settings")
	}
	for _, preset := range compressionPresets {
		scheme, ok := tt.AccessByRole(ui.RoleRadio, preset.name)
		if !ok || scheme.Checked {
			t.Fatalf("a custom scheme must not select a preset: %+v", scheme)
		}
	}
}

func TestPreviewComparisonKeepsCompressionResult(t *testing.T) {
	s := testSelectedApplication()
	result := &compress.Result{OriginalSize: 4096, OutputSize: 2048}
	compressed := ui.NewBitmap(image.NewNRGBA(image.Rect(0, 0, 20, 12)))
	s.completeCompression(context.Background(), result, compressed, nil)
	tt := ui.NewTester(s.view, 880, 660)
	for _, label := range []string{"原图", "压缩后", "原图"} {
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
		imageLabel := "原图预览"
		if label == "压缩后" {
			imageLabel = "压缩后预览"
		}
		if _, ok := tt.AccessByRole(ui.RoleImage, imageLabel); !ok {
			t.Fatalf("comparison must display %s", imageLabel)
		}
		if s.result != result || s.preview != compressed || !tt.HasText("另存副本") {
			t.Fatal("comparison must preserve the result bitmap and export")
		}
	}
	s.setQuality(60)
	tt.Frame()
	if _, ok := tt.AccessByRole(ui.RoleRadioGroup, "预览模式"); ok {
		t.Fatal("invalidating a result must remove comparison")
	}
	if s.preview != s.file.preview || s.result != nil {
		t.Fatal("invalidating comparison must restore the source")
	}
	s.completeCompression(context.Background(), result, compressed, nil)
	tt.Frame()
	after, ok := tt.AccessByRole(ui.RoleRadio, "压缩后")
	if !ok || !after.Checked {
		t.Fatal("a new result must default to the compressed preview")
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
	for _, state := range []string{"empty", "busy", "confirmation"} {
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
				if !ok || compare.Bounds.W > 160 || compare.Bounds.X+compare.Bounds.W > 880 {
					t.Fatalf("comparison must share the toolbar with replace: %+v", compare)
				}
			})
		}
	}
}

func TestPNGAdvancedParametersAndBusyState(t *testing.T) {
	s := testSelectedApplication()
	s.file.format, s.advanced = "png", true
	tt := ui.NewTester(s.view, 880, 660)
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

func TestAdvancedKeyboardFocusScrollsWithoutHidingSave(t *testing.T) {
	s := testSelectedApplication()
	s.file.format, s.advanced = "png", true
	s.result = &compress.Result{OriginalSize: 4096, OutputSize: 2048}
	tt := ui.NewTester(s.view, 880, 660)
	for i := 0; i < 12 && !tt.Focused("处理速度"); i++ {
		tt.Key(0, ui.KeyTab)
	}
	slider, ok := tt.AccessByRole(ui.RoleSlider, "处理速度")
	if !ok || !slider.Focused || slider.Offscreen || slider.Bounds.H < 10 {
		t.Fatalf("keyboard focus must scroll to advanced parameters: %+v", slider)
	}
	save, ok := tt.AccessByRole(ui.RoleButton, "另存副本")
	if !ok || save.Offscreen || save.Bounds.H < 20 || save.Bounds.Y+save.Bounds.H > 660 {
		t.Fatalf("scrolling advanced settings must keep save reachable: %+v", save)
	}
	tt.Key(0, ui.KeyRight)
	if s.png.Speed != 4 || s.scheme != "自定义" || s.result != nil {
		t.Fatal("advanced speed control must update settings and discard old result")
	}
}

func TestSettingsNoOpAndBusyKeepResult(t *testing.T) {
	for _, busy := range []bool{false, true} {
		s := testSelectedApplication()
		s.busy = busy
		result := &compress.Result{OriginalSize: 4096, OutputSize: 2048}
		s.result = result
		original := s.compressionOptions()
		if busy {
			s.setScheme("体积优先")
			s.setQuality(25)
			s.setPNGOptions(compress.PNGOptions{MinQuality: 0, MaxQuality: 60, Colors: 16, Speed: 4})
		} else {
			s.setScheme("均衡")
			s.setQuality(80)
			s.setPNGOptions(s.png)
			s.setPNGOptions(compress.PNGOptions{MaxQuality: 101})
			s.setScheme("unknown")
			s.setScheme("自定义")
		}
		if s.result != result || s.compressionOptions() != original {
			t.Fatalf("no-op, invalid, or busy edits changed options/result (busy=%v)", busy)
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
	s.setPNGOptions(options)
	if s.scheme != "自定义" || s.result != nil || s.png.Colors != 32 {
		t.Fatal("custom color cap must be applied and invalidate the old result")
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
