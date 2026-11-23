package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"zpic/internal/compress"
)

func TestCLIDispatchAndStartupPathFiltering(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want bool
	}{
		{nil, false},
		{[]string{"photo.jpg"}, false},
		{[]string{"./photos"}, false},
		{[]string{"-psn_0_12345"}, false},
		{[]string{"compress", "photo.jpg"}, true},
		{[]string{"presets"}, true},
		{[]string{"version"}, true},
		{[]string{"-v"}, true},
		{[]string{"--version"}, true},
		{[]string{"help"}, true},
		{[]string{"-h"}, true},
		{[]string{"--help"}, true},
	} {
		if got := shouldRunCLI(tc.args); got != tc.want {
			t.Fatalf("shouldRunCLI(%v) = %v, want %v", tc.args, got, tc.want)
		}
	}

	merged := mergeStartupPaths([]string{"-psn_0_9876", "a.jpg", "a.jpg"}, []string{"b.png", "a.jpg"})
	if len(merged) != 2 || merged[0] != "a.jpg" || merged[1] != "b.png" {
		t.Fatalf("mergeStartupPaths filtered unexpected paths: %v", merged)
	}
}

func TestCLIVersionMatchesMygoConfig(t *testing.T) {
	data, err := os.ReadFile("mygo.json")
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if config.Version == "" || appVersion != config.Version {
		t.Fatalf("appVersion = %q, mygo.json version = %q", appVersion, config.Version)
	}
}

func TestCLIVersionAndHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runCLIContext(context.Background(), []string{"version", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("version --json exited %d: %s", code, stderr.String())
	}
	var ver cliVersionReport
	if err := json.Unmarshal(stdout.Bytes(), &ver); err != nil {
		t.Fatalf("invalid version JSON: %v\n%s", err, stdout.String())
	}
	if ver.Name != "z-pic" || ver.Version != appVersion || ver.OS == "" || ver.Arch == "" {
		t.Fatalf("unexpected version report: %+v", ver)
	}

	stdout.Reset()
	stderr.Reset()
	if code := runCLIContext(context.Background(), []string{"help", "compress"}, &stdout, &stderr); code != 0 {
		t.Fatalf("help compress exited %d: %s", code, stderr.String())
	}
	for _, want := range []string{"--in-place", "--dry-run", "--trash", "--no-recursive", "64–16384"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("compress help missing %q: %s", want, stdout.String())
		}
	}
}

