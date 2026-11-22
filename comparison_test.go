package main

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"math"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"zpic/internal/compress"
)

func testComparisonApplication() *application {
	s := testSelectedApplication()
	result := &compress.Result{
		OriginalSize: 4096, OutputSize: 2048,
		Format: "jpeg", Width: 20, Height: 12,
	}
	output := ui.NewBitmap(image.NewNRGBA(image.Rect(0, 0, 20, 12)))
	s.completeCompression(context.Background(), result, output, nil)
	return s
}

func testLargeComparisonApplication() *application {
	s := testComparisonApplication()
	s.file.width, s.file.height = 1600, 1000
	s.result.Width, s.result.Height = 1600, 1000
	return s
}

func comparisonCanvas(t *testing.T, tester *ui.Tester) ui.AccessInfo {
	t.Helper()
	canvas, ok := tester.AccessByRole(ui.RoleSlider, "原图与压缩结果对比")
	if !ok || canvas.Offscreen || canvas.Bounds.W <= 0 || canvas.Bounds.H <= 0 {
		t.Fatalf("comparison must expose a visible slider canvas: %+v", canvas)
	}
	return canvas
}

func TestComparisonSplitDragAndKeyboard(t *testing.T) {
	s := testComparisonApplication()
	tester := ui.NewTester(s.view, 880, 660)
	canvas := comparisonCanvas(t, tester)
	bounds := canvas.Bounds
	centerY := bounds.Y + bounds.H/2
	if s.comparison.mode != previewCompare || s.comparison.split != 50 {
		t.Fatal("a completed result must start with a centered comparison")
	}
	tester.Press(bounds.X+bounds.W/2, centerY)
	tester.Move(bounds.X+bounds.W*0.75, centerY)
	tester.Release(bounds.X+bounds.W*0.75, centerY)
	if math.Abs(s.comparison.split-75) > 1 {
		t.Fatalf("dragging to three quarters must reveal 75%% source, got %.2f", s.comparison.split)
	}
	tester.Key(0, ui.KeyRight)
	if s.comparison.split <= 75 {
		t.Fatal("the right arrow must move the focused split to the right")
	}
	tester.Key(0, ui.KeyHome)
	if s.comparison.split != 0 {
		t.Fatalf("Home must reveal the complete result, got %.2f", s.comparison.split)
	}
	tester.Key(0, ui.KeyEnd)
	if s.comparison.split != 100 {
		t.Fatalf("End must reveal the complete original, got %.2f", s.comparison.split)
	}
	tester.Key(0, ui.KeyLeft)
	if s.comparison.split >= 100 {
		t.Fatal("the left arrow must move the focused split to the left")
	}
	tester.Press(bounds.X+bounds.W/2, centerY)
	tester.Move(bounds.X-100, centerY)
	tester.Release(bounds.X-100, centerY)
	if s.comparison.split != 0 {
		t.Fatalf("a captured drag beyond the left edge must clamp to zero, got %.2f", s.comparison.split)
	}
	tester.Press(bounds.X+bounds.W/2, centerY)
	tester.Move(bounds.X+bounds.W+100, centerY)
	tester.Release(bounds.X+bounds.W+100, centerY)
	if s.comparison.split != 100 {
		t.Fatalf("a captured drag beyond the right edge must clamp to 100, got %.2f", s.comparison.split)
	}
	canvas = comparisonCanvas(t, tester)
	if canvas.Min != 0 || canvas.Max != 100 || canvas.Now != 100 {
		t.Fatalf("assistive technology must receive the current split and range: %+v", canvas)
	}
}

func TestComparisonModesPreserveExportAndCamera(t *testing.T) {
	s := testLargeComparisonApplication()
	tester := ui.NewTester(s.view, 880, 660)
	result, output, source := s.result, s.preview, s.file.preview
	s.comparison.split = 68
	s.comparison.scale = 2
	s.comparison.offset = ui.Point{X: 16, Y: -12}
	tester.Frame()
	offset := s.comparison.offset
	if offset.X == 0 || offset.Y == 0 {
		t.Fatal("the preservation test must exercise a nonzero pan on both axes")
	}
	for _, mode := range []struct {
		label string
		value int
		image string
	}{
		{"原图", previewOriginal, "原图预览"},
		{"结果", previewResult, "压缩后预览"},
		{"差异", previewDiff, "像素差异热力图"},
		{"对比", previewCompare, ""},
	} {
		if err := tester.Click(mode.label); err != nil {
			t.Fatal(err)
		}
		if s.comparison.mode != mode.value {
			t.Fatalf("%s must select its preview mode", mode.label)
		}
		if mode.image != "" {
			if _, ok := tester.AccessByRole(ui.RoleImage, mode.image); !ok {
				t.Fatalf("%s must expose %s", mode.label, mode.image)
			}
		} else {
			comparisonCanvas(t, tester)
		}
		if s.result != result || s.preview != output || s.file.preview != source {
			t.Fatal("preview changes must preserve source, compressed bitmap, and export result")
		}
		if s.comparison.split != 68 || s.comparison.scale != 2 || s.comparison.offset != offset {
			t.Fatal("mode switching must retain the shared comparison position and camera")
		}
	}
	s.completeCompression(context.Background(), result, output, nil)
	tester.Frame()
	if s.comparison.mode != previewCompare || s.comparison.split != 50 ||
		s.comparison.scale != 0 || s.comparison.offset != (ui.Point{}) {
		t.Fatal("a fresh compression must reset to a centered comparison fitted to the window")
	}
}

