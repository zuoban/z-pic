package main

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"zpic/internal/compress"
)

const mainTitleBarHeight = 52

var (
	toolbarCompressIcon          = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="M2 2l4 4M2 6h4V2M14 14l-4-4m0 4v-4h4"/></svg>`))
	toolbarRestartIcon           = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="M13 6a5.25 5.25 0 1 0 .15 3.5M13 2.5V6H9.5"/></svg>`))
	toolbarContinueIcon          = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="M5 3l7 5-7 5Z"/></svg>`))
	toolbarFloatingIcon          = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round"><rect x="2" y="3" width="12" height="10" rx="2"/><rect x="8" y="7" width="4" height="4" rx=".5"/></svg>`))
	toolbarSettingsIcon          = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="M6.5 1.75h3l.4 1.75 1.2.7 1.7-.5 1.5 2.6L13 7.5v1l1.3 1.2-1.5 2.6-1.7-.5-1.2.7-.4 1.75h-3l-.4-1.75-1.2-.7-1.7.5-1.5-2.6L3 8.5v-1L1.7 6.3l1.5-2.6 1.7.5 1.2-.7Z"/><circle cx="8" cy="8" r="2"/></svg>`))
	toolbarPasteIcon             = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><rect x="5" y="1.5" width="6" height="3" rx="1"/><path d="M5 3H3.5a1 1 0 0 0-1 1v9.5a1 1 0 0 0 1 1h9a1 1 0 0 0 1-1V4a1 1 0 0 0-1-1H11M8 7v4m-2-2h4"/></svg>`))
	toolbarClipboardCompressIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><rect x="5" y="1.5" width="6" height="3" rx="1"/><path d="M5 3H3.5a1 1 0 0 0-1 1v9.5a1 1 0 0 0 1 1h9a1 1 0 0 0 1-1V4a1 1 0 0 0-1-1H11M5.5 7l2 2m0-2v2h-2m5 3-2-2m0 2v-2h2"/></svg>`))
	toolbarDragIcon              = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="M6 10l6-6M8 4h4v4M12 10v2.5a1 1 0 0 1-1 1H3.5a1 1 0 0 1-1-1V5a1 1 0 0 1 1-1H6"/></svg>`))
)

func toolbarButtonContent(c *ui.Context, button ui.Element, label string, icon *ui.SVG) ui.Element {
	return button.Label(label).Padding(0, 10).Gap(5).Children(func() {
		ui.Icon(c, icon).Size(15, 15).Shrink(0)
		ui.Text(c, label).FontSize(13).FontWeight(500).SingleLine()
	})
}

func dragExportControl(
	c *ui.Context,
	key, label, text, tooltip string,
	height, fontSize float32,
	weight int,
	disabled bool,
	data func() transfer.Data,
	opts transfer.DragOptions,
) ui.Element {
	t := c.Theme()
	padX, gap := float32(12), float32(6)
	if height <= 28 {
		padX, gap = 8, 4
	}
	radius := pillRadius(height, c)
	highContrast := c.Preferences().HighContrast
	solidBorder := ui.Transparent
	if highContrast {
		solidBorder = controlBorder(c)
	}
	dashColor := controlBorder(c)
	chip := ui.Row(c.Key(key)).Height(height).Padding(0, padX).Gap(gap).
		Center().Shrink(0).Radius(radius).
		Background(tonalWellSurface(c)).Border(1, solidBorder).
		Label(label).Tooltip(tooltip).Disabled(disabled)
	textColor := t.TextMuted
	if !disabled {
		chip.DragDataFrom(data, opts).Cursor(ui.CursorGrab)
		if chip.Pressed() {
			textColor = t.Text
			dashColor = controlHoverBorder(c)
			chip.Cursor(ui.CursorGrabbing).Background(glassControlPressed(c))
			if highContrast {
				chip.Border(1, controlHoverBorder(c))
			}
		} else if chip.Hovered() {
			textColor = t.Text
			dashColor = controlHoverBorder(c)
			chip.Background(glassControlHover(c))
			if highContrast {
				chip.Border(1, controlHoverBorder(c))
			}
		}
	}
	if !highContrast {
		chip.DrawOver(func(p *ui.Painter, bounds ui.Rect) {
			drawDashedOutline(p, bounds, radius, dashColor)
		})
	}
	chip.Children(func() {
		ui.Icon(c, toolbarDragIcon).Size(14, 14).TextColor(textColor).Shrink(0)
		ui.Text(c, text).FontSize(fontSize).FontWeight(weight).FontFeatures("tnum").
			TextColor(textColor).SingleLine().NoWrap()
	})
	return chip
}

func primaryAction(c *ui.Context, key, label string, disabled bool) ui.Element {
	button := ui.PrimaryButton(c.Key(key), label).Radius(pillRadius(36, c)).Disabled(disabled)
	if disabled {
		border := hairlineDivider(c)
		if c.Preferences().HighContrast {
			border = controlBorder(c)
		}
		button.Background(tonalWellSurface(c)).Border(1, border).
			TextColor(metadataColor(c)).DimDisabled(false)
	}
	return button
}

