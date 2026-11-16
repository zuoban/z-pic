package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"zpic/internal/compress"
)

func writeTestJPEG(t *testing.T, path string, width, height, quality int) []byte {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8((x*7 + y*3) % 256),
				G: uint8((x*5 + y*11) % 256),
				B: uint8((x*13 + y*2) % 256),
				A: 255,
			})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeTestPNG(t *testing.T, path string, width, height int, noisy bool) []byte {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	if noisy {
		for y := range height {
			for x := range width {
				img.SetNRGBA(x, y, color.NRGBA{
					R: uint8((x * 17) ^ (y * 31)),
					G: uint8((x * 23) ^ (y * 13)),
					B: uint8((x * 29) ^ (y * 7)),
					A: 255,
				})
			}
		}
	} else {
		draw.Draw(img, img.Bounds(), image.NewUniform(color.NRGBA{R: 40, G: 120, B: 200, A: 255}), image.Point{}, draw.Src)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeTestAnimatedPNG(t *testing.T, path string) {
	t.Helper()
	raw := writeTestPNG(t, path, 8, 8, false)
	payload := make([]byte, 8)
	binary.BigEndian.PutUint32(payload[0:4], 2)
	var chunk bytes.Buffer
	_ = binary.Write(&chunk, binary.BigEndian, uint32(len(payload)))
	chunk.WriteString("acTL")
	chunk.Write(payload)
	crc := crc32.ChecksumIEEE(append([]byte("acTL"), payload...))
	_ = binary.Write(&chunk, binary.BigEndian, crc)
	apng := append(append([]byte{}, raw[:33]...), chunk.Bytes()...)
	apng = append(apng, raw[33:]...)
	if err := os.WriteFile(path, apng, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestScanBatchPathsFiltersDeduplicatesAndHandlesErrors(t *testing.T) {
	root := t.TempDir()
	dirA := filepath.Join(root, "tripA")
	dirB := filepath.Join(root, "tripB")

	jpgA := writeTestJPEG(t, filepath.Join(dirA, "one.jpg"), 64, 48, 95)
	_ = jpgA
	writeTestPNG(t, filepath.Join(dirA, "nested", "two.PNG"), 40, 40, true)
	writeTestAnimatedPNG(t, filepath.Join(dirA, "anim.png"))
	writeTestJPEG(t, filepath.Join(dirA, ".hidden.jpg"), 16, 16, 90)
	writeTestJPEG(t, filepath.Join(dirA, ".git", "ignored.jpg"), 16, 16, 90)
	if err := os.WriteFile(filepath.Join(dirA, "notes.txt"), []byte("ignore me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirA, "broken.jpg"), []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dirA, "one.jpg"), filepath.Join(dirA, "link.jpg")); err != nil {
		t.Fatal(err)
	}
	writeTestJPEG(t, filepath.Join(dirB, "one.jpg"), 32, 32, 95)

	scan, err := scanBatchPaths(context.Background(), []string{dirA, dirB, filepath.Join(dirA, "one.jpg")}, nil, 0)
	if err != nil {
		t.Fatalf("scanBatchPaths failed: %v", err)
	}
	if scan.duplicates != 1 {
		t.Fatalf("expected 1 duplicate for explicitly repeated file, got %d", scan.duplicates)
	}
	if len(scan.items) != 5 {
		t.Fatalf("expected 5 scanned items (3 valid + broken + anim), got %d: %+v", len(scan.items), scan.items)
	}

	byRel := make(map[string]batchItem, len(scan.items))
	for _, item := range scan.items {
		byRel[item.relPath] = item
	}
	if item, ok := byRel[filepath.Join("tripA", "one.jpg")]; !ok || item.status != batchPending || item.format != "jpeg" {
		t.Fatalf("missing or wrong tripA/one.jpg: %+v", item)
	}
	if item, ok := byRel[filepath.Join("tripA", "nested", "two.PNG")]; !ok || item.status != batchPending || item.format != "png" {
		t.Fatalf("missing or wrong tripA/nested/two.PNG: %+v", item)
	}
	if item, ok := byRel[filepath.Join("tripB", "one.jpg")]; !ok || item.status != batchPending {
		t.Fatalf("multiple root folders must retain root folder prefix to keep distinct relPaths: %+v", byRel)
	}
	if item, ok := byRel[filepath.Join("tripA", "broken.jpg")]; !ok || item.status != batchFailed || !item.invalidInput {
		t.Fatalf("corrupted image inside folder should surface as invalidInput failure: %+v", item)
	}
	if item, ok := byRel[filepath.Join("tripA", "anim.png")]; !ok || item.status != batchFailed || !item.invalidInput {
		t.Fatalf("animated PNG should surface as invalidInput failure: %+v", item)
	}

	// Check batch capacity truncation without generating thousands of real files.
	limited, err := scanBatchPaths(context.Background(), []string{dirA}, nil, maxBatchFiles-1)
	if err != nil {
		t.Fatalf("truncated scan should not error: %v", err)
	}
	if !limited.truncated || len(limited.items) != 1 {
		t.Fatalf("expected truncated=true and 1 item when 1 slot left, got truncated=%v len=%d", limited.truncated, len(limited.items))
	}
}

func TestScanBatchPathsCanceled(t *testing.T) {
	dir := t.TempDir()
	writeTestJPEG(t, filepath.Join(dir, "a.jpg"), 32, 32, 90)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := scanBatchPaths(ctx, []string{dir}, nil, 0); err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestResolveBatchOutputPathAvoidsSourceAndCollisions(t *testing.T) {
	root := t.TempDir()
	srcDir := filepath.Join(root, "src")
	outDir := filepath.Join(root, "out")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	srcPath := filepath.Join(srcDir, "photo.jpg")
	writeTestJPEG(t, srcPath, 32, 32, 90)
	used := make(map[string]bool)

	// Clean destination keeps relative subpath.
	first, err := resolveBatchOutputPath(outDir, srcPath, filepath.Join("day1", "photo.jpg"), used)
	if err != nil {
		t.Fatal(err)
	}
	if first != filepath.Join(outDir, "day1", "photo.jpg") {
		t.Fatalf("unexpected first destination: %q", first)
	}

	// Same relative name in same batch run avoids collision.
	second, err := resolveBatchOutputPath(outDir, srcPath, filepath.Join("day1", "photo.jpg"), used)
	if err != nil {
		t.Fatal(err)
	}
	if second != filepath.Join(outDir, "day1", "photo-compressed.jpg") {
		t.Fatalf("collision must append -compressed suffix: %q", second)
	}

	// Existing file on disk triggers numbered suffix.
	if err := os.MkdirAll(filepath.Dir(second), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("taken"), 0o600); err != nil {
		t.Fatal(err)
	}
	third, err := resolveBatchOutputPath(outDir, srcPath, filepath.Join("day1", "photo.jpg"), used)
	if err != nil {
		t.Fatal(err)
	}
	if third != filepath.Join(outDir, "day1", "photo-compressed-2.jpg") {
		t.Fatalf("existing file must force -compressed-2: %q", third)
	}

	// Output directory equal to source directory never overwrites source.
	sameDirDest, err := resolveBatchOutputPath(srcDir, srcPath, "photo.jpg", used)
	if err != nil {
		t.Fatal(err)
	}
	if sameDirDest != filepath.Join(srcDir, "photo-compressed.jpg") {
		t.Fatalf("same-dir export must avoid source path: %q", sameDirDest)
	}

	// Path traversal relPath is sanitized inside outDir.
	safeDest, err := resolveBatchOutputPath(outDir, srcPath, filepath.Join("..", "..", "escape.jpg"), used)
	if err != nil {
		t.Fatal(err)
	}
	if safeDest != filepath.Join(outDir, "photo.jpg") {
		t.Fatalf("path traversal must fall back safely inside outputDir: %q", safeDest)
	}
}

func TestBatchFolderCompressionEndToEnd(t *testing.T) {
	root := t.TempDir()
	inDir := filepath.Join(root, "in")
	outDir := filepath.Join(root, "out")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	jpgPath := filepath.Join(inDir, "photo.jpg")
	pngPath := filepath.Join(inDir, "sub", "chart.png")
	tinyPNGPath := filepath.Join(inDir, "tiny.png")
	brokenPath := filepath.Join(inDir, "broken.jpg")

	origJPG := writeTestJPEG(t, jpgPath, 160, 120, 100)
	origPNG := writeTestPNG(t, pngPath, 120, 90, true)
	origTiny := writeTestPNG(t, tinyPNGPath, 8, 8, false)
	if err := os.WriteFile(brokenPath, []byte("bad"), 0o600); err != nil {
		t.Fatal(err)
	}

	scan, err := scanBatchPaths(context.Background(), []string{inDir}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	s := newApplication()
	s.setScheme("体积优先")
	s.batch = newBatchState(scan.items)
	s.batch.outputMode = batchOutputFolder
	s.batch.outputDir = outDir

	tasks, used := s.collectBatchTasks()
	if len(tasks) != 3 {
		t.Fatalf("expected 3 valid tasks (skipping broken), got %d", len(tasks))
	}
	s.batch.total = len(tasks)
	s.runBatchTasks(context.Background(), tasks, s.compressionOptions(), s.batch.outputMode, s.batch.outputDir, used, func(fn func()) { fn() })
	s.completeBatchRun(context.Background())

	total, pending, done, failed, _, outTotal, _ := s.batchStats()
	if total != 4 || pending != 0 || done != 3 || failed != 1 {
		t.Fatalf("unexpected batch stats: total=%d pending=%d done=%d failed=%d", total, pending, done, failed)
	}
	if outTotal <= 0 || !s.failed || !strings.Contains(s.message, "成功 3 张，失败 1 张") {
		t.Fatalf("unexpected batch completion status: outTotal=%d failed=%v msg=%q", outTotal, s.failed, s.message)
	}
	// Sources remain completely untouched.
	if got, _ := os.ReadFile(jpgPath); !bytes.Equal(got, origJPG) {
		t.Fatal("folder export modified original JPEG")
	}
	if got, _ := os.ReadFile(pngPath); !bytes.Equal(got, origPNG) {
		t.Fatal("folder export modified original PNG")
	}

	// Compressed files written to outDir preserving relative subdirs.
	outJPG, err := os.ReadFile(filepath.Join(outDir, "photo.jpg"))
	if err != nil || len(outJPG) >= len(origJPG) {
		t.Fatalf("expected smaller output JPEG, err=%v out=%d orig=%d", err, len(outJPG), len(origJPG))
	}
	outPNG, err := os.ReadFile(filepath.Join(outDir, "sub", "chart.png"))
	if err != nil || len(outPNG) > len(origPNG) {
		t.Fatalf("expected output PNG <= original, err=%v out=%d orig=%d", err, len(outPNG), len(origPNG))
	}
	outTiny, err := os.ReadFile(filepath.Join(outDir, "tiny.png"))
	if err != nil || len(outTiny) > len(origTiny) {
		t.Fatalf("expected output tiny PNG <= original, err=%v out=%d orig=%d", err, len(outTiny), len(origTiny))
	}
}

func TestBatchOverwriteRequiresConfirmationAndSkipsUnchanged(t *testing.T) {
	dir := t.TempDir()
	jpgPath := filepath.Join(dir, "photo.jpg")
	tinyPNGPath := filepath.Join(dir, "tiny.png")
	origJPG := writeTestJPEG(t, jpgPath, 180, 140, 100)
	// Use quality=92 preset so already-compressed low-quality JPEG stays unchanged.
	lowQualityPath := filepath.Join(dir, "already-small.jpg")
	origLow := writeTestJPEG(t, lowQualityPath, 64, 48, 25)
	origTiny := writeTestPNG(t, tinyPNGPath, 8, 8, false)
	_ = origTiny

	scan, err := scanBatchPaths(context.Background(), []string{jpgPath, lowQualityPath}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	s := newApplication()
	s.setScheme("画质优先") // quality 92
	s.batch = newBatchState(scan.items)
	s.batch.outputMode = batchOutputOverwrite
	s.startBatchCompression()
	if !s.confirmBatchOverwrite {
		t.Fatal("batch overwrite must require explicit confirmation before touching files")
	}
	tt := ui.NewTester(s.view, 880, 660)
	if _, ok := tt.AccessByRole(ui.RoleAlertDialog, "确认批量覆盖原图？"); !ok {
		t.Fatal("batch overwrite confirmation dialog must be visible")
	}
	tt.Key(0, ui.KeyEscape)
	if s.confirmBatchOverwrite {
		t.Fatal("Escape must cancel batch overwrite confirmation")
	}
	if got, _ := os.ReadFile(jpgPath); !bytes.Equal(got, origJPG) {
		t.Fatal("canceling confirmation must leave source files untouched")
	}

	// Confirm overwrite and execute worker.
	s.startBatchCompression()
	if !s.confirmBatchOverwrite {
		t.Fatal("re-requesting batch overwrite must reopen confirmation")
	}
	s.pendingBatchRestart = false
	s.confirmBatchOverwrite = false
	tasks, used := s.collectBatchTasks()
	s.batch.total = len(tasks)
	s.runBatchTasks(context.Background(), tasks, s.compressionOptions(), s.batch.outputMode, "", used, func(fn func()) { fn() })
	s.completeBatchRun(context.Background())

	if !s.batch.items[0].overwritten || s.batch.items[0].outputSize >= int64(len(origJPG)) {
		t.Fatalf("quality-100 JPEG should be overwritten with smaller data: %+v", s.batch.items[0])
	}
	newJPG, err := os.ReadFile(jpgPath)
	if err != nil || int64(len(newJPG)) != s.batch.items[0].outputSize {
		t.Fatalf("overwritten disk file mismatch: err=%v len=%d item=%+v", err, len(newJPG), s.batch.items[0])
	}
	if !s.batch.items[1].unchanged || s.batch.items[1].overwritten {
		t.Fatalf("unchanged low-quality JPEG must not be overwritten: %+v", s.batch.items[1])
	}
	if got, _ := os.ReadFile(lowQualityPath); !bytes.Equal(got, origLow) {
		t.Fatal("unchanged file bytes must remain identical on disk")
	}
}

func TestBatchMidRunCancellationResumesRemaining(t *testing.T) {
	root := t.TempDir()
	outDir := filepath.Join(root, "out")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	p1 := filepath.Join(root, "1.jpg")
	p2 := filepath.Join(root, "2.jpg")
	writeTestJPEG(t, p1, 100, 80, 95)
	writeTestJPEG(t, p2, 100, 80, 95)

	scan, err := scanBatchPaths(context.Background(), []string{p1, p2}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	s := newApplication()
	s.batch = newBatchState(scan.items)
	s.batch.outputMode = batchOutputFolder
	s.batch.outputDir = outDir

	ctx, cancel := context.WithCancel(context.Background())
	tasks, used := s.collectBatchTasks()
	s.batch.total = len(tasks)
	s.runBatchTasks(ctx, tasks, s.compressionOptions(), batchOutputFolder, outDir, used, func(fn func()) {
		fn()
		if s.batch.done == 1 {
			cancel()
		}
	})
	s.completeBatchRun(ctx)

	_, pending, done, _, _, _, _ := s.batchStats()
	if done != 1 || pending != 1 {
		t.Fatalf("mid-run cancel must preserve 1 completed and 1 pending item: done=%d pending=%d", done, pending)
	}
	if !strings.Contains(s.message, "已取消，已完成 1 张，剩余 1 张") {
		t.Fatalf("unexpected cancel summary: %q", s.message)
	}

	tt := ui.NewTester(s.view, 880, 660)
	if _, ok := tt.AccessByRole(ui.RoleButton, "继续压缩"); !ok {
		t.Fatal("partially completed batch must offer 继续压缩")
	}
	if _, ok := tt.AccessByRole(ui.RoleButton, "重新压缩"); !ok {
		t.Fatal("partially completed batch must offer 重新压缩")
	}

	// Resuming processes only the remaining pending task.
	remTasks, remUsed := s.collectBatchTasks()
	if len(remTasks) != 1 || remTasks[0].index != 1 {
		t.Fatalf("resume must only queue pending task 1, got %+v", remTasks)
	}
	s.runBatchTasks(context.Background(), remTasks, s.compressionOptions(), batchOutputFolder, outDir, remUsed, func(fn func()) { fn() })
	s.completeBatchRun(context.Background())
	_, pending, done, _, _, _, _ = s.batchStats()
	if done != 2 || pending != 0 {
		t.Fatalf("resuming must complete all items: done=%d pending=%d", done, pending)
	}
}

func TestBatchSchemeAndOutputSwitchInvalidateResults(t *testing.T) {
	s := newApplication()
	s.batch = newBatchState([]batchItem{
		{path: "/a.jpg", absPath: "/a.jpg", relPath: "a.jpg", format: "jpeg", width: 100, height: 100, originalSize: 5000, outputSize: 2000, status: batchDone, savedPath: "/out/a.jpg"},
		{path: "/bad.jpg", absPath: "/bad.jpg", relPath: "bad.jpg", status: batchFailed, invalidInput: true, errMsg: "损坏"},
	})

	// No-op scheme re-selection keeps batch results.
	s.setScheme("均衡")
	if s.batch.items[0].status != batchDone {
		t.Fatal("selecting the same active scheme must retain batch results")
	}

	// Changing scheme resets valid completed items while retaining invalidInput failure.
	s.setScheme("体积优先")
	if s.batch.items[0].status != batchPending || s.batch.items[0].outputSize != 0 || s.batch.items[0].savedPath != "" {
		t.Fatalf("changing scheme must reset completed item: %+v", s.batch.items[0])
	}
	if s.batch.items[1].status != batchFailed || !s.batch.items[1].invalidInput {
		t.Fatalf("changing scheme must keep invalidInput failure: %+v", s.batch.items[1])
	}

	// Changing from an original-folder copy preset to in-place saving resets results.
	s.applyPresetOutput(presetSaveOptions{UseSuffix: true, Suffix: "-compressed"})
	s.batch.items[0].status = batchDone
	s.batch.items[0].outputSize = 2000
	s.batch.filter = batchFilterDone
	s.applyPresetOutput(presetSaveOptions{})
	if s.batch.outputMode != batchOutputOverwrite || s.batch.items[0].status != batchPending || s.batch.filter != batchFilterAll {
		t.Fatalf("switching output mode must reset processed batch items and emptied filter: mode=%d filter=%d item=%+v", s.batch.outputMode, s.batch.filter, s.batch.items[0])
	}

	// Selecting the same output again must preserve the completed result and filter.
	s.batch.items[0].status = batchDone
	s.batch.items[0].outputSize = 2000
	s.batch.items[0].savedPath = "/a.jpg"
	s.batch.filter = batchFilterDone
	s.applyPresetOutput(presetSaveOptions{})
	if s.batch.items[0].status != batchDone || s.batch.items[0].outputSize != 2000 ||
		s.batch.items[0].savedPath != "/a.jpg" || s.batch.filter != batchFilterDone {
		t.Fatalf("reselecting the current output must retain the completed result and filter: %+v", s.batch)
	}
}

func TestBatchQueueRemoveClearAndPreview(t *testing.T) {
	s := newApplication()
	s.batch = newBatchState([]batchItem{
		{path: "/a.jpg", absPath: "/a.jpg", relPath: "a.jpg", format: "jpeg", width: 20, height: 12, originalSize: 4096, outputSize: 2048, status: batchDone, savedPath: "/out/a.jpg"},
		{path: "/b.png", absPath: "/b.png", relPath: "b.png", format: "png", width: 20, height: 12, originalSize: 4096, status: batchPending},
	})

	// Preview sub-view displays comparison and closes cleanly on Escape.
	srcImg := ui.NewBitmap(image.NewNRGBA(image.Rect(0, 0, 20, 12)))
	outImg := ui.NewBitmap(image.NewNRGBA(image.Rect(0, 0, 20, 12)))
	s.completeBatchPreview(context.Background(), 0, s.batch.items[0],
		&selectedImage{path: "/a.jpg", format: "jpeg", width: 20, height: 12, size: 4096, preview: srcImg},
		&compress.Result{SourcePath: "/a.jpg", Format: "jpeg", Width: 20, Height: 12, OriginalSize: 4096, OutputSize: 2048},
		outImg, nil)
	if !s.batch.previewOpen || s.result == nil || s.preview != outImg {
		t.Fatal("completeBatchPreview must open preview with loaded result")
	}
	tt := ui.NewTester(s.view, 980, 720)
	if _, ok := tt.AccessByRole(ui.RoleRadioGroup, "预览模式"); !ok {
		t.Fatal("previewing folder-exported item must show comparison segmented control")
	}
	if _, ok := tt.AccessByRole(ui.RoleSlider, "原图与压缩结果对比"); !ok {
		t.Fatal("previewing folder-exported item must show interactive comparison canvas")
	}
	tt.Key(0, ui.KeyEscape)
	if s.batch.previewOpen || s.file != nil || s.result != nil || s.preview != nil {
		t.Fatal("Escape must close batch preview and release temporary bitmaps")
	}

	// Removing items updates selection and clears batch when empty.
	s.removeSelectedBatchItem()
	if len(s.batch.items) != 1 || s.batch.items[0].relPath != "b.png" || s.batch.selected != 0 {
		t.Fatalf("removing item 0 must shift selection cleanly: %+v", s.batch)
	}
	s.clearBatch()
	if s.batch != nil {
		t.Fatal("clearBatch must return to empty state")
	}
}

func TestBatchViewFitsMinimumWindowAndBusyGuards(t *testing.T) {
	for _, width := range []int{760, 880, 980} {
		for _, dark := range []bool{false, true} {
			name := "light"
			if dark {
				name = "dark"
			}
			t.Run(fmt.Sprintf("%d/%s", width, name), func(t *testing.T) {
				s := newApplication()
				s.completeBatchScan(context.Background(), batchScanResult{items: []batchItem{
					{
						path:         "/photos/" + strings.Repeat("很长的目录名/", 8) + "photo-1.jpg",
						absPath:      "/photos/photo-1.jpg",
						relPath:      strings.Repeat("nested/", 6) + "photo-1.jpg",
						format:       "jpeg",
						width:        1920,
						height:       1080,
						originalSize: 2 << 20,
						status:       batchPending,
					},
					{
						path:         "/photos/photo-2.png",
						absPath:      "/photos/photo-2.png",
						relPath:      "photo-2.png",
						format:       "png",
						width:        800,
						height:       600,
						originalSize: 512 << 10,
						status:       batchPending,
					},
				}}, false, nil)
				s.batch.items[0].status = batchDone
				s.batch.items[0].outputSize = 1 << 20
				s.batch.items[0].savedPath = "/photos/photo-1.jpg"
				tt := ui.NewTester(func(c *ui.Context) {
					theme := ui.LightTheme()
					if dark {
						theme = ui.DarkTheme()
					}
					c.SetTheme(theme)
					s.view(c)
				}, width, 660)

				for _, label := range []string{"添加图片", "添加文件夹", "清空", "预览", "移除", "重新压缩", "继续压缩", "设置"} {
					button, ok := tt.AccessByRole(ui.RoleButton, label)
					if !ok || button.Offscreen || button.Bounds.H < 24 ||
						button.Bounds.Y+button.Bounds.H > 660 || button.Bounds.X+button.Bounds.W > float32(width) {
						t.Fatalf("%q must fit inside %dx660: %+v", label, width, button)
					}
				}
				if s.batch.outputMode != batchOutputOverwrite || s.batch.outputDir != "" || !s.output.replacesOriginal() {
					t.Fatalf("the batch must use its preset's in-place save behavior: output=%+v mode=%d directory=%q", s.output, s.batch.outputMode, s.batch.outputDir)
				}
				if _, ok := tt.AccessByRole(ui.RoleList, "批量图片列表"); !ok {
					t.Fatal("batch view must expose virtualized list accessibility role")
				}

				// Busy state disables interactive controls and exposes progress/cancel.
				s.busy = true
				s.phase = "正在压缩 1 / 2 · photo-2.png"
				s.batch.done, s.batch.total = 1, 2
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				s.cancel = cancel
				_ = ctx
				tt.Frame()

				for _, label := range []string{"添加图片", "添加文件夹", "清空", "预览", "移除", "重新压缩", "继续压缩", "设置"} {
					button, ok := tt.AccessByRole(ui.RoleButton, label)
					if !ok || !button.Disabled {
						t.Errorf("%q must be disabled while batch is busy: %+v", label, button)
					}
				}
				cancelBtn, ok := tt.AccessByRole(ui.RoleButton, "取消操作")
				if !ok || cancelBtn.Disabled || cancelBtn.Offscreen {
					t.Fatalf("cancel button must remain enabled and visible while busy: %+v", cancelBtn)
				}
				if _, ok := tt.AccessByRole(ui.RoleProgress, "批量压缩进度"); !ok {
					t.Fatal("busy batch view must expose progress indicator")
				}
			})
		}
	}
}

func TestDropFilesRoutesSingleAndBatchModes(t *testing.T) {
	root := t.TempDir()
	emptyDir := filepath.Join(root, "empty")
	if err := os.MkdirAll(emptyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	singleFile := filepath.Join(root, "one.jpg")
	secondFile := filepath.Join(root, "two.jpg")
	writeTestJPEG(t, singleFile, 16, 16, 85)
	writeTestJPEG(t, secondFile, 16, 16, 85)

	emptyApp := newApplication()
	if emptyApp.shouldOpenAsBatch([]string{singleFile}) {
		t.Fatal("dropping one image file when idle must stay in single-image mode")
	}
	if !emptyApp.shouldOpenAsBatch([]string{singleFile, secondFile}) {
		t.Fatal("dropping multiple files must enter batch mode")
	}
	if !emptyApp.shouldOpenAsBatch([]string{emptyDir}) {
		t.Fatal("dropping a directory must enter batch mode")
	}

	s := testSelectedApplication()
	s.batch = newBatchState([]batchItem{{path: singleFile, absPath: singleFile, relPath: "one.jpg"}})
	if !s.shouldOpenAsBatch([]string{secondFile}) {
		t.Fatal("dropping one image file while already in batch mode must append to batch")
	}
	s.batch = nil
	existing := s.file
	// Empty folder failure must keep existing valid single selection.
	s.completeBatchScan(context.Background(), batchScanResult{}, false, nil)
	if s.batch != nil || s.file != existing || !s.failed {
		t.Fatal("empty batch scan must retain existing selection and report error")
	}
}

func TestAppendingFoldersPreservesDistinctRelativePaths(t *testing.T) {
	root := t.TempDir()
	dirA := filepath.Join(root, "tripA")
	dirB := filepath.Join(root, "tripB")
	dirC := filepath.Join(root, "tripC")
	outDir := filepath.Join(root, "out")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}

	writeTestJPEG(t, filepath.Join(dirA, "one.jpg"), 64, 48, 95)
	writeTestJPEG(t, filepath.Join(dirB, "one.jpg"), 64, 48, 95)
	writeTestJPEG(t, filepath.Join(dirC, "one.jpg"), 64, 48, 95)

	// Step 1: load single folder dirA.
	scanA, err := scanBatchPathsWithDirs(context.Background(), []string{dirA}, nil, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	s := newApplication()
	s.completeBatchScan(context.Background(), scanA, false, nil)
	if len(s.batch.items) != 1 || s.batch.items[0].relPath != "one.jpg" {
		t.Fatalf("single folder must keep unprefixed relPath, got %+v", s.batch.items)
	}

	// Re-appending the same folder (with a newly added file) keeps unprefixed relPath.
	writeTestJPEG(t, filepath.Join(dirA, "two.jpg"), 64, 48, 95)
	existing := map[string]bool{s.batch.items[0].absPath: true}
	existingDirs := map[string]bool{s.batch.items[0].sourceDir: true}
	scanA2, err := scanBatchPathsWithDirs(context.Background(), []string{dirA}, existing, existingDirs, len(s.batch.items))
	if err != nil {
		t.Fatal(err)
	}
	s.completeBatchScan(context.Background(), scanA2, true, nil)
	if len(s.batch.items) != 2 || s.batch.items[1].relPath != "two.jpg" {
		t.Fatalf("re-appending same folder must keep unprefixed relPaths, got %+v", s.batch.items)
	}

	// Step 2: append dirB before running -> promotes dirA items and prefixes dirB.
	existing = map[string]bool{}
	existingDirs = map[string]bool{}
	for _, item := range s.batch.items {
		existing[item.absPath] = true
		if item.sourceDir != "" {
			existingDirs[item.sourceDir] = true
		}
	}
	scanB, err := scanBatchPathsWithDirs(context.Background(), []string{dirB}, existing, existingDirs, len(s.batch.items))
	if err != nil {
		t.Fatal(err)
	}
	s.completeBatchScan(context.Background(), scanB, true, nil)
	if len(s.batch.items) != 3 {
		t.Fatalf("expected 3 items after appending dirB, got %d", len(s.batch.items))
	}
	if s.batch.items[0].relPath != filepath.Join("tripA", "one.jpg") ||
		s.batch.items[1].relPath != filepath.Join("tripA", "two.jpg") ||
		s.batch.items[2].relPath != filepath.Join("tripB", "one.jpg") {
		t.Fatalf("appending second folder must organize each folder under its root name: %+v", s.batch.items)
	}

	// Step 3: if existing items already completed and saved, appending dirC leaves existing relPaths intact
	// while prefixing dirC to avoid output collisions.
	s.batch.items[0].status = batchDone
	s.batch.items[0].savedPath = filepath.Join(outDir, s.batch.items[0].relPath)
	existing[s.batch.items[2].absPath] = true
	existingDirs[s.batch.items[2].sourceDir] = true
	scanC, err := scanBatchPathsWithDirs(context.Background(), []string{dirC}, existing, existingDirs, len(s.batch.items))
	if err != nil {
		t.Fatal(err)
	}
	s.completeBatchScan(context.Background(), scanC, true, nil)
	if s.batch.items[0].relPath != filepath.Join("tripA", "one.jpg") ||
		s.batch.items[3].relPath != filepath.Join("tripC", "one.jpg") {
		t.Fatalf("post-run folder append must keep existing relPath and prefix new folder: %+v", s.batch.items)
	}
}

func TestBatchResultSummaryMatchesCompletedScope(t *testing.T) {
	// Case 1: pending queue with an invalid item shows waiting count + failure count.
	s := newApplication()
	s.batch = newBatchState([]batchItem{
		{path: "/a.jpg", absPath: "/a.jpg", relPath: "a.jpg", originalSize: 2048, status: batchPending},
		{path: "/bad.jpg", absPath: "/bad.jpg", relPath: "bad.jpg", status: batchFailed, invalidInput: true, errMsg: "损坏"},
	})
	tt := ui.NewTester(s.view, 980, 720)
	if !tt.HasText("等待压缩 · 1 张 · 1 失败") || !tt.HasText("原始总计") {
		t.Fatal("initial mixed queue must report pending and invalid counts under 原始总计")
	}

	// Case 2: partially completed queue displays completed original size alongside output total.
	s.batch.items = []batchItem{
		{path: "/a.jpg", absPath: "/a.jpg", relPath: "a.jpg", originalSize: 2048, outputSize: 1024, status: batchDone},
		{path: "/b.jpg", absPath: "/b.jpg", relPath: "b.jpg", originalSize: 10240, status: batchPending},
	}
	tt.Frame()
	if !tt.HasText("已完成原大小") || !tt.HasText("2.0 KiB") || !tt.HasText("1.0 KiB") || !tt.HasText("减少 50.0% · 余 1") {
		t.Fatal("partially completed summary must compare completed original size against output total")
	}

	// Case 3: fully completed queue restores 原始总计.
	s.batch.items[1].status = batchDone
	s.batch.items[1].outputSize = 5120
	tt.Frame()
	if !tt.HasText("原始总计") || tt.HasText("已完成原大小") || !tt.HasText("12.0 KiB") || !tt.HasText("6.0 KiB") {
		t.Fatal("fully completed summary must display full 原始总计 and 输出总计")
	}
	if !tt.HasText("共节省 6.0 KiB") {
		t.Fatal("completed summary must surface total saved bytes")
	}

	// Case 4: unchanged items surface concise skipped/kept count in both folder and overwrite modes at 880x660.
	s.batch.items[1].unchanged = true
	s.batch.items[1].outputSize = s.batch.items[1].originalSize
	tt.Frame()
	if !tt.HasText("共节省 1.0 KiB · 1 张未减小保持原样") {
		t.Fatal("folder summary must show saved bytes alongside kept-unchanged count")
	}
	s.batch.outputMode = batchOutputOverwrite
	s.message = "批处理完成，已处理 2 张图片。"
	s.batch.outputDir = ""
	ttMin := ui.NewTester(s.view, 880, 660)
	if !ttMin.HasText("共节省 1.0 KiB · 跳过 1 张未减小") {
		t.Fatal("overwrite summary must show skipped-unchanged count")
	}
	if button, ok := ttMin.AccessByRole(ui.RoleButton, "重新压缩"); !ok || button.Offscreen || button.Bounds.Y+button.Bounds.H > 660 {
		t.Fatalf("batch action button must remain visible inside 880x660 with summary note: %+v", button)
	}
}

func TestBatchQueueFilterSearchAndRemoval(t *testing.T) {
	s := newApplication()
	s.batch = newBatchState([]batchItem{
		{path: "/p/done.jpg", absPath: "/p/done.jpg", relPath: "done.jpg", format: "jpeg", width: 100, height: 100, originalSize: 4096, outputSize: 2048, status: batchDone, savedPath: "/out/done.jpg"},
		{path: "/p/sub/wait.png", absPath: "/p/sub/wait.png", relPath: filepath.Join("sub", "wait.png"), format: "png", width: 80, height: 60, originalSize: 3072, status: batchPending},
		{path: "/p/fail-a.jpg", absPath: "/p/fail-a.jpg", relPath: "fail-a.jpg", format: "jpeg", width: 64, height: 64, originalSize: 2048, status: batchFailed, errMsg: "写入失败"},
		{path: "/p/broken.jpg", absPath: "/p/broken.jpg", relPath: "broken.jpg", status: batchFailed, invalidInput: true, errMsg: "无法识别图片"},
		{path: "/p/fail-b.png", absPath: "/p/fail-b.png", relPath: "fail-b.png", format: "png", width: 64, height: 64, originalSize: 1024, status: batchFailed, errMsg: "权限不足"},
	})
	tt := ui.NewTester(s.view, 980, 720)

	if _, ok := tt.AccessByRole(ui.RoleRadioGroup, "队列筛选"); !ok {
		t.Fatal("batch view must expose status filter segmented control")
	}
	if !tt.HasText("批量队列 · 5 张") {
		t.Fatal("unfiltered header must display total batch count")
	}
	if s.retryableFailedCount() != 2 {
		t.Fatalf("retryableFailedCount must exclude invalidInput items: got %d", s.retryableFailedCount())
	}

	// Filter to pending items.
	if err := tt.Click("待处理"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("批量队列 · 1 / 5 张") || !tt.HasText(filepath.Join("sub", "wait.png")) || tt.HasText("done.jpg") {
		t.Fatal("待处理 filter must show only pending item")
	}
	if s.batch.selected != -1 {
		t.Fatalf("selected item hidden by filter must reset to -1, got %d", s.batch.selected)
	}

	// Filter to failed items (shows both retryable failures and invalidInput).
	if err := tt.Click("失败"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("批量队列 · 3 / 5 张") || !tt.HasText("fail-a.jpg") || !tt.HasText("broken.jpg") || !tt.HasText("fail-b.png") {
		t.Fatal("失败 filter must show all failed items")
	}

	// Selecting fail-a.jpg and removing it advances selection within the filtered view, not hidden items.
	if err := tt.Click("fail-a.jpg"); err != nil {
		t.Fatal(err)
	}
	if s.batch.selected != 2 {
		t.Fatalf("clicking fail-a.jpg must select item 2, got %d", s.batch.selected)
	}
	if err := tt.Click("移除"); err != nil {
		t.Fatal(err)
	}
	if len(s.batch.items) != 4 || s.batch.selected != 2 || s.batch.items[s.batch.selected].relPath != "broken.jpg" {
		t.Fatalf("removing filtered item must advance selection to next filtered item: selected=%d items=%+v", s.batch.selected, s.batch.items)
	}

	// Search query filters within active status filter and supports empty-state reset.
	searchField, ok := tt.AccessByRole(ui.RoleTextField, "筛选文件名")
	if !ok {
		t.Fatal("batch view must expose search field")
	}
	tt.ClickAt(searchField.Bounds.X+searchField.Bounds.W/2, searchField.Bounds.Y+searchField.Bounds.H/2)
	tt.Type("fail-b")
	if !tt.HasText("批量队列 · 1 / 4 张") || !tt.HasText("fail-b.png") || tt.HasText("broken.jpg") {
		t.Fatal("search query must narrow visible queue rows")
	}

	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("no-such-file")
	if !tt.HasText("没有找到匹配的图片") {
		t.Fatal("empty search match must show clear empty-filter message")
	}
	if err := tt.Click("显示全部"); err != nil {
		t.Fatal(err)
	}
	if s.batch.filter != batchFilterAll || s.batch.query != "" || !tt.HasText("批量队列 · 4 张") {
		t.Fatalf("clicking 显示全部 must reset filter and search: filter=%d query=%q", s.batch.filter, s.batch.query)
	}

	// Appending new files while viewing 已完成 switches back to 全部 so newly added items appear.
	s.batch.filter = batchFilterDone
	s.batch.query = "done"
	s.completeBatchScan(context.Background(), batchScanResult{
		items: []batchItem{{path: "/p/new.jpg", absPath: "/p/new.jpg", relPath: "new.jpg", status: batchPending}},
	}, true, nil)
	if s.batch.filter != batchFilterAll || s.batch.query != "" {
		t.Fatalf("appending items must clear finished/failed filter and query: filter=%d query=%q", s.batch.filter, s.batch.query)
	}

	// Appending an invalid item while viewing 待处理 switches back to 全部 so the failure is visible.
	s.batch.filter = batchFilterPending
	s.completeBatchScan(context.Background(), batchScanResult{
		items: []batchItem{{path: "/p/bad2.jpg", absPath: "/p/bad2.jpg", relPath: "bad2.jpg", status: batchFailed, invalidInput: true, errMsg: "损坏"}},
	}, true, nil)
	if s.batch.filter != batchFilterAll {
		t.Fatalf("appending invalid item under 待处理 filter must reset filter to 全部, got %d", s.batch.filter)
	}
}

func TestBatchFilteredPreviewNavigation(t *testing.T) {
	s := newApplication()
	s.batch = newBatchState([]batchItem{
		{path: "/a.jpg", absPath: "/a.jpg", relPath: "a.jpg", format: "jpeg", width: 20, height: 12, originalSize: 4096, outputSize: 2048, status: batchDone},
		{path: "/b.jpg", absPath: "/b.jpg", relPath: "b.jpg", format: "jpeg", width: 20, height: 12, originalSize: 4096, status: batchPending},
		{path: "/c.jpg", absPath: "/c.jpg", relPath: "c.jpg", format: "jpeg", width: 20, height: 12, originalSize: 4096, outputSize: 1024, status: batchDone},
	})
	s.batch.filter = batchFilterDone

	img := ui.NewBitmap(image.NewNRGBA(image.Rect(0, 0, 20, 12)))
	s.completeBatchPreview(context.Background(), 0, s.batch.items[0],
		&selectedImage{path: "/a.jpg", format: "jpeg", width: 20, height: 12, size: 4096, preview: img},
		&compress.Result{SourcePath: "/a.jpg", Format: "jpeg", Width: 20, Height: 12, OriginalSize: 4096, OutputSize: 2048},
		img, nil)

	tt := ui.NewTester(s.view, 980, 720)
	if !tt.HasText("1 / 2") {
		t.Fatal("filtered preview must display position within filtered items")
	}
	if prev := s.adjacentBatchPreviewIndex(-1); prev != -1 {
		t.Fatalf("first filtered item must have no previous item, got %d", prev)
	}
	if next := s.adjacentBatchPreviewIndex(1); next != 2 {
		t.Fatalf("next preview item under 已完成 filter must skip pending item 1 and jump to 2, got %d", next)
	}
}

func TestBatchRetryFailedItemsAndSingleItemRetry(t *testing.T) {
	root := t.TempDir()
	outDir := filepath.Join(root, "out")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	okPath := filepath.Join(root, "ok.jpg")
	retryPath := filepath.Join(root, "retry.jpg")
	pendingPath := filepath.Join(root, "pending.jpg")
	brokenPath := filepath.Join(root, "broken.jpg")

	writeTestJPEG(t, okPath, 160, 120, 100)
	retryOrig := writeTestJPEG(t, retryPath, 160, 120, 100)
	writeTestJPEG(t, pendingPath, 120, 90, 95)
	if err := os.WriteFile(brokenPath, []byte("bad"), 0o600); err != nil {
		t.Fatal(err)
	}

	scan, err := scanBatchPaths(context.Background(), []string{okPath, retryPath, pendingPath, brokenPath}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	s := newApplication()
	s.setScheme("体积优先")
	s.batch = newBatchState(scan.items)
	s.batch.outputMode = batchOutputFolder
	s.batch.outputDir = outDir

	findIndex := func(name string) int {
		for i, item := range s.batch.items {
			if filepath.Base(item.path) == name {
				return i
			}
		}
		t.Fatalf("missing item %s", name)
		return -1
	}
	okIdx := findIndex("ok.jpg")
	retryIdx := findIndex("retry.jpg")
	pendingIdx := findIndex("pending.jpg")
	brokenIdx := findIndex("broken.jpg")

	okSaved := filepath.Join(outDir, "ok.jpg")
	if err := os.WriteFile(okSaved, []byte("already-saved"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.batch.items[okIdx].status = batchDone
	s.batch.items[okIdx].outputSize = 500
	s.batch.items[okIdx].savedPath = okSaved
	s.batch.items[retryIdx].status = batchFailed
	s.batch.items[retryIdx].errMsg = "磁盘空间不足"
	s.batch.items[pendingIdx].status = batchPending
	if !s.batch.items[brokenIdx].invalidInput {
		t.Fatal("broken.jpg must be marked invalidInput")
	}

	s.batch.selected = okIdx
	tt := ui.NewTester(s.view, 980, 720)
	if _, ok := tt.AccessByRole(ui.RoleButton, "重试失败项"); !ok {
		t.Fatal("toolbar must show 重试失败项 when retryable failed items exist")
	}
	if _, ok := tt.AccessByRole(ui.RoleButton, "重试"); ok {
		t.Fatal("selected bar must not show 重试 for completed item")
	}
	s.batch.selected = brokenIdx
	tt.Frame()
	if _, ok := tt.AccessByRole(ui.RoleButton, "重试"); ok {
		t.Fatal("selected bar must not show 重试 for invalidInput item")
	}
	s.batch.selected = retryIdx
	tt.Frame()
	if _, ok := tt.AccessByRole(ui.RoleButton, "重试"); !ok {
		t.Fatal("selected bar must show 重试 for retryable failed item")
	}

	tasks, used := s.prepareBatchTasks(batchRunRetryFailed, -1)
	if len(tasks) != 1 || tasks[0].index != retryIdx || !used[okSaved] {
		t.Fatalf("prepareBatchTasks(retryFailed) must collect only retryIdx and retain usedPaths: tasks=%+v used=%+v", tasks, used)
	}
	s.batch.total = len(tasks)
	s.runBatchTasks(context.Background(), tasks, s.compressionOptions(), s.batch.outputMode, s.batch.outputDir, used, func(fn func()) { fn() })
	s.completeBatchRun(context.Background())

	if s.batch.items[retryIdx].status != batchDone || s.batch.items[retryIdx].outputSize >= int64(len(retryOrig)) {
		t.Fatalf("retried item must succeed with smaller output: %+v", s.batch.items[retryIdx])
	}
	if s.batch.items[okIdx].status != batchDone || s.batch.items[okIdx].savedPath != okSaved {
		t.Fatalf("already completed item must stay untouched: %+v", s.batch.items[okIdx])
	}
	if s.batch.items[pendingIdx].status != batchPending {
		t.Fatalf("pending item must remain pending after retrying failed items: %+v", s.batch.items[pendingIdx])
	}
	if s.batch.items[brokenIdx].status != batchFailed || !s.batch.items[brokenIdx].invalidInput {
		t.Fatalf("invalidInput item must remain failed: %+v", s.batch.items[brokenIdx])
	}
	if !s.failed || !strings.Contains(s.message, "当前已完成 2 张，失败 1 张，剩余 1 张待压缩") {
		t.Fatalf("retry completion with remaining pending items must report status: failed=%v msg=%q", s.failed, s.message)
	}

	// When no invalid/failed items remain while pending items exist, completion reports neutral retry status.
	s.batch.items[brokenIdx].status = batchPending
	s.batch.items[brokenIdx].invalidInput = false
	s.completeBatchRun(context.Background())
	if s.failed || !strings.Contains(s.message, "当前已完成 2 张，剩余 2 张待压缩") {
		t.Fatalf("retry completion without remaining failures must report neutral progress: failed=%v msg=%q", s.failed, s.message)
	}
	s.batch.items[brokenIdx].status = batchFailed
	s.batch.items[brokenIdx].invalidInput = true

	s.batch.items[retryIdx].status = batchFailed
	s.batch.items[retryIdx].errMsg = "临时错误"
	s.batch.items[pendingIdx].status = batchFailed
	s.batch.items[pendingIdx].errMsg = "另一个错误"
	singleTasks, _ := s.prepareBatchTasks(batchRunSingle, retryIdx)
	if len(singleTasks) != 1 || singleTasks[0].index != retryIdx {
		t.Fatalf("single retry must queue only selected item %d, got %+v", retryIdx, singleTasks)
	}
	if s.batch.items[pendingIdx].status != batchFailed {
		t.Fatalf("single retry must leave other failed items untouched: %+v", s.batch.items[pendingIdx])
	}

	s.batch.items[retryIdx].status = batchDone
	s.batch.items[pendingIdx].status = batchDone
	tt.Frame()
	if _, ok := tt.AccessByRole(ui.RoleButton, "重试失败项"); ok {
		t.Fatal("重试失败项 must be hidden when only invalidInput failures remain")
	}
}

func TestBatchRetryOverwriteRequiresConfirmationAndPreservesFailureOnCancel(t *testing.T) {
	s := newApplication()
	s.batch = newBatchState([]batchItem{
		{path: "/a.jpg", absPath: "/a.jpg", relPath: "a.jpg", format: "jpeg", originalSize: 4096, outputSize: 2048, status: batchDone, overwritten: true},
		{path: "/b.jpg", absPath: "/b.jpg", relPath: "b.jpg", format: "jpeg", originalSize: 4096, status: batchFailed, errMsg: "权限错误"},
		{path: "/c.jpg", absPath: "/c.jpg", relPath: "c.jpg", format: "jpeg", originalSize: 4096, status: batchFailed, errMsg: "磁盘已满"},
		{path: "/bad.jpg", absPath: "/bad.jpg", relPath: "bad.jpg", status: batchFailed, invalidInput: true, errMsg: "损坏"},
	})
	s.batch.outputMode = batchOutputOverwrite
	s.batch.selected = 1

	tt := ui.NewTester(s.view, 980, 720)
	if err := tt.Click("重试失败项"); err != nil {
		t.Fatal(err)
	}
	if !s.confirmBatchOverwrite || s.pendingBatchRun != batchRunRetryFailed {
		t.Fatalf("retrying failed items in overwrite mode must require confirmation: confirm=%v mode=%v", s.confirmBatchOverwrite, s.pendingBatchRun)
	}
	if !tt.HasText("将压缩 2 张图片") {
		t.Fatal("overwrite dialog must report exact count of retryable failed items (2)")
	}
	tt.Key(0, ui.KeyEscape)
	if s.confirmBatchOverwrite || s.pendingBatchRun != batchRunPending || s.pendingBatchIndex != -1 ||
		s.batch.items[1].status != batchFailed || s.batch.items[1].errMsg != "权限错误" ||
		s.batch.items[2].status != batchFailed || s.batch.items[2].errMsg != "磁盘已满" {
		t.Fatalf("canceling overwrite retry must leave failed items intact and clear pending state: confirm=%v mode=%v index=%d items=%+v",
			s.confirmBatchOverwrite, s.pendingBatchRun, s.pendingBatchIndex, s.batch.items)
	}

	if err := tt.Click("重试"); err != nil {
		t.Fatal(err)
	}
	if !s.confirmBatchOverwrite || s.pendingBatchRun != batchRunSingle || s.pendingBatchIndex != 1 {
		t.Fatalf("single-item retry in overwrite mode must require confirmation: confirm=%v mode=%v index=%d", s.confirmBatchOverwrite, s.pendingBatchRun, s.pendingBatchIndex)
	}
	if !tt.HasText("将压缩 1 张图片") {
		t.Fatal("single-item overwrite dialog must report count 1")
	}
	tt.Key(0, ui.KeyEscape)
	if s.confirmBatchOverwrite || s.pendingBatchRun != batchRunPending || s.pendingBatchIndex != -1 ||
		s.batch.items[1].status != batchFailed || s.batch.items[1].errMsg != "权限错误" {
		t.Fatalf("canceling single-item overwrite retry must preserve failure and reset confirmation: confirm=%v mode=%v index=%d item=%+v",
			s.confirmBatchOverwrite, s.pendingBatchRun, s.pendingBatchIndex, s.batch.items[1])
	}
}

func TestBatchFilterAndRetryFitMinimumWindow(t *testing.T) {
	for _, dark := range []bool{false, true} {
		name := "light"
		if dark {
			name = "dark"
		}
		t.Run(name, func(t *testing.T) {
			s := newApplication()
			s.batch = newBatchState([]batchItem{
				{path: "/a.jpg", absPath: "/a.jpg", relPath: "a.jpg", format: "jpeg", width: 100, height: 100, originalSize: 4096, status: batchFailed, errMsg: "磁盘已满"},
				{path: "/b.png", absPath: "/b.png", relPath: "b.png", format: "png", width: 100, height: 100, originalSize: 4096, outputSize: 2048, status: batchDone, savedPath: "/out/b.png"},
				{path: "/c.png", absPath: "/c.png", relPath: "c.png", status: batchFailed, invalidInput: true, errMsg: "不支持动画 PNG"},
			})
			tt := ui.NewTester(func(c *ui.Context) {
				theme := ui.LightTheme()
				if dark {
					theme = ui.DarkTheme()
				}
				c.SetTheme(theme)
				s.view(c)
			}, 880, 660)

			searchField, ok := tt.AccessByRole(ui.RoleTextField, "筛选文件名")
			if !ok || searchField.Offscreen || searchField.Bounds.W < 120 ||
				searchField.Bounds.X+searchField.Bounds.W > 880 || searchField.Bounds.Y+searchField.Bounds.H > 660 {
				t.Fatalf("search field must fit comfortably inside minimum window: %+v", searchField)
			}
			filterGroup, ok := tt.AccessByRole(ui.RoleRadioGroup, "队列筛选")
			if !ok || filterGroup.Offscreen || filterGroup.Bounds.X+filterGroup.Bounds.W > 880 {
				t.Fatalf("filter group must fit inside minimum window: %+v", filterGroup)
			}
			sortBtn, ok := tt.AccessByRole(ui.RoleMenuButton, "队列排序")
			if !ok || sortBtn.Offscreen || sortBtn.Bounds.X < filterGroup.Bounds.X+filterGroup.Bounds.W ||
				sortBtn.Bounds.X+sortBtn.Bounds.W > 880 || sortBtn.Bounds.Y+sortBtn.Bounds.H > 660 {
				t.Fatalf("sort menu button must sit beside filter without overlap inside 880x660: filter=%+v sort=%+v", filterGroup, sortBtn)
			}
			for _, label := range []string{"移除无效项", "移除失败项", "重试失败项", "重试", "预览", "移除"} {
				button, ok := tt.AccessByRole(ui.RoleButton, label)
				if !ok || button.Offscreen || button.Bounds.H < 24 ||
					button.Bounds.X+button.Bounds.W > 880 || button.Bounds.Y+button.Bounds.H > 660 {
					t.Fatalf("%q must fit inside minimum window: %+v", label, button)
				}
			}
			removeInvalidBtn, _ := tt.AccessByRole(ui.RoleButton, "移除无效项")
			removeFailedBtn, _ := tt.AccessByRole(ui.RoleButton, "移除失败项")
			retryAllBtn, _ := tt.AccessByRole(ui.RoleButton, "重试失败项")
			if removeInvalidBtn.Bounds.X+removeInvalidBtn.Bounds.W > removeFailedBtn.Bounds.X ||
				removeFailedBtn.Bounds.X+removeFailedBtn.Bounds.W > retryAllBtn.Bounds.X {
				t.Fatalf("failure banner buttons must not overlap: invalid=%+v failed=%+v retry=%+v",
					removeInvalidBtn, removeFailedBtn, retryAllBtn)
			}

			s.busy = true
			s.phase = "正在重试 0 / 1"
			s.batch.done, s.batch.total = 0, 1
			tt.Frame()
			for _, label := range []string{"移除无效项", "移除失败项", "重试失败项", "重试"} {
				button, ok := tt.AccessByRole(ui.RoleButton, label)
				if !ok || !button.Disabled {
					t.Fatalf("%q must be disabled while busy: %+v", label, button)
				}
			}

			s.busy = false
			tt.Frame()
			if err := tt.Click("b.png"); err != nil {
				t.Fatal(err)
			}
			for _, label := range []string{"定位文件", "预览", "移除"} {
				button, ok := tt.AccessByRole(ui.RoleButton, label)
				if !ok || button.Offscreen || button.Bounds.H < 24 ||
					button.Bounds.X+button.Bounds.W > 880 || button.Bounds.Y+button.Bounds.H > 660 {
					t.Fatalf("%q for done item must fit inside minimum window: %+v", label, button)
				}
			}
			revealBtn, _ := tt.AccessByRole(ui.RoleButton, "定位文件")
			previewBtn, _ := tt.AccessByRole(ui.RoleButton, "预览")
			if revealBtn.Bounds.X+revealBtn.Bounds.W > previewBtn.Bounds.X {
				t.Fatalf("定位文件 must sit left of 预览 without overlap: reveal=%+v preview=%+v", revealBtn, previewBtn)
			}
			s.busy = true
			tt.Frame()
			if btn, ok := tt.AccessByRole(ui.RoleButton, "定位文件"); !ok || !btn.Disabled {
				t.Fatalf("定位文件 must be disabled while busy: %+v", btn)
			}
		})
	}
}

func TestBatchOutputDirPersistenceAndSafety(t *testing.T) {
	root := t.TempDir()
	cfgPath := filepath.Join(root, "settings.json")
	outDir := filepath.Join(root, "out-a")
	otherOut := filepath.Join(root, "out-b")
	srcDir := filepath.Join(root, "src")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(otherOut, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestJPEG(t, filepath.Join(srcDir, "one.jpg"), 32, 32, 85)
	t.Setenv("ZPIC_CONFIG_FILE", cfgPath)

	s := newApplication()
	s.restoreSettings()
	s.rememberBatchOutputDir(outDir)

	// Switching schemes and saving preset edits must preserve persisted batch_output_dir.
	s.setScheme("体积优先")
	s.setSettingsOpen(true)
	s.savePresetEdits()

	restarted := newApplication()
	restarted.restoreSettings()
	if restarted.batchOutputDir != outDir {
		t.Fatalf("expected persisted batchOutputDir %q after restart, got %q", outDir, restarted.batchOutputDir)
	}
	scan, err := scanBatchPathsWithDirs(context.Background(), []string{srcDir}, nil, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	restarted.completeBatchScan(context.Background(), scan, false, nil)
	if restarted.batch == nil || restarted.batch.outputMode != batchOutputOverwrite || restarted.batch.outputDir != "" {
		t.Fatalf("a remembered picker directory must not override the default in-place preset: %+v", restarted.batch)
	}

	for _, test := range []struct {
		name   string
		output presetSaveOptions
		mode   int
		dir    string
	}{
		{name: "ask", output: presetSaveOptions{Mode: presetSaveAsk}, mode: batchOutputFolder},
		{name: "fixed", output: presetSaveOptions{Mode: presetSaveFixed, Directory: otherOut}, mode: batchOutputFolder, dir: otherOut},
		{name: "original-copy", output: presetSaveOptions{UseSuffix: true, Suffix: "-compressed"}, mode: batchOutputFolder},
	} {
		t.Run(test.name, func(t *testing.T) {
			app := newApplication()
			app.restoreSettings()
			app.applyPresetOutput(test.output)
			app.completeBatchScan(context.Background(), scan, false, nil)
			if app.batch == nil || app.batch.outputMode != test.mode || app.batch.outputDir != test.dir || app.batchOutputDir != outDir {
				t.Fatalf("new queues must use the preset output and retain the remembered picker directory separately: output=%+v batch=%+v remembered=%q", app.output, app.batch, app.batchOutputDir)
			}

			// A directory chosen for an ask queue survives appending files to that queue.
			if test.output.Mode == presetSaveAsk {
				app.batch.outputDir = otherOut
			}
			queueDir := app.batch.outputDir
			app.completeBatchScan(context.Background(), scan, true, nil)
			if app.batch.outputMode != test.mode || app.batch.outputDir != queueDir || app.output != test.output {
				t.Fatalf("appending files must preserve the current queue's preset output: output=%+v batch=%+v", app.output, app.batch)
			}
		})
	}

	// Deleted directory falls back to empty cleanly on startup without setting settingsError.
	if err := os.RemoveAll(outDir); err != nil {
		t.Fatal(err)
	}
	missingRestart := newApplication()
	missingRestart.restoreSettings()
	if missingRestart.settingsError != "" || missingRestart.batchOutputDir != "" {
		t.Fatalf("missing persisted outputDir must fall back cleanly: err=%q dir=%q", missingRestart.settingsError, missingRestart.batchOutputDir)
	}

	// Corrupt settings file must never be overwritten when rememberBatchOutputDir runs.
	corruptContent := `{"version":99`
	if err := os.WriteFile(cfgPath, []byte(corruptContent), 0o600); err != nil {
		t.Fatal(err)
	}
	corruptApp := newApplication()
	corruptApp.restoreSettings()
	if corruptApp.settingsError == "" {
		t.Fatal("expected settingsError on corrupt config")
	}
	corruptApp.rememberBatchOutputDir(otherOut)
	if corruptApp.batchOutputDir != otherOut {
		t.Fatalf("corrupt config session must still remember batchOutputDir in memory, got %q", corruptApp.batchOutputDir)
	}
	rawAfter, err := os.ReadFile(cfgPath)
	if err != nil || string(rawAfter) != corruptContent {
		t.Fatalf("corrupt config file must not be overwritten: got %q err=%v", rawAfter, err)
	}
}

func TestBatchListDeleteAndBackspaceShortcuts(t *testing.T) {
	s := newApplication()
	s.batch = newBatchState([]batchItem{
		{path: "/p/alpha.jpg", absPath: "/p/alpha.jpg", relPath: "alpha.jpg", format: "jpeg", width: 64, height: 64, originalSize: 4096, status: batchPending},
		{path: "/p/beta.jpg", absPath: "/p/beta.jpg", relPath: "beta.jpg", format: "jpeg", width: 64, height: 64, originalSize: 4096, status: batchPending},
		{path: "/p/gamma.jpg", absPath: "/p/gamma.jpg", relPath: "gamma.jpg", format: "jpeg", width: 64, height: 64, originalSize: 4096, status: batchPending},
	})
	tt := ui.NewTester(s.view, 980, 720)

	// Typing Backspace in search field must edit query only, not delete any queue item.
	searchField, ok := tt.AccessByRole(ui.RoleTextField, "筛选文件名")
	if !ok {
		t.Fatal("expected search field")
	}
	tt.ClickAt(searchField.Bounds.X+searchField.Bounds.W/2, searchField.Bounds.Y+searchField.Bounds.H/2)
	tt.Type("be")
	tt.Key(0, ui.KeyBackspace)
	if len(s.batch.items) != 3 || s.batch.query != "b" {
		t.Fatalf("Backspace in search field must not remove batch items: count=%d query=%q", len(s.batch.items), s.batch.query)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(0, ui.KeyBackspace)
	if len(s.batch.items) != 3 || s.batch.query != "" {
		t.Fatalf("clearing search field must leave batch items intact: count=%d query=%q", len(s.batch.items), s.batch.query)
	}

	// Focus the list row and remove alpha.jpg via Delete.
	if err := tt.Click("alpha.jpg"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyDelete)
	if len(s.batch.items) != 2 || s.batch.items[0].relPath != "beta.jpg" || s.batch.selected != 0 {
		t.Fatalf("Delete on focused list must remove alpha.jpg and advance to beta.jpg: selected=%d items=%+v", s.batch.selected, s.batch.items)
	}

	// Modal confirmation must block Delete/Backspace removal even if the list had focus beforehand.
	s.confirmBatchOverwrite = true
	tt.Frame()
	tt.Key(0, ui.KeyDelete)
	if len(s.batch.items) != 2 {
		t.Fatalf("modal confirmation must block Delete key removal, count=%d", len(s.batch.items))
	}
	s.confirmBatchOverwrite = false
	tt.Frame()
	if err := tt.Click("beta.jpg"); err != nil {
		t.Fatal(err)
	}

	tt.Key(0, ui.KeyBackspace)
	if len(s.batch.items) != 1 || s.batch.items[0].relPath != "gamma.jpg" {
		t.Fatalf("Backspace on focused list must remove beta.jpg cleanly: items=%+v", s.batch.items)
	}

	tt.Key(0, ui.KeyBackspace)
	if s.batch != nil {
		t.Fatalf("removing last batch item via Backspace must return cleanly to empty state, got %+v", s.batch)
	}
}

func TestBatchPreviewQuickActionsAndMinimumWindowLayout(t *testing.T) {
	srcImg := ui.NewBitmap(image.NewNRGBA(image.Rect(0, 0, 24, 16)))
	for _, dark := range []bool{false, true} {
		name := "light"
		if dark {
			name = "dark"
		}
		t.Run(name, func(t *testing.T) {
			s := newApplication()
			s.batch = newBatchState([]batchItem{
				{path: "/p/very-long-failed-image-name-for-preview-layout-check.jpg", absPath: "/p/very-long-failed-image-name-for-preview-layout-check.jpg", relPath: "very-long-failed-image-name-for-preview-layout-check.jpg", format: "jpeg", width: 24, height: 16, originalSize: 4096, status: batchFailed, errMsg: "目标磁盘空间不足，请更换目录后重试"},
				{path: "/p/other.png", absPath: "/p/other.png", relPath: "other.png", format: "png", width: 24, height: 16, originalSize: 2048, status: batchPending},
			})
			s.batch.outputMode = batchOutputOverwrite
			s.completeBatchPreview(context.Background(), 0, s.batch.items[0],
				&selectedImage{path: s.batch.items[0].path, format: "jpeg", width: 24, height: 16, size: 4096, preview: srcImg},
				nil, nil, nil)

			tt := ui.NewTester(func(c *ui.Context) {
				theme := ui.LightTheme()
				if dark {
					theme = ui.DarkTheme()
				}
				c.SetTheme(theme)
				s.view(c)
			}, 880, 660)

			if !tt.HasText("失败：目标磁盘空间不足，请更换目录后重试") {
				t.Fatal("preview bottom bar must show failure reason for failed items")
			}
			for _, label := range []string{"返回列表", "上一张", "下一张", "重试", "移除"} {
				button, ok := tt.AccessByRole(ui.RoleButton, label)
				if !ok || button.Offscreen || button.Bounds.X+button.Bounds.W > 880 || button.Bounds.Y+button.Bounds.H > 660 {
					t.Fatalf("preview button %q must fit inside 880x660: %+v", label, button)
				}
			}

			// Clicking 重试 in preview triggers single-item retry flow cleanly.
			if err := tt.Click("重试"); err != nil {
				t.Fatal(err)
			}
			if !s.confirmBatchOverwrite || s.pendingBatchRun != batchRunSingle || s.pendingBatchIndex != 0 {
				t.Fatalf("clicking 重试 in preview must trigger single-item retry: confirm=%v mode=%v index=%d", s.confirmBatchOverwrite, s.pendingBatchRun, s.pendingBatchIndex)
			}
			tt.Key(0, ui.KeyEscape)
			if s.confirmBatchOverwrite || !s.batch.previewOpen {
				t.Fatalf("canceling overwrite prompt must return to preview cleanly: confirm=%v previewOpen=%v", s.confirmBatchOverwrite, s.batch.previewOpen)
			}

			// Clicking 移除 in preview removes the previewed item and exits preview cleanly.
			if err := tt.Click("移除"); err != nil {
				t.Fatal(err)
			}
			if s.batch == nil || s.batch.previewOpen || len(s.batch.items) != 1 || s.batch.items[0].relPath != "other.png" {
				t.Fatalf("clicking 移除 in preview must remove previewed item and close preview: %+v", s.batch)
			}
		})
	}
}

func TestBatchDonePreviewWithComparisonFitsMinimumWindow(t *testing.T) {
	srcImg := ui.NewBitmap(image.NewNRGBA(image.Rect(0, 0, 24, 16)))
	outImg := ui.NewBitmap(image.NewNRGBA(image.Rect(0, 0, 24, 16)))
	for _, width := range []int{760, 880, 980} {
		for _, dark := range []bool{false, true} {
			name := "light"
			if dark {
				name = "dark"
			}
			t.Run(fmt.Sprintf("%d/%s", width, name), func(t *testing.T) {
				items := make([]batchItem, 100)
				for i := range items {
					items[i] = batchItem{
						path: "/p/img.jpg", absPath: "/p/img-" + string(rune('a'+i%26)) + string(rune('0'+i/26)) + ".jpg",
						relPath: "img.jpg", format: "jpeg", width: 24, height: 16, originalSize: 4096, outputSize: 2048,
						status: batchDone, savedPath: "/out/img.jpg",
					}
				}
				s := newApplication()
				s.batch = newBatchState(items)
				s.completeBatchPreview(context.Background(), 99, s.batch.items[99],
					&selectedImage{path: s.batch.items[99].path, format: "jpeg", width: 24, height: 16, size: 4096, preview: srcImg},
					&compress.Result{SourcePath: s.batch.items[99].path, Format: "jpeg", Width: 24, Height: 16, OriginalSize: 4096, OutputSize: 2048},
					outImg, nil)
				tt := ui.NewTester(func(c *ui.Context) {
					theme := ui.LightTheme()
					if dark {
						theme = ui.DarkTheme()
					}
					c.SetTheme(theme)
					s.view(c)
				}, width, 660)
				if !tt.HasText("100 / 100") {
					t.Fatalf("preview header must show 100 / 100 counter, texts=%v", tt.Texts())
				}
				for _, label := range []string{"返回列表", "上一张", "下一张", "缩小", "放大", "适应", "1:1", "移除"} {
					button, ok := tt.AccessByRole(ui.RoleButton, label)
					if !ok || button.Offscreen || button.Bounds.H < 24 || button.Bounds.X+button.Bounds.W > float32(width) || button.Bounds.Y+button.Bounds.H > 660 {
						t.Fatalf("preview button %q must fit inside %dx660: %+v", label, width, button)
					}
				}
				modeGroup, ok := tt.AccessByRole(ui.RoleRadioGroup, "预览模式")
				if !ok || modeGroup.Offscreen || modeGroup.Bounds.H < 24 || modeGroup.Bounds.X+modeGroup.Bounds.W > float32(width) {
					t.Fatalf("comparison mode control must fit inside %dx660: %+v", width, modeGroup)
				}
				nextBtn, _ := tt.AccessByRole(ui.RoleButton, "下一张")
				if nextBtn.Bounds.X+nextBtn.Bounds.W > modeGroup.Bounds.X &&
					nextBtn.Bounds.X < modeGroup.Bounds.X+modeGroup.Bounds.W &&
					nextBtn.Bounds.Y+nextBtn.Bounds.H > modeGroup.Bounds.Y &&
					nextBtn.Bounds.Y < modeGroup.Bounds.Y+modeGroup.Bounds.H {
					t.Fatalf("preview controls must not overlap mode switch: next=%+v mode=%+v", nextBtn, modeGroup)
				}
			})
		}
	}
}

func TestBatchQueueSortingAndSelection(t *testing.T) {
	s := newApplication()
	s.batch = newBatchState([]batchItem{
		{path: "/p/sub/B.jpg", absPath: "/p/sub/B.jpg", relPath: filepath.Join("sub", "B.jpg"), format: "jpeg", width: 40, height: 40, originalSize: 4000, outputSize: 1000, status: batchDone, savedPath: "/out/sub/B.jpg"},
		{path: "/p/a.jpg", absPath: "/p/a.jpg", relPath: "a.jpg", format: "jpeg", width: 40, height: 40, originalSize: 2000, outputSize: 1000, status: batchDone, savedPath: "/out/a.jpg"},
		{path: "/p/c.png", absPath: "/p/c.png", relPath: "c.png", format: "png", width: 40, height: 40, originalSize: 4000, outputSize: 2000, status: batchDone, savedPath: "/out/c.png"},
		{path: "/p/d.png", absPath: "/p/d.png", relPath: "d.png", format: "png", width: 40, height: 40, originalSize: 3000, outputSize: 3000, unchanged: true, status: batchDone, savedPath: "/out/d.png"},
		{path: "/p/e.jpg", absPath: "/p/e.jpg", relPath: "e.jpg", format: "jpeg", width: 40, height: 40, originalSize: 5000, status: batchPending},
		{path: "/p/f.jpg", absPath: "/p/f.jpg", relPath: "f.jpg", format: "jpeg", width: 40, height: 40, originalSize: 1500, status: batchFailed, errMsg: "写入失败"},
		{path: "/p/broken.jpg", absPath: "/p/broken.jpg", relPath: "broken.jpg", status: batchFailed, invalidInput: true, errMsg: "无法识别"},
	})
	tt := ui.NewTester(s.view, 980, 720)

	if !tt.HasText("默认顺序") {
		t.Fatal("batch queue must default to 默认顺序 sort")
	}

	chooseSort := func(menuLabel, expectedBtnLabel string) {
		t.Helper()
		if err := tt.Click("队列排序"); err != nil {
			t.Fatal(err)
		}
		if err := tt.ChooseMenuItem(menuLabel); err != nil {
			t.Fatal(err)
		}
		if !tt.HasText(expectedBtnLabel) {
			t.Fatalf("sort button must update to %q after choosing %q", expectedBtnLabel, menuLabel)
		}
	}

	// 1. Name (A-Z), case-insensitive relative path order.
	chooseSort("按名称 (A–Z)", "按名称")
	if got, want := s.filteredBatchIndices(), []int{1, 6, 2, 3, 4, 5, 0}; !slices.Equal(got, want) {
		t.Fatalf("name sort order mismatch: got=%v want=%v", got, want)
	}

	// Select c.png (index 2) and verify selection follows item across sort changes.
	s.batch.list.ScrollIntoView(slices.Index(s.filteredBatchIndices(), 2))
	tt.Frame()
	if err := tt.Click("c.png"); err != nil {
		t.Fatal(err)
	}
	if s.batch.selected != 2 {
		t.Fatalf("expected c.png (index 2) selected, got %d", s.batch.selected)
	}

	// 2. Original size descending (name tie-break for equal 4000B: c.png before sub/B.jpg; invalidInput 0B last).
	chooseSort("原大小（从大到小）", "原大小 ↓")
	if got, want := s.filteredBatchIndices(), []int{4, 2, 0, 3, 1, 5, 6}; !slices.Equal(got, want) {
		t.Fatalf("original size sort order mismatch: got=%v want=%v", got, want)
	}
	if s.batch.selected != 2 {
		t.Fatalf("selected item must stay c.png (index 2) after size sort, got %d", s.batch.selected)
	}

	// 3. Savings ratio descending: 75% (0: sub/B.jpg), 50% w/ 2000B saved (2: c.png),
	//    50% w/ 1000B saved (1: a.jpg), unchanged done (3: d.png), pending (4: e.jpg), failed (5, 6).
	chooseSort("节省比例（从高到低）", "节省率 ↓")
	if got, want := s.filteredBatchIndices(), []int{0, 2, 1, 3, 4, 5, 6}; !slices.Equal(got, want) {
		t.Fatalf("savings sort order mismatch: got=%v want=%v", got, want)
	}
	if s.batch.selected != 2 {
		t.Fatalf("selected item must stay c.png (index 2) after savings sort, got %d", s.batch.selected)
	}

	// Filter + search compose with active sort; resetting filter keeps sort mode.
	if err := tt.Click("已完成"); err != nil {
		t.Fatal(err)
	}
	if got, want := s.filteredBatchIndices(), []int{0, 2, 1, 3}; !slices.Equal(got, want) {
		t.Fatalf("已完成 filter with savings sort mismatch: got=%v want=%v", got, want)
	}
	s.batch.query = "no-match"
	tt.Frame()
	if err := tt.Click("显示全部"); err != nil {
		t.Fatal(err)
	}
	if s.batch.sort != batchSortSavings || !tt.HasText("节省率 ↓") {
		t.Fatalf("resetting filter must preserve active sort mode: sort=%d", s.batch.sort)
	}

	// Removing c.png (visual position 1 under savings sort) advances selection to a.jpg (next visual item).
	if err := tt.Click("c.png"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("移除"); err != nil {
		t.Fatal(err)
	}
	if s.batch.selected < 0 || s.batch.items[s.batch.selected].relPath != "a.jpg" {
		t.Fatalf("removing sorted item must advance selection to next visible item a.jpg, got selected=%d (%+v)", s.batch.selected, s.batch.items)
	}

	// 4. Default insertion order restores original indices.
	chooseSort("按添加顺序", "默认顺序")
	if got, want := s.filteredBatchIndices(), []int{0, 1, 2, 3, 4, 5}; !slices.Equal(got, want) {
		t.Fatalf("default sort order mismatch: got=%v want=%v", got, want)
	}
}

func TestBatchSearchFocusAndPreviewShortcuts(t *testing.T) {
	dir := t.TempDir()
	zPath := filepath.Join(dir, "z.jpg")
	aPath := filepath.Join(dir, "a.jpg")
	mPath := filepath.Join(dir, "m.jpg")
	writeTestJPEG(t, zPath, 80, 60, 90)
	writeTestJPEG(t, aPath, 80, 60, 90)
	writeTestJPEG(t, mPath, 80, 60, 90)

	updates := make(chan func(), 4)
	s := newApplication()
	s.dispatchUpdate = func(fn func()) { updates <- fn }
	scan, err := scanBatchPaths(context.Background(), []string{zPath, aPath, mPath}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	s.completeBatchScan(context.Background(), scan, false, nil)
	// Set alphabetical sort so visible order is a.jpg (idx 1), m.jpg (idx 2), z.jpg (idx 0).
	s.batch.sort = batchSortName
	tt := ui.NewTester(s.view, 980, 720)

	// Cmd+F focuses search field without clicking it; typing filters immediately; Esc clears query.
	tt.Key(ui.Cmd, ui.KeyF)
	searchField, ok := tt.AccessByRole(ui.RoleTextField, "筛选文件名")
	if !ok || !searchField.Focused {
		t.Fatalf("Cmd+F must focus batch search field: %+v", searchField)
	}
	tt.Type("m.jpg")
	if !tt.HasText("批量队列 · 1 / 3 张") || s.batch.query != "m.jpg" {
		t.Fatalf("typing after Cmd+F must filter queue: query=%q", s.batch.query)
	}
	tt.Key(0, ui.KeyEscape)
	if s.batch.query != "" || !tt.HasText("批量队列 · 3 张") {
		t.Fatalf("Escape in non-empty search field must clear query: query=%q", s.batch.query)
	}

	// Move focus away from search field, then verify modal guards block Cmd+F.
	if err := tt.Click("z.jpg"); err != nil {
		t.Fatal(err)
	}
	s.confirmBatchOverwrite = true
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyF)
	if sf, _ := tt.AccessByRole(ui.RoleTextField, "筛选文件名"); sf.Focused {
		t.Fatal("Cmd+F must not focus search field while overwrite confirmation is open")
	}
	tt.Key(0, ui.KeyEscape)
	if s.confirmBatchOverwrite {
		t.Fatal("Escape must dismiss batch overwrite confirmation")
	}
	s.settingsOpen = true
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyF)
	if sf, _ := tt.AccessByRole(ui.RoleTextField, "筛选文件名"); sf.Focused {
		t.Fatal("Cmd+F must not focus search field while settings modal is open")
	}
	s.settingsOpen = false
	tt.Frame()

	// Enter preview on first sorted item (a.jpg, index 1).
	if err := tt.Click("a.jpg"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("预览"); err != nil {
		t.Fatal(err)
	}
	select {
	case fn := <-updates:
		fn()
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for initial batch preview load")
	}
	tt.Frame()
	if !s.batch.previewOpen || !tt.HasText("1 / 3") || !tt.HasText("a.jpg") {
		t.Fatalf("expected preview open on a.jpg (1 / 3), previewOpen=%v idx=%d", s.batch.previewOpen, s.batch.previewIndex)
	}
	prevBtn, _ := tt.AccessByRole(ui.RoleButton, "上一张")
	nextBtn, _ := tt.AccessByRole(ui.RoleButton, "下一张")
	if prevBtn.KeyShortcuts == "" || nextBtn.KeyShortcuts == "" {
		t.Fatalf("preview prev/next buttons must expose shortcut metadata: prev=%q next=%q", prevBtn.KeyShortcuts, nextBtn.KeyShortcuts)
	}

	// Cmd+[ at first item must be a no-op.
	tt.Key(ui.Cmd, ui.KeyBracketLeft)
	if s.busy || filepath.Base(s.file.path) != "a.jpg" {
		t.Fatalf("Cmd+[ at first preview item must do nothing: busy=%v path=%s", s.busy, s.file.path)
	}

	// Cmd+] steps to m.jpg (2 / 3) following sorted order.
	tt.Key(ui.Cmd, ui.KeyBracketRight)
	select {
	case fn := <-updates:
		fn()
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for next batch preview load")
	}
	tt.Frame()
	if filepath.Base(s.file.path) != "m.jpg" || !tt.HasText("2 / 3") {
		t.Fatalf("Cmd+] must step to m.jpg (2 / 3), got %s", s.file.path)
	}

	// Preview zoom shortcuts work inside batch preview.
	fitScale := s.previewScale(s.comparison.viewport)
	tt.Key(ui.Cmd, ui.KeyEqual)
	if s.comparison.scale <= fitScale {
		t.Fatalf("Cmd+= in batch preview must zoom in: scale=%.4f fit=%.4f", s.comparison.scale, fitScale)
	}
	tt.Key(ui.Cmd, ui.Key1)
	if s.comparison.scale != 1 {
		t.Fatalf("Cmd+1 in batch preview must zoom to 100%%, got %.4f", s.comparison.scale)
	}
	tt.Key(ui.Cmd, ui.Key0)
	if s.comparison.scale != 0 {
		t.Fatalf("Cmd+0 in batch preview must reset to fit, got %.4f", s.comparison.scale)
	}

	// Modal guard blocks Cmd+[ / Cmd+] / zoom / Esc behind confirmation.
	s.confirmBatchOverwrite = true
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyBracketLeft)
	tt.Key(ui.Cmd, ui.Key1)
	if s.busy || s.comparison.scale != 0 || !s.batch.previewOpen {
		t.Fatal("modal confirmation must suppress preview navigation and zoom shortcuts")
	}
	s.confirmBatchOverwrite = false
	tt.Frame()

	// Cmd+[ steps back to a.jpg (1 / 3).
	tt.Key(ui.Cmd, ui.KeyBracketLeft)
	select {
	case fn := <-updates:
		fn()
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for previous batch preview load")
	}
	tt.Frame()
	if filepath.Base(s.file.path) != "a.jpg" || !tt.HasText("1 / 3") {
		t.Fatalf("Cmd+[ must return to a.jpg (1 / 3), got %s", s.file.path)
	}
}

func TestBatchCleanupInvalidAndFailedItems(t *testing.T) {
	s := newApplication()
	s.batch = newBatchState([]batchItem{
		{path: "/p/bad1.png", absPath: "/p/bad1.png", relPath: "bad1.png", status: batchFailed, invalidInput: true, errMsg: "不支持动画 PNG"},
		{path: "/p/fail.jpg", absPath: "/p/fail.jpg", relPath: "fail.jpg", format: "jpeg", width: 40, height: 40, originalSize: 4096, status: batchFailed, errMsg: "磁盘已满"},
		{path: "/p/done.jpg", absPath: "/p/done.jpg", relPath: "done.jpg", format: "jpeg", width: 40, height: 40, originalSize: 4096, outputSize: 2048, status: batchDone, savedPath: "/out/done.jpg"},
		{path: "/p/bad2.png", absPath: "/p/bad2.png", relPath: "bad2.png", status: batchFailed, invalidInput: true, errMsg: "损坏"},
	})
	s.batch.selected = 2

	tt := ui.NewTester(s.view, 880, 660)
	if !tt.HasText("3 张失败（2 无效）") {
		t.Fatalf("expected combined failure banner, texts=%v", tt.Texts())
	}

	s.confirmBatchOverwrite = true
	s.removeInvalidBatchItems()
	if len(s.batch.items) != 4 {
		t.Fatalf("modal guard must block removeInvalidBatchItems: len=%d", len(s.batch.items))
	}
	s.confirmBatchOverwrite = false

	if err := tt.Click("移除无效项"); err != nil {
		t.Fatal(err)
	}
	if len(s.batch.items) != 2 || s.batch.items[0].relPath != "fail.jpg" || s.batch.items[1].relPath != "done.jpg" {
		t.Fatalf("expected only fail.jpg and done.jpg to remain, got %+v", s.batch.items)
	}
	if s.batch.selected != 1 {
		t.Fatalf("selection must follow done.jpg to index 1, got %d", s.batch.selected)
	}
	if !tt.HasText("1 张图片处理失败") || tt.HasText("移除无效项") {
		t.Fatalf("banner must update after invalid items removed, texts=%v", tt.Texts())
	}

	if err := tt.Click("失败"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("移除失败项"); err != nil {
		t.Fatal(err)
	}
	if s.batch == nil || len(s.batch.items) != 1 || s.batch.items[0].relPath != "done.jpg" {
		t.Fatalf("expected done.jpg to remain after removing failed items, got %+v", s.batch)
	}
	if s.batch.filter != batchFilterAll || s.batch.selected != 0 {
		t.Fatalf("removing last failed item under 失败 filter must restore 全部 filter and select remaining item: filter=%d selected=%d",
			s.batch.filter, s.batch.selected)
	}

	s2 := newApplication()
	s2.batch = newBatchState([]batchItem{
		{path: "/p/bad1.png", absPath: "/p/bad1.png", relPath: "bad1.png", status: batchFailed, invalidInput: true, errMsg: "损坏"},
		{path: "/p/bad2.png", absPath: "/p/bad2.png", relPath: "bad2.png", status: batchFailed, invalidInput: true, errMsg: "动画"},
	})
	tt2 := ui.NewTester(s2.view, 880, 660)
	if !tt2.HasText("2 张无效图片无法处理") || tt2.HasText("重试失败项") || tt2.HasText("移除失败项") {
		t.Fatalf("invalid-only banner mismatch, texts=%v", tt2.Texts())
	}
	if err := tt2.Click("移除无效项"); err != nil {
		t.Fatal(err)
	}
	if s2.batch != nil || !strings.Contains(s2.message, "队列已清空") {
		t.Fatalf("removing all items via cleanup must clear batch cleanly: batch=%v msg=%q", s2.batch, s2.message)
	}
}

func TestBatchRevealOutputFile(t *testing.T) {
	root := t.TempDir()
	srcA := filepath.Join(root, "a.jpg")
	outA := filepath.Join(root, "out", "a.jpg")
	srcB := filepath.Join(root, "b.jpg")
	srcC := filepath.Join(root, "c.jpg")
	writeTestJPEG(t, srcA, 24, 16, 85)
	writeTestJPEG(t, outA, 24, 16, 60)
	writeTestJPEG(t, srcB, 24, 16, 85)
	writeTestJPEG(t, srcC, 24, 16, 85)

	var revealed []string
	updates := make(chan func(), 4)
	s := newApplication()
	s.dispatchUpdate = func(fn func()) { updates <- fn }
	s.showInFolder = func(path string) { revealed = append(revealed, path) }
	s.batch = newBatchState([]batchItem{
		{path: srcA, absPath: srcA, relPath: "a.jpg", format: "jpeg", width: 24, height: 16, originalSize: 4096, outputSize: 2048, status: batchDone, savedPath: outA},
		{path: srcB, absPath: srcB, relPath: "b.jpg", format: "jpeg", width: 24, height: 16, originalSize: 4096, outputSize: 2048, status: batchDone, overwritten: true},
		{path: srcC, absPath: srcC, relPath: "c.jpg", format: "jpeg", width: 24, height: 16, originalSize: 4096, outputSize: 4096, status: batchDone, unchanged: true},
	})
	tt := ui.NewTester(s.view, 880, 660)

	if err := tt.Click("定位文件"); err != nil {
		t.Fatal(err)
	}
	if len(revealed) != 1 || revealed[0] != outA {
		t.Fatalf("expected folder-exported item to reveal %q, got %v", outA, revealed)
	}

	if err := tt.Click("b.jpg"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("定位文件"); err != nil {
		t.Fatal(err)
	}
	if len(revealed) != 2 || revealed[1] != srcB {
		t.Fatalf("expected overwritten item to reveal %q, got %v", srcB, revealed)
	}
	if err := tt.Click("c.jpg"); err != nil {
		t.Fatal(err)
	}
	if _, ok := tt.AccessByRole(ui.RoleButton, "定位文件"); ok {
		t.Fatal("unchanged item without savedPath or overwrite must not show 定位文件")
	}

	if err := tt.Click("a.jpg"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("预览"); err != nil {
		t.Fatal(err)
	}
	select {
	case fn := <-updates:
		fn()
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for batch preview load")
	}
	tt.Frame()
	revealBtn, ok := tt.AccessByRole(ui.RoleButton, "定位文件")
	if !ok || revealBtn.Offscreen || revealBtn.Bounds.X+revealBtn.Bounds.W > 880 || revealBtn.Bounds.Y+revealBtn.Bounds.H > 660 {
		t.Fatalf("preview 定位文件 button must fit inside 880x660: %+v", revealBtn)
	}
	if err := tt.Click("定位文件"); err != nil {
		t.Fatal(err)
	}
	if len(revealed) != 3 || revealed[2] != outA {
		t.Fatalf("preview 定位文件 must reveal %q, got %v", outA, revealed)
	}

	if err := os.Remove(outA); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("定位文件"); err != nil {
		t.Fatal(err)
	}
	if !s.failed || !strings.Contains(s.message, "找不到输出文件") || len(revealed) != 3 {
		t.Fatalf("missing output file in preview must report error instead of revealing: failed=%v msg=%q revealed=%v",
			s.failed, s.message, revealed)
	}
}
