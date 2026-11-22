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
		theme.Surface = ui.Hex("#161a22")
		theme.SurfaceHover = ui.Hex("#262c38")
		theme.SurfacePressed = ui.Hex("#2e3544")
		theme.Text = ui.Hex("#f1f4f9")
		theme.TextMuted = ui.Hex("#bcc5d4")
		theme.Danger = ui.Hex("#ff808a")
		theme.Warning = ui.Hex("#f4be5c")
		theme.Success = ui.Hex("#4ee09a")
		theme.Border = theme.Text.Alpha(0.08)
		theme.Selection = accentForeground(theme).Alpha(0.26)
		theme.Scrollbar = theme.Text.Alpha(0.18)
	} else {
		theme.Background = ui.Hex(lightWindowBackgroundHex)
		theme.Surface = ui.Hex("#ffffff")
		theme.SurfaceHover = ui.Hex("#edf0f5")
		theme.SurfacePressed = ui.Hex("#e3e7ef")
		theme.Text = ui.Hex("#11141a")
		theme.TextMuted = ui.Hex("#3d4657")
		theme.Danger = ui.Hex("#bc1c2c")
		theme.Warning = ui.Hex("#8d4b06")
		theme.Success = ui.Hex("#0c6d43")
		theme.Border = theme.Text.Alpha(0.07)
		theme.Selection = theme.Accent.Alpha(0.18)
		theme.Scrollbar = theme.Text.Alpha(0.16)
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
			return semantic.Alpha(0.14)
		}
		return semantic.Alpha(0.09)
	}
	if t.Dark {
		return t.Surface.Mix(semantic, 0.12)
	}
	return t.Surface.Mix(semantic, 0.07)
}

func statusTintBorder(c *ui.Context, semantic ui.Color) ui.Color {
	if c.Preferences().HighContrast {
		return semantic
	}
	if c.Theme().Dark {
		return semantic.Alpha(0.28)
	}
	return semantic.Alpha(0.20)
}

func statusCapsule(c *ui.Context, text string, semantic ui.Color) ui.Element {
	border := ui.Transparent
	if c.Preferences().HighContrast {
		border = semantic
	}
	return ui.Text(c, text).FontSize(11).FontWeight(600).FontFeatures("tnum").
		TextColor(semantic).Padding(3, 8).Radius(badgeRadius(20)).
		Background(statusTintSurface(c, semantic)).Border(1, border).
		Shrink(0)
}

func neutralCapsule(c *ui.Context, text string) ui.Element {
	border := ui.Transparent
	if c.Preferences().HighContrast {
		border = controlBorder(c)
	}
	return ui.Text(c, text).FontSize(11).FontWeight(500).FontFeatures("tnum").
		TextColor(metadataColor(c)).Padding(3, 8).Radius(badgeRadius(20)).
		Background(keycapSurface(c)).Border(1, border).
		SingleLine().Shrink(0)
}

func accentCapsule(c *ui.Context, text string) ui.Element {
	border := ui.Transparent
	if c.Preferences().HighContrast {
		border = selectionBorder(c)
	}
	return ui.Text(c, text).FontSize(11).FontWeight(600).FontFeatures("tnum").
		TextColor(accentForeground(c.Theme())).Padding(3, 8).Radius(badgeRadius(20)).
		Background(glassSelectionBackground(c)).Border(1, border).
		SingleLine().Shrink(0)
}

func cardElevation(el ui.Element, c *ui.Context) ui.Element {
	if c.Theme().Dark || c.Preferences().HighContrast || usesGlass(c) {
		return el
	}
	return el.Shadow(0, 1, 3, 0, ui.RGBA(17, 20, 26, 0.05))
}

func controlElevation(el ui.Element, c *ui.Context) ui.Element {
	if c.Theme().Dark || c.Preferences().HighContrast {
		return el
	}
	return el.Shadow(0, 1, 2, 0, ui.RGBA(17, 20, 26, 0.045))
}

