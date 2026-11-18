package main

import (
	"errors"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type themeMode string

const (
	themeSystem themeMode = "system"
	themeLight  themeMode = "light"
	themeDark   themeMode = "dark"
)

type themeOption struct {
	mode  themeMode
	label string
}

var themeOptions = [...]themeOption{
	{mode: themeSystem, label: "跟随系统"},
	{mode: themeLight, label: "浅色"},
	{mode: themeDark, label: "深色"},
}

func (m themeMode) normalized() themeMode {
	if m == "" {
		return themeSystem
	}
	return m
}

func (m themeMode) validate() error {
	switch m {
	case "", themeSystem, themeLight, themeDark:
		return nil
	default:
		return errors.New("未知的主题模式")
	}
}

func (m themeMode) storedValue() string {
	switch m.normalized() {
	case themeLight:
		return string(themeLight)
	case themeDark:
		return string(themeDark)
	default:
		return ""
	}
}

func (m themeMode) source() mygo.ThemeSource {
	switch m.normalized() {
	case themeLight:
		return mygo.ThemeLight
	case themeDark:
		return mygo.ThemeDark
	default:
		return mygo.ThemeSystem
	}
}

func (m themeMode) label() string {
	switch m.normalized() {
	case themeLight:
		return "浅色"
	case themeDark:
		return "深色"
	default:
		return "跟随系统"
	}
}

func (s *application) effectiveThemeMode() themeMode {
	if s == nil {
		return themeSystem
	}
	return s.theme.normalized()
}

func (s *application) syncTheme(mode themeMode) {
	s.theme = mode.normalized()
	s.syncThemeAppearance(s.theme)
}

func (s *application) syncThemeAppearance(mode themeMode) {
	if s.applyNativeTheme != nil {
		s.applyNativeTheme(mode.source())
	} else if s.window != nil {
		mygo.Theme.SetSource(mode.source())
	}
	if !s.closed {
		if s.window != nil {
			s.window.Invalidate()
		}
		if s.settingsWindow != nil {
			s.settingsWindow.Invalidate()
		}
		s.invalidateFloatingWindow()
	}
}

func (s *application) previewSettingsTheme(mode themeMode) {
	if s.settingsDraftLocked() || !s.settingsOpen || mode.validate() != nil {
		return
	}
	s.editor.theme = mode.normalized()
	s.syncThemeAppearance(s.editor.theme)
}

func (s *application) setTheme(mode themeMode) {
	if s.settingsOpen || s.confirmOverwrite || s.confirmBatchOverwrite {
		return
	}
	if mode.validate() != nil {
		return
	}
	mode = mode.normalized()
	if s.effectiveThemeMode() == mode {
		return
	}
	if s.settingsFile != "" && s.settingsError == "" {
		if err := s.saveCurrentSettings(s.presets, s.scheme, s.batchOutputDir, mode); err != nil {
			s.setMessage("保存主题设置失败："+err.Error(), true)
			return
		}
	}
	s.syncTheme(mode)
}

func applyTheme(c *ui.Context) {
	applyThemeMode(c, themeSystem)
}

func (s *application) applyTheme(c *ui.Context) {
	mode := s.effectiveThemeMode()
	if s.settingsOpen && s.editor != nil {
		mode = s.editor.theme
	}
	applyThemeMode(c, mode)
}

func applyThemeMode(c *ui.Context, mode themeMode) {
	dark := c.Theme().Dark
	switch mode.normalized() {
	case themeLight:
		dark = false
	case themeDark:
		dark = true
	}
	base := ui.LightTheme()
	if dark {
		base = ui.DarkTheme()
	}
	theme := base.WithAccent(ui.Hex("#2563eb"))
	theme.Radius, theme.FontSize = 8, 13
	theme.AccentHover = ui.Hex("#1d4ed8")
	theme.AccentPressed = ui.Hex("#1e40af")
	if theme.Dark {
		theme.Background = ui.Hex("#141416")
		theme.Surface = ui.Hex("#1e2026")
		theme.SurfaceHover = ui.Hex("#2a2d35")
		theme.SurfacePressed = ui.Hex("#353944")
		theme.Text = ui.Hex("#ededf0")
		theme.TextMuted = ui.Hex("#b0b4c0")
		theme.Danger = ui.Hex("#ff8585")
		theme.Warning = ui.Hex("#edbc68")
		theme.Success = ui.Hex("#73d9a0")
	} else {
		theme.Background = ui.Hex("#f7f7f8")
		theme.Surface = ui.Hex("#ffffff")
		theme.SurfaceHover = ui.Hex("#f0f1f4")
		theme.SurfacePressed = ui.Hex("#e5e7ec")
		theme.Text = ui.Hex("#1c1d22")
		theme.TextMuted = ui.Hex("#555863")
		theme.Danger = ui.Hex("#b91c1c")
		theme.Warning = ui.Hex("#925408")
		theme.Success = ui.Hex("#137a39")
	}
	theme.Border = theme.Text.Alpha(0.08)
	theme.Focus = accentForeground(theme).Alpha(0.85)
	preferences := c.Preferences()
	preferences.Accent = ui.Transparent
	c.SetTheme(theme.WithPreferences(preferences))
}

func accentForeground(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.Hex("#93b4ff")
	}
	return t.Accent
}

