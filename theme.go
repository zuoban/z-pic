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
		theme.Surface = ui.Hex("#1d1d20")
		theme.SurfaceHover = ui.Hex("#29292e")
		theme.SurfacePressed = ui.Hex("#34343a")
		theme.Text = ui.Hex("#ededee")
		theme.TextMuted = ui.Hex("#b0b0b8")
	} else {
		theme.Background = ui.Hex("#f7f7f8")
		theme.Surface = ui.Hex("#ffffff")
		theme.SurfaceHover = ui.Hex("#f0f0f3")
		theme.SurfacePressed = ui.Hex("#e6e6eb")
		theme.Text = ui.Hex("#202025")
		theme.TextMuted = ui.Hex("#565660")
		theme.Success = ui.Hex("#15803d")
	}
	theme.Border = theme.Text.Alpha(0.08)
	c.SetTheme(theme)
}

func (s *application) themeControl(c *ui.Context) {
	current := s.effectiveThemeMode()
	disabled := s.settingsOpen || s.confirmOverwrite || s.confirmBatchOverwrite
	ui.PopoverMenuButton(c.Key("theme-picker"), "主题", func(m *ui.Menu) {
		for _, option := range themeOptions {
			if m.Item(option.label).Radio(current == option.mode).Disabled(disabled).Chosen() {
				s.setTheme(option.mode)
			}
		}
	}, ui.PopoverMenuButtonOptions{
		At: ui.AnchorBottomEnd, Self: ui.AnchorTopEnd,
	}).Label("主题").Tooltip("外观主题：" + current.label()).Height(32).Disabled(disabled)
}
