package main

import (
	"bytes"
	"encoding/json"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"zpic/internal/compress"
)

func themeContrast(foreground, background ui.Color) float64 {
	luminance := func(value ui.Color) float64 {
		channel := func(component uint8) float64 {
			normalized := float64(component) / 255
			if normalized <= 0.04045 {
				return normalized / 12.92
			}
			return math.Pow((normalized+0.055)/1.055, 2.4)
		}
		return 0.2126*channel(value.R) + 0.7152*channel(value.G) + 0.0722*channel(value.B)
	}
	first, second := luminance(foreground.Over(background)), luminance(background)
	return (max(first, second) + 0.05) / (min(first, second) + 0.05)
}

func TestThemeTextContrast(t *testing.T) {
	for _, mode := range []themeMode{themeLight, themeDark} {
		t.Run(string(mode), func(t *testing.T) {
			tester := ui.NewTester(func(c *ui.Context) {
				applyThemeMode(c, mode)
				theme := c.Theme()
				backgrounds := map[string]ui.Color{
					"window": theme.Background, "panel": theme.Surface,
					"stage": stageBackground(c), "empty-stage": emptyStageBackground(c),
					"subtle": subtleSurface(c), "control": controlSurface(c),
					"field": fieldSurface(c), "card": cardSurface(c),
					"seg-thumb": segmentedThumbSurface(c),
					"hover":     theme.SurfaceHover, "pressed": theme.SurfacePressed,
					"selection": selectionBackground(c), "keycap": keycapSurface(c),
				}
				foregrounds := map[string]ui.Color{
					"body": theme.Text, "secondary": theme.TextMuted, "metadata": metadataColor(c),
					"accent": accentForeground(theme), "danger": theme.Danger,
					"warning": theme.Warning, "success": theme.Success,
				}
				for backgroundName, background := range backgrounds {
					for foregroundName, foreground := range foregrounds {
						if ratio := themeContrast(foreground, background); ratio < 4.5 {
							t.Errorf("%s text on %s: contrast %.2f, want at least 4.5", foregroundName, backgroundName, ratio)
						}
					}
				}
				for name, fill := range map[string]ui.Color{
					"default": theme.Accent, "hover": theme.AccentHover, "pressed": theme.AccentPressed,
				} {
					if ratio := themeContrast(theme.AccentText, fill); ratio < 4.5 {
						t.Errorf("primary %s: contrast %.2f, want at least 4.5", name, ratio)
					}
				}
			}, 100, 100)
			tester.Frame()
		})
	}
}

func TestThemePreservesAccessibilityPreferences(t *testing.T) {
	for _, mode := range []themeMode{themeSystem, themeLight, themeDark} {
		t.Run(string(mode), func(t *testing.T) {
			var theme ui.Theme
			var glass bool
			var metadata, border, selection ui.Color
			tester := ui.NewTester(func(c *ui.Context) {
				applyThemeMode(c, mode)
				theme = *c.Theme()
				glass = usesGlass(c)
				metadata, border, selection = metadataColor(c), controlBorder(c), selectionBorder(c)
			}, 100, 100)
			tester.SetVibrancy(true)
			for _, dark := range []bool{false, true} {
				tester.SetDark(dark)
				tester.SetPreferences(ui.Preferences{})
				if !glass {
					t.Fatal("ordinary appearance must retain native glass")
				}
				normalBorder := border
				tester.SetPreferences(ui.Preferences{
					HighContrast: true, TextScale: 1.25, Accent: ui.Hex("#e11d48"),
				})
				for range 3 {
					tester.Frame()
					if glass || theme.Focus.A != 255 || border.A <= normalBorder.A {
						t.Fatalf("high contrast must use opaque surfaces, solid focus and stronger borders: glass=%v focus=%v border=%v", glass, theme.Focus, border)
					}
					if metadata != theme.TextMuted || selection.A != 255 {
						t.Fatal("high contrast must strengthen metadata and selection outlines")
					}
					if theme.FontSize != 16.25 {
						t.Fatalf("text scale must apply once, got font size %v", theme.FontSize)
					}
					if theme.Accent != ui.Hex(productAccentHex) {
						t.Fatal("accessibility preferences must retain the product accent")
					}
					wantDark := mode == themeDark || mode == themeSystem && dark
					if theme.Dark != wantDark {
						t.Fatalf("appearance mode %q on dark=%v resolved to dark=%v", mode, dark, theme.Dark)
					}
				}
				tester.SetPreferences(ui.Preferences{})
				if !glass || theme.FontSize != 13 {
					t.Fatal("clearing accessibility preferences must restore glass and default font size")
				}
			}
		})
	}
}