func TestCLIPresetsListsSavedAndDefaultPresets(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	presets := []compressionPreset{
		{name: "均衡", quality: 80, png: compress.DefaultPNGOptions()},
		{
			name: "WebP 精选", quality: 75, png: compress.DefaultPNGOptions(),
			outputFormat: compress.OutputFormatWebP, maxDimension: 1920,
			metadata: compress.MetadataKeepICC,
		},
	}
	if err := saveFullSettings(settingsPath, appSettings{
		presets:  presets,
		selected: "WebP 精选",
		theme:    themeSystem,
	}); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := runCLIContext(context.Background(), []string{"presets", "--settings", settingsPath, "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("presets --json exited %d: %s", code, stderr.String())
	}
	var report cliPresetsReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("invalid presets JSON: %v", err)
	}
	if report.Selected != "WebP 精选" || len(report.Presets) != 2 {
		t.Fatalf("unexpected presets report: %+v", report)
	}
	if !report.Presets[1].Active || report.Presets[1].Format != "webp" || report.Presets[1].MaxDimension != 1920 || report.Presets[1].Metadata != "keep-icc" {
		t.Fatalf("unexpected second preset entry: %+v", report.Presets[1])
	}

	// Text output marks active preset.
	stdout.Reset()
	stderr.Reset()
	if code := runCLIContext(context.Background(), []string{"presets", "--settings", settingsPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("presets text exited %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "* WebP 精选") {
		t.Fatalf("expected active preset marker in text output:\n%s", stdout.String())
	}
}

func TestCLICompressSingleCopyExplicitFileDirectoryDryRunAndInPlace(t *testing.T) {
	t.Run("default safe copy preserves original", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "hero.jpg")
		orig := writeTestJPEG(t, src, 160, 120, 100)

		var stdout, stderr bytes.Buffer
		// Also test trailing flags after positional input.
		code := runCLIContext(context.Background(), []string{"compress", src, "-q", "40", "--json"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("compress exited %d: stderr=%s stdout=%s", code, stderr.String(), stdout.String())
		}
		var report cliCompressReport
		if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
			t.Fatalf("invalid compress JSON: %v", err)
		}
		if report.Total != 1 || report.Succeeded != 1 || report.Failed != 0 || report.SavedBytes <= 0 {
			t.Fatalf("unexpected report: %+v", report)
		}
		outPath := report.Files[0].Output
		if filepath.Base(outPath) != "hero-zpic.jpg" {
			t.Fatalf("expected hero-zpic.jpg, got %q", outPath)
		}
		// Original untouched.
		gotOrig, err := os.ReadFile(src)
		if err != nil || !bytes.Equal(gotOrig, orig) {
			t.Fatal("default copy mode must leave original intact")
		}
		outInfo, err := os.Stat(outPath)
		if err != nil || outInfo.Size() != report.Files[0].OutputBytes {
			t.Fatalf("output size mismatch: stat=%v report=%d", outInfo, report.Files[0].OutputBytes)
		}
	})

	t.Run("explicit output file with resize and metadata strip", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "wide.jpg")
		writeTestJPEG(t, src, 320, 160, 95)
		outFile := filepath.Join(dir, "out", "wide-small.jpg")

		var stdout, stderr bytes.Buffer
		code := runCLIContext(context.Background(), []string{
			"compress", "--max-dim", "160", "--metadata", "strip",
			"-o", outFile, "--json", src,
		}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("compress resize exited %d: %s", code, stderr.String())
		}
		var report cliCompressReport
		if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
			t.Fatal(err)
		}
		if len(report.Files) != 1 || report.Files[0].Output != outFile ||
			report.Files[0].OutputFormat != "jpeg" || report.Files[0].Width != 160 || report.Files[0].Height != 80 {
			t.Fatalf("unexpected resize report: %+v", report.Files)
		}
		info, err := compress.Inspect(context.Background(), outFile)
		if err != nil || info.Format != "jpeg" || info.Width != 160 || info.Height != 80 {
			t.Fatalf("output file inspect mismatch: %+v err=%v", info, err)
		}
	})

	t.Run("explicit output file with webp conversion", func(t *testing.T) {
		if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
			t.Skip("native WebP bundle currently ships for darwin/arm64")
		}
		dir := t.TempDir()
		src := filepath.Join(dir, "wide.jpg")
		writeTestJPEG(t, src, 320, 160, 95)
		outFile := filepath.Join(dir, "out", "wide.webp")

		var stdout, stderr bytes.Buffer
		code := runCLIContext(context.Background(), []string{
			"compress", "--format", "webp", "--max-dim", "160", "--metadata", "strip",
			"-o", outFile, "--json", src,
		}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("compress webp exited %d: %s", code, stderr.String())
		}
		var report cliCompressReport
		if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
			t.Fatal(err)
		}
		if len(report.Files) != 1 || report.Files[0].Output != outFile ||
			report.Files[0].OutputFormat != "webp" || report.Files[0].Width != 160 || report.Files[0].Height != 80 {
			t.Fatalf("unexpected webp resize report: %+v", report.Files)
		}
		info, err := compress.Inspect(context.Background(), outFile)
		if err != nil || info.Format != "webp" || info.Width != 160 || info.Height != 80 {
			t.Fatalf("output file inspect mismatch: %+v err=%v", info, err)
		}
	})

	t.Run("dry run writes nothing", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "dry.jpg")
		orig := writeTestJPEG(t, src, 120, 90, 100)

		var stdout, stderr bytes.Buffer
		code := runCLIContext(context.Background(), []string{"compress", "--dry-run", "-q", "35", "--json", src}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("dry-run exited %d: %s", code, stderr.String())
		}
		var report cliCompressReport
		if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
			t.Fatal(err)
		}
		if !report.DryRun || report.Files[0].Output != "" || report.SavedBytes <= 0 {
			t.Fatalf("unexpected dry-run report: %+v", report)
		}
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 1 {
			t.Fatalf("dry-run must not write any files, dir entries=%v", entries)
		}
		gotOrig, _ := os.ReadFile(src)
		if !bytes.Equal(gotOrig, orig) {
			t.Fatal("dry-run modified original file")
		}
	})

	t.Run("in-place replaces smaller source and skips non-existent", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "replace.jpg")
		orig := writeTestJPEG(t, src, 160, 120, 100)

		var stdout, stderr bytes.Buffer
		code := runCLIContext(context.Background(), []string{"compress", "--in-place", "-q", "35", "--json", src}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("in-place exited %d: %s", code, stderr.String())
		}
		var report cliCompressReport
		if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
			t.Fatal(err)
		}
		if !report.InPlace || !report.Files[0].Overwritten || report.Files[0].Output != src {
			t.Fatalf("unexpected in-place report: %+v", report)
		}
		after, err := os.ReadFile(src)
		if err != nil || len(after) >= len(orig) {
			t.Fatalf("in-place did not shrink file: before=%d after=%d err=%v", len(orig), len(after), err)
		}
	})

	t.Run("recursive folder to output directory preserves tree", func(t *testing.T) {
		root := t.TempDir()
		sub := filepath.Join(root, "nested")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		writeTestJPEG(t, filepath.Join(root, "a.jpg"), 100, 80, 95)
		writeTestJPEG(t, filepath.Join(sub, "b.jpg"), 100, 80, 95)
		outDir := filepath.Join(t.TempDir(), "compressed")

		var stdout, stderr bytes.Buffer
		code := runCLIContext(context.Background(), []string{"compress", "-q", "50", "-o", outDir, "--json", root}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("folder compress exited %d: %s", code, stderr.String())
		}
		var report cliCompressReport
		if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
			t.Fatal(err)
		}
		if report.Total != 2 || report.Succeeded != 2 || report.Failed != 0 {
			t.Fatalf("unexpected folder report: %+v", report)
		}
		if _, err := os.Stat(filepath.Join(outDir, "a.jpg")); err != nil {
			t.Fatalf("missing root output: %v", err)
		}
		if _, err := os.Stat(filepath.Join(outDir, "nested", "b.jpg")); err != nil {
			t.Fatalf("missing nested output: %v", err)
		}
	})
}

