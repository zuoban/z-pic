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

	productAccentHex         = "#215be6"
	lightWindowBackgroundHex = "#f4f5f7"
	darkWindowBackgroundHex  = "#0e1014"
	windowBackgroundCSS      = "light-dark(" + lightWindowBackgroundHex + ", " + darkWindowBackgroundHex + ")"
	windowVibrancyTintCSS    = "light-dark(rgba(244, 245, 247, 0.62), rgba(14, 16, 20, 0.62))"
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
	theme.AccentHover = ui.Hex("#1a4ec7")
	theme.AccentPressed = ui.Hex("#1540a8")
	theme.ScrollbarWidth = 6
	if theme.Dark {
		theme.Background = ui.Hex(darkWindowBackgroundHex)
		theme.Surface = ui.Hex("#161920")
		theme.SurfaceHover = ui.Hex("#282e3a")
		theme.SurfacePressed = ui.Hex("#2f3644")
		theme.Text = ui.Hex("#f1f3f8")
		theme.TextMuted = ui.Hex("#c2cad7")
		theme.Danger = ui.Hex("#ff808a")
		theme.Warning = ui.Hex("#f4be5c")
		theme.Success = ui.Hex("#4ee09a")
		theme.Border = theme.Text.Alpha(0.09)
		theme.Selection = accentForeground(theme).Alpha(0.28)
		theme.Scrollbar = theme.Text.Alpha(0.20)
	} else {
		theme.Background = ui.Hex(lightWindowBackgroundHex)
		theme.Surface = ui.Hex("#ffffff")
		theme.SurfaceHover = ui.Hex("#eef1f6")
		theme.SurfacePressed = ui.Hex("#e5e9f0")
		theme.Text = ui.Hex("#11141a")
		theme.TextMuted = ui.Hex("#374050")
		theme.Danger = ui.Hex("#bc1c2c")
		theme.Warning = ui.Hex("#8d4b06")
		theme.Success = ui.Hex("#0c6d43")
		theme.Border = theme.Text.Alpha(0.08)
		theme.Selection = theme.Accent.Alpha(0.20)
		theme.Scrollbar = theme.Text.Alpha(0.18)
	}
	theme.Focus = accentForeground(theme).Alpha(0.85)
	preferences := c.Preferences()
	preferences.Accent = ui.Transparent
	c.SetTheme(theme.WithPreferences(preferences))
}

func accentForeground(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.Hex("#80b4ff")
	}
	return ui.Hex("#184dc0")
}

func metadataColor(c *ui.Context) ui.Color {
	if c.Preferences().HighContrast {
		return c.Theme().TextMuted
	}
	if c.Theme().Dark {
		return ui.Hex("#adb6c6")
	}
	return ui.Hex("#465063")
}

func chromeMutedColor(c *ui.Context) ui.Color {
	t := c.Theme()
	if !usesGlass(c) {
		return t.TextMuted
	}
	if t.Dark {
		return ui.Hex("#dfe5ef")
	}
	return ui.Hex("#222833")
}

func chromeMetadataColor(c *ui.Context) ui.Color {
	if !usesGlass(c) {
		return metadataColor(c)
	}
	if c.Theme().Dark {
		return ui.Hex("#cfd7e4")
	}
	return ui.Hex("#2d3442")
}

func stageBackground(c *ui.Context) ui.Color {
	if c.Theme().Dark {
		return ui.Hex("#0a0b0c")
	}
	return ui.Hex("#eeeeef")
}

func emptyStageBackground(c *ui.Context) ui.Color {
	if c.Theme().Dark {
		return ui.Hex("#12151b")
	}
	return ui.Hex("#f8f9fb")
}

func subtleSurface(c *ui.Context) ui.Color {
	if c.Theme().Dark {
		return ui.Hex("#12151b")
	}
	return ui.Hex("#f7f8fa")
}

func controlSurface(c *ui.Context) ui.Color {
	if c.Theme().Dark {
		return ui.Hex("#212630")
	}
	return ui.Hex("#ffffff")
}

func fieldSurface(c *ui.Context) ui.Color {
	if c.Theme().Dark {
		return ui.Hex("#101318")
	}
	return ui.Hex("#f2f4f7")
}

func cardSurface(c *ui.Context) ui.Color {
	if c.Theme().Dark {
		return ui.Hex("#1c2028")
	}
	return ui.Hex("#ffffff")
}