func iconLabelButton(c *ui.Context, button ui.Element, label string, icon *ui.SVG, fontSize float32, weight int) ui.Element {
	iconSize := float32(14)
	if fontSize >= 13 {
		iconSize = 15
	}
	return button.Label(label).Gap(5).Children(func() {
		ui.Icon(c, icon).Size(iconSize, iconSize).Shrink(0)
		ui.Text(c, label).FontSize(fontSize).FontWeight(weight).SingleLine()
	})
}

func stylePopoverPanel(panel ui.Element, c *ui.Context) ui.Element {
	return panel.Radius(12).Background(c.Theme().Surface).Border(1, glassBorder(c)).
		NoShadow().Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.18))
}

// Small passive surfaces stay opaque so they remain legible over native glass.
func keycapSurface(c *ui.Context) ui.Color {
	t := c.Theme()
	return t.Surface.Mix(t.Text, 0.05)
}

func keyboardKeycap(c *ui.Context, key string) ui.Element {
	return ui.Text(c, shortcutLabel(key)).FontSize(11).FontWeight(500).FontFeatures("tnum").
		TextColor(c.Theme().TextMuted).Padding(2, 7).Radius(5).
		Background(keycapSurface(c)).Border(1, controlBorder(c)).SingleLine()
}

func controlBorder(c *ui.Context) ui.Color {
	t := c.Theme()
	if c.Preferences().HighContrast {
		return t.Border
	}
	if t.Dark {
		return t.Text.Alpha(0.12)
	}
	return t.Text.Alpha(0.10)
}