func brandLockup(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Gap(8).AlignItems(ui.Center).Grow(1).MinWidth(0).Children(func() {
		ui.Box(c).Size(24, 24).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
			frame := ui.Rect{X: r.X + 1, Y: r.Y + 1, W: 22, H: 22}
			p.Fill(frame, t.Accent, 6)
			p.Stroke(frame, ui.RGBA(255, 255, 255, 0.22), 6, 1)
			p.StrokePolyline([][2]float32{
				{r.X + 6, r.Y + 10}, {r.X + 6, r.Y + 6}, {r.X + 10, r.Y + 6},
			}, 1.6, t.AccentText)
			p.StrokePolyline([][2]float32{
				{r.X + 14, r.Y + 18}, {r.X + 18, r.Y + 18}, {r.X + 18, r.Y + 14},
			}, 1.6, t.AccentText)
		})
		ui.Text(c, "Z Pic").FontSize(15).FontWeight(600).SingleLine()
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
		s.batchEffectiveOutputDir() != ""
	width, _ := c.Size()
	mobile := width < 720
	toolbarPadding := float32(16)
	if mobile {
		toolbarPadding = 12
	}
	if !s.modalOpen() {
		c.OnWindowShortcut(ui.Cmd, ui.KeyO, s.chooseFile)
		c.OnWindowShortcut(ui.Cmd, ui.KeyV, s.pasteImages)
		if !c.FocusTakesText() && s.canCompressClipboardPNG() {
			c.OnWindowShortcut(ui.Cmd|ui.Shift, ui.KeyV, s.compressClipboardPNG)
		}
		if !c.FocusTakesText() && s.canCopyResult() {
			c.OnWindowShortcut(ui.Cmd, ui.KeyC, s.copyResult)
		}
		if !c.FocusTakesText() && s.supportsQuickLook() {
			c.OnWindowShortcut(0, ui.KeySpace, s.quickLookCurrent)
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
		s.topToolbar(c, width, toolbarPadding, mobile, pending, done, canRun)
		ui.Box(c).FillWidth().Height(1).Background(hairlineDivider(c))
		if mobile {
			ui.Scroll(c.Key("mobile-workspace")).Grow(1).MinHeight(0).FillWidth().Children(func() {
				s.previewPanel(c)
				ui.Box(c).FillWidth().Height(1).Background(hairlineDivider(c))
				s.controlsPanel(c, showBatchOutput)
			})
		} else {
			ui.Row(c).Grow(1).MinHeight(0).FillWidth().AlignItems(ui.Stretch).Children(func() {
				s.previewPanel(c)
				ui.Box(c).Width(1).FillHeight().Background(hairlineDivider(c))
				s.controlsPanel(c, showBatchOutput)
			})
		}
		if s.watch.session != nil || s.watch.last.Input != "" || s.watch.err != nil {
			ui.Box(c).FillWidth().Height(1).Background(hairlineDivider(c))
			ui.Box(c).FillWidth().Padding(8, toolbarPadding).Background(sidebarSurface(c)).Children(func() {
				s.folderWatchStatusBar(c)
			})
		}
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
		if availWidth >= 820 || mobile {
			toolbarButtonContent(c, pasteButton, "粘贴", toolbarPasteIcon).Label("粘贴图片")
		} else {
			pasteButton.Size(32, 32).Padding(0).Center().Children(func() { ui.Icon(c, toolbarPasteIcon).Size(15, 15) })
		}
		if s.clipboardPNGCompressionAvailable() {
			const label = "压缩剪贴板图片"
			button := quietToolbarButton(c, "compress-clipboard-png", "", !s.canCompressClipboardPNG()).
				Label(label).KeyShortcuts(ui.Cmd|ui.Shift, ui.KeyV).
				Tooltip("压缩剪贴板中的单张 PNG 并写回 · " + shortcutLabel("⇧V")).OnClick(s.compressClipboardPNG)
			if availWidth >= 820 || mobile {
				toolbarButtonContent(c, button, "剪贴板压缩", toolbarClipboardCompressIcon).Label(label)
			} else {
				button.Size(32, 32).Padding(0).Center().Children(func() { ui.Icon(c, toolbarClipboardCompressIcon).Size(15, 15) })
			}
		}
		floatingLabel := "悬浮窗"
		if s.floatingOpen {
			floatingLabel = "收起悬浮窗"
		}
		floatingBtn := toolbarButtonContent(c, quietToolbarButton(c, "toggle-floating", "", false), floatingLabel, toolbarFloatingIcon).
			Tooltip("打开置顶拖图窗口，接收本地或网页图片").
			OnClick(func() { s.setFloatingOpen(!s.floatingOpen) })
		if s.floatingOpen {
			floatingBtn.Background(glassSelectionBackground(c)).Border(1, selectionBorder(c)).TextColor(accentForeground(t))
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
		ui.Column(c).FillWidth().Padding(topPad, 0, 8, 0).Gap(8).Role(ui.RoleToolbar).Label("操作栏").Children(func() {
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
	toolbarGap := float32(6)
	if availWidth < 660 {
		toolbarGap = 4
	}
	ui.Row(c).FillWidth().Height(toolbarHeight).Padding(0, rightPad, 0, leftPad).
		Gap(toolbarGap).AlignItems(ui.Center).DragWindow().
		Role(ui.RoleToolbar).Label("操作栏").Children(func() {
		brandLockup(c)
		s.compressionControls(c, pending, done, canRun)
		if _, runnable := s.batchPreviewTarget(); s.batch == nil || !s.batch.previewOpen || runnable {
			ui.Box(c).Size(1, 14).Margin(0, 2).Background(chromeDivider(c))
		}
		utilityControls()
	})
}

func shortcutLabel(key string) string {
	if runtime.GOOS == "darwin" {
		if strings.HasPrefix(key, "⇧") && len(key) > len("⇧") {
			return "⇧ ⌘ " + strings.TrimPrefix(key, "⇧")
		}
		return "⌘ " + key
	}
	if strings.HasPrefix(key, "⇧") && len(key) > len("⇧") {
		return "Ctrl+Shift+" + strings.TrimPrefix(key, "⇧")
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
	mobile := width < 720
	pad := float32(16)
	if width < 960 {
		pad = 12
	}
	minW := float32(340)
	if mobile {
		minW = 0
	}
	panel := ui.Column(c).Grow(1).MinWidth(minW).Padding(pad).Gap(10)
	if mobile {
		panel.FillWidth().MinHeight(380)
	}
	panel.Children(func() {
		if s.preview == nil {
			dropzone := ui.Column(c).Grow(1).FillWidth().MinHeight(240).Center().Padding(16).
				Radius(panelRadius(c)).Background(emptyDropzoneSurface(c)).Border(1, glassBorder(c))
			dropzone.Draw(func(p *ui.Painter, bounds ui.Rect) {
				inset := float32(16)
				if bounds.W <= inset*2+36 || bounds.H <= inset*2+36 {
					return
				}
				dashColor := t.Text.Alpha(0.10)
				cornerColor := t.Text.Alpha(0.18)
				if t.Dark {
					dashColor = ui.RGBA(255, 255, 255, 0.10)
					cornerColor = ui.RGBA(255, 255, 255, 0.18)
				}
				x1, y1 := bounds.X+inset, bounds.Y+inset
				x2, y2 := bounds.X+bounds.W-inset, bounds.Y+bounds.H-inset
				const corner = float32(10)
				// Subtle corner brackets anchor the drop frame cleanly.
				p.StrokePolyline([][2]float32{{x1, y1 + corner}, {x1, y1}, {x1 + corner, y1}}, 1.25, cornerColor)
				p.StrokePolyline([][2]float32{{x2 - corner, y1}, {x2, y1}, {x2, y1 + corner}}, 1.25, cornerColor)
				p.StrokePolyline([][2]float32{{x1, y2 - corner}, {x1, y2}, {x1 + corner, y2}}, 1.25, cornerColor)
				p.StrokePolyline([][2]float32{{x2 - corner, y2}, {x2, y2}, {x2, y2 - corner}}, 1.25, cornerColor)
				const seg, gap = float32(5), float32(7)
				for x := x1 + corner + gap; x < x2-corner-gap; x += seg + gap {
					end := min(x+seg, x2-corner-gap)
					p.Line(x, y1, end, y1, 1, dashColor)
					p.Line(x, y2, end, y2, 1, dashColor)
				}
				for y := y1 + corner + gap; y < y2-corner-gap; y += seg + gap {
					end := min(y+seg, y2-corner-gap)
					p.Line(x1, y, x1, end, 1, dashColor)
					p.Line(x2, y, x2, end, 1, dashColor)
				}
			})
			dropzone.Children(func() {
				ui.Column(c).Center().Gap(16).Padding(24).Children(func() {
					ui.Box(c).Size(52, 52).Draw(func(p *ui.Painter, bounds ui.Rect) {
						bg := ui.Rect{X: bounds.X + 2, Y: bounds.Y + 2, W: 48, H: 48}
						p.Fill(bg, statusTintSurface(c, t.Accent), 14)
						p.Stroke(bg, statusTintBorder(c, accentForeground(t)), 14, 1)
						frame := ui.Rect{X: bounds.X + 14, Y: bounds.Y + 16, W: 24, H: 20}
						p.Stroke(frame, accentForeground(t), 4, 1.5)
						p.Fill(ui.Rect{X: bounds.X + 19, Y: bounds.Y + 21, W: 3.5, H: 3.5}, accentForeground(t), 1.75)
						p.StrokePolyline([][2]float32{
							{bounds.X + 16, bounds.Y + 33},
							{bounds.X + 23, bounds.Y + 26},
							{bounds.X + 28, bounds.Y + 31},
							{bounds.X + 32, bounds.Y + 27},
							{bounds.X + 36, bounds.Y + 33},
						}, 1.5, accentForeground(t))
					})
					ui.Column(c).Center().Gap(6).Children(func() {
						ui.Text(c, "选择或拖入图片").FontSize(17).FontWeight(600)
						formats := strings.Join(supportedImageFormats(), " · ")
						ui.Text(c, formats+" · 最大 64 MiB").FontSize(12).FontFeatures("tnum").
							TextColor(metadataColor(c)).SingleLine()
					})
					ui.Row(c).Gap(10).Children(func() {
						primaryAction(c, "select", "选择图片", s.modalOpen()).Height(36).Padding(0, 20).
							KeyShortcuts(ui.Cmd, ui.KeyO).Tooltip("选择单张或多张图片 · " + shortcutLabel("O")).
							OnClick(s.chooseFile)
						quietButton(c, "select-folder", "选择文件夹", 36, s.modalOpen()).Padding(0, 16).
							Tooltip("选择包含图片的文件夹").
							OnClick(s.chooseFolder)
					})
					ui.Row(c).Gap(5).AlignItems(ui.Center).Padding(2, 0, 0, 0).Children(func() {
						ui.Text(c, "本机处理不上传 · 按").FontSize(12).TextColor(metadataColor(c)).SingleLine()
						keyboardKeycap(c, "V")
						ui.Text(c, "粘贴或拖入悬浮窗").FontSize(12).TextColor(metadataColor(c)).SingleLine()
					})
				})
			})
			return
		}
		s.previewUnifiedBar(c)
		stage := ui.Box(c).Grow(1).FillWidth().MinHeight(220).Radius(innerRadius(c)).
			Background(canvasWellBackground(c)).Border(1, glassBorder(c)).Clip()
		stage.Children(func() {
			s.previewCanvas(c)
		})
		if s.file != nil {
			ui.Row(c).MinHeight(34).Padding(2, 0, 0, 0).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Width(0).Grow(1).Gap(2).Children(func() {
					ui.Text(c, filepath.Base(s.file.path)).FontSize(13).FontWeight(600).
						SingleLine().TruncateMiddle().Tooltip(s.file.path)
					meta := fmt.Sprintf("%s  ·  %d × %d  ·  %s",
						strings.ToUpper(s.file.format), s.file.width, s.file.height, formatSize(s.file.size))
					if result := s.result; result != nil {
						if result.TargetResized {
							if s.comparison.mode != previewOriginal {
								meta = outputPreviewMetadata(result)
							}
						} else {
							if result.Format != "" && result.Format != s.file.format {
								meta += " → " + strings.ToUpper(result.Format)
							}
							if result.Width > 0 && result.Height > 0 &&
								(result.Width != s.file.width || result.Height != s.file.height) {
								meta += fmt.Sprintf(" · %d × %d", result.Width, result.Height)
							}
						}
					}
					ui.Text(c, meta).
						FontSize(12).FontFeatures("tnum").TextColor(metadataColor(c)).SingleLine().TruncateMiddle().Tooltip(meta)
				})
				quietButton(c, "replace", "更换图片", 30, s.modalOpen()).Padding(0, 10).
					KeyShortcuts(ui.Cmd, ui.KeyO).Tooltip("更换图片 · " + shortcutLabel("O")).
					OnClick(s.chooseFile)
				ghostButton(c, "single-clear", "清空", 30, s.modalOpen()).Padding(0, 10).
					Tooltip("关闭当前图片").
					OnClick(s.clearSingleImage)
			})
		}
	})
}

func toolbarPrimaryAction(c *ui.Context, key, label string, icon *ui.SVG, disabled bool) ui.Element {
	return toolbarButtonContent(c, primaryAction(c, key, "", disabled), label, icon).
		Height(32).Radius(pillRadius(32, c)).Padding(0, 12)
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
		toolbarPrimaryAction(c, "compress", label, icon, disabled || s.file == nil).
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
		toolbarPrimaryAction(c, key, label, icon, disabled).
			KeyShortcuts(ui.Cmd, ui.KeyEnter).
			Tooltip("仅处理当前预览图片 · " + shortcutLabel("↵")).
			OnClick(func() { s.compressBatchItem(index) })
		return
	}
	disabled = disabled || !canRun || s.batch.previewOpen
	switch {
	case (pending > 0 && done == 0) || !canRun:
		toolbarPrimaryAction(c, "batch-start", "开始压缩", toolbarCompressIcon, disabled).
			KeyShortcuts(ui.Cmd, ui.KeyEnter).Tooltip("开始批量压缩 · " + shortcutLabel("↵")).
			OnClick(s.startBatchCompression)
	case pending > 0 && done > 0:
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			toolbarButtonContent(c, ghostButton(c, "batch-restart", "", 32, disabled), "重新压缩", toolbarRestartIcon).
				OnClick(s.restartBatchCompression)
			toolbarPrimaryAction(c, "batch-continue", "继续压缩", toolbarContinueIcon, disabled).
				KeyShortcuts(ui.Cmd, ui.KeyEnter).Tooltip("继续压缩剩余图片 · " + shortcutLabel("↵")).
				OnClick(s.startBatchCompression)
		})
	default:
		toolbarPrimaryAction(c, "batch-restart", "重新压缩", toolbarRestartIcon, disabled).
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
	panelWidth := float32(312)
	if width < 960 {
		panelWidth = 280
	}
	panel := ui.Column(c).Padding(16).Gap(10).Background(sidebarSurface(c))
	if mobile {
		panel.FillWidth()
	} else {
		panel.Width(panelWidth).Shrink(0)
	}
	panel.Children(func() {
		s.presetOverview(c)
		if s.batch != nil {
			s.batchControls(c, showBatchOutput, mobile)
			return
		}
		summaryBody := func() {
			if s.file != nil {
				s.imageResultSummary(c, "压缩结果")
			}
			s.singleOutputSummary(c)
			if s.file != nil {
				resolvedFmt := compress.ResolveOutputFormat(s.file.format, s.outputFormat, s.lossless)
				formatTip := ""
				switch {
				case resolvedFmt == "png" && !s.lossless:
					formatTip = "PNG 有损压缩可能改变渐变与半透明边缘"
				case resolvedFmt == "jpeg" && s.file.format != "jpeg":
					formatTip = "转为 JPEG 时透明区域将自动填充白色背景"
				}
				if formatTip != "" {
					ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Start).Children(func() {
						ui.Icon(c, presetInfoIcon).Size(13, 13).Padding(1, 0, 0, 0).
							TextColor(metadataColor(c)).Shrink(0)
						ui.Text(c, formatTip).FontSize(11).TextColor(t.TextMuted).
							Width(0).Grow(1).MaxLines(2)
					})
				}
			}
		}
		if mobile {
			ui.Column(c).FillWidth().Gap(8).Children(summaryBody)
		} else {
			ui.Scroll(c.Key("single-compression-summary")).Grow(1).MinHeight(0).FillWidth().Gap(8).Children(summaryBody)
		}
		if s.busy {
			s.workFeedback(c)
		} else if s.message != "" {
			color := t.TextMuted
			role := ui.RoleStatus
			bg, border := tonalWellSurface(c), tonalWellBorder(c)
			if s.failed {
				color, role = t.Danger, ui.RoleAlert
				bg, border = statusTintSurface(c, t.Danger), statusTintBorder(c, t.Danger)
			}
			ui.Row(c).FillWidth().Padding(8, 10).Gap(8).Radius(innerRadius(c)).
				Background(bg).Border(1, border).AlignItems(ui.Center).Children(func() {
				ui.Text(c, s.message).FontSize(12).TextColor(color).Role(role).
					Width(0).Grow(1).MaxLines(3).Tooltip(s.message)
				if !s.failed && s.lastSavedPath != "" {
					quietButton(c, "reveal-saved", "定位文件", 28, s.modalOpen()).Padding(0, 8).FontSize(12).
						Tooltip("在文件夹中显示输出文件").
						OnClick(func() { s.revealFileInFolder(s.lastSavedPath) })
				}
			})
		}
		if s.result != nil {
			ui.Box(c).FillWidth().Height(1).Background(hairlineDivider(c))
			ui.Column(c).FillWidth().Gap(6).Children(func() {
				label, hint := s.saveActionDescription()
				replaces := s.singleReplacesOriginal()
				disabled := s.modalOpen() || (replaces && !s.canOverwrite())
				var save ui.Element
				if replaces {
					save = destructiveAction(c, "save", label, 36, disabled)
				} else {
					save = primaryAction(c, "save", label, disabled).Height(36)
				}
				save.FillWidth().KeyShortcuts(ui.Cmd, ui.KeyS).
					Tooltip(hint + " · " + shortcutLabel("S")).OnClick(s.savePresetResult)
				ui.Row(c).FillWidth().Gap(6).Children(func() {
					quietButton(c, "save-copy", "另存副本", 32, s.modalOpen()).Width(0).Grow(1).
						OnClick(s.saveCopy)
					quietButton(c, "copy-result", "复制图片", 32, !s.canCopyResult()).Width(0).Grow(1).
						KeyShortcuts(ui.Cmd, ui.KeyC).
						Tooltip("将压缩结果复制到剪贴板，可直接粘贴图片 · " + shortcutLabel("C")).OnClick(s.copyResult)
				})
				dragExportControl(c, "drag-out-single", "拖出压缩图", "拖出压缩图",
					"按住拖动压缩后的图片到 Finder、聊天窗口或其他应用",
					30, 12, 500, s.modalOpen(), s.singleDragExportData, s.dragExportOptions()).FillWidth()
			})
		}
	})
}

func (s *application) workFeedback(c *ui.Context) {
	batchRunning := s.batch != nil && s.batch.running
	ui.Column(c).FillWidth().Padding(10, 12).Gap(8).Radius(innerRadius(c)).
		Background(tonalWellSurface(c)).Border(1, tonalWellBorder(c)).Children(func() {
		if batchRunning && s.batch.total > 0 {
			done, total := min(s.batch.done, s.batch.total), s.batch.total
			progress := float64(done) / float64(total)
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "本次进度").FontSize(12).TextColor(metadataColor(c)).Grow(1)
				ui.Textf(c, "%d / %d · %.0f%%", done, total, progress*100).
					FontSize(12).FontWeight(500).FontFeatures("tnum").SingleLine()
			})
			ui.Progress(c, progress).Label("批量压缩进度").FillWidth()
		}
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c).Size(15, 15).Shrink(0)
			ui.Text(c, s.phase).FontSize(12).Role(ui.RoleStatus).Width(0).Grow(1).
				MaxLines(3).Tooltip(s.phase)
		})
		if s.cancel != nil {
			label := "取消操作"
			if s.canceling {
				label = "正在取消…"
			}
			quietButton(c, "cancel", label, 30, s.canceling).FillWidth().
				Description("等待当前编解码步骤结束后停止").OnClick(s.cancelWork)
			if batchRunning {
				ui.Text(c, "已完成项会保留，可继续剩余图片").FontSize(11).
					TextColor(metadataColor(c)).MaxLines(2)
			}
		}
		if len(s.dropQueue) > 0 {
			queuedNote := fmt.Sprintf("已暂存 %d 次拖入，当前操作结束后入队", len(s.dropQueue))
			if s.dropFailed && s.dropMessage != "" {
				queuedNote = s.dropMessage
			}
			ui.Text(c, queuedNote).FontSize(11).TextColor(metadataColor(c)).MaxLines(2)
		}
	})
}

