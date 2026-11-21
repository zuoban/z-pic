package main

import (
	"fmt"
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"zpic/internal/compress"
)

func (s *application) folderWatchSettingsContent(c *ui.Context) {
	editor := s.editor
	t := c.Theme()
	locked := s.settingsDraftLocked() || s.watch.session != nil
	ui.Scroll(c.Key("folder-watch-settings")).FillWidth().Grow(1).MinHeight(0).
		Padding(t.Space(4), settingsPagePadding(c)).Children(func() {
		settingsPanel(c, "folder-watch-panel").Children(func() {
			ui.Text(c, "仅处理监听开始后加入输入目录顶层的图片。请在输入目录之外选择输出位置。").
				FontSize(settingsFontSize(c, 13)).TextColor(t.TextMuted)
			ui.Form(c, func() {
				s.folderWatchDirectoryField(c, "watch-input-dir", "输入目录", editor.watch.InputDir, locked, func() {
					s.chooseFolderWatchDirectory(false)
				})
				s.folderWatchDirectoryField(c, "watch-output-dir", "输出目录", editor.watch.OutputDir, locked, func() {
					s.chooseFolderWatchDirectory(true)
				})
				ui.Field(c, "监听预设", func() { s.folderWatchPresetPicker(c, locked) })
			}).FillWidth()
			if _, ok := s.folderWatchPreset(editor.watch.Preset); !ok && s.watch.session == nil {
				ui.Text(c, "所选监听预设已不可用，请重新选择").Role(ui.RoleAlert).
					FontSize(settingsFontSize(c, 12)).TextColor(t.Danger)
			}
			if editor.watchMessage != "" {
				ui.Text(c, editor.watchMessage).Role(ui.RoleAlert).
					FontSize(settingsFontSize(c, 12)).TextColor(t.Danger)
			}
			s.folderWatchStatusBar(c)
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
	display := dir
	if display == "" {
		display = "未选择"
	}
	ui.Field(c, label, func() {
		ui.Row(c).FillWidth().Gap(c.Theme().Space(2)).AlignItems(ui.Center).Children(func() {
			ui.Text(c, display).FontSize(settingsFontSize(c, 13)).TextColor(c.Theme().TextMuted).
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
	picker := ui.SelectBase(c.Key("watch-preset"), &editor.watch.Preset)
	trigger := picker.Trigger.Label("监听预设").Value(selected).Tooltip(selected).FillWidth().MinWidth(0).
		Height(settingsControlHeight(c, 36)).Padding(0, t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).
		Radius(innerRadius(c)).Background(glassControlSurface(c)).Border(1, controlBorder(c)).Disabled(disabled)
	if !disabled {
		if trigger.Pressed() {
			trigger.Background(glassControlPressed(c))
		} else if trigger.Hovered() {
			trigger.Background(glassControlHover(c))
		}
	}
	settingsGlassControl(trigger, c, 36, disabled, ui.Transparent)
	trigger.Children(func() {
		ui.Text(c, selected).FontSize(settingsFontSize(c, 13)).Width(0).Grow(1).MinWidth(0).SingleLine().TruncateMiddle()
		ui.Icon(c, settingsChevronIcon).Size(16, 16).Shrink(0).TextColor(t.TextMuted)
	})
	if disabled && picker.Open() {
		picker.SetOpen(false)
	}
	picker.Popup(func(panel ui.Element) {
		panel.Padding(t.Space(1)).Radius(innerRadius(c)).Background(t.Surface).Border(1, glassBorder(c)).
			MaxHeight(t.Space(60))
		for _, preset := range s.presets {
			name := preset.name
			item := picker.Item(name).Padding(t.Space(1.5), t.Space(2)).Radius(innerRadius(c))
			color := t.Text
			if item.Highlighted() {
				item.Background(t.Accent)
				color = t.AccentText
			} else if name == selected {
				item.Background(panelSubtleSurface(c))
			}
			item.Children(func() {
				ui.Text(c, name).FontSize(settingsFontSize(c, 13)).TextColor(color).SingleLine().TruncateMiddle().Tooltip(name)
			})
		}
	})
	if trigger.Changed() {
		editor.watchMessage = ""
	}
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
	watch := &s.watch
	if watch.session == nil && watch.last.Input == "" && watch.err == nil {
		return
	}
	t := c.Theme()
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
	ui.Row(c.Key("folder-watch-status")).FillWidth().Gap(t.Space(2)).AlignItems(ui.Center).
		Padding(t.Space(1), t.Space(2)).Radius(innerRadius(c)).
		Background(panelSubtleSurface(c)).Border(1, glassBorder(c)).Children(func() {
		ui.Badge(c, status).Role(ui.RoleStatus).TextColor(color).Background(statusTintSurface(c, color)).Shrink(0)
		ui.Column(c).Width(0).Grow(1).MinWidth(0).Gap(t.Space(0.5)).Children(func() {
			if watch.err != nil {
				message := watch.err.Error()
				ui.Text(c, message).Role(ui.RoleAlert).FontSize(settingsFontSize(c, 12)).TextColor(t.Danger).
					MaxLines(2).Tooltip(message)
			}
			if watch.last.Input != "" {
				folderWatchOutcomeLine(c, watch.last)
			} else if session != nil && watch.err == nil {
				ui.Text(c, session.settings.Preset).FontSize(settingsFontSize(c, 12)).TextColor(t.TextMuted).SingleLine().TruncateMiddle().
					Tooltip(session.settings.InputDir + " → " + session.settings.OutputDir)
			}
		})
		if session != nil {
			label := "停止监听"
			if session.stopping {
				label = "正在停止…"
			}
			quietButton(c, "stop-folder-watch", label, 28, session.stopping).Shrink(0).OnClick(s.stopFolderWatch)
		}
	})
}

func folderWatchOutcomeLine(c *ui.Context, outcome folderWatchOutcome) {
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
	ui.Text(c.Key("folder-watch-last-outcome"), message).Role(role).FontSize(12).
		TextColor(color).SingleLine().Tooltip(detail)
}
