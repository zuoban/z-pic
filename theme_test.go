package main

import (
	"bytes"
	"encoding/json"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"zpic/internal/compress"
)

func TestThemePickerSelectionKeyboardAndDismissal(t *testing.T) {
	s := testSelectedApplication()
	s.settingsFile = filepath.Join(t.TempDir(), "settings.json")
	nativeSource := mygo.ThemeSystem
	s.applyNativeTheme = func(source mygo.ThemeSource) { nativeSource = source }
	result := &compress.Result{OriginalSize: 4096, OutputSize: 2048}
	s.result = result

	tt := ui.NewTester(s.view, 980, 720)
	picker, ok := tt.AccessByRole(ui.RoleMenuButton, "主题")
	if !ok || picker.Disabled {
		t.Fatalf("main toolbar must expose an enabled theme menu button: %+v", picker)
	}
	if s.effectiveThemeMode() != themeSystem || nativeSource != mygo.ThemeSystem {
		t.Fatalf("default theme must follow system: app=%q source=%q", s.theme, nativeSource)
	}

	if err := tt.Click("主题"); err != nil {
		t.Fatal(err)
	}
	for _, option := range themeOptions {
		item, ok := tt.AccessByRole(ui.RoleMenuItemRadio, option.label)
		if !ok || item.Checked != (option.mode == themeSystem) {
			t.Fatalf("theme menu must mark only the active option (%s): %+v", option.label, item)
		}
	}

	tt.Key(0, ui.KeyEscape)
	if s.effectiveThemeMode() != themeSystem || !tt.Focused("主题") {
		t.Fatal("Escape must close the theme menu without changing selection and restore focus")
	}
	if _, ok := tt.AccessByRole(ui.RoleMenuItemRadio, "跟随系统"); ok {
		t.Fatal("Escape must dismiss the theme menu")
	}

	if err := tt.Click("主题"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("深色"); err != nil {
		t.Fatal(err)
	}
	if s.effectiveThemeMode() != themeDark || nativeSource != mygo.ThemeDark || s.result != result {
		t.Fatalf("choosing dark theme must apply immediately and preserve compression result: theme=%q source=%q result=%v",
			s.theme, nativeSource, s.result)
	}

	if err := tt.Click("主题"); err != nil {
		t.Fatal(err)
	}
	if item, ok := tt.AccessByRole(ui.RoleMenuItemRadio, "深色"); !ok || !item.Checked {
		t.Fatalf("reopening must show 深色 checked: %+v", item)
	}
	tt.Key(0, ui.KeyHome)
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyEnter)
	if s.effectiveThemeMode() != themeLight || nativeSource != mygo.ThemeLight || s.result != result {
		t.Fatalf("keyboard selection must apply 浅色 and preserve compression result: theme=%q source=%q",
			s.theme, nativeSource)
	}
}