func (s *application) activePresetSnapshot() compressionPreset {
	return compressionPreset{
		name:                     s.scheme,
		quality:                  s.quality,
		png:                      s.png,
		lossless:                 s.lossless,
		metadata:                 s.metadata,
		outputFormat:             s.outputFormat,
		maxDimension:             s.maxDimension,
		maxWidth:                 s.maxWidth,
		maxHeight:                s.maxHeight,
		scalePercent:             s.scalePercent,
		targetSizeBytes:          s.targetSizeBytes,
		targetResizeMinDimension: s.targetResizeMinDimension,
		output:                   s.output,
	}
}

func (s *application) singleReplacesOriginal() bool {
	sourceFormat := ""
	if s.file != nil {
		sourceFormat = s.file.format
	}
	targetFormat := s.outputFormat
	if s.result != nil && s.result.Format != "" && sourceFormat != "" && s.result.Format != sourceFormat {
		targetFormat = compress.OutputFormat(s.result.Format)
	}
	return s.output.replacesFile(sourceFormat, targetFormat)
}

func quickDimensionLabel(limits resizeLimits) string {
	mode, value := limits.selection()
	switch mode {
	case resizeLongEdge:
		return fmt.Sprintf("≤ %d", value)
	case resizeWidth:
		return fmt.Sprintf("宽 ≤ %d", value)
	case resizeHeight:
		return fmt.Sprintf("高 ≤ %d", value)
	case resizePercent:
		return fmt.Sprintf("%d%%", value)
	default:
		return "原尺寸"
	}
}

