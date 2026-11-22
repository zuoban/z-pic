package main

import (
	"fmt"

	"github.com/egoist/mygo/ui"
)

const (
	settingsSidebarWidth      = 164
	settingsSidebarBreakpoint = 580
)

var (
	settingsChevronIcon      = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="m5 6.5 3 3 3-3"/></svg>`))
	settingsGeneralIcon      = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><circle cx="8" cy="8" r="2.25"/><path d="M8 2.25v1.5M8 12.25v1.5M2.25 8h1.5M12.25 8h1.5M3.93 3.93l1.06 1.06M11.01 11.01l1.06 1.06M12.07 3.93l-1.06 1.06M4.99 11.01l-1.06 1.06"/></svg>`))
	settingsPresetsIcon      = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"><path d="M2 5h2.5m3.5 0h6M2 11h6m3.5 0H14"/><circle cx="6.25" cy="5" r="1.75"/><circle cx="9.75" cy="11" r="1.75"/></svg>`))
	settingsIntegrationsIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><rect x="2.25" y="3" width="11.5" height="10" rx="2"/><path d="m5 6.5 2 1.75-2 1.75M8.5 10h2.5"/></svg>`))
	settingsAboutIcon        = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"><circle cx="8" cy="8" r="5.75"/><path d="M8 7.25v3.5M8 5.1v.15"/></svg>`))
)

type settingsNavItem struct {
	section settingsSection
	label   string
	icon    *ui.SVG
}

var settingsNavItems = [...]settingsNavItem{
	{section: settingsGeneral, label: "通用", icon: settingsGeneralIcon},
	{section: settingsPresets, label: "预设", icon: settingsPresetsIcon},
	{section: settingsFolderWatch, label: "文件夹监听", icon: folderWatchIcon},
	{section: settingsIntegrations, label: "系统集成", icon: settingsIntegrationsIcon},
	{section: settingsAbout, label: "关于", icon: settingsAboutIcon},
}

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
	width, _ := c.Size()
	wide := width >= settingsSidebarBreakpoint
	ui.Column(c).Fill().Background(windowRootBackground(c)).Role(ui.RoleDialog).Label("应用设置").Children(func() {
		if wide {
			ui.Row(c).FillWidth().Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
				s.settingsSidebar(c)
				ui.Box(c).Width(1).FillHeight().Shrink(0).Background(hairlineDivider(c))
				ui.Column(c).Width(0).Grow(1).MinWidth(0).FillHeight().Children(func() {
					s.settingsSectionContent(c)
				})
			})
		} else {
			s.settingsCompactNavigation(c)
			if s.editor.section == settingsGeneral || s.editor.section == settingsPresets {
				ui.Box(c).FillWidth().Height(1).Background(subtleRowDivider(c))
			} else {
				ui.Box(c).FillWidth().Height(1).Background(hairlineDivider(c))
			}
			s.settingsSectionContent(c)
		}
		s.settingsFooter(c)
	})
}

func (s *application) settingsSectionContent(c *ui.Context) {
	switch s.editor.section {
	case settingsPresets:
		s.presetSettingsContent(c)
	case settingsFolderWatch:
		s.folderWatchSettingsContent(c)
	case settingsIntegrations:
		s.integrationsSettingsContent(c)
	case settingsAbout:
		s.aboutSettingsContent(c)
	default:
		ui.Column(c).FillWidth().Grow(1).MinHeight(0).Children(func() {
			s.settingsPresetSelection(c)
			ui.Box(c).FillWidth().Height(1).Background(hairlineDivider(c))
			s.generalSettingsContent(c)
		})
	}
}

func (s *application) settingsPresetSelection(c *ui.Context) {
	editor := s.editor
	t := c.Theme()
	ui.Row(c).FillWidth().Height(settingsControlHeight(c, 42)).Padding(0, settingsPagePadding(c)).Gap(10).Background(sidebarSurface(c)).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "当前预设").FontSize(settingsFontSize(c, 12)).FontWeight(500).TextColor(metadataColor(c)).Shrink(0)
		preset := editor.presets[editor.active]
		disabled := s.busy || editor.confirmDelete
		ui.Row(c).Width(0).Grow(1).MinWidth(0).AlignItems(ui.Center).Children(func() {
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
				Width(0).Grow(1).MinWidth(0).MaxWidth(340).Height(settingsControlHeight(c, 28)).Padding(0, 10).Gap(8).
				AlignItems(ui.Center).Radius(innerRadius(c)).
				Background(glassControlSurface(c)).Border(1, controlBorder(c)).Disabled(disabled)
			if !disabled {
				if picker.Pressed() {
					picker.Background(glassControlPressed(c)).Border(1, controlHoverBorder(c))
				} else {
					controlElevation(picker, c)
					if picker.Hovered() {
						picker.Background(glassControlHover(c)).Border(1, controlHoverBorder(c))
					}
				}
			}
			settingsGlassControl(picker, c, 28, disabled, ui.Transparent)
			picker.Children(func() {
				ui.Text(c, presetDisplayName(preset)).FontSize(settingsFontSize(c, 13)).FontWeight(500).SingleLine().TruncateMiddle().
					Width(0).Grow(1).MinWidth(0)
				ui.Icon(c, settingsChevronIcon).Size(15, 15).Shrink(0).TextColor(t.TextMuted)
			})
		})
		settingsButton(c, "manage-presets", "管理预设", 28, s.settingsDraftLocked()).Padding(0, 12).OnClick(func() {
			editor.selected = editor.active
			editor.section = settingsPresets
		})
	})
}

