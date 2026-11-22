package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func floatingTestApplication() *application {
	app := newApplication()
	items := make([]batchItem, 4)
	for index := range items {
		name := fmt.Sprintf("photo-%d.webp", index+1)
		items[index] = batchItem{
			path: "/pictures/" + name, absPath: "/pictures/" + name, relPath: name,
			format: "webp", width: 1100, height: 580,
			originalSize: int64((4 - index) * 1024),
		}
	}
	app.batch = newBatchState(items)
	return app
}

func TestFloatingCompactExpandsWithoutChangingQueue(t *testing.T) {
	app := floatingTestApplication()
	items := slices.Clone(app.batch.items)
	tester := ui.NewTester(app.floatingView, floatingCompactWidth, floatingCompactHeight)
	for _, label := range []string{"展开图片", "4 张图片", "图片缩略图堆叠，展开图片"} {
		if _, ok := tester.AccessByRole(ui.RoleButton, label); !ok {
			t.Fatalf("compact stack is missing %q", label)
		}
	}
	if err := tester.Click("4 张图片"); err != nil || !app.floating.expanded {
		t.Fatalf("count should expand the panel: %v", err)
	}
	expanded := ui.NewTester(app.floatingView, floatingExpandedWidth, floatingExpandedHeight)
	if _, ok := expanded.AccessByRole(ui.RoleList, "悬浮窗图片网格"); !ok {
		t.Fatal("expanded panel must show a thumbnail grid")
	}
	if err := expanded.Click("收起图片"); err != nil || app.floating.expanded {
		t.Fatalf("back should collapse the panel: %v", err)
	}
	if !slices.Equal(app.batch.items, items) || len(app.dropQueue) != 0 {
		t.Fatal("opening and collapsing must preserve the image queue")
	}
}

func TestFloatingGallerySelectionKeyboardAndViewSwitch(t *testing.T) {
	app := floatingTestApplication()
	app.floating.expanded = true
	tester := ui.NewTester(app.floatingView, floatingExpandedWidth, floatingExpandedHeight)
	if err := tester.Click("photo-2.webp"); err != nil {
		t.Fatal(err)
	}
	if app.floating.selected != app.batch.items[1].path {
		t.Fatalf("grid click selected %q", app.floating.selected)
	}
	tester.Key(0, ui.KeyRight)
	if app.floating.selected != app.batch.items[2].path {
		t.Fatalf("grid arrows selected %q", app.floating.selected)
	}
	if err := tester.Click("列表视图"); err != nil || !app.floating.listView {
		t.Fatalf("list toggle failed: %v", err)
	}
	if _, ok := tester.AccessByRole(ui.RoleList, "悬浮窗图片列表"); !ok {
		t.Fatal("list view is not accessible")
	}
	if app.floating.selected != app.batch.items[2].path {
		t.Fatal("view switching lost selection")
	}
	if err := tester.Click("photo-1.webp"); err != nil {
		t.Fatal(err)
	}
	tester.Key(0, ui.KeyDown)
	if app.floating.selected != app.batch.items[1].path {
		t.Fatal("list keyboard navigation should select the next image")
	}
	if err := tester.Click("网格视图"); err != nil || app.floating.listView {
		t.Fatalf("grid toggle failed: %v", err)
	}
	tester.Key(0, ui.KeyEscape)
	if app.floating.expanded {
		t.Fatal("Escape should collapse the expanded gallery")
	}
}

func TestFloatingSortingFilteringAndRemovalPreservePathSelection(t *testing.T) {
	app := floatingTestApplication()
	app.floating.expanded = true
	app.batch.items[1].status = batchDone
	app.batch.items[1].outputSize = 200
	tester := ui.NewTester(app.floatingView, floatingExpandedWidth, floatingExpandedHeight)
	if err := tester.Click("photo-2.webp"); err != nil {
		t.Fatal(err)
	}
	app.floating.sort = batchSortOriginalSize
	app.batch.items[3].originalSize = 100_000
	tester.Frame()
	if app.floating.selected != app.batch.items[1].path || app.floating.cursor != 2 {
		t.Fatal("sorting must retain the selected file")
	}
	app.floating.filter = batchFilterDone
	tester.Frame()
	if len(app.floating.paths) != 1 || app.floating.selected != app.batch.items[1].path {
		t.Fatal("filtering should show completed images and retain a visible selection")
	}
	app.floating.filter = batchFilterFailed
	tester.Frame()
	if len(app.floating.paths) != 0 || app.floating.selected != "" {
		t.Fatal("an empty filter must clear its hidden selection")
	}
	reveal, ok := tester.AccessByRole(ui.RoleButton, floatingRevealLabel())
	if ok && !reveal.Disabled {
		t.Fatal("Finder reveal should be unavailable without a visible selection")
	}
	if _, ok := tester.AccessByRole(ui.RoleButton, "显示全部"); !ok {
		t.Fatal("an empty filter should provide a recovery action")
	}
	app.floating.filter = batchFilterAll
	tester.Frame()
	selected := app.floating.selected
	app.batch.items = slices.DeleteFunc(app.batch.items, func(item batchItem) bool { return item.path == selected })
	tester.Frame()
	if app.floating.selected == selected || len(app.floating.paths) != 3 {
		t.Fatal("removed items must not leave a stale selected path")
	}
}