func outputPreviewMetadata(result *compress.Result) string {
	return fmt.Sprintf("输出 %s · %d × %d · %s", strings.ToUpper(result.Format), result.Width, result.Height, formatSize(result.OutputSize))
}

func sizeGrowthHint(sourceFormat, resultFormat string) string {
	switch {
	case resultFormat == "png" && sourceFormat != "" && sourceFormat != "png":
		return "转为 PNG 易增大复杂图体积，可改用 WebP、JPEG 或保持原格式"
	case resultFormat != "" && sourceFormat != "" && resultFormat != sourceFormat:
		return "跨格式转换增大了体积，可尝试保持原格式或调低质量"
	case compress.SupportsAVIF():
		return "当前参数使体积增大，可尝试调低质量或换用 WebP / AVIF"
	default:
		return "当前参数使体积增大，可尝试调低质量或换用 WebP"
	}
}

func batchGrowthHint(targetFormat compress.OutputFormat) string {
	switch targetFormat {
	case compress.OutputFormatPNG:
		return "批量转为 PNG 易增大体积，可改用 WebP、JPEG 或保持原格式"
	case compress.OutputFormatKeep:
		return "本次批量输出总体积增大，可尝试调低质量或调整输出格式"
	default:
		return "跨格式转换使总体积增大，可尝试保持原格式或调低质量"
	}
}

