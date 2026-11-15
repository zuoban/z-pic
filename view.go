package main

import (
	"fmt"
	"math"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/egoist/mygo/ui"
)

func applyTheme(c *ui.Context) {
	theme := c.Theme().WithAccent(ui.Hex("#2563eb"))
	theme.Radius, theme.FontSize = 8, 13
	if theme.Dark {
		theme.Background = ui.Hex("#141416")
		theme.Surface = ui.Hex("#1d1d20")
		theme.SurfaceHover = ui.Hex("#29292e")
		theme.SurfacePressed = ui.Hex("#34343a")
		theme.Text = ui.Hex("#ededee")
		theme.TextMuted = ui.Hex("#a1a1aa")
	} else {
		theme.Background = ui.Hex("#f7f7f8")
		theme.Surface = ui.Hex("#ffffff")
		theme.SurfaceHover = ui.Hex("#f0f0f3")
		theme.SurfacePressed = ui.Hex("#e6e6eb")
		theme.Text = ui.Hex("#202025")
		theme.TextMuted = ui.Hex("#686870")
		theme.Success = ui.Hex("#15803d")
	}
	theme.Border = theme.Text.Alpha(0.10)
	c.SetTheme(theme)
}

func (s *application) view(c *ui.Context) {
	applyTheme(c)
	theme := c.Theme()
	if !s.busy && !s.confirmOverwrite && !s.settingsOpen {
		c.OnWindowShortcut(ui.Cmd, ui.KeyO, s.chooseFile)
		c.OnWindowShortcut(ui.Cmd, ui.KeyEnter, s.startCompression)
		c.OnWindowShortcut(ui.Cmd, ui.KeyS, s.saveCopy)
		c.OnWindowShortcut(ui.Cmd, ui.KeyComma, func() { s.setSettingsOpen(true) })
	}
	ui.Column(c).Fill().Padding(24).Gap(20).Background(theme.Background).Children(func() {
		ui.Row(c).Height(48).Gap(16).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Gap(4).Grow(1).Children(func() {
				ui.Text(c, "Z Pic").FontSize(24).FontWeight(600)
				ui.Text(c, "图片压缩，尺寸不变").FontSize(12).TextColor(theme.TextMuted)
			})
			ui.Text(c, "JPEG / PNG").FontSize(12).TextColor(theme.TextMuted)
			ui.Box(c).Size(1, 12).Background(theme.Border)
			ui.Text(c, "本地处理").FontSize(12).TextColor(theme.TextMuted)
			ui.Button(c.Key("open-settings"), "设置").Height(36).
				KeyShortcuts(ui.Cmd, ui.KeyComma).
				Tooltip("打开设置（" + shortcutLabel(",") + "）").
				Disabled(s.busy || s.confirmOverwrite).
				OnClick(func() { s.setSettingsOpen(true) })
		})
		ui.Row(c).Grow(1).FillWidth().Gap(20).AlignItems(ui.Stretch).Children(func() {
			s.previewPanel(c)
			s.controlsPanel(c)
		})
		ui.Row(c).Height(20).Gap(16).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "无需联网，图片不离开本机").FontSize(12).
				TextColor(theme.TextMuted).Grow(1)
			shortcutHint(c, "打开", "O")
			shortcutHint(c, "压缩", "↵")
			shortcutHint(c, "另存", "S")
			shortcutHint(c, "设置", ",")
		})
	})
	s.overwriteConfirmation(c)
}

func shortcutLabel(key string) string {
	if runtime.GOOS == "darwin" {
		return "⌘ " + key
	}
	if key == "↵" {
		key = "Enter"
	}
	return "Ctrl+" + key
}

func shortcutHint(c *ui.Context, label, key string) {
	t := c.Theme()
	ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).FontSize(12).TextColor(t.TextMuted)
		ui.Text(c, shortcutLabel(key)).FontSize(12).TextColor(t.TextMuted).
			Padding(0, 4).Radius(4).Border(1, t.Border)
	})
}