func TestThemeSelectionIsAvailableOnlyInSettings(t *testing.T) {
	s := testSelectedApplication()
	s.settingsFile = filepath.Join(t.TempDir(), "settings.json")
	nativeSource := mygo.ThemeSystem
	s.applyNativeTheme = func(source mygo.ThemeSource) { nativeSource = source }
	result := &compress.Result{OriginalSize: 4096, OutputSize: 2048}
	s.result = result

	tt := ui.NewTester(s.view, 980, 720)
	if _, ok := tt.AccessByRole(ui.RoleMenuButton, "主题"); ok {
		t.Fatal("main toolbar must not expose a theme menu")
	}
	if s.effectiveThemeMode() != themeSystem || nativeSource != mygo.ThemeSystem {
		t.Fatalf("default theme must follow system: app=%q source=%q", s.theme, nativeSource)
	}

	if err := tt.Click("设置"); err != nil {
		t.Fatal(err)
	}
	settings := ui.NewTester(s.settingsView, 640, 600)
	for _, option := range themeOptions {
		item, ok := settings.AccessByRole(ui.RoleRadio, option.label)
		if !ok || item.Checked != (option.mode == themeSystem) {
			t.Fatalf("settings must mark only the active theme (%s): %+v", option.label, item)
		}
	}

	if err := settings.Click("深色"); err != nil {
		t.Fatal(err)
	}
	if s.editor.theme != themeDark || nativeSource != mygo.ThemeDark || s.result != result {
		t.Fatalf("choosing dark theme must preview immediately and preserve compression result: theme=%q source=%q result=%v",
			s.theme, nativeSource, s.result)
	}

	settings.Key(0, ui.KeyHome)
	settings.Key(0, ui.KeyRight)
	settings.Key(0, ui.KeySpace)
	if s.editor.theme != themeLight || nativeSource != mygo.ThemeLight || s.result != result {
		t.Fatalf("keyboard selection must apply 浅色 and preserve compression result: theme=%q source=%q",
			s.theme, nativeSource)
	}
	if err := settings.Click("保存"); err != nil {
		t.Fatal(err)
	}
	if s.effectiveThemeMode() != themeLight || s.settingsOpen {
		t.Fatal("saving settings must commit the chosen appearance")
	}
}

func TestThemeAppliesAcrossMainSettingsAndFloatingViews(t *testing.T) {
	lightBg := color.RGBA{R: 0xf4, G: 0xf5, B: 0xf7, A: 0xff}
	darkBg := color.RGBA{R: 0x0e, G: 0x10, B: 0x14, A: 0xff}

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
		s.editor = &settingsEditor{presets: s.presets, theme: s.effectiveThemeMode()}
		settingsTester.Frame()
		s.settingsOpen, s.editor = false, nil

		if got := mainTester.Image().RGBAAt(2, 2); got != want {
			t.Fatalf("%s: main window background = %+v, want %+v", context, got, want)
		}
		if got := floatingTester.Image().RGBAAt(2, 2); got != want {
			t.Fatalf("%s: floating window background = %+v, want %+v", context, got, want)
		}
		if got := settingsTester.Image().RGBAAt(4, 180); got != want {
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
	s.setTheme(themeLight)
	assertSurfaceColors(lightBg, "forced-light-on-dark-os")

	// Explicit dark overrides OS light across all three views.
	mainTester.SetDark(false)
	floatingTester.SetDark(false)
	settingsTester.SetDark(false)
	s.setTheme(themeDark)
	assertSurfaceColors(darkBg, "forced-dark-on-light-os")

	// Returning to system follows OS light again.
	s.setTheme(themeSystem)
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
	busyApp.setTheme(themeDark)
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
			if _, ok := tt.AccessByRole(ui.RoleMenuButton, "主题"); ok {
				t.Fatal("main toolbar must not expose a theme menu")
			}
		})
	}
}

func TestResponsiveMobileViewportFitsControlsWithoutHorizontalOverflow(t *testing.T) {
	const mobileW, mobileH = 420, 860
	s := testSelectedApplication()
	s.result = &compress.Result{OriginalSize: 4096, OutputSize: 1536}
	singleTester := ui.NewTester(s.view, mobileW, mobileH)
	for _, label := range []string{"重新压缩", "悬浮窗", "设置"} {
		btn, ok := singleTester.AccessByRole(ui.RoleButton, label)
		if !ok || btn.Bounds.W <= 0 || btn.Bounds.X < 0 || btn.Bounds.X+btn.Bounds.W > mobileW {
			t.Fatalf("mobile toolbar button %q must fit horizontally within %dpx: %+v", label, mobileW, btn)
		}
	}
	for _, label := range []string{"选择预设"} {
		menuBtn, ok := singleTester.AccessByRole(ui.RoleMenuButton, label)
		if ok {
			t.Fatalf("mobile main view must not expose %q: %+v", label, menuBtn)
		}
	}

	s.openPresetSettings()
	settingsTester := ui.NewTester(s.settingsView, mobileW, 680)
	for _, label := range []string{"新增预设", "复制预设", "删除预设", "取消", "保存"} {
		btn, ok := settingsTester.AccessByRole(ui.RoleButton, label)
		if !ok || btn.Bounds.W <= 0 || btn.Bounds.X < 0 || btn.Bounds.X+btn.Bounds.W > mobileW {
			t.Fatalf("mobile settings button %q must fit horizontally within %dpx: %+v", label, mobileW, btn)
		}
	}
}

