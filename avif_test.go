package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"zpic/internal/aviftools"
	"zpic/internal/compress"
	"zpic/internal/heictools"
)

const (
	avifTestWidth  = 192
	avifTestHeight = 144
)

func requireDesktopAVIF(t *testing.T) {
	t.Helper()
	if !compress.SupportsAVIF() {
		t.Skip("bundled AVIF tools require macOS on Apple silicon")
	}
}

func writeTestAVIF(t *testing.T, path string) []byte {
	t.Helper()
	data := writeTestPNG(t, filepath.Join(t.TempDir(), "source.png"), avifTestWidth, avifTestHeight, true)
	xmp := []byte("XML:com.adobe.xmp\x00\x00\x00\x00\x00<x:xmpmeta xmlns:x='adobe:ns:meta/'/>")
	chunk := binary.BigEndian.AppendUint32(nil, uint32(len(xmp)))
	chunk = append(chunk, "iTXt"...)
	chunk = append(chunk, xmp...)
	chunk = binary.BigEndian.AppendUint32(chunk, crc32.ChecksumIEEE(chunk[4:]))
	const afterIHDR = 33
	data = bytes.Join([][]byte{data[:afterIHDR], chunk, data[afterIHDR:]}, nil)
	encoded, err := aviftools.EncodePNG(t.Context(), data, aviftools.EncodeOptions{Lossless: true, Threads: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	return encoded
}

func newAVIFTestApplication(t *testing.T) (*application, <-chan func()) {
	t.Helper()
	app := newApplication()
	updates := make(chan func(), 16)
	app.dispatchUpdate = func(update func()) { updates <- update }
	t.Cleanup(app.saves.Wait)
	return app, updates
}

func TestAVIFSingleSelectionPreviewAndSave(t *testing.T) {
	requireDesktopAVIF(t)
	for _, tc := range []struct {
		sourceName, sourceFormat, outputName string
		target                               compress.OutputFormat
		overwrite                            bool
	}{
		{"photo.AVIF", "avif", "photo.AVIF", compress.OutputFormatKeep, true},
		{"photo.PNG", "png", "photo.avif", compress.OutputFormatAVIF, false},
		{"photo.JPEG", "jpeg", "photo.avif", compress.OutputFormatAVIF, false},
	} {
		t.Run(tc.sourceFormat, func(t *testing.T) {
			dir := t.TempDir()
			source := filepath.Join(dir, tc.sourceName)
			var before []byte
			switch tc.sourceFormat {
			case "avif":
				before = writeTestAVIF(t, source)
			case "png":
				before = writeTestPNG(t, source, avifTestWidth, avifTestHeight, true)
			case "jpeg":
				before = writeTestJPEG(t, source, avifTestWidth, avifTestHeight, 100)
			}

			app, updates := newAVIFTestApplication(t)
			app.output = presetSaveOptions{Mode: presetSaveOriginal}
			app.outputFormat, app.maxDimension = tc.target, 96
			app.load(source)
			awaitBatchUpdate(t, updates)()
			selected := app.file
			if selected == nil || selected.format != tc.sourceFormat || selected.preview == nil ||
				selected.width != avifTestWidth || selected.height != avifTestHeight || selected.size != int64(len(before)) {
				t.Fatalf("single selection has incorrect preview or metadata: %s", app.message)
			}
			if app.singleReplacesOriginal() != tc.overwrite {
				t.Fatalf("incorrect save policy for %s -> AVIF", tc.sourceFormat)
			}

			app.startCompression()
			awaitBatchUpdate(t, updates)()
			if app.result == nil || app.result.Format != "avif" || app.result.Unchanged || app.preview == nil {
				t.Fatalf("expected an AVIF result and preview: %s", app.message)
			}
			if w, h := app.preview.Size(); w != 96 || h != 72 {
				t.Fatalf("AVIF preview size = %dx%d, want 96x72", w, h)
			}
			info, err := aviftools.InspectBytes(t.Context(), app.result.Data)
			if err != nil || info.HasXMP != tc.overwrite {
				t.Fatalf("AVIF metadata after compression = %+v, %v", info, err)
			}
			if got := app.singleDragFilename(); got != tc.outputName {
				t.Errorf("drag filename = %q, want %q", got, tc.outputName)
			}
			if got := app.canOverwrite(); got != tc.overwrite {
				t.Fatalf("canOverwrite() = %v, want %v", got, tc.overwrite)
			}

			if tc.sourceFormat == "avif" {
				var clipboard []byte
				app.writeClipboardImage = func(data []byte) error { clipboard = data; return nil }
				app.copyResult()
				awaitBatchUpdate(t, updates)()
				cfg, err := png.DecodeConfig(bytes.NewReader(clipboard))
				if err != nil || cfg.Width != 96 || cfg.Height != 72 {
					t.Fatalf("copied AVIF result did not produce a 96x72 PNG: %v (%s)", err, app.message)
				}
			}

			app.savePresetResult()
			if app.confirmOverwrite != tc.overwrite {
				t.Fatalf("overwrite confirmation = %v, want %v", app.confirmOverwrite, tc.overwrite)
			}
			if tc.overwrite {
				if !bytes.Equal(readFormatConvertFile(t, source), before) {
					t.Fatal("source changed before overwrite confirmation")
				}
				tester := ui.NewTester(app.view, 880, 660)
				if err := tester.Click("覆盖原图"); err != nil {
					t.Fatal(err)
				}
			}
			awaitBatchUpdate(t, updates)()
			output := filepath.Join(dir, tc.outputName)
			if app.failed || app.lastSavedPath != output || app.overwritten != tc.overwrite {
				t.Fatalf("save state: path=%q overwritten=%v message=%q", app.lastSavedPath, app.overwritten, app.message)
			}
			saved := readFormatConvertFile(t, output)
			if !bytes.Equal(saved, app.result.Data) {
				t.Fatal("saved bytes differ from the AVIF compression result")
			}
			if tc.overwrite && len(saved) >= len(before) {
				t.Fatal("AVIF overwrite must reduce file size")
			}
			if !tc.overwrite && !bytes.Equal(readFormatConvertFile(t, source), before) {
				t.Fatal("AVIF conversion modified the source")
			}
		})
	}
}

func TestAVIFBatchScanAndSave(t *testing.T) {
	requireDesktopAVIF(t)
	for _, tc := range []struct {
		target compress.OutputFormat
		ext    string
	}{
		{compress.OutputFormatAVIF, ".avif"},
		{compress.OutputFormatJPEG, ".jpg"},
	} {
		t.Run(string(tc.target), func(t *testing.T) {
			dir := t.TempDir()
			avifPath, pngPath := filepath.Join(dir, "photo.AVIF"), filepath.Join(dir, "chart.PNG")
			originals := map[string][]byte{
				avifPath: writeTestAVIF(t, avifPath),
				pngPath:  writeTestPNG(t, pngPath, avifTestWidth, avifTestHeight, true),
			}
			scan, err := scanBatchPaths(t.Context(), []string{dir}, nil, 0)
			if err != nil || len(scan.items) != len(originals) {
				t.Fatalf("scan found %d items, want %d: %v", len(scan.items), len(originals), err)
			}
			for _, item := range scan.items {
				format := "png"
				if item.path == avifPath {
					format = "avif"
				}
				if item.status != batchPending || item.sourceFormat != format || item.format != format ||
					item.width != avifTestWidth || item.height != avifTestHeight || item.originalSize != int64(len(originals[item.path])) {
					t.Fatalf("incorrect batch scan metadata: %+v", item)
				}
			}

			app, updates := newAVIFTestApplication(t)
			app.output = presetSaveOptions{Mode: presetSaveOriginal}
			app.outputFormat, app.maxDimension = tc.target, 96
			app.batch = newBatchState(scan.items)
			app.configureBatchPresetOutput()
			tester := ui.NewTester(app.view, 980, 720)
			if err := tester.Click("开始压缩"); err != nil {
				t.Fatal(err)
			}
			if app.confirmBatchOverwrite != (tc.target == compress.OutputFormatAVIF) {
				t.Fatalf("incorrect batch overwrite confirmation for %s", tc.target)
			}
			if app.confirmBatchOverwrite {
				if err := tester.Click("覆盖原图"); err != nil {
					t.Fatal(err)
				}
			}
			for app.busy {
				awaitBatchUpdate(t, updates)()
			}
			if app.failed || app.batch.done != len(originals) {
				t.Fatalf("batch completed %d/%d items: %s", app.batch.done, len(originals), app.message)
			}
			for _, item := range app.batch.items {
				overwrite := item.path == avifPath && tc.target == compress.OutputFormatAVIF
				wantPath := strings.TrimSuffix(item.path, filepath.Ext(item.path)) + tc.ext
				if overwrite {
					wantPath = item.path
				}
				if item.status != batchDone || item.format != string(tc.target) || item.overwritten != overwrite || batchItemOutputPath(item) != wantPath {
					t.Fatalf("unexpected batch item: %+v, want output=%q overwritten=%v", item, wantPath, overwrite)
				}
				saved := readFormatConvertFile(t, wantPath)
				if format, err := compress.VerifyImageData(t.Context(), saved); err != nil || format != string(tc.target) || int64(len(saved)) != item.outputSize {
					t.Fatalf("invalid saved output %q: format=%q size=%d err=%v", wantPath, format, len(saved), err)
				}
				if overwrite && len(saved) >= len(originals[item.path]) {
					t.Error("overwritten AVIF did not shrink")
				}
				if !overwrite && !bytes.Equal(readFormatConvertFile(t, item.path), originals[item.path]) {
					t.Errorf("conversion modified %q", item.path)
				}
			}
		})
	}
}

func TestAVIFDesktopImport(t *testing.T) {
	requireDesktopAVIF(t)
	root := t.TempDir()
	source := filepath.Join(root, "source.avif")
	original := writeTestAVIF(t, source)
	data := transfer.New(transfer.NewItem(transfer.Bytes("image/avif", original)))
	for _, paste := range []bool{false, true} {
		name := "drop"
		if paste {
			name = "paste"
		}
		t.Run(name, func(t *testing.T) {
			app, updates := newAVIFTestApplication(t)
			app.downloadDir = filepath.Join(root, name)
			if paste {
				app.readClipboardImages = func() (transfer.Data, error) { return data, nil }
				app.pasteImages()
			} else {
				app.receiveDrop(data)
			}
			waitForDropImports(t, app, updates)
			if app.failed || app.batch == nil || len(app.batch.items) != 1 {
				t.Fatalf("AVIF import failed: %s", app.message)
			}
			item := app.batch.items[0]
			if item.sourceFormat != "avif" || filepath.Ext(item.path) != ".avif" || filepath.Dir(item.path) != app.downloadDir {
				t.Fatalf("unexpected AVIF import: %+v", item)
			}
			if !bytes.Equal(readFormatConvertFile(t, item.path), original) {
				t.Fatal("imported AVIF differs from the original")
			}
		})
	}
}

func TestAVIFPresetRoundTrip(t *testing.T) {
	const label = "转为 AVIF"
	app := newApplication()
	tester := openTestSettings(t, app)
	tester.Scroll(320, 300, 0, 400)
	if compress.SupportsAVIF() {
		save, ok := tester.AccessByRole(ui.RoleButton, "保存")
		if !ok || save.Offscreen {
			t.Fatal("save button is hidden at the minimum window size")
		}
		var right float32
		for _, option := range []string{"保持原格式", "转为 JPEG", "转为 PNG", "转为 WebP", label} {
			choice, ok := tester.AccessByRole(ui.RoleRadio, option)
			bounds := choice.Bounds
			if !ok || choice.Offscreen || choice.Disabled || bounds.X < right || bounds.W <= 0 ||
				bounds.X+bounds.W > 640 || bounds.Y < 0 || bounds.Y+bounds.H > save.Bounds.Y {
				t.Fatalf("format %q is clipped or overlaps another control: %+v", option, choice)
			}
			right = bounds.X + bounds.W
		}
		if err := tester.Click(label); err != nil {
			t.Fatal(err)
		}
		if app.editor.selectedPreset().outputFormat != compress.OutputFormatAVIF || app.outputFormat != compress.OutputFormatKeep {
			t.Fatal("selecting AVIF should only change the draft before saving")
		}
		if err := tester.Click("保存"); err != nil {
			t.Fatal(err)
		}
		if app.settingsOpen || app.outputFormat != compress.OutputFormatAVIF {
			t.Fatalf("AVIF preset was not applied: %s", app.message)
		}
	} else {
		if _, ok := tester.AccessByRole(ui.RoleRadio, label); ok {
			t.Fatal("AVIF output should be hidden when no AVIF tools are bundled")
		}
		app.presets[0].outputFormat = compress.OutputFormatAVIF
		if err := saveSettings(app.settingsFile, app.presets, app.scheme); err != nil {
			t.Fatal(err)
		}
	}

	t.Setenv("ZPIC_CONFIG_FILE", app.settingsFile)
	restarted := newApplication()
	restarted.restoreSettings()
	if restarted.settingsError != "" || restarted.outputFormat != compress.OutputFormatAVIF ||
		!strings.Contains(presetProcessingSummary(restarted.presets[0]), label) {
		t.Fatalf("reloaded AVIF preset has format %q or error %q", restarted.outputFormat, restarted.settingsError)
	}
	tester = openTestSettings(t, restarted)
	tester.Scroll(320, 300, 0, 400)
	if choice, ok := tester.AccessByRole(ui.RoleRadio, label); !ok || !choice.Checked {
		t.Fatalf("reloaded AVIF format is not selected: %+v", choice)
	}
}

func TestAVIFCLICompressModes(t *testing.T) {
	requireDesktopAVIF(t)
	original := writeTestAVIF(t, filepath.Join(t.TempDir(), "original.avif"))
	for _, tc := range []struct {
		name, sourceName, sourceFormat, outputName, outputFormat string
		flags                                                    []string
		overwrite                                                bool
	}{
		{"keep", "photo.AVIF", "avif", "photo-zpic.AVIF", "avif", []string{"--max-dim", "96"}, false},
		{"in_place", "photo.avif", "avif", "photo.avif", "avif", []string{"--in-place", "--max-dim", "96"}, true},
		{"png_to_avif_lossless", "chart.png", "png", "chart-zpic.avif", "avif", []string{"--format", "avif", "--lossless"}, false},
		{"jpeg_to_avif_in_place", "photo.jpg", "jpeg", "photo.avif", "avif", []string{"--format", "avif", "--in-place"}, false},
		{"to_jpeg", "photo.avif", "avif", "photo-zpic.jpg", "jpeg", []string{"--format", "jpeg"}, false},
		{"to_png", "photo.avif", "avif", "photo-zpic.png", "png", []string{"--format", "png"}, false},
		{"to_webp", "photo.avif", "avif", "photo-zpic.webp", "webp", []string{"--format", "webp"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			source, output := filepath.Join(dir, tc.sourceName), filepath.Join(dir, tc.outputName)
			before := original
			if tc.sourceFormat == "avif" {
				if err := os.WriteFile(source, before, 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				before = writeFormatConvertFixture(t, source, tc.sourceFormat)
			}
			report := runFormatConvertCompress(t, 1, append(tc.flags, source)...)
			entry := report.Files[0]
			if entry.Input != source || entry.SourceFormat != tc.sourceFormat || entry.OutputFormat != tc.outputFormat ||
				entry.Output != output || entry.Overwritten != tc.overwrite || entry.Unchanged || report.InPlace != slices.Contains(tc.flags, "--in-place") {
				t.Fatalf("unexpected CLI entry: %+v", entry)
			}
			saved := readFormatConvertFile(t, output)
			if format, err := compress.VerifyImageData(t.Context(), saved); err != nil || format != tc.outputFormat || int64(len(saved)) != entry.OutputBytes {
				t.Fatalf("invalid output %q: format=%q size=%d err=%v", output, format, len(saved), err)
			}
			if tc.overwrite && len(saved) >= len(before) {
				t.Fatal("in-place AVIF was not reduced")
			}
			if !tc.overwrite && !bytes.Equal(readFormatConvertFile(t, source), before) {
				t.Fatal("CLI modified the original during copy mode")
			}
		})
	}

	t.Run("dry_run_target_size", func(t *testing.T) {
		dir := t.TempDir()
		source, output := filepath.Join(dir, "photo.avif"), filepath.Join(dir, "output.AVIF")
		if err := os.WriteFile(source, original, 0o600); err != nil {
			t.Fatal(err)
		}
		report := runFormatConvertCompress(t, 1, "--format", "avif", "--max-dim", "96", "--max-size", "4KiB", "--dry-run", "-o", output, source)
		entry := report.Files[0]
		if !report.DryRun || report.TargetSizeBytes != 4*1024 || report.TargetSizeMissed != 0 || entry.TargetSizeMissed ||
			entry.OutputFormat != "avif" || entry.Status != "ok" || entry.Output != "" || entry.OutputBytes <= 0 || entry.OutputBytes > report.TargetSizeBytes {
			t.Fatalf("unexpected dry-run target-size report: %+v", report)
		}
		if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("dry run created an output at %q: %v", output, err)
		}
		if !bytes.Equal(readFormatConvertFile(t, source), original) {
			t.Fatal("dry run modified the source")
		}
	})
}

func TestAVIFPlatformCapabilities(t *testing.T) {
	filter := supportedFileFilter()
	for _, tc := range []struct {
		name, ext string
		enabled   bool
	}{
		{"AVIF", "avif", compress.SupportsAVIF()},
		{"HEIC", "heic", compress.SupportsHEIC()},
		{"HEIC", "heif", compress.SupportsHEIC()},
	} {
		if strings.Contains(filter.Name, tc.name) != tc.enabled || slices.Contains(filter.Extensions, tc.ext) != tc.enabled ||
			isSupportedImageExt("photo."+strings.ToUpper(tc.ext)) != tc.enabled || strings.Contains(noSupportedImagesMessage(), tc.name) != tc.enabled {
			t.Errorf("desktop capabilities disagree for %s: available=%v filter=%+v", tc.ext, tc.enabled, filter)
		}
	}
	if format, err := parseCLIOutputFormat(" AVIF "); err != nil || format != compress.OutputFormatAVIF || !expectedExtMatchesFormat(".AVIF", "avif") {
		t.Fatalf("CLI did not recognize AVIF as an output format: %q, %v", format, err)
	}
}

func TestAVIFCLIUnavailableEncoder(t *testing.T) {
	if compress.SupportsAVIF() {
		t.Skip("requires a platform without bundled AVIF tools")
	}
	dir := t.TempDir()
	source, output := filepath.Join(dir, "chart.png"), filepath.Join(dir, "converted.avif")
	writeTestPNG(t, source, 32, 24, true)
	var stdout, stderr bytes.Buffer
	if code := runCLIContext(t.Context(), []string{"compress", "--format", "avif", "-o", output, "--json", source}, &stdout, &stderr); code != 1 {
		t.Fatalf("unavailable AVIF encoder: exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var report cliCompressReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil || report.Failed != 1 || len(report.Files) != 1 ||
		!strings.Contains(report.Files[0].Error, aviftools.ErrUnsupportedPlatform.Error()) {
		t.Fatalf("missing AVIF platform error: %s (%v)", stdout.String(), err)
	}
	if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unavailable encoder created an output at %q: %v", output, err)
	}
}

func TestAVIFSelectionContainerErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
		want error
		skip bool
	}{
		{"animation.avif", avifTestBox("ftyp", []byte("avis\x00\x00\x00\x00avif")), aviftools.ErrAnimatedAVIF, false},
		{"movie.mp4", avifTestBox("ftyp", []byte("mp42\x00\x00\x00\x00isom")), heictools.ErrNotHEIC, false},
		{"sequence.heic", avifTestBox("ftyp", []byte("msf1\x00\x00\x00\x00hevc")), heictools.ErrAnimatedHEIC, false},
		{"photo.avif", staticImageTestHeader("avif", "av01"), aviftools.ErrUnsupportedPlatform, compress.SupportsAVIF()},
		{"photo.heic", staticImageTestHeader("heic", "hvc1"), heictools.ErrUnsupportedPlatform, compress.SupportsHEIC()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.skip {
				t.Skip("requires a platform without the corresponding decoder")
			}
			path := filepath.Join(t.TempDir(), tc.name)
			if err := os.WriteFile(path, tc.data, 0o600); err != nil {
				t.Fatal(err)
			}
			if selected, err := readSelection(t.Context(), path); selected != nil || !errors.Is(err, tc.want) {
				t.Fatalf("readSelection(%q) = %v, %v; want %v", tc.name, selected != nil, err, tc.want)
			}
		})
	}
}

