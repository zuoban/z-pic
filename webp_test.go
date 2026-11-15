package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"zpic/internal/compress"
)

func webPAppFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("internal", "compress", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func writeWebPAppFixture(t *testing.T, path, name string) []byte {
	t.Helper()
	data := webPAppFixture(t, name)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return data
}

func TestWebPReadSelectionAndHeader(t *testing.T) {
	tests := []struct {
		name   string
		width  int
		height int
		alpha  bool
	}{
		{"opaque.webp", 256, 192, false},
		{"alpha.webp", 96, 64, true},
		{"tiny.webp", 1, 1, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), test.name)
			source := writeWebPAppFixture(t, path, test.name)
			selected, err := readSelection(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			if selected.path != path || selected.format != "webp" ||
				selected.width != test.width || selected.height != test.height ||
				selected.size != int64(len(source)) || !bytes.Equal(selected.data, source) {
				t.Fatalf("incorrect WebP selection: %+v", selected)
			}
			if selected.preview == nil {
				t.Fatal("WebP selection has no UI bitmap")
			}
			width, height := selected.preview.Size()
			if width != test.width || height != test.height {
				t.Fatalf("UI bitmap dimensions = %dx%d", width, height)
			}
			decoded, format, err := image.Decode(bytes.NewReader(selected.data))
			if err != nil || format != "webp" {
				t.Fatalf("selected image decode = %q, %v", format, err)
			}
			hasAlpha := false
			for row := range test.height {
				for column := range test.width {
					_, _, _, alpha := decoded.At(column, row).RGBA()
					hasAlpha = hasAlpha || alpha < 0xffff
				}
			}
			if hasAlpha != test.alpha {
				t.Fatalf("source transparency = %v; want %v", hasAlpha, test.alpha)
			}
			app := newApplication()
			app.completeSelection(context.Background(), selected, nil)
			tester := ui.NewTester(app.view, 880, 660)
			header := fmt.Sprintf("WEBP  ·  %d × %d  ·  %s", test.width, test.height, formatSize(int64(len(source))))
			if !tester.HasText(header) || app.preview != selected.preview {
				t.Fatalf("WebP header or preview missing: %q", tester.Texts())
			}
		})
	}
}

func TestWebPAnimationRejectedBySelectionAndQueue(t *testing.T) {
	flagged := append([]byte(nil), webPAppFixture(t, "alpha.webp")...)
	flagged[20] |= 2
	chunked := append([]byte(nil), webPAppFixture(t, "opaque.webp")...)
	chunked = append(chunked, []byte{'A', 'N', 'I', 'M', 6, 0, 0, 0, 0, 0, 0, 0, 0, 0}...)
	binary.LittleEndian.PutUint32(chunked[4:], uint32(len(chunked)-8))
	for name, data := range map[string][]byte{"flag.webp": flagged, "chunk.webp": chunked} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, name)
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			selected, err := readSelection(context.Background(), path)
			if err == nil || selected != nil || !strings.Contains(err.Error(), "动画 WebP") {
				t.Fatalf("animated selection = %+v, %v", selected, err)
			}
			app := newApplication()
			app.completeSelection(context.Background(), selected, err)
			if !app.failed || app.file != nil || !strings.Contains(app.message, "动画 WebP") {
				t.Fatalf("animated selection was not reported: %q", app.message)
			}
			scan, err := scanBatchPaths(context.Background(), []string{dir}, nil, 0)
			if err != nil || len(scan.items) != 1 {
				t.Fatalf("animated queue scan = %+v, %v", scan, err)
			}
			item := scan.items[0]
			if item.status != batchFailed || !item.invalidInput || !strings.Contains(item.errMsg, "动画 WebP") {
				t.Fatalf("animated queue item accepted: %+v", item)
			}
		})
	}
}

