package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestBatchThumbnailRechecksFileVersion(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "photo.png")
	writeTestPNG(t, path, 240, 120, false)
	app := newApplication()
	app.batch = newBatchState(nil)
	app.prepareBatchThumbnails()
	entry := app.batchThumbnail(batchItem{path: path})
	updates := make(chan func(), 1)
	app.dispatchUpdate = func(update func()) { updates <- update }
	app.startNextBatchThumbnail()
	nextThumbnailUpdate(t, updates)()
	initialBitmap := entry.bitmap
	initialInfo := entry.info
	if initialBitmap == nil || initialInfo == nil {
		t.Fatal("initial thumbnail must retain a file version")
	}

	entry.checked = time.Time{}
	app.startNextBatchThumbnail()
	nextThumbnailUpdate(t, updates)()
	if entry.bitmap != initialBitmap {
		t.Fatal("an unchanged file must reuse its decoded thumbnail")
	}

	replacement := filepath.Join(root, "replacement.png")
	writeTestPNG(t, replacement, 120, 240, false)
	if err := os.Chtimes(replacement, initialInfo.ModTime(), initialInfo.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	entry.checked = time.Time{}
	app.startNextBatchThumbnail()
	nextThumbnailUpdate(t, updates)()
	if entry.bitmap == initialBitmap || entry.failed {
		t.Fatal("replaced files must refresh the bitmap even with the old modification time")
	}
	width, height := entry.bitmap.Size()
	if width >= height {
		t.Fatalf("expected replacement portrait, got %dx%d", width, height)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	entry.checked = time.Time{}
	app.startNextBatchThumbnail()
	nextThumbnailUpdate(t, updates)()
	if !entry.failed || entry.bitmap != nil {
		t.Fatal("deleted files must discard stale thumbnails")
	}
	writeTestPNG(t, path, 240, 120, false)
	entry.checked = time.Time{}
	app.startNextBatchThumbnail()
	nextThumbnailUpdate(t, updates)()
	if entry.failed || entry.bitmap == nil {
		t.Fatal("restored files must recover from cached failures")
	}
}

func TestBatchThumbnailCancelsDemandOutsideBothSurfaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "photo.png")
	writeTestPNG(t, path, 240, 120, false)
	app := newApplication()
	app.batch = newBatchState(nil)
	app.prepareBatchThumbnails()
	entry := app.batchThumbnail(batchItem{path: path})
	updates := make(chan func(), 1)
	app.dispatchUpdate = func(update func()) { updates <- update }
	release, err := app.imageMemory.acquire(context.Background(), imageMemoryLimit)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	app.startNextBatchThumbnail()
	app.prepareBatchThumbnails()
	app.startNextBatchThumbnail()
	nextThumbnailUpdate(t, updates)()
	if entry.bitmap != nil || entry.failed || !entry.checked.IsZero() || app.thumbnails.active != nil {
		t.Fatal("invisible work must cancel without caching a failure")
	}
	release()
	app.batchThumbnail(batchItem{path: path})
	app.startNextBatchThumbnail()
	nextThumbnailUpdate(t, updates)()
	if entry.bitmap == nil || entry.failed {
		t.Fatal("a canceled thumbnail must load when visible again")
	}
}

func TestBatchThumbnailSurfacesKeepIndependentDemand(t *testing.T) {
	s := newApplication()
	s.batch = newBatchState(nil)
	s.prepareBatchThumbnails()
	mainItem := batchItem{path: "/photos/main.png"}
	floatingItem := batchItem{path: "/photos/floating.png"}
	mainEntry := s.batchThumbnail(mainItem)
	s.prepareFloatingThumbnails()
	floatingEntry := s.floatingThumbnail(floatingItem)
	if shared := s.floatingThumbnail(mainItem); shared != mainEntry {
		t.Fatal("both surfaces must reuse the same entry for a path")
	}
	if !slices.Equal(s.thumbnails.wanted, []string{mainItem.path}) {
		t.Fatal("preparing and requesting floating thumbnails changed main demand")
	}
	s.prepareBatchThumbnails()
	if len(s.thumbnails.wanted) != 0 ||
		!slices.Equal(s.thumbnails.floatingWanted, []string{floatingItem.path, mainItem.path}) {
		t.Fatal("preparing main thumbnails must only clear main demand")
	}
	s.batchThumbnail(mainItem)
	s.prepareFloatingThumbnails()
	if len(s.thumbnails.floatingWanted) != 0 ||
		!slices.Equal(s.thumbnails.wanted, []string{mainItem.path}) {
		t.Fatal("preparing floating thumbnails must only clear floating demand")
	}
	s.floatingThumbnail(floatingItem)
	s.thumbnails.floatingWanted = nil
	if len(s.thumbnails.wanted) != 1 || s.thumbnails.entries[floatingItem.path] != floatingEntry {
		t.Fatal("closing floating must preserve main demand and reusable entries")
	}
	if s.floatingThumbnail(batchItem{}) != nil ||
		s.floatingThumbnail(batchItem{path: "/photos/invalid.png", invalidInput: true}) != nil {
		t.Fatal("floating must reject empty and invalid paths")
	}
}

