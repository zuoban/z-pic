package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"zpic/internal/compress"
)

func TestFormatConvertPresetSettingsRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		format compress.OutputFormat
		label  string
	}{
		{compress.OutputFormatJPEG, "转为 JPEG"},
		{compress.OutputFormatPNG, "转为 PNG"},
	} {
		t.Run(string(tc.format), func(t *testing.T) {
			app := newApplication()
			tester := openTestSettings(t, app)
			tester.Scroll(320, 300, 0, 400)
			if err := tester.Click(tc.label); err != nil {
				t.Fatal(err)
			}
			if got := app.editor.selectedPreset().outputFormat; got != tc.format || app.outputFormat != compress.OutputFormatKeep {
				t.Fatalf("format before save: draft=%q active=%q, want draft=%q and active=keep", got, app.outputFormat, tc.format)
			}
			if err := tester.Click("保存"); err != nil {
				t.Fatal(err)
			}
			if app.settingsOpen || app.outputFormat != tc.format {
				t.Fatalf("saved format was not applied: open=%v active=%q message=%q", app.settingsOpen, app.outputFormat, app.message)
			}

			var stored struct {
				Version  int    `json:"version"`
				Selected string `json:"selected"`
				Presets  []struct {
					Name   string `json:"name"`
					Format string `json:"output_format"`
				} `json:"presets"`
			}
			if err := json.Unmarshal(readFormatConvertFile(t, app.settingsFile), &stored); err != nil {
				t.Fatal(err)
			}
			if stored.Version != 1 || stored.Selected != app.scheme {
				t.Fatalf("stored version=%d selected=%q, want version=1 selected=%q", stored.Version, stored.Selected, app.scheme)
			}
			var storedFormat string
			for _, preset := range stored.Presets {
				if preset.Name == stored.Selected {
					storedFormat = preset.Format
					break
				}
			}
			if storedFormat != string(tc.format) {
				t.Fatalf("stored output_format for %q = %q, want %q", stored.Selected, storedFormat, tc.format)
			}

			t.Setenv("ZPIC_CONFIG_FILE", app.settingsFile)
			restarted := newApplication()
			restarted.restoreSettings()
			if restarted.settingsError != "" || restarted.scheme != app.scheme || restarted.outputFormat != tc.format {
				t.Fatalf("restored preset=%q format=%q error=%q", restarted.scheme, restarted.outputFormat, restarted.settingsError)
			}
			tester = openTestSettings(t, restarted)
			tester.Scroll(320, 300, 0, 400)
			if choice, ok := tester.AccessByRole(ui.RoleRadio, tc.label); !ok || !choice.Checked {
				t.Fatalf("restored output format %q is not selected: %+v", tc.label, choice)
			}
		})
	}
}