func avifTestBox(kind string, parts ...[]byte) []byte {
	body := bytes.Join(parts, nil)
	return append(append(binary.BigEndian.AppendUint32(nil, uint32(8+len(body))), kind...), body...)
}

// The pixel payload is never read when the platform lacks the format's decoder.
func staticImageTestHeader(brand, codec string) []byte {
	ftyp := avifTestBox("ftyp", []byte(brand+"\x00\x00\x00\x00mif1"))
	handler := avifTestBox("hdlr", make([]byte, 8), []byte("pict"))
	primary := avifTestBox("pitm", []byte{0, 0, 0, 0, 0, 1})
	item := avifTestBox("infe", []byte{2, 0, 0, 0, 0, 1, 0, 0}, []byte(codec+"\x00"))
	info := avifTestBox("iinf", []byte{0, 0, 0, 0, 0, 1}, item)
	ispe := avifTestBox("ispe", []byte{0, 0, 0, 0, 0, 0, 0, 32, 0, 0, 0, 24})
	assoc := avifTestBox("ipma", []byte{0, 0, 0, 0, 0, 0, 0, 1, 0, 1, 1, 0x81})
	meta := avifTestBox("meta", make([]byte, 4), handler, primary, info, avifTestBox("iprp", avifTestBox("ipco", ispe), assoc))
	return bytes.Join([][]byte{ftyp, meta, avifTestBox("mdat", []byte{0})}, nil)
}
