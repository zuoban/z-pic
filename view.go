package main

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/egoist/mygo/ui"
)

func metadataColor(c *ui.Context) ui.Color {
	if c.Theme().Dark {
		return ui.Hex("#92929c")
	}
	return ui.Hex("#707078")
}

func selectionBackground(c *ui.Context) ui.Color {
	t := c.Theme()
	if t.Dark {
		return t.Surface.Mix(t.Accent, 0.12)
	}
	return t.Surface.Mix(t.Accent, 0.06)
}

func primaryAction(c *ui.Context, key, label string, disabled bool) ui.Element {
	button := ui.PrimaryButton(c.Key(key), label).Disabled(disabled)
	if disabled {
		button.Background(c.Theme().SurfacePressed).TextColor(c.Theme().TextMuted).DimDisabled(false)
	}
	return button
}

func (s *application) view(c *ui.Context) {
	s.applyTheme(c)
	s.prepareBatchThumbnails()
	theme := c.Theme()
	_, pending, done, _, _, _, canRun := s.batchStats()
	showBatchOutput := s.batch != nil && canRun && pending == 0 && done > 0 &&
		s.batch.outputMode == batchOutputFolder && s.batch.outputDir != ""
	width, _ := c.Size()
	pagePadding, panelGap := float32(24), float32(20)
	if width < 960 {
		pagePadding, panelGap = 16, 16
	}
	if !s.modalOpen() {
		c.OnWindowShortcut(ui.Cmd, ui.KeyO, s.chooseFile)
		c.OnWindowShortcut(ui.Cmd, ui.KeyEnter, s.startActiveCompression)
		c.OnWindowShortcut(ui.Cmd, ui.KeyS, s.saveActiveAction)
		c.OnWindowShortcut(ui.Cmd, ui.KeyComma, func() { s.setSettingsOpen(true) })
		if s.batch != nil {
			if s.batch.previewOpen {
				c.OnWindowShortcut(0, ui.KeyEscape, s.closeBatchPreview)
				c.OnWindowShortcut(ui.Cmd, ui.KeyBracketLeft, func() { s.stepBatchPreview(-1) })
				c.OnWindowShortcut(ui.Cmd, ui.KeyBracketRight, func() { s.stepBatchPreview(1) })
			} else {
				c.OnWindowShortcut(ui.Cmd, ui.KeyF, func() { s.batchSearchRef.Focus(c) })
			}
		}
		if (s.batch == nil || s.batch.previewOpen) && s.preview != nil && !s.previewLocked() {
			c.OnWindowShortcut(ui.Cmd, ui.KeyMinus, func() { s.zoomPreviewBy(0.8) })
			c.OnWindowShortcut(ui.Cmd, ui.KeyEqual, func() { s.zoomPreviewBy(1.25) })
			c.OnWindowShortcut(ui.Cmd|ui.Shift, ui.KeyEqual, func() { s.zoomPreviewBy(1.25) })
			c.OnWindowShortcut(ui.Cmd, ui.Key0, s.resetPreviewFit)
			c.OnWindowShortcut(ui.Cmd, ui.Key1, s.zoomPreviewActual)
		}
	}
	ui.Column(c).Fill().Padding(pagePadding).Gap(16).Background(theme.Background).Children(func() {
		ui.Row(c).Height(48).Gap(8).AlignItems(ui.Center).Role(ui.RoleToolbar).Label("操作栏").Children(func() {
			ui.Column(c).Gap(4).Grow(1).Children(func() {
				ui.Text(c, "Z Pic").FontSize(24).FontWeight(600)
				ui.Text(c, "图片压缩，尺寸不变").FontSize(12).TextColor(metadataColor(c))
			})
			s.presetControl(c)
			s.compressionControls(c, pending, done, canRun)
			floatingLabel := "悬浮窗"
			if s.floatingOpen {
				floatingLabel = "收起悬浮窗"
			}
			ui.Button(c.Key("toggle-floating"), floatingLabel).Height(32).
				Tooltip("打开置顶拖图窗口，接收本地或网页图片").
				OnClick(func() { s.setFloatingOpen(!s.floatingOpen) })
			s.themeControl(c)
			ui.Button(c.Key("open-settings"), "设置").Height(32).
				KeyShortcuts(ui.Cmd, ui.KeyComma).
				Tooltip("打开设置（" + shortcutLabel(",") + "）").
				Disabled(s.busy || s.confirmOverwrite || s.confirmBatchOverwrite).
				OnClick(func() { s.setSettingsOpen(true) })
		})
		ui.Row(c).Grow(1).FillWidth().Gap(panelGap).AlignItems(ui.Stretch).Children(func() {
			s.previewPanel(c)
			s.controlsPanel(c, showBatchOutput)
		})
		ui.Row(c).Height(20).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "本机压缩 · 网页图片仅在下载时联网").FontSize(12).
				TextColor(metadataColor(c)).Width(0).Grow(1).SingleLine().
				Tooltip("本地图片在本机处理；仅下载网页图片时需要联网")
			shortcutHint(c, "打开", "O")
			shortcutHint(c, "压缩", "↵")
			if s.batch == nil {
				shortcutHint(c, "另存", "S")
			} else if !s.batch.previewOpen && s.batch.outputMode == batchOutputFolder {
				shortcutHint(c, "输出位置", "S")
			}
			shortcutHint(c, "设置", ",")
		})
	})
	s.overwriteConfirmation(c)
	s.batchOverwriteConfirmation(c)
	if s.window != nil {
		s.startNextBatchThumbnail()
	}
	s.startNextDropImport()
	s.invalidateFloatingWindow()
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
		ui.Text(c, label).FontSize(12).TextColor(metadataColor(c))
		ui.Text(c, shortcutLabel(key)).FontSize(12).FontFeatures("tnum").TextColor(t.TextMuted).
			Padding(0, 4).Radius(4).Background(t.Surface).Border(1, t.Border)
	})
}