func TestFormatConvertSingleSave(t *testing.T) {
	for _, tc := range []struct {
		name         string
		sourceFormat string
		sourceName   string
		target       compress.OutputFormat
		outputName   string
		overwrite    bool
	}{
		{"jpeg_to_png", "jpeg", "photo.v2.JPEG", compress.OutputFormatPNG, "photo.v2.png", false},
		{"png_to_jpeg", "png", "chart.PNG", compress.OutputFormatJPEG, "chart.jpg", false},
		{"jpeg_to_jpeg", "jpeg", "photo.v2.JPEG", compress.OutputFormatJPEG, "photo.v2.JPEG", true},
		{"png_to_png", "png", "chart.PNG", compress.OutputFormatPNG, "chart.PNG", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output := presetSaveOptions{Mode: presetSaveOriginal}
			if got := output.replacesFile(tc.sourceFormat, tc.target); got != tc.overwrite {
				t.Errorf("replacesFile(%q, %q) = %v, want %v", tc.sourceFormat, tc.target, got, tc.overwrite)
			}
			if got := replaceFilenameExtensionFor(tc.sourceName, tc.sourceFormat, tc.target, false); got != tc.outputName {
				t.Errorf("replaceFilenameExtensionFor(%q, %q, %q) = %q, want %q", tc.sourceName, tc.sourceFormat, tc.target, got, tc.outputName)
			}

			t.Run("save", func(t *testing.T) {
				requireFormatConvertEncoder(t, tc.target)
				dir := t.TempDir()
				source := filepath.Join(dir, tc.sourceName)
				before := writeFormatConvertFixture(t, source, tc.sourceFormat)
				wantPath := filepath.Join(dir, tc.outputName)

				app := newApplication()
				app.presets = testLegacyPresets()
				preset := &app.presets[0]
				preset.output, preset.outputFormat = output, tc.target
				if tc.overwrite {
					// A substantially smaller image makes the overwrite path deterministic.
					preset.maxDimension = 160
				}
				app.setScheme(preset.name)
				updates := make(chan func(), 16)
				app.dispatchUpdate = func(update func()) { updates <- update }
				t.Cleanup(app.saves.Wait)

				ctx := context.Background()
				selected, err := readSelection(ctx, source)
				if err != nil {
					t.Fatal(err)
				}
				app.completeSelection(ctx, selected, nil)
				if got := app.singleReplacesOriginal(); got != tc.overwrite {
					t.Errorf("singleReplacesOriginal before compression = %v, want %v", got, tc.overwrite)
				}
				app.startCompression()
				for app.busy {
					awaitBatchUpdate(t, updates)()
				}
				if app.result == nil || app.result.Format != string(tc.target) {
					t.Fatalf("compression did not produce %s: result=%v message=%q", tc.target, app.result != nil, app.message)
				}
				if got := app.singleReplacesOriginal(); got != tc.overwrite {
					t.Errorf("singleReplacesOriginal with compression result = %v, want %v", got, tc.overwrite)
				}
				app.savePresetResult()
				if app.confirmOverwrite != tc.overwrite {
					t.Fatalf("savePresetResult overwrite confirmation = %v, want %v (message=%q)", app.confirmOverwrite, tc.overwrite, app.message)
				}
				if tc.overwrite {
					if app.busy || !bytes.Equal(readFormatConvertFile(t, source), before) {
						t.Fatal("original changed before overwrite confirmation")
					}
					tester := ui.NewTester(app.view, 880, 660)
					if err := tester.Click("覆盖原图"); err != nil {
						t.Fatal(err)
					}
				}
				for app.busy {
					awaitBatchUpdate(t, updates)()
				}
				if app.failed || app.lastSavedPath != wantPath || app.overwritten != tc.overwrite {
					t.Fatalf("save state: path=%q want=%q overwritten=%v want=%v message=%q", app.lastSavedPath, wantPath, app.overwritten, tc.overwrite, app.message)
				}
				if saved := decodeFormatConvertFile(t, wantPath, string(tc.target)); !bytes.Equal(saved, app.result.Data) {
					t.Fatal("saved bytes differ from the compression result")
				}
				if after := readFormatConvertFile(t, source); tc.overwrite && len(after) >= len(before) {
					t.Fatalf("overwritten source did not shrink: before=%d after=%d", len(before), len(after))
				} else if !tc.overwrite && !bytes.Equal(after, before) {
					t.Fatal("cross-format save changed the original")
				}
			})
		})
	}
}