func (s *application) previewPanel(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Grow(1).MinWidth(340).Padding(16).Gap(12).
		Radius(16).Background(t.Surface).Border(1, t.Border).Children(func() {
		ui.Row(c).Height(32).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "图片预览").FontSize(14).FontWeight(500).Grow(1)
			if s.result != nil && !s.overwritten {
				ui.Segmented(c.Key("preview-mode"), &s.comparison.mode, "对比", "原图", "结果").
					Label("预览模式").FillEqually().Width(192).Height(32).
					Disabled(s.previewLocked())
			}
			if s.file != nil {
				ui.Button(c.Key("replace"), "更换图片").Height(32).
					KeyShortcuts(ui.Cmd, ui.KeyO).Tooltip("更换图片 · " + shortcutLabel("O")).
					Disabled(s.busy).OnClick(s.chooseFile)
			}
		})
		if s.preview != nil {
			s.previewToolbar(c)
		}
		ui.Box(c).Grow(1).FillWidth().MinHeight(220).Radius(8).
			Background(t.Background).Clip().Children(func() {
			if s.preview == nil {
				ui.Column(c).Fill().Center().Gap(12).Padding(20).Children(func() {
					ui.Box(c).Size(48, 48).Draw(func(p *ui.Painter, bounds ui.Rect) {
						p.Stroke(ui.Rect{X: bounds.X + 8, Y: bounds.Y + 8, W: 32, H: 32},
							t.TextMuted, 6, 1.5)
						p.Fill(ui.Rect{X: bounds.X + 15, Y: bounds.Y + 15, W: 5, H: 5},
							t.TextMuted, 3)
						p.StrokePolyline([][2]float32{
							{bounds.X + 10, bounds.Y + 34},
							{bounds.X + 22, bounds.Y + 23},
							{bounds.X + 38, bounds.Y + 36},
						}, 1.5, t.TextMuted)
					})
					ui.Text(c, "从一张图片开始").FontSize(20).FontWeight(500)
					ui.Text(c, "把图片拖到这里，或从文件中选择").FontSize(13).TextColor(t.TextMuted)
					ui.PrimaryButton(c.Key("select"), "选择图片").Height(40).Margin(4, 0).
						KeyShortcuts(ui.Cmd, ui.KeyO).Tooltip("选择图片 · " + shortcutLabel("O")).
						Disabled(s.busy).OnClick(s.chooseFile)
					ui.Text(c, "JPEG / PNG · 最大 64 MiB / 3200 万像素").
						FontSize(12).TextColor(t.TextMuted)
				})
				return
			}
			s.previewCanvas(c)
		})
		if s.file != nil {
			ui.Column(c).Gap(4).Children(func() {
				ui.Text(c, filepath.Base(s.file.path)).FontSize(13).FontWeight(500).
					SingleLine().TruncateMiddle().Tooltip(s.file.path)
				ui.Textf(c, "%s  ·  %d × %d  ·  %s",
					strings.ToUpper(s.file.format), s.file.width, s.file.height, formatSize(s.file.size)).
					FontSize(12).FontFeatures("tnum").TextColor(t.TextMuted)
			})
		} else {
			ui.Column(c).Gap(4).Children(func() {
				ui.Text(c, "预览细节，再决定如何保存").FontSize(13).FontWeight(500)
				ui.Text(c, "保留图片尺寸，支持另存副本或确认覆盖").FontSize(12).TextColor(t.TextMuted)
			})
		}
	})
}