func TestWebPFolderScanAndDefaultOutputNames(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"lower.webp", "nested/UPPER.WEBP"} {
		path := filepath.Join(root, filepath.FromSlash(name))
		writeWebPAppFixture(t, path, "tiny.webp")
		if !isSupportedImageExt(path) {
			t.Fatalf("WebP extension rejected: %q", path)
		}
		want := strings.TrimSuffix(path, filepath.Ext(path)) + "-compressed.webp"
		if got := suggestedPath(path, ".webp"); got != want {
			t.Fatalf("suggestedPath = %q; want %q", got, want)
		}
		if err := os.WriteFile(want, []byte("keep"), 0600); err != nil {
			t.Fatal(err)
		}
		if got := suggestedPath(path, ".webp"); got != strings.TrimSuffix(want, ".webp")+"-2.webp" {
			t.Fatalf("WebP collision name = %q", got)
		}
		if err := os.Remove(want); err != nil {
			t.Fatal(err)
		}
	}
	scan, err := scanBatchPaths(context.Background(), []string{root}, nil, 0)
	if err != nil || len(scan.items) != 2 {
		t.Fatalf("folder scan = %+v, %v", scan, err)
	}
	for _, item := range scan.items {
		if item.status != batchPending || item.format != "webp" || item.width != 1 || item.height != 1 {
			t.Fatalf("incorrect WebP queue metadata: %+v", item)
		}
	}
}