func TestBatchThumbnailSharedWorkerAlternatesSurfaces(t *testing.T) {
	root := t.TempDir()
	s := newApplication()
	s.batch = newBatchState(nil)
	s.prepareBatchThumbnails()
	s.prepareFloatingThumbnails()
	items := make([]batchItem, 5)
	for index := range items {
		items[index].path = filepath.Join(root, fmt.Sprintf("photo-%d.png", index))
		writeTestPNG(t, items[index].path, 600, 300, false)
	}
	shared := s.batchThumbnail(items[0])
	mainSecond := s.batchThumbnail(items[2])
	mainThird := s.batchThumbnail(items[4])
	if s.floatingThumbnail(items[0]) != shared {
		t.Fatal("shared demand must deduplicate cache entries")
	}
	floatingFirst := s.floatingThumbnail(items[1])
	floatingSecond := s.floatingThumbnail(items[3])
	updates := make(chan func(), len(items))
	s.dispatchUpdate = func(update func()) { updates <- update }
	s.startNextBatchThumbnail()
	for index, entry := range []*batchThumbnail{shared, floatingFirst, mainSecond, floatingSecond, mainThird} {
		if s.thumbnails.active != entry {
			t.Fatalf("decode %d should alternate surfaces without starving main", index)
		}
		s.startNextBatchThumbnail()
		if s.thumbnails.active != entry {
			t.Fatal("both surfaces must share a single active decoder")
		}
		update := nextThumbnailUpdate(t, updates)
		if entry.bitmap != nil {
			t.Fatal("worker results must be applied through the dispatched update")
		}
		update()
		if entry.bitmap == nil || entry.failed {
			t.Fatalf("decode %d did not populate its shared entry", index)
		}
		width, height := entry.bitmap.Size()
		if width > batchThumbnailPixels || height > batchThumbnailPixels {
			t.Fatalf("shared bitmap exceeded thumbnail size: %dx%d", width, height)
		}
	}
	if len(s.thumbnails.entries) != len(items) || s.thumbnails.active != nil {
		t.Fatal("shared requests must decode once and stop after both demands finish")
	}
}

func TestBatchThumbnailFloatingPreparationRejectsStaleOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "photo.png")
	writeTestPNG(t, path, 240, 120, false)
	item := batchItem{path: path}
	s := newApplication()
	s.batch = newBatchState(nil)
	s.prepareBatchThumbnails()
	s.prepareFloatingThumbnails()
	s.batchThumbnail(batchItem{path: "/photos/old-main.png"})
	oldEntry := s.floatingThumbnail(item)
	updates := make(chan func(), 2)
	s.dispatchUpdate = func(update func()) { updates <- update }
	s.thumbnails.floatingNext = true
	s.startNextBatchThumbnail()
	oldUpdate := nextThumbnailUpdate(t, updates)

	s.batch = newBatchState(nil)
	s.prepareFloatingThumbnails()
	if len(s.thumbnails.wanted) != 0 || len(s.thumbnails.floatingWanted) != 0 ||
		len(s.thumbnails.entries) != 0 || s.thumbnails.floatingNext {
		t.Fatal("owner replacement through floating must reset both surfaces")
	}
	newEntry := s.floatingThumbnail(item)
	s.prepareBatchThumbnails()
	if s.batchThumbnail(item) != newEntry {
		t.Fatal("new owner should share entries across surfaces")
	}
	s.startNextBatchThumbnail()
	if s.thumbnails.active != oldEntry {
		t.Fatal("owner replacement must wait for the cancelled decoder to return")
	}
	oldUpdate()
	if oldEntry.bitmap != nil || newEntry.bitmap != nil {
		t.Fatal("stale floating result must not populate either owner's entry")
	}
	nextThumbnailUpdate(t, updates)()
	if newEntry.bitmap == nil || newEntry.failed || s.thumbnails.active != nil {
		t.Fatal("new owner must resume shared decoding after stale work returns")
	}
	s.batch = nil
	s.prepareFloatingThumbnails()
	if s.thumbnails.owner != nil || len(s.thumbnails.entries) != 0 ||
		len(s.thumbnails.wanted) != 0 || len(s.thumbnails.floatingWanted) != 0 {
		t.Fatal("clearing the owner must release both surfaces and their cache")
	}
}