func TestFloatingRevealUsesSelectedSourceOrOutputAndRespectsGuards(t *testing.T) {
	root := t.TempDir()
	source, output := filepath.Join(root, "source.png"), filepath.Join(root, "output.png")
	writeTestPNG(t, source, 40, 20, true)
	writeTestPNG(t, output, 40, 20, true)
	app := newApplication()
	app.floating.expanded = true
	app.batch = newBatchState([]batchItem{{
		path: source, absPath: source, relPath: "source.png", width: 40, height: 20,
		format: "png", originalSize: 1024,
	}})
	var revealed []string
	app.showInFolder = func(path string) { revealed = append(revealed, path) }
	tester := ui.NewTester(app.floatingView, floatingExpandedWidth, floatingExpandedHeight)
	if err := tester.Click(floatingRevealLabel()); err != nil || !slices.Equal(revealed, []string{source}) {
		t.Fatalf("Finder should reveal the selected source: %v %v", revealed, err)
	}
	app.batch.items[0].savedPath = output
	app.batch.items[0].status = batchDone
	tester.Frame()
	if err := tester.Click(floatingRevealLabel()); err != nil ||
		!slices.Equal(revealed, []string{source, output}) {
		t.Fatalf("Finder should prefer the saved output: %v %v", revealed, err)
	}
	app.settingsOpen = true
	app.revealFloatingSelection()
	if len(revealed) != 2 {
		t.Fatal("a modal guard must block Finder reveal")
	}
}

func TestFloatingListFallbackChoosesNewItemAtSamePosition(t *testing.T) {
	for _, change := range []string{"filter", "remove"} {
		t.Run(change, func(t *testing.T) {
			app := floatingTestApplication()
			app.floating.expanded, app.floating.listView = true, true
			app.batch.items[1].status = batchDone
			tester := ui.NewTester(app.floatingView, floatingExpandedWidth, floatingExpandedHeight)
			if err := tester.Click("photo-1.webp"); err != nil {
				t.Fatal(err)
			}
			expected := app.batch.items[1].path
			if change == "filter" {
				app.floating.filter = batchFilterDone
			} else {
				app.batch.items = app.batch.items[1:]
			}
			tester.Frame()
			if app.floating.cursor != 0 || app.floating.selected != expected {
				t.Fatalf("fallback selection is inconsistent: cursor=%d path=%q; want %q",
					app.floating.cursor, app.floating.selected, expected)
			}
		})
	}
}

func TestFloatingListFinderActionScrollsAboveFooter(t *testing.T) {
	app := floatingTestApplication()
	app.floating.expanded, app.floating.listView = true, true
	tester := ui.NewTester(app.floatingView, floatingExpandedWidth, floatingExpandedHeight)
	if err := tester.Click("photo-1.webp"); err != nil {
		t.Fatal(err)
	}
	tester.Key(0, ui.KeyEnd)
	row, ok := tester.AccessByRole(ui.RoleListItem, floatingRevealLabel())
	if !ok || row.Offscreen || row.Bounds.Y < 60 || row.Bounds.Y+row.Bounds.H > 420 {
		t.Fatalf("Finder action must scroll into view above the fixed footer: %+v", row)
	}
	for _, label := range []string{"查看队列", "下载位置"} {
		button, ok := tester.AccessByRole(ui.RoleButton, label)
		if !ok || button.Offscreen || button.Bounds.Y < row.Bounds.Y+row.Bounds.H {
			t.Fatalf("gallery scrolling must retain its fixed footer: %q %+v", label, button)
		}
	}
}

func TestFloatingVirtualGalleryAndLongNamesFit(t *testing.T) {
	for _, list := range []bool{false, true} {
		t.Run(fmt.Sprintf("list=%v", list), func(t *testing.T) {
			app := newApplication()
			items := make([]batchItem, maxBatchFiles)
			for index := range items {
				name := fmt.Sprintf("%04d-%s.webp", index, strings.Repeat("很长的图片名称", 10))
				items[index] = batchItem{
					path: "/photos/" + name, absPath: "/photos/" + name, relPath: name,
					width: 1100, height: 580, format: "webp", originalSize: 1024,
				}
			}
			app.batch = newBatchState(items)
			app.floating.expanded, app.floating.listView = true, list
			tester := ui.NewTester(app.floatingView, floatingExpandedWidth, floatingExpandedHeight)
			if count := len(app.thumbnails.entries); count == 0 || count > 64 {
				t.Fatalf("gallery must load only visible thumbnails: %d", count)
			}
			for _, label := range []string{"收起图片", "网格视图", "列表视图", "查看队列", "下载位置"} {
				control, ok := tester.AccessByRole(ui.RoleButton, label)
				if !ok || control.Offscreen || control.Bounds.X+control.Bounds.W > floatingExpandedWidth {
					t.Fatalf("long names must not displace controls: %q %+v", label, control)
				}
			}
		})
	}
}

