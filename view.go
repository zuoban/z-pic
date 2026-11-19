package main

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/egoist/mygo/ui"
)

const mainTitleBarHeight = 52

var (
	toolbarCompressIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="M2 2l4 4M2 6h4V2M14 14l-4-4m0 4v-4h4"/></svg>`))
	toolbarRestartIcon  = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="M13 6a5.25 5.25 0 1 0 .15 3.5M13 2.5V6H9.5"/></svg>`))
	toolbarContinueIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="M5 3l7 5-7 5Z"/></svg>`))
	toolbarFloatingIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round"><rect x="2" y="3" width="12" height="10" rx="2"/><rect x="8" y="7" width="4" height="4" rx=".5"/></svg>`))
	toolbarSettingsIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="M6.5 1.75h3l.4 1.75 1.2.7 1.7-.5 1.5 2.6L13 7.5v1l1.3 1.2-1.5 2.6-1.7-.5-1.2.7-.4 1.75h-3l-.4-1.75-1.2-.7-1.7.5-1.5-2.6L3 8.5v-1L1.7 6.3l1.5-2.6 1.7.5 1.2-.7Z"/><circle cx="8" cy="8" r="2"/></svg>`))
	toolbarPasteIcon    = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><rect x="5" y="1.5" width="6" height="3" rx="1"/><path d="M5 3H3.5a1 1 0 0 0-1 1v9.5a1 1 0 0 0 1 1h9a1 1 0 0 0 1-1V4a1 1 0 0 0-1-1H11M8 7v4m-2-2h4"/></svg>`))
)

func toolbarButtonContent(c *ui.Context, button ui.Element, label string, icon *ui.SVG) ui.Element {
	return button.Label(label).Gap(6).Children(func() {
		ui.Icon(c, icon).Size(16, 16).Shrink(0)
		ui.Text(c, label).SingleLine()
	})
}

func primaryAction(c *ui.Context, key, label string, disabled bool) ui.Element {
	button := ui.PrimaryButton(c.Key(key), label).Radius(pillRadius(36, c)).Disabled(disabled)
	if disabled {
		button.Background(keycapSurface(c)).Border(1, controlBorder(c)).
			TextColor(c.Theme().TextMuted).DimDisabled(false)
	}
	return button
}

func brandLockup(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Gap(8).AlignItems(ui.Center).Grow(1).MinWidth(0).Children(func() {
		ui.Box(c).Size(24, 24).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
			frame := ui.Rect{X: r.X + 2, Y: r.Y + 2, W: 20, H: 20}
			p.Fill(frame, t.Accent, 5)
			p.Stroke(frame, ui.RGBA(255, 255, 255, 0.22), 5, 1)
			p.StrokePolyline([][2]float32{
				{r.X + 6.5, r.Y + 10}, {r.X + 6.5, r.Y + 6.5}, {r.X + 10, r.Y + 6.5},
			}, 1.5, t.AccentText)
			p.StrokePolyline([][2]float32{
				{r.X + 14, r.Y + 17.5}, {r.X + 17.5, r.Y + 17.5}, {r.X + 17.5, r.Y + 14},
			}, 1.5, t.AccentText)
		})
		ui.Text(c, "Z Pic").FontSize(18).FontWeight(600).SingleLine()
	})
}

