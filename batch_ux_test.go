package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func clickContextMenuItem(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	info, ok := tt.AccessByRole(ui.RoleMenuItem, name)
	if !ok {
		t.Fatalf("context menu item %q not found", name)
	}
	tt.ClickAt(info.Bounds.X+info.Bounds.W/2, info.Bounds.Y+info.Bounds.H/2)
}

func TestRemoveCompletedBatchItemsMixedAndSelection(t *testing.T) {
	s := newApplication()
	s.batch = newBatchState([]batchItem{
		{path: "/p/done1.jpg", absPath: "/p/done1.jpg", relPath: "done1.jpg", format: "jpeg", width: 40, height: 40, originalSize: 4000, outputSize: 2000, status: batchDone, savedPath: "/out/done1.jpg"},
		{path: "/p/pending.jpg", absPath: "/p/pending.jpg", relPath: "pending.jpg", format: "jpeg", width: 40, height: 40, originalSize: 3000, status: batchPending},
		{path: "/p/done2.jpg", absPath: "/p/done2.jpg", relPath: "done2.jpg", format: "jpeg", width: 40, height: 40, originalSize: 5000, outputSize: 3000, status: batchDone, savedPath: "/out/done2.jpg", targetSizeMissed: true, targetSizeBytes: 1024},
		{path: "/p/failed.jpg", absPath: "/p/failed.jpg", relPath: "failed.jpg", format: "jpeg", width: 40, height: 40, originalSize: 2000, status: batchFailed, errMsg: "写入失败"},
	})
	s.batch.selected = 1
	s.batch.previewOpen = true
	s.batch.previewIndex = 1
	s.thumbnails.owner = s.batch
	s.thumbnails.entries = map[string]*batchThumbnail{
		"/p/done1.jpg":   {},
		"/p/pending.jpg": {},
		"/p/done2.jpg":   {},
	}

	s.removeCompletedBatchItems()

	if s.batch == nil || len(s.batch.items) != 2 {
		t.Fatalf("expected 2 non-done items to remain, got %+v", s.batch)
	}
	if s.batch.items[0].relPath != "pending.jpg" || s.batch.items[1].relPath != "failed.jpg" {
		t.Fatalf("unexpected remaining items: %+v", s.batch.items)
	}
	if s.batch.selected != 0 {
		t.Fatalf("expected selection to stay on pending.jpg at index 0, got %d", s.batch.selected)
	}
	if !s.batch.previewOpen || s.batch.previewIndex != 0 {
		t.Fatalf("expected preview to follow pending.jpg to index 0, got open=%v idx=%d", s.batch.previewOpen, s.batch.previewIndex)
	}
	if _, exists := s.thumbnails.entries["/p/done1.jpg"]; exists {
		t.Fatal("removed done1 thumbnail must be invalidated")
	}
	if _, exists := s.thumbnails.entries["/p/done2.jpg"]; exists {
		t.Fatal("removed done2 thumbnail must be invalidated")
	}
	if _, exists := s.thumbnails.entries["/p/pending.jpg"]; !exists {
		t.Fatal("kept pending thumbnail must remain cached")
	}
	if s.failed || !strings.Contains(s.message, "已清除 2 张已完成图片") {
		t.Fatalf("unexpected status message: failed=%v msg=%q", s.failed, s.message)
	}
}

func TestRemoveCompletedBatchItemsSelectedDoneAndFilterReset(t *testing.T) {
	s := newApplication()
	s.batch = newBatchState([]batchItem{
		{path: "/p/done1.jpg", absPath: "/p/done1.jpg", relPath: "done1.jpg", status: batchDone, savedPath: "/out/done1.jpg"},
		{path: "/p/pending.jpg", absPath: "/p/pending.jpg", relPath: "pending.jpg", status: batchPending},
	})
	s.batch.selected = 0
	s.batch.previewOpen = true
	s.batch.previewIndex = 0
	s.batch.filter = batchFilterDone

	s.removeCompletedBatchItems()

	if s.batch == nil || len(s.batch.items) != 1 || s.batch.items[0].relPath != "pending.jpg" {
		t.Fatalf("expected pending.jpg to remain, got %+v", s.batch)
	}
	if s.batch.previewOpen {
		t.Fatal("preview of removed done item must be closed")
	}
	if s.batch.filter != batchFilterAll {
		t.Fatalf("filter must reset from 已完成 to 全部, got %d", s.batch.filter)
	}
	if s.batch.selected != 0 {
		t.Fatalf("expected remaining item to be selected, got %d", s.batch.selected)
	}
}