func TestFloatingBoundsRemainOnTheirDisplay(t *testing.T) {
	area := mygo.Rectangle{X: -1920, Y: 24, Width: 1920, Height: 1056}
	for _, current := range []mygo.Rectangle{
		{X: -250, Y: 80, Width: 220, Height: 240},
		{X: -1900, Y: 80, Width: 220, Height: 240},
		{X: -250, Y: 900, Width: 220, Height: 240},
	} {
		bounds := floatingPanelBounds(current, area, true)
		if bounds.Width != floatingExpandedWidth || bounds.Height != floatingExpandedHeight ||
			bounds.X < area.X || bounds.Y < area.Y ||
			bounds.X+bounds.Width > area.X+area.Width || bounds.Y+bounds.Height > area.Y+area.Height {
			t.Fatalf("expanded panel left its work area: %+v", bounds)
		}
		compact := floatingPanelBounds(bounds, area, false)
		if compact.X+compact.Width != bounds.X+bounds.Width && compact.X != bounds.X {
			t.Fatalf("collapse must retain the adjacent edge: %+v -> %+v", bounds, compact)
		}
	}
}

func TestFloatingExpandedFooterIconButtonsAndBreathingRoom(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "photo-1.png")
	output := filepath.Join(root, "photo-1-compressed.png")
	writeTestPNG(t, source, 40, 20, true)
	writeTestPNG(t, output, 40, 20, true)

	app := floatingTestApplication()
	app.batch.items[0].path = source
	app.batch.items[0].absPath = source
	app.batch.items[0].relPath = "photo-1.png"
	app.batch.items[0].savedPath = output
	app.batch.items[0].status = batchDone
	app.batch.items[0].outputSize = 512
	app.floating.expanded = true

	tester := ui.NewTester(app.floatingView, floatingExpandedWidth, floatingExpandedHeight)

	// Main navigation buttons stay legible and accessible.
	for _, label := range []string{"继续压缩", "查看队列", "预览选中"} {
		btn, ok := tester.AccessByRole(ui.RoleButton, label)
		if !ok || btn.Offscreen || btn.Bounds.X < 0 || btn.Bounds.X+btn.Bounds.W > floatingExpandedWidth {
			t.Fatalf("footer main button %q must be visible inside 480px: %+v", label, btn)
		}
	}

	// Utility actions render as compact 28x28 icon-only controls with full accessible labels.
	for _, label := range []string{"复制图片", "下载位置"} {
		btn, ok := tester.AccessByRole(ui.RoleButton, label)
		if !ok || btn.Offscreen {
			t.Fatalf("footer icon button %q must be accessible as RoleButton: %+v", label, btn)
		}
		if btn.Bounds.W != 28 || btn.Bounds.H != 28 {
			t.Fatalf("footer icon button %q must be 28x28, got %.0fx%.0f", label, btn.Bounds.W, btn.Bounds.H)
		}
		if btn.Bounds.X < 0 || btn.Bounds.X+btn.Bounds.W > floatingExpandedWidth {
			t.Fatalf("footer icon button %q overflowed 480px footer: %+v", label, btn)
		}
	}

	drag, ok := tester.Access("拖出图片")
	if !ok || drag.Offscreen {
		t.Fatalf("footer drag icon must be accessible: %+v", drag)
	}
	if drag.Bounds.W != 28 || drag.Bounds.H != 28 {
		t.Fatalf("footer drag icon must be 28x28, got %.0fx%.0f", drag.Bounds.W, drag.Bounds.H)
	}
	if drag.Bounds.X < 0 || drag.Bounds.X+drag.Bounds.W > floatingExpandedWidth {
		t.Fatalf("footer drag icon overflowed 480px footer: %+v", drag)
	}

	// Verify strict left-to-right ordering, no overlaps, and breathing room between
	// main navigation controls and the icon utility cluster.
	preview, _ := tester.AccessByRole(ui.RoleButton, "预览选中")
	copyBtn, _ := tester.AccessByRole(ui.RoleButton, "复制图片")
	downloads, _ := tester.AccessByRole(ui.RoleButton, "下载位置")
	if gap := copyBtn.Bounds.X - (preview.Bounds.X + preview.Bounds.W); gap < 24 {
		t.Fatalf("expected generous breathing room between navigation and icon group, got %.1fpx", gap)
	}
	if copyBtn.Bounds.X+copyBtn.Bounds.W > drag.Bounds.X || drag.Bounds.X+drag.Bounds.W > downloads.Bounds.X {
		t.Fatalf("footer utility icons must not overlap: copy=%+v drag=%+v downloads=%+v",
			copyBtn.Bounds, drag.Bounds, downloads.Bounds)
	}
}