func (s *application) view(c *ui.Context) {
	s.applyTheme(c)
	if s.clipboardToast != nil {
		c.AddToast(*s.clipboardToast)
		s.clipboardToast = nil
	}
	s.prepareBatchThumbnails()
	_, pending, done, _, _, _, canRun := s.batchStats()
	showBatchOutput := s.batch != nil && !s.batch.previewOpen && canRun && pending == 0 && done > 0 &&
		s.batch.outputMode == batchOutputFolder && s.batch.outputDir != ""
	width, _ := c.Size()
	mobile := width < 720
	pagePadding, panelGap := float32(24), float32(20)
	bodyTopPad := float32(8)
	if mobile {
		pagePadding, panelGap = 12, 12
		bodyTopPad = panelGap
	} else if width < 960 {
		pagePadding, panelGap = 16, 16
		bodyTopPad = 8
	}
	if !s.modalOpen() {
		c.OnWindowShortcut(ui.Cmd, ui.KeyO, s.chooseFile)
		c.OnWindowShortcut(ui.Cmd, ui.KeyV, s.pasteImages)
		if !c.FocusTakesText() && s.canCopyResult() {
			c.OnWindowShortcut(ui.Cmd, ui.KeyC, s.copyResult)
		}
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
	ui.Column(c).Fill().Background(windowRootBackground(c)).Children(func() {
		s.topToolbar(c, width, pagePadding, mobile, pending, done, canRun)
		ui.Column(c).Grow(1).MinHeight(0).FillWidth().
			Padding(bodyTopPad, pagePadding, pagePadding, pagePadding).
			Gap(panelGap).Children(func() {
			if mobile {
				ui.Scroll(c.Key("mobile-workspace")).Grow(1).MinHeight(0).FillWidth().Gap(panelGap).Children(func() {
					s.previewPanel(c)
					s.controlsPanel(c, showBatchOutput)
				})
			} else {
				ui.Row(c).Grow(1).MinHeight(0).FillWidth().Gap(panelGap).AlignItems(ui.Stretch).Children(func() {
					s.previewPanel(c)
					s.controlsPanel(c, showBatchOutput)
				})
			}
			ui.Row(c).Height(20).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "本机压缩 · 网页图片仅在下载时联网").FontSize(12).
					TextColor(chromeMetadataColor(c)).Width(0).Grow(1).SingleLine().
					Tooltip("本地图片在本机处理；仅下载网页图片时需要联网")
				if width >= 680 {
					shortcutHint(c, "打开", "O")
					if s.batch == nil || !s.batch.previewOpen {
						shortcutHint(c, "压缩", "↵")
					} else if _, runnable := s.batchPreviewTarget(); runnable {
						shortcutHint(c, "处理此张", "↵")
					}
					if s.batch == nil {
						shortcutHint(c, "保存", "S")
					} else if !s.batch.previewOpen && s.output.Mode == presetSaveAsk {
						shortcutHint(c, "输出位置", "S")
					}
					shortcutHint(c, "设置", ",")
				}
			})
		})
	})
	s.overwriteConfirmation(c)
	s.batchOverwriteConfirmation(c)
	if s.window != nil {
		s.startNextBatchThumbnail()
	}
	s.startNextDropImport()
	s.syncFloatingWindowIfChanged()
}