func (s *application) settingsSidebar(c *ui.Context) {
	t := c.Theme()
	disabled := s.busy || s.editor.confirmDelete
	ui.Column(c).Width(settingsSidebarWidth).FillHeight().Shrink(0).
		Background(sidebarSurface(c)).Padding(14, 8, 12, 8).Gap(10).Children(func() {
		ui.Text(c, "设置").FontSize(settingsFontSize(c, 16)).FontWeight(600).
			Role(ui.RoleHeading).Level(1).Padding(0, 8)
		ui.Column(c.Key("settings-navigation")).FillWidth().Gap(3).
			Role(ui.RoleTabList).Label("设置分类").Children(func() {
			for _, item := range settingsNavItems {
				active := s.editor.section == item.section
				btn := ui.ButtonBase(c.Key(fmt.Sprintf("settings-nav-%d", item.section)))
				btn.Role(ui.RoleTab).Label(item.label).AccessSelected(active).Disabled(disabled).
					FillWidth().Height(settingsControlHeight(c, 34)).Padding(0, 8).Gap(8).
					Radius(innerRadius(c)).AlignItems(ui.Center).
					Border(1, ui.Transparent).TextColor(chromeMutedColor(c))
				iconColor := chromeMetadataColor(c)
				weight := 400
				if active {
					iconColor = accentForeground(t)
					weight = 600
					btn.Background(glassSelectionBackground(c)).Border(1, selectionBorder(c)).TextColor(t.Text)
					settingsGlassControl(btn, c, 34, disabled, t.Accent.Alpha(0.14)).Radius(innerRadius(c))
				} else if !disabled {
					if btn.Pressed() {
						btn.Background(glassControlPressed(c)).TextColor(t.Text)
						iconColor = t.Text
					} else if btn.Hovered() {
						btn.Background(glassControlHover(c)).TextColor(t.Text)
						iconColor = t.Text
					}
				}
				section := item.section
				btn.OnClick(func() {
					if !disabled {
						s.editor.section = section
					}
				})
				btn.Children(func() {
					ui.Icon(c, item.icon).Size(15, 15).Shrink(0).TextColor(iconColor)
					ui.Text(c, item.label).FontSize(settingsFontSize(c, 13)).FontWeight(weight).
						SingleLine().Width(0).Grow(1).MinWidth(0)
				})
			}
		})
	})
}