func TestComparisonZoomPanAndFit(t *testing.T) {
	s := testLargeComparisonApplication()
	tester := ui.NewTester(s.view, 880, 660)
	canvas := comparisonCanvas(t, tester)
	bounds := canvas.Bounds
	fitScale := s.previewScale(bounds)
	if err := tester.Click("放大"); err != nil {
		t.Fatal(err)
	}
	if s.comparison.scale <= fitScale {
		t.Fatal("zooming in must increase the effective image scale")
	}
	anchorX, anchorY := bounds.X+bounds.W/4, bounds.Y+bounds.H/2
	before := s.previewImageRect(bounds)
	imageX := (anchorX - before.X) / before.W
	imageY := (anchorY - before.Y) / before.H
	tester.Pinch(anchorX, anchorY, 2)
	after := s.previewImageRect(bounds)
	if math.Abs(float64((anchorX-after.X)/after.W-imageX)) > 0.001 ||
		math.Abs(float64((anchorY-after.Y)/after.H-imageY)) > 0.001 {
		t.Fatal("pinching must retain the image point beneath the gesture anchor")
	}
	if s.comparison.offset.X == 0 {
		t.Fatal("an off-center pinch must move the shared image origin")
	}
	offset := s.comparison.offset
	tester.Scroll(anchorX, anchorY, 20, 10)
	if s.comparison.offset.X >= offset.X || s.comparison.offset.Y >= offset.Y {
		t.Fatal("precise two-axis scrolling must pan the zoomed source and result together")
	}
	if err := tester.Click("原图"); err != nil {
		t.Fatal(err)
	}
	offset = s.comparison.offset
	centerX, centerY := bounds.X+bounds.W/2, bounds.Y+bounds.H/2
	tester.Press(centerX, centerY)
	tester.Move(centerX+40, centerY+25)
	tester.Release(centerX+40, centerY+25)
	if s.comparison.offset.X <= offset.X || s.comparison.offset.Y <= offset.Y {
		t.Fatal("dragging the single-image view must pan the zoomed image")
	}
	if err := tester.Click("1:1"); err != nil {
		t.Fatal(err)
	}
	if s.comparison.scale != 1 {
		t.Fatalf("1:1 must use one image pixel per logical preview point, got %.4f", s.comparison.scale)
	}
	if err := tester.Click("缩小"); err != nil {
		t.Fatal(err)
	}
	if s.comparison.scale >= 1 {
		t.Fatal("zooming out must reduce the effective image scale")
	}
	if err := tester.Click("适应"); err != nil {
		t.Fatal(err)
	}
	if s.comparison.scale != 0 || s.comparison.offset != (ui.Point{}) {
		t.Fatal("fitting the image must restore automatic scale and a centered image")
	}
	if s.previewScale(bounds) != fitScale || s.comparison.split != 50 {
		t.Fatal("fitting must restore the original contain scale without changing the comparison split")
	}
}

func TestComparisonZoomLimitsAndGuards(t *testing.T) {
	s := testLargeComparisonApplication()
	tester := ui.NewTester(s.view, 880, 660)
	canvas := comparisonCanvas(t, tester)
	center := ui.Point{X: canvas.Bounds.W / 2, Y: canvas.Bounds.H / 2}
	s.zoomPreview(1e9, center)
	tester.Frame()
	if s.comparison.scale != 16 {
		t.Fatalf("zoom must clamp to the supported maximum, got %.4f", s.comparison.scale)
	}
	zoomIn, ok := tester.AccessByRole(ui.RoleButton, "放大")
	if !ok || !zoomIn.Disabled {
		t.Fatal("zooming in must be unavailable at the maximum scale")
	}
	s.zoomPreview(1e-9, center)
	tester.Frame()
	if s.comparison.scale != 0.01 {
		t.Fatalf("zoom must clamp to the supported minimum, got %.4f", s.comparison.scale)
	}
	zoomOut, ok := tester.AccessByRole(ui.RoleButton, "缩小")
	if !ok || !zoomOut.Disabled {
		t.Fatal("zooming out must be unavailable at the minimum scale")
	}
	for _, state := range []string{"busy", "settings", "confirmation"} {
		t.Run(state, func(t *testing.T) {
			s.busy, s.settingsOpen, s.confirmOverwrite = state == "busy", state == "settings", state == "confirmation"
			scale, offset := s.comparison.scale, s.comparison.offset
			s.zoomPreview(2, center)
			if s.comparison.scale != scale || s.comparison.offset != offset {
				t.Fatal("guarded zoom actions must retain scale and position")
			}
		})
	}
}

func TestComparisonControlsFitMinimumWindow(t *testing.T) {
	for _, dark := range []bool{false, true} {
		name := "light"
		if dark {
			name = "dark"
		}
		t.Run(name, func(t *testing.T) {
			s := testLargeComparisonApplication()
			tester := ui.NewTester(func(c *ui.Context) {
				theme := ui.LightTheme()
				if dark {
					theme = ui.DarkTheme()
				}
				c.SetTheme(theme)
				s.view(c)
			}, 880, 660)
			canvas := comparisonCanvas(t, tester)
			if canvas.Bounds.H < 220 || canvas.Bounds.X < 0 || canvas.Bounds.Y < 0 ||
				canvas.Bounds.X+canvas.Bounds.W > 880 || canvas.Bounds.Y+canvas.Bounds.H > 660 {
				t.Fatalf("the comparison image must fit the minimum window: %+v", canvas.Bounds)
			}
			for _, label := range []string{"缩小", "放大", "适应", "1:1", "更换图片"} {
				button, ok := tester.AccessByRole(ui.RoleButton, label)
				if !ok || button.Offscreen || button.Bounds.W < 28 || button.Bounds.H < 28 ||
					button.Bounds.X < 0 || button.Bounds.Y < 0 ||
					button.Bounds.X+button.Bounds.W > 880 || button.Bounds.Y+button.Bounds.H > 660 {
					t.Fatalf("%s must remain visible and operable at the minimum size: %+v", label, button)
				}
			}
		})
	}
}