func (s *application) topToolbar(c *ui.Context, width, pagePadding float32, mobile bool, pending, done int, canRun bool) {
	t := c.Theme()
	bar := c.TitleBar()
	leftPad := pagePadding
	if bar.Left > 0 {
		leftPad = max(pagePadding, bar.Left+12)
	}
	rightPad := pagePadding
	if bar.Right > 0 {
		rightPad = max(pagePadding, bar.Right+12)
	}
	availWidth := max(0, width-leftPad-rightPad)
	utilityControls := func() {
		pasteButton := quietToolbarButton(c, "paste-images", "", !s.canPasteImages()).Label("粘贴图片").
			KeyShortcuts(ui.Cmd, ui.KeyV).
			Tooltip("从剪贴板粘贴图片并加入队列 · " + shortcutLabel("V")).
			OnClick(s.pasteImages)
		if availWidth >= 850 || mobile {
			toolbarButtonContent(c, pasteButton, "粘贴图片", toolbarPasteIcon)
		} else {
			pasteButton.Children(func() { ui.Icon(c, toolbarPasteIcon).Size(16, 16) })
		}
		floatingLabel := "悬浮窗"
		if s.floatingOpen {
			floatingLabel = "收起悬浮窗"
		}
		floatingBtn := toolbarButtonContent(c, quietToolbarButton(c, "toggle-floating", "", false), floatingLabel, toolbarFloatingIcon).
			Tooltip("打开置顶拖图窗口，接收本地或网页图片").
			OnClick(func() { s.setFloatingOpen(!s.floatingOpen) })
		if s.floatingOpen {
			floatingBtn.Background(glassSelectionBackground(c)).Border(1, selectionBorder(c)).TextColor(t.Text)
		}
		settingsDisabled := s.busy || s.confirmOverwrite || s.confirmBatchOverwrite
		toolbarButtonContent(c, quietToolbarButton(c, "open-settings", "", settingsDisabled), "设置", toolbarSettingsIcon).
			KeyShortcuts(ui.Cmd, ui.KeyComma).
			Tooltip("打开设置（" + shortcutLabel(",") + "）").
			OnClick(func() { s.setSettingsOpen(true) })
	}
	if mobile || availWidth < 560 {
		row1Height := float32(36)
		topPad := pagePadding
		if bar.Height > 0 {
			row1Height = max(bar.Height, 44)
			topPad = 0
		}
		ui.Column(c).FillWidth().Padding(topPad, 0, 0, 0).Gap(8).Role(ui.RoleToolbar).Label("操作栏").Children(func() {
			ui.Row(c).FillWidth().Height(row1Height).Padding(0, rightPad, 0, leftPad).
				Gap(8).AlignItems(ui.Center).DragWindow().Children(func() {
				brandLockup(c)
				s.compressionControls(c, pending, done, canRun)
			})
			ui.Row(c).FillWidth().Padding(0, pagePadding, 0, pagePadding).
				Gap(8).Wrap().AlignItems(ui.Center).DragWindow().Children(func() {
				ui.Box(c).Grow(1)
				utilityControls()
			})
		})
		return
	}
	toolbarHeight := max(bar.Height, mainTitleBarHeight)
	toolbarGap := float32(8)
	if availWidth < 660 {
		toolbarGap = 6
	}
	ui.Row(c).FillWidth().Height(toolbarHeight).Padding(0, rightPad, 0, leftPad).
		Gap(toolbarGap).AlignItems(ui.Center).DragWindow().
		Role(ui.RoleToolbar).Label("操作栏").Children(func() {
		brandLockup(c)
		s.compressionControls(c, pending, done, canRun)
		if _, runnable := s.batchPreviewTarget(); s.batch == nil || !s.batch.previewOpen || runnable {
			ui.Box(c).Size(1, 16).Background(chromeDivider(c))
		}
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
	ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).FontSize(12).TextColor(chromeMetadataColor(c))
		keyboardKeycap(c, key).Padding(0, 5)
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
		Radius(panelRadius(c)).Background(panelSurface(c)).Border(1, glassBorder(c))
	if width < 720 {
		panel.FillWidth()
	}
	panel.Children(func() {
		ui.Row(c).Height(32).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "图片预览").FontSize(16).FontWeight(600).Width(0).Grow(1).SingleLine()
			s.previewModeControl(c)
		})
		if s.preview != nil {
			s.previewZoomToolbar(c)
		}
		stageBg := panelStageBackground(c)
		if s.preview == nil {
			stageBg = panelEmptyStageBackground(c)
		}
		stage := ui.Box(c).Grow(1).FillWidth().MinHeight(220).Radius(innerRadius(c)).
			Background(stageBg).Border(1, glassBorder(c)).Clip()
		if s.preview == nil {
			stage.Draw(func(p *ui.Painter, bounds ui.Rect) {
				if bounds.W < 120 || bounds.H < 120 {
					return
				}
				inset, arm := float32(16), float32(12)
				mark := controlBorder(c)
				x0, y0 := bounds.X+inset, bounds.Y+inset
				x1, y1 := bounds.X+bounds.W-inset, bounds.Y+bounds.H-inset
				p.StrokePolyline([][2]float32{{x0, y0 + arm}, {x0, y0}, {x0 + arm, y0}}, 1, mark)
				p.StrokePolyline([][2]float32{{x1 - arm, y0}, {x1, y0}, {x1, y0 + arm}}, 1, mark)
				p.StrokePolyline([][2]float32{{x0, y1 - arm}, {x0, y1}, {x0 + arm, y1}}, 1, mark)
				p.StrokePolyline([][2]float32{{x1 - arm, y1}, {x1, y1}, {x1, y1 - arm}}, 1, mark)
			})
		}
		stage.Children(func() {
			if s.preview == nil {
				ui.Column(c).Fill().Center().Gap(20).Padding(24).Children(func() {
					ui.Box(c).Size(60, 60).Draw(func(p *ui.Painter, bounds ui.Rect) {
						back := ui.Rect{X: bounds.X + 12, Y: bounds.Y + 6, W: 40, H: 36}
						p.Fill(back, statusTintSurface(c, t.Accent), 9)
						p.Stroke(back, statusTintBorder(c, accentForeground(t)), 9, 1)
						card := ui.Rect{X: bounds.X + 6, Y: bounds.Y + 12, W: 44, H: 40}
						p.Shadow(card, 10, 0, 3, 10, 0, ui.RGBA(0, 0, 0, 0.10))
						p.Fill(card, glassCardSurface(c), 10)
						p.Stroke(card, controlBorder(c), 10, 1)
						p.StrokePolyline([][2]float32{
							{bounds.X + 12, bounds.Y + 24}, {bounds.X + 12, bounds.Y + 18}, {bounds.X + 18, bounds.Y + 18},
						}, 1.5, accentForeground(t))
						p.StrokePolyline([][2]float32{
							{bounds.X + 38, bounds.Y + 46}, {bounds.X + 44, bounds.Y + 46}, {bounds.X + 44, bounds.Y + 40},
						}, 1.5, accentForeground(t))
						p.Stroke(ui.Rect{X: bounds.X + 33, Y: bounds.Y + 21, W: 5, H: 5}, t.TextMuted, 2.5, 1.5)
						p.StrokePolyline([][2]float32{
							{bounds.X + 14, bounds.Y + 42},
							{bounds.X + 24, bounds.Y + 31},
							{bounds.X + 31, bounds.Y + 38},
							{bounds.X + 36, bounds.Y + 34},
							{bounds.X + 42, bounds.Y + 42},
						}, 1.5, t.TextMuted)
					})
					ui.Text(c, "选择或拖入图片").FontSize(18).FontWeight(600)
					ui.Column(c).Gap(12).AlignItems(ui.Center).Children(func() {
						ui.Row(c).Gap(8).Children(func() {
							primaryAction(c, "select", "选择图片", s.modalOpen()).Height(36).Padding(0, 16).
								KeyShortcuts(ui.Cmd, ui.KeyO).Tooltip("选择单张或多张图片 · " + shortcutLabel("O")).
								OnClick(s.chooseFile)
							quietButton(c, "select-folder", "选择文件夹", 36, s.modalOpen()).Padding(0, 16).
								Tooltip("选择包含图片的文件夹").
								OnClick(s.chooseFolder)
						})
						ui.Column(c).Gap(4).AlignItems(ui.Center).Children(func() {
							ui.Text(c, "JPEG · PNG · WebP").FontSize(12).FontFeatures("tnum").TextColor(metadataColor(c))
							ui.Text(c, "单张最大 64 MiB · 3200 万像素").FontSize(12).FontFeatures("tnum").TextColor(metadataColor(c))
						})
					})
				})
				return
			}
			s.previewCanvas(c)
		})
		if s.file != nil {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Width(0).Grow(1).Gap(4).Children(func() {
					ui.Text(c, filepath.Base(s.file.path)).FontSize(13).FontWeight(500).
						SingleLine().TruncateMiddle().Tooltip(s.file.path)
					ui.Textf(c, "%s  ·  %d × %d  ·  %s",
						strings.ToUpper(s.file.format), s.file.width, s.file.height, formatSize(s.file.size)).
						FontSize(12).FontFeatures("tnum").TextColor(metadataColor(c)).SingleLine()
				})
				quietButton(c, "replace", "更换图片", 32, s.modalOpen()).
					KeyShortcuts(ui.Cmd, ui.KeyO).Tooltip("更换图片 · " + shortcutLabel("O")).
					OnClick(s.chooseFile)
			})
		}
	})
}

