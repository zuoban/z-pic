package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
