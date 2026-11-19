package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"zpic/internal/compress"
)

func TestSettingsRoundTripAndEditorForCompressionOptionsAndAutoCompress(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	app := newApplication()
	app.settingsFile = path
	app.presets = testLegacyPresets()
	app.setSettingsOpen(true)
	tester := ui.NewTester(app.settingsView, 724, 700)

	// Toggle auto-compress on General page.
	if err := tester.Click("导入图片或切换预设后自动压缩"); err != nil {
		t.Fatalf("failed toggling auto-compress: %v", err)
	}
	if !app.editor.autoCompress || app.autoCompress {
		t.Fatal("auto-compress must stay in draft until saved")
	}

	// Switch to Presets page and configure lossless, WebP, resize 1920, and ICC-only metadata.
	if err := tester.Click("管理预设"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("无损"); err != nil {
		t.Fatalf("failed toggling lossless: %v", err)
	}
	if !app.editor.selectedPreset().lossless {
		t.Fatal("lossless draft flag must update")
	}
	tester.Scroll(362, 350, 0, 600)
	if err := tester.Click("转为 WebP"); err != nil {
		t.Fatalf("failed selecting WebP format: %v", err)
	}
	if err := tester.Click("1920"); err != nil {
		t.Fatalf("failed selecting 1920 resize: %v", err)
	}
	if err := tester.Click("仅色彩配置"); err != nil {
		t.Fatalf("failed selecting ICC-only metadata: %v", err)
	}
	draft := app.editor.selectedPreset()
	if draft.outputFormat != compress.OutputFormatWebP || draft.maxDimension != 1920 || draft.metadata != compress.MetadataKeepICC {
		t.Fatalf("unexpected draft options: %+v", draft)
	}

	if err := tester.Click("保存"); err != nil {
		t.Fatal(err)
	}
	if app.settingsOpen || !app.autoCompress || !app.lossless ||
		app.outputFormat != compress.OutputFormatWebP || app.maxDimension != 1920 ||
		app.metadata != compress.MetadataKeepICC {
		t.Fatalf("saved settings not applied: auto=%v lossless=%v fmt=%q dim=%d meta=%q",
			app.autoCompress, app.lossless, app.outputFormat, app.maxDimension, app.metadata)
	}

	reloaded, err := loadFullSettings(path)
	if err != nil {
		t.Fatalf("reload failed: %v", err)
	}
	if !reloaded.autoCompress || !reloaded.presets[1].lossless ||
		reloaded.presets[1].outputFormat != compress.OutputFormatWebP ||
		reloaded.presets[1].maxDimension != 1920 ||
		reloaded.presets[1].metadata != compress.MetadataKeepICC {
		t.Fatalf("persisted settings mismatch: %+v", reloaded)
	}

	// Reject invalid persisted values.
	for name, mutate := range map[string]func(*compressionPreset){
		"bad-metadata": func(p *compressionPreset) { p.metadata = "bogus" },
		"bad-format":   func(p *compressionPreset) { p.outputFormat = "avif" },
		"bad-dim-low":  func(p *compressionPreset) { p.maxDimension = 32 },
		"bad-dim-high": func(p *compressionPreset) { p.maxDimension = 20000 },
	} {
		t.Run(name, func(t *testing.T) {
			presets := testLegacyPresets()
			mutate(&presets[0])
			if err := validatePresets(presets, "均衡"); err == nil {
				t.Fatalf("invalid preset (%s) must be rejected", name)
			}
		})
	}
}

func TestMainQuickPresetPickerClearSingleAndOptionSummary(t *testing.T) {
	app := testSelectedApplication()
	app.settingsFile = filepath.Join(t.TempDir(), "settings.json")
	app.presets[2].lossless = true
	app.presets[2].outputFormat = compress.OutputFormatWebP
	app.presets[2].maxDimension = 1920
	app.presets[2].metadata = compress.MetadataKeepICC

	tester := ui.NewTester(app.view, 980, 720)
	picker, ok := tester.AccessByRole(ui.RoleMenuButton, "快速切换预设")
	if !ok || picker.Offscreen || picker.Value != "均衡" {
		t.Fatalf("quick preset picker missing or offscreen: %+v", picker)
	}
	if err := tester.Click("快速切换预设"); err != nil {
		t.Fatal(err)
	}
	if err := tester.ChooseMenuItem("画质优先"); err != nil {
		t.Fatal(err)
	}
	if app.scheme != "画质优先" || !app.lossless || app.outputFormat != compress.OutputFormatWebP ||
		app.maxDimension != 1920 || app.metadata != compress.MetadataKeepICC {
		t.Fatalf("quick preset switch did not apply parameters: scheme=%q lossless=%v fmt=%q dim=%d meta=%q",
			app.scheme, app.lossless, app.outputFormat, app.maxDimension, app.metadata)
	}
	tester.Frame()
	if !tester.HasText("无损压缩") || !tester.HasText("转为 WebP · 长边 ≤ 1920px · 仅色彩配置") {
		t.Fatal("overview must summarize active lossless and processing options")
	}

	// Verify converted result does not treat PNG->WebP as overwrite and displays growth/resize honestly.
	app.file.format, app.file.width, app.file.height = "png", 2000, 1000
	app.result = &compress.Result{
		SourcePath:   app.file.path,
		Format:       "webp",
		Width:        1000,
		Height:       500,
		OriginalSize: 1000,
		OutputSize:   1200,
	}
	tester.Frame()
	if !tester.HasText("增大 20.0%") || !tester.HasText("转为 WEBP · 2000 × 1000 → 1000 × 500") {
		t.Fatal("result summary must show size growth and format/dimension changes")
	}
	if _, ok := tester.AccessByRole(ui.RoleButton, "保存到原文件夹"); !ok {
		t.Fatal("format-converted result in original-folder mode must save as copy, never overwrite")
	}

	// Clicking clear returns to empty state.
	if err := tester.Click("清空"); err != nil {
		t.Fatal(err)
	}
	if app.file != nil || app.preview != nil || app.result != nil {
		t.Fatal("clear button must reset single-image state")
	}
}

func TestAutoCompressTriggersOnSingleAndSafeBatch(t *testing.T) {
	root := t.TempDir()
	jpgPath := filepath.Join(root, "sample.jpg")
	writeTestJPEG(t, jpgPath, 120, 90, 95)

	updates := make(chan func(), 16)
	app := newApplication()
	app.autoCompress = true
	app.dispatchUpdate = func(fn func()) { updates <- fn }
	app.load(jpgPath)

	// First update completes selection and immediately launches auto-compression.
	fn := <-updates
	fn()
	if app.file == nil || !app.busy || app.phase != "正在压缩图片" {
		t.Fatalf("autoCompress must immediately start single compression: file=%v busy=%v phase=%q", app.file != nil, app.busy, app.phase)
	}
	fn = <-updates
	fn()
	if app.busy || app.result == nil {
		t.Fatalf("auto-compression did not complete: busy=%v result=%v", app.busy, app.result)
	}

	// Safe batch (subfolder copy) auto-compresses; overwrite mode does NOT auto-compress silently.
	app2 := newApplication()
	app2.autoCompress = true
	app2.output = presetSaveOptions{Mode: presetSaveOriginal, UseSubfolder: true, Subfolder: "zpic"}
	app2.dispatchUpdate = func(fn func()) { updates <- fn }
	app2.loadBatch([]string{jpgPath}, false)
	(<-updates)() // completeBatchScan -> should launch batch compression
	if app2.batch == nil || !app2.batch.running {
		t.Fatal("safe subfolder batch with autoCompress must auto-start")
	}
	for app2.busy {
		(<-updates)()
	}
	if app2.batch.items[0].status != batchDone || app2.batch.items[0].savedPath == "" {
		t.Fatalf("auto-compressed batch item did not save: %+v", app2.batch.items[0])
	}
	if app2.batchEffectiveOutputDir() != filepath.Join(root, "zpic") {
		t.Fatalf("expected effective output dir %q, got %q", filepath.Join(root, "zpic"), app2.batchEffectiveOutputDir())
	}

	// Overwrite mode must NOT silently auto-compress.
	app3 := newApplication()
	app3.autoCompress = true
	app3.output = presetSaveOptions{Mode: presetSaveOriginal}
	app3.dispatchUpdate = func(fn func()) { updates <- fn }
	app3.loadBatch([]string{jpgPath}, false)
	(<-updates)()
	if app3.busy || app3.confirmBatchOverwrite || app3.batch.running {
		t.Fatal("overwrite mode must never auto-compress without explicit confirmation")
	}
}

func TestFloatingCompressCopyAndConcurrentWebDownloads(t *testing.T) {
	root := t.TempDir()
	imgPath := filepath.Join(root, "shot.jpg")
	writeTestJPEG(t, imgPath, 100, 80, 95)

	// 1. Floating window compress + copy actions fit cleanly and work end-to-end.
	updates := make(chan func(), 32)
	app := newApplication()
	app.floating.expanded = true
	app.output = presetSaveOptions{Mode: presetSaveOriginal, UseSuffix: true, Suffix: "-min"}
	app.dispatchUpdate = func(fn func()) { updates <- fn }
	var copied []byte
	app.writeClipboardImage = func(data []byte) error {
		copied = bytes.Clone(data)
		return nil
	}
	app.batch = newBatchState([]batchItem{{
		path: imgPath, absPath: imgPath, relPath: "shot.jpg",
		format: "jpeg", width: 100, height: 80, originalSize: 4096,
	}})
	app.configureBatchPresetOutput()

	tester := ui.NewTester(app.floatingView, floatingExpandedWidth, floatingExpandedHeight)
	btn, ok := tester.AccessByRole(ui.RoleButton, "开始压缩")
	if !ok || btn.Offscreen || btn.Disabled || btn.Bounds.X+btn.Bounds.W > floatingExpandedWidth {
		t.Fatalf("floating compress button must fit and be enabled: %+v", btn)
	}
	if err := tester.Click("开始压缩"); err != nil {
		t.Fatal(err)
	}
	for app.busy {
		(<-updates)()
	}
	if app.batch.items[0].status != batchDone || app.batch.items[0].savedPath == "" {
		t.Fatalf("floating compress did not complete item: %+v", app.batch.items[0])
	}
	tester.Frame()
	copyBtn, ok := tester.AccessByRole(ui.RoleButton, "复制图片")
	if !ok || copyBtn.Offscreen || copyBtn.Disabled || copyBtn.Bounds.X+copyBtn.Bounds.W > floatingExpandedWidth {
		t.Fatalf("floating copy button must appear and fit for completed item: %+v", copyBtn)
	}
	if err := tester.Click("复制图片"); err != nil {
		t.Fatal(err)
	}
	for app.busy {
		(<-updates)()
	}
	if len(copied) == 0 || !bytes.HasPrefix(copied, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatalf("floating copy did not write valid PNG clipboard data: len=%d", len(copied))
	}

	// 2. Concurrent web downloads execute in parallel while preserving import order.
	var inFlight, maxInFlight atomic.Int32
	gate := make(chan struct{})
	var closeGate sync.Once
	original := writeTestJPEG(t, filepath.Join(root, "remote.jpg"), 64, 48, 95)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := inFlight.Add(1)
		for {
			prev := maxInFlight.Load()
			if cur <= prev || maxInFlight.CompareAndSwap(prev, cur) {
				break
			}
		}
		if cur >= 2 {
			closeGate.Do(func() { close(gate) })
		}
		select {
		case <-gate:
		case <-time.After(2 * time.Second):
		}
		inFlight.Add(-1)
		_, _ = w.Write(original)
	}))
	defer server.Close()

	app2 := newApplication()
	app2.dropTransport = newLoopbackDropTransport(t)
	app2.dispatchUpdate = func(fn func()) { updates <- fn }
	app2.downloadDir = filepath.Join(root, "downloads")
	urls := []string{server.URL + "/a/img.jpg", server.URL + "/b/img.jpg", server.URL + "/c/img.jpg"}
	app2.receiveDrop(transfer.New(transfer.NewItem(transfer.Bytes(transfer.URIList, []byte(strings.Join(urls, "\r\n"))))))
	waitForDropImports(t, app2, updates)
	if maxInFlight.Load() < 2 {
		t.Fatalf("expected concurrent web downloads (>=2 in flight), got max=%d", maxInFlight.Load())
	}
	if app2.batch == nil || len(app2.batch.items) != 3 {
		t.Fatalf("unexpected concurrent import result: %+v", app2.batch)
	}
	for i, want := range []string{"img.jpg", "img-1.jpg", "img-2.jpg"} {
		if filepath.Base(app2.batch.items[i].path) != want {
			t.Fatalf("item %d filename=%q want %q", i, filepath.Base(app2.batch.items[i].path), want)
		}
	}
}