func (s *application) compressionControls(c *ui.Context, pending, done int, canRun bool) {
	disabled := s.modalOpen()
	if s.batch == nil {
		label := "开始压缩"
		icon := toolbarCompressIcon
		if s.result != nil {
			label = "重新压缩"
			icon = toolbarRestartIcon
		}
		toolbarButtonContent(c, primaryAction(c, "compress", "", disabled || s.file == nil), label, icon).Height(32).
			KeyShortcuts(ui.Cmd, ui.KeyEnter).Tooltip("压缩当前图片 · " + shortcutLabel("↵")).
			OnClick(s.startCompression)
		return
	}
	if s.batch.previewOpen {
		index, runnable := s.batchPreviewTarget()
		if !runnable {
			return
		}
		item := s.batch.items[index]
		label, key, icon := "压缩此张", "batch-preview-compress", toolbarCompressIcon
		if item.status == batchFailed {
			label, key, icon = "重试", "batch-preview-retry", toolbarRestartIcon
		}
		toolbarButtonContent(c, primaryAction(c, key, "", disabled), label, icon).Height(32).
			KeyShortcuts(ui.Cmd, ui.KeyEnter).
			Tooltip("仅处理当前预览图片 · " + shortcutLabel("↵")).
			OnClick(func() { s.compressBatchItem(index) })
		return
	}
	disabled = disabled || !canRun || s.batch.previewOpen
	switch {
	case (pending > 0 && done == 0) || !canRun:
		toolbarButtonContent(c, primaryAction(c, "batch-start", "", disabled), "开始压缩", toolbarCompressIcon).Height(32).
			KeyShortcuts(ui.Cmd, ui.KeyEnter).Tooltip("开始批量压缩 · " + shortcutLabel("↵")).
			OnClick(s.startBatchCompression)
	case pending > 0 && done > 0:
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			toolbarButtonContent(c, quietButton(c, "batch-restart", "", 32, disabled), "重新压缩", toolbarRestartIcon).
				OnClick(s.restartBatchCompression)
			toolbarButtonContent(c, primaryAction(c, "batch-continue", "", disabled), "继续压缩", toolbarContinueIcon).Height(32).
				KeyShortcuts(ui.Cmd, ui.KeyEnter).Tooltip("继续压缩剩余图片 · " + shortcutLabel("↵")).
				OnClick(s.startBatchCompression)
		})
	default:
		toolbarButtonContent(c, primaryAction(c, "batch-restart", "", disabled), "重新压缩", toolbarRestartIcon).Height(32).
			KeyShortcuts(ui.Cmd, ui.KeyEnter).Tooltip("重新压缩全部图片 · " + shortcutLabel("↵")).
			OnClick(s.restartBatchCompression)
	}
}