func quickPickerButton(
	c *ui.Context,
	key, label, display, tooltip string,
	fontSize float32,
	fontWeight int,
	disabled, highlighted bool,
	buildMenu func(*ui.Menu),
) ui.Element {
	t := c.Theme()
	picker := ui.ButtonBase(c.Key(key))
	bg := glassControlSurface(c)
	border := controlBorder(c)
	if highlighted && !disabled {
		bg = glassSelectionBackground(c)
		border = selectionBorder(c)
	}
	picker.PopoverMenuButton(buildMenu, ui.PopoverMenuButtonOptions{
		At: ui.AnchorBottomStart, Self: ui.AnchorTopStart,
		Panel: func(panel ui.Element) {
			stylePopoverPanel(panel, c).MinWidth(max(148, picker.Bounds().W)).MaxWidth(280)
		},
	}).Label(label).Value(display).Tooltip(tooltip).
		Height(30).Padding(0, 10).Gap(6).
		AlignItems(ui.Center).Radius(innerRadius(c)).
		Background(bg).Border(1, border).Disabled(disabled)
	if !disabled {
		if picker.Pressed() {
			picker.Background(glassControlPressed(c)).Border(1, controlHoverBorder(c))
		} else if picker.Hovered() {
			picker.Background(glassControlHover(c)).Border(1, controlHoverBorder(c))
		}
	}
	picker.Children(func() {
		ui.Text(c, display).FontSize(fontSize).FontWeight(fontWeight).FontFeatures("tnum").
			SingleLine().TruncateMiddle().Width(0).Grow(1).MinWidth(0)
		ui.Icon(c, settingsChevronIcon).Size(14, 14).Shrink(0).TextColor(t.TextMuted)
	})
	return picker
}