func TestComparisonUnavailableWithoutCurrentOriginal(t *testing.T) {
	for _, state := range []string{"empty", "selected", "overwritten", "invalidated"} {
		t.Run(state, func(t *testing.T) {
			s := testComparisonApplication()
			switch state {
			case "empty":
				s = newApplication()
			case "selected":
				s = testSelectedApplication()
			case "overwritten":
				s.completeOverwrite(s.result, nil)
			case "invalidated":
				s.invalidateResult()
			}
			tester := ui.NewTester(s.view, 880, 660)
			if _, ok := tester.AccessByRole(ui.RoleSlider, "原图与压缩结果对比"); ok {
				t.Fatal("comparison needs both a valid result and its unchanged original snapshot")
			}
			if _, ok := tester.AccessByRole(ui.RoleRadioGroup, "预览模式"); ok {
				t.Fatal("comparison mode choices must disappear when no original/result pair is available")
			}
		})
	}
}

func TestComparisonGesturesRespectOperationGuards(t *testing.T) {
	for _, state := range []string{"busy", "settings", "confirmation"} {
		t.Run(state, func(t *testing.T) {
			s := testComparisonApplication()
			tester := ui.NewTester(s.view, 880, 660)
			canvas := comparisonCanvas(t, tester)
			centerX := canvas.Bounds.X + canvas.Bounds.W/2
			centerY := canvas.Bounds.Y + canvas.Bounds.H/2
			tester.Press(centerX, centerY)
			tester.Release(centerX, centerY)
			switch state {
			case "busy":
				s.busy = true
			case "settings":
				s.settingsOpen = true
			case "confirmation":
				s.confirmOverwrite = true
			}
			tester.Frame()
			split, scale, offset := s.comparison.split, s.comparison.scale, s.comparison.offset
			lockedCanvas, visible := tester.AccessByRole(ui.RoleSlider, "原图与压缩结果对比")
			if !visible && state != "confirmation" {
				t.Fatal("busy and settings states must retain the disabled comparison")
			}
			if visible && !lockedCanvas.Disabled {
				t.Fatal("comparison must expose its unavailable state while an operation owns the window")
			}
			tester.Key(0, ui.KeyEnd)
			if state == "confirmation" {
				tester.Press(canvas.Bounds.X+8, centerY)
				tester.Move(canvas.Bounds.X+16, centerY)
				tester.Release(canvas.Bounds.X+16, centerY)
			} else {
				tester.Press(centerX, centerY)
				tester.Move(canvas.Bounds.X+canvas.Bounds.W*0.9, centerY)
				tester.Release(canvas.Bounds.X+canvas.Bounds.W*0.9, centerY)
			}
			tester.Pinch(centerX, centerY, 2)
			tester.Scroll(centerX, centerY, 20, 20)
			if s.comparison.split != split || s.comparison.scale != scale || s.comparison.offset != offset {
				t.Fatal("guarded pointer, keyboard, and camera input must leave the preview unchanged")
			}
		})
	}
}

func TestComparisonRendersIndependentSourceAndResult(t *testing.T) {
	for _, transparent := range []bool{false, true} {
		name := "opaque"
		if transparent {
			name = "transparent"
		}
		t.Run(name, func(t *testing.T) {
			s := testComparisonApplication()
			source := image.NewNRGBA(image.Rect(0, 0, 20, 12))
			if !transparent {
				draw.Draw(source, source.Bounds(), image.NewUniform(color.NRGBA{R: 255, A: 255}), image.Point{}, draw.Src)
			}
			output := image.NewNRGBA(source.Bounds())
			draw.Draw(output, output.Bounds(), image.NewUniform(color.NRGBA{B: 255, A: 255}), image.Point{}, draw.Src)
			s.file.preview = ui.NewBitmap(source)
			s.preview = ui.NewBitmap(output)
			tester := ui.NewTester(s.view, 880, 660)
			canvas := comparisonCanvas(t, tester)
			pixels := tester.Image()
			centerY := int(canvas.Bounds.Y + canvas.Bounds.H/2)
			left := pixels.RGBAAt(int(canvas.Bounds.X+canvas.Bounds.W/4), centerY)
			right := pixels.RGBAAt(int(canvas.Bounds.X+canvas.Bounds.W*3/4), centerY)
			if right.B < 250 || right.R > 5 || right.G > 5 {
				t.Fatalf("the right region must show the compressed bitmap, got %v", right)
			}
			if transparent {
				if math.Abs(float64(left.R)-float64(left.B)) > 5 ||
					math.Abs(float64(left.G)-float64(left.B)) > 5 {
					t.Fatalf("transparent source must reveal the neutral background without blue result bleed, got %v", left)
				}
			} else if left.R < 250 || left.G > 5 || left.B > 5 {
				t.Fatalf("the left region must show the original bitmap, got %v", left)
			}
		})
	}
}

func TestComparisonSharesImageGeometry(t *testing.T) {
	s := testComparisonApplication()
	source := image.NewNRGBA(image.Rect(0, 0, 20, 12))
	draw.Draw(source, image.Rect(0, 0, 10, 12),
		image.NewUniform(color.NRGBA{R: 255, A: 255}), image.Point{}, draw.Src)
	draw.Draw(source, image.Rect(10, 0, 20, 12),
		image.NewUniform(color.NRGBA{G: 255, A: 255}), image.Point{}, draw.Src)
	output := image.NewNRGBA(source.Bounds())
	draw.Draw(output, output.Bounds(),
		image.NewUniform(color.NRGBA{B: 255, A: 255}), image.Point{}, draw.Src)
	s.file.preview, s.preview = ui.NewBitmap(source), ui.NewBitmap(output)
	s.comparison.split = 75
	tester := ui.NewTester(s.view, 880, 660)
	canvas := comparisonCanvas(t, tester)
	pixels := tester.Image()
	centerY := int(canvas.Bounds.Y + canvas.Bounds.H/2)
	red := pixels.RGBAAt(int(canvas.Bounds.X+canvas.Bounds.W*0.4), centerY)
	green := pixels.RGBAAt(int(canvas.Bounds.X+canvas.Bounds.W*0.65), centerY)
	blue := pixels.RGBAAt(int(canvas.Bounds.X+canvas.Bounds.W*0.9), centerY)
	if red.R < 250 || red.G > 5 || red.B > 5 ||
		green.G < 250 || green.R > 5 || green.B > 5 ||
		blue.B < 250 || blue.R > 5 || blue.G > 5 {
		t.Fatalf("splitting must clip the shared fitted image instead of fitting each half independently: red=%v green=%v blue=%v",
			red, green, blue)
	}
}