func (s *application) batchPreviewTarget() (int, bool) {
	if s.batch == nil || !s.batch.previewOpen {
		return -1, false
	}
	index := s.batch.previewIndex
	if index < 0 || index >= len(s.batch.items) {
		return -1, false
	}
	item := s.batch.items[index]
	return index, !item.invalidInput && (item.status == batchPending || item.status == batchFailed)
}

func (s *application) controlsPanel(c *ui.Context, showBatchOutput bool) {
	t := c.Theme()
	width, _ := c.Size()
	mobile := width < 720
	panelWidth := float32(320)
	if width < 960 {
		panelWidth = 280
	}
	panel := ui.Column(c).Padding(16).Gap(12).Radius(panelRadius(c)).
		Background(panelSurface(c)).Border(1, glassBorder(c))
	if mobile {
		panel.FillWidth()
	} else {
		panel.Width(panelWidth).Shrink(0)
	}
	panel.Children(func() {
		s.presetOverview(c)
		ui.Box(c).FillWidth().Height(1).Background(glassDivider(c))
		if s.batch != nil {
			s.batchControls(c, showBatchOutput, mobile)
			return
		}
		summaryBody := func() {
			if s.result != nil {
				s.imageResultSummary(c, "压缩结果")
			}
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
			s.workFeedback(c)
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
					quietButton(c, "reveal-saved", "定位文件", 32, s.modalOpen()).
						Tooltip("在文件夹中显示输出文件").
						OnClick(func() { s.revealFileInFolder(s.lastSavedPath) })
				})
			} else {
				ui.Text(c, s.message).FontSize(12).TextColor(color).Role(role).
					MaxLines(2).Tooltip(s.message)
			}
		}
		if s.result != nil {
			label, hint := s.saveActionDescription()
			disabled := s.modalOpen() || (s.output.replacesOriginal() && !s.canOverwrite())
			var save ui.Element
			if s.output.replacesOriginal() {
				save = destructiveAction(c, "save", label, 36, disabled)
			} else {
				save = primaryAction(c, "save", label, disabled).Height(36)
			}
			save.FillWidth().KeyShortcuts(ui.Cmd, ui.KeyS).
				Tooltip(hint + " · " + shortcutLabel("S")).OnClick(s.savePresetResult)
			ui.Row(c).FillWidth().Gap(8).Children(func() {
				quietButton(c, "save-copy", "另存副本", 32, s.modalOpen()).Width(0).Grow(1).
					OnClick(s.saveCopy)
				quietButton(c, "copy-result", "复制图片", 32, !s.canCopyResult()).Width(0).Grow(1).
					KeyShortcuts(ui.Cmd, ui.KeyC).
					Tooltip("将压缩结果复制到剪贴板，可直接粘贴图片 · " + shortcutLabel("C")).OnClick(s.copyResult)
			})
		}
	})
}

