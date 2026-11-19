package main

import (
	"fmt"

	"github.com/egoist/mygo/ui"
)

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
	c.OnWindowShortcut(0, ui.KeyEscape, func() {
		if s.editor.confirmDelete {
			s.editor.confirmDelete = false
			s.editor.focusField = "delete-preset"
			return
		}
		s.setSettingsOpen(false)
	})
	c.OnWindowShortcut(ui.Cmd, ui.KeyS, s.saveSettingsEdits)
	ui.Column(c).Fill().Background(windowRootBackground(c)).Role(ui.RoleDialog).Label("应用设置").Children(func() {
		ui.Column(c).FillWidth().Background(panelSurface(c)).Children(func() {
			s.settingsNavigation(c)
			ui.Box(c).FillWidth().Height(1).Background(glassDivider(c))
			s.settingsPresetSelection(c)
		})
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
	ui.Row(c).FillWidth().Height(settingsControlHeight(c, 52)).Padding(0, settingsPagePadding(c)).Gap(12).Background(panelSubtleSurface(c)).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "当前预设").FontSize(settingsFontSize(c, 13)).TextColor(t.TextMuted).Shrink(0)
		preset := editor.presets[editor.active]
		disabled := s.busy || editor.confirmDelete
		picker := ui.ButtonBase(c.Key("settings-preset-picker"))
		picker.PopoverMenuButton(func(menu *ui.Menu) {
			menu.MaxWidth(360)
			for index, option := range editor.presets {
				name := presetDisplayName(option)
				item := menu.Item(name).Radio(editor.active == index).
					Tooltip(name).Disabled(s.settingsDraftLocked())
				if item.Chosen() {
					s.selectSettingsPreset(index)
				}
			}
		}, ui.PopoverMenuButtonOptions{
			At: ui.AnchorBottomStart, Self: ui.AnchorTopStart,
			Panel: func(panel ui.Element) {
				panel.MinWidth(max(180, picker.Bounds().W)).MaxWidth(360).
					Radius(12).Background(t.Surface).Border(1, glassBorder(c)).
					NoShadow().Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.18))
			},
		}).Label("选择预设").Value(presetDisplayName(preset)).Tooltip(presetDisplayName(preset)).
			Width(0).Grow(1).MinWidth(0).MaxWidth(360).Height(settingsControlHeight(c, 36)).Padding(0, 12).Gap(8).
			AlignItems(ui.Center).Radius(innerRadius(c)).
			Background(glassControlSurface(c)).Border(1, controlBorder(c)).Disabled(disabled)
		if !disabled {
			if picker.Pressed() {
				picker.Background(glassControlPressed(c))
			} else if picker.Hovered() {
				picker.Background(glassControlHover(c))
			}
		}
		settingsGlassControl(picker, c, 36, disabled, ui.Transparent)
		picker.Children(func() {
			ui.Text(c, presetDisplayName(preset)).FontSize(settingsFontSize(c, 13)).FontWeight(500).SingleLine().TruncateMiddle().
				Width(0).Grow(1).MinWidth(0)
			ui.Icon(c, settingsChevronIcon).Size(16, 16).Shrink(0).TextColor(t.TextMuted)
		})
		ui.Box(c).Grow(1).MinWidth(0)
		if editor.section == settingsGeneral {
			settingsButton(c, "manage-presets", "管理预设", 32, s.settingsDraftLocked()).OnClick(func() {
				editor.selected = editor.active
				editor.section = settingsPresets
			})
		}
	})
}

func (s *application) settingsNavigation(c *ui.Context) {
	ui.Row(c).FillWidth().Height(settingsControlHeight(c, 52)).Padding(0, settingsPagePadding(c)).Gap(16).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "设置").FontSize(settingsFontSize(c, 16)).FontWeight(600).Role(ui.RoleHeading).Level(1).Grow(1)
		section := int(s.editor.section)
		settingsSegments(c, "settings-navigation", "设置分类", &section, s.busy || s.editor.confirmDelete, "通用", "预设").
			Width(settingsFontSize(c, 200))
		s.editor.section = settingsSection(section)
	})
}

