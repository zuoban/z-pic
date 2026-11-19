package main

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"zpic/internal/compress"
)

func createHEICTestFile(t *testing.T, dir, name string, w, h int) string {
	t.Helper()
	if runtime.GOOS != "darwin" || !compress.SupportsHEIC() {
		t.Skip("macOS HEIC support unavailable")
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetRGBA(x, y, color.RGBA{
				R: uint8((x * 255) / max(1, w-1)),
				G: uint8((y * 255) / max(1, h-1)),
				B: uint8(((x ^ y) * 7) & 0xff),
				A: 255,
			})
		}
	}
	tmpPNG := filepath.Join(t.TempDir(), "src.png")
	f, err := os.Create(tmpPNG)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()
	outPath := filepath.Join(dir, name)
	if out, err := exec.Command("/usr/bin/sips", "-s", "format", "heic", "--out", outPath, tmpPNG).CombinedOutput(); err != nil {
		t.Fatalf("sips encode heic failed: %v (%s)", err, out)
	}
	return outPath
}

func TestHEICSingleSelectionPreviewAndSavePolicy(t *testing.T) {
	dir := t.TempDir()
	heicPath := createHEICTestFile(t, dir, "IMG_0001.HEIC", 120, 90)

	selected, err := readSelection(context.Background(), heicPath)
	if err != nil {
		t.Fatalf("readSelection(heic) failed: %v", err)
	}
	if selected.format != "heic" || selected.width != 120 || selected.height != 90 || selected.preview == nil {
		t.Fatalf("unexpected selected HEIC: format=%q %dx%d preview=%v", selected.format, selected.width, selected.height, selected.preview != nil)
	}

	app := newApplication()
	app.file = selected
	app.output = presetSaveOptions{Mode: presetSaveOriginal}
	app.outputFormat = compress.OutputFormatKeep
	app.lossless = false

	// Even in overwrite mode, HEIC must NOT replace original.
	if app.singleReplacesOriginal() {
		t.Fatalf("expected singleReplacesOriginal() == false for HEIC")
	}

	// Output filename examples for lossy, lossless, webp.
	if got := presetOutputNameForResolved(app.output, "IMG_0001.HEIC", "heic", compress.OutputFormatKeep, false); got != "IMG_0001.jpg" {
		t.Fatalf("lossy HEIC output example = %q, want IMG_0001.jpg", got)
	}
	if got := presetOutputNameForResolved(app.output, "IMG_0001.HEIC", "heic", compress.OutputFormatKeep, true); got != "IMG_0001.png" {
		t.Fatalf("lossless HEIC output example = %q, want IMG_0001.png", got)
	}
	if got := presetOutputNameForResolved(app.output, "IMG_0001.HEIC", "heic", compress.OutputFormatWebP, false); got != "IMG_0001.webp" {
		t.Fatalf("webp HEIC output example = %q, want IMG_0001.webp", got)
	}

	// Compress and check overwrite guard + drag filename.
	res, err := compress.Bytes(context.Background(), selected.data, selected.path, app.compressionOptions())
	if err != nil {
		t.Fatalf("compress HEIC failed: %v", err)
	}
	app.result = res
	if app.canOverwrite() {
		t.Fatalf("expected canOverwrite() == false for HEIC result")
	}
	if dragName := app.singleDragFilename(); dragName != "IMG_0001.jpg" {
		t.Fatalf("singleDragFilename() = %q, want IMG_0001.jpg", dragName)
	}
}

func TestHEICBatchOverwriteModeAndRestart(t *testing.T) {
	dir := t.TempDir()
	heicPath := createHEICTestFile(t, dir, "iphone.heic", 120, 90)
	origHEICBytes, err := os.ReadFile(heicPath)
	if err != nil {
		t.Fatal(err)
	}

	scan, err := scanBatchPathsWithDirs(context.Background(), []string{dir}, nil, nil, 0)
	if err != nil {
		t.Fatalf("scanBatchPathsWithDirs failed: %v", err)
	}
	if len(scan.items) != 1 || scan.items[0].format != "heic" || scan.items[0].sourceFormat != "heic" {
		t.Fatalf("unexpected HEIC batch scan: %+v", scan.items)
	}

	app := newApplication()
	app.output = presetSaveOptions{Mode: presetSaveOriginal}
	app.batch = newBatchState(scan.items)
	app.configureBatchPresetOutput()

	if app.batchRunWillOverwriteAny(batchRunPending, -1) {
		t.Fatalf("batchRunWillOverwriteAny should be false when batch only contains HEIC")
	}

	tasks, used := app.prepareBatchTasks(batchRunPending, -1)
	app.runBatchTasksWithBackup(context.Background(), tasks, app.compressionOptions(), app.batch.outputMode, app.batch.outputDir, used, nil, func(fn func()) { fn() })

	item := app.batch.items[0]
	if item.status != batchDone || item.overwritten || item.format != "jpeg" {
		t.Fatalf("unexpected batch item after HEIC run: %+v", item)
	}
	wantOut := filepath.Join(dir, "iphone.jpg")
	if item.savedPath != wantOut {
		t.Fatalf("savedPath = %q, want %q", item.savedPath, wantOut)
	}
	if _, err := os.Stat(wantOut); err != nil {
		t.Fatalf("expected %q to exist: %v", wantOut, err)
	}
	afterHEICBytes, err := os.ReadFile(heicPath)
	if err != nil || !bytes.Equal(origHEICBytes, afterHEICBytes) {
		t.Fatalf("original HEIC file was modified!")
	}

	// Restart in lossless mode: sourceFormat must still be "heic" so it saves .png, not overwrite.
	app.lossless = true
	if app.batchRunWillOverwriteAny(batchRunRestart, -1) {
		t.Fatalf("batchRunWillOverwriteAny on restart should still be false for HEIC")
	}
	tasks, used = app.prepareBatchTasks(batchRunRestart, -1)
	if len(tasks) != 1 || tasks[0].sourceFormat != "heic" || !tasks[0].lossless {
		t.Fatalf("unexpected restart task: %+v", tasks)
	}
	app.runBatchTasksWithBackup(context.Background(), tasks, app.compressionOptions(), app.batch.outputMode, app.batch.outputDir, used, nil, func(fn func()) { fn() })

	item = app.batch.items[0]
	wantPNG := filepath.Join(dir, "iphone.png")
	if item.status != batchDone || item.overwritten || item.format != "png" || item.savedPath != wantPNG {
		t.Fatalf("unexpected batch item after lossless restart: %+v", item)
	}
	if _, err := os.Stat(wantPNG); err != nil {
		t.Fatalf("expected %q to exist: %v", wantPNG, err)
	}
}

