package main

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/egoist/mygo/ui"
)

func primaryAction(c *ui.Context, key, label string, disabled bool) ui.Element {
	button := ui.PrimaryButton(c.Key(key), label).Radius(8).Disabled(disabled)
	if disabled {
		button.Background(c.Theme().SurfacePressed).TextColor(c.Theme().TextMuted).DimDisabled(false)
	}
	return button
}

func brandLockup(c *ui.Context, showSubtitle bool) {
	t := c.Theme()
	ui.Row(c).Gap(8).AlignItems(ui.Center).Grow(1).MinWidth(0).Children(func() {
		ui.Box(c).Size(24, 24).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
			frame := ui.Rect{X: r.X + 2, Y: r.Y + 2, W: 20, H: 20}
			p.Fill(frame, selectionBackground(c), 4)
			p.Stroke(frame, selectionBorder(c), 4, 1)
			p.StrokePolyline([][2]float32{
				{r.X + 6, r.Y + 10}, {r.X + 6, r.Y + 6}, {r.X + 10, r.Y + 6},
			}, 1.5, t.Accent)
			p.StrokePolyline([][2]float32{
				{r.X + 14, r.Y + 18}, {r.X + 18, r.Y + 18}, {r.X + 18, r.Y + 14},
			}, 1.5, t.Accent)
		})
		ui.Text(c, "Z Pic").FontSize(18).FontWeight(600).SingleLine()
		if showSubtitle {
			ui.Box(c).Size(1, 12).Background(t.Border)
			ui.Text(c, "图片压缩，尺寸不变").FontSize(12).TextColor(metadataColor(c)).SingleLine()
		}
	})
}