func TestWebPThumbnailAndArrowListAccess(t *testing.T) {
	root := t.TempDir()
	paths := []string{filepath.Join(root, "alpha.webp"), filepath.Join(root, "tiny.WEBP")}
	writeWebPAppFixture(t, paths[0], "alpha.webp")
	writeWebPAppFixture(t, paths[1], "tiny.webp")
	scan, err := scanBatchPaths(context.Background(), paths, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	app := newApplication()
	app.batch = newBatchState(scan.items)
	tester := ui.NewTester(app.view, 880, 660)
	if _, ok := tester.AccessByRole(ui.RoleList, "批量图片列表"); !ok {
		t.Fatal("WebP queue has no accessible list")
	}
	for _, item := range scan.items {
		if _, ok := tester.AccessByRole(ui.RoleImage, item.relPath+" 缩略图"); !ok {
			t.Fatalf("WebP thumbnail lacks accessible name: %q", item.relPath)
		}
	}
	if err := tester.Click(scan.items[0].relPath); err != nil {
		t.Fatal(err)
	}
	tester.Key(0, ui.KeyDown)
	if app.batch.selected != 1 {
		t.Fatal("arrow navigation did not select the next WebP")
	}
	updates := make(chan func(), len(paths))
	app.dispatchUpdate = func(update func()) { updates <- update }
	app.startNextBatchThumbnail()
	for range paths {
		nextThumbnailUpdate(t, updates)()
	}
	for _, item := range app.batch.items {
		entry := app.thumbnails.entries[item.path]
		if entry == nil || entry.failed || entry.bitmap == nil {
			t.Fatalf("WebP thumbnail did not load: %+v", entry)
		}
		width, height := entry.bitmap.Size()
		if width != item.width || height != item.height {
			t.Fatalf("small WebP thumbnail dimensions = %dx%d; want %dx%d", width, height, item.width, item.height)
		}
	}
}

func TestWebPQualityLabelUsesPersistedQuality(t *testing.T) {
	t.Setenv("ZPIC_CONFIG_FILE", filepath.Join(t.TempDir(), "settings.json"))
	app := newApplication()
	app.restoreSettings()
	tester := openTestSettings(t, app)
	if _, ok := tester.AccessByRole(ui.RoleSlider, "JPEG / WebP 质量"); !ok {
		t.Fatal("shared JPEG / WebP quality control is missing")
	}
	if err := tester.Click("新增方案"); err != nil {
		t.Fatal(err)
	}
	replaceTestPresetName(t, tester, "WebP 分享")
	app.editQuality(37)
	if err := tester.Click("保存"); err != nil {
		t.Fatal(err)
	}
	main := ui.NewTester(app.view, 880, 660)
	if err := main.Click("WebP 分享"); err != nil {
		t.Fatal(err)
	}
	restarted := newApplication()
	restarted.restoreSettings()
	if restarted.scheme != "WebP 分享" || restarted.quality != 37 ||
		restarted.compressionOptions().Quality != 37 || restarted.png != app.png {
		t.Fatalf("shared quality did not survive restart: scheme=%q quality=%v", restarted.scheme, restarted.quality)
	}
}

func TestWebPMixedBatchFolderExportAndRecompression(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("bundled native encoders require darwin/arm64")
	}
	root := t.TempDir()
	inputDir, outputDir := filepath.Join(root, "input"), filepath.Join(root, "output")
	if err := os.MkdirAll(outputDir, 0700); err != nil {
		t.Fatal(err)
	}
	sources := map[string][]byte{}
	jpegPath := filepath.Join(inputDir, "photo.jpg")
	pngPath := filepath.Join(inputDir, "nested", "chart.png")
	sources[jpegPath] = writeTestJPEG(t, jpegPath, 160, 120, 100)
	sources[pngPath] = writeTestPNG(t, pngPath, 120, 90, true)
	for _, name := range []string{"opaque.webp", "alpha.webp", "tiny.webp"} {
		path := filepath.Join(inputDir, "nested", name)
		sources[path] = writeWebPAppFixture(t, path, name)
	}
	scan, err := scanBatchPaths(context.Background(), []string{inputDir}, nil, 0)
	if err != nil || len(scan.items) != len(sources) {
		t.Fatalf("mixed queue scan = %+v, %v", scan, err)
	}
	app := newApplication()
	app.setScheme("体积优先")
	app.batch = newBatchState(scan.items)
	app.batch.outputMode, app.batch.outputDir = batchOutputFolder, outputDir
	updates := make(chan func(), len(sources)*3+1)
	app.dispatchUpdate = func(update func()) { updates <- update }
	previousOutputs := make(map[string][]byte)
	for run := range 2 {
		expected := make(map[string]*compress.Result)
		for path := range sources {
			result, err := compress.File(context.Background(), path, app.compressionOptions())
			if err != nil {
				t.Fatalf("independent compression of %q: %v", path, err)
			}
			expected[path] = result
		}
		if run == 0 {
			app.startBatchCompression()
		} else {
			app.restartBatchCompression()
		}
		if !app.busy || app.batch.total != len(sources) || app.batch.done != 0 {
			t.Fatalf("run %d: busy=%v total=%d done=%d", run, app.busy, app.batch.total, app.batch.done)
		}
		finished := make(chan struct{})
		go func() {
			app.saves.Wait()
			close(finished)
		}()
		select {
		case <-finished:
		case <-time.After(20 * time.Second):
			t.Fatal("mixed batch worker did not finish with queued UI updates")
		}
		if !app.busy || app.batch.done != 0 {
			t.Fatal("worker changed UI state before queued updates were applied")
		}
		for len(updates) > 0 {
			(<-updates)()
		}
		if app.busy || app.failed || app.batch.done != len(sources) {
			t.Fatalf("run %d: busy=%v done=%d message=%q items=%+v", run, app.busy, app.batch.done, app.message, app.batch.items)
		}
		for _, item := range app.batch.items {
			result := expected[item.path]
			if result == nil {
				t.Fatalf("unexpected queue source: %q", item.path)
			}
			if item.status != batchDone || item.format != result.Format || item.width != result.Width ||
				item.height != result.Height || item.outputSize != result.OutputSize ||
				item.unchanged != result.Unchanged || item.overwritten {
				t.Fatalf("run %d item differs from compression: %+v", run, item)
			}
			source, err := os.ReadFile(item.path)
			if err != nil || !bytes.Equal(source, sources[item.path]) {
				t.Fatalf("source %q changed: %v", item.path, err)
			}
			output, err := os.ReadFile(item.savedPath)
			if err != nil || !bytes.Equal(output, result.Data) {
				t.Fatalf("incorrect exported bytes for %q: %v", item.path, err)
			}
			if filepath.Ext(item.savedPath) != filepath.Ext(item.path) ||
				filepath.Dir(item.savedPath) != filepath.Dir(filepath.Join(outputDir, item.relPath)) {
				t.Fatalf("export lost format or relative directory: %q", item.savedPath)
			}
			if _, exists := previousOutputs[item.savedPath]; exists {
				t.Fatalf("recompression reused earlier output: %q", item.savedPath)
			}
			previousOutputs[item.savedPath] = output
			if filepath.Base(item.path) == "tiny.webp" && (!item.unchanged || !bytes.Equal(output, source)) {
				t.Fatal("tiny WebP should export unchanged original bytes")
			}
		}
		for path, before := range previousOutputs {
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(after, before) {
				t.Fatalf("run %d changed a previous export %q: %v", run, path, err)
			}
		}
	}
}
