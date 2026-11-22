package main

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func settingsReviewApplication(t *testing.T, scene string) *application {
	t.Helper()
	app := newApplication()
	app.presets, app.scheme = testLegacyPresets(), "均衡"
	if scene == "many-presets" {
		for index := range 16 {
			preset := app.presets[1]
			preset.name = fmt.Sprintf("项目图片 %02d", index+1)
			app.presets = append(app.presets, preset)
		}
	}
	app.settingsFile = filepath.Join(t.TempDir(), "settings.json")
	app.setSettingsOpen(true)
	if scene == "general" || scene == "general-large-text" {
		return app
	}
	if scene == "folder-watch" {
		app.editor.section = settingsFolderWatch
		app.editor.watch = folderWatchSettings{
			InputDir:  "/Users/example/Desktop/待压缩截图",
			OutputDir: "/Users/example/Pictures/Z Pic 输出",
			Preset:    "均衡",
		}
		return app
	}
	if scene == "integrations" {
		app.editor.section = settingsIntegrations
		return app
	}
	if scene == "about" {
		app.editor.section = settingsAbout
		return app
	}
	app.editor.section = settingsPresets
	switch scene {
	case "naming", "naming-bottom", "large-text", "large-text-bottom", "high-contrast", "reduced-transparency":
		app.editor.selectedPreset().output = presetSaveOptions{
			Mode: presetSaveFixed, Directory: "/Users/example/Pictures/项目素材/已压缩图片",
			UseSubfolder: true, Subfolder: "发布图片", UseSuffix: true, Suffix: "-web",
		}
	case "invalid-name":
		app.editor.presets[2].name = ""
		app.editor.section = settingsGeneral
		app.saveSettingsEdits()
	case "deleting":
		app.editor.selectedPreset().name = strings.Repeat("项目", 20)
		app.requestDeletePreset()
	case "many-presets":
		app.editor.selected = len(app.editor.presets) - 1
	case "dirty":
		app.editQualityLevel(4)
	case "error":
		app.editor.message = "无法保存设置：输出目录不可写，请检查文件夹权限后重试。草稿已保留，可以继续编辑。"
	case "busy":
		app.busy = true
	}
	return app
}

// Run before and after a settings refinement with ZPIC_SETTINGS_CAPTURES.
// Ordinary tests leave screenshots and manifests untouched.
func TestSettingsReviewRenders(t *testing.T) {
	dir := os.Getenv("ZPIC_SETTINGS_CAPTURES")
	if dir == "" {
		t.Skip("set ZPIC_SETTINGS_CAPTURES to export settings review frames")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []themeMode{themeLight, themeDark} {
		for _, size := range [][2]int{{640, 600}, {724, 700}} {
			for _, scene := range []string{
				"general", "presets", "folder-watch", "integrations", "about", "naming", "invalid-name", "deleting", "many-presets",
				"dirty", "error", "busy", "large-text", "general-large-text",
				"high-contrast", "reduced-transparency", "naming-bottom", "large-text-bottom",
			} {
				app := settingsReviewApplication(t, scene)
				app.theme = mode
				app.editor.theme = mode
				preferences := ui.Preferences{ReduceMotion: true}
				switch scene {
				case "large-text", "general-large-text", "large-text-bottom":
					preferences.TextScale = 1.5
				case "high-contrast":
					preferences.HighContrast = true
				case "reduced-transparency":
					preferences.ReduceTransparency = true
				}
				tester := ui.NewTesterWith(app.settingsView, size[0], size[1], ui.TesterOptions{
					Dark: mode == themeDark, Preferences: preferences,
				})
				tester.SetVibrancy(true)
				if strings.HasSuffix(scene, "-bottom") {
					tester.Scroll(float32(size[0])/2, float32(size[1])/2, 0, 2000)
				}
				background := color.RGBA{0xf4, 0xf5, 0xf7, 0xff}
				if mode == themeDark {
					background = color.RGBA{0x0e, 0x10, 0x14, 0xff}
				}
				frame := image.NewRGBA(image.Rect(0, 0, size[0], size[1]))
				draw.Draw(frame, frame.Bounds(), image.NewUniform(background), image.Point{}, draw.Src)
				draw.Draw(frame, frame.Bounds(), tester.Image(), image.Point{}, draw.Over)
				name := fmt.Sprintf("%s-%dx%d-%s", scene, size[0], size[1], mode)
				file, err := os.Create(filepath.Join(dir, name+".png"))
				if err != nil {
					t.Fatal(err)
				}
				if err := png.Encode(file, frame); err != nil {
					file.Close()
					t.Fatal(err)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
				semantics, err := json.MarshalIndent(tester.AccessAll(), "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, name+".json"), semantics, 0o644); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}