func TestComparisonZoomShortcutsAndGuards(t *testing.T) {
	s := testLargeComparisonApplication()
	tester := ui.NewTester(s.view, 880, 660)
	canvas := comparisonCanvas(t, tester)
	fitScale := s.previewScale(canvas.Bounds)

	for _, label := range []string{"缩小", "放大", "适应", "1:1"} {
		btn, ok := tester.AccessByRole(ui.RoleButton, label)
		if !ok || btn.KeyShortcuts == "" {
			t.Fatalf("%q must expose shortcut metadata: %+v", label, btn)
		}
	}

	tester.Key(ui.Cmd, ui.KeyEqual)
	zoomed := s.comparison.scale
	if zoomed <= fitScale {
		t.Fatalf("Cmd+= must zoom in above fit scale: zoomed=%.4f fit=%.4f", zoomed, fitScale)
	}

	tester.Key(ui.Cmd|ui.Shift, ui.KeyEqual)
	zoomedMore := s.comparison.scale
	if zoomedMore <= zoomed {
		t.Fatalf("Cmd+Shift+= must also zoom in: before=%.4f after=%.4f", zoomed, zoomedMore)
	}

	tester.Key(ui.Cmd, ui.KeyMinus)
	if s.comparison.scale >= zoomedMore {
		t.Fatalf("Cmd+- must zoom out: before=%.4f after=%.4f", zoomedMore, s.comparison.scale)
	}

	tester.Key(ui.Cmd, ui.Key1)
	if s.comparison.scale != 1 || !tester.HasText("100%") {
		t.Fatalf("Cmd+1 must zoom to 100%%, got scale=%.4f", s.comparison.scale)
	}

	s.comparison.offset = ui.Point{X: 24, Y: -18}
	tester.Key(ui.Cmd, ui.Key0)
	if s.comparison.scale != 0 || s.comparison.offset != (ui.Point{}) {
		t.Fatalf("Cmd+0 must reset to fit and center offset, got scale=%.4f offset=%+v", s.comparison.scale, s.comparison.offset)
	}

	for _, state := range []string{"busy", "settings", "confirmation"} {
		t.Run(state, func(t *testing.T) {
			s.busy = state == "busy"
			s.settingsOpen = state == "settings"
			s.confirmOverwrite = state == "confirmation"
			s.comparison.scale = 0.75
			s.comparison.offset = ui.Point{X: 10, Y: -5}
			tester.Frame()

			for _, modKey := range []struct {
				mods ui.Modifiers
				key  ui.Key
			}{
				{ui.Cmd, ui.KeyEqual},
				{ui.Cmd | ui.Shift, ui.KeyEqual},
				{ui.Cmd, ui.KeyMinus},
				{ui.Cmd, ui.Key0},
				{ui.Cmd, ui.Key1},
			} {
				tester.Key(modKey.mods, modKey.key)
			}
			if s.comparison.scale != 0.75 || s.comparison.offset != (ui.Point{X: 10, Y: -5}) {
				t.Fatalf("guarded zoom shortcuts must not alter scale or offset during %s: scale=%.4f offset=%+v",
					state, s.comparison.scale, s.comparison.offset)
			}
			s.busy, s.settingsOpen, s.confirmOverwrite = false, false, false
		})
	}
}

func TestComparisonZoomedDragPansWithoutMovingSplit(test *testing.T) {
	appState := testLargeComparisonApplication()
	tester := ui.NewTester(appState.view, 880, 660)
	canvas := comparisonCanvas(test, tester)
	bounds := canvas.Bounds
	if appState.previewCanPan(bounds) {
		test.Fatal("fitted image must not enable drag panning")
	}

	if err := tester.Click("1:1"); err != nil {
		test.Fatal(err)
	}
	if !appState.previewCanPan(bounds) {
		test.Fatal("1:1 zoom on a 1600x1000 image must enable drag panning")
	}
	if appState.comparison.split != 50 {
		test.Fatalf("expected initial split 50, got %.2f", appState.comparison.split)
	}

	panStartX := bounds.X + bounds.W*0.18
	centerY := bounds.Y + bounds.H/2
	beforeOffset := appState.comparison.offset
	tester.Press(panStartX, centerY)
	tester.Move(panStartX+48, centerY+32)
	tester.Release(panStartX+48, centerY+32)

	if appState.comparison.split != 50 {
		test.Fatalf("zoomed drag away from divider must keep split at 50, got %.2f", appState.comparison.split)
	}
	if appState.comparison.offset.X <= beforeOffset.X || appState.comparison.offset.Y <= beforeOffset.Y {
		test.Fatalf("zoomed drag away from divider must pan offset: before=%+v after=%+v",
			beforeOffset, appState.comparison.offset)
	}
	canvas = comparisonCanvas(test, tester)
	if canvas.Now != 50 {
		test.Fatalf("assistive slider value must stay 50 after pan, got %.2f", canvas.Now)
	}

	tester.Key(0, ui.KeyRight)
	if appState.comparison.split <= 50 {
		test.Fatalf("keyboard arrow after pan release must adjust split, got %.2f", appState.comparison.split)
	}

	pannedOffset := appState.comparison.offset
	dividerX := bounds.X + bounds.W*float32(appState.comparison.split/100)
	targetX := bounds.X + bounds.W*0.75
	tester.Press(dividerX, centerY)
	tester.Move(targetX, centerY+20)
	tester.Release(targetX, centerY+20)
	if math.Abs(appState.comparison.split-75) > 1 {
		test.Fatalf("dragging on divider while zoomed must move split to ~75, got %.2f", appState.comparison.split)
	}
	if appState.comparison.offset != pannedOffset {
		test.Fatalf("dragging on divider must not alter pan offset: got %+v, want %+v",
			appState.comparison.offset, pannedOffset)
	}

	newDividerX := bounds.X + bounds.W*float32(appState.comparison.split/100)
	tester.PressButton(2, newDividerX, centerY)
	tester.Move(newDividerX-30, centerY-20)
	tester.ReleaseButton(2, newDividerX-30, centerY-20)
	if math.Abs(appState.comparison.split-75) > 1 {
		test.Fatalf("middle-button pan on divider must preserve split, got %.2f", appState.comparison.split)
	}
	if appState.comparison.offset.X >= pannedOffset.X || appState.comparison.offset.Y >= pannedOffset.Y {
		test.Fatalf("middle-button drag must pan image even over divider: before=%+v after=%+v",
			pannedOffset, appState.comparison.offset)
	}
}

