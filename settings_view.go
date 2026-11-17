package main

import "github.com/egoist/mygo/ui"

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
		ui.Text(c, "当前预设").FontSize(13).TextColor(t.TextMuted)
		preset := editor.presets[editor.active]
		ui.PopoverMenuButton(c.Key("settings-preset-picker"), preset.name, func(menu *ui.Menu) {
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
		}).Label("选择预设").Tooltip("保存后使用："+preset.name).
			Width(0).Grow(1).MinWidth(0).Height(36).Radius(pillRadius(36, c)).
			Background(glassControlSurface(c)).Border(1, glassBorder(c)).
			Disabled(s.busy || editor.confirmDelete)
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
		ui.Segmented(c.Key("settings-navigation"), &section, "通用", "压缩预设").
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
			selected := 0
			for index, option := range themeOptions {
				if option.mode == s.editor.theme.normalized() {
					selected = index
				}
			}
			choice := ui.Segmented(c.Key("settings-theme"), &selected, "跟随系统", "浅色", "深色").
				Label("外观主题").FillEqually().Height(36).Disabled(s.busy)
			if choice.Changed() {
				s.previewSettingsTheme(themeOptions[selected].mode)
			}
			ui.Text(c, "立即预览外观，保存后记住选择；取消可恢复原主题。").
				FontSize(12).TextColor(metadataColor(c))
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