func TestCLICompressTrashRequiresInPlace(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runCLIContext(context.Background(), []string{"compress", "--trash", "photo.jpg"}, &stdout, &stderr)
	if code != 2 || strings.TrimSpace(stderr.String()) != "参数冲突：--trash 仅可与 --in-place 一起使用。" {
		t.Fatalf("--trash without --in-place: exit=%d, stderr=%q", code, stderr.String())
	}
}

func TestCLICompressTrashBackup(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	if err := saveFullSettings(settingsPath, appSettings{
		presets:       defaultPresets(),
		selected:      "均衡",
		backupToTrash: true,
	}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ZPIC_CONFIG_FILE", settingsPath)

	originalTrashItem := cliTrashItem
	defer func() { cliTrashItem = originalTrashItem }()
	for _, tc := range []struct {
		name       string
		args       []string
		wantBackup bool
	}{
		{name: "explicit trash", args: []string{"--trash"}, wantBackup: true},
		{name: "inherits from settings", args: []string{"--settings", settingsPath}, wantBackup: true},
		{name: "inherits from preset settings", args: []string{"--preset", "均衡"}, wantBackup: true},
		{name: "explicit false overrides settings", args: []string{"--settings", settingsPath, "--trash=false"}},
		{name: "in-place alone"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "hero.jpg")
			backup := filepath.Join(dir, "backup.jpg")
			original := writeTestJPEG(t, src, 160, 120, 100)
			trashCalls := 0
			cliTrashItem = func(path string) error {
				trashCalls++
				if path != src {
					t.Fatalf("trash path = %q, want %q", path, src)
				}
				if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, original) {
					t.Fatalf("trash callback must see the original image: %v", err)
				}
				return os.Rename(path, backup)
			}

			args := append([]string{"compress", "--in-place", "-q", "35", "--json", src}, tc.args...)
			var stdout, stderr bytes.Buffer
			if code := runCLIContext(context.Background(), args, &stdout, &stderr); code != 0 {
				t.Fatalf("compress exited %d: stderr=%s stdout=%s", code, stderr.String(), stdout.String())
			}
			var report cliCompressReport
			if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			if len(report.Files) != 1 || !report.Files[0].Overwritten || report.Files[0].BackedUp != tc.wantBackup {
				t.Fatalf("unexpected overwrite report: %+v", report)
			}
			if tc.wantBackup {
				if data, err := os.ReadFile(backup); trashCalls != 1 || err != nil || !bytes.Equal(data, original) {
					t.Errorf("expected one backup containing original bytes: calls=%d, err=%v", trashCalls, err)
				}
			} else if trashCalls != 0 {
				t.Errorf("unexpected trash calls: %d", trashCalls)
			}
			if data, err := os.ReadFile(src); err != nil || len(data) >= len(original) || int64(len(data)) != report.Files[0].OutputBytes {
				t.Errorf("expected compressed image at source: before=%d, after=%d, err=%v", len(original), len(data), err)
			}
		})
	}
}

func TestCLICompressRejectsInvalidSettingsWithoutPreset(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	args := []string{"compress", "--in-place", "--settings", settingsPath, filepath.Join(dir, "hero.jpg")}
	code := runCLIContext(context.Background(), args, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "读取设置失败") {
		t.Fatalf("invalid --settings without --preset: exit=%d, stderr=%q", code, stderr.String())
	}
}