func metadataColor(c *ui.Context) ui.Color {
	if c.Preferences().HighContrast {
		return c.Theme().TextMuted
	}
	if c.Theme().Dark {
		return ui.Hex("#969ba8")
	}
	return ui.Hex("#626875")
}

func stageBackground(c *ui.Context) ui.Color {
	if c.Theme().Dark {
		return ui.Hex("#101216")
	}
	return ui.Hex("#eff0f3")
}

func subtleSurface(c *ui.Context) ui.Color {
	if c.Theme().Dark {
		return ui.Hex("#181a1f")
	}
	return ui.Hex("#fafafb")
}

func selectionBackground(c *ui.Context) ui.Color {
	t := c.Theme()
	if t.Dark {
		return t.Surface.Mix(t.Accent, 0.14)
	}
	return t.Surface.Mix(t.Accent, 0.07)
}

func selectionBorder(c *ui.Context) ui.Color {
	t := c.Theme()
	if c.Preferences().HighContrast {
		return accentForeground(t)
	}
	if t.Dark {
		return accentForeground(t).Alpha(0.65)
	}
	return t.Accent.Alpha(0.42)
}

func usesGlass(c *ui.Context) bool {
	return c.Vibrancy() && !c.Preferences().ReduceTransparency && !c.Preferences().HighContrast
}

func windowRootBackground(c *ui.Context) ui.Color {
	if usesGlass(c) {
		c.Root().Background(ui.Transparent)
		return ui.Transparent
	}
	bg := c.Theme().Background
	c.Root().Background(bg)
	return bg
}

func panelRadius(c *ui.Context) float32 {
	if usesGlass(c) {
		return 16
	}
	return 12
}

func innerRadius(c *ui.Context) float32 {
	if usesGlass(c) {
		return 12
	}
	return 8
}

func pillRadius(height float32, c *ui.Context) float32 {
	if usesGlass(c) {
		return height / 2
	}
	return 8
}

func panelSurface(c *ui.Context) ui.Color {
	t := c.Theme()
	if !usesGlass(c) {
		return t.Surface
	}
	if t.Dark {
		return t.Surface.Alpha(0.64)
	}
	return t.Surface.Alpha(0.70)
}

func panelSubtleSurface(c *ui.Context) ui.Color {
	if !usesGlass(c) {
		return subtleSurface(c)
	}
	if c.Theme().Dark {
		return ui.RGBA(255, 255, 255, 0.06)
	}
	return ui.RGBA(255, 255, 255, 0.52)
}

func panelStageBackground(c *ui.Context) ui.Color {
	if !usesGlass(c) {
		return stageBackground(c)
	}
	if c.Theme().Dark {
		return ui.RGBA(12, 13, 16, 0.52)
	}
	return ui.RGBA(239, 240, 243, 0.58)
}