func (s *application) view(c *ui.Context) {
	s.applyTheme(c)
	s.prepareBatchThumbnails()
	theme := c.Theme()
	_, pending, done, _, _, _, canRun := s.batchStats()
	showBatchOutput := s.batch != nil && canRun && pending == 0 && done > 0 &&
		s.batch.outputMode == batchOutputFolder && s.batch.outputDir != ""
	width, _ := c.Size()
	mobile := width < 720
	pagePadding, panelGap := float32(24), float32(20)
	if mobile {
		pagePadding, panelGap = 12, 12
	} else if width < 960 {
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
	ui.Column(c).Fill().Padding(pagePadding).Gap(panelGap).Background(theme.Background).Children(func() {
		s.topToolbar(c, width, mobile, pending, done, canRun)
		if mobile {
			ui.Scroll(c.Key("mobile-workspace")).Grow(1).MinHeight(0).FillWidth().Gap(panelGap).Children(func() {
				s.previewPanel(c)
				s.controlsPanel(c, showBatchOutput)
			})
		} else {
			ui.Row(c).Grow(1).FillWidth().Gap(panelGap).AlignItems(ui.Stretch).Children(func() {
				s.previewPanel(c)
				s.controlsPanel(c, showBatchOutput)
			})
		}
		ui.Row(c).Height(20).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "本机压缩 · 网页图片仅在下载时联网").FontSize(12).
				TextColor(metadataColor(c)).Width(0).Grow(1).SingleLine().
				Tooltip("本地图片在本机处理；仅下载网页图片时需要联网")
			if width >= 680 {
				shortcutHint(c, "打开", "O")
				shortcutHint(c, "压缩", "↵")
				if s.batch == nil {
					shortcutHint(c, "另存", "S")
				} else if !s.batch.previewOpen && s.batch.outputMode == batchOutputFolder {
					shortcutHint(c, "输出位置", "S")
				}
				shortcutHint(c, "设置", ",")
			}
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

func (s *application) topToolbar(c *ui.Context, width float32, mobile bool, pending, done int, canRun bool) {
	t := c.Theme()
	utilityControls := func() {
		floatingLabel := "悬浮窗"
		if s.floatingOpen {
			floatingLabel = "收起悬浮窗"
		}
		floatingBtn := quietToolbarButton(c, "toggle-floating", floatingLabel).
			Tooltip("打开置顶拖图窗口，接收本地或网页图片").
			OnClick(func() { s.setFloatingOpen(!s.floatingOpen) })
		if s.floatingOpen {
			floatingBtn.Background(selectionBackground(c)).Border(1, selectionBorder(c)).TextColor(t.Text)
		}
		s.themeControl(c)
		settingsDisabled := s.busy || s.confirmOverwrite || s.confirmBatchOverwrite
		quietToolbarButton(c, "open-settings", "设置").
			KeyShortcuts(ui.Cmd, ui.KeyComma).
			Tooltip("打开设置（" + shortcutLabel(",") + "）").
			Disabled(settingsDisabled).
			OnClick(func() { s.setSettingsOpen(true) })
	}
	if mobile {
		ui.Column(c).FillWidth().Gap(8).Role(ui.RoleToolbar).Label("操作栏").Children(func() {
			ui.Row(c).Height(36).Gap(8).AlignItems(ui.Center).Children(func() {
				brandLockup(c, false)
				s.compressionControls(c, pending, done, canRun)
			})
			ui.Row(c).Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
				s.presetControl(c)
				ui.Box(c).Grow(1)
				utilityControls()
			})
		})
		return
	}
	ui.Row(c).Height(40).Gap(8).AlignItems(ui.Center).Role(ui.RoleToolbar).Label("操作栏").Children(func() {
		brandLockup(c, width >= 920)
		s.presetControl(c)
		s.compressionControls(c, pending, done, canRun)
		ui.Box(c).Size(1, 16).Background(t.Border)
		utilityControls()
	})
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
	width, _ := c.Size()
	minW := float32(340)
	if width < 720 {
		minW = 0
	}
	panel := ui.Column(c).Grow(1).MinWidth(minW).Padding(16).Gap(12).
		Radius(12).Background(t.Surface).Border(1, t.Border)
	if width < 720 {
		panel.FillWidth()
	}
	panel.Children(func() {
		ui.Row(c).Height(32).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "图片预览").FontSize(16).FontWeight(500).Width(0).Grow(1).SingleLine()
			if s.result != nil && !s.overwritten {
				ui.Segmented(c.Key("preview-mode"), &s.comparison.mode, "对比", "原图", "结果").
					Label("预览模式").FillEqually().Width(168).Height(32).
					Disabled(s.previewLocked())
			}
			if s.file != nil {
				quietButton(c, "replace", "更换图片", 32, s.busy).
					KeyShortcuts(ui.Cmd, ui.KeyO).Tooltip("更换图片 · " + shortcutLabel("O")).
					OnClick(s.chooseFile)
			}
		})
		if s.preview != nil {
			s.previewToolbar(c)
		}
		ui.Box(c).Grow(1).FillWidth().MinHeight(220).Radius(8).
			Background(stageBackground(c)).Border(1, t.Border).Clip().Children(func() {
			if s.preview == nil {
				ui.Column(c).Fill().Center().Gap(12).Padding(20).Children(func() {
					ui.Box(c).Size(56, 56).Draw(func(p *ui.Painter, bounds ui.Rect) {
						card := ui.Rect{X: bounds.X + 6, Y: bounds.Y + 8, W: 44, H: 40}
						p.Fill(card, t.Surface, 8)
						p.Stroke(card, t.Border, 8, 1)
						p.StrokePolyline([][2]float32{
							{bounds.X + 12, bounds.Y + 20}, {bounds.X + 12, bounds.Y + 14}, {bounds.X + 18, bounds.Y + 14},
						}, 1.5, t.Accent)
						p.StrokePolyline([][2]float32{
							{bounds.X + 38, bounds.Y + 42}, {bounds.X + 44, bounds.Y + 42}, {bounds.X + 44, bounds.Y + 36},
						}, 1.5, t.Accent)
						p.Stroke(ui.Rect{X: bounds.X + 33, Y: bounds.Y + 17, W: 5, H: 5}, t.TextMuted, 2.5, 1.5)
						p.StrokePolyline([][2]float32{
							{bounds.X + 14, bounds.Y + 38},
							{bounds.X + 24, bounds.Y + 27},
							{bounds.X + 31, bounds.Y + 34},
							{bounds.X + 36, bounds.Y + 30},
							{bounds.X + 42, bounds.Y + 38},
						}, 1.5, t.TextMuted)
					})
					ui.Text(c, "选择或拖入图片").FontSize(18).FontWeight(500)
					ui.Text(c, "支持单张、多张图片或文件夹").FontSize(13).TextColor(t.TextMuted)
					ui.Row(c).Gap(8).Margin(4, 0).Children(func() {
						primaryAction(c, "select", "选择图片", s.busy).Height(36).Padding(0, 16).
							KeyShortcuts(ui.Cmd, ui.KeyO).Tooltip("选择单张或多张图片 · " + shortcutLabel("O")).
							OnClick(s.chooseFile)
						quietButton(c, "select-folder", "选择文件夹", 36, s.busy).Padding(0, 16).
							Tooltip("选择包含图片的文件夹").
							OnClick(s.chooseFolder)
					})
					ui.Column(c).Gap(4).AlignItems(ui.Center).Children(func() {
						ui.Text(c, "JPEG · PNG · WebP").FontSize(12).FontFeatures("tnum").TextColor(metadataColor(c))
						ui.Text(c, "单张最大 64 MiB · 3200 万像素").FontSize(12).FontFeatures("tnum").TextColor(metadataColor(c))
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
			quietButton(c, "batch-restart", "重新压缩", 32, disabled).
				OnClick(s.restartBatchCompression)
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
	mobile := width < 720
	panelWidth := float32(320)
	if width < 960 {
		panelWidth = 280
	}
	panel := ui.Column(c).Padding(16).Gap(12).Radius(12).
		Background(t.Surface).Border(1, t.Border)
	if mobile {
		panel.FillWidth()
	} else {
		panel.Width(panelWidth).Shrink(0)
	}
	panel.Children(func() {
		s.presetOverview(c)
		ui.Box(c).FillWidth().Height(1).Background(t.Border)
		if s.batch != nil {
			s.batchControls(c, showBatchOutput, mobile)
			return
		}
		summaryBody := func() {
			s.resultSummary(c)
			s.singleOutputSummary(c)
			if s.file != nil && s.file.format == "png" {
				ui.Text(c, "PNG 有损压缩可能改变渐变与半透明边缘").
					FontSize(12).TextColor(t.TextMuted).MaxLines(2)
			}
		}
		if mobile {
			ui.Column(c).FillWidth().Gap(12).Children(summaryBody)
		} else {
			ui.Scroll(c.Key("single-compression-summary")).Grow(1).MinHeight(0).FillWidth().Gap(12).Children(summaryBody)
		}
		if s.busy {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c).Size(16, 16)
				ui.Text(c, s.phase).FontSize(12).Role(ui.RoleStatus).Grow(1).MaxLines(2)
			})
			if s.cancel != nil {
				quietButton(c, "cancel", "取消操作", 32, s.canceling).
					OnClick(s.cancelWork)
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
					quietButton(c, "reveal-saved", "定位文件", 32, s.busy).
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
			quietButton(c, "save-copy", "另存副本", 32, s.busy).FillWidth().
				OnClick(s.saveCopy)
		}
		ui.Text(c, "不缩放尺寸 · 请保留原图").FontSize(12).TextColor(metadataColor(c))
	})
}

func (s *application) presetOverview(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Gap(8).Children(func() {
		ui.Row(c).Height(24).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "压缩概览").FontSize(16).FontWeight(500).Grow(1)
			badge := "等待图片"
			if s.batch != nil {
				badge = fmt.Sprintf("%d 张图片", len(s.batch.items))
			} else if s.file != nil {
				badge = "单张图片"
			}
			ui.Text(c, badge).FontSize(12).FontFeatures("tnum").TextColor(metadataColor(c)).
				Padding(2, 8).Radius(4).Background(stageBackground(c)).Border(1, t.Border)
		})
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "当前预设").FontSize(12).TextColor(metadataColor(c)).Width(64).Shrink(0)
			ui.Text(c, s.scheme).FontSize(13).FontWeight(500).Width(0).Grow(1).
				SingleLine().TruncateMiddle().Tooltip(s.scheme)
		})
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "图片质量").FontSize(12).TextColor(metadataColor(c)).Width(64).Shrink(0)
			ui.Text(c, presetQualityLabel(compressionPreset{quality: s.quality, png: s.png})).
				FontSize(13).FontFeatures("tnum").Width(0).Grow(1).SingleLine()
		})
	})
}