func TestCLICompressNoRecursive(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	topImage := filepath.Join(root, "top.jpg")
	writeTestJPEG(t, topImage, 160, 120, 100)
	writeTestJPEG(t, filepath.Join(nested, "nested.jpg"), 160, 120, 100)
	outDir := t.TempDir()

	var stdout, stderr bytes.Buffer
	args := []string{"compress", "--no-recursive", "-q", "50", "-o", outDir, "--json", root}
	if code := runCLIContext(context.Background(), args, &stdout, &stderr); code != 0 {
		t.Fatalf("compress exited %d: stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var report cliCompressReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Total != 1 || report.Succeeded != 1 || len(report.Files) != 1 || report.Files[0].Input != topImage {
		t.Fatalf("expected only the top-level image in the report: %+v", report)
	}
	entries, err := os.ReadDir(outDir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "top.jpg" || entries[0].IsDir() {
		t.Fatalf("expected only top.jpg in output directory: entries=%v, err=%v", entries, err)
	}
}

func TestCLICompressPresetAndValidationErrors(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	if err := saveFullSettings(settingsPath, appSettings{
		presets: []compressionPreset{
			{
				name: "轻量", quality: 45, png: cliPNGOptionsForQuality(45),
				output: presetSaveOptions{Mode: presetSaveOriginal, UseSuffix: true, Suffix: "-lite"},
			},
		},
		selected: "轻量",
		theme:    themeSystem,
	}); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	src := filepath.Join(dir, "sample.jpg")
	writeTestJPEG(t, src, 120, 90, 100)

	var stdout, stderr bytes.Buffer
	code := runCLIContext(context.Background(), []string{
		"compress", "--settings", settingsPath, "--preset", "轻量", "--json", src,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("preset compress exited %d: %s", code, stderr.String())
	}
	var report cliCompressReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Preset != "轻量" || filepath.Base(report.Files[0].Output) != "sample-lite.jpg" {
		t.Fatalf("preset save suffix was not applied: %+v", report)
	}

	// Conflicting flags return exit code 2.
	for _, badArgs := range [][]string{
		{"compress"},
		{"compress", "--in-place", "-o", dir, src},
		{"compress", "--in-place", "--suffix", "-x", src},
		{"compress", "-q", "150", src},
		{"compress", "--format", "bmp", src},
		{"compress", "--metadata", "unknown", src},
		{"compress", "--preset", "不存在", "--settings", settingsPath, src},
		{"compress", "--format", "webp", "-o", filepath.Join(dir, "wrong.jpg"), src},
	} {
		stdout.Reset()
		stderr.Reset()
		if c := runCLIContext(context.Background(), badArgs, &stdout, &stderr); c != 2 {
			t.Fatalf("args %v returned exit %d, want 2 (stderr=%q)", badArgs, c, stderr.String())
		}
	}

	// Partial failure (one valid + one missing) still processes valid and exits 1.
	missing := filepath.Join(dir, "does-not-exist.jpg")
	stdout.Reset()
	stderr.Reset()
	code = runCLIContext(context.Background(), []string{"compress", "--dry-run", "--json", src, missing}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("partial failure should exit 1, got %d", code)
	}
	var partial cliCompressReport
	if err := json.Unmarshal(stdout.Bytes(), &partial); err != nil {
		t.Fatal(err)
	}
	if partial.Total != 2 || partial.Succeeded != 1 || partial.Failed != 1 {
		t.Fatalf("unexpected partial failure report: %+v", partial)
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing file check sanity")
	}
}

func TestCLIResizeModes(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "photo.jpg")
	original := writeTestJPEG(t, source, 320, 240, 95)
	settings := filepath.Join(dir, "settings.json")
	preset := compressionPreset{name: "按高缩放", quality: 80, png: compress.DefaultPNGOptions(), maxHeight: 144}
	if err := saveSettings(settings, []compressionPreset{preset}, preset.name); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name          string
		flags         []string
		width, height int
	}{
		{"inherit", nil, 192, 144},
		{"width", []string{"--max-width", "160"}, 160, 120},
		{"height", []string{"--max-height=96"}, 128, 96},
		{"percentage", []string{"--scale-percent", "25"}, 80, 60},
		{"full scale", []string{"--scale-percent", "100"}, 320, 240},
		{"legacy override", []string{"--max-dim", "128"}, 128, 96},
		{"disable inherited resize", []string{"--max-width", "0"}, 320, 240},
		{"no upscale", []string{"--max-width", "640"}, 320, 240},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "result.jpg")
			args := append([]string{source, "--settings", settings, "--preset", preset.name, "-o", output}, tc.flags...)
			result := runFormatConvertCompress(t, 1, args...).Files[0]
			data := readFormatConvertFile(t, output)
			config, err := jpeg.DecodeConfig(bytes.NewReader(data))
			if err != nil || config.Width != tc.width || config.Height != tc.height ||
				result.Width != tc.width || result.Height != tc.height || result.OutputBytes != int64(len(data)) {
				t.Fatalf("expected %dx%d in saved output and report: config=%+v result=%+v err=%v", tc.width, tc.height, config, result, err)
			}
		})
	}
	if !bytes.Equal(readFormatConvertFile(t, source), original) {
		t.Fatal("resizing a copy changed the source image")
	}
	for _, flags := range [][]string{
		{"--max-width", "63"}, {"--max-height", "16385"}, {"--scale-percent=-1"}, {"--scale-percent", "101"},
		{"--max-width", "0", "--max-height", "96"}, {"--max-dim", "128", "--scale-percent", "50"},
	} {
		var stdout, stderr bytes.Buffer
		if code := runCLIContext(t.Context(), append([]string{"compress", source}, flags...), &stdout, &stderr); code != 2 || stderr.Len() == 0 {
			t.Errorf("invalid resize flags %q: exit=%d stderr=%q", flags, code, stderr.String())
		}
	}

	var stdout, stderr bytes.Buffer
	args := []string{"presets", "--settings", settings, "--json"}
	if code := runCLIContext(t.Context(), args, &stdout, &stderr); code != 0 {
		t.Fatalf("presets JSON: exit=%d stderr=%q", code, stderr.String())
	}
	var report cliPresetsReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil || len(report.Presets) != 1 || report.Presets[0].MaxHeight != 144 {
		t.Fatalf("resize mode missing from presets JSON: %v\n%s", err, stdout.String())
	}
}