func TestFormatConvertBatchOverwriteMixedFolder(t *testing.T) {
	for _, tc := range []struct {
		target      compress.OutputFormat
		sameName    string
		siblingName string
	}{
		{compress.OutputFormatJPEG, "photo.jpg", "photo-compressed.jpg"},
		{compress.OutputFormatPNG, "photo.png", "photo-compressed.png"},
	} {
		t.Run(string(tc.target), func(t *testing.T) {
			requireFormatConvertEncoder(t, tc.target)
			dir := t.TempDir()
			jpegPath, pngPath := filepath.Join(dir, "photo.jpg"), filepath.Join(dir, "photo.png")
			originals := map[string][]byte{
				jpegPath: writeTestJPEG(t, jpegPath, 320, 240, 100),
				pngPath:  writeTestPNG(t, pngPath, 320, 240, true),
			}
			scan, err := scanBatchPaths(context.Background(), []string{dir}, nil, 0)
			if err != nil || len(scan.items) != len(originals) {
				t.Fatalf("scan mixed folder: items=%+v err=%v", scan.items, err)
			}
			app := newApplication()
			app.presets = testLegacyPresets()
			preset := &app.presets[0]
			preset.output = presetSaveOptions{Mode: presetSaveOriginal}
			preset.outputFormat, preset.maxDimension = tc.target, 160
			app.setScheme(preset.name)
			app.batch = newBatchState(scan.items)
			app.configureBatchPresetOutput()
			if app.batch.outputMode != batchOutputOverwrite {
				t.Fatal("original-folder preset did not enable batch overwrite mode")
			}
			updates := make(chan func(), 16)
			app.dispatchUpdate = func(update func()) { updates <- update }
			t.Cleanup(app.saves.Wait)
			tester := ui.NewTester(app.view, 980, 720)
			if err := tester.Click("开始压缩"); err != nil {
				t.Fatal(err)
			}
			if _, ok := tester.AccessByRole(ui.RoleAlertDialog, "确认批量覆盖原图？"); !ok || app.busy {
				t.Fatal("mixed batch must request confirmation before replacing the same-format source")
			}
			for path, before := range originals {
				if !bytes.Equal(readFormatConvertFile(t, path), before) {
					t.Fatalf("%q changed before confirmation", path)
				}
			}
			if err := tester.Click("覆盖原图"); err != nil {
				t.Fatal(err)
			}
			for app.busy {
				awaitBatchUpdate(t, updates)()
			}
			if app.batch.done != len(originals) || app.failed {
				t.Errorf("batch handled %d/%d: message=%q", app.batch.done, len(originals), app.message)
			}
			for _, item := range app.batch.items {
				before, ok := originals[item.path]
				if !ok {
					t.Fatalf("unexpected batch source %q", item.path)
				}
				overwrite := filepath.Base(item.path) == tc.sameName
				wantPath := filepath.Join(dir, tc.siblingName)
				if overwrite {
					wantPath = item.path
				}
				if item.status != batchDone || item.format != string(tc.target) || item.overwritten != overwrite || batchItemOutputPath(item) != wantPath {
					t.Errorf("batch item %+v, want format=%q overwritten=%v output=%q", item, tc.target, overwrite, wantPath)
					continue
				}
				saved := decodeFormatConvertFile(t, wantPath, string(tc.target))
				if int64(len(saved)) != item.outputSize {
					t.Errorf("output size differs from disk: item=%d disk=%d", item.outputSize, len(saved))
				}
				if overwrite && len(saved) >= len(before) {
					t.Errorf("overwritten source %q did not shrink", item.path)
				}
				if !overwrite && !bytes.Equal(readFormatConvertFile(t, item.path), before) {
					t.Errorf("cross-format batch changed %q", item.path)
				}
			}
		})
	}
}

func TestFormatConvertCLIFormats(t *testing.T) {
	for _, tc := range []struct {
		flag, source, sourceName string
		target                   compress.OutputFormat
		outputName               string
		explicit                 bool
	}{
		{"jpeg", "png", "photo.v2.png", compress.OutputFormatJPEG, "photo.v2-zpic.jpg", false},
		{"jpg", "png", "photo.v2.png", compress.OutputFormatJPEG, "export.JPEG", true},
		{"png", "jpeg", "photo.v2.jpg", compress.OutputFormatPNG, "photo.v2-zpic.png", false},
	} {
		t.Run(tc.flag, func(t *testing.T) {
			requireFormatConvertEncoder(t, tc.target)
			dir := t.TempDir()
			source, output := filepath.Join(dir, tc.sourceName), filepath.Join(dir, tc.outputName)
			before := writeFormatConvertFixture(t, source, tc.source)
			args := []string{"--format", tc.flag}
			if tc.explicit {
				args = append(args, "-o", output)
			}
			report := runFormatConvertCompress(t, 1, append(args, source)...)
			entry := report.Files[0]
			if entry.Input != source || entry.SourceFormat != tc.source || entry.OutputFormat != string(tc.target) ||
				entry.Output != output || entry.Overwritten || entry.Unchanged {
				t.Fatalf("unexpected conversion report: %+v, want %q at %q", entry, tc.target, output)
			}
			decodeFormatConvertFile(t, output, string(tc.target))
			if !bytes.Equal(readFormatConvertFile(t, source), before) {
				t.Fatal("CLI format conversion changed the original")
			}
		})
	}
}