func segmentedThumbSurface(c *ui.Context) ui.Color {
	if usesGlass(c) {
		if c.Theme().Dark {
			return ui.RGBA(255, 255, 255, 0.16)
		}
		return ui.RGBA(255, 255, 255, 0.92)
	}
	if c.Theme().Dark {
		return ui.Hex("#262b36")
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
		return accentForeground(t).Alpha(0.62)
	}
	return accentForeground(t).Alpha(0.42)
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

func badgeRadius(height float32) float32 {
	return height / 2
}

func panelSurface(c *ui.Context) ui.Color {
	t := c.Theme()
	if !usesGlass(c) {
		return t.Surface
	}
	if t.Dark {
		return t.Surface.Alpha(0.72)
	}
	return t.Surface.Alpha(0.78)
}

func panelSubtleSurface(c *ui.Context) ui.Color {
	if !usesGlass(c) {
		return subtleSurface(c)
	}
	if c.Theme().Dark {
		return ui.RGBA(14, 17, 23, 0.48)
	}
	return ui.RGBA(245, 247, 250, 0.64)
}

func panelStageBackground(c *ui.Context) ui.Color {
	if !usesGlass(c) {
		return stageBackground(c)
	}
	if c.Theme().Dark {
		return ui.RGBA(10, 11, 12, 0.60)
	}
	return ui.RGBA(238, 238, 239, 0.66)
}

func panelEmptyStageBackground(c *ui.Context) ui.Color {
	if !usesGlass(c) {
		return emptyStageBackground(c)
	}
	if c.Theme().Dark {
		return ui.RGBA(16, 19, 25, 0.44)
	}
	return ui.RGBA(248, 249, 251, 0.70)
}

func glassControlSurface(c *ui.Context) ui.Color {
	if !usesGlass(c) {
		return controlSurface(c)
	}
	if c.Theme().Dark {
		return ui.RGBA(255, 255, 255, 0.09)
	}
	return ui.RGBA(255, 255, 255, 0.78)
}

func glassFieldSurface(c *ui.Context) ui.Color {
	if !usesGlass(c) {
		return fieldSurface(c)
	}
	if c.Theme().Dark {
		return ui.RGBA(9, 11, 15, 0.50)
	}
	return ui.RGBA(242, 244, 248, 0.72)
}

func glassCardSurface(c *ui.Context) ui.Color {
	if !usesGlass(c) {
		return cardSurface(c)
	}
	if c.Theme().Dark {
		return ui.RGBA(255, 255, 255, 0.07)
	}
	return ui.RGBA(255, 255, 255, 0.82)
}

func glassControlHover(c *ui.Context) ui.Color {
	t := c.Theme()
	if !usesGlass(c) {
		return t.SurfaceHover
	}
	if t.Dark {
		return ui.RGBA(255, 255, 255, 0.15)
	}
	return ui.RGBA(255, 255, 255, 0.92)
}

func glassControlPressed(c *ui.Context) ui.Color {
	t := c.Theme()
	if !usesGlass(c) {
		return t.SurfacePressed
	}
	if t.Dark {
		return ui.RGBA(255, 255, 255, 0.21)
	}
	return ui.RGBA(230, 234, 242, 0.95)
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
		return t.Text.Alpha(0.14)
	}
	return t.Text.Alpha(0.12)
}

func controlHoverBorder(c *ui.Context) ui.Color {
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
		return ui.RGBA(255, 255, 255, 0.12)
	}
	return t.Text.Alpha(0.08)
}

func glassDivider(c *ui.Context) ui.Color {
	t := c.Theme()
	if !usesGlass(c) {
		return t.Border
	}
	if t.Dark {
		return ui.RGBA(255, 255, 255, 0.08)
	}
	return t.Text.Alpha(0.07)
}

func chromeDivider(c *ui.Context) ui.Color {
	if !usesGlass(c) {
		return glassDivider(c)
	}
	if c.Theme().Dark {
		return ui.RGBA(255, 255, 255, 0.16)
	}
	return c.Theme().Text.Alpha(0.16)
}

func glassSelectionBackground(c *ui.Context) ui.Color {
	t := c.Theme()
	if !usesGlass(c) {
		return selectionBackground(c)
	}
	if t.Dark {
		return t.Accent.Alpha(0.24)
	}
	return t.Accent.Alpha(0.12)
}

func segmentedThumbBorder(c *ui.Context) ui.Color {
	if c.Preferences().HighContrast {
		return selectionBorder(c)
	}
	if c.Theme().Dark {
		return ui.RGBA(255, 255, 255, 0.14)
	}
	return c.Theme().Text.Alpha(0.10)
}

func segmentedControl(c *ui.Context, key, label string, selected *int, height float32, disabled bool, options ...string) ui.Element {
	t := c.Theme()
	parts := ui.SegmentedBase(c.Key(key), selected, len(options))
	trackRadius, segRadius := innerRadius(c), float32(6)
	if usesGlass(c) {
		trackRadius, segRadius = height/2, max(6, height/2-2)
	}
	parts.Track.Label(label).Height(height).Padding(2).Gap(2).
		AlignItems(ui.Stretch).Radius(trackRadius).
		Background(panelSubtleSurface(c)).Border(1, glassBorder(c)).
		Disabled(disabled).Children(func() {
		for index, option := range options {
			active := *selected == index
			segment := parts.Segment(index).Label(option).Padding(0, 6).Radius(segRadius).
				Center().Border(1, ui.Transparent)
			textColor, weight := t.TextMuted, 400
			if active {
				textColor, weight = t.Text, 500
				segment.Background(segmentedThumbSurface(c)).Border(1, segmentedThumbBorder(c))
			} else if !disabled {
				if segment.Pressed() {
					textColor = t.Text
					segment.Background(glassControlPressed(c))
				} else if segment.Hovered() {
					textColor = t.Text
					segment.Background(glassControlHover(c))
				}
			}
			segment.Children(func() {
				ui.Text(c, option).FontSize(12).FontWeight(weight).FontFeatures("tnum").TextColor(textColor).SingleLine().NoWrap()
			})
		}
	})
	return parts.Track
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
			btn.Background(glassControlPressed(c)).Border(1, controlHoverBorder(c))
		} else if btn.Hovered() {
			btn.Background(glassControlHover(c)).Border(1, controlHoverBorder(c))
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
		Background(ui.Transparent).Border(1, ui.Transparent).TextColor(chromeMutedColor(c)).Disabled(disabled)
	if !disabled {
		if btn.Pressed() {
			btn.Background(glassControlPressed(c)).TextColor(t.Text)
		} else if btn.Hovered() {
			btn.Background(glassControlHover(c)).TextColor(t.Text)
		}
	}
	return btn
}
