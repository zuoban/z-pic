package main

import (
	"context"
	"fmt"
	"math"

	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"

	"zpic/internal/compress"
)

const (
	previewCompare = iota
	previewOriginal
	previewResult
	previewDiff
)

const comparisonDividerHitHalfWidth = 20

type comparisonView struct {
	mode               int
	split              float64
	scale              float64
	offset             ui.Point
	viewport           ui.Rect
	viewportProbed     bool
	panning            bool
	panSuppressSlider  bool
	panFrozenSplit     float64
	preClickSplit      float64
	hasPreClickSplit   bool
	pendingDoubleClick bool
	doubleClickOrigin  ui.Point
	panPointer         ui.Point

	diffBitmap  *ui.Bitmap
	diffMetrics compress.QualityMetrics
	diffReady   bool
	diffLoading bool
	diffError   string
	diffGen     uint64
	diffCancel  context.CancelFunc
}

func (s *application) cancelComparisonDiff() {
	if s.comparison.diffCancel != nil {
		s.comparison.diffCancel()
		s.comparison.diffCancel = nil
	}
	s.comparison.diffLoading = false
}

func (s *application) clearComparisonDiff() {
	s.cancelComparisonDiff()
	s.comparison.diffGen++
	s.comparison.diffBitmap = nil
	s.comparison.diffMetrics = compress.QualityMetrics{}
	s.comparison.diffReady = false
	s.comparison.diffError = ""
}

func (s *application) retryComparisonDiff() {
	if s.comparison.diffLoading {
		return
	}
	s.clearComparisonDiff()
	s.ensureComparisonDiff()
}

func (s *application) resetComparison() {
	s.cancelComparisonDiff()
	nextGen := s.comparison.diffGen + 1
	s.comparison = comparisonView{split: 50, diffGen: nextGen}
}

func (s *application) ensureComparisonDiff() {
	if s.file == nil || s.result == nil || s.overwritten ||
		s.comparison.diffReady || s.comparison.diffLoading || s.comparison.diffError != "" {
		return
	}
	origData := s.file.data
	compData := s.result.Data
	if len(compData) == 0 && s.result.Unchanged {
		compData = origData
	}
	if len(origData) == 0 || len(compData) == 0 {
		if s.result.Unchanged {
			s.comparison.diffMetrics = compress.QualityMetrics{
				PSNR:      math.Inf(1),
				SSIM:      1,
				Identical: true,
			}
			s.comparison.diffReady = true
		}
		return
	}
	s.comparison.diffGen++
	gen := s.comparison.diffGen
	ctx, cancel := context.WithCancel(context.Background())
	s.comparison.diffCancel = cancel
	s.comparison.diffLoading = true
	info := compress.ImageInfo{
		Path:   s.file.path,
		Format: s.file.format,
		Width:  s.file.width,
		Height: s.file.height,
		Size:   s.file.size,
	}
	go func() {
		defer cancel()
		release, memErr := s.acquirePreviewMemory(ctx, info)
		var (
			metrics compress.QualityMetrics
			bitmap  *ui.Bitmap
			err     error
		)
		if memErr != nil {
			err = memErr
		} else {
			m, heatmap, analyzeErr := compress.AnalyzeVisualDiffBytes(ctx, origData, compData)
			metrics, err = m, analyzeErr
			if err == nil && heatmap != nil && ctx.Err() == nil {
				bitmap = ui.NewBitmap(heatmap)
			}
			release()
		}
		if err == nil {
			err = ctx.Err()
		}
		s.updateWithInvalidate(false, func() {
			if s.comparison.diffGen != gen {
				return
			}
			s.comparison.diffLoading = false
			s.comparison.diffCancel = nil
			if err != nil {
				if ctx.Err() == nil {
					s.comparison.diffError = err.Error()
				}
				return
			}
			s.comparison.diffMetrics = metrics
			s.comparison.diffBitmap = bitmap
			s.comparison.diffReady = true
		})
	}()
}

