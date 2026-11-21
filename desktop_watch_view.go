package main

import (
	"fmt"
	"path/filepath"

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
		Padding(16, settingsPagePadding(c)).Gap(12).Children(func() {
		settingsPanel(c, "folder-watch-panel").Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, folderWatchIcon).Size(16, 16).TextColor(t.TextMuted)
				ui.Text(c, "文件夹监听").FontSize(settingsFontSize(c, 16)).FontWeight(600).Role(ui.RoleHeading).Level(2)
			})
			ui.Text(c, "仅处理监听开始后加入输入目录顶层的图片，请在输入目录之外选择输出位置").
				FontSize(settingsFontSize(c, 12)).TextColor(metadataColor(c)).MaxLines(2)
			s.folderWatchDirectoryField(c, "watch-input-dir", "输入目录", editor.watch.InputDir, locked, func() {
				s.chooseFolderWatchDirectory(false)
			})
			s.folderWatchDirectoryField(c, "watch-output-dir", "输出目录", editor.watch.OutputDir, locked, func() {
				s.chooseFolderWatchDirectory(true)
			})
			ui.Column(c).FillWidth().Gap(8).Children(func() {
				ui.Text(c, "监听预设").FontSize(settingsFontSize(c, 13)).FontWeight(500)
				s.folderWatchPresetPicker(c, locked)
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
				ui.Row(c).FillWidth().Justify(ui.End).Children(func() {
					primaryAction(c, "start-folder-watch", "开始监听", disabled).
						Height(settingsControlHeight(c, 36)).FontSize(settingsFontSize(c, 13)).OnClick(func() {
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
	})
}

func (s *application) folderWatchDirectoryField(c *ui.Context, key, label, dir string, disabled bool, choose func()) {
	t := c.Theme()
	display := dir
	textColor := t.TextMuted
	if display == "" {
		display = "未选择文件夹"
		textColor = metadataColor(c)
	}
	ui.Column(c).FillWidth().Gap(8).Children(func() {
		ui.Text(c, label).FontSize(settingsFontSize(c, 13)).FontWeight(500)
		ui.Row(c).FillWidth().Gap(8).Padding(8, 12).Radius(innerRadius(c)).
			Background(panelSubtleSurface(c)).Border(1, glassBorder(c)).AlignItems(ui.Center).Children(func() {
			ui.Text(c, display).FontSize(settingsFontSize(c, 12)).TextColor(textColor).
				Width(0).Grow(1).MinWidth(0).SingleLine().TruncateMiddle().Tooltip(display)
			settingsButton(c, key, "选择…", 32, disabled).Label("选择" + label).Shrink(0).OnClick(choose)
		})
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
		At: ui.AnchorBottomStart, Self: ui.AnchorTopStart,
		Panel: func(panel ui.Element) {
			stylePopoverPanel(panel, c).MinWidth(max(180, picker.Bounds().W)).MaxWidth(360)
		},
	}).Label("监听预设").Value(selected).Tooltip(selected).FillWidth().MinWidth(0).
		Height(settingsControlHeight(c, 36)).Padding(0, 12).Gap(8).AlignItems(ui.Center).
		Radius(innerRadius(c)).Background(glassControlSurface(c)).Border(1, controlBorder(c)).Disabled(disabled)
	if !disabled {
		if picker.Pressed() {
			picker.Background(glassControlPressed(c))
		} else if picker.Hovered() {
			picker.Background(glassControlHover(c))
		}
	}
	settingsGlassControl(picker, c, 36, disabled, ui.Transparent)
	picker.Children(func() {
		ui.Text(c, selected).FontSize(settingsFontSize(c, 13)).FontWeight(500).Width(0).Grow(1).MinWidth(0).SingleLine().TruncateMiddle()
		ui.Icon(c, settingsChevronIcon).Size(16, 16).Shrink(0).TextColor(t.TextMuted)
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
	ui.Row(c.Key("folder-watch-status")).FillWidth().Gap(8).AlignItems(ui.Center).
		Padding(8, 12).Radius(innerRadius(c)).
		Background(panelSubtleSurface(c)).Border(1, glassBorder(c)).Children(func() {
		statusCapsule(c, status, color).FontSize(fontSize).Role(ui.RoleStatus)
		ui.Column(c).Width(0).Grow(1).MinWidth(0).Gap(4).Children(func() {
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
			quietButton(c, "stop-folder-watch", label, 28, session.stopping).FontSize(fontSize).Shrink(0).OnClick(s.stopFolderWatch)
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