func writeCLITestPNG(t *testing.T, dir, name string, w, h int) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8((x * 255) / max(1, w-1)),
				G: uint8((y * 255) / max(1, h-1)),
				B: uint8((x*7 + y*13) % 256),
				A: 255,
			})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCLICompressPipeAndMetrics(t *testing.T) {
	dir := t.TempDir()
	srcPath := writeCLITestPNG(t, dir, "sample.png", 96, 64)
	srcBytes, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatal(err)
	}

	// 1. stdin -> stdout (implicit stdout when input is "-" and no -o/--dry-run/--json).
	var stdout, stderr bytes.Buffer
	code := runCLIWithIO(t.Context(), []string{"compress", "--format", "jpeg", "-q", "75", "-"}, bytes.NewReader(srcBytes), &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("stdin->stdout failed: code=%d stderr=%q", code, stderr.String())
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(stdout.Bytes()))
	if err != nil || cfg.Width != 96 || cfg.Height != 64 {
		t.Fatalf("stdin->stdout did not emit valid 96x64 JPEG: cfg=%+v err=%v", cfg, err)
	}

	// 2. file -> stdout (-o -) with --metrics writes JPEG to stdout and metrics to stderr.
	stdout.Reset()
	stderr.Reset()
	code = runCLIWithIO(t.Context(), []string{"compress", srcPath, "--format", "jpeg", "-q", "65", "--metrics", "-o", "-"}, nil, &stdout, &stderr)
	if code != 0 || !strings.Contains(stderr.String(), "METRICS") || !strings.Contains(stderr.String(), "SSIM ") {
		t.Fatalf("file->stdout with --metrics failed: code=%d stderr=%q", code, stderr.String())
	}
	cfg, err = jpeg.DecodeConfig(bytes.NewReader(stdout.Bytes()))
	if err != nil || cfg.Width != 96 || cfg.Height != 64 {
		t.Fatalf("file->stdout did not emit valid JPEG: cfg=%+v err=%v", cfg, err)
	}

	// 3. stdin -> file with --metrics --json.
	outPath := filepath.Join(dir, "from-stdin.jpg")
	stdout.Reset()
	stderr.Reset()
	code = runCLIWithIO(t.Context(), []string{"compress", "-", "-o", outPath, "--format", "jpeg", "-q", "70", "--metrics", "--json"}, bytes.NewReader(srcBytes), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("stdin->file failed: code=%d stderr=%q", code, stderr.String())
	}
	var report cliCompressReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("invalid JSON from stdin->file: %v\n%s", err, stdout.String())
	}
	if report.Total != 1 || report.Succeeded != 1 || len(report.Files) != 1 || report.Files[0].Output != outPath {
		t.Fatalf("unexpected stdin->file report: %+v", report)
	}
	if m := report.Files[0].Metrics; m == nil || m.SSIM <= 0 || m.SSIM > 1 || m.PSNRDB == nil || *m.PSNRDB <= 0 {
		t.Fatalf("expected valid quality metrics in JSON report, got %+v", m)
	}
	if _, err := os.Stat(outPath); err != nil {
		t.Fatalf("expected output file %s to exist: %v", outPath, err)
	}

	// 4. stdin -> --dry-run --metrics --json.
	stdout.Reset()
	stderr.Reset()
	code = runCLIWithIO(t.Context(), []string{"compress", "-", "--dry-run", "--format", "jpeg", "-q", "60", "--metrics", "--json"}, bytes.NewReader(srcBytes), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("stdin dry-run failed: code=%d stderr=%q", code, stderr.String())
	}
	var dryReport cliCompressReport
	if err := json.Unmarshal(stdout.Bytes(), &dryReport); err != nil || !dryReport.DryRun || dryReport.Files[0].Metrics == nil {
		t.Fatalf("expected dry-run JSON with metrics: err=%v report=%+v", err, dryReport)
	}

	// 5. File batch mode --metrics text output contains SSIM and PSNR.
	stdout.Reset()
	stderr.Reset()
	code = runCLIWithIO(t.Context(), []string{"compress", srcPath, "--format", "jpeg", "-q", "65", "--dry-run", "--metrics"}, nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("file --metrics failed: code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "SSIM ") || !strings.Contains(stdout.String(), "PSNR ") {
		t.Fatalf("expected SSIM/PSNR in text output, got:\n%s", stdout.String())
	}

	// 6. Conflict & error cases.
	for _, tc := range []struct {
		name string
		args []string
		in   []byte
		want int
	}{
		{"stdin with extra file", []string{"compress", "-", srcPath}, srcBytes, 2},
		{"stdin with in-place", []string{"compress", "--in-place", "-"}, srcBytes, 2},
		{"stdout with json", []string{"compress", srcPath, "-o", "-", "--json"}, nil, 2},
		{"stdout with dry-run", []string{"compress", srcPath, "-o", "-", "--dry-run"}, nil, 2},
		{"stdout with directory", []string{"compress", dir, "-o", "-"}, nil, 2},
		{"stdin json without dry-run or output", []string{"compress", "-", "--json"}, srcBytes, 2},
		{"empty stdin", []string{"compress", "-"}, nil, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, errBuf bytes.Buffer
			got := runCLIWithIO(t.Context(), tc.args, bytes.NewReader(tc.in), &out, &errBuf)
			if got != tc.want || errBuf.Len() == 0 {
				t.Fatalf("%s: exit=%d want=%d stderr=%q", tc.name, got, tc.want, errBuf.String())
			}
		})
	}
}

