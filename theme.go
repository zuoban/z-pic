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

	productAccentHex         = "#1d5bd6"
	lightWindowBackgroundHex = "#eef1f6"
	darkWindowBackgroundHex  = "#12151c"
	windowBackgroundCSS      = "light-dark(" + lightWindowBackgroundHex + ", " + darkWindowBackgroundHex + ")"
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
	theme := base.WithAccent(ui.Hex(productAccentHex))
	theme.Radius, theme.FontSize = 8, 13
	theme.AccentHover = ui.Hex("#1649b8")
	theme.AccentPressed = ui.Hex("#113a96")
	if theme.Dark {
		theme.Background = ui.Hex(darkWindowBackgroundHex)
		theme.Surface = ui.Hex("#1b202b")
		theme.SurfaceHover = ui.Hex("#2d3546")
		theme.SurfacePressed = ui.Hex("#323b4e")
		theme.Text = ui.Hex("#edf1f8")
		theme.TextMuted = ui.Hex("#b2bbcd")
		theme.Danger = ui.Hex("#ff858d")
		theme.Warning = ui.Hex("#f2be63")
		theme.Success = ui.Hex("#55df9d")
		theme.Border = theme.Text.Alpha(0.10)
	} else {
		theme.Background = ui.Hex(lightWindowBackgroundHex)
		theme.Surface = ui.Hex("#ffffff")
		theme.SurfaceHover = ui.Hex("#e8eef7")
		theme.SurfacePressed = ui.Hex("#dce4f2")
		theme.Text = ui.Hex("#141923")
		theme.TextMuted = ui.Hex("#465063")
		theme.Danger = ui.Hex("#b91c2c")
		theme.Warning = ui.Hex("#8f4f06")
		theme.Success = ui.Hex("#0d7344")
		theme.Border = theme.Text.Alpha(0.09)
	}
	theme.Focus = accentForeground(theme).Alpha(0.85)
	preferences := c.Preferences()
	preferences.Accent = ui.Transparent
	c.SetTheme(theme.WithPreferences(preferences))
}

func accentForeground(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.Hex("#80b3ff")
	}
	return ui.Hex("#1850c4")
}

func metadataColor(c *ui.Context) ui.Color {
	if c.Preferences().HighContrast {
		return c.Theme().TextMuted
	}
	if c.Theme().Dark {
		return ui.Hex("#9fa9be")
	}
	return ui.Hex("#576175")
}

func stageBackground(c *ui.Context) ui.Color {
	if c.Theme().Dark {
		return ui.Hex("#0f1012")
	}
	return ui.Hex("#e9eaec")
}

func subtleSurface(c *ui.Context) ui.Color {
	if c.Theme().Dark {
		return ui.Hex("#161a23")
	}
	return ui.Hex("#f3f5fa")
}

func controlSurface(c *ui.Context) ui.Color {
	if c.Theme().Dark {
		return ui.Hex("#242a38")
	}
	return ui.Hex("#f3f6fb")
}

func fieldSurface(c *ui.Context) ui.Color {
	if c.Theme().Dark {
		return ui.Hex("#141821")
	}
	return ui.Hex("#f5f7fb")
}

func cardSurface(c *ui.Context) ui.Color {
	if c.Theme().Dark {
		return ui.Hex("#212734")
	}
	return ui.Hex("#ffffff")
}

func selectionBackground(c *ui.Context) ui.Color {
	t := c.Theme()
	if t.Dark {
		return t.Surface.Mix(t.Accent, 0.16)
	}
	return t.Surface.Mix(t.Accent, 0.08)
}

func selectionBorder(c *ui.Context) ui.Color {
	t := c.Theme()
	if c.Preferences().HighContrast {
		return accentForeground(t)
	}
	if t.Dark {
		return accentForeground(t).Alpha(0.68)
	}
	return accentForeground(t).Alpha(0.46)
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
		return t.Surface.Alpha(0.68)
	}
	return t.Surface.Alpha(0.74)
}

func panelSubtleSurface(c *ui.Context) ui.Color {
	if !usesGlass(c) {
		return subtleSurface(c)
	}
	if c.Theme().Dark {
		return ui.RGBA(15, 18, 26, 0.44)
	}
	return ui.RGBA(243, 245, 250, 0.58)
}

