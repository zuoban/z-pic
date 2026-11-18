package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"zpic/internal/compress"
)

func TestSettingsGeneralEntryAndSharedDraft(t *testing.T) {
	app := testSelectedApplication()
	app.settingsFile = filepath.Join(t.TempDir(), "settings.json")
	app.setSettingsOpen(true)
	tester := ui.NewTester(app.settingsView, 640, 600)
	if app.editor.section != settingsGeneral || !tester.HasText("外观") {
		t.Fatal("settings must open the general page")
	}
	if _, ok := tester.AccessByRole(ui.RoleSlider, "图片质量"); ok {
		t.Fatal("general settings must not contain preset editing controls")
	}
	if err := tester.Click("深色"); err != nil {
		t.Fatal(err)
	}
	if app.editor.theme != themeDark || app.effectiveThemeMode() != themeSystem {
		t.Fatal("appearance must remain a draft until saved")
	}
	if err := tester.Click("管理预设"); err != nil {
		t.Fatal(err)
	}
	replaceTestPresetName(t, tester, "自定义预设")
	if err := tester.Click("通用"); err != nil {
		t.Fatal(err)
	}
	if app.editor.theme != themeDark || app.editor.selectedPreset().name != "自定义预设" {
		t.Fatal("switching pages must preserve both drafts")
	}
	if err := tester.Click("保存"); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadFullSettings(app.settingsFile)
	if err != nil {
		t.Fatal(err)
	}
	if app.settingsOpen || app.effectiveThemeMode() != themeDark ||
		cfg.theme != themeDark || cfg.selected != "自定义预设" {
		t.Fatalf("save must apply and persist both pages together: %+v", cfg)
	}
}

func TestGeneralSettingsDiscardAndThemeOnlySave(t *testing.T) {
	for _, exit := range []string{"取消", "Escape", "close", "保存"} {
		t.Run(exit, func(t *testing.T) {
			app := testSelectedApplication()
			app.settingsFile = filepath.Join(t.TempDir(), "settings.json")
			var nativeSource mygo.ThemeSource
			app.applyNativeTheme = func(source mygo.ThemeSource) { nativeSource = source }
			app.syncTheme(themeLight)
			if err := app.saveCurrentSettings(app.presets, app.scheme, "", themeLight); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(app.settingsFile)
			if err != nil {
				t.Fatal(err)
			}
			result := &compress.Result{OriginalSize: 2048, OutputSize: 1024}
			app.result = result
			presets := append([]compressionPreset(nil), app.presets...)
			app.setSettingsOpen(true)
			tester := ui.NewTester(app.settingsView, 640, 600)
			if err := tester.Click("深色"); err != nil {
				t.Fatal(err)
			}
			if nativeSource != mygo.ThemeDark {
				t.Fatal("selecting a theme must preview native chrome immediately")
			}
			switch exit {
			case "Escape":
				tester.Key(0, ui.KeyEscape)
			case "close":
				app.setSettingsOpen(false)
			default:
				if err := tester.Click(exit); err != nil {
					t.Fatal(err)
				}
			}
			expected := themeLight
			if exit == "保存" {
				expected = themeDark
			}
			if app.settingsOpen || app.editor != nil || app.effectiveThemeMode() != expected || nativeSource != expected.source() ||
				app.result != result || !reflect.DeepEqual(app.presets, presets) {
				t.Fatal("theme edits must respect save/discard and preserve compression results")
			}
			cfg, err := loadFullSettings(app.settingsFile)
			if err != nil || cfg.theme != expected {
				t.Fatalf("persisted theme = %q, err = %v", cfg.theme, err)
			}
			if exit != "保存" {
				after, err := os.ReadFile(app.settingsFile)
				if err != nil || string(before) != string(after) {
					t.Fatal("discarding settings must leave the original file intact")
				}
			}
		})
	}
}