func formatQualityMetricsSummary(m compress.QualityMetrics) string {
	if m.Identical {
		return "无损一致 · SSIM 1.000 · PSNR ∞"
	}
	psnr := "∞"
	if !math.IsInf(m.PSNR, 0) && !math.IsNaN(m.PSNR) {
		psnr = fmt.Sprintf("%.1f dB", m.PSNR)
	}
	if m.Resized {
		return fmt.Sprintf("已缩放 · SSIM %.3f · PSNR %s", m.SSIM, psnr)
	}
	return fmt.Sprintf("SSIM %.3f · PSNR %s · 变化 %.1f%%", m.SSIM, psnr, m.ChangedPercent)
}

func formatQualityMetricsTooltip(m compress.QualityMetrics) string {
	if m.Identical {
		return "所有像素与原图完全一致（SSIM 1.000，PSNR ∞）"
	}
	psnr := "∞"
	if !math.IsInf(m.PSNR, 0) && !math.IsNaN(m.PSNR) {
		psnr = fmt.Sprintf("%.2f dB", m.PSNR)
	}
	prefix := ""
	if m.Resized {
		prefix = "已按输出尺寸对齐原图 · "
	}
	return fmt.Sprintf("%sSSIM %.4f · PSNR %s · 变化像素 %.1f%% · 最大通道差 ±%d",
		prefix, m.SSIM, psnr, m.ChangedPercent, m.MaxChannelDiff)
}

func (s *application) previewLocked() bool {
	return s.modalOpen()
}

func (s *application) comparing() bool {
	return s.file != nil && s.result != nil && !s.overwritten &&
		s.comparison.mode == previewCompare
}

func (s *application) previewScale(bounds ui.Rect) float64 {
	if s.comparison.scale > 0 {
		return s.comparison.scale
	}
	if s.file == nil || s.file.width <= 0 || s.file.height <= 0 || bounds.W <= 0 || bounds.H <= 0 {
		return 1
	}
	return min(float64(bounds.W)/float64(s.file.width), float64(bounds.H)/float64(s.file.height))
}

func (s *application) previewCanPan(bounds ui.Rect) bool {
	if s.file == nil || bounds.W <= 0 || bounds.H <= 0 {
		return false
	}
	scale := float32(s.previewScale(bounds))
	return float32(s.file.width)*scale > bounds.W+0.5 ||
		float32(s.file.height)*scale > bounds.H+0.5
}

func (s *application) nearComparisonDivider(width, localX float32) bool {
	if width <= 0 {
		return true
	}
	divider := width * float32(s.comparison.split/100)
	handleX := min(width-16, max(16, divider))
	return math.Abs(float64(localX-divider)) <= comparisonDividerHitHalfWidth ||
		math.Abs(float64(localX-handleX)) <= comparisonDividerHitHalfWidth
}

func (s *application) previewImageRect(bounds ui.Rect) ui.Rect {
	scale := float32(s.previewScale(bounds))
	width, height := float32(s.file.width)*scale, float32(s.file.height)*scale
	limitX, limitY := max(0, (width-bounds.W)/2), max(0, (height-bounds.H)/2)
	return ui.Rect{
		X: bounds.X + (bounds.W-width)/2 + min(limitX, max(-limitX, s.comparison.offset.X)),
		Y: bounds.Y + (bounds.H-height)/2 + min(limitY, max(-limitY, s.comparison.offset.Y)),
		W: width, H: height,
	}
}

func (s *application) clampPreviewOffset() {
	if s.file == nil {
		return
	}
	bounds := s.comparison.viewport
	scale := float32(s.previewScale(bounds))
	limitX := max(0, (float32(s.file.width)*scale-bounds.W)/2)
	limitY := max(0, (float32(s.file.height)*scale-bounds.H)/2)
	s.comparison.offset.X = min(limitX, max(-limitX, s.comparison.offset.X))
	s.comparison.offset.Y = min(limitY, max(-limitY, s.comparison.offset.Y))
}

func (s *application) zoomPreview(scale float64, anchor ui.Point) {
	if s.previewLocked() || s.file == nil || math.IsNaN(scale) || math.IsInf(scale, 0) || scale <= 0 {
		return
	}
	bounds := s.comparison.viewport
	previous := s.previewScale(bounds)
	scale = min(16, max(0.01, scale))
	ratio := float32(scale / previous)
	s.comparison.offset.X = anchor.X - bounds.W/2 - (anchor.X-bounds.W/2-s.comparison.offset.X)*ratio
	s.comparison.offset.Y = anchor.Y - bounds.H/2 - (anchor.Y-bounds.H/2-s.comparison.offset.Y)*ratio
	s.comparison.scale = scale
	s.clampPreviewOffset()
}