func panelStageBackground(c *ui.Context) ui.Color {
	if !usesGlass(c) {
		return stageBackground(c)
	}
	if c.Theme().Dark {
		return ui.RGBA(12, 13, 15, 0.56)
	}
	return ui.RGBA(233, 234, 236, 0.62)
}

func glassControlSurface(c *ui.Context) ui.Color {
	if !usesGlass(c) {
		return controlSurface(c)
	}
	if c.Theme().Dark {
		return ui.RGBA(255, 255, 255, 0.10)
	}
	return ui.RGBA(255, 255, 255, 0.72)
}

func glassFieldSurface(c *ui.Context) ui.Color {
	if !usesGlass(c) {
		return fieldSurface(c)
	}
	if c.Theme().Dark {
		return ui.RGBA(10, 13, 19, 0.46)
	}
	return ui.RGBA(255, 255, 255, 0.64)
}

func glassCardSurface(c *ui.Context) ui.Color {
	if !usesGlass(c) {
		return cardSurface(c)
	}
	if c.Theme().Dark {
		return ui.RGBA(255, 255, 255, 0.09)
	}
	return ui.RGBA(255, 255, 255, 0.76)
}

func glassControlHover(c *ui.Context) ui.Color {
	t := c.Theme()
	if !usesGlass(c) {
		return t.SurfaceHover
	}
	if t.Dark {
		return ui.RGBA(255, 255, 255, 0.17)
	}
	return ui.RGBA(255, 255, 255, 0.88)
}

func glassControlPressed(c *ui.Context) ui.Color {
	t := c.Theme()
	if !usesGlass(c) {
		return t.SurfacePressed
	}
	if t.Dark {
		return ui.RGBA(255, 255, 255, 0.23)
	}
	return ui.RGBA(232, 238, 248, 0.94)
}

func meterTrackColor(c *ui.Context) ui.Color {
	t := c.Theme()
	if c.Preferences().HighContrast {
		return t.Border
	}
	if t.Dark {
		return t.Text.Alpha(0.16)
	}
	return t.Text.Alpha(0.12)
}

func statusTintSurface(c *ui.Context, semantic ui.Color) ui.Color {
	t := c.Theme()
	if usesGlass(c) {
		if t.Dark {
			return semantic.Alpha(0.16)
		}
		return semantic.Alpha(0.11)
	}
	if t.Dark {
		return t.Surface.Mix(semantic, 0.14)
	}
	return t.Surface.Mix(semantic, 0.09)
}

func statusTintBorder(c *ui.Context, semantic ui.Color) ui.Color {
	if c.Preferences().HighContrast {
		return semantic
	}
	if c.Theme().Dark {
		return semantic.Alpha(0.32)
	}
	return semantic.Alpha(0.26)
}

// Small passive surfaces stay opaque so they remain legible over native glass.
func keycapSurface(c *ui.Context) ui.Color {
	t := c.Theme()
	return t.Surface.Mix(t.Text, 0.05)
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
	return t.Accent.Alpha(0.13)
}

func styleGlassSegmented(el ui.Element, c *ui.Context, height float32) ui.Element {
	if usesGlass(c) {
		el.Radius(height/2).Background(panelSubtleSurface(c)).Border(1, glassBorder(c))
	} else {
		el.Background(panelSubtleSurface(c)).Border(1, glassBorder(c))
	}
	return el
}

func styleGlassField(el ui.Element, c *ui.Context, height float32) ui.Element {
	return el.Radius(min(innerRadius(c), height/2)).Background(glassFieldSurface(c)).Border(1, controlBorder(c))
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

func destructiveAction(c *ui.Context, key, label string, height float32, disabled bool) ui.Element {
	button := quietButton(c, key, label, height, disabled)
	if disabled {
		return button
	}
	t := c.Theme()
	mix := float32(0.06)
	if button.Pressed() {
		mix = 0.16
	} else if button.Hovered() {
		mix = 0.10
	}
	border := t.Danger.Alpha(0.35)
	if c.Preferences().HighContrast {
		border = t.Danger
	}
	return button.Background(panelSurface(c).Mix(t.Danger, mix)).Border(1, border).TextColor(t.Danger)
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