func TestCLIPresetsExportPipeToImportStdin(t *testing.T) {
	dir := t.TempDir()
	srcSettings := filepath.Join(dir, "src.json")
	dstSettings := filepath.Join(dir, "dst.json")

	custom := defaultPresets()[0]
	custom.name = "管道预设"
	custom.quality = 66
	if err := saveFullSettings(srcSettings, appSettings{
		presets:  []compressionPreset{custom},
		selected: custom.name,
		theme:    themeSystem,
	}); err != nil {
		t.Fatal(err)
	}
	if err := saveFullSettings(dstSettings, appSettings{
		presets:  defaultPresets(),
		selected: defaultPresets()[0].name,
		theme:    themeSystem,
	}); err != nil {
		t.Fatal(err)
	}

	var exported, errBuf bytes.Buffer
	if code := runCLIWithIO(t.Context(), []string{"presets", "export", "--settings", srcSettings}, nil, &exported, &errBuf); code != 0 {
		t.Fatalf("presets export failed: code=%d stderr=%q", code, errBuf.String())
	}

	var importOut bytes.Buffer
	errBuf.Reset()
	if code := runCLIWithIO(t.Context(), []string{"presets", "import", "--settings", dstSettings, "--json", "-"}, bytes.NewReader(exported.Bytes()), &importOut, &errBuf); code != 0 {
		t.Fatalf("presets import from stdin failed: code=%d stderr=%q", code, errBuf.String())
	}
	var report cliPresetImportReport
	if err := json.Unmarshal(importOut.Bytes(), &report); err != nil || report.Added != 1 {
		t.Fatalf("unexpected import report: err=%v report=%+v", err, report)
	}
	loaded, err := loadFullSettings(dstSettings)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range loaded.presets {
		if p.name == "管道预设" && p.quality == 66 {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("imported preset not found in %s", dstSettings)
	}

	// Canceled context must abort stdin preset import promptly.
	canceledCtx, cancel := context.WithCancel(t.Context())
	cancel()
	var cancelOut, cancelErr bytes.Buffer
	if code := runCLIWithIO(canceledCtx, []string{"presets", "import", "--settings", dstSettings, "-"}, bytes.NewReader(exported.Bytes()), &cancelOut, &cancelErr); code != 1 || cancelErr.Len() == 0 {
		t.Fatalf("canceled presets import should fail with exit 1: code=%d stderr=%q", code, cancelErr.String())
	}
}

func TestCLICompressStdinEdgeCases(t *testing.T) {
	dir := t.TempDir()
	srcPath := writeCLITestPNG(t, dir, "sample.png", 64, 48)
	srcBytes, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Stdin runtime errors with --json must still emit a valid JSON report with status=failed.
	for _, tc := range []struct {
		name string
		args []string
		in   []byte
	}{
		{"empty stdin json", []string{"compress", "-", "--dry-run", "--json"}, nil},
		{"corrupt stdin json", []string{"compress", "-", "--dry-run", "--json"}, []byte("not-an-image")},
		{"existing output json", []string{"compress", "-", "-o", srcPath, "--json"}, srcBytes},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, errBuf bytes.Buffer
			code := runCLIWithIO(t.Context(), tc.args, bytes.NewReader(tc.in), &out, &errBuf)
			if code != 1 {
				t.Fatalf("expected exit 1, got %d (stdout=%q stderr=%q)", code, out.String(), errBuf.String())
			}
			var report cliCompressReport
			if err := json.Unmarshal(out.Bytes(), &report); err != nil {
				t.Fatalf("expected valid JSON error report, got err=%v stdout=%q stderr=%q", err, out.String(), errBuf.String())
			}
			if report.Total != 1 || report.Failed != 1 || len(report.Files) != 1 || report.Files[0].Status != "failed" || report.Files[0].Error == "" {
				t.Fatalf("unexpected failed JSON report: %+v", report)
			}
		})
	}

	// 2. Empty -o "" with stdin must report an --output error, not a --json error.
	var out, errBuf bytes.Buffer
	code := runCLIWithIO(t.Context(), []string{"compress", "-o", "", "-"}, bytes.NewReader(srcBytes), &out, &errBuf)
	if code != 2 || strings.Contains(errBuf.String(), "--json") || !strings.Contains(errBuf.String(), "-o") {
		t.Fatalf("unexpected error for -o \"\" -: code=%d stderr=%q", code, errBuf.String())
	}

	// 3. Saving stdin to a file literally named "stdin.png" must not collide with any synthetic source path.
	t.Chdir(dir)
	out.Reset()
	errBuf.Reset()
	code = runCLIWithIO(t.Context(), []string{"compress", "-", "-o", "stdin.png", "--json"}, bytes.NewReader(srcBytes), &out, &errBuf)
	if code != 0 {
		t.Fatalf("compressing stdin to stdin.png failed: code=%d stdout=%q stderr=%q", code, out.String(), errBuf.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "stdin.png")); err != nil {
		t.Fatalf("expected stdin.png to be created: %v", err)
	}
}

