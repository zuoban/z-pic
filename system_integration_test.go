package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"zpic/internal/compress"
)

func TestNewDesktopApplicationRestoresSettings(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	t.Setenv("ZPIC_CONFIG_FILE", settingsPath)
	downloadDir := t.TempDir()
	batchOutputDir := t.TempDir()
	presets := []compressionPreset{{
		name:    "启动测试预设",
		quality: 57,
		png:     compress.PNGOptions{MinQuality: 45, MaxQuality: 65, Colors: 128, Speed: 5},
	}}
	if err := saveFullSettings(settingsPath, appSettings{
		presets:        presets,
		selected:       presets[0].name,
		batchOutputDir: batchOutputDir,
		theme:          themeDark,
		autoCompress:   true,
		backupToTrash:  true,
		downloadDir:    downloadDir,
	}); err != nil {
		t.Fatal(err)
	}

	app := newDesktopApplication()
	if app.settingsFile != settingsPath || app.settingsError != "" {
		t.Fatalf("startup settings: path=%q, error=%q", app.settingsFile, app.settingsError)
	}
	if app.effectiveThemeMode() != themeDark {
		t.Errorf("startup theme = %q, want %q", app.effectiveThemeMode(), themeDark)
	}
	if app.scheme != presets[0].name || app.quality != presets[0].quality || app.png != presets[0].png || !slices.Equal(app.presets, presets) {
		t.Errorf("startup preset: scheme=%q, quality=%v, png=%+v, presets=%+v", app.scheme, app.quality, app.png, app.presets)
	}
	if !app.autoCompress || !app.backupToTrash {
		t.Errorf("startup options: autoCompress=%v, backupToTrash=%v", app.autoCompress, app.backupToTrash)
	}
	if app.downloadDir != downloadDir || app.batchOutputDir != batchOutputDir {
		t.Errorf("startup directories: download=%q, batch output=%q", app.downloadDir, app.batchOutputDir)
	}
}

func TestSingleDragFilenameUsesDefaultSuffix(t *testing.T) {
	app := newApplication()
	app.file = &selectedImage{path: "hero.jpg"}
	app.output = presetSaveOptions{UseSuffix: true}
	if got := app.singleDragFilename(); got != "hero-zpic.jpg" {
		t.Fatalf("singleDragFilename() = %q, want hero-zpic.jpg", got)
	}
}

func TestStageSingleDragFileRemovesPreviousSlot(t *testing.T) {
	app := newApplication()
	app.dragTempDir = t.TempDir()
	app.result = &compress.Result{Format: "jpeg", Data: []byte("first result")}
	first := app.stageSingleDragFile()
	firstSlot := filepath.Dir(first)
	if !strings.HasPrefix(filepath.Base(firstSlot), "export-") {
		t.Fatalf("unexpected first staged path %q: %s", first, app.message)
	}
	if got := app.stageSingleDragFile(); got != first {
		t.Fatalf("staging the same result returned %q, want %q", got, first)
	}

	app.result = &compress.Result{Format: "jpeg", Data: []byte("second result")}
	second := app.stageSingleDragFile()
	if second == "" || filepath.Dir(second) == firstSlot {
		t.Fatalf("second result must use a new slot: first=%q, second=%q, message=%q", first, second, app.message)
	}
	if _, err := os.Stat(firstSlot); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("previous slot %q still exists or could not be checked: %v", firstSlot, err)
	}
	if data, err := os.ReadFile(second); err != nil || !bytes.Equal(data, app.result.Data) {
		t.Errorf("second staged file: data=%q, err=%v", data, err)
	}
}