func (s *application) settingsCompactNavigation(c *ui.Context) {
	t := c.Theme()
	disabled := s.busy || s.editor.confirmDelete
	pad := settingsPagePadding(c)
	navH := settingsControlHeight(c, 32)
	ui.Column(c).FillWidth().Background(sidebarSurface(c)).Padding(10, pad, 8, pad).Gap(8).Children(func() {
		ui.Text(c, "设置").FontSize(settingsFontSize(c, 16)).FontWeight(600).Role(ui.RoleHeading).Level(1)
		ui.ScrollHorizontal(c.Key("settings-navigation-scroll")).FillWidth().Height(navH).Children(func() {
			ui.Row(c.Key("settings-navigation")).Height(navH).Gap(4).AlignItems(ui.Center).
				Role(ui.RoleTabList).Label("设置分类").Children(func() {
				for _, item := range settingsNavItems {
					active := s.editor.section == item.section
					btn := ui.ButtonBase(c.Key(fmt.Sprintf("settings-nav-%d", item.section)))
					btn.Role(ui.RoleTab).Label(item.label).AccessSelected(active).Disabled(disabled).
						Height(navH).Padding(0, 10).Radius(innerRadius(c)).Center().
						Border(1, ui.Transparent).TextColor(chromeMutedColor(c)).Shrink(0)
					weight := 400
					if active {
						weight = 600
						btn.Background(glassSelectionBackground(c)).Border(1, selectionBorder(c)).TextColor(t.Text)
						settingsGlassControl(btn, c, 32, disabled, t.Accent.Alpha(0.14)).Radius(innerRadius(c))
					} else if !disabled {
						if btn.Pressed() {
							btn.Background(glassControlPressed(c)).TextColor(t.Text)
						} else if btn.Hovered() {
							btn.Background(glassControlHover(c)).TextColor(t.Text)
						}
					}
					section := item.section
					btn.OnClick(func() {
						if !disabled {
							s.editor.section = section
						}
					})
					btn.Children(func() {
						ui.Text(c, item.label).FontSize(settingsFontSize(c, 12)).FontWeight(weight).SingleLine()
					})
				}
			})
		})
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
				if !t.Dark && !c.Preferences().HighContrast {
					segment.Shadow(0, 1, 2, 0, ui.RGBA(17, 20, 26, 0.08))
				}
				settingsGlassControl(segment, c, 30, disabled, ui.Transparent)
				weight = 600
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
	width, height := c.Size()
	narrow := width < 560
	compactHeight := height > 0 && height <= 620
	scrollPadV, scrollGap := float32(12), float32(10)
	if narrow || compactHeight {
		scrollPadV, scrollGap = 10, 8
	}
	ui.Scroll(c.Key("general-settings")).FillWidth().Grow(1).MinHeight(0).Padding(scrollPadV, settingsPagePadding(c)).Gap(scrollGap).Children(func() {
		appearancePanel := settingsPanel(c, "settings-appearance-panel")
		if compactHeight {
			appearancePanel.Padding(12, 16).Gap(8)
		}
		appearancePanel.Children(func() {
			ui.Text(c, "外观").FontSize(settingsFontSize(c, 14)).FontWeight(600).Role(ui.RoleHeading).Level(2)
			s.settingsThemeSelection(c)
		})
		workflowPanel := settingsPanel(c, "settings-workflow-panel")
		if compactHeight {
			workflowPanel.Padding(12, 16).Gap(8)
		}
		workflowPanel.Children(func() {
			ui.Text(c, "工作流").FontSize(settingsFontSize(c, 14)).FontWeight(600).Role(ui.RoleHeading).Level(2)
			locked := s.settingsDraftLocked()
			checkRowH := float32(32)
			checkPadV := float32(3)
			if compactHeight {
				checkRowH = 26
				checkPadV = 1
			}
			ui.Column(c).FillWidth().Gap(0).Children(func() {
				ui.Row(c).FillWidth().MinHeight(settingsControlHeight(c, checkRowH)).Padding(checkPadV, 0).Gap(12).AlignItems(ui.Center).Children(func() {
					ui.Checkbox(c.Key("settings-auto-compress"), &s.editor.autoCompress, "导入图片或切换预设后自动压缩").
						FontSize(settingsFontSize(c, 13)).Disabled(locked).
						Tooltip("单图导入、主窗切预设或非覆盖/非询问目录的批量导入将自动开始压缩")
				})
				ui.Box(c).FillWidth().Height(1).Background(subtleRowDivider(c))
				ui.Row(c).FillWidth().MinHeight(settingsControlHeight(c, checkRowH)).Padding(checkPadV, 0).Gap(12).AlignItems(ui.Center).Children(func() {
					ui.Checkbox(c.Key("settings-backup-to-trash"), &s.editor.backupToTrash, "覆盖原图前移入废纸篓").
						FontSize(settingsFontSize(c, 13)).Disabled(locked).
						Tooltip("覆盖单图或批量原图前先将原文件移至系统废纸篓（回收站）")
				})
				ui.Box(c).FillWidth().Height(1).Background(subtleRowDivider(c))
				ui.Row(c).FillWidth().MinHeight(settingsControlHeight(c, checkRowH)).Padding(checkPadV, 0).Gap(12).AlignItems(ui.Center).Children(func() {
					ui.Checkbox(c.Key("settings-keep-in-menubar"), &s.editor.keepInMenuBar, "关闭主窗口时最小化到菜单栏").
						FontSize(settingsFontSize(c, 13)).Disabled(locked).
						Tooltip("关闭主窗口后保持菜单栏常驻，后台文件夹监听将继续运行；可通过菜单栏或 Dock 重新打开窗口")
				})
				if s.editor.openAtLoginSupported {
					ui.Box(c).FillWidth().Height(1).Background(subtleRowDivider(c))
					ui.Row(c).FillWidth().MinHeight(settingsControlHeight(c, checkRowH)).Padding(checkPadV, 0).Gap(12).AlignItems(ui.Center).Children(func() {
						ui.Checkbox(c.Key("settings-open-at-login"), &s.editor.openAtLogin, "登录系统时自动启动").
							FontSize(settingsFontSize(c, 13)).Disabled(locked).
							Tooltip("登录系统时自动启动 Z Pic；同时开启菜单栏常驻时将在后台静默启动")
					})
				}
				ui.Box(c).FillWidth().Height(1).Background(subtleRowDivider(c))
				downloadPath := s.editor.downloadDir
				downloadTooltip := downloadPath
				downloadColor := t.Text
				if downloadPath == "" {
					downloadPath = "默认（下载/Z Pic）"
					downloadTooltip = "系统下载目录下的 Z Pic 文件夹"
					downloadColor = t.TextMuted
				}
				ui.Row(c).FillWidth().MinHeight(settingsControlHeight(c, 42)).Padding(6, 0, 2, 0).Gap(10).AlignItems(ui.Center).Children(func() {
					ui.Column(c).Width(0).Grow(1).MinWidth(0).Gap(2).Children(func() {
						ui.Text(c, "下载目录").FontSize(settingsFontSize(c, 13)).FontWeight(500).TextColor(t.Text).SingleLine()
						ui.Text(c, downloadPath).FontSize(settingsFontSize(c, 12)).TextColor(downloadColor).
							FillWidth().SingleLine().TruncateMiddle().Tooltip(downloadTooltip)
					})
					settingsButton(c, "settings-download-dir", "选择…", 28, locked).
						FontSize(settingsFontSize(c, 12)).Padding(0, 10).
						Label("选择下载目录").Tooltip("选择网页图片与剪贴板图片保存目录").
						OnClick(s.chooseDownloadDirectory)
					if s.editor.downloadDir != "" {
						settingsButton(c, "settings-download-reset", "默认", 28, locked).
							FontSize(settingsFontSize(c, 12)).Padding(0, 10).
							Label("恢复默认下载目录").Tooltip("恢复为系统下载目录下的 Z Pic 文件夹").
							OnClick(s.resetDownloadDirectory)
					}
					settingsButton(c, "settings-download-clean", "清理", 28, locked).
						FontSize(settingsFontSize(c, 12)).Padding(0, 10).
						Label("清理下载目录").Tooltip("将下载目录中未在队列里的图片移入废纸篓").
						OnClick(s.cleanupDownloadedImages)
				})
			})
		})
	})
}

func (s *application) integrationsSettingsContent(c *ui.Context) {
	width, height := c.Size()
	narrow := width < 560
	compactHeight := height > 0 && height <= 620
	scrollPadV, scrollGap := float32(12), float32(10)
	if narrow || compactHeight {
		scrollPadV, scrollGap = 10, 8
	}
	ui.Scroll(c.Key("integrations-settings")).FillWidth().Grow(1).MinHeight(0).Padding(scrollPadV, settingsPagePadding(c)).Gap(scrollGap).Children(func() {
		s.cliInstallSettingsPanel(c, compactHeight)
		s.quickActionSettingsPanel(c, compactHeight)
	})
}

func (s *application) aboutSettingsContent(c *ui.Context) {
	width, height := c.Size()
	narrow := width < 560
	compactHeight := height > 0 && height <= 620
	scrollPadV, scrollGap := float32(12), float32(10)
	if narrow || compactHeight {
		scrollPadV, scrollGap = 10, 8
	}
	ui.Scroll(c.Key("about-settings")).FillWidth().Grow(1).MinHeight(0).Padding(scrollPadV, settingsPagePadding(c)).Gap(scrollGap).Children(func() {
		s.shortcutsSettingsPanel(c, width, narrow, compactHeight)
		s.aboutAndUpdateSettingsPanel(c, compactHeight)
	})
}

func settingsRawKeycap(c *ui.Context, text string) ui.Element {
	t := c.Theme()
	fg := t.TextMuted
	if t.Dark {
		fg = t.Text.Alpha(0.82)
	}
	return ui.Text(c, text).FontSize(settingsFontSize(c, 12)).FontWeight(500).FontFeatures("tnum").
		TextColor(fg).Padding(2, 7).Radius(5).
		Background(keycapSurface(c)).Border(1, controlBorder(c)).SingleLine().Shrink(0)
}

func (s *application) shortcutsSettingsPanel(c *ui.Context, width float32, narrow, compactHeight bool) {
	t := c.Theme()
	contentWidth := width
	if width >= settingsSidebarBreakpoint {
		contentWidth = width - settingsSidebarWidth - 1
	}
	twoColShortcuts := contentWidth >= 460 && c.Preferences().TextScale <= 1.1
	shortcutsPanel := settingsPanel(c, "settings-shortcuts-panel")
	if compactHeight {
		shortcutsPanel.Padding(12, 16).Gap(8)
	}
	shortcutsPanel.Children(func() {
		ui.Text(c, "快捷键").FontSize(settingsFontSize(c, 14)).FontWeight(600).Role(ui.RoleHeading).Level(2)
		type shortcutItem struct {
			label string
			key   string
			raw   bool
		}
		shortcuts := []shortcutItem{
			{label: "选择图片", key: "O"},
			{label: "粘贴图片入队", key: "V"},
			{label: "复制压缩结果", key: "C"},
			{label: "压缩队列或当前图片", key: "↵"},
			{label: "保存结果 / 选择输出位置", key: "S"},
			{label: "打开设置", key: ","},
			{label: "切换对比 / 原图 / 结果 / 差异", key: "1 ~ 4", raw: true},
		}
		if s.clipboardPNGCompressionAvailable() {
			shortcuts = append(shortcuts, shortcutItem{label: "压缩剪贴板图片", key: "⇧V"})
		}
		rowHeight := float32(26)
		if compactHeight {
			rowHeight = 24
		}
		renderRow := func(item shortcutItem) {
			ui.Row(c).FillWidth().MinHeight(settingsControlHeight(c, rowHeight)).Padding(2, 0).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Text(c, item.label).FontSize(settingsFontSize(c, 13)).TextColor(t.TextMuted).
					Width(0).Grow(1).SingleLine().TruncateMiddle().Tooltip(item.label)
				if item.raw {
					settingsRawKeycap(c, item.key)
				} else {
					keyboardKeycap(c, item.key).FontSize(settingsFontSize(c, 12))
				}
			})
		}
		if twoColShortcuts {
			ui.Column(c).FillWidth().Children(func() {
				for row := 0; row < len(shortcuts); row += 2 {
					shortcutGap := float32(14)
					if narrow {
						shortcutGap = 8
					}
					ui.Row(c).FillWidth().Gap(shortcutGap).AlignItems(ui.Stretch).Children(func() {
						ui.Column(c).Width(0).Grow(1).Children(func() {
							if row > 0 {
								ui.Box(c).FillWidth().Height(1).Background(subtleRowDivider(c))
							}
							renderRow(shortcuts[row])
						})
						centerDivider := ui.Box(c).Width(1).Shrink(0)
						if row+1 < len(shortcuts) {
							centerDivider.Background(subtleRowDivider(c))
						}
						if row+1 < len(shortcuts) {
							ui.Column(c).Width(0).Grow(1).Children(func() {
								if row > 0 {
									ui.Box(c).FillWidth().Height(1).Background(subtleRowDivider(c))
								}
								renderRow(shortcuts[row+1])
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
						ui.Box(c).FillWidth().Height(1).Background(subtleRowDivider(c))
					}
					renderRow(shortcut)
				}
			})
		}
	})
}

func (s *application) cliInstallSettingsPanel(c *ui.Context, compactHeight bool) {
	t := c.Theme()
	editor := s.editor
	locked := s.settingsDraftLocked()
	status := editor.cliStatus
	panel := settingsPanel(c, "settings-cli-panel")
	if compactHeight {
		panel.Padding(12, 16).Gap(8)
	}
	panel.Children(func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "命令行工具").FontSize(settingsFontSize(c, 14)).FontWeight(600).Role(ui.RoleHeading).Level(2).Grow(1)
			badgeText, badgeColor := "未安装", t.TextMuted
			switch {
			case !status.Supported:
				badgeText = "不支持"
			case status.State == cliInstalled:
				badgeText, badgeColor = "已安装", t.Success
			case status.State == cliNeedsUpdate:
				badgeText, badgeColor = "需要更新", t.Warning
			case status.State == cliConflict:
				badgeText, badgeColor = "存在冲突", t.Danger
			}
			statusCapsule(c, badgeText, badgeColor).FontSize(settingsFontSize(c, 11))
		})
		detail := "在终端中直接使用 z-pic compress / watch / presets 批量处理图片"
		detailColor := metadataColor(c)
		switch status.State {
		case cliInstalled:
			detail = status.LinkPath
			if !status.InPATH {
				detail += "（未在 PATH 中）"
			}
			detailColor = t.TextMuted
		case cliNeedsUpdate:
			detail = fmt.Sprintf("%s 当前指向其他位置，点击更新为当前应用", status.LinkPath)
		case cliConflict:
			detail = fmt.Sprintf("%s 已被其他文件占用，未自动覆盖", status.LinkPath)
			detailColor = t.Danger
		}
		if !status.Supported {
			detail = "当前平台暂不支持一键安装命令行符号链接"
		}
		ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, detail).FontSize(settingsFontSize(c, 12)).TextColor(detailColor).
				Width(0).Grow(1).MinWidth(0).SingleLine().TruncateMiddle().Tooltip(detail)
			if status.Supported {
				switch status.State {
				case cliInstalled:
					settingsButton(c, "settings-cli-uninstall", "卸载", 28, locked).
						FontSize(settingsFontSize(c, 12)).Padding(0, 10).
						Tooltip("移除 z-pic 命令行符号链接").OnClick(s.runUninstallCLI)
				case cliNeedsUpdate:
					settingsButton(c, "settings-cli-update", "更新链接", 28, locked).
						FontSize(settingsFontSize(c, 12)).Padding(0, 10).
						Tooltip("更新命令行链接指向当前应用").OnClick(s.runInstallCLI)
					settingsButton(c, "settings-cli-uninstall", "卸载", 28, locked).
						FontSize(settingsFontSize(c, 12)).Padding(0, 10).OnClick(s.runUninstallCLI)
				case cliConflict:
					settingsButton(c, "settings-cli-refresh", "重新检测", 28, locked).
						FontSize(settingsFontSize(c, 12)).Padding(0, 10).
						Tooltip("重新检测命令行工具安装状态").OnClick(s.refreshCLIInstallStatus)
				default:
					settingsButton(c, "settings-cli-install", "安装命令行工具", 28, locked).
						FontSize(settingsFontSize(c, 12)).Padding(0, 12).
						Tooltip("在 PATH 目录创建 z-pic 符号链接").OnClick(s.runInstallCLI)
				}
			}
		})
		if editor.cliMessage != "" {
			msgColor := t.Success
			if editor.cliFailed {
				msgColor = t.Danger
			}
			ui.Text(c, editor.cliMessage).FontSize(settingsFontSize(c, 12)).TextColor(msgColor).MaxLines(2)
		}
	})
}