func TestComparisonZeroWidthViewportStopsRequestingAnimationFrames(t *testing.T) {
	s := testComparisonApplication()
	tester := ui.NewTester(s.view, 0, 0)
	for range 4 {
		tester.Frame()
	}
	if tester.FramePending() {
		t.Fatal("zero-width preview must not loop AnimationFrame continuously")
	}
	tester.SetSize(880, 660)
	for range 4 {
		tester.Frame()
	}
	if s.comparison.viewport.W <= 0 || s.comparison.viewport.H <= 0 {
		t.Fatalf("viewport must recover after resize: %+v", s.comparison.viewport)
	}
	if tester.FramePending() {
		t.Fatal("recovered preview must settle without pending animation frames")
	}
}

func TestComparisonDoubleClickTogglesZoomAndPreservesSplit(t *testing.T) {
	s := testLargeComparisonApplication()
	tester := ui.NewTester(s.view, 960, 660)
	canvas := comparisonCanvas(t, tester)
	bounds := canvas.Bounds
	clickX := bounds.X + bounds.W*0.25
	clickY := bounds.Y + bounds.H*0.5

	tester.ClickAt(clickX, clickY)
	tester.ClickAt(clickX, clickY)
	if s.comparison.scale != 1 {
		t.Fatalf("double click in fit mode must zoom to 1:1, got %.2f", s.comparison.scale)
	}
	if math.Abs(s.comparison.split-50) > 0.5 {
		t.Fatalf("double click off-divider must preserve split at 50, got %.2f", s.comparison.split)
	}

	tester.Advance(time.Second)
	tester.ClickAt(clickX, clickY)
	tester.ClickAt(clickX, clickY)
	if s.comparison.scale != 0 {
		t.Fatalf("double click while zoomed must return to fit mode, got %.2f", s.comparison.scale)
	}
	if math.Abs(s.comparison.split-50) > 0.5 {
		t.Fatalf("double click reset must keep split at 50, got %.2f", s.comparison.split)
	}

	if err := tester.Click("原图"); err != nil {
		t.Fatal(err)
	}
	tester.Advance(time.Second)
	tester.ClickAt(clickX, clickY)
	tester.ClickAt(clickX, clickY)
	if s.comparison.scale != 1 {
		t.Fatalf("double click in single preview mode must zoom to 1:1, got %.2f", s.comparison.scale)
	}
}

func TestSupportsAlphaFormat(t *testing.T) {
	for _, format := range []string{"png", "webp", "avif", "heic"} {
		if !supportsAlphaFormat(format) {
			t.Fatalf("%s must enable alpha checkerboard", format)
		}
	}
	for _, format := range []string{"jpeg", "jpg", ""} {
		if supportsAlphaFormat(format) {
			t.Fatalf("%q must not enable alpha checkerboard", format)
		}
	}
}

func TestComparisonDiffHeatmapAndMetrics(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 64, 48))
	for y := 0; y < 48; y++ {
		for x := 0; x < 64; x++ {
			src.SetNRGBA(x, y, color.NRGBA{
				R: uint8(x * 4),
				G: uint8(y * 5),
				B: uint8((x*7 + y*11) % 256),
				A: 255,
			})
		}
	}
	var origBuf, compBuf bytes.Buffer
	if err := png.Encode(&origBuf, src); err != nil {
		t.Fatal(err)
	}
	if err := jpeg.Encode(&compBuf, src, &jpeg.Options{Quality: 30}); err != nil {
		t.Fatal(err)
	}
	origBitmap, err := ui.DecodeBitmap(origBuf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	compBitmap, err := ui.DecodeBitmap(compBuf.Bytes())
	if err != nil {
		t.Fatal(err)
	}

	s := newApplication()
	updates := make(chan func(), 4)
	s.dispatchUpdate = func(fn func()) { updates <- fn }
	s.file = &selectedImage{
		path:    "/tmp/sample.png",
		format:  "png",
		width:   64,
		height:  48,
		size:    int64(origBuf.Len()),
		data:    origBuf.Bytes(),
		preview: origBitmap,
	}
	s.completeCompression(context.Background(), &compress.Result{
		Format:       "jpeg",
		Width:        64,
		Height:       48,
		OriginalSize: int64(origBuf.Len()),
		OutputSize:   int64(compBuf.Len()),
		Data:         compBuf.Bytes(),
	}, compBitmap, nil)

	tester := ui.NewTester(s.view, 760, 660)
	// Confirm all 4 mode buttons fit cleanly in a single row even at minimum 760px width.
	var firstY float32
	for i, label := range []string{"对比", "原图", "结果", "差异"} {
		info, ok := tester.Access(label)
		if !ok || info.Offscreen {
			t.Fatalf("preview mode %q must be visible at 760px: %+v", label, info)
		}
		if i == 0 {
			firstY = info.Bounds.Y
		} else if math.Abs(float64(info.Bounds.Y-firstY)) > 1 {
			t.Fatalf("preview mode %q wrapped onto another line: %v vs %v", label, info.Bounds.Y, firstY)
		}
	}

	if err := tester.Click("差异"); err != nil {
		t.Fatal(err)
	}
	if s.comparison.mode != previewDiff {
		t.Fatalf("expected previewDiff mode, got %d", s.comparison.mode)
	}
	select {
	case fn := <-updates:
		fn()
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for async diff analysis")
	}
	tester.Frame()

	if !s.comparison.diffReady || s.comparison.diffBitmap == nil {
		t.Fatalf("expected diff heatmap ready, got ready=%v bitmap=%v err=%q",
			s.comparison.diffReady, s.comparison.diffBitmap != nil, s.comparison.diffError)
	}
	if s.comparison.diffMetrics.Identical || s.comparison.diffMetrics.SSIM <= 0 || s.comparison.diffMetrics.PSNR <= 0 {
		t.Fatalf("expected finite lossy quality metrics, got %+v", s.comparison.diffMetrics)
	}
	if !tester.HasText("差异热力图") {
		t.Fatal("diff view must display heatmap badge")
	}
	if !tester.HasText(formatQualityMetricsSummary(s.comparison.diffMetrics)) {
		t.Fatalf("diff view must display metrics summary %q", formatQualityMetricsSummary(s.comparison.diffMetrics))
	}

	// Resetting comparison clears cached diff state and bumps generation.
	prevGen := s.comparison.diffGen
	s.invalidateResult()
	if s.comparison.diffReady || s.comparison.diffBitmap != nil || s.comparison.diffGen <= prevGen {
		t.Fatalf("invalidateResult must reset diff cache and bump generation: %+v", s.comparison)
	}
}