func TestDragOutSingleStagingSavedReuseAndSelfDropGuard(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "hero.jpg")
	orig := writeTestJPEG(t, src, 120, 90, 100)
	res, err := compress.File(context.Background(), src, compress.Options{
		Quality: 60,
	})
	if err != nil {
		t.Fatal(err)
	}

	app := newApplication()
	defer app.cleanupDragStaging()
	app.file = &selectedImage{path: src, format: "jpeg", width: 120, height: 90, size: int64(len(orig)), data: orig}
	app.result = res
	app.output = presetSaveOptions{UseSuffix: true, Suffix: "-min"}

	// Verify WebP format extension renaming independently of bundled cwebp.
	app.result = &compress.Result{Format: "webp", Data: res.Data}
	if got := app.singleDragFilename(); got != "hero-min.webp" {
		t.Fatalf("expected hero-min.webp for webp result, got %q", got)
	}
	app.result = res

	// Unsaved result stages a clean filename in a managed temp dir.
	data := app.singleDragExportData()
	if !app.draggingOut {
		t.Fatal("starting drag export must set draggingOut guard")
	}
	files, err := data.Files()
	if err != nil || len(files) != 1 {
		t.Fatalf("expected 1 staged drag file, got %v (err=%v)", files, err)
	}
	staged := files[0]
	if filepath.Base(staged) != "hero-min.jpg" {
		t.Fatalf("unexpected staged filename %q, want hero-min.jpg", filepath.Base(staged))
	}
	gotBytes, err := os.ReadFile(staged)
	if err != nil || !bytes.Equal(gotBytes, res.Data) {
		t.Fatalf("staged drag content mismatch: err=%v", err)
	}

	// Self-drop while draggingOut must be ignored on both dropFiles and receiveDrop.
	app.dropFiles([]string{src})
	app.receiveDrop(data)
	if app.batch != nil || len(app.dropQueue) != 0 {
		t.Fatal("self-drop while draggingOut must be ignored")
	}

	// Done callback clears draggingOut.
	app.dragExportOptions().Done(transfer.Result{Operation: transfer.Copy})
	if app.draggingOut {
		t.Fatal("Done must clear draggingOut")
	}

	// Even after Done, dropping a path inside dragTempDir back onto Z Pic must be ignored.
	app.dropFiles([]string{staged})
	if app.batch != nil || len(app.dropQueue) != 0 {
		t.Fatal("staged drag temp file must never be re-imported")
	}

	// When already saved, drag export reuses lastSavedPath without staging another file.
	saved := filepath.Join(dir, "saved.jpg")
	if err := res.Save(saved); err != nil {
		t.Fatal(err)
	}
	app.lastSavedPath = saved
	savedData := app.singleDragExportData()
	app.dragExportOptions().Done(transfer.Result{Operation: transfer.Copy})
	savedFiles, err := savedData.Files()
	if err != nil || len(savedFiles) != 1 || savedFiles[0] != saved {
		t.Fatalf("saved result must drag saved path directly: got %v (err=%v)", savedFiles, err)
	}

	tempDir := app.dragTempDir
	app.cleanupDragStaging()
	if _, err := os.Stat(tempDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cleanupDragStaging must remove %s, got err=%v", tempDir, err)
	}
}