func (s *application) previewPanel(c *ui.Context) {
	if s.batch != nil {
		if s.batch.previewOpen {
			s.batchPreviewPanel(c)
			return
		}
		s.batchListPanel(c)
		return
	}
	t := c.Theme()
	ui.Column(c).Grow(1).MinWidth(340).Padding(16).Gap(12).
		Radius(16).Background(t.Surface).Border(1, t.Border).Children(func() {
		ui.Row(c).Height(32).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "图片预览").FontSize(16).FontWeight(500).Width(0).Grow(1).SingleLine()
			if s.result != nil && !s.overwritten {
				ui.Segmented(c.Key("preview-mode"), &s.comparison.mode, "对比", "原图", "结果").
					Label("预览模式").FillEqually().Width(168).Height(32).
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
					ui.Text(c, "选择或拖入图片").FontSize(20).FontWeight(500)
					ui.Text(c, "支持单张、多张图片或文件夹").FontSize(13).TextColor(t.TextMuted)
					ui.Row(c).Gap(8).Margin(4, 0).Children(func() {
						primaryAction(c, "select", "选择图片", s.busy).Height(40).
							KeyShortcuts(ui.Cmd, ui.KeyO).Tooltip("选择单张或多张图片 · " + shortcutLabel("O")).
							OnClick(s.chooseFile)
						ui.Button(c.Key("select-folder"), "选择文件夹").Height(40).
							Tooltip("选择包含图片的文件夹").
							Disabled(s.busy).OnClick(s.chooseFolder)
					})
					ui.Column(c).Gap(4).AlignItems(ui.Center).Children(func() {
						ui.Text(c, "JPEG / PNG / WebP").FontSize(12).TextColor(metadataColor(c))
						ui.Text(c, "单张最大 64 MiB · 3200 万像素").FontSize(12).TextColor(metadataColor(c))
					})
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
					FontSize(12).FontFeatures("tnum").TextColor(metadataColor(c))
			})
		} else {
			ui.Column(c).Gap(4).Children(func() {
				ui.Text(c, "压缩后可对比画质").FontSize(13).FontWeight(500)
				ui.Text(c, "另存副本、输出到文件夹，或确认后覆盖原图").FontSize(12).TextColor(metadataColor(c))
			})
		}
	})
}