// Categories and destinations share the same quiet, inset selection treatment.
func settingsSegments(c *ui.Context, key, label string, selected *int, disabled bool, options ...string) ui.Element {
	t := c.Theme()
	parts := ui.SegmentedBase(c.Key(key), selected, len(options))
	trackH := settingsControlHeight(c, 36)
	trackRadius, segRadius := innerRadius(c), float32(6)
	if usesGlass(c) {
		trackRadius, segRadius = trackH/2, max(6, (trackH-6)/2)
	}
	parts.Track.Label(label).Height(trackH).Padding(3).Gap(2).Radius(trackRadius).
		Background(panelSubtleSurface(c)).Border(1, glassBorder(c)).
		AlignItems(ui.Stretch).Disabled(disabled).Children(func() {
		for index, option := range options {
			segment := parts.Segment(index).Label(option).Radius(segRadius).Center().
				Border(1, ui.Transparent).TextColor(t.TextMuted)
			weight := 400
			if *selected == index {
				segment.Background(segmentedThumbSurface(c)).Border(1, segmentedThumbBorder(c)).TextColor(t.Text)
				settingsGlassControl(segment, c, 30, disabled, ui.Transparent)
				weight = 500
			} else if !disabled {
				if segment.Pressed() {
					segment.Background(glassControlPressed(c))
				} else if segment.Hovered() {
					segment.Background(glassControlHover(c)).TextColor(t.Text)
				}
			}
			segment.Children(func() { ui.Text(c, option).FontSize(settingsFontSize(c, 13)).FontWeight(weight).SingleLine() })
		}
	})
	return parts.Track.FillEqually()
}