func (s *application) presetOverview(c *ui.Context) {
	t := c.Theme()
	active := s.activePresetSnapshot()
	saved, hasSaved := s.activeSavedPreset()
	overridden := s.hasQuickOverrides()
	disabled := s.modalOpen()
	ui.Column(c).Gap(10).Children(func() {
		ui.Row(c).MinHeight(30).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "压缩概览").FontSize(14).FontWeight(600).Width(0).Grow(1).SingleLine()
			if overridden {
				accentCapsule(c, "已微调").
					Tooltip("已临时调整本次压缩参数，不会修改预设本身")
			}
			if s.batch != nil && s.batch.previewOpen {
				accentCapsule(c, "单张预览")
			}
		})
		ui.Column(c).FillWidth().Gap(8).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "预设").FontSize(12).TextColor(metadataColor(c)).Width(32).Shrink(0)
				ui.Row(c).Width(0).Grow(1).MinWidth(0).Gap(6).AlignItems(ui.Center).Children(func() {
					quickPickerButton(c, "quick-preset-picker", "快速切换预设", s.scheme, "快速切换预设："+s.scheme,
						13, 500, disabled, false, func(menu *ui.Menu) {
							menu.MaxWidth(280)
							for _, option := range s.presets {
								name := presetDisplayName(option)
								if menu.Item(name).Radio(option.name == s.scheme).Tooltip(name).Disabled(disabled).Chosen() {
									s.setScheme(option.name)
								}
							}
						}).Width(0).Grow(1).MinWidth(0)
					quietButton(c, "manage-preset", "编辑", 30, disabled).Padding(0, 10).
						Label("管理预设").Tooltip("编辑当前预设的质量和保存方式").
						OnClick(s.openPresetSettings)
				})
			})
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "质量").FontSize(12).TextColor(metadataColor(c)).Width(32).Shrink(0)
				level := qualityLevel(active)
				exactLevel := !s.lossless && s.quality == imageQualityLevels[level].quality && s.png == imageQualityLevels[level].png
				qualityTweaked := hasSaved && (s.quality != saved.quality || s.png != saved.png || s.lossless != saved.lossless)
				display := presetQualityLabel(active)
				quickPickerButton(c, "quick-quality-picker", "快速调整质量", display,
					"临时调整本次图片质量，不修改预设", 12, 500, disabled, qualityTweaked, func(menu *ui.Menu) {
						menu.MaxWidth(220)
						for index, option := range imageQualityLevels {
							itemLabel := fmt.Sprintf("%d · %s", index, option.label)
							if menu.Item(itemLabel).Radio(exactLevel && index == level).Disabled(disabled).Chosen() {
								s.quickSetQualityLevel(index)
							}
						}
						menu.Separator()
						if menu.Item("无损压缩").Radio(s.lossless).Disabled(disabled).Chosen() {
							s.quickSetLossless()
						}
					}).Width(0).Grow(1).MinWidth(0)
			})
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "输出").FontSize(12).TextColor(metadataColor(c)).Width(32).Shrink(0)
				ui.Row(c).Width(0).Grow(1).MinWidth(0).Gap(6).AlignItems(ui.Center).Children(func() {
					formatTweaked := hasSaved && s.outputFormat != saved.outputFormat
					formats := []compress.OutputFormat{
						compress.OutputFormatKeep, compress.OutputFormatJPEG, compress.OutputFormatPNG, compress.OutputFormatWebP,
					}
					if compress.SupportsAVIF() || s.outputFormat == compress.OutputFormatAVIF {
						formats = append(formats, compress.OutputFormatAVIF)
					}
					formatDisplay := presetOutputFormatLabel(s.outputFormat)
					quickPickerButton(c, "quick-format-picker", "快速切换输出格式", formatDisplay,
						"临时修改本次输出格式，不修改预设", 12, 500, disabled, formatTweaked, func(menu *ui.Menu) {
							menu.MaxWidth(200)
							for _, format := range formats {
								if menu.Item(presetOutputFormatLabel(format)).Radio(s.outputFormat == format).Disabled(disabled).Chosen() {
									s.quickSetOutputFormat(format)
								}
							}
						}).Width(0).Grow(1.1).MinWidth(0)

					dimTweaked := hasSaved && s.resizeLimits != saved.resizeLimits()
					dimDisplay := quickDimensionLabel(s.resizeLimits)
					quickPickerButton(c, "quick-dimension-picker", "快速切换尺寸缩放", dimDisplay,
						"临时调整本次输出尺寸 · "+presetResizeLabel(s.resizeLimits), 12, 500, disabled, dimTweaked, func(menu *ui.Menu) {
							menu.MaxWidth(200)
							for _, option := range resizeDimensionPresets {
								limits := resizeLimits{maxDimension: option.value}
								if menu.Item(presetResizeLabel(limits)).Radio(s.resizeLimits == limits).Disabled(disabled).Chosen() {
									s.quickSetMaxDimension(option.value)
								}
							}
							menu.Separator()
							for _, percent := range [...]int{75, 50, 25} {
								limits := resizeLimits{scalePercent: percent}
								if menu.Item(presetResizeLabel(limits)).Radio(s.resizeLimits == limits).Disabled(disabled).Chosen() {
									s.quickSetResize(limits)
								}
							}
							menu.Separator()
							if menu.Item("编辑预设缩放…").Disabled(disabled).Chosen() {
								s.openPresetSettings()
								if s.editor != nil {
									s.editor.focusField = presetResizeModeField
								}
							}
						}).Width(0).Grow(0.9).MinWidth(0)
				})
			})
			if summary := presetProcessingSummary(active); summary != "" {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(2, 0, 0, 0).Children(func() {
					ui.Text(c, "选项").FontSize(12).TextColor(metadataColor(c)).Width(32).Shrink(0)
					ui.Text(c, summary).FontSize(11).FontFeatures("tnum").TextColor(metadataColor(c)).
						Width(0).Grow(1).SingleLine().TruncateMiddle().Tooltip(summary)
				})
			}
		})
		if overridden {
			ui.Row(c).Padding(6, 10).Gap(6).Radius(innerRadius(c)).
				Background(glassSelectionBackground(c)).Border(1, statusTintBorder(c, accentForeground(t))).
				AlignItems(ui.Center).Children(func() {
				ui.Text(c, "临时生效，未改动预设").FontSize(11).FontWeight(500).TextColor(accentForeground(t)).
					Width(0).Grow(1).MinWidth(0).SingleLine().TruncateMiddle().
					Tooltip("已临时调整本次压缩参数，不会修改预设本身")
				quietButton(c, "quick-reset-overrides", "重置", 24, disabled).FontSize(11).Padding(0, 8).
					Label("恢复预设参数").Tooltip("放弃临时微调，恢复当前预设参数").
					OnClick(s.resetQuickOverrides)
				quietButton(c, "quick-save-preset", "存为预设", 24, disabled).FontSize(11).Padding(0, 8).
					Label("将微调存为新预设").Tooltip("打开设置并将当前临时参数保存为新预设").
					OnClick(s.openSaveQuickPreset)
			})
		}
	})
}