func TestRemoveCompletedBatchItemsAllDoneClearsBatch(t *testing.T) {
	s := newApplication()
	s.batch = newBatchState([]batchItem{
		{path: "/p/a.jpg", absPath: "/p/a.jpg", relPath: "a.jpg", status: batchDone, savedPath: "/out/a.jpg"},
		{path: "/p/b.jpg", absPath: "/p/b.jpg", relPath: "b.jpg", status: batchDone, savedPath: "/out/b.jpg"},
	})
	s.batch.previewOpen = true
	s.batch.previewIndex = 0
	s.thumbnails.owner = s.batch
	s.thumbnails.entries = map[string]*batchThumbnail{
		"/p/a.jpg": {},
		"/p/b.jpg": {},
	}

	s.removeCompletedBatchItems()

	if s.batch != nil {
		t.Fatalf("expected batch to be cleared when all items are done, got %+v", s.batch)
	}
	if len(s.thumbnails.entries) != 0 {
		t.Fatalf("expected thumbnail cache to be cleared, got %d entries", len(s.thumbnails.entries))
	}
	if s.failed || !strings.Contains(s.message, "已清除 2 张已完成图片") {
		t.Fatalf("unexpected status message: failed=%v msg=%q", s.failed, s.message)
	}
}

func TestRemoveCompletedBatchItemsGuards(t *testing.T) {
	s := newApplication()
	s.removeCompletedBatchItems() // nil batch is a safe no-op

	s.batch = newBatchState([]batchItem{
		{path: "/p/done.jpg", absPath: "/p/done.jpg", relPath: "done.jpg", status: batchDone, savedPath: "/out/done.jpg"},
		{path: "/p/pending.jpg", absPath: "/p/pending.jpg", relPath: "pending.jpg", status: batchPending},
	})

	s.batch.running = true
	s.removeCompletedBatchItems()
	if len(s.batch.items) != 2 {
		t.Fatalf("running guard must block removeCompletedBatchItems: len=%d", len(s.batch.items))
	}
	s.batch.running = false

	s.busy = true
	s.removeCompletedBatchItems()
	if len(s.batch.items) != 2 {
		t.Fatalf("busy guard must block removeCompletedBatchItems: len=%d", len(s.batch.items))
	}
	s.busy = false

	s.confirmBatchOverwrite = true
	s.removeCompletedBatchItems()
	if len(s.batch.items) != 2 {
		t.Fatalf("modal guard must block removeCompletedBatchItems: len=%d", len(s.batch.items))
	}
	s.confirmBatchOverwrite = false

	s.batch.items[0].status = batchPending
	s.setMessage("保留原消息", false)
	s.removeCompletedBatchItems()
	if len(s.batch.items) != 2 || s.message != "保留原消息" {
		t.Fatalf("no done items should be a no-op: len=%d msg=%q", len(s.batch.items), s.message)
	}
}

func TestBatchHeaderRemoveDoneButtonUI(t *testing.T) {
	s := newApplication()
	s.batch = newBatchState([]batchItem{
		{path: "/p/a.jpg", absPath: "/p/a.jpg", relPath: "a.jpg", format: "jpeg", width: 40, height: 40, originalSize: 4000, status: batchPending},
		{path: "/p/b.jpg", absPath: "/p/b.jpg", relPath: "b.jpg", format: "jpeg", width: 40, height: 40, originalSize: 4000, status: batchPending},
	})
	tt := ui.NewTester(s.view, 880, 660)
	if _, ok := tt.AccessByRole(ui.RoleButton, "清除已完成"); ok {
		t.Fatal("清除已完成 must not appear when no items are done")
	}

	s.batch.items[0].status = batchDone
	s.batch.items[0].outputSize = 2000
	s.batch.items[0].savedPath = "/out/a.jpg"
	s.batch.selected = 1
	tt.Frame()

	for _, size := range [][2]int{{520, 660}, {760, 660}, {880, 660}, {1100, 760}} {
		tt.SetSize(size[0], size[1])
		tt.Frame()
		btn, ok := tt.AccessByRole(ui.RoleButton, "清除已完成")
		if !ok {
			t.Fatalf("width %d: 清除已完成 button missing", size[0])
		}
		if btn.Disabled || btn.Offscreen || btn.Bounds.H < 28 || btn.Bounds.X+btn.Bounds.W > float32(size[0]) {
			t.Fatalf("width %d: 清除已完成 button must fit tidily: %+v", size[0], btn)
		}
	}

	// Disabled while running or busy (modalOpen).
	tt.SetSize(880, 660)
	s.batch.running = true
	tt.Frame()
	if btn, ok := tt.AccessByRole(ui.RoleButton, "清除已完成"); !ok || !btn.Disabled {
		t.Fatalf("清除已完成 must be disabled while running: ok=%v btn=%+v", ok, btn)
	}
	s.batch.running = false
	s.busy = true
	tt.Frame()
	if btn, ok := tt.AccessByRole(ui.RoleButton, "清除已完成"); !ok || !btn.Disabled {
		t.Fatalf("清除已完成 must be disabled while busy: ok=%v btn=%+v", ok, btn)
	}
	s.busy = false
	tt.Frame()

	// Click removes done items and preserves selection on b.jpg.
	if err := tt.Click("清除已完成"); err != nil {
		t.Fatal(err)
	}
	if s.batch == nil || len(s.batch.items) != 1 || s.batch.items[0].relPath != "b.jpg" || s.batch.selected != 0 {
		t.Fatalf("expected only b.jpg selected after clicking 清除已完成, got %+v", s.batch)
	}
	if _, ok := tt.AccessByRole(ui.RoleButton, "清除已完成"); ok {
		t.Fatal("清除已完成 must hide once done count reaches 0")
	}

	// Clicking 清除已完成 when all remaining items are done clears batch cleanly.
	s.batch.items[0].status = batchDone
	s.batch.items[0].outputSize = 1800
	s.batch.items[0].savedPath = "/out/b.jpg"
	tt.Frame()
	if err := tt.Click("清除已完成"); err != nil {
		t.Fatal(err)
	}
	if s.batch != nil {
		t.Fatalf("expected batch cleared when all items done, got %+v", s.batch)
	}
	if !strings.Contains(s.message, "已清除 1 张已完成图片") {
		t.Fatalf("expected clear message, got %q", s.message)
	}
}

