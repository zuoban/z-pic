package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

	// Switching output mode in UI resets completed results.
	s.batch.items[0].status = batchDone
	s.batch.items[0].outputSize = 2000
	tt := ui.NewTester(s.view, 880, 660)
	if err := tt.Click("覆盖原图"); err != nil {
		t.Fatal(err)
	}
	if s.batch.outputMode != batchOutputOverwrite || s.batch.items[0].status != batchPending {
		t.Fatalf("switching output mode must reset processed batch items: mode=%d item=%+v", s.batch.outputMode, s.batch.items[0])
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
	for _, dark := range []bool{false, true} {
		name := "light"
		if dark {
			name = "dark"
		}
		t.Run(name, func(t *testing.T) {
			s := newApplication()
			s.batch = newBatchState([]batchItem{
				{
					path:         "/photos/" + strings.Repeat("很长的目录名/", 8) + "photo-1.jpg",
					absPath:      "/photos/photo-1.jpg",
					relPath:      strings.Repeat("nested/", 6) + "photo-1.jpg",
					format:       "jpeg",
					width:        1920,
					height:       1080,
					originalSize: 2 << 20,
					outputSize:   1 << 20,
					status:       batchDone,
					savedPath:    "/out/photo-1.jpg",
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
			})
			s.batch.outputDir = "/Users/example/" + strings.Repeat("输出目录/", 6)
			tt := ui.NewTester(func(c *ui.Context) {
				theme := ui.LightTheme()
				if dark {
					theme = ui.DarkTheme()
				}
				c.SetTheme(theme)
				s.view(c)
			}, 880, 660)

			for _, label := range []string{"添加图片", "添加文件夹", "清空", "预览", "移除", "重新压缩", "继续压缩", "更改", "设置"} {
				button, ok := tt.AccessByRole(ui.RoleButton, label)
				if !ok || button.Offscreen || button.Bounds.H < 24 ||
					button.Bounds.Y+button.Bounds.H > 660 || button.Bounds.X+button.Bounds.W > 880 {
					t.Fatalf("%q must fit inside minimum window: %+v", label, button)
				}
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

			for _, label := range []string{"添加图片", "添加文件夹", "清空", "预览", "移除", "重新压缩", "继续压缩", "更改", "设置"} {
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