func (s *application) singleOutputSummary(c *ui.Context) {
	t := c.Theme()
	replaces := s.singleReplacesOriginal()
	sourceFormat := ""
	name := "example.png"
	if s.file != nil {
		sourceFormat = s.file.format
		name = filepath.Base(s.file.path)
	}
	resolvedFmt := compress.ResolveOutputFormat(sourceFormat, s.outputFormat, s.lossless)
	if s.result != nil && s.result.Format != "" {
		resolvedFmt = s.result.Format
	}
	wellBg := tonalWellSurface(c)
	wellBorder := tonalWellBorder(c)
	if replaces {
		wellBg = statusTintSurface(c, t.Danger)
		wellBorder = statusTintBorder(c, t.Danger)
	}
	ui.Column(c).Padding(10, 12).Gap(4).Radius(innerRadius(c)).Background(wellBg).Border(1, wellBorder).Children(func() {
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "保存方式").FontSize(11).FontWeight(600).TextColor(metadataColor(c)).Grow(1)
			if replaces {
				statusCapsule(c, "覆盖原图", t.Danger)
			}
		})
		description, color := presetSaveDescription(s.output), t.Text
		if replaces {
			color = t.Danger
		} else if s.output.replacesOriginal() && resolvedFmt != "" && resolvedFmt != sourceFormat {
			if ext := outputExtensionForFormat(resolvedFmt); ext != "" {
				description = "在原文件夹保存 " + ext + " 副本"
			}
		}
		ui.Text(c, description).FontSize(13).FontWeight(500).TextColor(color).MaxLines(2).Tooltip(description)
		previewFormat := s.outputFormat
		if s.result != nil && s.result.Format != "" {
			previewFormat = compress.OutputFormat(s.result.Format)
		}
		outputFilenamePreviewForResolved(c, s.output, name, sourceFormat, previewFormat, s.lossless, replaces)
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
	if s.backupToTrash {
		message = fmt.Sprintf("将先将原文件移入废纸篓，再用压缩结果替换：\n%s\n\n%s → %s。",
			s.file.path, formatSize(s.result.OriginalSize), formatSize(s.result.OutputSize))
	}
	if s.result.TargetResized {
		message += fmt.Sprintf("\n\n本次自动缩小为 %d × %d px。", s.result.Width, s.result.Height)
	}
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
	statusDetail, note, hint := "", "", ""
	ratio := -1.0
	if s.file != nil {
		original = formatSize(s.file.size)
	}
	if s.busy && s.result == nil {
		label, color = "压缩中…", accentForeground(t)
	}
	if s.result != nil {
		original = formatSize(s.result.OriginalSize)
		output = formatSize(s.result.OutputSize)
		if s.result.OriginalSize > 0 {
			ratio = max(0, float64(s.result.OutputSize)/float64(s.result.OriginalSize))
		}
		switch {
		case s.result.Unchanged:
			label, color = "未减小（0.0%）", t.TextMuted
			ratio = 1
			note = "已保留原始数据"
		case s.result.OutputSize > s.result.OriginalSize && s.result.OriginalSize > 0:
			grew := float64(s.result.OutputSize-s.result.OriginalSize) / float64(s.result.OriginalSize) * 100
			label, color = fmt.Sprintf("增大 %.1f%%", grew), t.Warning
			statusDetail = "+" + formatSize(s.result.OutputSize-s.result.OriginalSize)
			sourceFormat := ""
			if s.file != nil {
				sourceFormat = s.file.format
			}
			hint = sizeGrowthHint(sourceFormat, s.result.Format)
		case s.result.OriginalSize > 0:
			saved := float64(s.result.OriginalSize-s.result.OutputSize) / float64(s.result.OriginalSize) * 100
			label, color = fmt.Sprintf("减少 %.1f%%", saved), t.Success
			statusDetail = "节省 " + formatSize(s.result.OriginalSize-s.result.OutputSize)
		}
		var notes []string
		if note != "" {
			notes = append(notes, note)
		}
		if s.file != nil && s.result.Format != "" && s.result.Format != s.file.format {
			notes = append(notes, "转为 "+strings.ToUpper(s.result.Format))
		}
		if s.result.TargetResized {
			notes = append(notes, fmt.Sprintf("已自动缩小至 %d × %d", s.result.Width, s.result.Height))
		} else if s.file != nil && s.result.Width > 0 && s.result.Height > 0 &&
			(s.result.Width != s.file.width || s.result.Height != s.file.height) {
			notes = append(notes, fmt.Sprintf("%d × %d → %d × %d", s.file.width, s.file.height, s.result.Width, s.result.Height))
		}
		if s.result.TargetSizeMissed {
			notes = append(notes, fmt.Sprintf("未达目标 ≤ %s", compress.FormatTargetSize(s.result.TargetSizeBytes)))
		} else if s.result.AppliedQuality > 0 {
			notes = append(notes, fmt.Sprintf("自动质量 %d", s.result.AppliedQuality))
		}
		note = strings.Join(notes, " · ")
	}
	if s.batch != nil && s.batch.previewOpen && s.result == nil {
		if index := s.batch.previewIndex; index >= 0 && index < len(s.batch.items) && s.batch.items[index].status == batchFailed {
			label, color = "压缩失败", t.Danger
		}
	}
	summaryCardLayout(c, title, label, statusDetail, color, "原始大小", original, "输出大小", output, note, hint, ratio, nil)
}