func TestBatchRowContextMenuItemsByState(t *testing.T) {
	root := t.TempDir()
	pendingPath := filepath.Join(root, "pending.jpg")
	donePath := filepath.Join(root, "done.jpg")
	doneSavedPath := filepath.Join(root, "done-out.jpg")
	failedPath := filepath.Join(root, "failed.jpg")
	invalidPath := filepath.Join(root, "broken.png")
	writeTestJPEG(t, pendingPath, 80, 60, 95)
	writeTestJPEG(t, donePath, 80, 60, 95)
	writeTestJPEG(t, doneSavedPath, 80, 60, 60)
	writeTestJPEG(t, failedPath, 80, 60, 95)
	if err := os.WriteFile(invalidPath, []byte("not-a-png"), 0o600); err != nil {
		t.Fatal(err)
	}

	var revealed []string
	s := newApplication()
	s.showInFolder = func(path string) { revealed = append(revealed, path) }
	s.batch = newBatchState([]batchItem{
		{path: pendingPath, absPath: pendingPath, relPath: "pending.jpg", format: "jpeg", width: 80, height: 60, originalSize: 4000, status: batchPending},
		{path: donePath, absPath: donePath, relPath: "done.jpg", format: "jpeg", width: 80, height: 60, originalSize: 4000, outputSize: 2000, status: batchDone, savedPath: doneSavedPath},
		{path: failedPath, absPath: failedPath, relPath: "failed.jpg", format: "jpeg", width: 80, height: 60, originalSize: 4000, status: batchFailed, errMsg: "写入失败"},
		{path: invalidPath, absPath: invalidPath, relPath: "broken.png", format: "png", status: batchFailed, invalidInput: true, errMsg: "无法解码"},
	})
	s.batch.selected = 0

	tt := ui.NewTester(s.view, 960, 680)
	tt.UseScreenReader()

	// 1) Right-clicking done.jpg selects index 1 and shows done-state menu items.
	if err := tt.RightClick("done.jpg"); err != nil {
		t.Fatal(err)
	}
	if s.batch.selected != 1 {
		t.Fatalf("right-clicking done.jpg must select row 1, got %d", s.batch.selected)
	}
	for _, want := range []string{"预览", "定位文件", "复制图片", "从队列移除"} {
		if _, ok := tt.AccessByRole(ui.RoleMenuItem, want); !ok {
			t.Fatalf("done row context menu missing %q", want)
		}
	}
	for _, unwanted := range []string{"开始压缩", "重新压缩"} {
		if _, ok := tt.AccessByRole(ui.RoleMenuItem, unwanted); ok {
			t.Fatalf("done row context menu must not show %q", unwanted)
		}
	}
	clickContextMenuItem(t, tt, "定位文件")
	if len(revealed) != 1 || revealed[0] != doneSavedPath {
		t.Fatalf("expected 定位文件 on done row to reveal savedPath %q, got %v", doneSavedPath, revealed)
	}

	// 2) Right-clicking pending.jpg selects index 0 and shows pending-state menu items.
	if err := tt.RightClick("pending.jpg"); err != nil {
		t.Fatal(err)
	}
	if s.batch.selected != 0 {
		t.Fatalf("right-clicking pending.jpg must select row 0, got %d", s.batch.selected)
	}
	for _, want := range []string{"预览", "开始压缩", "定位文件", "从队列移除"} {
		if _, ok := tt.AccessByRole(ui.RoleMenuItem, want); !ok {
			t.Fatalf("pending row context menu missing %q", want)
		}
	}
	for _, unwanted := range []string{"重新压缩", "复制图片"} {
		if _, ok := tt.AccessByRole(ui.RoleMenuItem, unwanted); ok {
			t.Fatalf("pending row context menu must not show %q", unwanted)
		}
	}
	clickContextMenuItem(t, tt, "定位文件")
	if len(revealed) != 2 || revealed[1] != pendingPath {
		t.Fatalf("expected 定位文件 on pending row to reveal source path %q, got %v", pendingPath, revealed)
	}

	// 3) Right-clicking failed.jpg selects index 2 and shows failed-state menu items.
	if err := tt.RightClick("failed.jpg"); err != nil {
		t.Fatal(err)
	}
	if s.batch.selected != 2 {
		t.Fatalf("right-clicking failed.jpg must select row 2, got %d", s.batch.selected)
	}
	for _, want := range []string{"预览", "重新压缩", "定位文件", "从队列移除"} {
		if _, ok := tt.AccessByRole(ui.RoleMenuItem, want); !ok {
			t.Fatalf("failed row context menu missing %q", want)
		}
	}
	for _, unwanted := range []string{"开始压缩", "复制图片"} {
		if _, ok := tt.AccessByRole(ui.RoleMenuItem, unwanted); ok {
			t.Fatalf("failed row context menu must not show %q", unwanted)
		}
	}
	tt.Key(0, ui.KeyEscape)

	// 4) Right-clicking broken.png selects index 3 and omits 预览/压缩/复制.
	if err := tt.RightClick("broken.png"); err != nil {
		t.Fatal(err)
	}
	if s.batch.selected != 3 {
		t.Fatalf("right-clicking broken.png must select row 3, got %d", s.batch.selected)
	}
	for _, want := range []string{"定位文件", "从队列移除"} {
		if _, ok := tt.AccessByRole(ui.RoleMenuItem, want); !ok {
			t.Fatalf("invalid row context menu missing %q", want)
		}
	}
	for _, unwanted := range []string{"预览", "开始压缩", "重新压缩", "复制图片"} {
		if _, ok := tt.AccessByRole(ui.RoleMenuItem, unwanted); ok {
			t.Fatalf("invalid row context menu must not show %q", unwanted)
		}
	}
	tt.Key(0, ui.KeyEscape)

	// 5) When s.batch.running is true, 压缩 actions are hidden and 从队列移除 is disabled.
	s.batch.running = true
	tt.Frame()
	if err := tt.RightClick("pending.jpg"); err != nil {
		t.Fatal(err)
	}
	if _, ok := tt.AccessByRole(ui.RoleMenuItem, "开始压缩"); ok {
		t.Fatal("开始压缩 must be hidden while running")
	}
	removeItem, ok := tt.AccessByRole(ui.RoleMenuItem, "从队列移除")
	if !ok || !removeItem.Disabled {
		t.Fatalf("从队列移除 must be disabled while running: ok=%v info=%+v", ok, removeItem)
	}
	tt.ClickAt(removeItem.Bounds.X+removeItem.Bounds.W/2, removeItem.Bounds.Y+removeItem.Bounds.H/2)
	if len(s.batch.items) != 4 {
		t.Fatalf("clicking disabled 从队列移除 must not remove item: len=%d", len(s.batch.items))
	}
	tt.Key(0, ui.KeyEscape)
	s.batch.running = false

	// 6) When modalOpen is true (s.busy), right-clicking does not open a context menu.
	s.busy = true
	tt.Frame()
	if err := tt.RightClick("pending.jpg"); err != nil {
		t.Fatal(err)
	}
	if _, ok := tt.AccessByRole(ui.RoleMenuItem, "从队列移除"); ok {
		t.Fatal("context menu must not open while modalOpen is true")
	}
	s.busy = false
}