func (s *application) controlsPanel(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Width(320).Shrink(0).Padding(16).Gap(8).Radius(16).
		Background(t.Surface).Border(1, t.Border).Children(func() {
		ui.Row(c).Height(24).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "压缩方案").FontSize(18).FontWeight(500).Grow(1)
		})
		ui.Text(c, "在「设置」中新增或编辑压缩方案。").
			FontSize(12).TextColor(t.TextMuted).Margin(4, 0)
		ui.Scroll(c.Key("schemes")).Grow(1).MinHeight(80).Children(func() {
			s.presetControl(c)
		})
		if s.file != nil && s.file.format == "png" {
			ui.Text(c, "PNG 使用有损压缩，渐变与半透明边缘可能变化。").
				FontSize(12).TextColor(t.TextMuted)
		}
		s.resultSummary(c)
		if s.busy {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c).Size(16, 16)
				ui.Text(c, s.phase).FontSize(12).Role(ui.RoleStatus).Grow(1).MaxLines(2)
			})
			if s.cancel != nil {
				ui.Button(c.Key("cancel"), "取消操作").Height(32).
					Disabled(s.canceling).OnClick(s.cancelWork)
			}
		} else if s.message != "" {
			color := t.TextMuted
			role := ui.RoleStatus
			if s.failed {
				color, role = t.Danger, ui.RoleAlert
			}
			ui.Text(c, s.message).FontSize(12).TextColor(color).Role(role).
				MaxLines(2).Tooltip(s.message)
		}
		if s.result != nil {
			ui.Row(c).Gap(8).Children(func() {
				ui.Button(c.Key("compress"), "重新压缩").Grow(1).Height(36).
					KeyShortcuts(ui.Cmd, ui.KeyEnter).Tooltip("重新压缩 · " + shortcutLabel("↵")).
					Disabled(s.busy).OnClick(s.startCompression)
				ui.PrimaryButton(c.Key("save"), "另存副本").Grow(1).Height(36).
					KeyShortcuts(ui.Cmd, ui.KeyS).Tooltip("另存副本 · " + shortcutLabel("S")).
					Disabled(s.busy).OnClick(s.saveCopy)
			})
			label := "覆盖原图"
			if s.overwritten {
				label = "已覆盖原图"
			}
			ui.Button(c.Key("overwrite"), label).Height(32).
				TextColor(t.Danger).Disabled(!s.canOverwrite()).OnClick(s.requestOverwrite)
		} else {
			ui.PrimaryButton(c.Key("compress"), "开始压缩").Height(40).
				KeyShortcuts(ui.Cmd, ui.KeyEnter).Tooltip("开始压缩 · " + shortcutLabel("↵")).
				Disabled(s.busy || s.file == nil).OnClick(s.startCompression)
		}
		ui.Text(c, "不缩放尺寸 · 请保留原图").FontSize(12).TextColor(t.TextMuted)
	})
}

func (s *application) presetControl(c *ui.Context) {
	t := c.Theme()
	scheme := s.scheme
	ui.RadioGroup(c.Key("scheme"), func() {
		for _, preset := range s.presets {
			segment := ui.RadioBase(c.Key("preset-"+preset.name), &scheme, preset.name).
				Label(preset.name).FillWidth().Height(40).Padding(8, 12).AlignItems(ui.Center).Radius(8).
				Border(1, ui.Transparent).TextColor(t.TextMuted).
				Disabled(s.busy || s.settingsOpen).
				OnChange(func() { s.setScheme(scheme) })
			if scheme == preset.name {
				segment.Background(t.Surface).Border(1, t.Border).TextColor(t.Text)
			} else if segment.Hovered() {
				segment.Background(t.SurfaceHover)
			}
			segment.Children(func() {
				ui.Text(c, preset.name).FontSize(13).FontWeight(500).
					SingleLine().TruncateMiddle().Tooltip(preset.name)
			})
		}
	}).Label("压缩方案").FillWidth().Padding(4).Gap(4).
		Radius(12).Background(t.Background)
}