func (s *application) quickActionSettingsPanel(c *ui.Context, compactHeight bool) {
	t := c.Theme()
	editor := s.editor
	locked := s.settingsDraftLocked()
	status := editor.quickActionStatus
	panel := settingsPanel(c, "settings-quick-action-panel")
	if compactHeight {
		panel.Padding(12, 16).Gap(8)
	}
	panel.Children(func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "访达右键快捷操作").FontSize(settingsFontSize(c, 14)).FontWeight(600).Role(ui.RoleHeading).Level(2).Grow(1)
			badgeText, badgeColor := "未安装", t.TextMuted
			switch {
			case !status.Supported:
				badgeText = "仅 macOS"
			case status.State == quickActionInstalled:
				badgeText, badgeColor = "已安装", t.Success
			case status.State == quickActionNeedsUpdate:
				badgeText, badgeColor = "需要更新", t.Warning
			case status.State == quickActionConflict:
				badgeText, badgeColor = "存在冲突", t.Danger
			}
			statusCapsule(c, badgeText, badgeColor).FontSize(settingsFontSize(c, 11))
		})
		detail := "在访达中右键图片或文件夹 → 快速操作 →「使用 Z Pic 压缩」，按当前预设生成压缩副本"
		detailColor := metadataColor(c)
		switch status.State {
		case quickActionInstalled:
			detail = status.BundlePath
			detailColor = t.TextMuted
		case quickActionNeedsUpdate:
			detail = fmt.Sprintf("%s 指向旧版路径，点击更新为当前应用", status.BundlePath)
		case quickActionConflict:
			detail = fmt.Sprintf("%s 已被其他快捷操作占用，未自动覆盖", status.BundlePath)
			detailColor = t.Danger
		}
		if !status.Supported {
			detail = "访达右键快捷操作仅在 macOS 上可用"
		}
		qaLocked := locked || editor.quickActionBusy
		ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, detail).FontSize(settingsFontSize(c, 12)).TextColor(detailColor).
				Width(0).Grow(1).MinWidth(0).SingleLine().TruncateMiddle().Tooltip(detail)
			if status.Supported {
				switch status.State {
				case quickActionInstalled:
					settingsButton(c, "settings-qa-uninstall", "卸载", 28, qaLocked).
						FontSize(settingsFontSize(c, 12)).Padding(0, 10).
						Tooltip("移除访达右键快捷操作").OnClick(s.runUninstallQuickAction)
				case quickActionNeedsUpdate:
					settingsButton(c, "settings-qa-update", "更新快捷操作", 28, qaLocked).
						FontSize(settingsFontSize(c, 12)).Padding(0, 10).
						Tooltip("更新访达快捷操作指向当前应用").OnClick(s.runInstallQuickAction)
					settingsButton(c, "settings-qa-uninstall", "卸载", 28, qaLocked).
						FontSize(settingsFontSize(c, 12)).Padding(0, 10).OnClick(s.runUninstallQuickAction)
				case quickActionConflict:
					settingsButton(c, "settings-qa-refresh", "重新检测", 28, qaLocked).
						FontSize(settingsFontSize(c, 12)).Padding(0, 10).
						Tooltip("重新检测访达快捷操作状态").OnClick(s.refreshQuickActionStatus)
				default:
					settingsButton(c, "settings-qa-install", "安装快捷操作", 28, qaLocked).
						FontSize(settingsFontSize(c, 12)).Padding(0, 12).
						Tooltip("安装访达右键快速操作「使用 Z Pic 压缩」").OnClick(s.runInstallQuickAction)
				}
			}
		})
		if editor.quickActionMessage != "" {
			msgColor := t.Success
			if editor.quickActionFailed {
				msgColor = t.Danger
			} else if editor.quickActionBusy {
				msgColor = t.TextMuted
			}
			ui.Text(c, editor.quickActionMessage).FontSize(settingsFontSize(c, 12)).TextColor(msgColor).MaxLines(2)
		}
	})
}