func TestLiquidGlassVibrancyAndReduceTransparencyFallback(t *testing.T) {
	lightBg := color.RGBA{R: 0xf4, G: 0xf5, B: 0xf7, A: 0xff}
	darkBg := color.RGBA{R: 0x0e, G: 0x10, B: 0x14, A: 0xff}

	s := testSelectedApplication()
	s.result = &compress.Result{OriginalSize: 4096, OutputSize: 2048}
	s.comparison.mode = previewCompare
	s.comparison.split = 50

	mainTester := ui.NewTester(s.view, 980, 720)
	floatingTester := ui.NewTester(s.floatingView, floatingCompactWidth, floatingCompactHeight)
	s.setSettingsOpen(true)
	settingsTester := ui.NewTester(s.settingsView, 724, 700)
	s.setSettingsOpen(false)

	renderAll := func() {
		t.Helper()
		mainTester.Frame()
		floatingTester.Frame()
		s.settingsOpen = true
		s.editor = &settingsEditor{presets: s.presets, theme: s.effectiveThemeMode()}
		settingsTester.Frame()
		s.settingsOpen, s.editor = false, nil
	}

	// 1. With vibrancy active and ReduceTransparency false, window roots are transparent for Liquid Glass.
	mainTester.SetVibrancy(true)
	floatingTester.SetVibrancy(true)
	settingsTester.SetVibrancy(true)
	renderAll()

	if got := mainTester.Image().RGBAAt(2, 2); got.A != 0 {
		t.Fatalf("main root must be transparent when glass vibrancy is enabled, got %+v", got)
	}
	if got := floatingTester.Image().RGBAAt(2, 2); got.A != 0 {
		t.Fatalf("floating root must be transparent when glass vibrancy is enabled, got %+v", got)
	}
	if got := settingsTester.Image().RGBAAt(4, 180); got.A != 0 {
		t.Fatalf("settings root must be transparent when glass vibrancy is enabled, got %+v", got)
	}
	// Preview panel surface should be translucent over the glass root.
	if got := mainTester.Image().RGBAAt(40, 80); got.A == 0 || got.A == 255 {
		t.Fatalf("preview panel should render translucent glass surface (0 < alpha < 255), got %+v", got)
	}

	// 2. Dark mode keeps transparent roots and translucent panels without losing controls.
	mainTester.SetDark(true)
	floatingTester.SetDark(true)
	settingsTester.SetDark(true)
	renderAll()

	if got := mainTester.Image().RGBAAt(2, 2); got.A != 0 {
		t.Fatalf("dark main root must stay transparent when glass vibrancy is enabled, got %+v", got)
	}
	if got := mainTester.Image().RGBAAt(40, 80); got.A == 0 || got.A == 255 {
		t.Fatalf("dark preview panel should render translucent glass surface, got %+v", got)
	}

	// 3. ReduceTransparency falls back to opaque backgrounds even when vibrancy is enabled.
	reducePref := ui.Preferences{ReduceTransparency: true}
	mainTester.SetPreferences(reducePref)
	floatingTester.SetPreferences(reducePref)
	settingsTester.SetPreferences(reducePref)
	renderAll()

	if got := mainTester.Image().RGBAAt(2, 2); got != darkBg {
		t.Fatalf("dark ReduceTransparency fallback main background = %+v, want %+v", got, darkBg)
	}
	if got := floatingTester.Image().RGBAAt(2, 2); got != darkBg {
		t.Fatalf("dark ReduceTransparency fallback floating background = %+v, want %+v", got, darkBg)
	}
	if got := settingsTester.Image().RGBAAt(4, 180); got != darkBg {
		t.Fatalf("dark ReduceTransparency fallback settings background = %+v, want %+v", got, darkBg)
	}

	// 4. Disabling ReduceTransparency in light mode restores transparent glass root; disabling vibrancy restores opaque.
	mainTester.SetDark(false)
	floatingTester.SetDark(false)
	settingsTester.SetDark(false)
	mainTester.SetPreferences(ui.Preferences{})
	floatingTester.SetPreferences(ui.Preferences{})
	settingsTester.SetPreferences(ui.Preferences{})
	renderAll()
	if got := mainTester.Image().RGBAAt(2, 2); got.A != 0 {
		t.Fatalf("restoring transparency must re-enable transparent glass root, got %+v", got)
	}

	mainTester.SetVibrancy(false)
	floatingTester.SetVibrancy(false)
	settingsTester.SetVibrancy(false)
	renderAll()
	if got := mainTester.Image().RGBAAt(2, 2); got != lightBg {
		t.Fatalf("disabling vibrancy must restore opaque light background = %+v, want %+v", got, lightBg)
	}
}