func TestComparisonDiffUnchangedOverwriteAndRetry(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 48, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 48; x++ {
			src.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 5), G: uint8(y * 7), B: 180, A: 255})
		}
	}
	var origBuf bytes.Buffer
	if err := png.Encode(&origBuf, src); err != nil {
		t.Fatal(err)
	}
	origBitmap, err := ui.DecodeBitmap(origBuf.Bytes())
	if err != nil {
		t.Fatal(err)
	}

	s := newApplication()
	updates := make(chan func(), 4)
	s.dispatchUpdate = func(fn func()) { updates <- fn }
	s.file = &selectedImage{
		path:    "/tmp/unchanged.png",
		format:  "png",
		width:   48,
		height:  32,
		size:    int64(origBuf.Len()),
		data:    origBuf.Bytes(),
		preview: origBitmap,
	}
	// Simulate an unchanged batch in-place item whose result.Data was not populated.
	s.result = &compress.Result{
		Format:       "png",
		Width:        48,
		Height:       32,
		OriginalSize: int64(origBuf.Len()),
		OutputSize:   int64(origBuf.Len()),
		Unchanged:    true,
	}
	s.preview = origBitmap

	tester := ui.NewTester(s.view, 760, 660)
	if err := tester.Click("差异"); err != nil {
		t.Fatal(err)
	}
	select {
	case fn := <-updates:
		fn()
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for unchanged diff heatmap")
	}
	tester.Frame()
	if !s.comparison.diffReady || s.comparison.diffBitmap == nil || !s.comparison.diffMetrics.Identical {
		t.Fatalf("unchanged item should still build zero-diff heatmap bitmap: ready=%v bitmap=%v metrics=%+v err=%q",
			s.comparison.diffReady, s.comparison.diffBitmap != nil, s.comparison.diffMetrics, s.comparison.diffError)
	}

	// Simulate an error state and verify clicking the retry button recovers.
	s.comparison.diffReady = false
	s.comparison.diffBitmap = nil
	s.comparison.diffError = "模拟计算失败"
	tester.Frame()
	if err := tester.Click("重试"); err != nil {
		t.Fatalf("expected clickable retry button on diff error: %v", err)
	}
	select {
	case fn := <-updates:
		fn()
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for retried diff analysis")
	}
	if !s.comparison.diffReady || s.comparison.diffBitmap == nil || s.comparison.diffError != "" {
		t.Fatalf("retry should recover diff state: ready=%v bitmap=%v err=%q",
			s.comparison.diffReady, s.comparison.diffBitmap != nil, s.comparison.diffError)
	}

	// Overwriting the original must cancel/clear diff state and bump generation.
	genBeforeOverwrite := s.comparison.diffGen
	s.completeOverwriteWithBackup(s.result, false, nil)
	if s.comparison.diffReady || s.comparison.diffBitmap != nil || s.comparison.diffLoading || s.comparison.diffGen <= genBeforeOverwrite {
		t.Fatalf("completeOverwriteWithBackup must clear diff state and bump diffGen: %+v", s.comparison)
	}
}