func TestSettingsThemeImmediatelyPreviewsEverySurface(t *testing.T) {
	for _, systemDark := range []bool{false, true} {
		t.Run(map[bool]string{false: "light system", true: "dark system"}[systemDark], func(t *testing.T) {
			app := testSelectedApplication()
			app.settingsFile = filepath.Join(t.TempDir(), "settings.json")
			var nativeSource mygo.ThemeSource
			app.applyNativeTheme = func(source mygo.ThemeSource) { nativeSource = source }
			app.syncTheme(themeSystem)
			if err := app.saveCurrentSettings(app.presets, app.scheme, "", themeSystem); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(app.settingsFile)
			if err != nil {
				t.Fatal(err)
			}
			app.setSettingsOpen(true)
			var settingsDark, mainDark, floatingDark bool
			settings := ui.NewTester(func(c *ui.Context) {
				app.settingsView(c)
				settingsDark = c.Theme().Dark
			}, 640, 600)
			main := ui.NewTester(func(c *ui.Context) {
				app.view(c)
				mainDark = c.Theme().Dark
			}, 980, 720)
			floating := ui.NewTester(func(c *ui.Context) {
				app.floatingView(c)
				floatingDark = c.Theme().Dark
			}, floatingCompactWidth, floatingCompactHeight)
			for _, tester := range []*ui.Tester{settings, main, floating} {
				tester.SetDark(systemDark)
			}
			for _, mode := range []themeMode{themeDark, themeLight, themeSystem} {
				if err := settings.Click(mode.label()); err != nil {
					t.Fatal(err)
				}
				for _, tester := range []*ui.Tester{settings, main, floating} {
					tester.Frame()
				}
				wantDark := mode == themeDark || mode == themeSystem && systemDark
				if settingsDark != wantDark || mainDark != wantDark || floatingDark != wantDark ||
					nativeSource != mode.source() {
					t.Fatalf("%s must immediately preview on every surface: settings=%t main=%t floating=%t native=%v",
						mode, settingsDark, mainDark, floatingDark, nativeSource)
				}
				if app.effectiveThemeMode() != themeSystem {
					t.Fatal("preview must not change the committed preference")
				}
				after, err := os.ReadFile(app.settingsFile)
				if err != nil || string(after) != string(before) {
					t.Fatal("preview must not persist before saving")
				}
			}
		})
	}
}

func TestSettingsSharedSaveFailureIsAtomic(t *testing.T) {
	app := testSelectedApplication()
	var nativeSource mygo.ThemeSource
	app.applyNativeTheme = func(source mygo.ThemeSource) { nativeSource = source }
	app.syncTheme(themeLight)
	app.settingsFile = t.TempDir()
	app.setSettingsOpen(true)
	editor := app.editor
	app.previewSettingsTheme(themeDark)
	editor.selectedPreset().name = "草稿名称"
	original := append([]compressionPreset(nil), app.presets...)
	app.saveSettingsEdits()
	if !app.settingsOpen || app.editor != editor || editor.theme != themeDark || nativeSource != mygo.ThemeDark ||
		app.effectiveThemeMode() != themeLight || !reflect.DeepEqual(app.presets, original) ||
		!strings.Contains(editor.message, "无法保存设置") {
		t.Fatal("failed persistence must keep drafts open and leave live settings unchanged")
	}
	app.settingsFile = filepath.Join(t.TempDir(), "settings.json")
	app.saveSettingsEdits()
	if app.settingsOpen || app.effectiveThemeMode() != themeDark || app.scheme != "草稿名称" {
		t.Fatal("retry must save the retained draft")
	}
}

func TestGeneralSaveRevealsInvalidPreset(t *testing.T) {
	app := testSelectedApplication()
	app.settingsFile = filepath.Join(t.TempDir(), "settings.json")
	app.setSettingsOpen(true)
	app.editor.presets[0].name = ""
	app.saveSettingsEdits()
	if !app.settingsOpen || app.editor.section != settingsPresets || app.editor.message == "" {
		t.Fatal("invalid presets must reveal the preset page with an error")
	}
}

func TestPresetEntryAndDeleteConfirmationRetainDraft(t *testing.T) {
	app := testSelectedApplication()
	app.openPresetSettings()
	editor := app.editor
	if editor.section != settingsPresets {
		t.Fatal("preset entry must open preset settings directly")
	}
	editor.theme = themeDark
	app.requestDeletePreset()
	tester := ui.NewTester(app.settingsView, 640, 600)
	if err := tester.Click("通用"); err != nil {
		t.Fatal(err)
	}
	app.setSettingsOpen(true)
	app.openPresetSettings()
	if app.editor != editor || editor.section != settingsPresets || !editor.confirmDelete ||
		editor.theme != themeDark {
		t.Fatal("pending deletion must block navigation and preserve the shared draft")
	}
}

