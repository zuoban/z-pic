package main

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"zpic/internal/compress"
)

// Exercise the space available to the queue, which is considerably narrower
// than the window when the compression overview is beside it.
func TestStyleQueueActionsFitAtDesktopSizes(t *testing.T) {
	for _, width := range []int{760, 880, 980, 1440} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			app := styleReviewApplication(t, "batch-errors")
			tester := ui.NewTester(app.view, width, 660)
			for _, label := range []string{"添加图片", "添加文件夹", "清空", "移除无效项", "移除失败项", "重试失败项", "预览", "移除"} {
				control, ok := tester.AccessByRole(ui.RoleButton, label)
				if !ok || control.Offscreen || control.Bounds.W < 28 || control.Bounds.H < 28 {
					t.Fatalf("%s must remain usable: %+v", label, control)
				}
				// The right column starts at width - edge - overview width.
				edge, overview := float32(24), float32(320)
				if width < 960 {
					edge, overview = 16, 280
				}
				if control.Bounds.X < edge || control.Bounds.X+control.Bounds.W > float32(width)-edge-overview {
					t.Fatalf("%s escaped the queue into the overview: %+v", label, control.Bounds)
				}
			}
		})
	}
}

func TestStyleWorkspaceHeadingsAlign(t *testing.T) {
	for _, width := range []int{760, 980, 1440} {
		app := newApplication()
		tester := ui.NewTester(app.view, width, 720)
		preview, _ := tester.Access("图片预览")
		overview, _ := tester.Access("压缩概览")
		if preview.Bounds.Y != overview.Bounds.Y {
			t.Fatalf("peer headings must align at %d: preview=%v overview=%v", width, preview.Bounds, overview.Bounds)
		}
	}
}

func TestStyleToolbarFitsNativeChrome(t *testing.T) {
	for _, width := range []int{760, 880, 980, 1440} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			app := styleReviewApplication(t, "batch")
			tester := ui.NewTester(app.view, width, 660)
			tester.SetTitleBar(ui.TitleBar{Left: 80, Height: 66})
			toolbar, ok := tester.AccessByRole(ui.RoleToolbar, "操作栏")
			if !ok {
				t.Fatal("main toolbar missing")
			}
			var controls []ui.AccessInfo
			labels := []string{"重新压缩", "继续压缩", "粘贴图片", "悬浮窗", "设置"}
			if app.clipboardPNGCompressionAvailable() {
				labels = append(labels, "压缩剪贴板图片")
			}
			for _, label := range labels {
				control, ok := tester.AccessByRole(ui.RoleButton, label)
				if !ok || control.Offscreen || control.Bounds.X < 80 ||
					control.Bounds.X+control.Bounds.W > float32(width) ||
					control.Bounds.Y+control.Bounds.H > toolbar.Bounds.Y+toolbar.Bounds.H {
					t.Fatalf("toolbar control must fit beside native window buttons: %s %+v", label, control)
				}
				controls = append(controls, control)
			}
			slices.SortFunc(controls, func(a, b ui.AccessInfo) int {
				if a.Bounds.X < b.Bounds.X {
					return -1
				}
				if a.Bounds.X > b.Bounds.X {
					return 1
				}
				return 0
			})
			for index := 1; index < len(controls); index++ {
				previous, current := controls[index-1], controls[index]
				if previous.Bounds.X+previous.Bounds.W > current.Bounds.X {
					t.Fatalf("toolbar controls overlap at %d: %s / %s", width, previous.Name, current.Name)
				}
			}
		})
	}
}