func testLossyComparisonDiffApp(t *testing.T) (*application, chan func()) {
	t.Helper()
	src := image.NewNRGBA(image.Rect(0, 0, 64, 48))
	for y := 0; y < 48; y++ {
		for x := 0; x < 64; x++ {
			src.SetNRGBA(x, y, color.NRGBA{
				R: uint8(x * 4),
				G: uint8(y * 5),
				B: uint8((x*7 + y*11) % 256),
				A: 255,
			})
		}
	}
	var origBuf, compBuf bytes.Buffer
	if err := jpeg.Encode(&origBuf, src, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	if err := jpeg.Encode(&compBuf, src, &jpeg.Options{Quality: 30}); err != nil {
		t.Fatal(err)
	}
	origBitmap, err := ui.DecodeBitmap(origBuf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	compBitmap, err := ui.DecodeBitmap(compBuf.Bytes())
	if err != nil {
		t.Fatal(err)
	}

	s := newApplication()
	updates := make(chan func(), 4)
	s.dispatchUpdate = func(fn func()) { updates <- fn }
	s.file = &selectedImage{
		path:    "/tmp/lossy.jpg",
		format:  "jpeg",
		width:   64,
		height:  48,
		size:    int64(origBuf.Len()),
		data:    origBuf.Bytes(),
		preview: origBitmap,
	}
	s.completeCompression(context.Background(), &compress.Result{
		Format:       "jpeg",
		Width:        64,
		Height:       48,
		OriginalSize: int64(origBuf.Len()),
		OutputSize:   int64(compBuf.Len()),
		Data:         compBuf.Bytes(),
	}, compBitmap, nil)
	return s, updates
}

func TestComparisonModeShortcutsAndGuards(t *testing.T) {
	s, updates := testLossyComparisonDiffApp(t)
	// Start in previewOriginal so we also verify key 4 starts diff analysis.
	s.comparison.mode = previewOriginal
	tester := ui.NewTester(s.view, 880, 660)

	if modeInfo, ok := tester.Access("预览模式"); !ok || modeInfo.Description != "快捷键 1-4 切换对比 / 原图 / 结果 / 差异" {
		t.Fatalf("mode control must expose shortcut tooltip: ok=%v info=%+v", ok, modeInfo)
	}
	if s.comparison.diffLoading || s.comparison.diffReady {
		t.Fatalf("diff must not have started while initially in previewOriginal: %+v", s.comparison)
	}

	// Cmd+1 must zoom to 1:1 without changing preview mode.
	tester.Key(ui.Cmd, ui.Key1)
	if s.comparison.scale != 1 || s.comparison.mode != previewOriginal {
		t.Fatalf("Cmd+1 must zoom without switching mode: scale=%.2f mode=%d", s.comparison.scale, s.comparison.mode)
	}

	// Unmodified 1~4 switch between compare, original, result, and diff.
	for _, step := range []struct {
		key  ui.Key
		want int
	}{
		{ui.Key3, previewResult},
		{ui.Key4, previewDiff},
		{ui.Key1, previewCompare},
		{ui.Key2, previewOriginal},
	} {
		tester.Key(0, step.key)
		if s.comparison.mode != step.want {
			t.Fatalf("key %v must switch mode to %d, got %d", step.key, step.want, s.comparison.mode)
		}
	}

	// Switching to previewDiff via key 4 must have triggered async diff computation.
	select {
	case fn := <-updates:
		fn()
	case <-time.After(5 * time.Second):
		t.Fatal("pressing 4 for previewDiff must trigger diff analysis")
	}
	if !s.comparison.diffReady {
		t.Fatalf("expected diff ready after key 4 analysis: err=%q", s.comparison.diffError)
	}

	// Shortcuts must be ignored while modal/locked.
	for _, state := range []string{"busy", "settings", "confirmation"} {
		t.Run(state, func(t *testing.T) {
			s.comparison.mode = previewOriginal
			s.busy = state == "busy"
			s.settingsOpen = state == "settings"
			s.confirmOverwrite = state == "confirmation"
			tester.Frame()
			for _, key := range []ui.Key{ui.Key1, ui.Key3, ui.Key4} {
				tester.Key(0, key)
			}
			if s.comparison.mode != previewOriginal {
				t.Fatalf("mode shortcuts must be ignored during %s, got mode %d", state, s.comparison.mode)
			}
			s.busy, s.settingsOpen, s.confirmOverwrite = false, false, false
		})
	}

	// Shortcuts must be ignored when a text field holds focus (FocusTakesText).
	textValue := ""
	textTester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() {
			ui.TextInput(c.Key("shortcut-guard-input"), &textValue).Label("快捷键拦截输入框").Height(28).FillWidth()
			ui.Box(c).Grow(1).FillWidth().Children(func() {
				s.view(c)
			})
		})
	}, 880, 700)
	s.comparison.mode = previewOriginal
	textTester.Frame()
	if err := textTester.Click("快捷键拦截输入框"); err != nil {
		t.Fatal(err)
	}
	if field, ok := textTester.AccessByRole(ui.RoleTextField, "快捷键拦截输入框"); !ok || !field.Focused {
		t.Fatalf("expected text input to hold focus: ok=%v info=%+v", ok, field)
	}
	for _, key := range []ui.Key{ui.Key1, ui.Key3, ui.Key4} {
		textTester.Key(0, key)
	}
	if s.comparison.mode != previewOriginal {
		t.Fatalf("mode shortcuts must be ignored while FocusTakesText is true, got mode %d", s.comparison.mode)
	}
}