func TestBatchThumbnailSharedCacheStaysBoundedDuringDecode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "active.png")
	writeTestPNG(t, path, 240, 120, false)
	s := newApplication()
	s.batch = newBatchState(nil)
	s.prepareBatchThumbnails()
	s.prepareFloatingThumbnails()
	active := s.floatingThumbnail(batchItem{path: path})
	updates := make(chan func(), 1)
	s.dispatchUpdate = func(update func()) { updates <- update }
	s.startNextBatchThumbnail()
	update := nextThumbnailUpdate(t, updates)
	for index := range maxBatchThumbnails * 3 {
		item := batchItem{path: fmt.Sprintf("/photos/photo-%d.png", index)}
		if index%2 == 0 {
			s.batchThumbnail(item)
		} else {
			s.floatingThumbnail(item)
		}
		if len(s.thumbnails.entries) > maxBatchThumbnails || s.thumbnails.entries[path] != active {
			t.Fatal("mixed demand must respect the shared bound and preserve active decoding")
		}
	}
	recentItem := batchItem{path: "/photos/photo-1.png"}
	recent := s.floatingThumbnail(recentItem)
	s.batchThumbnail(batchItem{path: "/photos/new.png"})
	if s.thumbnails.entries[recentItem.path] != recent {
		t.Fatal("main demand must respect floating LRU usage")
	}
	s.prepareBatchThumbnails()
	s.thumbnails.floatingWanted = nil
	update()
	if active.bitmap == nil || s.thumbnails.active != nil {
		t.Fatal("closing floating must let active decoding finish without scheduling released demand")
	}
}

func nextThumbnailUpdate(t *testing.T, updates <-chan func()) func() {
	t.Helper()
	select {
	case update := <-updates:
		return update
	case <-time.After(5 * time.Second):
		t.Fatal("thumbnail worker did not deliver its result")
		return nil
	}
}

func TestBatchThumbnailCacheIsLazyAndBounded(t *testing.T) {
	s := newApplication()
	items := make([]batchItem, maxBatchFiles)
	for index := range items {
		path := fmt.Sprintf("/photos/image-%04d.png", index)
		items[index] = batchItem{
			path: path, absPath: path, relPath: filepath.Base(path),
			format: "png", width: 1200, height: 800, originalSize: 500_000,
		}
	}
	s.batch = newBatchState(items)
	tester := ui.NewTester(s.view, 880, 660)
	if count := len(s.thumbnails.entries); count == 0 || count > 24 {
		t.Fatalf("only visible rows and overscan should request thumbnails, got %d", count)
	}
	if _, ok := tester.AccessByRole(ui.RoleImage, items[0].relPath+" 缩略图"); !ok {
		t.Fatal("thumbnail card should expose a named image")
	}
	for _, item := range items {
		s.batchThumbnail(item)
		if len(s.thumbnails.entries) > maxBatchThumbnails {
			t.Fatal("thumbnail cache exceeded its memory bound")
		}
	}
	first := items[0]
	recent := s.batchThumbnail(first)
	s.batchThumbnail(batchItem{path: "/photos/new.png"})
	if s.thumbnails.entries[first.path] != recent {
		t.Fatal("least-recently-used eviction discarded a recent thumbnail")
	}
	if s.batchThumbnail(batchItem{path: "/photos/invalid.png", invalidInput: true}) != nil {
		t.Fatal("known invalid inputs must not schedule image decoding")
	}
}