func (s *application) aboutAndUpdateSettingsPanel(c *ui.Context, compactHeight bool) {
	t := c.Theme()
	locked := s.settingsDraftLocked()
	checking := s.updateState == updateChecking
	panel := settingsPanel(c, "settings-about-panel")
	if compactHeight {
		panel.Padding(12, 16).Gap(8)
	}
	panel.Children(func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "关于与更新").FontSize(settingsFontSize(c, 14)).FontWeight(600).Role(ui.RoleHeading).Level(2).Grow(1)
			statusCapsule(c, "v"+normalizeVersionLabel(appVersion), t.TextMuted).FontSize(settingsFontSize(c, 11))
			switch s.updateState {
			case updateChecking:
				statusCapsule(c, "检查中…", t.Accent).FontSize(settingsFontSize(c, 11))
			case updateUpToDate:
				statusCapsule(c, "已是最新", t.Success).FontSize(settingsFontSize(c, 11))
			case updateAvailable:
				statusCapsule(c, "新版本 v"+s.updateResult.LatestVersion, t.Warning).FontSize(settingsFontSize(c, 11))
			case updateFailed:
				statusCapsule(c, "检查失败", t.Danger).FontSize(settingsFontSize(c, 11))
			}
		})
		detail := "从 GitHub Releases 检查 Z Pic 最新正式版本"
		detailColor := metadataColor(c)
		if s.updateMessage != "" {
			detail = s.updateMessage
			switch {
			case s.updateMessageFailed || s.updateState == updateFailed:
				detailColor = t.Danger
			case s.updateState == updateAvailable:
				detailColor = t.Text
			case s.updateState == updateUpToDate:
				detailColor = t.Success
			}
		}
		ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, detail).FontSize(settingsFontSize(c, 12)).TextColor(detailColor).
				Width(0).Grow(1).MinWidth(0).SingleLine().TruncateMiddle().Tooltip(detail)
			if s.updateState == updateAvailable {
				h := settingsControlHeight(c, 28)
				downloadBtn := primaryAction(c, "settings-update-download", "前往下载", locked).
					FontSize(settingsFontSize(c, 12)).Height(h).Radius(pillRadius(h, c)).Padding(0, 12).
					Tooltip("在浏览器中打开发布页面").OnClick(s.openReleaseDownloadPage)
				if usesGlass(c) && !locked {
					settingsGlassControl(downloadBtn, c, 28, false, t.AccentHover).TextColor(t.AccentText)
				}
			}
			checkLabel := "检查更新"
			if checking {
				checkLabel = "检查中…"
			}
			settingsButton(c, "settings-check-update", checkLabel, 28, locked || checking).
				FontSize(settingsFontSize(c, 12)).Padding(0, 12).
				Tooltip("检查 GitHub Releases 是否有新版本").OnClick(s.checkAppUpdates)
		})
	})
}