func TestComparisonDiffHeatmapLegendRenderingAndTooltip(t *testing.T) {
	// Verify gradient ramp stops match the heatmap palette.
	if got, want := heatmapLegendColor(0), ui.Hex("#2084ec"); got != want {
		t.Fatalf("ramp(0) = %+v, want %+v", got, want)
	}
	if got, want := heatmapLegendColor(0.25), ui.Hex("#22d3ee"); got != want {
		t.Fatalf("ramp(0.25) = %+v, want %+v", got, want)
	}
	if got, want := heatmapLegendColor(0.50), ui.Hex("#facc15"); got != want {
		t.Fatalf("ramp(0.50) = %+v, want %+v", got, want)
	}
	if got, want := heatmapLegendColor(1), ui.Hex("#f43f5e"); got != want {
		t.Fatalf("ramp(1) = %+v, want %+v", got, want)
	}

	for _, dark := range []bool{false, true} {
		name := "light"
		if dark {
			name = "dark"
		}
		t.Run(name, func(t *testing.T) {
			s := testComparisonApplication()
			s.comparison.mode = previewDiff
			s.comparison.diffReady = true
			s.comparison.diffMetrics = compress.QualityMetrics{
				SSIM: 0.962, PSNR: 32.4, ChangedPercent: 41.2, MaxChannelDiff: 12,
			}
			tester := ui.NewTester(func(c *ui.Context) {
				if dark {
					c.SetTheme(ui.DarkTheme())
				} else {
					c.SetTheme(ui.LightTheme())
				}
				s.view(c)
			}, 880, 660)

			for _, label := range []string{"差异热力图", "低", "高"} {
				if !tester.HasText(label) {
					t.Fatalf("diff legend must render text %q", label)
				}
			}
			legendRect, ok := tester.Find("差异热力图")
			if !ok {
				t.Fatal("diff legend label must have visible bounds")
			}
			tester.Move(legendRect.X+legendRect.W/2, legendRect.Y+legendRect.H/2)
			tester.Advance(time.Second)
			wantTooltip := "蓝：几乎无差异 · 青：轻微差异 · 黄：中等差异 · 红：明显像素差异"
			if !tester.HasText(wantTooltip) {
				t.Fatalf("hovering diff legend must display tooltip %q, got texts %v", wantTooltip, tester.Texts())
			}

			lowRect, okLow := tester.Find("低")
			highRect, okHigh := tester.Find("高")
			if !okLow || !okHigh || highRect.X <= lowRect.X+lowRect.W+20 {
				t.Fatalf("expected horizontal gradient bar between 低 and 高: low=%+v high=%+v", lowRect, highRect)
			}
			barStartX := lowRect.X + lowRect.W + 4
			barEndX := highRect.X - 4
			barY := int(lowRect.Y + lowRect.H/2)
			img := tester.Image()
			leftPx := img.RGBAAt(int(barStartX+3), barY)
			rightPx := img.RGBAAt(int(barEndX-3), barY)
			if int(leftPx.B) <= int(leftPx.R)+60 {
				t.Fatalf("left end of gradient legend must be blue-dominant, got %+v", leftPx)
			}
			if int(rightPx.R) <= int(rightPx.B)+60 {
				t.Fatalf("right end of gradient legend must be red/rose-dominant, got %+v", rightPx)
			}
		})
	}
}

func TestComparisonProactiveQualityMetricsInCompareMode(t *testing.T) {
	// Locked modal state in compare mode must not eagerly start diff computation.
	lockedApp, _ := testLossyComparisonDiffApp(t)
	lockedApp.confirmOverwrite = true
	ui.NewTester(lockedApp.view, 880, 660)
	if lockedApp.comparison.diffLoading || lockedApp.comparison.diffReady {
		t.Fatalf("locked compare view must not proactively launch diff: %+v", lockedApp.comparison)
	}

	s, updates := testLossyComparisonDiffApp(t)
	tester := ui.NewTester(s.view, 880, 660)
	if s.comparison.mode != previewCompare {
		t.Fatalf("expected initial previewCompare mode, got %d", s.comparison.mode)
	}
	if !s.comparison.diffLoading {
		t.Fatal("compare mode must proactively start background SSIM/PSNR analysis")
	}

	select {
	case fn := <-updates:
		fn()
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for proactive compare-mode diff analysis")
	}
	tester.Frame()

	if !s.comparison.diffReady || s.comparison.diffMetrics.SSIM <= 0 || s.comparison.diffMetrics.PSNR <= 0 {
		t.Fatalf("expected computed quality metrics in compare mode: ready=%v metrics=%+v err=%q",
			s.comparison.diffReady, s.comparison.diffMetrics, s.comparison.diffError)
	}
	summary := formatQualityMetricsSummary(s.comparison.diffMetrics)
	tooltip := formatQualityMetricsTooltip(s.comparison.diffMetrics)
	if !tester.HasText(summary) {
		t.Fatalf("compare mode must render quality metrics summary %q", summary)
	}
	qualRect, ok := tester.Find(summary)
	if !ok {
		t.Fatalf("compare quality badge must have visible bounds for %q", summary)
	}
	tester.Move(qualRect.X+qualRect.W/2, qualRect.Y+qualRect.H/2)
	tester.Advance(time.Second)
	if !tester.HasText(tooltip) {
		t.Fatalf("hovering compare quality badge must display tooltip %q, got texts %v", tooltip, tester.Texts())
	}

	// Verify balanced non-overlapping layout between 原图 (left), quality badge (center), and 压缩后 (right).
	for _, width := range []int{880, 760} {
		tester.SetSize(width, 660)
		tester.Frame()
		origRect, okOrig := tester.Find("原图 · " + formatSize(s.file.size))
		qualRect, okQual := tester.Find(summary)
		resRect, okRes := tester.Find("压缩后 · " + formatSize(s.result.OutputSize))
		if !okOrig || !okQual || !okRes {
			t.Fatalf("width %d: all three compare badges must be visible: orig=%v qual=%v res=%v",
				width, okOrig, okQual, okRes)
		}
		if origRect.X+origRect.W >= qualRect.X || qualRect.X+qualRect.W >= resRect.X {
			t.Fatalf("width %d: compare badges must not overlap: orig=%+v qual=%+v res=%+v",
				width, origRect, qualRect, resRect)
		}
	}

	// Original/Result single-view modes should keep overlays uncluttered (no center quality badge).
	tester.Key(0, ui.Key2)
	if tester.HasText(summary) {
		t.Fatal("original-only mode must not display the centered compare quality badge")
	}
	tester.Key(0, ui.Key3)
	if tester.HasText(summary) {
		t.Fatal("result-only mode must not display the centered compare quality badge")
	}
	// Switching to diff mode reuses the already-computed heatmap without starting a new job.
	genBeforeDiff := s.comparison.diffGen
	tester.Key(0, ui.Key4)
	if s.comparison.diffGen != genBeforeDiff || !s.comparison.diffReady || !tester.HasText(summary) {
		t.Fatalf("switching to diff mode must reuse proactive metrics and heatmap: gen=%d->%d ready=%v",
			genBeforeDiff, s.comparison.diffGen, s.comparison.diffReady)
	}
}