func (s *application) compressionControls(c *ui.Context, pending, done int, canRun bool) {
	disabled := s.modalOpen()
	if s.batch == nil {
		label := "开始压缩"
		if s.result != nil {
			label = "重新压缩"
		}
		primaryAction(c, "compress", label, disabled || s.file == nil).Height(32).
			KeyShortcuts(ui.Cmd, ui.KeyEnter).Tooltip("压缩当前图片 · " + shortcutLabel("↵")).
			OnClick(s.startCompression)
		return
	}
	disabled = disabled || !canRun || s.batch.previewOpen
	switch {
	case (pending > 0 && done == 0) || !canRun:
		primaryAction(c, "batch-start", "开始压缩", disabled).Height(32).
			KeyShortcuts(ui.Cmd, ui.KeyEnter).Tooltip("开始批量压缩 · " + shortcutLabel("↵")).
			OnClick(s.startBatchCompression)
	case pending > 0 && done > 0:
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Button(c.Key("batch-restart"), "重新压缩").Height(32).
				Disabled(disabled).OnClick(s.restartBatchCompression)
			primaryAction(c, "batch-continue", "继续压缩", disabled).Height(32).
				KeyShortcuts(ui.Cmd, ui.KeyEnter).Tooltip("继续压缩剩余图片 · " + shortcutLabel("↵")).
				OnClick(s.startBatchCompression)
		})
	default:
		primaryAction(c, "batch-restart", "重新压缩", disabled).Height(32).
			KeyShortcuts(ui.Cmd, ui.KeyEnter).Tooltip("重新压缩全部图片 · " + shortcutLabel("↵")).
			OnClick(s.restartBatchCompression)
	}
}

func (s *application) controlsPanel(c *ui.Context, showBatchOutput bool) {
	t := c.Theme()
	width, _ := c.Size()
	panelWidth := float32(320)
	if width < 960 {
		panelWidth = 280
	}
	ui.Column(c).Width(panelWidth).Shrink(0).Padding(16).Gap(12).Radius(16).
		Background(t.Surface).Border(1, t.Border).Children(func() {
		s.presetOverview(c)
		ui.Box(c).FillWidth().Height(1).Background(t.Border)
		if s.batch != nil {
			s.batchControls(c, showBatchOutput)
			return
		}
		ui.Scroll(c.Key("single-compression-summary")).Grow(1).MinHeight(0).FillWidth().Gap(12).Children(func() {
			s.resultSummary(c)
			s.singleOutputSummary(c)
			if s.file != nil && s.file.format == "png" {
				ui.Text(c, "PNG 有损压缩可能改变渐变与半透明边缘").
					FontSize(12).TextColor(t.TextMuted).MaxLines(2)
			}
		})
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
			if !s.failed && s.lastSavedPath != "" {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, s.message).FontSize(12).TextColor(color).Role(role).
						Width(0).Grow(1).MaxLines(2).Tooltip(s.message)
					ui.Button(c.Key("reveal-saved"), "定位文件").Height(32).
						Disabled(s.busy).
						Tooltip("在文件夹中显示输出文件").
						OnClick(func() { s.revealFileInFolder(s.lastSavedPath) })
				})
			} else {
				ui.Text(c, s.message).FontSize(12).TextColor(color).Role(role).
					MaxLines(2).Tooltip(s.message)
			}
		}
		if s.result != nil {
			primaryAction(c, "save", "保存结果", s.busy || (s.output.replacesOriginal() && !s.canOverwrite())).FillWidth().Height(36).
				KeyShortcuts(ui.Cmd, ui.KeyS).Tooltip("按当前预设保存 · " + shortcutLabel("S")).
				OnClick(s.savePresetResult)
			ui.Button(c.Key("save-copy"), "另存副本").Height(32).
				Disabled(s.busy).OnClick(s.saveCopy)
		}
		ui.Text(c, "不缩放尺寸 · 请保留原图").FontSize(12).TextColor(metadataColor(c))
	})
}

func (s *application) presetOverview(c *ui.Context) {
	ui.Column(c).Gap(8).Children(func() {
		ui.Row(c).Height(24).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "压缩概览").FontSize(16).FontWeight(600).Grow(1)
			if s.batch != nil {
				ui.Textf(c, "%d 张图片", len(s.batch.items)).FontSize(12).TextColor(metadataColor(c))
			} else if s.file != nil {
				ui.Text(c, "单张图片").FontSize(12).TextColor(metadataColor(c))
			} else {
				ui.Text(c, "等待图片").FontSize(12).TextColor(metadataColor(c))
			}
		})
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "当前预设").FontSize(12).TextColor(metadataColor(c)).Width(64).Shrink(0)
			ui.Text(c, s.scheme).FontSize(14).FontWeight(500).Width(0).Grow(1).
				SingleLine().TruncateMiddle().Tooltip(s.scheme)
		})
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "图片质量").FontSize(12).TextColor(metadataColor(c)).Width(64).Shrink(0)
			ui.Text(c, presetQualityLabel(compressionPreset{quality: s.quality, png: s.png})).
				FontSize(13).Width(0).Grow(1).SingleLine()
		})
	})
}