func TestFormatConvertCLIPresetText(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	presets := testLegacyPresets()
	presets[0].outputFormat, presets[1].outputFormat = compress.OutputFormatJPEG, compress.OutputFormatPNG
	if err := saveSettings(settingsPath, presets, "均衡"); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := runCLIContext(context.Background(), []string{"presets", "--settings", settingsPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("presets exited %d: %s", code, stderr.String())
	}
	lines := strings.Split(stdout.String(), "\n")
	for _, tc := range []struct{ prefix, label string }{
		{"  体积优先  (", "转为 JPEG"},
		{"* 均衡  (", "转为 PNG"},
	} {
		if !slices.ContainsFunc(lines, func(line string) bool {
			return strings.HasPrefix(line, tc.prefix) && strings.Contains(line, tc.label)
		}) {
			t.Errorf("presets text has no line for %q with format %q:\n%s", tc.prefix, tc.label, stdout.String())
		}
	}
}

func TestFormatConvertCLIMismatchedExtension(t *testing.T) {
	for _, tc := range []struct{ target, source, sourceName, outputName string }{
		{"jpeg", "png", "photo.png", "export.png"},
		{"png", "jpeg", "photo.jpg", "export.jpeg"},
	} {
		t.Run(tc.target, func(t *testing.T) {
			dir := t.TempDir()
			source, output := filepath.Join(dir, tc.sourceName), filepath.Join(dir, tc.outputName)
			before := writeFormatConvertFixture(t, source, tc.source)
			var stdout, stderr bytes.Buffer
			args := []string{"compress", "--format", tc.target, "-o", output, source}
			if code := runCLIContext(context.Background(), args, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "不匹配") {
				t.Errorf("extension mismatch: exit=%d stdout=%q stderr=%q, want exit 2 and a mismatch diagnostic", code, stdout.String(), stderr.String())
			}
			if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("mismatched output path %q should be absent: %v", output, err)
			}
			if !bytes.Equal(readFormatConvertFile(t, source), before) {
				t.Fatal("rejected CLI command changed the original")
			}
		})
	}
}

func TestFormatConvertCLIInPlaceMixedFolder(t *testing.T) {
	dir := t.TempDir()
	jpegPath := filepath.Join(dir, "photo.JPEG")
	pngPath := filepath.Join(dir, "nested", "chart.png")
	copyPath := filepath.Join(dir, "nested", "chart.jpg")
	jpegBefore := writeTestJPEG(t, jpegPath, 320, 240, 100)
	pngBefore := writeTestPNG(t, pngPath, 320, 240, true)

	report := runFormatConvertCompress(t, 2, "--in-place", "--format", "jpeg", "-q", "35", dir)
	if !report.InPlace {
		t.Fatal("CLI report did not mark the run as in-place")
	}
	byInput := make(map[string]cliFileReport, len(report.Files))
	for _, entry := range report.Files {
		byInput[entry.Input] = entry
	}
	for _, tc := range []struct {
		input, output, sourceFormat string
		overwrite                   bool
	}{
		{jpegPath, jpegPath, "jpeg", true},
		{pngPath, copyPath, "png", false},
	} {
		entry := byInput[tc.input]
		if entry.SourceFormat != tc.sourceFormat || entry.OutputFormat != "jpeg" || entry.Output != tc.output ||
			entry.Overwritten != tc.overwrite || entry.Unchanged {
			t.Errorf("in-place entry for %q = %+v, want output=%q overwritten=%v", tc.input, entry, tc.output, tc.overwrite)
		}
	}
	if jpegAfter := decodeFormatConvertFile(t, jpegPath, "jpeg"); len(jpegAfter) >= len(jpegBefore) {
		t.Errorf("in-place JPEG did not shrink: before=%d after=%d", len(jpegBefore), len(jpegAfter))
	}
	if !bytes.Equal(readFormatConvertFile(t, pngPath), pngBefore) {
		t.Error("in-place conversion changed the PNG original")
	}
	decodeFormatConvertFile(t, copyPath, "jpeg")
}