func TestBatchThumbnailWorkerLoadsSeriallyAndRemembersFailures(t *testing.T) {
	root := t.TempDir()
	paths := []string{
		filepath.Join(root, "landscape.png"),
		filepath.Join(root, "portrait.jpg"),
		filepath.Join(root, "missing.png"),
	}
	writeTestPNG(t, paths[0], 600, 300, false)
	writeTestJPEG(t, paths[1], 300, 600, 90)
	s := newApplication()
	s.batch = newBatchState(nil)
	s.prepareBatchThumbnails()
	entries := make([]*batchThumbnail, len(paths))
	for index, path := range paths {
		entries[index] = s.batchThumbnail(batchItem{path: path})
	}
	updates := make(chan func(), len(paths))
	s.dispatchUpdate = func(update func()) { updates <- update }
	s.startNextBatchThumbnail()
	firstActive := s.thumbnails.active
	s.startNextBatchThumbnail()
	if firstActive != entries[0] || s.thumbnails.active != firstActive {
		t.Fatal("repeated scheduling must keep exactly one decoder active")
	}
	for index, entry := range entries {
		nextThumbnailUpdate(t, updates)()
		if index == 2 {
			if !entry.failed || entry.bitmap != nil {
				t.Fatal("failed thumbnails should have a cached fallback")
			}
			continue
		}
		if entry.failed || entry.bitmap == nil {
			t.Fatalf("thumbnail %d was not loaded", index)
		}
		width, height := entry.bitmap.Size()
		if width > batchThumbnailPixels || height > batchThumbnailPixels {
			t.Fatalf("full-size source retained in cache: %dx%d", width, height)
		}
	}
	if s.thumbnails.active != nil {
		t.Fatal("decoder should stop after completing the requested thumbnails")
	}
	s.startNextBatchThumbnail()
	if s.thumbnails.active != nil {
		t.Fatal("completed or failed requests must not decode repeatedly")
	}
}

func TestBatchThumbnailRejectsResultsFromReplacedQueue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "photo.png")
	writeTestPNG(t, path, 240, 120, false)
	item := batchItem{path: path, absPath: path, relPath: "photo.png"}
	s := newApplication()
	s.batch = newBatchState([]batchItem{item})
	s.prepareBatchThumbnails()
	oldEntry := s.batchThumbnail(item)
	s.prepareFloatingThumbnails()
	s.floatingThumbnail(batchItem{path: "/photos/old-floating.png"})
	updates := make(chan func(), 2)
	s.dispatchUpdate = func(update func()) { updates <- update }
	s.startNextBatchThumbnail()
	oldUpdate := nextThumbnailUpdate(t, updates)

	s.batch = newBatchState([]batchItem{item})
	s.prepareBatchThumbnails()
	if len(s.thumbnails.wanted) != 0 || len(s.thumbnails.floatingWanted) != 0 {
		t.Fatal("owner replacement through main must clear demand from both surfaces")
	}
	newEntry := s.batchThumbnail(item)
	s.startNextBatchThumbnail()
	if s.thumbnails.active != oldEntry {
		t.Fatal("replacing a queue must wait for the old decoder to return")
	}
	oldUpdate()
	if oldEntry.bitmap != nil || newEntry.bitmap != nil {
		t.Fatal("old queue result must not populate either cache")
	}
	nextThumbnailUpdate(t, updates)()
	if newEntry.bitmap == nil || newEntry.failed || s.thumbnails.active != nil {
		t.Fatal("new queue thumbnail should load after discarding stale work")
	}
	s.batch = nil
	s.prepareBatchThumbnails()
	if len(s.thumbnails.entries) != 0 || s.thumbnails.owner != nil {
		t.Fatal("clearing the queue should release cached thumbnails")
	}
}

func TestBatchThumbnailRefreshesAfterOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "photo.png")
	writeTestPNG(t, path, 240, 120, false)
	item := batchItem{path: path, absPath: path, relPath: "photo.png"}
	s := newApplication()
	s.batch = newBatchState([]batchItem{item})
	s.prepareBatchThumbnails()
	oldEntry := s.batchThumbnail(item)
	updates := make(chan func(), 2)
	s.dispatchUpdate = func(update func()) { updates <- update }
	s.startNextBatchThumbnail()
	oldUpdate := nextThumbnailUpdate(t, updates)

	writeTestPNG(t, path, 120, 240, false)
	s.applyBatchItemUpdate(batchItemUpdate{index: 0, status: batchDone, overwritten: true})
	s.prepareBatchThumbnails()
	newEntry := s.batchThumbnail(s.batch.items[0])
	oldUpdate()
	if oldEntry.bitmap != nil || newEntry.bitmap != nil {
		t.Fatal("overwriting must discard the in-flight original thumbnail")
	}
	nextThumbnailUpdate(t, updates)()
	if newEntry.bitmap == nil {
		t.Fatal("replacement thumbnail did not load")
	}
	width, height := newEntry.bitmap.Size()
	if width >= height {
		t.Fatalf("expected updated portrait thumbnail, got %dx%d", width, height)
	}
}

func TestBatchThumbnailRecoversAfterSuccessfulRetry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "restored.png")
	item := batchItem{path: path, absPath: path, relPath: "restored.png", status: batchFailed}
	s := newApplication()
	s.batch = newBatchState([]batchItem{item})
	s.prepareBatchThumbnails()
	failedEntry := s.batchThumbnail(item)
	updates := make(chan func(), 2)
	s.dispatchUpdate = func(update func()) { updates <- update }
	s.startNextBatchThumbnail()
	nextThumbnailUpdate(t, updates)()
	if !failedEntry.failed {
		t.Fatal("missing source should initially show the thumbnail fallback")
	}

	writeTestPNG(t, path, 240, 120, false)
	s.applyBatchItemUpdate(batchItemUpdate{index: 0, status: batchDone})
	s.prepareBatchThumbnails()
	freshEntry := s.batchThumbnail(s.batch.items[0])
	if freshEntry == failedEntry {
		t.Fatal("successful retry must invalidate a failed thumbnail")
	}
	s.startNextBatchThumbnail()
	nextThumbnailUpdate(t, updates)()
	if freshEntry.failed || freshEntry.bitmap == nil {
		t.Fatal("restored source should show a thumbnail after successful retry")
	}
}

func TestBatchThumbnailCardsKeepSelectionAndMinimumLayout(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprintf("dark=%v", dark), func(t *testing.T) {
			s := newApplication()
			s.batch = newBatchState([]batchItem{
				{
					path: "/p/first.png", absPath: "/p/first.png",
					relPath: "very-long-filename-with-important-suffix-first.png",
					format:  "png", width: 1200, height: 800, originalSize: 2 << 20,
					status: batchDone, outputSize: 1 << 20,
				},
				{
					path: "/p/second.jpg", absPath: "/p/second.jpg", relPath: "second.jpg",
					format: "jpeg", width: 800, height: 1200, originalSize: 500_000,
				},
			})
			s.batch.outputDir = t.TempDir()
			tester := ui.NewTester(func(c *ui.Context) {
				if dark {
					c.SetTheme(ui.DarkTheme())
				} else {
					c.SetTheme(ui.LightTheme())
				}
				s.view(c)
			}, 880, 660)
			list, ok := tester.AccessByRole(ui.RoleList, "批量图片列表")
			if !ok {
				t.Fatal("queue must retain native list semantics")
			}
			var previousBottom float32
			for _, item := range s.batch.items {
				thumbnail, ok := tester.AccessByRole(ui.RoleImage, item.relPath+" 缩略图")
				if !ok || thumbnail.Offscreen || thumbnail.Bounds.W < 60 ||
					thumbnail.Bounds.H < 60 || thumbnail.Bounds.X < list.Bounds.X ||
					thumbnail.Bounds.X+thumbnail.Bounds.W > list.Bounds.X+list.Bounds.W ||
					thumbnail.Bounds.Y < previousBottom {
					t.Fatalf("thumbnail cards must be stacked inside the list: %+v", thumbnail)
				}
				previousBottom = thumbnail.Bounds.Y + thumbnail.Bounds.H
			}
			if err := tester.Click(s.batch.items[0].relPath); err != nil {
				t.Fatal(err)
			}
			tester.Key(0, ui.KeyDown)
			if s.batch.selected != 1 {
				t.Fatal("arrow navigation must select the next thumbnail card")
			}
			tester.Key(0, ui.KeyDelete)
			if len(s.batch.items) != 1 || s.batch.items[0].absPath != "/p/first.png" {
				t.Fatal("Delete must remove the selected thumbnail card")
			}
		})
	}
}