func (s *application) saveActionDescription() (string, string) {
	if s.singleReplacesOriginal() {
		if s.overwritten {
			return "已覆盖原图", "此结果已经写入原文件，可另存副本或复制图片"
		}
		if s.result != nil && s.result.Unchanged {
			return "无需覆盖（未减小）", "已保留原始数据，可另存副本或复制图片"
		}
		if s.result != nil && s.result.OutputSize >= s.result.OriginalSize {
			return "无需覆盖（未减小）", "输出体积未小于原图，可另存副本或复制图片"
		}
		if s.backupToTrash {
			return "覆盖原图…", "确认后先将原文件移入废纸篓并替换"
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
	outputFilenamePreviewForFormat(c, options, filename, compress.OutputFormatKeep, options.replacesOriginal())
}

func outputFilenamePreviewForFormat(c *ui.Context, options presetSaveOptions, filename string, targetFormat compress.OutputFormat, replaces bool) {
	outputFilenamePreviewForResolved(c, options, filename, "", targetFormat, false, replaces)
}

func outputFilenamePreviewForResolved(c *ui.Context, options presetSaveOptions, filename, sourceFormat string, targetFormat compress.OutputFormat, lossless, replaces bool) {
	if replaces {
		return
	}
	example := presetOutputNameForResolved(options, filename, sourceFormat, targetFormat, lossless)
	ui.Box(c).FillWidth().Height(1).Background(subtleRowDivider(c))
	ui.Column(c).Gap(2).Children(func() {
		ui.Text(c, "文件示例").FontSize(11).TextColor(metadataColor(c))
		ui.Text(c, example).FontSize(12).FontFeatures("tnum").SingleLine().TruncateMiddle().
			FillWidth().Label("输出文件示例：" + example).
			Tooltip(example + "\n已有同名文件时自动使用新名称")
	})
}

func summaryCard(c *ui.Context, title, status string, statusColor ui.Color, firstLabel, firstValue, secondLabel, secondValue string) {
	summaryCardWithNote(c, title, status, statusColor, firstLabel, firstValue, secondLabel, secondValue, "", -1)
}

func summaryCardWithNote(c *ui.Context, title, status string, statusColor ui.Color, firstLabel, firstValue, secondLabel, secondValue, note string, ratio float64) {
	summaryCardWithHint(c, title, status, statusColor, firstLabel, firstValue, secondLabel, secondValue, note, "", ratio, nil)
}

func summaryCardWithHint(c *ui.Context, title, status string, statusColor ui.Color, firstLabel, firstValue, secondLabel, secondValue, note, hint string, ratio float64, footer func()) {
	summaryCardLayout(c, title, status, "", statusColor, firstLabel, firstValue, secondLabel, secondValue, note, hint, ratio, footer)
}

func summaryCardLayout(c *ui.Context, title, status, statusDetail string, statusColor ui.Color, firstLabel, firstValue, secondLabel, secondValue, note, hint string, ratio float64, footer func()) {
	t := c.Theme()
	width, _ := c.Size()
	compact := width >= 720 && width < 960
	ui.Column(c).Padding(11, 14).Gap(8).Radius(innerRadius(c)).
		Background(tonalWellSurface(c)).Border(1, tonalWellBorder(c)).Children(func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, title).FontSize(11).FontWeight(600).TextColor(metadataColor(c)).Grow(1).SingleLine()
			if statusColor == t.Success || statusColor == t.Warning || statusColor == t.Danger {
				statusCapsule(c, status, statusColor).Role(ui.RoleStatus).
					MaxLines(2).Tooltip(status)
			} else if status != "" {
				neutralCapsule(c, status).Role(ui.RoleStatus).Tooltip(status)
			}
		})
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.End).Children(func() {
			firstSize, secondSize := float32(14), float32(19)
			if compact {
				firstSize, secondSize = 13, 17
			}
			ui.Column(c).Gap(3).Shrink(0).Children(func() {
				ui.Text(c, firstLabel).FontSize(11).TextColor(metadataColor(c)).SingleLine()
				ui.Text(c, firstValue).FontSize(firstSize).FontWeight(500).FontFeatures("tnum").
					TextColor(t.TextMuted).SingleLine().Padding(0, 0, 2, 0).Tooltip(firstValue)
			})
			ui.Text(c, "→").FontSize(12).TextColor(metadataColor(c)).SingleLine().Shrink(0).Padding(0, 0, 3, 0)
			ui.Column(c).Width(0).Grow(1).MinWidth(0).Gap(3).Children(func() {
				ui.Text(c, secondLabel).FontSize(11).TextColor(metadataColor(c)).SingleLine()
				ui.Text(c, secondValue).FontSize(secondSize).FontWeight(700).FontFeatures("tnum").
					TextColor(t.Text).SingleLine().TruncateMiddle().Tooltip(secondValue)
			})
		})
		if ratio >= 0 || statusDetail != "" || note != "" {
			ui.Column(c).FillWidth().Gap(6).Children(func() {
				if ratio >= 0 {
					barColor := t.Success
					fillRatio := max(0, 1-ratio)
					tooltip := fmt.Sprintf("已节省 %.1f%%（输出体积为原始体积的 %.1f%%）", fillRatio*100, ratio*100)
					switch {
					case ratio > 1:
						barColor = t.Warning
						fillRatio = 1
						tooltip = fmt.Sprintf("输出体积为原始体积的 %.1f%%", ratio*100)
					case ratio >= 1:
						barColor = t.TextMuted.Alpha(0.45)
						fillRatio = 1
						tooltip = "输出体积未减小（100.0%）"
					}
					ui.Box(c).FillWidth().Height(5).Tooltip(tooltip).
						Draw(func(p *ui.Painter, r ui.Rect) {
							p.Fill(r, meterTrackColor(c), 2.5)
							fillW := max(5, r.W*float32(min(1, fillRatio)))
							p.Fill(ui.Rect{X: r.X, Y: r.Y, W: fillW, H: r.H}, barColor, 2.5)
						})
				}
				if statusDetail != "" {
					detailColor := metadataColor(c)
					if statusColor == t.Success {
						detailColor = t.TextMuted
					}
					ui.Text(c, statusDetail).FontSize(12).FontWeight(500).FontFeatures("tnum").
						TextColor(detailColor).FillWidth().SingleLine().TruncateMiddle().Tooltip(statusDetail)
				}
				if note != "" {
					ui.Text(c, note).FontSize(12).FontFeatures("tnum").TextColor(metadataColor(c)).
						MaxLines(2).Tooltip(note)
				}
			})
		}
		if hint != "" {
			ui.Text(c, hint).FontSize(12).TextColor(t.Warning).
				MaxLines(2).Tooltip(hint)
		}
		if footer != nil {
			footer()
		}
	})
}