func (s *application) previewViewportCenter() ui.Point {
	return ui.Point{X: s.comparison.viewport.W / 2, Y: s.comparison.viewport.H / 2}
}

func (s *application) zoomPreviewBy(factor float64) {
	if s.previewLocked() || s.preview == nil || s.file == nil {
		return
	}
	s.zoomPreview(s.previewScale(s.comparison.viewport)*factor, s.previewViewportCenter())
}

func (s *application) resetPreviewFit() {
	if s.previewLocked() || s.preview == nil {
		return
	}
	s.comparison.scale, s.comparison.offset = 0, ui.Point{}
	s.comparison.panning, s.comparison.panSuppressSlider = false, false
}

func (s *application) zoomPreviewActual() {
	if s.previewLocked() || s.preview == nil || s.file == nil {
		return
	}
	s.zoomPreview(1, s.previewViewportCenter())
}

func (s *application) previewToolbar(c *ui.Context) {
	s.previewUnifiedBar(c)
}

func (s *application) previewUnifiedBar(c *ui.Context) {
	hasModes := s.result != nil && !s.overwritten
	ui.Row(c).FillWidth().MinHeight(32).Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
		if hasModes {
			s.previewModeControl(c)
		}
		ui.Box(c).Grow(1)
		s.previewZoomControls(c)
	})
}

func (s *application) previewModeControl(c *ui.Context) {
	if s.result == nil || s.overwritten {
		return
	}
	segmentedControl(c, "preview-mode", "预览模式", &s.comparison.mode, 30, s.previewLocked(), "对比", "原图", "结果", "差异").
		FillEqually().Width(196)
	if s.comparison.mode == previewDiff {
		s.ensureComparisonDiff()
	}
}

func (s *application) previewZoomToolbar(c *ui.Context) {
	ui.Row(c).FillWidth().MinHeight(30).Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
		ui.Box(c).Grow(1)
		s.previewZoomControls(c)
	})
}

