package main

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"zpic/internal/compress"
)

func TestDesktopFolderWatchMultiRuleRecursiveAndNotify(t *testing.T) {
	root := t.TempDir()
	in1, out1 := filepath.Join(root, "in1"), filepath.Join(root, "out1")
	in2, out2 := filepath.Join(root, "in2"), filepath.Join(root, "out2")
	subIn2 := filepath.Join(in2, "nested", "album")
	if err := os.MkdirAll(in1, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(subIn2, 0o755); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(root, "settings.json")
	t.Setenv("ZPIC_CONFIG_FILE", settingsPath)

	app := newApplication()
	app.restoreSettings()
	updates := make(chan func(), 32)
	app.dispatchUpdate = func(update func()) { updates <- update }
	var notifications []string
	app.sendNotification = func(title, body string) {
		notifications = append(notifications, title+"|"+body)
	}
	t.Cleanup(app.closeFolderWatch)

	primary := folderWatchSettings{
		InputDir:  in1,
		OutputDir: out1,
		Preset:    "≤ 500 KiB",
		Recursive: false,
		AutoStart: true,
		Notify:    true,
	}
	extra := folderWatchRule{
		InputDir:  in2,
		OutputDir: out2,
		Preset:    "≤ 500 KiB",
		Recursive: true,
		AutoStart: true,
	}

	if err := app.startFolderWatch(primary); err != nil {
		t.Fatalf("start primary rule: %v", err)
	}
	awaitDesktopWatchUpdate(t, updates, func() bool { return app.watch.session != nil && app.watch.session.ready })

	if err := app.startWatchRule(1, extra); err != nil {
		t.Fatalf("start extra rule: %v", err)
	}
	awaitDesktopWatchUpdate(t, updates, func() bool {
		sess := app.watchSessionForRule(1)
		return sess != nil && sess.ready
	})

	if got := app.activeFolderWatchCount(); got != 2 {
		t.Fatalf("expected 2 active watch sessions, got %d", got)
	}
	if app.watchSessionForRule(1) == nil {
		t.Fatal("expected watchSessionForRule(1) to be active")
	}

	// Write a file to rule 0 and a nested file to rule 1.
	writeTestJPEG(t, filepath.Join(in1, "one.jpg"), 200, 140, 95)
	writeTestJPEG(t, filepath.Join(subIn2, "two.jpg"), 200, 140, 95)

	awaitDesktopWatchUpdate(t, updates, func() bool {
		s0 := app.watchSessionForRule(0)
		s1 := app.watchSessionForRule(1)
		return s0 != nil && s0.last.Input != "" && s1 != nil && s1.last.Input != ""
	})

	s1 := app.watchSessionForRule(1)
	if s1.last.Err != nil {
		t.Fatalf("recursive rule failed: %v", s1.last.Err)
	}
	resolvedOut2, err := filepath.EvalSymlinks(out2)
	if err != nil {
		t.Fatal(err)
	}
	expectedRel := filepath.Join("nested", "album", "two.jpg")
	gotRel, err := filepath.Rel(resolvedOut2, s1.last.Output)
	if err != nil || gotRel != expectedRel {
		t.Fatalf("recursive rule must mirror relative path: want %q, got %q (%v)", expectedRel, gotRel, err)
	}
	if len(notifications) < 2 {
		t.Fatalf("expected at least 2 notifications, got %v", notifications)
	}

	app.stopAllFolderWatches()
	awaitDesktopWatchUpdate(t, updates, func() bool { return app.activeFolderWatchCount() == 0 })

	// Verify persistence and AutoStart restoration in a fresh app instance.
	reopened := newApplication()
	reopened.restoreSettings()
	reopenedUpdates := make(chan func(), 32)
	reopened.dispatchUpdate = func(update func()) { reopenedUpdates <- update }
	t.Cleanup(reopened.closeFolderWatch)

	if reopened.watch.settings != primary {
		t.Fatalf("primary watch mismatch after restore: got %+v, want %+v", reopened.watch.settings, primary)
	}
	if len(reopened.watch.extraRules) != 1 || reopened.watch.extraRules[0] != extra {
		t.Fatalf("extra rules mismatch after restore: got %+v, want [%+v]", reopened.watch.extraRules, extra)
	}

	reopened.startAutoFolderWatches()
	awaitDesktopWatchUpdate(t, reopenedUpdates, func() bool {
		s0 := reopened.watchSessionForRule(0)
		s1 := reopened.watchSessionForRule(1)
		return s0 != nil && s0.ready && s1 != nil && s1.ready
	})
	if reopened.activeFolderWatchCount() != 2 {
		t.Fatalf("expected AutoStart to restore 2 running rules, got %d", reopened.activeFolderWatchCount())
	}
}

func TestCLIPresetsExportImportAndJPEGBg(t *testing.T) {
	root := t.TempDir()
	settingsPath := filepath.Join(root, "settings.json")
	exportPath := filepath.Join(root, "presets.json")

	// 1. Export a single named preset to a file.
	var stdout, stderr bytes.Buffer
	code := runCLI([]string{"presets", "export", "--settings", settingsPath, "-o", exportPath, "≤ 500 KiB"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("presets export failed (code=%d): %s", code, stderr.String())
	}
	exported, err := readPresetBundleFile(exportPath)
	if err != nil {
		t.Fatalf("read exported bundle: %v", err)
	}
	if len(exported) != 1 || exported[0].name != "≤ 500 KiB" {
		t.Fatalf("unexpected exported presets: %+v", exported)
	}

	// 2. Import into a fresh settings file (non-overwrite renames collision).
	targetSettings := filepath.Join(root, "target-settings.json")
	stdout.Reset()
	stderr.Reset()
	code = runCLI([]string{"presets", "import", "--settings", targetSettings, "--json", exportPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("presets import failed (code=%d): %s", code, stderr.String())
	}
	var report cliPresetImportReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode import JSON report: %v\nraw: %s", err, stdout.String())
	}
	if report.Added != 1 || report.Updated != 0 {
		t.Fatalf("expected 1 added / 0 updated without --overwrite, got %+v", report)
	}

	// 3. Import again with --overwrite.
	stdout.Reset()
	stderr.Reset()
	code = runCLI([]string{"presets", "import", "--settings", targetSettings, "--overwrite", "--json", exportPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("presets import --overwrite failed (code=%d): %s", code, stderr.String())
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode overwrite report: %v", err)
	}
	if report.Added != 0 || report.Updated != 1 {
		t.Fatalf("expected 0 added / 1 updated with --overwrite, got %+v", report)
	}

	// 4. Compress transparent PNG to JPEG with custom --jpeg-bg.
	transparentPNG := filepath.Join(root, "alpha.png")
	img := image.NewNRGBA(image.Rect(0, 0, 24, 24))
	// Fully transparent image so JPEG output reflects pure background color.
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transparentPNG, pngBuf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(root, "jpg-out")
	stdout.Reset()
	stderr.Reset()
	code = runCLI([]string{"compress", "--format", "jpeg", "--jpeg-bg", "#204080", "-o", outDir, transparentPNG}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("cli compress --jpeg-bg failed (code=%d): %s", code, stderr.String())
	}
	outJPEG := filepath.Join(outDir, "alpha.jpg")
	raw, err := os.ReadFile(outJPEG)
	if err != nil {
		t.Fatalf("read output jpeg: %v", err)
	}
	decoded, err := jpeg.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("decode output jpeg: %v", err)
	}
	gotColor := color.RGBAModel.Convert(decoded.At(12, 12)).(color.RGBA)
	absDiff := func(a, b uint8) int {
		if a > b {
			return int(a - b)
		}
		return int(b - a)
	}
	if absDiff(gotColor.R, 0x20) > 8 || absDiff(gotColor.G, 0x40) > 8 || absDiff(gotColor.B, 0x80) > 8 {
		t.Fatalf("expected JPEG background near #204080, got #%02x%02x%02x", gotColor.R, gotColor.G, gotColor.B)
	}

	// 5. Reject invalid --jpeg-bg with exit code 2.
	stdout.Reset()
	stderr.Reset()
	code = runCLI([]string{"compress", "--format", "jpeg", "--jpeg-bg", "not-a-color", transparentPNG}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "--jpeg-bg") {
		t.Fatalf("expected exit 2 with --jpeg-bg error, got code=%d stderr=%q", code, stderr.String())
	}
}

func TestSettingsStorePersistsKeepInMenuBarAndJPEGBackground(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	presets := defaultPresets()
	presets[0].outputFormat = compress.OutputFormatJPEG
	presets[0].jpegBackground = "#1a2b3c"

	cfg := appSettings{
		presets:       presets,
		selected:      presets[0].name,
		keepInMenuBar: true,
		watch: folderWatchSettings{
			InputDir:  "/tmp/in",
			OutputDir: "/tmp/out",
			Preset:    presets[0].name,
			Recursive: true,
			AutoStart: true,
			Notify:    true,
		},
		watchRules: []folderWatchRule{
			{InputDir: "/tmp/in2", OutputDir: "/tmp/out2", Preset: presets[0].name, Recursive: true, AutoStart: false},
		},
	}
	if err := saveFullSettings(path, cfg); err != nil {
		t.Fatalf("saveFullSettings: %v", err)
	}
	loaded, err := loadFullSettings(path)
	if err != nil {
		t.Fatalf("loadFullSettings: %v", err)
	}
	if !loaded.keepInMenuBar {
		t.Fatal("expected keepInMenuBar=true after roundtrip")
	}
	if loaded.presets[0].jpegBackground != "#1a2b3c" {
		t.Fatalf("expected jpegBackground #1a2b3c, got %q", loaded.presets[0].jpegBackground)
	}
	if !loaded.watch.Recursive || !loaded.watch.AutoStart || !loaded.watch.Notify {
		t.Fatalf("expected watch flags persisted, got %+v", loaded.watch)
	}
	if len(loaded.watchRules) != 1 || loaded.watchRules[0] != cfg.watchRules[0] {
		t.Fatalf("expected watchRules persisted, got %+v", loaded.watchRules)
	}
}

func TestTrayCloseAndIcon(t *testing.T) {
	icon := buildTrayTemplateIconPNG()
	cfg, err := png.DecodeConfig(bytes.NewReader(icon))
	if err != nil {
		t.Fatalf("tray icon must be valid PNG: %v", err)
	}
	if cfg.Width != 32 || cfg.Height != 32 {
		t.Fatalf("expected 32x32 tray icon, got %dx%d", cfg.Width, cfg.Height)
	}

	app := newApplication()
	if app.shouldHideMainWindowOnClose() {
		t.Fatal("must not hide when keepInMenuBar is false")
	}
	app.keepInMenuBar = true
	if app.shouldHideMainWindowOnClose() {
		t.Fatal("must not hide when tray is unavailable")
	}
	app.tray = &appTray{}
	if !app.shouldHideMainWindowOnClose() {
		t.Fatal("expected hide on close when keepInMenuBar and tray are active")
	}
	app.quitting = true
	if app.shouldHideMainWindowOnClose() {
		t.Fatal("must not hide when app is explicitly quitting")
	}
}
