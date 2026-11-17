package main

import "github.com/egoist/mygo/ui"

var (
	settingsChevronIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="m5 6.5 3 3 3-3"/></svg>`))
	settingsSystemIcon  = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><rect x="2.5" y="3.5" width="15" height="10" rx="2"/><path d="M10 13.5v3M6.5 16.5h7"/></svg>`))
	settingsLightIcon   = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"><circle cx="10" cy="10" r="3.25"/><path d="M10 2v1.5M10 16.5V18M2 10h1.5M16.5 10H18M4.35 4.35l1.1 1.1m9.1 9.1 1.1 1.1M4.35 15.65l1.1-1.1m9.1-9.1 1.1-1.1"/></svg>`))
	settingsDarkIcon    = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="M16.8 11.8A7 7 0 0 1 8.2 3.2a7 7 0 1 0 8.6 8.6Z"/></svg>`))
)

func (s *application) settingsView(c *ui.Context) {
	s.applyTheme(c)
	if s.editor == nil || s.editor.selectedPreset() == nil {
		return
	}
	c.OnWindowShortcut(0, ui.KeyEscape, func() { s.setSettingsOpen(false) })
	c.OnWindowShortcut(ui.Cmd, ui.KeyS, s.saveSettingsEdits)
	ui.Column(c).Fill().Background(windowRootBackground(c)).Role(ui.RoleDialog).Label("应用设置").Children(func() {
		s.settingsNavigation(c)
		ui.Box(c).FillWidth().Height(1).Background(glassDivider(c))
		s.settingsPresetSelection(c)
		ui.Box(c).FillWidth().Height(1).Background(glassDivider(c))
		if s.editor.section == settingsPresets {
			s.presetSettingsContent(c)
		} else {
			s.generalSettingsContent(c)
		}
		s.settingsFooter(c)
	})
}

func (s *application) settingsPresetSelection(c *ui.Context) {
	editor := s.editor
	t := c.Theme()
	ui.Row(c).FillWidth().Height(64).Padding(12, 16).Gap(12).AlignItems(ui.Center).
		Background(panelSurface(c)).Children(func() {
		ui.Text(c, "当前预设").FontSize(13).TextColor(t.TextMuted).Shrink(0)
		preset := editor.presets[editor.active]
		disabled := s.busy || editor.confirmDelete
		picker := ui.ButtonBase(c.Key("settings-preset-picker"))
		picker.PopoverMenuButton(func(menu *ui.Menu) {
			menu.MaxWidth(360)
			for index, option := range editor.presets {
				item := menu.Item(option.name).Radio(editor.active == index).
					Tooltip(option.name).Disabled(s.busy || editor.confirmDelete)
				if item.Chosen() {
					s.selectSettingsPreset(index)
				}
			}
		}, ui.PopoverMenuButtonOptions{
			At: ui.AnchorBottomStart, Self: ui.AnchorTopStart,
			Panel: func(panel ui.Element) {
				panel.MinWidth(max(180, picker.Bounds().W)).MaxWidth(360).
					Radius(12).Background(t.Surface).Border(1, controlBorder(c)).
					NoShadow().Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.14))
			},
		}).Label("选择预设").Tooltip("保存后使用："+preset.name).
			Width(0).Grow(1).MinWidth(0).MaxWidth(360).Height(36).Padding(0, 12).Gap(8).
			AlignItems(ui.Center).Radius(innerRadius(c)).
			Background(glassControlSurface(c)).Border(1, controlBorder(c)).Disabled(disabled)
		if !disabled {
			if picker.Pressed() {
				picker.Background(glassControlPressed(c))
			} else if picker.Hovered() {
				picker.Background(glassControlHover(c))
			}
		}
		picker.Children(func() {
			ui.Text(c, preset.name).FontSize(13).FontWeight(500).SingleLine().TruncateMiddle().
				Width(0).Grow(1).MinWidth(0)
			ui.Icon(c, settingsChevronIcon).Size(16, 16).Shrink(0).TextColor(t.TextMuted)
		})
		ui.Box(c).Grow(1).MinWidth(0)
		if editor.section == settingsGeneral {
			quietButton(c, "manage-presets", "管理预设", 32, s.busy).OnClick(func() {
				editor.selected = editor.active
				editor.section = settingsPresets
			})
		}
	})
}

func (s *application) settingsNavigation(c *ui.Context) {
	ui.Row(c).FillWidth().Height(56).Padding(0, 16).Gap(16).AlignItems(ui.Center).
		Background(panelSurface(c)).Children(func() {
		ui.Text(c, "设置").FontSize(16).FontWeight(600).Grow(1)
		section := int(s.editor.section)
		ui.Segmented(c.Key("settings-navigation"), &section, "通用", "预设").
			Label("设置分类").FillEqually().Width(224).Height(32).Disabled(s.busy || s.editor.confirmDelete)
		s.editor.section = settingsSection(section)
	})
}

func (s *application) generalSettingsContent(c *ui.Context) {
	t := c.Theme()
	ui.Scroll(c.Key("general-settings")).FillWidth().Grow(1).MinHeight(0).Padding(24).Gap(24).Children(func() {
		ui.Column(c).FillWidth().Gap(4).Children(func() {
			ui.Text(c, "通用").FontSize(24).FontWeight(600)
			ui.Text(c, "应用外观与常用快捷键").FontSize(13).TextColor(t.TextMuted)
		})
		ui.Column(c).FillWidth().Padding(16).Gap(12).Radius(panelRadius(c)).
			Background(panelSurface(c)).Border(1, glassBorder(c)).Children(func() {
			ui.Text(c, "外观").FontSize(16).FontWeight(500)
			s.settingsThemeSelection(c)
			ui.Text(c, "立即预览外观，保存后记住选择；取消可恢复原主题。").
				FontSize(12).TextColor(metadataColor(c)).MaxLines(2)
		})
		ui.Column(c).FillWidth().Gap(12).Children(func() {
			ui.Text(c, "快捷键").FontSize(16).FontWeight(500)
			for _, shortcut := range []struct{ label, key string }{
				{"选择图片", "O"}, {"开始或继续压缩", "↵"}, {"保存结果 / 选择输出位置", "S"}, {"打开设置", ","},
			} {
				ui.Row(c).FillWidth().Gap(16).AlignItems(ui.Center).Children(func() {
					ui.Text(c, shortcut.label).FontSize(13).TextColor(t.TextMuted).Grow(1)
					ui.Text(c, shortcutLabel(shortcut.key)).FontSize(12).FontFeatures("tnum").
						Padding(4, 8).Radius(4).Background(glassControlSurface(c)).
						Border(1, glassBorder(c)).TextColor(t.TextMuted)
				})
			}
		})
	})
}

func (s *application) settingsThemeSelection(c *ui.Context) {
	t := c.Theme()
	selected := 0
	for index, option := range themeOptions {
		if option.mode == s.editor.theme.normalized() {
			selected = index
		}
	}
	icons := [...]*ui.SVG{settingsSystemIcon, settingsLightIcon, settingsDarkIcon}
	choice := ui.SegmentedBase(c.Key("settings-theme"), &selected, len(themeOptions))
	choice.Track.Label("外观主题").Height(80).Gap(8).AlignItems(ui.Stretch).Disabled(s.busy).
		Children(func() {
			for index, option := range themeOptions {
				active := index == selected
				segment := choice.Segment(index).Label(option.label).Width(0).MinWidth(0).
					Radius(innerRadius(c)).Padding(12, 8).
					Background(glassControlSurface(c)).Border(1, controlBorder(c))
				color := t.TextMuted
				if active {
					color = accentForeground(t)
					segment.Background(glassSelectionBackground(c)).Border(1, selectionBorder(c))
				} else if !s.busy {
					if segment.Pressed() {
						segment.Background(glassControlPressed(c))
					} else if segment.Hovered() {
						segment.Background(glassControlHover(c))
					}
				}
				segment.Children(func() {
					ui.Column(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Icon(c, icons[index]).Size(20, 20).TextColor(color)
						ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
							ui.Text(c, option.label).FontSize(13).FontWeight(500).SingleLine().TextColor(t.Text)
							if active {
								ui.Icon(c, presetCheckIcon).Size(12, 12).TextColor(color)
							}
						})
					})
				})
			}
		}).FillEqually()
	if choice.Track.Changed() {
		s.previewSettingsTheme(themeOptions[selected].mode)
	}
}

func (s *application) settingsFooter(c *ui.Context) {
	t, editor := c.Theme(), s.editor
	width, _ := c.Size()
	padding := float32(24)
	if width < 560 {
		padding = 16
	}
	ui.Box(c).FillWidth().Height(1).Background(glassDivider(c))
	if editor.confirmDelete {
		ui.Row(c).Padding(12, padding).Gap(8).Background(panelSubtleSurface(c)).AlignItems(ui.Center).Children(func() {
			ui.Textf(c, "删除「%s」？保存后生效。", editor.selectedPreset().name).
				FontSize(12).Width(0).Grow(1).SingleLine().TruncateMiddle()
			quietButton(c, "cancel-delete", "保留预设", 32, false).
				OnClick(func() { editor.confirmDelete = false })
			quietButton(c, "confirm-delete", "确认删除", 32, false).TextColor(t.Danger).
				OnClick(s.deletePreset)
		})
		ui.Box(c).FillWidth().Height(1).Background(glassDivider(c))
	}
	ui.Column(c).Padding(12, padding).Gap(8).Background(panelSurface(c)).Children(func() {
		message, color, role := "更改将在保存后生效", t.TextMuted, ui.RoleStatus
		if editor.presets[editor.active].name != s.scheme {
			message = "保存后使用：" + editor.presets[editor.active].name
		}
		if editor.message != "" {
			message, color, role = editor.message, t.Danger, ui.RoleAlert
		}
		ui.Text(c, message).FontSize(12).TextColor(color).Role(role).MaxLines(2).Tooltip(message)
		ui.Row(c).FillWidth().Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
			if editor.section == settingsPresets {
				quietButton(c, "duplicate-preset", "复制预设", 36, s.busy).OnClick(s.duplicatePreset)
				quietButton(c, "delete-preset", "删除预设", 36, s.busy || len(editor.presets) <= 1).
					TextColor(t.Danger).OnClick(s.requestDeletePreset)
			}
			ui.Box(c).Grow(1)
			quietButton(c, "cancel-settings", "取消", 36, s.busy).Width(72).
				OnClick(func() { s.setSettingsOpen(false) })
			primaryAction(c, "save-settings", "保存", s.busy || editor.confirmDelete).Width(88).Height(36).
				KeyShortcuts(ui.Cmd, ui.KeyS).OnClick(s.saveSettingsEdits)
		})
	})
}