func (s *application) generalSettingsContent(c *ui.Context) {
	t := c.Theme()
	width, _ := c.Size()
	narrow := width < 560
	sideBySideWorkflow := !narrow && c.Preferences().TextScale <= 1.1
	twoColShortcuts := width >= 400 && c.Preferences().TextScale <= 1.1
	scrollPadV, scrollGap := float32(16), float32(12)
	if narrow {
		scrollPadV, scrollGap = 10, 8
	}
	ui.Scroll(c.Key("general-settings")).FillWidth().Grow(1).MinHeight(0).Padding(scrollPadV, settingsPagePadding(c)).Gap(scrollGap).Children(func() {
		settingsPanel(c, "settings-appearance-panel").Children(func() {
			ui.Text(c, "外观").FontSize(settingsFontSize(c, 16)).FontWeight(600).Role(ui.RoleHeading).Level(2)
			s.settingsThemeSelection(c)
		})
		settingsPanel(c, "settings-workflow-panel").Children(func() {
			ui.Text(c, "工作流").FontSize(settingsFontSize(c, 16)).FontWeight(600).Role(ui.RoleHeading).Level(2)
			locked := s.settingsDraftLocked()
			autoCheck := func() {
				ui.Checkbox(c.Key("settings-auto-compress"), &s.editor.autoCompress, "导入图片或切换预设后自动压缩").
					FontSize(settingsFontSize(c, 13)).Disabled(locked).
					Tooltip("单图导入、主窗切预设或非覆盖/非询问目录的批量导入将自动开始压缩")
			}
			trashCheck := func() {
				ui.Checkbox(c.Key("settings-backup-to-trash"), &s.editor.backupToTrash, "覆盖原图前移入废纸篓").
					FontSize(settingsFontSize(c, 13)).Disabled(locked).
					Tooltip("覆盖单图或批量原图前先将原文件移至系统废纸篓（回收站）")
			}
			if sideBySideWorkflow {
				ui.Row(c).FillWidth().Gap(16).AlignItems(ui.Center).Children(func() {
					ui.Box(c).Width(0).Grow(1).Children(autoCheck)
					ui.Box(c).Width(0).Grow(1).Children(trashCheck)
				})
			} else {
				autoCheck()
				trashCheck()
			}
			downloadPath := s.editor.downloadDir
			downloadTooltip := downloadPath
			if downloadPath == "" {
				downloadPath = "默认（下载/Z Pic）"
				downloadTooltip = "系统下载目录下的 Z Pic 文件夹"
			}
			ui.Row(c).FillWidth().Gap(8).Padding(6, 10).Radius(innerRadius(c)).
				Background(panelSubtleSurface(c)).Border(1, glassBorder(c)).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "下载目录").FontSize(settingsFontSize(c, 12)).TextColor(metadataColor(c)).Shrink(0)
				ui.Text(c, downloadPath).FontSize(settingsFontSize(c, 12)).TextColor(t.TextMuted).
					Width(0).Grow(1).MinWidth(0).SingleLine().TruncateMiddle().Tooltip(downloadTooltip)
				settingsButton(c, "settings-download-dir", "选择…", 28, locked).
					Label("选择下载目录").Tooltip("选择网页图片与剪贴板图片保存目录").
					OnClick(s.chooseDownloadDirectory)
				if s.editor.downloadDir != "" {
					settingsButton(c, "settings-download-reset", "默认", 28, locked).
						Label("恢复默认下载目录").Tooltip("恢复为系统下载目录下的 Z Pic 文件夹").
						OnClick(s.resetDownloadDirectory)
				}
				settingsButton(c, "settings-download-clean", "清理", 28, locked).
					Label("清理下载目录").Tooltip("将下载目录中未在队列里的图片移入废纸篓").
					OnClick(s.cleanupDownloadedImages)
			})
		})
		settingsPanel(c, "settings-shortcuts-panel").Children(func() {
			ui.Text(c, "快捷键").FontSize(settingsFontSize(c, 16)).FontWeight(600).Role(ui.RoleHeading).Level(2)
			shortcuts := []struct{ label, key string }{
				{"选择图片", "O"}, {"粘贴图片入队", "V"}, {"复制压缩结果", "C"}, {"压缩队列或当前图片", "↵"}, {"保存结果 / 选择输出位置", "S"}, {"打开设置", ","},
			}
			renderRow := func(label, key string) {
				ui.Row(c).FillWidth().MinHeight(settingsControlHeight(c, 28)).Gap(12).AlignItems(ui.Center).Children(func() {
					ui.Text(c, label).FontSize(settingsFontSize(c, 13)).TextColor(t.TextMuted).
						Width(0).Grow(1).SingleLine().TruncateMiddle().Tooltip(label)
					keyboardKeycap(c, key).FontSize(settingsFontSize(c, 12))
				})
			}
			if twoColShortcuts {
				ui.Column(c).FillWidth().Children(func() {
					for row := 0; row < len(shortcuts); row += 2 {
						if row > 0 {
							ui.Box(c).FillWidth().Height(1).Background(glassDivider(c))
						}
						shortcutGap := float32(20)
						if narrow {
							shortcutGap = 12
						}
						ui.Row(c).FillWidth().Gap(shortcutGap).AlignItems(ui.Center).Children(func() {
							ui.Box(c).Width(0).Grow(1).Children(func() { renderRow(shortcuts[row].label, shortcuts[row].key) })
							if row+1 < len(shortcuts) {
								ui.Box(c).Width(0).Grow(1).Children(func() { renderRow(shortcuts[row+1].label, shortcuts[row+1].key) })
							}
						})
					}
				})
			} else {
				ui.Column(c).FillWidth().Children(func() {
					for index, shortcut := range shortcuts {
						if index > 0 {
							ui.Box(c).FillWidth().Height(1).Background(glassDivider(c))
						}
						renderRow(shortcut.label, shortcut.key)
					}
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
	locked := s.settingsDraftLocked()
	choice.Track.Label("外观主题").Height(settingsControlHeight(c, 64)).Gap(8).AlignItems(ui.Stretch).Disabled(locked).
		Children(func() {
			for index, option := range themeOptions {
				active := index == selected
				segment := choice.Segment(index).Label(option.label).Width(0).MinWidth(0).
					Radius(innerRadius(c)).Padding(8).
					Background(glassCardSurface(c)).Border(1, controlBorder(c))
				color := t.TextMuted
				if active {
					color = accentForeground(t)
					segment.Background(glassSelectionBackground(c)).Border(1, selectionBorder(c))
					settingsGlassControl(segment, c, 64, locked, t.Accent.Alpha(0.14)).
						Radius(innerRadius(c))
				} else if !locked {
					if segment.Pressed() {
						segment.Background(glassControlPressed(c)).Border(1, controlHoverBorder(c))
					} else if segment.Hovered() {
						segment.Background(glassControlHover(c)).Border(1, controlHoverBorder(c))
					}
				}
				segment.Children(func() {
					ui.Column(c).Gap(4).AlignItems(ui.Center).Children(func() {
						ui.Icon(c, icons[index]).Size(20, 20).TextColor(color)
						ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
							ui.Text(c, option.label).FontSize(settingsFontSize(c, 13)).FontWeight(500).SingleLine().TextColor(t.Text)
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
	padding := settingsPagePadding(c)
	ui.Box(c).FillWidth().Height(1).Background(glassDivider(c))
	if editor.confirmDelete {
		ui.Row(c).Padding(12, padding).Gap(8).Background(statusTintSurface(c, t.Danger)).AlignItems(ui.Center).Children(func() {
			name := presetDisplayName(*editor.selectedPreset())
			short := []rune(name)
			if len(short) > 16 {
				short = append(short[:16], '…')
			}
			message := fmt.Sprintf("删除「%s」？保存后生效。", string(short))
			ui.Text(c, message).FontSize(settingsFontSize(c, 12)).Width(0).Grow(1).MaxLines(2).
				Tooltip("删除「" + name + "」？保存后生效。")
			keep := settingsButton(c, "cancel-delete", "保留预设", 32, false).
				OnClick(func() {
					editor.confirmDelete = false
					editor.focusField = "delete-preset"
				})
			s.focusSettingsFieldDuringDelete(keep)
			destructiveAction(c, "confirm-delete", "确认删除", settingsControlHeight(c, 32), false).
				FontSize(settingsFontSize(c, 13)).OnClick(s.deletePreset)
		})
		ui.Box(c).FillWidth().Height(1).Background(glassDivider(c))
	}
	ui.Column(c).Padding(12, padding).Gap(8).Background(panelSurface(c)).Children(func() {
		message, color, role := "", t.TextMuted, ui.RoleStatus
		dirty := s.settingsHaveChanges()
		presetChanged := editor.presets[editor.active].name != s.scheme
		switch {
		case editor.message != "":
			message, color, role = editor.message, t.Danger, ui.RoleAlert
		case editor.notice != "":
			message = editor.notice
		case presetChanged:
			message = "保存后使用：" + presetDisplayName(editor.presets[editor.active])
		case dirty:
			message = "有未保存的更改"
		}
		width, _ := c.Size()
		statusAbove := editor.message != "" || editor.notice != "" || presetChanged || (dirty && width < 560)
		if statusAbove {
			ui.Text(c, message).FontSize(settingsFontSize(c, 12)).TextColor(color).Role(role).MaxLines(2).Tooltip(message)
		}
		ui.Row(c).FillWidth().Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
			if editor.section == settingsPresets {
				quietToolbarButton(c, "duplicate-preset", "复制预设", s.settingsDraftLocked()).
					Height(settingsControlHeight(c, 32)).FontSize(settingsFontSize(c, 13)).OnClick(s.duplicatePreset)
				deleteButton := quietToolbarButton(c, "delete-preset", "删除预设", s.settingsDraftLocked() || len(editor.presets) <= 1).
					Height(settingsControlHeight(c, 32)).FontSize(settingsFontSize(c, 13)).TextColor(t.Danger).OnClick(s.requestDeletePreset)
				if len(editor.presets) <= 1 {
					deleteButton.Tooltip("至少保留一个预设")
				}
				s.focusSettingsField(deleteButton, "delete-preset")
			}
			if !statusAbove && message != "" {
				ui.Text(c, message).FontSize(settingsFontSize(c, 12)).TextColor(color).Role(role).
					Width(0).Grow(1).MinWidth(0).SingleLine().TruncateMiddle().Tooltip(message)
			} else {
				ui.Box(c).Grow(1)
			}
			settingsButton(c, "cancel-settings", "取消", 36, s.busy).Width(72).
				OnClick(func() { s.setSettingsOpen(false) })
			saveDisabled := s.busy || editor.confirmDelete
			settingsSaveButton(c, saveDisabled).Width(88).Height(settingsControlHeight(c, 36)).
				KeyShortcuts(ui.Cmd, ui.KeyS).OnClick(s.saveSettingsEdits)
		})
	})
}

func (s *application) focusSettingsFieldDuringDelete(keep ui.Element) {
	if s.editor.focusField == "cancel-delete" && !s.busy {
		keep.Focus()
		s.editor.focusField = ""
	}
}
