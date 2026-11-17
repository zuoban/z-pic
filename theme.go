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
	if s.applyNativeTheme != nil {
		s.applyNativeTheme(s.theme.source())
	} else if s.window != nil {
		mygo.Theme.SetSource(s.theme.source())
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
	applyThemeMode(c, s.effectiveThemeMode())
}

func applyThemeMode(c *ui.Context, mode themeMode) {
	base := c.Theme()
	switch mode.normalized() {
	case themeLight:
		base = ui.LightTheme().WithPreferences(c.Preferences())
	case themeDark:
		base = ui.DarkTheme().WithPreferences(c.Preferences())
	}
	theme := base.WithAccent(ui.Hex("#2563eb"))
	theme.Radius, theme.FontSize = 8, 13
	if theme.Dark {
		theme.Background = ui.Hex("#141416")
		theme.Surface = ui.Hex("#1c1d21")
		theme.SurfaceHover = ui.Hex("#26282e")
		theme.SurfacePressed = ui.Hex("#30333b")
		theme.Text = ui.Hex("#ededf0")
		theme.TextMuted = ui.Hex("#a6a8b3")
	} else {
		theme.Background = ui.Hex("#f7f7f8")
		theme.Surface = ui.Hex("#ffffff")
		theme.SurfaceHover = ui.Hex("#f0f1f4")
		theme.SurfacePressed = ui.Hex("#e5e7ec")
		theme.Text = ui.Hex("#1c1d22")
		theme.TextMuted = ui.Hex("#555863")
		theme.Success = ui.Hex("#15803d")
	}
	theme.Border = theme.Text.Alpha(0.08)
	c.SetTheme(theme)
}

func metadataColor(c *ui.Context) ui.Color {
	if c.Theme().Dark {
		return ui.Hex("#858894")
	}
	return ui.Hex("#70737f")
}

func stageBackground(c *ui.Context) ui.Color {
	if c.Theme().Dark {
		return ui.Hex("#101114")
	}
	return ui.Hex("#eff0f3")
}

func subtleSurface(c *ui.Context) ui.Color {
	if c.Theme().Dark {
		return ui.Hex("#17181c")
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
	if t.Dark {
		return t.Accent.Alpha(0.55)
	}
	return t.Accent.Alpha(0.42)
}

func quietButton(c *ui.Context, key, label string, height float32, disabled bool) ui.Element {
	t := c.Theme()
	btn := ui.Button(c.Key(key), label).Height(height).Radius(8).
		Background(t.Surface).Border(1, t.Border).TextColor(t.Text).Disabled(disabled)
	if !disabled {
		if btn.Pressed() {
			btn.Background(t.SurfacePressed)
		} else if btn.Hovered() {
			btn.Background(t.SurfaceHover)
		}
	}
	return btn
}

func quietToolbarButton(c *ui.Context, key, label string) ui.Element {
	t := c.Theme()
	btn := ui.Button(c.Key(key), label).Height(32).Radius(8).
		Background(ui.Transparent).Border(1, ui.Transparent).TextColor(t.TextMuted)
	if btn.Hovered() && !btn.IsDisabled() {
		btn.Background(t.SurfaceHover).TextColor(t.Text)
	}
	return btn
}

func (s *application) themeControl(c *ui.Context) {
	t := c.Theme()
	current := s.effectiveThemeMode()
	disabled := s.settingsOpen || s.confirmOverwrite || s.confirmBatchOverwrite
	btn := ui.PopoverMenuButton(c.Key("theme-picker"), "主题", func(m *ui.Menu) {
		for _, option := range themeOptions {
			if m.Item(option.label).Radio(current == option.mode).Disabled(disabled).Chosen() {
				s.setTheme(option.mode)
			}
		}
	}, ui.PopoverMenuButtonOptions{
		At: ui.AnchorBottomEnd, Self: ui.AnchorTopEnd,
	}).Label("主题").Tooltip("外观主题："+current.label()).Height(32).Radius(8).
		Background(ui.Transparent).Border(1, ui.Transparent).TextColor(t.TextMuted).Disabled(disabled)
	if btn.Hovered() && !disabled {
		btn.Background(t.SurfaceHover).TextColor(t.Text)
	}
}