func TestGeneralSettingsControlsFitWindow(t *testing.T) {
	for _, size := range [][2]float32{{724, 700}, {640, 600}, {420, 680}} {
		app := newApplication()
		app.setSettingsOpen(true)
		tester := ui.NewTester(app.settingsView, int(size[0]), int(size[1]))
		for _, label := range []string{"取消", "保存", "管理预设"} {
			control, ok := tester.AccessByRole(ui.RoleButton, label)
			if !ok || control.Offscreen || control.Bounds.X < 0 ||
				control.Bounds.X+control.Bounds.W > size[0] ||
				control.Bounds.Y+control.Bounds.H > size[1] {
				t.Fatalf("%q must fit %v: %+v", label, size, control)
			}
		}
		// All shortcuts should be readable without scrolling at the supported
		// minimum size, rather than disappearing beneath the fixed footer.
		shortcut, ok := tester.AccessByRole(ui.RoleText, "打开设置")
		save, _ := tester.AccessByRole(ui.RoleButton, "保存")
		if !ok || shortcut.Offscreen || shortcut.Bounds.Y+shortcut.Bounds.H > save.Bounds.Y-12 {
			t.Fatalf("the last shortcut must fit above the footer at %v: %+v", size, shortcut)
		}
	}
}

func TestSettingsGlassTextContrast(t *testing.T) {
	for _, mode := range []themeMode{themeLight, themeDark} {
		for _, preference := range []struct {
			name  string
			value ui.Preferences
		}{
			{"glass", ui.Preferences{}},
			{"reduced-transparency", ui.Preferences{ReduceTransparency: true}},
			{"high-contrast", ui.Preferences{HighContrast: true}},
		} {
			t.Run(string(mode)+"/"+preference.name, func(t *testing.T) {
				app := newApplication()
				app.setSettingsOpen(true)
				app.editor.theme = mode
				var theme ui.Theme
				var metadata ui.Color
				tester := ui.NewTester(func(c *ui.Context) {
					app.settingsView(c)
					theme = *c.Theme()
					metadata = metadataColor(c)
				}, 640, 600)
				tester.SetVibrancy(true)
				tester.SetPreferences(preference.value)
				for _, active := range []bool{true, false} {
					tester.SetWindowActive(active)
					save, _ := tester.AccessByRole(ui.RoleButton, "保存")
					heading, _ := tester.AccessByRole(ui.RoleHeading, "外观")
					frame := tester.Image()
					foreground := theme.AccentText
					if !active && preference.name != "glass" {
						foreground = theme.Text
					}
					background := frame.RGBAAt(int(save.Bounds.X+save.Bounds.W/2), int(save.Bounds.Y+6))
					if ratio := themeContrast(foreground, ui.RGB(background.R, background.G, background.B)); ratio < 4.5 {
						t.Errorf("save label contrast %.2f in active=%t window, want 4.5", ratio, active)
					}
					if preference.name == "glass" {
						// The inactive window's native default button uses a dark
						// label. A blue glass action must explicitly keep white text.
						whitePixels := 0
						for y := int(save.Bounds.Y + 10); y < int(save.Bounds.Y+26); y++ {
							for x := int(save.Bounds.X + 28); x < int(save.Bounds.X+60); x++ {
								pixel := frame.RGBAAt(x, y)
								if pixel.R > 220 && pixel.G > 220 && pixel.B > 220 {
									whitePixels++
								}
							}
						}
						if whitePixels < 4 {
							t.Errorf("glass save action lost its white label in active=%t window", active)
						}
					}
					panel := frame.RGBAAt(int(heading.Bounds.X+4), int(heading.Bounds.Y-6))
					for _, foreground := range []ui.Color{theme.TextMuted, metadata} {
						if ratio := themeContrast(foreground, ui.RGB(panel.R, panel.G, panel.B)); ratio < 4.5 {
							t.Errorf("panel fallback text contrast %.2f, want 4.5", ratio)
						}
					}
				}
			})
		}
	}
}