func (s *application) previewZoomControls(c *ui.Context) {
	t := c.Theme()
	locked := s.previewLocked()
	bounds := s.comparison.viewport
	scale := s.previewScale(bounds)
	ui.Row(c).MinHeight(30).Gap(6).Wrap().AlignItems(ui.Center).
		Role(ui.RoleToolbar).Label("图片缩放").Children(func() {
		zoomGroup := ui.Row(c).Height(30).Gap(0).Radius(pillRadius(30, c)).
			Background(glassControlSurface(c)).Border(1, controlBorder(c)).AlignItems(ui.Center)
		controlElevation(zoomGroup, c)
		zoomGroup.Children(func() {
			zoomBtn := func(key, label string, keyCode ui.Key, hintKey string, factor float64) {
				disabled := locked || (factor < 1 && scale <= 0.01) || (factor > 1 && scale >= 16)
				strokeColor := t.Text
				if disabled {
					strokeColor = t.TextMuted.Alpha(0.42)
				}
				button := ui.Button(c.Key(key), "").Size(28, 28).Radius(pillRadius(28, c)).
					Background(ui.Transparent).Border(0, ui.Transparent).Label(label).
					KeyShortcuts(ui.Cmd, keyCode).
					Tooltip(label+" · "+shortcutLabel(hintKey)).
					Disabled(disabled)
				if !disabled {
					if button.Pressed() {
						button.Background(glassControlPressed(c))
					} else if button.Hovered() {
						button.Background(glassControlHover(c))
					}
				}
				button.DrawOver(func(p *ui.Painter, r ui.Rect) {
					p.Line(r.X+9, r.Y+r.H/2, r.X+r.W-9, r.Y+r.H/2, 1.5, strokeColor)
					if factor > 1 {
						p.Line(r.X+r.W/2, r.Y+9, r.X+r.W/2, r.Y+r.H-9, 1.5, strokeColor)
					}
				}).OnClick(func() {
					s.zoomPreviewBy(factor)
				})
			}
			zoomBtn("zoom-out", "缩小", ui.KeyMinus, "-", 0.8)
			ui.Box(c).Size(1, 14).Background(hairlineDivider(c))
			ui.Textf(c, "%.0f%%", scale*100).Width(46).FontSize(12).FontWeight(500).
				FontFeatures("tnum").TextAlign(ui.Center).TextColor(t.TextMuted).SingleLine()
			ui.Box(c).Size(1, 14).Background(hairlineDivider(c))
			zoomBtn("zoom-in", "放大", ui.KeyEqual, "=", 1.25)
		})
		trackBg := tonalWellSurface(c)
		if !usesGlass(c) && !t.Dark {
			trackBg = ui.Hex("#e6eaf1")
		}
		trackRadius, segRadius := innerRadius(c), float32(7)
		if usesGlass(c) {
			trackRadius, segRadius = 15, 14
		}
		ui.Row(c).Height(30).Padding(1).Gap(2).Radius(trackRadius).
			Background(trackBg).Border(1, segmentedTrackBorder(c)).AlignItems(ui.Stretch).Children(func() {
			scaleOption := func(key, label, tooltip string, keyCode ui.Key, active bool, action func()) {
				btn := ui.Button(c.Key(key), label).Height(28).Padding(0, 9).Radius(segRadius).
					FontSize(12).FontFeatures("tnum").
					KeyShortcuts(ui.Cmd, keyCode).Tooltip(tooltip).
					Disabled(locked).Selected(active).OnClick(action)
				if active {
					btn.FontWeight(600).Background(segmentedThumbSurface(c)).
						Border(1, segmentedThumbBorder(c)).TextColor(t.Text)
					if !t.Dark && !c.Preferences().HighContrast {
						btn.Shadow(0, 1, 2, 0, ui.RGBA(17, 20, 26, 0.08))
					}
				} else {
					btn.FontWeight(400).Background(ui.Transparent).
						Border(1, ui.Transparent).TextColor(t.TextMuted)
					if !locked {
						if btn.Pressed() {
							btn.Background(glassControlPressed(c)).TextColor(t.Text)
						} else if btn.Hovered() {
							btn.Background(glassControlHover(c)).TextColor(t.Text)
						}
					}
				}
			}
			scaleOption("preview-fit", "适应", "完整显示图片，重置缩放和位置 · "+shortcutLabel("0"),
				ui.Key0, s.comparison.scale == 0, s.resetPreviewFit)
			scaleOption("preview-actual", "1:1", "按原始尺寸查看（100%） · "+shortcutLabel("1"),
				ui.Key1, s.comparison.scale == 1, s.zoomPreviewActual)
		})
	})
}

func supportsAlphaFormat(format string) bool {
	switch format {
	case "png", "webp", "avif", "heic":
		return true
	default:
		return false
	}
}

func drawAlphaCheckerboard(p *ui.Painter, bounds ui.Rect, dark bool) {
	if bounds.W <= 0 || bounds.H <= 0 {
		return
	}
	lightTile, darkTile := ui.Hex("#f6f7f9"), ui.Hex("#e7eaf0")
	if dark {
		lightTile, darkTile = ui.Hex("#161920"), ui.Hex("#202530")
	}
	const tile = float32(10)
	p.Fill(bounds, lightTile, 0)
	for row, y := 0, bounds.Y; y < bounds.Y+bounds.H; row, y = row+1, y+tile {
		h := min(tile, bounds.Y+bounds.H-y)
		for col, x := 0, bounds.X; x < bounds.X+bounds.W; col, x = col+1, x+tile {
			if (row+col)%2 == 0 {
				continue
			}
			w := min(tile, bounds.X+bounds.W-x)
			p.Fill(ui.Rect{X: x, Y: y, W: w, H: h}, darkTile, 0)
		}
	}
}