func (s *application) settingsThemeSelection(c *ui.Context) {
	t := c.Theme()
	_, height := c.Size()
	cardHeight := float32(74)
	if height > 0 && height <= 620 {
		cardHeight = 68
	}
	selected := 0
	for index, option := range themeOptions {
		if option.mode == s.editor.theme.normalized() {
			selected = index
		}
	}
	choice := ui.SegmentedBase(c.Key("settings-theme"), &selected, len(themeOptions))
	locked := s.settingsDraftLocked()
	choice.Track.Label("外观主题").Height(settingsControlHeight(c, cardHeight)).Gap(10).AlignItems(ui.Stretch).Disabled(locked).
		Children(func() {
			for index, option := range themeOptions {
				active := index == selected
				segment := choice.Segment(index).Label(option.label).Width(0).MinWidth(0).
					Center().Radius(innerRadius(c)).Padding(6, 10).
					Background(tonalWellSurface(c)).Border(1, controlBorder(c))
				color := t.TextMuted
				if active {
					color = accentForeground(t)
					segment.Background(glassSelectionBackground(c)).Border(1.5, selectionBorder(c))
					controlElevation(segment, c)
					settingsGlassControl(segment, c, cardHeight, locked, t.Accent.Alpha(0.14)).
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
					ui.Column(c).Gap(6).AlignItems(ui.Center).Children(func() {
						ui.Box(c).Size(58, 34).Draw(func(p *ui.Painter, r ui.Rect) {
							lightWin, darkWin := ui.Hex("#eef1f5"), ui.Hex("#141720")
							lightSide, darkSide := ui.Hex("#ffffff"), ui.Hex("#1e2432")
							lightCard, darkCard := ui.Hex("#dce2ec"), ui.Hex("#283042")
							lightLine, darkLine := ui.RGBA(17, 20, 26, 0.08), ui.RGBA(255, 255, 255, 0.09)
							drawTrafficLights := func(x, y float32, dark bool) {
								if dark {
									for i := range 3 {
										p.Fill(ui.Rect{X: x + float32(i)*4.5, Y: y, W: 2.5, H: 2.5}, ui.Hex("#505a70"), 1.25)
									}
									return
								}
								dots := [3]ui.Color{ui.Hex("#e26b63"), ui.Hex("#e8b64c"), ui.Hex("#63be68")}
								for i, dot := range dots {
									p.Fill(ui.Rect{X: x + float32(i)*4.5, Y: y, W: 2.5, H: 2.5}, dot.Alpha(0.82), 1.25)
								}
							}
							if !t.Dark {
								p.Shadow(r, 5, 0, 1, 3, 0, ui.RGBA(17, 20, 26, 0.07))
							}
							p.Clip(r, 5, func() {
								switch mode {
								case themeLight:
									p.Fill(r, lightWin, 0)
									sideX := r.X + r.W*0.60
									p.Fill(ui.Rect{X: sideX, Y: r.Y, W: r.W - r.W*0.60, H: r.H}, lightSide, 0)
									p.Line(sideX, r.Y, sideX, r.Y+r.H, 1, lightLine)
									p.Line(r.X, r.Y+9, r.X+r.W, r.Y+9, 1, lightLine)
									drawTrafficLights(r.X+4.5, r.Y+3.5, false)
									p.Fill(ui.Rect{X: r.X + 5, Y: r.Y + 12.5, W: 24, H: 16}, lightCard, 2.5)
									p.Fill(ui.Rect{X: sideX + 4, Y: r.Y + 12.5, W: r.W*0.40 - 8, H: 3.5}, lightCard, 1.75)
									p.Fill(ui.Rect{X: sideX + 4, Y: r.Y + 23.5, W: r.W*0.40 - 8, H: 5}, t.Accent, 2.5)
								case themeDark:
									p.Fill(r, darkWin, 0)
									sideX := r.X + r.W*0.60
									p.Fill(ui.Rect{X: sideX, Y: r.Y, W: r.W - r.W*0.60, H: r.H}, darkSide, 0)
									p.Line(sideX, r.Y, sideX, r.Y+r.H, 1, darkLine)
									p.Line(r.X, r.Y+9, r.X+r.W, r.Y+9, 1, darkLine)
									drawTrafficLights(r.X+4.5, r.Y+3.5, true)
									p.Fill(ui.Rect{X: r.X + 5, Y: r.Y + 12.5, W: 24, H: 16}, darkCard, 2.5)
									p.Fill(ui.Rect{X: sideX + 4, Y: r.Y + 12.5, W: r.W*0.40 - 8, H: 3.5}, ui.Hex("#364056"), 1.75)
									p.Fill(ui.Rect{X: sideX + 4, Y: r.Y + 23.5, W: r.W*0.40 - 8, H: 5}, accentForeground(ui.DarkTheme()), 2.5)
								default:
									midX := r.X + r.W/2
									p.Fill(ui.Rect{X: r.X, Y: r.Y, W: r.W / 2, H: r.H}, lightWin, 0)
									p.Fill(ui.Rect{X: midX, Y: r.Y, W: r.W / 2, H: r.H}, darkWin, 0)
									p.Line(r.X, r.Y+9, midX, r.Y+9, 1, lightLine)
									p.Line(midX, r.Y+9, r.X+r.W, r.Y+9, 1, darkLine)
									p.Line(midX, r.Y, midX, r.Y+r.H, 1, ui.RGBA(120, 130, 150, 0.28))
									drawTrafficLights(r.X+4.5, r.Y+3.5, false)
									p.Fill(ui.Rect{X: r.X + 5, Y: r.Y + 12.5, W: 19, H: 16}, lightCard, 2.5)
									p.Fill(ui.Rect{X: midX + 4.5, Y: r.Y + 12.5, W: 20, H: 16}, darkSide, 2.5)
									p.Fill(ui.Rect{X: midX + 7.5, Y: r.Y + 21, W: 14, H: 4}, accentForeground(ui.DarkTheme()), 2)
								}
							})
							frameBorder := controlBorder(c)
							if !t.Dark {
								frameBorder = t.Text.Alpha(0.14)
							}
							p.Stroke(r, frameBorder, 5, 1)
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
		ui.Row(c).Padding(10, padding).Gap(8).Background(statusTintSurface(c, t.Danger)).AlignItems(ui.Center).Children(func() {
			name := presetDisplayName(*editor.selectedPreset())
			short := []rune(name)
			if len(short) > 16 {
				short = append(short[:16], '…')
			}
			message := fmt.Sprintf("删除「%s」？保存后生效。", string(short))
			ui.Text(c, message).FontSize(settingsFontSize(c, 13)).FontWeight(500).Width(0).Grow(1).MaxLines(2).
				Tooltip("删除「" + name + "」？保存后生效。")
			keep := settingsButton(c, "cancel-delete", "保留预设", 30, false).Padding(0, 12).
				OnClick(func() {
					editor.confirmDelete = false
					editor.focusField = "delete-preset"
				})
			s.focusSettingsFieldDuringDelete(keep)
			solidDestructiveAction(c, "confirm-delete", "确认删除", settingsControlHeight(c, 30), false).
				FontSize(settingsFontSize(c, 13)).Padding(0, 12).OnClick(s.deletePreset)
		})
		ui.Box(c).FillWidth().Height(1).Background(hairlineDivider(c))
	}
	ui.Column(c).Shrink(0).Padding(10, padding).Gap(8).Background(sidebarSurface(c)).Children(func() {
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
		statusAbove := editor.message != "" ||
			(message != "" && width < 560) ||
			(editor.section == settingsPresets && width < 640 && (presetChanged || editor.notice != ""))
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
				iconLabelButton(c, ghostButton(c, "duplicate-preset", "", settingsControlHeight(c, 32), s.settingsDraftLocked()).Padding(0, 10),
					"复制预设", actionCopyIcon, settingsFontSize(c, 13), 500).OnClick(s.duplicatePreset)
				deleteButton := iconLabelButton(c, ghostDestructiveButton(c, "delete-preset", "", settingsControlHeight(c, 32), s.settingsDraftLocked() || len(editor.presets) <= 1).Padding(0, 10),
					"删除预设", actionTrashIcon, settingsFontSize(c, 13), 500).OnClick(s.requestDeletePreset)
				if len(editor.presets) <= 1 {
					deleteButton.Tooltip("至少保留一个预设")
				}
				s.focusSettingsField(deleteButton, "delete-preset")
				if !statusAbove && message != "" {
					ui.Box(c).Size(1, 14).Background(hairlineDivider(c))
				}
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