func styleReviewApplication(t *testing.T, state string) *application {
	t.Helper()
	app := testSelectedApplication()
	data, err := os.ReadFile("resources/icon.png")
	if err != nil {
		t.Fatal(err)
	}
	bitmap, err := ui.DecodeBitmap(data)
	if err != nil {
		t.Fatal(err)
	}
	app.file.path = "/Pictures/项目素材/透明图片与很长的文件名用于检查界面边界.png"
	app.file.format, app.file.width, app.file.height = "png", 1024, 1024
	app.file.size, app.file.preview, app.preview = 649321, bitmap, bitmap
	switch state {
	case "empty":
		app = newApplication()
	case "result", "unchanged", "error", "busy":
		app.result = &compress.Result{OriginalSize: 649321, OutputSize: 137924}
		if state == "unchanged" {
			app.result.Unchanged, app.result.OutputSize = true, 649321
		}
		if state == "error" {
			app.failed, app.message = true, "无法写入输出目录：文件夹不可写，请选择其他保存位置。"
		}
		if state == "busy" {
			app.busy, app.phase = true, "正在压缩图片，请稍候…"
		}
	case "batch", "batch-errors", "floating":
		source := app.file
		app.batch = newBatchState([]batchItem{
			{path: source.path, absPath: source.path, relPath: filepath.Base(source.path), format: "png", width: 1024, height: 1024, originalSize: 649321, outputSize: 137924, status: batchDone, savedPath: "/Pictures/output/result.png"},
			{path: "/Pictures/screenshot.webp", absPath: "/Pictures/screenshot.webp", relPath: "screenshot.webp", format: "webp", width: 1920, height: 1080, originalSize: 1048321, status: batchPending},
			{path: "/Pictures/photo.jpg", absPath: "/Pictures/photo.jpg", relPath: "photo.jpg", format: "jpeg", width: 2400, height: 1600, originalSize: 2948321, status: batchPending},
		})
		if state == "batch-errors" {
			app.batch.items[1].status, app.batch.items[1].errMsg = batchFailed, "无法写入输出目录，请选择其他文件夹"
			app.batch.items[2].status, app.batch.items[2].invalidInput, app.batch.items[2].errMsg = batchFailed, true, "图片数据损坏"
		}
		app.batch.selected = 0
		app.prepareThumbnailCache()
		app.thumbnails.entries = make(map[string]*batchThumbnail)
		for _, item := range app.batch.items {
			app.thumbnails.entries[item.path] = &batchThumbnail{bitmap: bitmap}
		}
		app.file, app.preview, app.result = nil, nil, nil
	}
	return app
}

// Opt-in captures provide repeatable visual review without writing artifacts
// during the ordinary suite: ZPIC_STYLE_CAPTURES=build/verification/style-10/after
func TestStyleReviewRenders(t *testing.T) {
	dir := os.Getenv("ZPIC_STYLE_CAPTURES")
	if dir == "" {
		t.Skip("set ZPIC_STYLE_CAPTURES to export review frames")
	}
	save := func(name string, tester *ui.Tester) {
		t.Helper()
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		file, err := os.Create(filepath.Join(dir, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(file, tester.Image()); err != nil {
			file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
	for _, dark := range []bool{false, true} {
		theme := "light"
		if dark {
			theme = "dark"
		}
		for _, size := range [][2]int{{760, 660}, {880, 660}, {980, 720}, {1440, 900}} {
			width, height := size[0], size[1]
			for _, state := range []string{"empty", "result", "unchanged", "error", "busy", "batch", "batch-errors"} {
				app := styleReviewApplication(t, state)
				tester := ui.NewTester(app.view, width, height)
				tester.SetDark(dark)
				save(fmt.Sprintf("%s-%d-%s", state, width, theme), tester)
			}
		}
		app := styleReviewApplication(t, "batch-errors")
		accessible := ui.NewTester(app.view, 760, 660)
		accessible.SetDark(dark)
		accessible.SetVibrancy(true)
		accessible.SetPreferences(ui.Preferences{HighContrast: true, ReduceTransparency: true})
		save("high-contrast-760-"+theme, accessible)
		for _, size := range [][2]int{{640, 600}, {724, 700}} {
			width, height := size[0], size[1]
			for _, section := range []settingsSection{settingsGeneral, settingsPresets} {
				app := styleReviewApplication(t, "empty")
				app.setSettingsOpen(true)
				app.editor.section = section
				app.editor.presets[0].name = strings.Repeat("预设", 20)
				app.editor.presets[0].output = presetSaveOptions{Mode: presetSaveFixed, Directory: "/Users/example/Pictures/很长的输出路径/项目素材/已压缩图片", UseSubfolder: true, Subfolder: "zpic", UseSuffix: true, Suffix: "-zpic"}
				tester := ui.NewTester(app.settingsView, width, height)
				tester.SetDark(dark)
				save(fmt.Sprintf("settings-%d-%d-%s", section, width, theme), tester)
				tester.SetVibrancy(true)
				save(fmt.Sprintf("settings-glass-%d-%d-%s", section, width, theme), tester)
				for _, preferences := range []struct {
					name  string
					value ui.Preferences
				}{
					{"reduced-transparency", ui.Preferences{ReduceTransparency: true}},
					{"high-contrast", ui.Preferences{HighContrast: true}},
				} {
					tester.SetPreferences(preferences.value)
					save(fmt.Sprintf("settings-%s-%d-%d-%s", preferences.name, section, width, theme), tester)
				}
			}
		}
		for _, state := range []string{"compact", "grid", "list"} {
			app := styleReviewApplication(t, "floating")
			app.floating.expanded, app.floating.listView = state != "compact", state == "list"
			width, height := floatingCompactWidth, floatingCompactHeight
			if app.floating.expanded {
				width, height = floatingExpandedWidth, floatingExpandedHeight
			}
			tester := ui.NewTester(app.floatingView, width, height)
			tester.SetDark(dark)
			save("floating-"+state+"-"+theme, tester)
		}
	}
}