func (s *application) workFeedback(c *ui.Context) {
	batchRunning := s.batch != nil && s.batch.running
	if batchRunning && s.batch.total > 0 {
		done, total := min(s.batch.done, s.batch.total), s.batch.total
		progress := float64(done) / float64(total)
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "本次进度").FontSize(12).TextColor(metadataColor(c)).Grow(1)
			ui.Textf(c, "%d / %d · %.0f%%", done, total, progress*100).
				FontSize(12).FontFeatures("tnum").SingleLine()
		})
		ui.Progress(c, progress).Label("批量压缩进度").FillWidth()
	}
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Spinner(c).Size(16, 16).Shrink(0)
		ui.Text(c, s.phase).FontSize(12).Role(ui.RoleStatus).Width(0).Grow(1).
			MaxLines(3).Tooltip(s.phase)
	})
	if s.cancel != nil {
		label := "取消操作"
		if s.canceling {
			label = "正在取消…"
		}
		quietButton(c, "cancel", label, 32, s.canceling).
			Description("等待当前编解码步骤结束后停止").OnClick(s.cancelWork)
		if batchRunning {
			ui.Text(c, "已完成项会保留，可继续剩余图片").FontSize(12).
				TextColor(metadataColor(c)).MaxLines(2)
		}
	}
	if len(s.dropQueue) > 0 {
		queuedNote := fmt.Sprintf("已暂存 %d 次拖入，当前操作结束后入队", len(s.dropQueue))
		if s.dropFailed && s.dropMessage != "" {
			queuedNote = s.dropMessage
		}
		ui.Text(c, queuedNote).FontSize(12).TextColor(metadataColor(c)).MaxLines(2)
	}
}

func (s *application) presetOverview(c *ui.Context) {
	ui.Column(c).Gap(8).Children(func() {
		ui.Row(c).Height(32).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "压缩概览").FontSize(16).FontWeight(600).Width(0).Grow(1).SingleLine()
			if s.batch != nil && s.batch.previewOpen {
				ui.Text(c, "单张预览").FontSize(12).FontWeight(500).TextColor(accentForeground(c.Theme())).
					Padding(2, 8).Shrink(0).Radius(badgeRadius(20)).
					Background(glassSelectionBackground(c)).Border(1, selectionBorder(c))
			}
		})
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "当前预设").FontSize(12).TextColor(metadataColor(c)).Width(64).Shrink(0)
			ui.Text(c, s.scheme).FontSize(13).FontWeight(500).Width(0).Grow(1).
				SingleLine().TruncateMiddle().Tooltip(s.scheme)
			quietButton(c, "manage-preset", "编辑", 28, s.modalOpen()).
				Label("管理预设").Tooltip("编辑当前预设的质量和保存方式").
				OnClick(s.openPresetSettings)
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
	ui.Column(c).Padding(12).Gap(8).Radius(innerRadius(c)).Background(panelSubtleSurface(c)).Border(1, glassBorder(c)).Children(func() {
		ui.Text(c, "保存方式").FontSize(12).FontWeight(500).TextColor(metadataColor(c))
		description, color := presetSaveDescription(s.output), t.Text
		if s.output.replacesOriginal() {
			color = t.Danger
		}
		ui.Text(c, description).FontSize(13).FontWeight(500).TextColor(color).MaxLines(2).Tooltip(description)
		name := "example.png"
		if s.file != nil {
			name = filepath.Base(s.file.path)
		}
		outputFilenamePreview(c, s.output, name)
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

func (s *application) imageResultSummary(c *ui.Context, title string) {
	t := c.Theme()
	original, output := "—", "—"
	label, color := "待压缩", t.TextMuted
	note := ""
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
			note = "已保留原始数据"
		}
	}
	if s.batch != nil && s.batch.previewOpen && s.result == nil {
		if index := s.batch.previewIndex; index >= 0 && index < len(s.batch.items) && s.batch.items[index].status == batchFailed {
			label, color = "压缩失败", t.Danger
		}
	}
	summaryCardWithNote(c, title, label, color, "原始大小", original, "输出大小", output, note, ratio)
}

func (s *application) saveActionDescription() (string, string) {
	if s.output.replacesOriginal() {
		if s.overwritten {
			return "已覆盖原图", "此结果已经写入原文件，可另存副本或复制图片"
		}
		if s.result != nil && s.result.Unchanged {
			return "无需覆盖（未减小）", "已保留原始数据，可另存副本或复制图片"
		}
		return "覆盖原图…", "确认后替换原文件，无法撤销"
	}
	switch s.output.Mode {
	case presetSaveAsk:
		return "选择位置并保存…", "选择文件夹后保存副本"
	case presetSaveFixed:
		return "保存到预设目录", "按当前预设保存副本，已有文件不会被覆盖"
	default:
		return "保存到原文件夹", "按当前预设保存副本，已有文件不会被覆盖"
	}
}