func TestThemeAppliesAcrossMainSettingsAndFloatingViews(t *testing.T) {
	lightBg := color.RGBA{R: 0xf7, G: 0xf7, B: 0xf8, A: 0xff}
	darkBg := color.RGBA{R: 0x14, G: 0x14, B: 0x16, A: 0xff}

	s := floatingTestApplication()
	s.settingsFile = filepath.Join(t.TempDir(), "settings.json")

	mainTester := ui.NewTester(s.view, 980, 720)
	floatingTester := ui.NewTester(s.floatingView, floatingCompactWidth, floatingCompactHeight)

	s.setSettingsOpen(true)
	settingsTester := ui.NewTester(s.settingsView, 724, 700)
	s.setSettingsOpen(false)

	assertSurfaceColors := func(want color.RGBA, context string) {
		t.Helper()
		mainTester.Frame()
		floatingTester.Frame()
		s.settingsOpen = true
		s.editor = &presetEditor{presets: s.presets}
		settingsTester.Frame()
		s.settingsOpen, s.editor = false, nil

		if got := mainTester.Image().RGBAAt(2, 2); got != want {
			t.Fatalf("%s: main window background = %+v, want %+v", context, got, want)
		}
		if got := floatingTester.Image().RGBAAt(2, 2); got != want {
			t.Fatalf("%s: floating window background = %+v, want %+v", context, got, want)
		}
		// Settings dialog top bar uses SurfaceHover; check content area below tabs (x=4, y=100).
		if got := settingsTester.Image().RGBAAt(4, 100); got != want {
			t.Fatalf("%s: settings window background = %+v, want %+v", context, got, want)
		}
	}

	// System mode follows OS light/dark changes across all three views.
	assertSurfaceColors(lightBg, "system-light")
	mainTester.SetDark(true)
	floatingTester.SetDark(true)
	settingsTester.SetDark(true)
	assertSurfaceColors(darkBg, "system-dark")

	// Explicit light overrides OS dark across all three views.
	if err := mainTester.Click("主题"); err != nil {
		t.Fatal(err)
	}
	if err := mainTester.ChooseMenuItem("浅色"); err != nil {
		t.Fatal(err)
	}
	assertSurfaceColors(lightBg, "forced-light-on-dark-os")

	// Explicit dark overrides OS light across all three views.
	mainTester.SetDark(false)
	floatingTester.SetDark(false)
	settingsTester.SetDark(false)
	if err := mainTester.Click("主题"); err != nil {
		t.Fatal(err)
	}
	if err := mainTester.ChooseMenuItem("深色"); err != nil {
		t.Fatal(err)
	}
	assertSurfaceColors(darkBg, "forced-dark-on-light-os")

	// Returning to system follows OS light again.
	if err := mainTester.Click("主题"); err != nil {
		t.Fatal(err)
	}
	if err := mainTester.ChooseMenuItem("跟随系统"); err != nil {
		t.Fatal(err)
	}
	assertSurfaceColors(lightBg, "returned-to-system-light")
}

