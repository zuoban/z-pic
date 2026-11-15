package main

import (
	"math"

	"github.com/egoist/mygo/ui"
)

const (
	previewCompare = iota
	previewOriginal
	previewResult
)

type comparisonView struct {
	mode     int
	split    float64
	scale    float64
	offset   ui.Point
	viewport ui.Rect
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
}

func (s *application) zoomPreviewActual() {
	if s.previewLocked() || s.preview == nil || s.file == nil {
		return
	}
	s.zoomPreview(1, s.previewViewportCenter())
}

func (s *application) previewToolbar(c *ui.Context) {
	t := c.Theme()
	locked := s.previewLocked()
	bounds := s.comparison.viewport
	scale := s.previewScale(bounds)
	ui.Row(c).Height(32).Gap(8).AlignItems(ui.Center).Children(func() {
		for _, action := range []struct {
			key, label string
			keyCode    ui.Key
			hintKey    string
			factor     float64
		}{
			{"zoom-out", "缩小", ui.KeyMinus, "-", 0.8},
			{"zoom-in", "放大", ui.KeyEqual, "=", 1.25},
		} {
			button := ui.Button(c.Key(action.key), "").Label(action.label).
				KeyShortcuts(ui.Cmd, action.keyCode).
				Tooltip(action.label+" · "+shortcutLabel(action.hintKey)).
				Size(32, 32).Disabled(locked || (action.factor < 1 && scale <= 0.01) ||
				(action.factor > 1 && scale >= 16))
			button.DrawOver(func(p *ui.Painter, r ui.Rect) {
				p.Line(r.X+11, r.Y+r.H/2, r.X+r.W-11, r.Y+r.H/2, 1.5, t.Text)
				if action.factor > 1 {
					p.Line(r.X+r.W/2, r.Y+11, r.X+r.W/2, r.Y+r.H-11, 1.5, t.Text)
				}
			}).OnClick(func() {
				s.zoomPreviewBy(action.factor)
			})
		}
		ui.Textf(c, "%.0f%%", scale*100).Width(52).FontSize(12).
			FontFeatures("tnum").TextAlign(ui.Center).TextColor(t.TextMuted)
		ui.Button(c.Key("preview-fit"), "适应").Height(32).Disabled(locked).
			KeyShortcuts(ui.Cmd, ui.Key0).
			Tooltip("完整显示图片，重置缩放和位置 · " + shortcutLabel("0")).
			OnClick(s.resetPreviewFit)
		ui.Button(c.Key("preview-actual"), "1:1").Height(32).Disabled(locked).
			KeyShortcuts(ui.Cmd, ui.Key1).
			Tooltip("按原始尺寸查看（100%） · " + shortcutLabel("1")).
			OnClick(s.zoomPreviewActual)
		ui.Box(c).Grow(1)
		hint := "滚轮缩放"
		if s.comparing() {
			hint = "拖动分隔线"
		}
		ui.Text(c, hint).FontSize(12).TextColor(t.TextMuted).
			Tooltip("拖动分隔线对比；滚轮或捏合缩放；双指滚动平移图片")
	})
}

func (s *application) previewCanvas(c *ui.Context) {
	t := c.Theme()
	comparing := s.comparing()
	var canvas ui.Element
	if comparing {
		canvas = ui.SliderBase(c.Key("comparison"), &s.comparison.split, 0, 100).
			Step(1).Label("原图与压缩结果对比").Cursor(ui.CursorResizeEW).
			Description("左侧原图，右侧压缩后。拖动或使用方向键调整分隔线，Home 和 End 查看整张图片。")
	} else {
		label := "原图预览"
		if s.result != nil && (s.overwritten || s.comparison.mode != previewOriginal) {
			label = "压缩后预览"
		}
		canvas = ui.Box(c.Key("preview-image")).Role(ui.RoleImage).Label(label).
			Focusable().Cursor(ui.CursorGrab)
		if deltaX, deltaY, held := canvas.Dragged(); held && !s.previewLocked() {
			s.comparison.offset.X += deltaX
			s.comparison.offset.Y += deltaY
			s.clampPreviewOffset()
			canvas.Cursor(ui.CursorGrabbing)
		}
	}
	canvas.Fill().Radius(8).Clip().FocusRingOffset(-3).
		Disabled(s.previewLocked()).DimDisabled(false)
	previousBounds := s.comparison.viewport
	s.comparison.viewport = canvas.Bounds()
	if s.comparison.viewport != previousBounds || s.comparison.viewport.W <= 0 {
		c.AnimationFrame()
	}
	s.clampPreviewOffset()
	canvas.HandleInput(func(event ui.InputEvent) bool {
		if s.previewLocked() {
			return false
		}
		switch event.Kind {
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
		if !comparing {
			bitmap := s.preview
			if s.result != nil && !s.overwritten && s.comparison.mode == previewOriginal {
				bitmap = s.file.preview
			}
			p.Image(bitmap, imageBounds, ui.FillBox)
			return
		}
		divider := bounds.W * float32(s.comparison.split/100)
		p.Clip(ui.Rect{X: bounds.X, Y: bounds.Y, W: divider, H: bounds.H}, 0, func() {
			p.Image(s.file.preview, imageBounds, ui.FillBox)
		})
		p.Clip(ui.Rect{X: bounds.X + divider, Y: bounds.Y, W: bounds.W - divider, H: bounds.H}, 0, func() {
			p.Image(s.preview, imageBounds, ui.FillBox)
		})
	})
	canvas.Children(func() {
		if s.result == nil || s.overwritten {
			return
		}
		ui.Row(c).Absolute().Top(12).Left(12).Right(12).Gap(8).Children(func() {
			if comparing || s.comparison.mode == previewOriginal {
				ui.Text(c, "原图 · "+formatSize(s.file.size)).FontSize(12).FontWeight(500).
					Padding(6, 8).Radius(6).Background(t.Surface).TextColor(t.Text).
					SingleLine()
			}
			ui.Box(c).Grow(1)
			if comparing || s.comparison.mode == previewResult {
				ui.Text(c, "压缩后 · "+formatSize(s.result.OutputSize)).FontSize(12).FontWeight(500).
					Padding(6, 8).Radius(6).Background(t.Surface).TextColor(t.Text).
					SingleLine()
			}
		})
	})
	if comparing {
		canvas.DrawOver(func(p *ui.Painter, bounds ui.Rect) {
			centerX := bounds.X + bounds.W*float32(s.comparison.split/100)
			centerY := bounds.Y + bounds.H/2
			p.Line(centerX, bounds.Y, centerX, bounds.Y+bounds.H, 3, ui.RGBA(0, 0, 0, 0.25))
			p.Line(centerX, bounds.Y, centerX, bounds.Y+bounds.H, 1, ui.Hex("#ffffff"))
			handleX := min(bounds.X+bounds.W-20, max(bounds.X+20, centerX))
			handle := ui.Rect{X: handleX - 20, Y: centerY - 20, W: 40, H: 40}
			p.Fill(handle, t.Surface, 20)
			p.Stroke(handle, t.TextMuted, 20, 1)
			p.StrokePolyline([][2]float32{
				{handleX - 5, centerY - 5}, {handleX - 10, centerY}, {handleX - 5, centerY + 5},
			}, 1.5, t.Text)
			p.StrokePolyline([][2]float32{
				{handleX + 5, centerY - 5}, {handleX + 10, centerY}, {handleX + 5, centerY + 5},
			}, 1.5, t.Text)
		})
	}
}