func TestCLICompressActivePreset(t *testing.T) {
	dir := t.TempDir()
	srcPath := writeCLITestPNG(t, dir, "hero.png", 64, 48)
	settingsPath := filepath.Join(dir, "settings.json")
	custom := compressionPreset{
		name:         "访达专用 WebP",
		quality:      72,
		png:          compress.DefaultPNGOptions(),
		outputFormat: compress.OutputFormatWebP,
		metadata:     compress.MetadataStrip,
	}
	if err := saveFullSettings(settingsPath, appSettings{
		presets:  append(defaultPresets(), custom),
		selected: custom.name,
	}); err != nil {
		t.Fatal(err)
	}

	// 1. --active-preset loads the active desktop preset and records its name in JSON output.
	var out, errBuf bytes.Buffer
	code := runCLIContext(t.Context(), []string{"compress", "--active-preset", "--settings", settingsPath, "--dry-run", "--json", srcPath}, &out, &errBuf)
	if code != 0 {
		t.Fatalf("compress --active-preset failed: code=%d stdout=%q stderr=%q", code, out.String(), errBuf.String())
	}
	var report cliCompressReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out.String())
	}
	if report.Preset != custom.name || len(report.Files) != 1 || report.Files[0].OutputFormat != "webp" {
		t.Fatalf("expected active preset %q with webp output, got %+v", custom.name, report)
	}

	// 2. Combining --preset and --active-preset returns parameter conflict (exit 2).
	out.Reset()
	errBuf.Reset()
	code = runCLIContext(t.Context(), []string{"compress", "--preset", "均衡", "--active-preset", "--settings", settingsPath, "--dry-run", srcPath}, &out, &errBuf)
	if code != 2 || !strings.Contains(errBuf.String(), "--active-preset") {
		t.Fatalf("expected exit 2 conflict for --preset + --active-preset: code=%d stderr=%q", code, errBuf.String())
	}
}

func TestCLICompressOutputPathEdgeCases(t *testing.T) {
	dir := t.TempDir()
	srcA := filepath.Join(dir, "photo.jpg")
	srcB := filepath.Join(dir, "second.jpg")
	writeTestJPEG(t, srcA, 120, 90, 95)
	writeTestJPEG(t, srcB, 120, 90, 95)

	// 1. Single file with unsupported output extension rejects with exit 2 and does not create a directory.
	for _, name := range []string{"out.bmp", "out.jgp"} {
		t.Run("unsupported ext "+name, func(t *testing.T) {
			target := filepath.Join(dir, name)
			var stdout, stderr bytes.Buffer
			code := runCLIContext(t.Context(), []string{"compress", "-o", target, srcA}, &stdout, &stderr)
			if code != 2 || !strings.Contains(stderr.String(), "不支持的输出文件扩展名") {
				t.Fatalf("expected exit 2 for unsupported ext %s: code=%d stderr=%q", name, code, stderr.String())
			}
			if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("must not create directory or file at %s, stat err=%v", target, err)
			}
		})
	}

	// 2. Existing regular file as -o target rejects with exit 2 for both single-file non-image and multi-file/dir runs.
	notesFile := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(notesFile, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	existingImg := filepath.Join(dir, "existing.jpg")
	writeTestJPEG(t, existingImg, 80, 60, 90)
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"single file to existing txt", []string{"compress", "-o", notesFile, srcA}},
		{"multiple files to existing jpg", []string{"compress", "-o", existingImg, srcA, srcB}},
		{"directory to existing txt", []string{"compress", "-o", notesFile, dir}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runCLIContext(t.Context(), tc.args, &stdout, &stderr)
			if code != 2 || !strings.Contains(stderr.String(), "已存在且不是目录") {
				t.Fatalf("expected exit 2 for %s: code=%d stderr=%q", tc.name, code, stderr.String())
			}
		})
	}
	if data, err := os.ReadFile(notesFile); err != nil || string(data) != "keep me" {
		t.Fatalf("existing regular file was modified: %q err=%v", string(data), err)
	}

	// 3. Existing directory containing a dot in its name still works as an output directory for a single file.
	t.Run("existing dotted dir", func(t *testing.T) {
		dottedDir := filepath.Join(dir, "out.v1")
		if err := os.MkdirAll(dottedDir, 0o755); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		code := runCLIContext(t.Context(), []string{"compress", "-q", "50", "-o", dottedDir, "--json", srcA}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("existing dotted dir failed: code=%d stderr=%q", code, stderr.String())
		}
		var report cliCompressReport
		if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
			t.Fatal(err)
		}
		wantOut := filepath.Join(dottedDir, "photo.jpg")
		if len(report.Files) != 1 || report.Files[0].Output != wantOut {
			t.Fatalf("expected output %q, got %+v", wantOut, report.Files)
		}
		if _, err := os.Stat(wantOut); err != nil {
			t.Fatalf("expected file at %s: %v", wantOut, err)
		}
	})

	// 4. Non-existent dotted directory with trailing path separator works as an output directory.
	t.Run("nonexistent dotted dir with trailing separator", func(t *testing.T) {
		dottedDir := filepath.Join(dir, "out.v2")
		var stdout, stderr bytes.Buffer
		code := runCLIContext(t.Context(), []string{"compress", "-q", "50", "-o", dottedDir + string(filepath.Separator), "--json", srcA}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("dotted dir with trailing slash failed: code=%d stderr=%q", code, stderr.String())
		}
		var report cliCompressReport
		if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
			t.Fatal(err)
		}
		wantOut := filepath.Join(dottedDir, "photo.jpg")
		if len(report.Files) != 1 || report.Files[0].Output != wantOut {
			t.Fatalf("expected output %q, got %+v", wantOut, report.Files)
		}
		if _, err := os.Stat(wantOut); err != nil {
			t.Fatalf("expected file at %s: %v", wantOut, err)
		}
	})
}