func (s *application) singleOutputSummary(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Padding(12).Gap(8).Radius(8).Background(t.Background).Children(func() {
		ui.Text(c, "保存方式").FontSize(13).FontWeight(500)
		description, color := presetSaveDescription(s.output), t.Text
		if s.output.replacesOriginal() {
			color = t.Danger
		}
		ui.Text(c, description).FontSize(13).TextColor(color).MaxLines(2).Tooltip(description)
		if s.output.supportsSubfolder() && s.output.UseSubfolder {
			name := s.output.Subfolder
			if name == "" {
				name = defaultSubfolderName
			}
			ui.Row(c).Gap(8).Children(func() {
				ui.Text(c, "子文件夹").FontSize(12).TextColor(metadataColor(c)).Grow(1)
				ui.Text(c, name).FontSize(12).Width(0).Grow(1).SingleLine().TruncateMiddle().Tooltip(name)
			})
		}
		if s.output.UseSuffix {
			suffix := s.output.Suffix
			if suffix == "" {
				suffix = defaultFilenameSuffix
			}
			ui.Row(c).Gap(8).Children(func() {
				ui.Text(c, "文件名后缀").FontSize(12).TextColor(metadataColor(c)).Grow(1)
				ui.Text(c, suffix).FontSize(12).Width(0).Grow(1).SingleLine().TruncateMiddle().Tooltip(suffix)
			})
		}
	})
}

func (s *application) presetControl(c *ui.Context) {
	width, _ := c.Size()
	pickerWidth := float32(176)
	if width < 960 {
		pickerWidth = 136
	}
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "预设").FontSize(13).TextColor(metadataColor(c))
		ui.PopoverMenuButton(c.Key("preset-picker"), s.scheme, func(m *ui.Menu) {
			m.MaxWidth(320)
			for _, preset := range s.presets {
				item := m.Item(preset.name).Radio(s.scheme == preset.name).
					Tooltip(preset.name).Disabled(s.modalOpen())
				if preset.favorite {
					item.Trailing("♥")
				}
				if item.Chosen() {
					s.setScheme(preset.name)
				}
			}
			m.Separator()
			if m.Item("编辑预设…").Disabled(s.modalOpen()).Chosen() {
				s.setSettingsOpen(true)
			}
		}, ui.PopoverMenuButtonOptions{
			At: ui.AnchorBottomEnd, Self: ui.AnchorTopEnd,
		}).Label("选择预设").Tooltip(s.scheme).Width(pickerWidth).Height(32).Disabled(s.modalOpen())
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
	summaryCard(c, "压缩结果", label, color, "原始大小", original, "输出大小", output)
}

func summaryCard(c *ui.Context, title, status string, statusColor ui.Color, firstLabel, firstValue, secondLabel, secondValue string) {
	summaryCardWithNote(c, title, status, statusColor, firstLabel, firstValue, secondLabel, secondValue, "")
}

func summaryCardWithNote(c *ui.Context, title, status string, statusColor ui.Color, firstLabel, firstValue, secondLabel, secondValue, note string) {
	t := c.Theme()
	width, _ := c.Size()
	compact := width < 960
	ui.Column(c).Padding(12).Gap(8).Radius(8).Background(t.Background).Children(func() {
		ui.Text(c, title).FontSize(13).FontWeight(500)
		ui.Row(c).Gap(12).AlignItems(ui.End).Children(func() {
			for index, row := range []struct{ label, value string }{
				{firstLabel, firstValue}, {secondLabel, secondValue},
			} {
				if index == 1 {
					ui.Text(c, "→").Width(12).Height(32).FontSize(16).TextColor(metadataColor(c))
				}
				grow := float32(1)
				if index == 1 {
					grow = 1.2
				}
				ui.Column(c).Width(0).Grow(grow).Gap(4).Children(func() {
					ui.Text(c, row.label).FontSize(12).TextColor(metadataColor(c))
					size := float32(18)
					if compact {
						size = 16
					}
					if index == 1 {
						size = 24
						if compact {
							size = 20
						}
					}
					ui.Text(c, row.value).FontSize(size).FontWeight(500).FontFeatures("tnum").
						SingleLine().Height(32).Tooltip(row.value)
				})
			}
		})
		ui.Text(c, status).FontSize(12).FontWeight(500).FontFeatures("tnum").
			TextColor(statusColor).MaxLines(2).Tooltip(status)
		if note != "" {
			ui.Text(c, note).FontSize(12).FontFeatures("tnum").TextColor(metadataColor(c)).
				MaxLines(2).Tooltip(note)
		}
	})
}