func glassControlSurface(c *ui.Context) ui.Color {
	t := c.Theme()
	if !usesGlass(c) {
		return t.Surface
	}
	if t.Dark {
		return ui.RGBA(255, 255, 255, 0.09)
	}
	return ui.RGBA(255, 255, 255, 0.68)
}

func glassControlHover(c *ui.Context) ui.Color {
	t := c.Theme()
	if !usesGlass(c) {
		return t.SurfaceHover
	}
	if t.Dark {
		return ui.RGBA(255, 255, 255, 0.16)
	}
	return ui.RGBA(255, 255, 255, 0.86)
}

func glassControlPressed(c *ui.Context) ui.Color {
	t := c.Theme()
	if !usesGlass(c) {
		return t.SurfacePressed
	}
	if t.Dark {
		return ui.RGBA(255, 255, 255, 0.22)
	}
	return ui.RGBA(255, 255, 255, 0.94)
}

// Small passive surfaces stay opaque so they remain legible over native glass.
func keycapSurface(c *ui.Context) ui.Color {
	t := c.Theme()
	return t.Surface.Mix(t.Text, 0.045)
}

func keyboardKeycap(c *ui.Context, key string) ui.Element {
	return ui.Text(c, shortcutLabel(key)).FontSize(12).FontFeatures("tnum").
		TextColor(c.Theme().TextMuted).Padding(2, 8).Radius(4).
		Background(keycapSurface(c)).Border(1, controlBorder(c)).SingleLine()
}

func controlBorder(c *ui.Context) ui.Color {
	t := c.Theme()
	if c.Preferences().HighContrast {
		return t.Border
	}
	if t.Dark {
		return t.Text.Alpha(0.22)
	}
	return t.Text.Alpha(0.18)
}

func glassBorder(c *ui.Context) ui.Color {
	t := c.Theme()
	if !usesGlass(c) {
		return t.Border
	}
	if t.Dark {
		return ui.RGBA(255, 255, 255, 0.15)
	}
	return t.Text.Alpha(0.09)
}

func glassDivider(c *ui.Context) ui.Color {
	t := c.Theme()
	if !usesGlass(c) {
		return t.Border
	}
	if t.Dark {
		return ui.RGBA(255, 255, 255, 0.10)
	}
	return t.Text.Alpha(0.08)
}

func glassSelectionBackground(c *ui.Context) ui.Color {
	t := c.Theme()
	if !usesGlass(c) {
		return selectionBackground(c)
	}
	if t.Dark {
		return t.Accent.Alpha(0.24)
	}
	return t.Accent.Alpha(0.14)
}

func styleGlassSegmented(el ui.Element, c *ui.Context, height float32) ui.Element {
	if usesGlass(c) {
		el.Radius(height/2).Background(panelStageBackground(c)).Border(1, glassBorder(c))
	}
	return el
}

func styleGlassField(el ui.Element, c *ui.Context, height float32) ui.Element {
	return el.Radius(min(innerRadius(c), height/2)).Background(glassControlSurface(c)).Border(1, controlBorder(c))
}

func quietButton(c *ui.Context, key, label string, height float32, disabled bool) ui.Element {
	t := c.Theme()
	btn := ui.Button(c.Key(key), label).Height(height).Radius(pillRadius(height, c)).
		Background(glassControlSurface(c)).Border(1, controlBorder(c)).TextColor(t.Text).Disabled(disabled)
	if !disabled {
		if btn.Pressed() {
			btn.Background(glassControlPressed(c))
		} else if btn.Hovered() {
			btn.Background(glassControlHover(c))
		}
	}
	return btn
}

func quietToolbarButton(c *ui.Context, key, label string, disabled bool) ui.Element {
	t := c.Theme()
	btn := ui.Button(c.Key(key), label).Height(32).Radius(pillRadius(32, c)).
		Background(ui.Transparent).Border(1, ui.Transparent).TextColor(t.TextMuted).Disabled(disabled)
	if !disabled {
		if btn.Pressed() {
			btn.Background(glassControlPressed(c)).TextColor(t.Text)
		} else if btn.Hovered() {
			btn.Background(glassControlHover(c)).TextColor(t.Text)
		}
	}
	return btn
}