func TestFormatConvertCleanupDownloadedHEIF(t *testing.T) {
	dir, trashDir := t.TempDir(), t.TempDir()
	idleNames := []string{"idle.heic", "idle.heif"}
	activeNames := []string{"queued.HEIC", "queued.HEIF"}
	pathsByURL := make(map[string]string)
	for _, name := range []string{"idle.heic", "idle.heif", "queued.HEIC", "queued.HEIF", "notes.txt"} {
		path := filepath.Join(dir, name)
		// Cleanup uses file extensions and does not decode image contents.
		if err := os.WriteFile(path, []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
		pathsByURL["https://example.com/"+name] = path
	}
	app := newApplication()
	app.downloadDir = dir
	app.dispatchUpdate = func(update func()) { update() }
	app.trashItem = func(path string) error {
		return os.Rename(path, filepath.Join(trashDir, filepath.Base(path)))
	}
	var queued []batchItem
	for _, name := range activeNames {
		path := filepath.Join(dir, name)
		queued = append(queued, batchItem{path: path, absPath: path, relPath: name})
	}
	app.batch = newBatchState(queued)
	app.recordDownloadedURLs(pathsByURL)
	app.setSettingsOpen(true)
	app.cleanupDownloadedImages()
	app.saves.Wait()
	if app.busy || app.editor.message != "" || !strings.Contains(app.editor.notice, "2 张下载图片移入废纸篓") ||
		!strings.Contains(app.editor.notice, "保留 2 张") {
		t.Errorf("cleanup status: busy=%v message=%q notice=%q", app.busy, app.editor.message, app.editor.notice)
	}
	for _, name := range idleNames {
		if _, err := os.Lstat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("idle download %s should be absent: %v", name, err)
		}
		if got := string(readFormatConvertFile(t, filepath.Join(trashDir, name))); got != name {
			t.Errorf("trashed %s contents = %q", name, got)
		}
		if _, ok := app.downloadedURLs["https://example.com/"+name]; ok {
			t.Errorf("URL cache still contains trashed %s", name)
		}
	}
	for _, name := range append(activeNames, "notes.txt") {
		path := filepath.Join(dir, name)
		if got := string(readFormatConvertFile(t, path)); got != name {
			t.Errorf("retained %s contents = %q", name, got)
		}
		if name != "notes.txt" && app.downloadedURLs["https://example.com/"+name] != path {
			t.Errorf("URL cache lost queued %s", name)
		}
	}
}

func requireFormatConvertEncoder(t *testing.T, format compress.OutputFormat) {
	t.Helper()
	if format == compress.OutputFormatPNG && (runtime.GOOS != "darwin" || runtime.GOARCH != "arm64") {
		t.Skip("bundled PNG encoders require darwin/arm64")
	}
}

func writeFormatConvertFixture(t *testing.T, path, format string) []byte {
	t.Helper()
	switch format {
	case "jpeg":
		return writeTestJPEG(t, path, 320, 240, 100)
	case "png":
		return writeTestPNG(t, path, 320, 240, true)
	default:
		t.Fatalf("no test fixture for %q", format)
		return nil
	}
}

func readFormatConvertFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func decodeFormatConvertFile(t *testing.T, path, format string) []byte {
	t.Helper()
	data := readFormatConvertFile(t, path)
	if _, got, err := image.Decode(bytes.NewReader(data)); err != nil || got != format {
		t.Fatalf("decode %q: format=%q want=%q err=%v", path, got, format, err)
	}
	return data
}

func runFormatConvertCompress(t *testing.T, files int, args ...string) cliCompressReport {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var stdout, stderr bytes.Buffer
	if code := runCLIContext(ctx, append([]string{"compress", "--json"}, args...), &stdout, &stderr); code != 0 {
		t.Fatalf("compress exited %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var report cliCompressReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("invalid compress JSON: %v\n%s", err, stdout.String())
	}
	if report.Total != files || report.Succeeded != files || report.Failed != 0 || len(report.Files) != files {
		t.Fatalf("unexpected report for %d files: %+v", files, report)
	}
	return report
}