func outputFilenamePreview(c *ui.Context, options presetSaveOptions, filename string) {
	if options.replacesOriginal() {
		return
	}
	example := presetOutputName(options, filename)
	ui.Column(c).Gap(4).Children(func() {
		ui.Text(c, "文件示例").FontSize(12).TextColor(metadataColor(c))
		ui.Text(c, example).FontSize(12).FontFeatures("tnum").SingleLine().TruncateMiddle().
			FillWidth().Label("输出文件示例：" + example).
			Tooltip(example + "\n已有同名文件时自动使用新名称")
	})
}

func summaryCard(c *ui.Context, title, status string, statusColor ui.Color, firstLabel, firstValue, secondLabel, secondValue string) {
	summaryCardWithNote(c, title, status, statusColor, firstLabel, firstValue, secondLabel, secondValue, "", -1)
}

func summaryCardWithNote(c *ui.Context, title, status string, statusColor ui.Color, firstLabel, firstValue, secondLabel, secondValue, note string, ratio float64) {
	t := c.Theme()
	width, _ := c.Size()
	compact := width >= 720 && width < 960
	ui.Column(c).Padding(12).Gap(8).Radius(innerRadius(c)).Background(panelSubtleSurface(c)).Border(1, glassBorder(c)).Children(func() {
		ui.Text(c, title).FontSize(12).FontWeight(500).TextColor(metadataColor(c))
		ui.Row(c).Gap(12).AlignItems(ui.End).Children(func() {
			for index, row := range []struct{ label, value string }{
				{firstLabel, firstValue}, {secondLabel, secondValue},
			} {
				if index == 1 {
					ui.Row(c).Width(16).Height(28).Center().Children(func() {
						ui.Text(c, "→").FontSize(14).TextColor(metadataColor(c)).SingleLine()
					})
				}
				grow := float32(1)
				if index == 1 {
					grow = 1.2
				}
				ui.Column(c).Width(0).Grow(grow).Gap(4).Children(func() {
					ui.Text(c, row.label).FontSize(12).TextColor(metadataColor(c))
					size := float32(18)
					weight := 500
					valColor := t.TextMuted
					if compact {
						size = 16
					}
					if index == 1 {
						size = 24
						weight = 600
						valColor = t.Text
						if compact {
							size = 20
						}
					}
					ui.Row(c).Height(28).AlignItems(ui.Center).Children(func() {
						ui.Text(c, row.value).FontSize(size).FontWeight(weight).FontFeatures("tnum").
							TextColor(valColor).SingleLine().Tooltip(row.value)
					})
				})
			}
		})
		if ratio >= 0 {
			barColor := t.Success
			if ratio >= 1 {
				barColor = t.TextMuted.Alpha(0.45)
			}
			ui.Box(c).FillWidth().Height(5).Tooltip(fmt.Sprintf("输出体积为原始体积的 %.1f%%", ratio*100)).
				Draw(func(p *ui.Painter, r ui.Rect) {
					p.Fill(r, meterTrackColor(c), 2.5)
					fillW := max(5, r.W*float32(min(1, ratio)))
					p.Fill(ui.Rect{X: r.X, Y: r.Y, W: fillW, H: r.H}, barColor, 2.5)
				})
		}
		if statusColor == t.Success {
			ui.Row(c).Children(func() {
				ui.Text(c, status).FontSize(12).FontWeight(500).FontFeatures("tnum").Role(ui.RoleStatus).
					TextColor(statusColor).Padding(2, 8).Radius(badgeRadius(20)).
					Background(statusTintSurface(c, t.Success)).Border(1, statusTintBorder(c, t.Success)).
					MaxLines(2).Tooltip(status)
			})
		} else {
			ui.Text(c, status).FontSize(12).FontWeight(500).FontFeatures("tnum").Role(ui.RoleStatus).
				TextColor(statusColor).MaxLines(2).Tooltip(status)
		}
		if note != "" {
			ui.Text(c, note).FontSize(12).FontFeatures("tnum").TextColor(metadataColor(c)).
				MaxLines(2).Tooltip(note)
		}
	})
}
