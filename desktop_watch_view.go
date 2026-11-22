package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"zpic/internal/compress"
)

var folderWatchIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="M2.5 4.25A1.25 1.25 0 0 1 3.75 3h2.6l1.4 1.5h4.5A1.25 1.25 0 0 1 13.5 5.75v6A1.25 1.25 0 0 1 12.25 13h-8.5A1.25 1.25 0 0 1 2.5 11.75Z"/><circle cx="8" cy="8.75" r="1.75"/></svg>`))

func (s *application) folderWatchSettingsContent(c *ui.Context) {
	editor := s.editor
	t := c.Theme()
	locked := s.settingsDraftLocked() || s.watch.session != nil
	ui.Scroll(c.Key("folder-watch-settings")).FillWidth().Grow(1).MinHeight(0).
		Padding(12, settingsPagePadding(c)).Gap(12).Children(func() {
		settingsPanel(c, "folder-watch-panel").Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, folderWatchIcon).Size(15, 15).TextColor(metadataColor(c))
				ui.Text(c, "文件夹监听").FontSize(settingsFontSize(c, 14)).FontWeight(600).Role(ui.RoleHeading).Level(2)
			})
			ui.Text(c, "仅处理监听开始后加入输入目录顶层的图片，请在输入目录之外选择输出位置").
				FontSize(settingsFontSize(c, 12)).TextColor(metadataColor(c)).MaxLines(2)
			ui.Column(c).FillWidth().Gap(0).Children(func() {
				s.folderWatchDirectoryField(c, "watch-input-dir", "输入目录", editor.watch.InputDir, locked, func() {
					s.chooseFolderWatchDirectory(false)
				})
				ui.Box(c).FillWidth().Height(1).Background(subtleRowDivider(c))
				s.folderWatchDirectoryField(c, "watch-output-dir", "输出目录", editor.watch.OutputDir, locked, func() {
					s.chooseFolderWatchDirectory(true)
				})
				ui.Box(c).FillWidth().Height(1).Background(subtleRowDivider(c))
				ui.Row(c).FillWidth().MinHeight(settingsControlHeight(c, 46)).Padding(8, 0, 2, 0).Gap(12).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "监听预设").FontSize(settingsFontSize(c, 13)).FontWeight(500).TextColor(t.Text).Shrink(0)
					ui.Box(c).Width(0).Grow(1).MinWidth(0)
					s.folderWatchPresetPicker(c, locked)
				})
			})
			if _, ok := s.folderWatchPreset(editor.watch.Preset); !ok && s.watch.session == nil {
				ui.Text(c, "所选监听预设已不可用，请重新选择").Role(ui.RoleAlert).
					FontSize(settingsFontSize(c, 12)).TextColor(t.Danger)
			}
			if editor.watchMessage != "" {
				ui.Text(c, editor.watchMessage).Role(ui.RoleAlert).
					FontSize(settingsFontSize(c, 12)).TextColor(t.Danger)
			}
			s.renderFolderWatchStatusBar(c, true)
			if s.watch.session == nil {
				_, validPreset := s.folderWatchPreset(editor.watch.Preset)
				disabled := locked || editor.watch.InputDir == "" || editor.watch.OutputDir == "" || !validPreset
				hint := "就绪 · 点击开始自动压缩新增图片"
				if editor.watch.InputDir == "" || editor.watch.OutputDir == "" {
					hint = "请先选择输入目录与输出目录"
				} else if !validPreset {
					hint = "请先选择可用的监听预设"
				}
				ui.Box(c).FillWidth().Height(1).Background(subtleRowDivider(c))
				ui.Row(c).FillWidth().Padding(4, 0, 0, 0).Gap(12).AlignItems(ui.Center).Children(func() {
					ui.Row(c).Width(0).Grow(1).MinWidth(0).Gap(6).AlignItems(ui.Center).Children(func() {
						dotColor := metadataColor(c).Alpha(0.55)
						if !disabled {
							dotColor = t.Success
						}
						ui.Box(c).Size(6, 6).Radius(3).Shrink(0).Background(dotColor)
						ui.Text(c, hint).FontSize(settingsFontSize(c, 12)).TextColor(metadataColor(c)).
							Width(0).Grow(1).MinWidth(0).SingleLine().TruncateMiddle().Tooltip(hint)
					})
					iconLabelButton(c, primaryAction(c, "start-folder-watch", "", disabled).
						Height(settingsControlHeight(c, 34)).Radius(pillRadius(settingsControlHeight(c, 34), c)).
						Padding(0, 14), "开始监听", toolbarContinueIcon, settingsFontSize(c, 13), 500).OnClick(func() {
						if s.settingsDraftLocked() || s.editor != editor {
							return
						}
						if err := s.startFolderWatch(editor.watch); err != nil {
							editor.watchMessage = err.Error()
							return
						}
						editor.watch, editor.watchMessage = s.watch.session.settings, ""
					})
				})
			}
		})
		settingsPanel(c, "folder-watch-rules-panel").Padding(12, 16).Gap(8).Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, presetInfoIcon).Size(14, 14).TextColor(metadataColor(c)).Shrink(0)
				ui.Text(c, "监听说明").FontSize(settingsFontSize(c, 13)).FontWeight(600).TextColor(t.Text)
			})
			rules := [...]struct {
				title string
				body  string
			}{
				{"触发范围", "仅处理监听启动后新写入输入目录顶层的图片，不改动已有历史文件"},
				{"支持格式", strings.Join(supportedImageFormats(), " / ") + "，按所选预设输出副本且保留原图"},
				{"状态控制", "运行期间可在主窗口底部状态栏查看最近处理结果或随时停止监听"},
			}
			ui.Column(c).FillWidth().Gap(0).Children(func() {
				for index, rule := range rules {
					if index > 0 {
						ui.Box(c).FillWidth().Height(1).Background(subtleRowDivider(c))
					}
					ui.Row(c).FillWidth().Padding(6, 0).Gap(12).AlignItems(ui.Start).Children(func() {
						ui.Text(c, rule.title).FontSize(settingsFontSize(c, 12)).FontWeight(500).
							TextColor(metadataColor(c)).Width(settingsFontSize(c, 60)).Shrink(0).SingleLine()
						ui.Text(c, rule.body).FontSize(settingsFontSize(c, 12)).TextColor(t.TextMuted).
							Width(0).Grow(1).MinWidth(0).MaxLines(2)
					})
				}
			})
		})
	})
}

func (s *application) folderWatchDirectoryField(c *ui.Context, key, label, dir string, disabled bool, choose func()) {
	t := c.Theme()
	display := dir
	textColor := t.TextMuted
	iconColor := metadataColor(c)
	if display == "" {
		display = "未选择文件夹"
		textColor = metadataColor(c)
		iconColor = metadataColor(c).Alpha(0.65)
	}
	ui.Row(c).FillWidth().MinHeight(settingsControlHeight(c, 46)).Padding(6, 0).Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Width(0).Grow(1).MinWidth(0).Gap(3).Children(func() {
			ui.Text(c, label).FontSize(settingsFontSize(c, 13)).FontWeight(500).TextColor(t.Text).SingleLine()
			ui.Row(c).FillWidth().Gap(5).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, actionFolderIcon).Size(13, 13).TextColor(iconColor).Shrink(0)
				ui.Text(c, display).FontSize(settingsFontSize(c, 12)).TextColor(textColor).
					Width(0).Grow(1).MinWidth(0).SingleLine().TruncateMiddle().Tooltip(display)
			})
		})
		settingsButton(c, key, "选择…", 28, disabled).FontSize(settingsFontSize(c, 12)).Padding(0, 10).
			Label("选择" + label).Shrink(0).OnClick(choose)
	})
}

func (s *application) folderWatchPresetPicker(c *ui.Context, disabled bool) {
	editor := s.editor
	selected := editor.watch.Preset
	if selected == "" {
		selected = s.scheme
	}
	t := c.Theme()
	picker := ui.ButtonBase(c.Key("watch-preset"))
	picker.PopoverMenuButton(func(menu *ui.Menu) {
		menu.MaxWidth(360)
		for _, preset := range s.presets {
			name := preset.name
			if menu.Item(name).Radio(name == selected).Tooltip(name).Disabled(disabled).Chosen() {
				editor.watch.Preset = name
				editor.watchMessage = ""
			}
		}
	}, ui.PopoverMenuButtonOptions{
		At: ui.AnchorBottomEnd, Self: ui.AnchorTopEnd,
		Panel: func(panel ui.Element) {
			stylePopoverPanel(panel, c).MinWidth(max(180, picker.Bounds().W)).MaxWidth(360)
		},
	}).Label("监听预设").Value(selected).Tooltip(selected).MinWidth(180).MaxWidth(280).
		Height(settingsControlHeight(c, 32)).Padding(0, 12).Gap(8).AlignItems(ui.Center).
		Radius(innerRadius(c)).Background(glassControlSurface(c)).Border(1, controlBorder(c)).Disabled(disabled)
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
	settingsGlassControl(picker, c, 32, disabled, ui.Transparent)
	picker.Children(func() {
		ui.Text(c, selected).FontSize(settingsFontSize(c, 13)).FontWeight(500).Width(0).Grow(1).MinWidth(0).SingleLine().TruncateMiddle()
		ui.Icon(c, settingsChevronIcon).Size(15, 15).Shrink(0).TextColor(t.TextMuted)
	})
}

func (s *application) chooseFolderWatchDirectory(output bool) {
	if s.settingsDraftLocked() || s.watch.session != nil {
		return
	}
	editor, parent := s.editor, s.settingsWindow
	title, dir := "选择输入目录", &editor.watch.InputDir
	if output {
		title, dir = "选择输出目录", &editor.watch.OutputDir
	}
	defaultPath := *dir
	s.busy = true
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent: parent, Title: title, ButtonLabel: "选择此文件夹",
			DefaultPath: defaultPath, Directory: true, CreateDirectories: output,
		})
		s.updateWithInvalidate(false, func() {
			s.busy = false
			if s.editor != editor {
				return
			}
			if err != nil {
				editor.watchMessage = title + "失败：" + err.Error()
			} else if len(paths) > 0 {
				*dir, editor.watchMessage = paths[0], ""
			}
			if s.settingsWindow != nil {
				s.settingsWindow.Invalidate()
			}
		})
	}()
}

func (s *application) folderWatchStatusBar(c *ui.Context) {
	s.renderFolderWatchStatusBar(c, false)
}

func (s *application) renderFolderWatchStatusBar(c *ui.Context, inSettings bool) {
	watch := &s.watch
	if watch.session == nil && watch.last.Input == "" && watch.err == nil {
		return
	}
	t := c.Theme()
	fontSize := float32(12)
	if inSettings {
		fontSize = settingsFontSize(c, 12)
	}
	status, color := "监听已停止", t.TextMuted
	session := watch.session
	if session != nil {
		switch {
		case session.stopping:
			status = "正在停止监听"
		case session.ready:
			status, color = "正在监听", t.Success
		default:
			status = "正在启动监听"
		}
	}
	surface, border := tonalWellSurface(c), tonalWellBorder(c)
	if watch.err != nil {
		surface, border = statusTintSurface(c, t.Danger), statusTintBorder(c, t.Danger)
	} else if session != nil && session.ready {
		surface, border = statusTintSurface(c, t.Success), statusTintBorder(c, t.Success)
	}
	ui.Row(c.Key("folder-watch-status")).FillWidth().Gap(8).AlignItems(ui.Center).
		Padding(7, 10).Radius(innerRadius(c)).
		Background(surface).Border(1, border).Children(func() {
		statusCapsule(c, status, color).FontSize(fontSize).Role(ui.RoleStatus)
		ui.Column(c).Width(0).Grow(1).MinWidth(0).Gap(2).Children(func() {
			if watch.err != nil {
				message := watch.err.Error()
				ui.Text(c, message).Role(ui.RoleAlert).FontSize(fontSize).TextColor(t.Danger).
					MaxLines(2).Tooltip(message)
			}
			if watch.last.Input != "" {
				folderWatchOutcomeLine(c, watch.last, fontSize)
			} else if session != nil && watch.err == nil {
				ui.Text(c, session.settings.Preset).FontSize(fontSize).TextColor(t.TextMuted).SingleLine().TruncateMiddle().
					Tooltip(session.settings.InputDir + " → " + session.settings.OutputDir)
			}
		})
		if session != nil {
			label := "停止监听"
			if session.stopping {
				label = "正在停止…"
			}
			ghostDestructiveButton(c, "stop-folder-watch", label, 28, session.stopping).
				FontSize(fontSize).FontWeight(500).Padding(0, 10).Shrink(0).OnClick(s.stopFolderWatch)
		}
	})
}

func folderWatchOutcomeLine(c *ui.Context, outcome folderWatchOutcome, fontSize float32) {
	name := filepath.Base(outcome.Input)
	t := c.Theme()
	color, role := t.TextMuted, ui.RoleStatus
	var message string
	switch {
	case outcome.Err != nil:
		color, role = t.Danger, ui.RoleAlert
		message = fmt.Sprintf("处理失败 · %s · %v", name, outcome.Err)
	case outcome.TargetSizeMissed:
		color = t.Warning
		message = fmt.Sprintf("未达目标 · %s · %s / %s（已保存）", name,
			formatSize(outcome.OutputBytes), compress.FormatTargetSize(outcome.TargetBytes))
	case outcome.Unchanged:
		message = fmt.Sprintf("体积未变 · %s（已保存副本）", name)
	default:
		message = fmt.Sprintf("已保存 · %s · %s → %s", name, formatSize(outcome.OriginalBytes), formatSize(outcome.OutputBytes))
	}
	detail := message
	if outcome.Output != "" {
		detail += "\n" + outcome.Output
	}
	ui.Text(c.Key("folder-watch-last-outcome"), message).Role(role).FontSize(fontSize).FontFeatures("tnum").
		TextColor(color).SingleLine().TruncateMiddle().Tooltip(detail)
}