func TestBatchDirectListCopyAndMixedWebPConvertPreservesOriginal(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("bundled native PNG/WebP tools currently ship only for darwin/arm64")
	}
	root := t.TempDir()
	jpgPath := filepath.Join(root, "pic.jpg")
	writeTestJPEG(t, jpgPath, 160, 120, 95)
	origBytes, err := os.ReadFile(jpgPath)
	if err != nil {
		t.Fatal(err)
	}

	updates := make(chan func(), 32)
	app := newApplication()
	app.output = presetSaveOptions{Mode: presetSaveOriginal}
	app.outputFormat = compress.OutputFormatWebP
	app.dispatchUpdate = func(fn func()) { updates <- fn }
	var copied []byte
	app.writeClipboardImage = func(data []byte) error {
		copied = bytes.Clone(data)
		return nil
	}
	app.batch = newBatchState([]batchItem{{
		path: jpgPath, absPath: jpgPath, relPath: "pic.jpg",
		format: "jpeg", width: 160, height: 120, originalSize: int64(len(origBytes)),
	}})
	app.configureBatchPresetOutput()

	// Clicking 压缩此张 directly from the batch list must NOT ask to overwrite .jpg; it saves pic.webp alongside.
	tester := ui.NewTester(app.view, 980, 720)
	if err := tester.Click("压缩此张"); err != nil {
		t.Fatalf("direct list compress button missing: %v", err)
	}
	if app.confirmBatchOverwrite {
		t.Fatal("JPEG->WebP conversion must not request overwrite confirmation when no WebP source is replaced")
	}
	for app.busy {
		(<-updates)()
	}
	item := app.batch.items[0]
	if item.status != batchDone || item.overwritten || filepath.Ext(item.savedPath) != ".webp" {
		t.Fatalf("expected non-destructive .webp output, got %+v", item)
	}
	afterOrig, err := os.ReadFile(jpgPath)
	if err != nil || !bytes.Equal(afterOrig, origBytes) {
		t.Fatal("original .jpg must remain completely untouched after WebP conversion")
	}

	// Completed item exposes 复制图片 and 打开输出文件夹 directly in list view.
	tester.Frame()
	if _, ok := tester.AccessByRole(ui.RoleButton, "打开输出文件夹"); !ok {
		t.Fatal("completed original-folder copy batch must expose 打开输出文件夹")
	}
	if err := tester.Click("复制图片"); err != nil {
		t.Fatalf("direct list copy button missing: %v", err)
	}
	for app.busy {
		(<-updates)()
	}
	if len(copied) == 0 || !bytes.HasPrefix(copied, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatalf("direct list copy did not write valid PNG clipboard data: len=%d", len(copied))
	}
}