func TestDragOutBatchItemAndAllDoneOutputs(t *testing.T) {
	dir := t.TempDir()
	outA := filepath.Join(dir, "a-out.jpg")
	outB := filepath.Join(dir, "b-out.jpg")
	pending := filepath.Join(dir, "c.jpg")
	for _, p := range []string{outA, outB, pending} {
		if err := os.WriteFile(p, []byte("img"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	app := newApplication()
	app.batch = newBatchState([]batchItem{
		{path: filepath.Join(dir, "a.jpg"), absPath: filepath.Join(dir, "a.jpg"), relPath: "a.jpg", status: batchDone, savedPath: outA, originalSize: 100, outputSize: 50},
		{path: pending, absPath: pending, relPath: "c.jpg", status: batchPending, originalSize: 100},
		{path: outB, absPath: outB, relPath: "b-out.jpg", status: batchDone, overwritten: true, originalSize: 100, outputSize: 60},
	})

	// Pending item produces empty transfer.Data.
	if items := app.batchItemDragExportData(1).Items(); len(items) != 0 {
		t.Fatalf("pending item must not drag out, got %d items", len(items))
	}

	// Done item drags its output path.
	itemFiles, err := app.batchItemDragExportData(0).Files()
	app.dragExportOptions().Done(transfer.Result{})
	if err != nil || !slices.Equal(itemFiles, []string{outA}) {
		t.Fatalf("expected [%s], got %v (err=%v)", outA, itemFiles, err)
	}

	// All-done drag bundles both completed outputs.
	allFiles, err := app.batchAllDoneDragExportData().Files()
	app.dragExportOptions().Done(transfer.Result{})
	if err != nil || !slices.Equal(allFiles, []string{outA, outB}) {
		t.Fatalf("expected [%s %s], got %v (err=%v)", outA, outB, allFiles, err)
	}
}

func TestQuickLookSpaceShortcutAcrossMainAndFloatingViews(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "photo.jpg")
	saved := filepath.Join(dir, "photo-zpic.jpg")
	data := writeTestJPEG(t, src, 80, 60, 90)
	if err := os.WriteFile(saved, data, 0o600); err != nil {
		t.Fatal(err)
	}
	var previewed []string
	app := newApplication()
	app.previewItem = func(path string) error {
		previewed = append(previewed, path)
		return nil
	}
	app.file = &selectedImage{path: src, format: "jpeg", width: 80, height: 60, size: int64(len(data)), data: data}

	tester := ui.NewTester(app.view, 980, 720)
	tester.Key(0, ui.KeySpace)
	if !slices.Equal(previewed, []string{src}) {
		t.Fatalf("Space in single view must preview source: %v", previewed)
	}

	// Prefer saved output when present.
	app.lastSavedPath = saved
	tester.Frame()
	tester.Key(0, ui.KeySpace)
	if !slices.Equal(previewed, []string{src, saved}) {
		t.Fatalf("Space in single view must prefer saved path: %v", previewed)
	}

	// Batch mode: Space previews selected item (preferring output path).
	app.batch = newBatchState([]batchItem{
		{path: src, absPath: src, relPath: "photo.jpg", format: "jpeg", width: 80, height: 60, status: batchDone, savedPath: saved, originalSize: 100, outputSize: 50},
	})
	app.batch.selected = 0
	tester.Frame()
	tester.Key(0, ui.KeySpace)
	if !slices.Equal(previewed, []string{src, saved, saved}) {
		t.Fatalf("Space in batch view must preview selected output: %v", previewed)
	}

	// Floating expanded view: Space previews selected item.
	app.floating.expanded = true
	floatingTester := ui.NewTester(app.floatingView, floatingExpandedWidth, floatingExpandedHeight)
	floatingTester.Key(0, ui.KeySpace)
	if len(previewed) != 4 || previewed[3] != saved {
		t.Fatalf("Space in floating expanded view must preview selected output: %v", previewed)
	}

	// ErrUnsupported stays silent (no error banner).
	app.previewItem = func(string) error { return errors.ErrUnsupported }
	app.setMessage("", false)
	app.quickLookCurrent()
	if app.failed || app.message != "" {
		t.Fatalf("ErrUnsupported must not show an error banner: failed=%v msg=%q", app.failed, app.message)
	}
}

func TestTrashBackupBeforeSingleAndBatchOverwrite(t *testing.T) {
	dir := t.TempDir()
	trashDir := t.TempDir()

	// Single image overwrite with Trash backup enabled.
	singlePath := filepath.Join(dir, "single.jpg")
	orig := writeTestJPEG(t, singlePath, 120, 90, 100)
	res, err := compress.File(context.Background(), singlePath, compress.Options{Quality: 35})
	if err != nil {
		t.Fatal(err)
	}

	var trashed []string
	app := newApplication()
	app.dispatchUpdate = func(fn func()) { fn() }
	app.backupToTrash = true
	app.trashItem = func(path string) error {
		trashed = append(trashed, path)
		return os.Rename(path, filepath.Join(trashDir, filepath.Base(path)))
	}
	app.file = &selectedImage{path: singlePath, format: "jpeg", width: 120, height: 90, size: int64(len(orig)), data: orig}
	app.result = res

	app.overwriteOriginal()
	app.saves.Wait()
	if app.failed || !app.overwritten {
		t.Fatalf("single overwrite with trash backup failed: failed=%v msg=%q", app.failed, app.message)
	}
	if !strings.Contains(app.message, "废纸篓") {
		t.Fatalf("expected trash backup notice in message, got %q", app.message)
	}
	if !slices.Equal(trashed, []string{singlePath}) {
		t.Fatalf("expected %s trashed, got %v", singlePath, trashed)
	}
	trashedBytes, err := os.ReadFile(filepath.Join(trashDir, "single.jpg"))
	if err != nil || !bytes.Equal(trashedBytes, orig) {
		t.Fatalf("trashed backup must contain original bytes: err=%v", err)
	}
	newBytes, err := os.ReadFile(singlePath)
	if err != nil || !bytes.Equal(newBytes, res.Data) {
		t.Fatalf("overwritten path must contain compressed bytes: err=%v", err)
	}

	// Batch overwrite with Trash backup enabled.
	batchPath := filepath.Join(dir, "batch.jpg")
	if err := os.WriteFile(batchPath, orig, 0o600); err != nil {
		t.Fatal(err)
	}
	app.file, app.result, app.overwritten = nil, nil, false
	app.quality = 35
	app.batch = newBatchState([]batchItem{
		{path: batchPath, absPath: batchPath, relPath: "batch.jpg", format: "jpeg", width: 120, height: 90, originalSize: int64(len(orig))},
	})
	app.batch.outputMode = batchOutputOverwrite
	tasks, used := app.collectBatchTasks()
	app.runBatchTasks(context.Background(), tasks, app.compressionOptions(), batchOutputOverwrite, "", used, func(fn func()) { fn() })

	if app.batch.items[0].status != batchDone || !app.batch.items[0].overwritten {
		t.Fatalf("batch overwrite with trash backup failed: %+v", app.batch.items[0])
	}
	if !slices.Contains(trashed, batchPath) {
		t.Fatalf("expected batch source %s to be trashed: %v", batchPath, trashed)
	}
	batchTrashBytes, err := os.ReadFile(filepath.Join(trashDir, "batch.jpg"))
	if err != nil || !bytes.Equal(batchTrashBytes, orig) {
		t.Fatalf("batch trashed backup must contain original bytes: err=%v", err)
	}
}

func TestDownloadDirSettingsPersistenceAndSafeCleanup(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	downloadDir := t.TempDir()
	trashDir := t.TempDir()

	// Create files in downloadDir: idle image, active queued image, temp file, non-image file, subdirectory.
	idleImg := filepath.Join(downloadDir, "idle.png")
	activeImg := filepath.Join(downloadDir, "active.jpg")
	tempLeftover := filepath.Join(downloadDir, ".zpic-drop-12345")
	docFile := filepath.Join(downloadDir, "notes.txt")
	subDir := filepath.Join(downloadDir, "nested")
	for _, f := range []string{idleImg, activeImg, tempLeftover, docFile} {
		if err := os.WriteFile(f, []byte("data"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(subDir, 0o700); err != nil {
		t.Fatal(err)
	}

	app := newApplication()
	app.dispatchUpdate = func(fn func()) { fn() }
	app.settingsFile = settingsPath
	app.trashItem = func(path string) error {
		return os.Rename(path, filepath.Join(trashDir, filepath.Base(path)))
	}
	app.batch = newBatchState([]batchItem{
		{path: activeImg, absPath: activeImg, relPath: "active.jpg", originalSize: 4},
	})
	app.recordDownloadedURLs(map[string]string{
		"https://example.com/idle.png":   idleImg,
		"https://example.com/active.jpg": activeImg,
	})

	// Open settings, toggle backupToTrash, set custom downloadDir, save, and verify persistence.
	app.setSettingsOpen(true)
	app.editor.backupToTrash = true
	app.editor.downloadDir = downloadDir
	if !app.settingsHaveChanges() {
		t.Fatal("changing backupToTrash and downloadDir must mark settings dirty")
	}
	app.saveSettingsEdits()
	if app.settingsOpen || !app.backupToTrash || app.downloadDir != downloadDir {
		t.Fatalf("settings save did not apply fields: open=%v trash=%v dir=%q", app.settingsOpen, app.backupToTrash, app.downloadDir)
	}
	reloaded := newApplication()
	reloaded.settingsFile = settingsPath
	cfg, err := loadFullSettings(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.backupToTrash || cfg.downloadDir != downloadDir {
		t.Fatalf("settings did not persist round-trip: %+v", cfg)
	}

	// Trigger safe download directory cleanup while in settings.
	app.setSettingsOpen(true)
	app.cleanupDownloadedImages()
	app.saves.Wait()
	if app.editor.message != "" {
		t.Fatalf("cleanup produced error: %q", app.editor.message)
	}
	if !strings.Contains(app.editor.notice, "1 张下载图片移入废纸篓") || !strings.Contains(app.editor.notice, "保留 1 张") {
		t.Fatalf("unexpected cleanup notice: %q", app.editor.notice)
	}

	// Verify exact cleanup boundaries.
	if _, err := os.Stat(idleImg); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("idle image should have been moved to trash")
	}
	if _, err := os.Stat(filepath.Join(trashDir, "idle.png")); err != nil {
		t.Fatal("idle image should exist in trash")
	}
	if _, err := os.Stat(tempLeftover); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("drop temp leftover should have been removed")
	}
	if _, err := os.Stat(activeImg); err != nil {
		t.Fatal("active queued image must NOT be cleaned")
	}
	if _, err := os.Stat(docFile); err != nil {
		t.Fatal("non-image file must NOT be cleaned")
	}
	if _, err := os.Stat(subDir); err != nil {
		t.Fatal("subdirectory must NOT be cleaned")
	}
	if _, stillCached := app.downloadedURLs["https://example.com/idle.png"]; stillCached {
		t.Fatal("cleaned file URL cache entry must be pruned")
	}
	if _, stillCached := app.downloadedURLs["https://example.com/active.jpg"]; !stillCached {
		t.Fatal("active file URL cache entry must be retained")
	}

	// Resetting download dir clears it back to default.
	app.resetDownloadDirectory()
	if app.editor.downloadDir != "" {
		t.Fatalf("resetDownloadDirectory failed: %q", app.editor.downloadDir)
	}
	_ = reloaded
}