func TestCLICompressIncompletePresetSaveFixed(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "photo.jpg")
	writeTestJPEG(t, src, 120, 90, 95)

	settingsPath := filepath.Join(dir, "settings.json")
	incomplete := compressionPreset{
		name:    "未配目录",
		quality: 70,
		png:     compress.DefaultPNGOptions(),
		output:  presetSaveOptions{Mode: presetSaveFixed, Directory: ""},
	}
	if err := saveFullSettings(settingsPath, appSettings{
		presets:  append(defaultPresets(), incomplete),
		selected: incomplete.name,
	}); err != nil {
		t.Fatal(err)
	}

	// 1. Fails fast before scanning when using --preset or --active-preset without -o/--in-place/--dry-run/--suffix.
	missingInput := filepath.Join(dir, "does-not-exist.jpg")
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"explicit preset missing input fails fast", []string{"compress", "--settings", settingsPath, "--preset", incomplete.name, missingInput}},
		{"explicit preset valid input fails fast", []string{"compress", "--settings", settingsPath, "--preset", incomplete.name, src}},
		{"active preset fails fast", []string{"compress", "--settings", settingsPath, "--active-preset", missingInput}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runCLIContext(t.Context(), tc.args, &stdout, &stderr)
			if code != 2 || !strings.Contains(stderr.String(), "固定输出目录") || !strings.Contains(stderr.String(), incomplete.name) {
				t.Fatalf("expected exit 2 incomplete preset error for %s: code=%d stderr=%q", tc.name, code, stderr.String())
			}
		})
	}

	// 2. Overriding with -o, --dry-run, --suffix, or --in-place succeeds.
	t.Run("overrides bypass incomplete preset directory", func(t *testing.T) {
		outFile := filepath.Join(dir, "explicit.jpg")
		inPlaceSrc := filepath.Join(dir, "inplace.jpg")
		writeTestJPEG(t, inPlaceSrc, 120, 90, 95)
		for _, args := range [][]string{
			{"compress", "--settings", settingsPath, "--preset", incomplete.name, "-o", outFile, src},
			{"compress", "--settings", settingsPath, "--preset", incomplete.name, "--dry-run", src},
			{"compress", "--settings", settingsPath, "--preset", incomplete.name, "--suffix", "-ok", src},
			{"compress", "--settings", settingsPath, "--preset", incomplete.name, "--in-place", inPlaceSrc},
		} {
			var stdout, stderr bytes.Buffer
			if code := runCLIContext(t.Context(), args, &stdout, &stderr); code != 0 {
				t.Fatalf("args %v failed: code=%d stderr=%q", args, code, stderr.String())
			}
		}
	})
}

func TestCLICompressSkipOverwriteSnapshot(t *testing.T) {
	origHook := cliCompressFileWithAcquire
	defer func() { cliCompressFileWithAcquire = origHook }()

	var recorded []bool
	cliCompressFileWithAcquire = func(
		ctx context.Context,
		path string,
		options compress.Options,
		acquire func(compress.ImageInfo) (func(), error),
		onStart func(),
	) (*compress.Result, func(), error) {
		recorded = append(recorded, options.SkipOverwriteSnapshot)
		return origHook(ctx, path, options, acquire, onStart)
	}

	dir := t.TempDir()
	src := filepath.Join(dir, "photo.jpg")
	writeTestJPEG(t, src, 120, 90, 100)

	for _, tc := range []struct {
		name     string
		args     []string
		wantSkip bool
	}{
		{
			name:     "single explicit file -o skips overwrite snapshot",
			args:     []string{"compress", "-q", "50", "-o", filepath.Join(dir, "single.jpg"), src},
			wantSkip: true,
		},
		{
			name:     "default copy mode skips overwrite snapshot",
			args:     []string{"compress", "-q", "50", src},
			wantSkip: true,
		},
		{
			name:     "dry-run skips overwrite snapshot",
			args:     []string{"compress", "--dry-run", "-q", "50", src},
			wantSkip: true,
		},
		{
			name:     "in-place captures overwrite snapshot",
			args:     []string{"compress", "--in-place", "-q", "50", src},
			wantSkip: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorded = nil
			var stdout, stderr bytes.Buffer
			if code := runCLIContext(t.Context(), tc.args, &stdout, &stderr); code != 0 {
				t.Fatalf("compress failed: code=%d stderr=%q", code, stderr.String())
			}
			if len(recorded) != 1 || recorded[0] != tc.wantSkip {
				t.Fatalf("SkipOverwriteSnapshot = %v, want [%v]", recorded, tc.wantSkip)
			}
		})
	}
}