func (s *application) previewCanvas(c *ui.Context) {
	t := c.Theme()
	comparing := s.comparing()
	if !comparing || s.previewLocked() {
		s.comparison.panning, s.comparison.panSuppressSlider = false, false
	}
	if s.previewLocked() {
		s.comparison.pendingDoubleClick = false
	}
	var canvas ui.Element
	splitBeforeSlider := s.comparison.split
	if comparing {
		splitTarget := &s.comparison.split
		suppressSlider := s.comparison.panning || s.comparison.panSuppressSlider
		frozenSplit := s.comparison.panFrozenSplit
		if suppressSlider {
			splitTarget = &frozenSplit
		}
		canvas = ui.SliderBase(c.Key("comparison"), splitTarget, 0, 100).
			Step(1).Label("原图与压缩结果对比").
			Description("左侧原图，右侧压缩后。拖动或使用方向键调整分隔线，放大后拖动图片平移，Home 和 End 查看整张图片。")
		if suppressSlider {
			s.comparison.split = s.comparison.panFrozenSplit
			if !s.comparison.panning {
				s.comparison.panSuppressSlider = false
			}
			canvas.Range(0, 100, s.comparison.split)
		}
		cursor := ui.CursorResizeEW
		if s.comparison.panning {
			cursor = ui.CursorGrabbing
		} else if s.previewCanPan(s.comparison.viewport) {
			pointerX, _, pointerOver := canvas.PointerPosition()
			if pointerOver && !s.nearComparisonDivider(s.comparison.viewport.W, pointerX) {
				cursor = ui.CursorGrab
			}
		}
		canvas.Cursor(cursor)
	} else {
		diffMode := s.result != nil && !s.overwritten && s.comparison.mode == previewDiff
		if diffMode {
			s.ensureComparisonDiff()
		}
		label := "原图预览"
		if diffMode {
			label = "像素差异热力图"
		} else if s.result != nil && (s.overwritten || s.comparison.mode != previewOriginal) {
			label = "压缩后预览"
		}
		singleCursor := ui.CursorDefault
		if s.previewCanPan(s.comparison.viewport) {
			singleCursor = ui.CursorGrab
		}
		canvas = ui.Box(c.Key("preview-image")).Role(ui.RoleImage).Label(label).
			Focusable().Cursor(singleCursor)
		if deltaX, deltaY, held := canvas.Dragged(); held && !s.previewLocked() {
			s.comparison.offset.X += deltaX
			s.comparison.offset.Y += deltaY
			s.clampPreviewOffset()
			if s.previewCanPan(s.comparison.viewport) {
				canvas.Cursor(ui.CursorGrabbing)
			}
		}
	}
	canvas.Fill().Radius(8).Clip().FocusRingOffset(-3).
		Disabled(s.previewLocked()).DimDisabled(false)
	previousBounds := s.comparison.viewport
	s.comparison.viewport = canvas.Bounds()
	needsProbe := !s.comparison.viewportProbed && s.comparison.viewport.W <= 0
	if needsProbe {
		s.comparison.viewportProbed = true
	}
	if s.comparison.viewport != previousBounds || needsProbe {
		c.AnimationFrame()
	}
	s.clampPreviewOffset()
	canvas.HandleInput(func(event ui.InputEvent) bool {
		if s.previewLocked() {
			s.comparison.panning = false
			s.comparison.pendingDoubleClick = false
			return false
		}
		switch event.Kind {
		case ui.InputPointerDown:
			if event.Button == 0 && event.Clicks == 2 {
				s.comparison.pendingDoubleClick = true
				s.comparison.doubleClickOrigin = ui.Point{X: event.X, Y: event.Y}
			} else {
				s.comparison.pendingDoubleClick = false
				if event.Button == 0 && event.Clicks == 1 && comparing {
					s.comparison.preClickSplit = splitBeforeSlider
					s.comparison.hasPreClickSplit = true
				}
			}
			if !comparing || !s.previewCanPan(s.comparison.viewport) {
				return false
			}
			if event.Button != 0 && event.Button != 2 {
				return false
			}
			if event.Button == 0 && s.nearComparisonDivider(s.comparison.viewport.W, event.X) {
				return false
			}
			s.comparison.panning = true
			s.comparison.panSuppressSlider = event.Button == 0
			s.comparison.panFrozenSplit = s.comparison.split
			s.comparison.panPointer = ui.Point{X: event.X, Y: event.Y}
			return true
		case ui.InputPointerMove:
			if s.comparison.pendingDoubleClick &&
				math.Hypot(float64(event.X-s.comparison.doubleClickOrigin.X), float64(event.Y-s.comparison.doubleClickOrigin.Y)) > 4 {
				s.comparison.pendingDoubleClick = false
			}
			if !s.comparison.panning {
				return false
			}
			s.comparison.offset.X += event.X - s.comparison.panPointer.X
			s.comparison.offset.Y += event.Y - s.comparison.panPointer.Y
			s.comparison.panPointer = ui.Point{X: event.X, Y: event.Y}
			s.clampPreviewOffset()
			return true
		case ui.InputPointerUp:
			wasPanning := s.comparison.panning
			s.comparison.panning = false
			if s.comparison.pendingDoubleClick {
				origin := s.comparison.doubleClickOrigin
				s.comparison.pendingDoubleClick = false
				if math.Hypot(float64(event.X-origin.X), float64(event.Y-origin.Y)) <= 4 {
					frozen := splitBeforeSlider
					if s.comparison.hasPreClickSplit {
						frozen = s.comparison.preClickSplit
					}
					s.comparison.hasPreClickSplit = false
					if s.comparison.scale == 0 {
						s.zoomPreview(1, origin)
					} else {
						s.resetPreviewFit()
					}
					if comparing {
						s.comparison.split = frozen
						s.comparison.panSuppressSlider = true
						s.comparison.panFrozenSplit = frozen
					}
					return true
				}
			}
			return wasPanning
		case ui.InputZoom:
			s.zoomPreview(s.previewScale(s.comparison.viewport)*float64(event.Scale),
				ui.Point{X: event.X, Y: event.Y})
		case ui.InputScroll:
			if event.Precise {
				s.comparison.offset.X -= event.DX
				s.comparison.offset.Y -= event.DY
				s.clampPreviewOffset()
			} else {
				s.zoomPreview(s.previewScale(s.comparison.viewport)*math.Exp(-float64(event.DY)/240),
					ui.Point{X: event.X, Y: event.Y})
			}
		default:
			return false
		}
		return true
	})
	canvas.Draw(func(p *ui.Painter, bounds ui.Rect) {
		imageBounds := s.previewImageRect(bounds)
		sourceFormat := ""
		if s.file != nil {
			sourceFormat = s.file.format
		}
		resultFormat := sourceFormat
		if s.result != nil && s.result.Format != "" {
			resultFormat = s.result.Format
		}
		if !comparing {
			diffMode := s.result != nil && !s.overwritten && s.comparison.mode == previewDiff
			bitmap := s.preview
			displayFormat := resultFormat
			if diffMode && s.comparison.diffBitmap != nil {
				bitmap = s.comparison.diffBitmap
				displayFormat = ""
			} else if s.result == nil || s.overwritten || s.comparison.mode == previewOriginal {
				displayFormat = sourceFormat
			}
			if s.result != nil && !s.overwritten && s.comparison.mode == previewOriginal {
				bitmap = s.file.preview
			}
			if supportsAlphaFormat(displayFormat) {
				p.Clip(imageBounds, 0, func() {
					drawAlphaCheckerboard(p, imageBounds, t.Dark)
				})
			}
			p.Image(bitmap, imageBounds, ui.FillBox)
			p.Stroke(imageBounds, glassBorder(c), 0, 1)
			return
		}
		divider := bounds.W * float32(s.comparison.split/100)
		p.Clip(ui.Rect{X: bounds.X, Y: bounds.Y, W: divider, H: bounds.H}, 0, func() {
			if supportsAlphaFormat(sourceFormat) {
				p.Clip(imageBounds, 0, func() {
					drawAlphaCheckerboard(p, imageBounds, t.Dark)
				})
			}
			p.Image(s.file.preview, imageBounds, ui.FillBox)
		})
		p.Clip(ui.Rect{X: bounds.X + divider, Y: bounds.Y, W: bounds.W - divider, H: bounds.H}, 0, func() {
			if supportsAlphaFormat(resultFormat) {
				p.Clip(imageBounds, 0, func() {
					drawAlphaCheckerboard(p, imageBounds, t.Dark)
				})
			}
			p.Image(s.preview, imageBounds, ui.FillBox)
		})
		p.Stroke(imageBounds, glassBorder(c), 0, 1)
	})
	canvas.Children(func() {
		if s.result == nil || s.overwritten {
			return
		}
		reduceGlass := c.Preferences().ReduceTransparency || c.Preferences().HighContrast
		badgeBg := ui.RGBA(255, 255, 255, 0.90)
		badgeBorder := ui.RGBA(17, 20, 26, 0.14)
		badgeShadow := ui.RGBA(0, 0, 0, 0.08)
		if t.Dark {
			badgeBg = ui.RGBA(20, 24, 32, 0.86)
			badgeBorder = ui.RGBA(255, 255, 255, 0.16)
			badgeShadow = ui.RGBA(0, 0, 0, 0.28)
		}
		if reduceGlass {
			badgeBg = panelSurface(c)
			badgeBorder = controlBorder(c)
		}
		styleBadge := func(el ui.Element) ui.Element {
			el.Padding(4, 10).Radius(badgeRadius(22)).Background(badgeBg).Border(1, badgeBorder).
				Shadow(0, 1, 4, 0, badgeShadow)
			if !reduceGlass {
				el.Material(glass.Glass{})
			}
			return el
		}
		ui.Row(c).Absolute().Top(10).Left(10).Right(10).Gap(8).Children(func() {
			if s.comparison.mode == previewDiff {
				styleBadge(ui.Row(c).AlignItems(ui.Center).Gap(6)).Children(func() {
					ui.Row(c).AlignItems(ui.Center).Gap(2).Children(func() {
						ui.Box(c).Size(6, 6).Radius(3).Background(ui.Hex("#2084ec"))
						ui.Box(c).Size(6, 6).Radius(3).Background(ui.Hex("#22d3ee"))
						ui.Box(c).Size(6, 6).Radius(3).Background(ui.Hex("#facc15"))
						ui.Box(c).Size(6, 6).Radius(3).Background(ui.Hex("#f43f5e"))
					})
					ui.Text(c, "差异热力图").FontSize(11).FontWeight(500).TextColor(t.Text).SingleLine()
				})
				ui.Box(c).Grow(1)
				switch {
				case s.comparison.diffLoading:
					styleBadge(ui.Row(c).AlignItems(ui.Center).Gap(5)).Children(func() {
						ui.Text(c, "正在计算画质差异…").FontSize(11).FontWeight(500).TextColor(t.TextMuted).SingleLine()
					})
				case s.comparison.diffError != "":
					styleBadge(ui.Row(c).AlignItems(ui.Center).Gap(6)).Tooltip(s.comparison.diffError).Children(func() {
						ui.Box(c).Size(6, 6).Radius(3).Background(t.Danger)
						ui.Text(c, "差异计算失败").FontSize(11).FontWeight(500).TextColor(t.Danger).SingleLine()
						retryBtn := ui.Button(c.Key("diff-retry"), "重试").
							Padding(1, 6).Radius(badgeRadius(16)).
							FontSize(11).FontWeight(600).TextColor(t.Danger).
							Background(ui.Transparent).Border(1, t.Danger.Alpha(0.35)).
							Tooltip(s.comparison.diffError).
							OnClick(s.retryComparisonDiff)
						if retryBtn.Hovered() {
							retryBtn.Background(t.Danger.Alpha(0.12))
						}
					})
				case s.comparison.diffReady:
					dotColor := t.Success
					if !s.comparison.diffMetrics.Identical {
						if s.comparison.diffMetrics.SSIM < 0.90 {
							dotColor = t.Warning
						} else if s.comparison.diffMetrics.SSIM < 0.97 {
							dotColor = accentForeground(t)
						}
					}
					styleBadge(ui.Row(c).AlignItems(ui.Center).Gap(5)).
						Tooltip(formatQualityMetricsTooltip(s.comparison.diffMetrics)).Children(func() {
						ui.Box(c).Size(6, 6).Radius(3).Background(dotColor)
						ui.Text(c, formatQualityMetricsSummary(s.comparison.diffMetrics)).
							FontSize(11).FontWeight(500).FontFeatures("tnum").TextColor(t.Text).SingleLine()
					})
				}
				return
			}
			if comparing || s.comparison.mode == previewOriginal {
				styleBadge(ui.Row(c).AlignItems(ui.Center).Gap(5)).Children(func() {
					ui.Box(c).Size(6, 6).Radius(3).Background(metadataColor(c).Alpha(0.82))
					ui.Text(c, "原图 · "+formatSize(s.file.size)).FontSize(11).FontWeight(500).
						FontFeatures("tnum").TextColor(t.Text).SingleLine()
				})
			}
			ui.Box(c).Grow(1)
			if comparing || s.comparison.mode == previewResult {
				dotColor := metadataColor(c).Alpha(0.82)
				switch {
				case !s.result.Unchanged && s.result.OutputSize < s.result.OriginalSize:
					dotColor = t.Success
				case s.result.OutputSize > s.result.OriginalSize:
					dotColor = t.Warning
				}
				badge := styleBadge(ui.Row(c).AlignItems(ui.Center).Gap(5))
				if s.comparison.diffReady {
					badge.Tooltip(formatQualityMetricsTooltip(s.comparison.diffMetrics))
				}
				badge.Children(func() {
					ui.Box(c).Size(6, 6).Radius(3).Background(dotColor)
					ui.Text(c, "压缩后 · "+formatSize(s.result.OutputSize)).FontSize(11).FontWeight(500).
						FontFeatures("tnum").TextColor(t.Text).SingleLine()
				})
			}
		})
	})
	if comparing {
		pointerX, _, pointerOver := canvas.PointerPosition()
		dividerHovered := pointerOver && !s.comparison.panning && s.nearComparisonDivider(s.comparison.viewport.W, pointerX)
		reduceGlass := c.Preferences().ReduceTransparency || c.Preferences().HighContrast
		canvas.DrawOver(func(p *ui.Painter, bounds ui.Rect) {
			centerX := bounds.X + bounds.W*float32(s.comparison.split/100)
			centerY := bounds.Y + bounds.H/2
			p.Line(centerX, bounds.Y, centerX, bounds.Y+bounds.H, 3, ui.RGBA(0, 0, 0, 0.26))
			lineColor := ui.Hex("#ffffff")
			if dividerHovered {
				lineColor = accentForeground(t)
			}
			p.Line(centerX, bounds.Y, centerX, bounds.Y+bounds.H, 1.5, lineColor)
			handleX := min(bounds.X+bounds.W-16, max(bounds.X+16, centerX))
			handle := ui.Rect{X: handleX - 16, Y: centerY - 16, W: 32, H: 32}
			p.Shadow(handle, 16, 0, 2, 10, 0, ui.RGBA(0, 0, 0, 0.28))
			handleFill := ui.RGBA(255, 255, 255, 0.94)
			if t.Dark {
				handleFill = ui.RGBA(28, 33, 44, 0.92)
			}
			if reduceGlass {
				p.Fill(handle, panelSurface(c), 16)
			} else {
				glass.Paint(p, handle, 16, glass.Glass{})
				p.Fill(handle, handleFill, 16)
			}
			handleBorder := ui.RGBA(17, 20, 26, 0.16)
			if t.Dark {
				handleBorder = ui.RGBA(255, 255, 255, 0.28)
			}
			chevronColor := t.Text
			if dividerHovered {
				handleBorder = accentForeground(t)
				chevronColor = accentForeground(t)
			} else if reduceGlass {
				handleBorder = controlBorder(c)
			}
			p.Stroke(handle, handleBorder, 16, 1.25)
			p.StrokePolyline([][2]float32{
				{handleX - 3.5, centerY - 4}, {handleX - 7.5, centerY}, {handleX - 3.5, centerY + 4},
			}, 1.5, chevronColor)
			p.StrokePolyline([][2]float32{
				{handleX + 3.5, centerY - 4}, {handleX + 7.5, centerY}, {handleX + 3.5, centerY + 4},
			}, 1.5, chevronColor)
		})
	}
}