func (s *application) singleOutputSummary(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Padding(12).Gap(8).Radius(8).Background(subtleSurface(c)).Border(1, t.Border).Children(func() {
		ui.Text(c, "保存方式").FontSize(12).FontWeight(500).TextColor(metadataColor(c))
		description, color := presetSaveDescription(s.output), t.Text
		if s.output.replacesOriginal() {
			color = t.Danger
		}
		ui.Text(c, description).FontSize(13).FontWeight(500).TextColor(color).MaxLines(2).Tooltip(description)
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
	t := c.Theme()
	width, _ := c.Size()
	pickerWidth := float32(168)
	if width < 720 {
		pickerWidth = 156
	} else if width < 960 {
		pickerWidth = 128
	}
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		if width >= 860 || width < 720 {
			ui.Text(c, "预设").FontSize(12).TextColor(metadataColor(c))
		}
		ui.PopoverMenuButton(c.Key("preset-picker"), s.scheme, func(m *ui.Menu) {
			m.MaxWidth(320)
			for _, preset := range s.presets {
				item := m.Item(preset.name).Radio(s.scheme == preset.name).
					Tooltip(preset.name).Disabled(s.modalOpen())
				if preset.favorite {
					presetName := preset.name
					item.Content(func(x ui.MenuItemContent) {
						iconColor := t.Accent
						if x.Highlighted {
							iconColor = x.Text
						}
						ui.Row(x.Context).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
							ui.Text(x.Context, presetName).SingleLine().TruncateMiddle().Width(0).Grow(1)
							ui.Icon(x.Context, presetHeartFilledIcon).Size(12, 12).TextColor(iconColor)
						})
					})
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
		}).Label("选择预设").Tooltip(s.scheme).Width(pickerWidth).Height(32).Radius(8).
			Background(t.Surface).Border(1, t.Border).Disabled(s.modalOpen())
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
	ratio := -1.0
	if s.file != nil {
		original = formatSize(s.file.size)
	}
	if s.result != nil {
		original = formatSize(s.result.OriginalSize)
		output = formatSize(s.result.OutputSize)
		saved := float64(s.result.OriginalSize-s.result.OutputSize) / float64(s.result.OriginalSize) * 100
		label, color = fmt.Sprintf("减少 %.1f%%", saved), t.Success
		if s.result.OriginalSize > 0 {
			ratio = min(1, max(0, float64(s.result.OutputSize)/float64(s.result.OriginalSize)))
		}
		if s.result.Unchanged {
			label, color = "未减小（0.0%）", t.TextMuted
			ratio = 1
		}
	}
	summaryCardWithNote(c, "压缩结果", label, color, "原始大小", original, "输出大小", output, "", ratio)
}

func summaryCard(c *ui.Context, title, status string, statusColor ui.Color, firstLabel, firstValue, secondLabel, secondValue string) {
	summaryCardWithNote(c, title, status, statusColor, firstLabel, firstValue, secondLabel, secondValue, "", -1)
}

func summaryCardWithNote(c *ui.Context, title, status string, statusColor ui.Color, firstLabel, firstValue, secondLabel, secondValue, note string, ratio float64) {
	t := c.Theme()
	width, _ := c.Size()
	compact := width >= 720 && width < 960
	ui.Column(c).Padding(12).Gap(8).Radius(8).Background(subtleSurface(c)).Border(1, t.Border).Children(func() {
		ui.Text(c, title).FontSize(12).FontWeight(500).TextColor(metadataColor(c))
		ui.Row(c).Gap(12).AlignItems(ui.End).Children(func() {
			for index, row := range []struct{ label, value string }{
				{firstLabel, firstValue}, {secondLabel, secondValue},
			} {
				if index == 1 {
					ui.Text(c, "→").Width(16).Height(28).FontSize(15).TextAlign(ui.Center).TextColor(metadataColor(c))
				}
				grow := float32(1)
				if index == 1 {
					grow = 1.2
				}
				ui.Column(c).Width(0).Grow(grow).Gap(4).Children(func() {
					ui.Text(c, row.label).FontSize(12).TextColor(metadataColor(c))
					size := float32(16)
					weight := 500
					valColor := t.TextMuted
					if compact {
						size = 15
					}
					if index == 1 {
						size = 22
						weight = 600
						valColor = t.Text
						if compact {
							size = 19
						}
					}
					ui.Text(c, row.value).FontSize(size).FontWeight(weight).FontFeatures("tnum").
						TextColor(valColor).SingleLine().Height(28).Tooltip(row.value)
				})
			}
		})
		if ratio >= 0 {
			barColor := t.Success
			if ratio >= 1 {
				barColor = t.TextMuted.Alpha(0.45)
			}
			ui.Box(c).FillWidth().Height(4).Draw(func(p *ui.Painter, r ui.Rect) {
				p.Fill(r, t.SurfacePressed, 2)
				fillW := max(4, r.W*float32(min(1, ratio)))
				p.Fill(ui.Rect{X: r.X, Y: r.Y, W: fillW, H: r.H}, barColor, 2)
			})
		}
		ui.Text(c, status).FontSize(12).FontWeight(500).FontFeatures("tnum").
			TextColor(statusColor).MaxLines(2).Tooltip(status)
		if note != "" {
			ui.Text(c, note).FontSize(12).FontFeatures("tnum").TextColor(metadataColor(c)).
				MaxLines(2).Tooltip(note)
		}
	})
}
