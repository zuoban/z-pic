package main

import (
	"context"
	"image"
	"image/color"
	"image/draw"
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
