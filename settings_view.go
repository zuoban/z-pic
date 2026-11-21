package main

import (
	"fmt"

	"github.com/egoist/mygo/ui"
)

var (
	settingsChevronIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="m5 6.5 3 3 3-3"/></svg>`))
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
		ui.Column(c).FillWidth().Background(sidebarSurface(c)).Children(func() {
			s.settingsNavigation(c)
			if s.editor.section != settingsFolderWatch {
				ui.Box(c).FillWidth().Height(1).Background(hairlineDivider(c))
				s.settingsPresetSelection(c)
			}
		})
		ui.Box(c).FillWidth().Height(1).Background(hairlineDivider(c))
		switch s.editor.section {
		case settingsPresets:
			s.presetSettingsContent(c)
		case settingsFolderWatch:
			s.folderWatchSettingsContent(c)
		default:
			s.generalSettingsContent(c)
		}
		s.settingsFooter(c)
	})
}

func (s *application) settingsPresetSelection(c *ui.Context) {
	editor := s.editor
	t := c.Theme()
	ui.Row(c).FillWidth().Height(settingsControlHeight(c, 46)).Padding(0, settingsPagePadding(c)).Gap(10).Background(tonalWellSurface(c)).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "当前预设").FontSize(settingsFontSize(c, 12)).FontWeight(500).TextColor(metadataColor(c)).Shrink(0)
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
				stylePopoverPanel(panel, c).MinWidth(max(180, picker.Bounds().W)).MaxWidth(360)
			},
		}).Label("选择预设").Value(presetDisplayName(preset)).Tooltip(presetDisplayName(preset)).
			Width(0).Grow(1).MinWidth(0).MaxWidth(340).Height(settingsControlHeight(c, 32)).Padding(0, 10).Gap(8).
			AlignItems(ui.Center).Radius(innerRadius(c)).
			Background(glassControlSurface(c)).Border(1, controlBorder(c)).Disabled(disabled)
		if !disabled {
			if picker.Pressed() {
				picker.Background(glassControlPressed(c))
			} else if picker.Hovered() {
				picker.Background(glassControlHover(c))
			}
		}
		settingsGlassControl(picker, c, 32, disabled, ui.Transparent)
		picker.Children(func() {
			ui.Text(c, presetDisplayName(preset)).FontSize(settingsFontSize(c, 13)).FontWeight(500).SingleLine().TruncateMiddle().
				Width(0).Grow(1).MinWidth(0)
			ui.Icon(c, settingsChevronIcon).Size(15, 15).Shrink(0).TextColor(t.TextMuted)
		})
		ui.Box(c).Grow(1).MinWidth(0)
		if editor.section == settingsGeneral {
			settingsButton(c, "manage-presets", "管理预设", 30, s.settingsDraftLocked()).OnClick(func() {
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
		settingsSegments(c, "settings-navigation", "设置分类", &section, s.busy || s.editor.confirmDelete, "通用", "预设", "文件夹监听").
			Width(settingsFontSize(c, 282))
		s.editor.section = settingsSection(section)
	})
}

// Categories and destinations share the same quiet, inset selection treatment.
func settingsSegments(c *ui.Context, key, label string, selected *int, disabled bool, options ...string) ui.Element {
	track, _ := settingsSegmentsWithSelection(c, key, label, selected, disabled, options...)
	return track
}

func settingsSegmentsWithSelection(c *ui.Context, key, label string, selected *int, disabled bool, options ...string) (track, selection ui.Element) {
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
				selection = segment
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
	return parts.Track.FillEqually(), selection
}

func (s *application) generalSettingsContent(c *ui.Context) {
	t := c.Theme()
	width, _ := c.Size()
	narrow := width < 560
	sideBySideWorkflow := !narrow && c.Preferences().TextScale <= 1.1
	twoColShortcuts := width >= 400 && c.Preferences().TextScale <= 1.1
	scrollPadV, scrollGap := float32(12), float32(12)
	if narrow {
		scrollPadV, scrollGap = 12, 8
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
			downloadColor := t.Text
			if downloadPath == "" {
				downloadPath = "默认（下载/Z Pic）"
				downloadTooltip = "系统下载目录下的 Z Pic 文件夹"
				downloadColor = t.TextMuted
			}
			ui.Row(c).FillWidth().Gap(8).Padding(7, 12).Radius(innerRadius(c)).
				Background(tonalWellSurface(c)).Border(1, tonalWellBorder(c)).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "下载目录").FontSize(settingsFontSize(c, 12)).FontWeight(500).TextColor(metadataColor(c)).Shrink(0)
				ui.Box(c).Size(1, 14).Background(hairlineDivider(c))
				ui.Text(c, downloadPath).FontSize(settingsFontSize(c, 12)).TextColor(downloadColor).
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
			if s.clipboardPNGCompressionAvailable() {
				shortcuts = append(shortcuts, struct{ label, key string }{"压缩剪贴板图片", "⇧V"})
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
						shortcutGap := float32(20)
						if narrow {
							shortcutGap = 12
						}
						ui.Row(c).FillWidth().Gap(shortcutGap).AlignItems(ui.Center).Children(func() {
							ui.Column(c).Width(0).Grow(1).Children(func() {
								if row > 0 {
									ui.Box(c).FillWidth().Height(1).Background(glassDivider(c))
								}
								renderRow(shortcuts[row].label, shortcuts[row].key)
							})
							if row+1 < len(shortcuts) {
								ui.Column(c).Width(0).Grow(1).Children(func() {
									if row > 0 {
										ui.Box(c).FillWidth().Height(1).Background(glassDivider(c))
									}
									renderRow(shortcuts[row+1].label, shortcuts[row+1].key)
								})
							} else {
								ui.Box(c).Width(0).Grow(1)
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
	choice := ui.SegmentedBase(c.Key("settings-theme"), &selected, len(themeOptions))
	locked := s.settingsDraftLocked()
	choice.Track.Label("外观主题").Height(settingsControlHeight(c, 68)).Gap(10).AlignItems(ui.Stretch).Disabled(locked).
		Children(func() {
			for index, option := range themeOptions {
				active := index == selected
				segment := choice.Segment(index).Label(option.label).Width(0).MinWidth(0).
					Radius(innerRadius(c)).Padding(6, 10).
					Background(tonalWellSurface(c)).Border(1, controlBorder(c))
				color := t.TextMuted
				if active {
					color = accentForeground(t)
					segment.Background(glassSelectionBackground(c)).Border(1.5, selectionBorder(c))
					settingsGlassControl(segment, c, 68, locked, t.Accent.Alpha(0.14)).
						Radius(innerRadius(c))
				} else if !locked {
					if segment.Pressed() {
						segment.Background(glassControlPressed(c)).Border(1, controlHoverBorder(c))
					} else if segment.Hovered() {
						segment.Background(glassControlHover(c)).Border(1, controlHoverBorder(c))
					}
				}
				mode := option.mode
				segment.Children(func() {
					ui.Column(c).Gap(4).AlignItems(ui.Center).Children(func() {
						ui.Box(c).Size(58, 34).Draw(func(p *ui.Painter, r ui.Rect) {
							lightWin, darkWin := ui.Hex("#eef1f5"), ui.Hex("#141720")
							lightSide, darkSide := ui.Hex("#ffffff"), ui.Hex("#202634")
							lightCard, darkCard := ui.Hex("#dde2ec"), ui.Hex("#283042")
							drawTrafficLights := func(x, y float32, dotColor ui.Color) {
								for i := range 3 {
									p.Fill(ui.Rect{X: x + float32(i)*4.5, Y: y, W: 2.5, H: 2.5}, dotColor, 1.25)
								}
							}
							switch mode {
							case themeLight:
								p.Fill(r, lightWin, 5)
								p.Fill(ui.Rect{X: r.X + r.W*0.60, Y: r.Y, W: r.W * 0.40, H: r.H}, lightSide, 4)
								drawTrafficLights(r.X+5, r.Y+4.5, ui.Hex("#c4cad6"))
								p.Fill(ui.Rect{X: r.X + 5, Y: r.Y + 11, W: 24, H: 17}, lightCard, 2.5)
								p.Fill(ui.Rect{X: r.X + r.W*0.66, Y: r.Y + 7, W: r.W * 0.27, H: 3.5}, lightCard, 1.75)
								p.Fill(ui.Rect{X: r.X + r.W*0.66, Y: r.Y + 23, W: r.W * 0.27, H: 5}, t.Accent, 2.5)
							case themeDark:
								p.Fill(r, darkWin, 5)
								p.Fill(ui.Rect{X: r.X + r.W*0.60, Y: r.Y, W: r.W * 0.40, H: r.H}, darkSide, 4)
								drawTrafficLights(r.X+5, r.Y+4.5, ui.Hex("#475066"))
								p.Fill(ui.Rect{X: r.X + 5, Y: r.Y + 11, W: 24, H: 17}, darkCard, 2.5)
								p.Fill(ui.Rect{X: r.X + r.W*0.66, Y: r.Y + 7, W: r.W * 0.27, H: 3.5}, ui.Hex("#364056"), 1.75)
								p.Fill(ui.Rect{X: r.X + r.W*0.66, Y: r.Y + 23, W: r.W * 0.27, H: 5}, accentForeground(ui.DarkTheme()), 2.5)
							default:
								p.Fill(r, lightWin, 5)
								p.Fill(ui.Rect{X: r.X + r.W/2, Y: r.Y, W: r.W / 2, H: r.H}, darkWin, 4)
								drawTrafficLights(r.X+5, r.Y+4.5, ui.Hex("#c4cad6"))
								p.Fill(ui.Rect{X: r.X + 5, Y: r.Y + 11, W: 18, H: 17}, lightCard, 2.5)
								p.Fill(ui.Rect{X: r.X + r.W/2 + 5, Y: r.Y + 8, W: 18, H: 18}, darkSide, 2.5)
								p.Fill(ui.Rect{X: r.X + r.W/2 + 8, Y: r.Y + 19, W: 12, H: 4}, accentForeground(ui.DarkTheme()), 2)
							}
							p.Stroke(r, controlBorder(c), 5, 1)
						})
						ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
							ui.Text(c, option.label).FontSize(settingsFontSize(c, 12)).FontWeight(500).SingleLine().TextColor(t.Text)
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
	ui.Box(c).FillWidth().Height(1).Background(hairlineDivider(c))
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
		ui.Box(c).FillWidth().Height(1).Background(hairlineDivider(c))
	}
	ui.Column(c).Padding(12, padding).Gap(8).Background(sidebarSurface(c)).Children(func() {
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
			statusText := ui.Text(c, message).FontSize(settingsFontSize(c, 12)).TextColor(color).Role(role).
				FillWidth().Tooltip(message)
			if editor.message != "" {
				statusText.MaxLines(2)
			} else {
				statusText.SingleLine().TruncateMiddle()
			}
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