func TestHEICCLICompressModes(t *testing.T) {
	dir := t.TempDir()
	heicPath := createHEICTestFile(t, dir, "shot.heic", 120, 80)
	origBytes, err := os.ReadFile(heicPath)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Default copy mode -> shot-zpic.jpg
	var stdout, stderr bytes.Buffer
	code := runCLIContext(context.Background(), []string{"compress", "--json", heicPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("CLI default compress failed (code=%d): %s", code, stderr.String())
	}
	var report cliCompressReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("unmarshal CLI report: %v", err)
	}
	if report.Succeeded != 1 || len(report.Files) != 1 ||
		report.Files[0].SourceFormat != "heic" || report.Files[0].OutputFormat != "jpeg" ||
		filepath.Base(report.Files[0].Output) != "shot-zpic.jpg" {
		t.Fatalf("unexpected default CLI HEIC report: %+v", report)
	}

	// 2. Lossless mode -> shot-zpic.png
	stdout.Reset()
	stderr.Reset()
	code = runCLIContext(context.Background(), []string{"compress", "--lossless", "--json", heicPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("CLI lossless compress failed (code=%d): %s", code, stderr.String())
	}
	report = cliCompressReport{}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("unmarshal lossless CLI report: %v", err)
	}
	if report.Files[0].OutputFormat != "png" || filepath.Base(report.Files[0].Output) != "shot-zpic.png" {
		t.Fatalf("unexpected lossless CLI HEIC report: %+v", report.Files[0])
	}

	// 3. WebP mode -> shot-zpic.webp
	stdout.Reset()
	stderr.Reset()
	code = runCLIContext(context.Background(), []string{"compress", "--format", "webp", "--json", heicPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("CLI webp compress failed (code=%d): %s", code, stderr.String())
	}
	report = cliCompressReport{}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("unmarshal webp CLI report: %v", err)
	}
	if report.Files[0].OutputFormat != "webp" || filepath.Base(report.Files[0].Output) != "shot-zpic.webp" {
		t.Fatalf("unexpected webp CLI HEIC report: %+v", report.Files[0])
	}

	// 4. --in-place -> must NOT overwrite shot.heic; creates shot.jpg in same dir.
	stdout.Reset()
	stderr.Reset()
	code = runCLIContext(context.Background(), []string{"compress", "--in-place", "--json", heicPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("CLI --in-place compress failed (code=%d): %s", code, stderr.String())
	}
	report = cliCompressReport{}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("unmarshal in-place CLI report: %v", err)
	}
	if report.Files[0].Overwritten || filepath.Base(report.Files[0].Output) != "shot.jpg" {
		t.Fatalf("unexpected in-place CLI HEIC report: %+v", report.Files[0])
	}
	afterBytes, err := os.ReadFile(heicPath)
	if err != nil || !bytes.Equal(origBytes, afterBytes) {
		t.Fatalf("original shot.heic modified in --in-place mode")
	}

	// 5. Explicit -o with mismatched .heic extension -> exit 2
	stdout.Reset()
	stderr.Reset()
	code = runCLIContext(context.Background(), []string{"compress", "-o", filepath.Join(dir, "bad.heic"), heicPath}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "不匹配") {
		t.Fatalf("expected extension mismatch exit 2, got code=%d stderr=%q", code, stderr.String())
	}

	// 6. Explicit -o with matching .jpg extension -> succeeds
	stdout.Reset()
	stderr.Reset()
	explicitOut := filepath.Join(dir, "custom.jpg")
	code = runCLIContext(context.Background(), []string{"compress", "-o", explicitOut, heicPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected explicit -o custom.jpg to succeed, got code=%d stderr=%q", code, stderr.String())
	}
	if info, err := compress.Inspect(context.Background(), explicitOut); err != nil || info.Format != "jpeg" {
		t.Fatalf("explicit output invalid: info=%+v err=%v", info, err)
	}
}