func controlHoverBorder(c *ui.Context) ui.Color {
	t := c.Theme()
	if c.Preferences().HighContrast {
		return t.Border
	}
	if t.Dark {
		return t.Text.Alpha(0.20)
	}
	return t.Text.Alpha(0.17)
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

func hairlineDivider(c *ui.Context) ui.Color {
	if c.Preferences().HighContrast {
		return c.Theme().Border
	}
	if usesGlass(c) {
		if c.Theme().Dark {
			return ui.RGBA(255, 255, 255, 0.11)
		}
		return c.Theme().Text.Alpha(0.09)
	}
	if c.Theme().Dark {
		return ui.RGBA(255, 255, 255, 0.08)
	}
	return c.Theme().Text.Alpha(0.08)
}

func subtleRowDivider(c *ui.Context) ui.Color {
	if c.Preferences().HighContrast {
		return c.Theme().Border
	}
	if c.Theme().Dark {
		return ui.RGBA(255, 255, 255, 0.05)
	}
	return c.Theme().Text.Alpha(0.05)
}

func sidebarSurface(c *ui.Context) ui.Color {
	t := c.Theme()
	if !usesGlass(c) {
		return t.Surface
	}
	if t.Dark {
		return t.Surface.Alpha(0.76)
	}
	return t.Surface.Alpha(0.84)
}

func tonalWellSurface(c *ui.Context) ui.Color {
	t := c.Theme()
	if !usesGlass(c) {
		if t.Dark {
			return ui.Hex("#1c212c")
		}
		return ui.Hex("#f3f5f8")
	}
	if t.Dark {
		return ui.RGBA(255, 255, 255, 0.055)
	}
	return ui.RGBA(17, 20, 26, 0.04)
}

func tonalWellBorder(c *ui.Context) ui.Color {
	if c.Preferences().HighContrast {
		return c.Theme().Border
	}
	return ui.Transparent
}

func canvasWellBackground(c *ui.Context) ui.Color {
	if !usesGlass(c) {
		if c.Theme().Dark {
			return ui.Hex("#0c0c0c")
		}
		return ui.Hex("#ededed")
	}
	if c.Theme().Dark {
		return ui.RGBA(10, 10, 10, 0.56)
	}
	return ui.RGBA(237, 237, 237, 0.62)
}

func emptyDropzoneSurface(c *ui.Context) ui.Color {
	if !usesGlass(c) {
		if c.Theme().Dark {
			return ui.Hex("#14171f")
		}
		return ui.Hex("#ffffff")
	}
	if c.Theme().Dark {
		return ui.RGBA(22, 26, 35, 0.58)
	}
	return ui.RGBA(255, 255, 255, 0.72)
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

func cardSelectionBackground(c *ui.Context) ui.Color {
	t := c.Theme()
	if !usesGlass(c) {
		if t.Dark {
			return cardSurface(c).Mix(t.Accent, 0.16)
		}
		return cardSurface(c).Mix(t.Accent, 0.07)
	}
	if t.Dark {
		return ui.RGBA(28, 34, 46, 0.88).Mix(t.Accent, 0.16)
	}
	return ui.RGBA(255, 255, 255, 0.94).Mix(t.Accent, 0.08)
}

// customSelectionListTheme suppresses MyGo's solid-accent default row/cell fill
// so custom cards keep crisp contrast and rounded corners over vibrancy chrome
// while preserving native list/grid selection, focus and accessibility states.
func customSelectionListTheme(c *ui.Context) *ui.Theme {
	lt := *c.Theme()
	lt.Radius = innerRadius(c)
	lt.Accent = ui.Transparent
	lt.InactiveAccent = ui.Color{R: 1, A: 0}
	lt.SurfaceHover = ui.Transparent
	lt.SurfacePressed = ui.Transparent
	lt.Border = ui.Transparent
	return &lt
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
	trackBg := tonalWellSurface(c)
	if !usesGlass(c) && !t.Dark {
		trackBg = ui.Hex("#e8ecf2")
	}
	parts.Track.Label(label).Height(height).Padding(2).Gap(2).
		AlignItems(ui.Stretch).Radius(trackRadius).
		Background(trackBg).Border(1, tonalWellBorder(c)).
		Disabled(disabled).Children(func() {
		for index, option := range options {
			active := *selected == index
			segment := parts.Segment(index).Label(option).Padding(0, 6).Radius(segRadius).
				Center().Border(1, ui.Transparent)
			textColor, weight := t.TextMuted, 400
			if active {
				textColor, weight = t.Text, 600
				segment.Background(segmentedThumbSurface(c)).Border(1, segmentedThumbBorder(c))
				if !t.Dark && !c.Preferences().HighContrast {
					segment.Shadow(0, 1, 2, 0, ui.RGBA(17, 20, 26, 0.08))
				}
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
		} else {
			controlElevation(btn, c)
			if btn.Hovered() {
				btn.Background(glassControlHover(c)).Border(1, controlHoverBorder(c))
			}
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

func solidDestructiveAction(c *ui.Context, key, label string, height float32, disabled bool) ui.Element {
	t := c.Theme()
	bg := ui.Hex("#c92a36")
	hoverBg := ui.Hex("#b4222e")
	pressBg := ui.Hex("#9c1b26")
	if t.Dark {
		bg = ui.Hex("#cf3340")
		hoverBg = ui.Hex("#e04350")
		pressBg = ui.Hex("#b52834")
	}
	border := ui.Transparent
	if c.Preferences().HighContrast {
		border = ui.Hex("#ffffff").Alpha(0.7)
	}
	btn := ui.Button(c.Key(key), label).Height(height).Radius(pillRadius(height, c)).
		FontWeight(500).Background(bg).Border(1, border).TextColor(ui.Hex("#ffffff")).Disabled(disabled)
	if !disabled {
		if btn.Pressed() {
			btn.Background(pressBg)
		} else if btn.Hovered() {
			btn.Background(hoverBg)
		}
	}
	return btn
}

func ghostButton(c *ui.Context, key, label string, height float32, disabled bool) ui.Element {
	t := c.Theme()
	border := ui.Transparent
	if c.Preferences().HighContrast {
		border = t.Border
	}
	btn := ui.Button(c.Key(key), label).Height(height).Radius(pillRadius(height, c)).
		Background(ui.Transparent).Border(1, border).TextColor(t.TextMuted).Disabled(disabled)
	if !disabled {
		if btn.Pressed() {
			btn.Background(glassControlPressed(c)).Border(1, controlBorder(c)).TextColor(t.Text)
		} else if btn.Hovered() {
			btn.Background(glassControlHover(c)).Border(1, controlBorder(c)).TextColor(t.Text)
		}
	}
	return btn
}

func ghostDestructiveButton(c *ui.Context, key, label string, height float32, disabled bool) ui.Element {
	t := c.Theme()
	border := ui.Transparent
	if c.Preferences().HighContrast {
		border = t.Danger
	}
	btn := ui.Button(c.Key(key), label).Height(height).Radius(pillRadius(height, c)).
		Background(ui.Transparent).Border(1, border).TextColor(t.Danger).Disabled(disabled)
	if !disabled {
		if btn.Pressed() {
			btn.Background(panelSurface(c).Mix(t.Danger, 0.16)).Border(1, t.Danger.Alpha(0.35))
		} else if btn.Hovered() {
			btn.Background(panelSurface(c).Mix(t.Danger, 0.09)).Border(1, t.Danger.Alpha(0.28))
		}
	}
	return btn
}

func quietToolbarButton(c *ui.Context, key, label string, disabled bool) ui.Element {
	t := c.Theme()
	btn := ui.Button(c.Key(key), label).Height(32).Radius(pillRadius(32, c)).
		FontSize(13).FontWeight(500).
		Background(ui.Transparent).Border(1, ui.Transparent).TextColor(chromeMutedColor(c)).Disabled(disabled)
	if !disabled {
		hoverBorder := hairlineDivider(c)
		if c.Preferences().HighContrast {
			hoverBorder = controlHoverBorder(c)
		}
		if btn.Pressed() {
			btn.Background(glassControlPressed(c)).Border(1, hoverBorder).TextColor(t.Text)
		} else if btn.Hovered() {
			btn.Background(glassControlHover(c)).Border(1, hoverBorder).TextColor(t.Text)
		}
	}
	return btn
}

// drawDashedOutline paints a subtle dashed rounded border for drag handles and
// passive drop/reveal slots so they read distinctly from clickable buttons.
func drawDashedOutline(p *ui.Painter, bounds ui.Rect, radius float32, stroke ui.Color) {
	if bounds.W <= 6 || bounds.H <= 6 {
		return
	}
	x1, y1 := bounds.X+0.5, bounds.Y+0.5
	x2, y2 := bounds.X+bounds.W-0.5, bounds.Y+bounds.H-0.5
	r := min(radius, min((x2-x1)/2, (y2-y1)/2))
	const seg, gap = float32(4), float32(4)
	for x := x1 + r; x < x2-r; x += seg + gap {
		end := min(x+seg, x2-r)
		p.Line(x, y1, end, y1, 1, stroke)
		p.Line(x, y2, end, y2, 1, stroke)
	}
	for y := y1 + r; y < y2-r; y += seg + gap {
		end := min(y+seg, y2-r)
		p.Line(x1, y, x1, end, 1, stroke)
		p.Line(x2, y, x2, end, 1, stroke)
	}
	if r >= 2 {
		c := r * 0.55
		p.StrokePolyline([][2]float32{{x1, y1 + r}, {x1 + r - c, y1 + r - c}, {x1 + r, y1}}, 1, stroke)
		p.StrokePolyline([][2]float32{{x2 - r, y1}, {x2 - r + c, y1 + r - c}, {x2, y1 + r}}, 1, stroke)
		p.StrokePolyline([][2]float32{{x1, y2 - r}, {x1 + r - c, y2 - r + c}, {x1 + r, y2}}, 1, stroke)
		p.StrokePolyline([][2]float32{{x2 - r, y2}, {x2 - r + c, y2 - r + c}, {x2, y2 - r}}, 1, stroke)
	}
}