func (s *application) settingsView(c *ui.Context) {
	applyTheme(c)
	editor := s.editor
	if editor == nil {
		return
	}
	t := c.Theme()
	c.OnWindowShortcut(0, ui.KeyEscape, func() { s.setSettingsOpen(false) })
	c.OnWindowShortcut(ui.Cmd, ui.KeyS, s.savePresetEdits)
	ui.Column(c).Fill().Padding(20).Gap(16).Background(t.Background).
		Role(ui.RoleDialog).Label("设置").Children(func() {
		ui.Text(c, "设置").FontSize(24).FontWeight(600)
		ui.Column(c).Gap(4).Children(func() {
			ui.Text(c, "压缩方案").FontSize(16).FontWeight(500)
			ui.Text(c, "维护方案名称与参数，保存后可在主界面选择。").
				FontSize(12).TextColor(t.TextMuted)
		})
		ui.Row(c).Grow(1).FillWidth().Gap(20).AlignItems(ui.Stretch).Children(func() {
			ui.Column(c).Width(180).Gap(12).Children(func() {
				ui.Scroll(c.Key("preset-list")).Grow(1).Gap(4).Children(func() {
					for index, preset := range editor.presets {
						button := ui.Button(c.Key(fmt.Sprintf("edit-preset-%d", index)), "").
							Label("编辑方案：" + preset.name).FillWidth().Height(36).
							Tooltip(preset.name).Disabled(s.busy).OnClick(func() {
							editor.selected = index
							editor.message, editor.confirmDelete = "", false
						})
						if editor.selected == index {
							button.Background(t.SurfacePressed)
						}
						button.Children(func() {
							ui.Text(c, preset.name).FontSize(13).SingleLine().TruncateMiddle()
						})
					}
				})
				ui.Button(c.Key("add-preset"), "新增方案").Height(32).
					Disabled(s.busy).OnClick(s.addPreset)
				ui.Button(c.Key("delete-preset"), "删除方案").Height(32).TextColor(t.Danger).
					Disabled(s.busy || len(editor.presets) <= 1).
					OnClick(func() { editor.confirmDelete = true })
			})
			ui.Scroll(c.Key("preset-editor")).Width(0).Grow(1).Gap(12).Padding(0, 8, 0, 0).Children(func() {
				preset := &editor.presets[editor.selected]
				ui.Column(c).Gap(4).Children(func() {
					ui.Text(c, "方案名称").FontSize(13)
					if ui.TextInput(c.Key("preset-name"), &preset.name).
						Label("方案名称").Placeholder("输入方案名称").Height(36).
						Disabled(s.busy).Changed() {
						editor.message = ""
					}
				})
				ui.Text(c, "JPEG").FontSize(14).FontWeight(500)
				s.settingSlider(c, "quality", "JPEG 质量", preset.quality, 1, 100, s.editQuality)
				ui.Text(c, "较低质量通常带来更小体积，也会减少照片细节").
					FontSize(12).TextColor(t.TextMuted)
				ui.Divider(c)
				s.pngSettings(c)
				ui.Text(c, "JPEG 与 PNG 有损压缩可能移除元数据，请保留原图").
					FontSize(12).TextColor(t.TextMuted)
			})
		})
		if editor.confirmDelete {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Textf(c, "删除「%s」？保存后生效。", editor.presets[editor.selected].name).
					FontSize(12).Grow(1).SingleLine().TruncateMiddle()
				ui.Button(c.Key("cancel-delete"), "保留方案").Height(32).
					OnClick(func() { editor.confirmDelete = false })
				ui.Button(c.Key("confirm-delete"), "确认删除").Height(32).
					TextColor(t.Danger).OnClick(s.deletePreset)
			})
		}
		ui.Row(c).Height(40).Gap(12).AlignItems(ui.Center).Children(func() {
			message, color, role := "保存后生效，关闭应用仍会保留", t.TextMuted, ui.RoleStatus
			if editor.message != "" {
				message, color, role = editor.message, t.Danger, ui.RoleAlert
			}
			ui.Text(c, message).FontSize(12).TextColor(color).Role(role).
				Grow(1).MaxLines(2).Tooltip(message)
			ui.Button(c.Key("cancel-settings"), "取消").Width(72).Height(36).
				Disabled(s.busy).OnClick(func() { s.setSettingsOpen(false) })
			ui.PrimaryButton(c.Key("save-settings"), "保存").Width(88).Height(36).
				KeyShortcuts(ui.Cmd, ui.KeyS).Disabled(s.busy || editor.confirmDelete).
				OnClick(s.savePresetEdits)
		})
	})
}

func (s *application) overwriteConfirmation(c *ui.Context) {
	if !s.confirmOverwrite {
		return
	}
	if !s.canOverwrite() {
		s.confirmOverwrite = false
		return
	}
	message := fmt.Sprintf("将用压缩结果替换以下文件：\n%s\n\n%s → %s。覆盖后无法通过本工具恢复原文件。",
		s.file.path, formatSize(s.result.OriginalSize), formatSize(s.result.OutputSize))
	// The last button receives the default focus, so Enter and Escape cancel.
	if ui.AlertDialog(c.Key("overwrite-confirmation"), &s.confirmOverwrite,
		"确认覆盖原图？", message, "覆盖原图", "取消") == 0 {
		s.overwriteOriginal()
	}
}