func TestBatchRowContextMenuActions(t *testing.T) {
	root := t.TempDir()
	outputDir := filepath.Join(root, "out")
	pendingPath := filepath.Join(root, "pending.jpg")
	failedPath := filepath.Join(root, "failed.jpg")
	donePath := filepath.Join(root, "done.jpg")
	doneOutPath := filepath.Join(outputDir, "done.jpg")
	writeTestJPEG(t, pendingPath, 140, 100, 100)
	writeTestJPEG(t, failedPath, 140, 100, 100)
	writeTestJPEG(t, donePath, 100, 80, 100)
	doneOutData := writeTestJPEG(t, doneOutPath, 100, 80, 60)

	scan, err := scanBatchPaths(context.Background(), []string{pendingPath, failedPath, donePath}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	updates := make(chan func(), 16)
	drainUpdates := func(waitCond func() bool) {
		t.Helper()
		deadline := time.NewTimer(5 * time.Second)
		defer deadline.Stop()
		for waitCond() {
			select {
			case fn := <-updates:
				fn()
			case <-deadline.C:
				t.Fatal("timed out waiting for async batch action")
			}
		}
	}

	var copiedPNG []byte
	s := newApplication()
	s.presets = testLegacyPresets()
	s.dispatchUpdate = func(fn func()) { updates <- fn }
	s.writeClipboardImage = func(data []byte) error {
		copiedPNG = append([]byte(nil), data...)
		return nil
	}
	s.setScheme("体积优先")
	s.batch = newBatchState(scan.items)
	s.batch.outputMode = batchOutputFolder
	s.batch.outputDir = outputDir
	s.batch.items[1].status = batchFailed
	s.batch.items[1].errMsg = "上次失败"
	s.batch.items[2].status = batchDone
	s.batch.items[2].savedPath = doneOutPath
	s.batch.items[2].outputSize = int64(len(doneOutData))
	t.Cleanup(s.saves.Wait)

	tt := ui.NewTester(s.view, 960, 680)
	tt.UseScreenReader()

	// Action 1: 预览 from context menu opens batch preview for done.jpg.
	if err := tt.RightClick("done.jpg"); err != nil {
		t.Fatal(err)
	}
	clickContextMenuItem(t, tt, "预览")
	drainUpdates(func() bool { return s.busy })
	tt.Frame()
	if !s.batch.previewOpen || s.batch.previewIndex != 2 {
		t.Fatalf("expected 预览 context action to open preview for index 2, got open=%v idx=%d", s.batch.previewOpen, s.batch.previewIndex)
	}
	s.closeBatchPreview()
	tt.Frame()

	// Action 2: 开始压缩 from context menu compresses only pending.jpg.
	if err := tt.RightClick("pending.jpg"); err != nil {
		t.Fatal(err)
	}
	clickContextMenuItem(t, tt, "开始压缩")
	drainUpdates(func() bool { return s.busy })
	tt.Frame()
	if s.batch.items[0].status != batchDone {
		t.Fatalf("expected pending.jpg to complete via context menu 开始压缩, got status=%v", s.batch.items[0].status)
	}
	if s.batch.items[1].status != batchFailed {
		t.Fatalf("expected failed.jpg to remain untouched, got status=%v", s.batch.items[1].status)
	}

	// Action 3: 重新压缩 from context menu retries failed.jpg.
	if err := tt.RightClick("failed.jpg"); err != nil {
		t.Fatal(err)
	}
	clickContextMenuItem(t, tt, "重新压缩")
	drainUpdates(func() bool { return s.busy })
	tt.Frame()
	if s.batch.items[1].status != batchDone {
		t.Fatalf("expected failed.jpg to complete via context menu 重新压缩, got status=%v", s.batch.items[1].status)
	}

	// Action 4: 复制图片 from context menu copies output image for done.jpg.
	if err := tt.RightClick("done.jpg"); err != nil {
		t.Fatal(err)
	}
	clickContextMenuItem(t, tt, "复制图片")
	drainUpdates(func() bool { return s.busy })
	tt.Frame()
	if s.failed || len(copiedPNG) == 0 {
		t.Fatalf("expected 复制图片 to succeed with PNG bytes, failed=%v len=%d msg=%q", s.failed, len(copiedPNG), s.message)
	}

	// Action 5: 从队列移除 removes rows one by one cleanly down to empty batch.
	for _, name := range []string{"failed.jpg", "pending.jpg", "done.jpg"} {
		if err := tt.RightClick(name); err != nil {
			t.Fatalf("right-click %s: %v", name, err)
		}
		clickContextMenuItem(t, tt, "从队列移除")
		tt.Frame()
	}
	if s.batch != nil {
		t.Fatalf("expected batch to become nil after removing all items via context menu, got %+v", s.batch)
	}
}