func TestThemePersistenceAcrossPresetEditsAndRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	batchOut := filepath.Join(dir, "exports")
	if err := os.MkdirAll(batchOut, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ZPIC_CONFIG_FILE", path)

	s := newApplication()
	s.presets = testLegacyPresets()
	s.restoreSettings()
	if err := saveSettings(path, testLegacyPresets(), "均衡"); err != nil {
		t.Fatal(err)
	}
	s.restoreSettings()
	if s.effectiveThemeMode() != themeSystem {
		t.Fatalf("legacy config without theme must restore themeSystem, got %q", s.theme)
	}

	s.setTheme(themeDark)
	s.setScheme("画质优先")
	s.rememberBatchOutputDir(batchOut)

	settings := openTestSettings(t, s)
	s.addPreset()
	if err := settings.Click("保存"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw["theme"]) != `"dark"` {
		t.Fatalf("preset switches, batch output updates and settings saves must retain theme: %s", data)
	}

	var restoredSource mygo.ThemeSource
	restarted := newApplication()
	restarted.applyNativeTheme = func(source mygo.ThemeSource) { restoredSource = source }
	restarted.restoreSettings()
	if restarted.effectiveThemeMode() != themeDark || restoredSource != mygo.ThemeDark ||
		restarted.scheme != "画质优先" || restarted.batchOutputDir != batchOut || len(restarted.presets) != 4 {
		t.Fatalf("restarted app did not restore full state: theme=%q source=%q scheme=%q batchDir=%q presets=%d",
			restarted.theme, restoredSource, restarted.scheme, restarted.batchOutputDir, len(restarted.presets))
	}

	// Switching back to system omits "theme" from JSON while preserving everything else.
	restarted.setTheme(themeSystem)
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw = nil
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, exists := raw["theme"]; exists || restoredSource != mygo.ThemeSystem {
		t.Fatalf("system theme must be omitted from JSON and sync ThemeSystem: source=%q json=%s", restoredSource, data)
	}

	// Explicit "theme":"system" in hand-edited settings.json is also accepted cleanly.
	explicitSystem := strings.Replace(validStoredSettings, `"version":1,`, `"version":1,"theme":"system",`, 1)
	if err := os.WriteFile(path, []byte(explicitSystem), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadFullSettings(path)
	if err != nil || cfg.theme != themeSystem {
		t.Fatalf("explicit system theme must load cleanly: cfg=%+v err=%v", cfg, err)
	}
}

func TestThemeErrorHandlingCorruptConfigBusyAndModalGuards(t *testing.T) {
	// 1. Corrupt config is never overwritten by theme switch until user explicitly saves settings.
	corruptDir := t.TempDir()
	corruptPath := filepath.Join(corruptDir, "settings.json")
	corruptBytes := []byte(`{"version":1,"broken"`)
	if err := os.WriteFile(corruptPath, corruptBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ZPIC_CONFIG_FILE", corruptPath)

	var syncedSource mygo.ThemeSource
	s := newApplication()
	s.applyNativeTheme = func(source mygo.ThemeSource) { syncedSource = source }
	s.restoreSettings()
	if s.settingsError == "" || s.effectiveThemeMode() != themeSystem || syncedSource != mygo.ThemeSystem {
		t.Fatalf("corrupt startup must record settingsError and default to system theme: err=%q theme=%q source=%q",
			s.settingsError, s.theme, syncedSource)
	}
	s.setTheme(themeLight)
	if s.effectiveThemeMode() != themeLight || syncedSource != mygo.ThemeLight {
		t.Fatal("theme switch during corrupt-config session should still update appearance in memory")
	}
	afterSwitch, err := os.ReadFile(corruptPath)
	if err != nil || !bytes.Equal(afterSwitch, corruptBytes) {
		t.Fatal("theme switch must not overwrite a corrupt settings.json without explicit settings save")
	}
	settings := openTestSettings(t, s)
	if err := settings.Click("保存"); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadFullSettings(corruptPath)
	if err != nil || cfg.theme != themeLight {
		t.Fatalf("explicit settings save must repair file and persist chosen theme: cfg=%+v err=%v", cfg, err)
	}

	// 2. Write failure leaves previous theme and compression result intact.
	unwritable := testSelectedApplication()
	result := &compress.Result{OriginalSize: 4096, OutputSize: 2048}
	unwritable.result = result
	unwritable.settingsFile = t.TempDir() // directory causes save failure
	unwritable.setTheme(themeDark)
	if unwritable.effectiveThemeMode() != themeSystem || unwritable.result != result ||
		!unwritable.failed || !strings.Contains(unwritable.message, "保存主题设置失败") {
		t.Fatalf("failed theme save must retain previous theme and result: theme=%q failed=%v msg=%q",
			unwritable.theme, unwritable.failed, unwritable.message)
	}

	// 3. Busy processing allows visual theme switching without interrupting work, while modal dialogs guard it.
	busyApp := testSelectedApplication()
	busyApp.settingsFile = filepath.Join(t.TempDir(), "settings.json")
	busyApp.busy, busyApp.phase = true, "正在压缩图片"
	busyTester := ui.NewTester(busyApp.view, 880, 660)
	if err := busyTester.Click("主题"); err != nil {
		t.Fatal(err)
	}
	if err := busyTester.ChooseMenuItem("深色"); err != nil {
		t.Fatal(err)
	}
	if !busyApp.busy || busyApp.effectiveThemeMode() != themeDark {
		t.Fatal("theme switching during background work must apply cleanly without interrupting work")
	}

	for _, modal := range []string{"settings", "overwrite", "batch-overwrite"} {
		t.Run(modal, func(t *testing.T) {
			guarded := testSelectedApplication()
			switch modal {
			case "settings":
				guarded.setSettingsOpen(true)
			case "overwrite":
				guarded.result = &compress.Result{OriginalSize: 4096, OutputSize: 2048}
				guarded.requestOverwrite()
			case "batch-overwrite":
				guarded.batch = newBatchState([]batchItem{{path: "/photos/example.jpg", relPath: "example.jpg", status: batchPending}})
				guarded.confirmBatchOverwrite = true
			}
			guarded.setTheme(themeDark)
			if guarded.effectiveThemeMode() != themeSystem {
				t.Fatalf("modal state %q must guard setTheme", modal)
			}
			tt := ui.NewTester(guarded.view, 880, 660)
			if modal == "settings" {
				if picker, ok := tt.AccessByRole(ui.RoleMenuButton, "主题"); !ok || !picker.Disabled {
					t.Fatalf("theme menu button must be disabled while settings modal is open: %+v", picker)
				}
			}
		})
	}
}