func (s *application) pngSettings(c *ui.Context) {
	t := c.Theme()
	preset := s.editor.presets[s.editor.selected]
	ui.Text(c, "PNG 有损压缩").FontSize(13).FontWeight(500)
	s.settingSlider(c, "png-quality", "目标质量", float64(preset.png.MaxQuality), 1, 100, func(value float64) {
		options := s.editor.presets[s.editor.selected].png
		options.MaxQuality = int(value)
		options.MinQuality = min(options.MinQuality, options.MaxQuality)
		s.editPNGOptions(options)
	})
	s.settingSlider(c, "png-colors", "最大颜色数", float64(preset.png.Colors), 2, 256, func(value float64) {
		options := s.editor.presets[s.editor.selected].png
		options.Colors = int(value)
		s.editPNGOptions(options)
	})
	ui.Collapsible(c.Key("advanced"), "高级设置", &s.advanced, func() {
		ui.Column(c).Gap(12).Padding(8, 0).Children(func() {
			s.settingSlider(c, "png-min-quality", "最低质量", float64(preset.png.MinQuality), 0, float64(preset.png.MaxQuality), func(value float64) {
				options := s.editor.presets[s.editor.selected].png
				options.MinQuality = int(value)
				s.editPNGOptions(options)
			})
			ui.Text(c, "未达最低质量时跳过量化。质量值是工具评分，并非画质保留百分比。").
				FontSize(12).TextColor(t.TextMuted)
			s.settingSlider(c, "png-speed", "处理速度", float64(preset.png.Speed), 1, 11, func(value float64) {
				options := s.editor.presets[s.editor.selected].png
				options.Speed = int(value)
				s.editPNGOptions(options)
			})
			ui.Text(c, "1 最慢、11 最快。更快可能降低压缩效果。").
				FontSize(12).TextColor(t.TextMuted)
		})
	})
	ui.Text(c, "质量与颜色数越低，文件通常越小。支持透明度，但渐变、阴影与半透明边缘可能变化。").
		FontSize(12).TextColor(t.TextMuted)
}

func (s *application) settingSlider(c *ui.Context, key, label string, value, low, high float64, change func(float64)) {
	ui.Column(c).Gap(4).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Text(c, label).FontSize(13).Grow(1)
			ui.Textf(c, "%.0f", value).FontSize(13).FontWeight(500).FontFeatures("tnum")
		})
		if ui.StepSlider(c.Key(key), &value, low, high, 1).
			Label(label).Disabled(s.busy).Changed() {
			change(math.Round(value))
		}
	})
}

func (s *application) resultSummary(c *ui.Context) {
	t := c.Theme()
	original, output := "—", "—"
	label, color := "等待压缩", t.TextMuted
	if s.file != nil {
		original = formatSize(s.file.size)
	}
	if s.result != nil {
		original = formatSize(s.result.OriginalSize)
		output = formatSize(s.result.OutputSize)
		saved := float64(s.result.OriginalSize-s.result.OutputSize) / float64(s.result.OriginalSize) * 100
		label, color = fmt.Sprintf("减少 %.1f%%", saved), t.Success
		if s.result.Unchanged {
			label, color = "未减小（0.0%）", t.TextMuted
		}
	}
	ui.Column(c).Padding(12).Gap(8).Radius(12).Background(t.Background).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, "压缩结果").FontSize(12).TextColor(t.TextMuted).Grow(1)
			ui.Text(c, label).FontSize(12).FontWeight(500).FontFeatures("tnum").TextColor(color)
		})
		ui.Row(c).Gap(16).AlignItems(ui.End).Children(func() {
			for index, row := range []struct{ label, value string }{
				{"原始大小", original}, {"输出大小", output},
			} {
				ui.Column(c).Grow(1).Gap(4).Children(func() {
					ui.Text(c, row.label).FontSize(12).TextColor(t.TextMuted)
					size := float32(18)
					if index == 1 {
						size = 24
					}
					ui.Text(c, row.value).FontSize(size).FontWeight(500).FontFeatures("tnum").
						SingleLine().Height(32)
				})
			}
		})
	})
}
