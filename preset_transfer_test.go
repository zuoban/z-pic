package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"zpic/internal/compress"
)

func testTransferPresets(t *testing.T) []compressionPreset {
	t.Helper()
	fixedDir := filepath.Join(t.TempDir(), "exports")
	return []compressionPreset{
		{
			name:         "网页发布",
			quality:      78,
			png:          compress.PNGOptions{MinQuality: 45, MaxQuality: 78, Colors: 192, Speed: 3},
			metadata:     compress.MetadataKeepICC,
			outputFormat: compress.OutputFormatWebP,
			maxDimension: 1920,
			output: presetSaveOptions{
				Mode:         presetSaveFixed,
				Directory:    fixedDir,
				UseSubfolder: true,
				Subfolder:    "web",
				UseSuffix:    true,
				Suffix:       "-min",
			},
		},
		{
			name:                     "社媒限体积",
			quality:                  82,
			png:                      compress.DefaultPNGOptions(),
			metadata:                 compress.MetadataStrip,
			targetSizeBytes:          300 << 10,
			targetResizeMinDimension: 640,
			output: presetSaveOptions{
				Mode:      presetSaveAsk,
				UseSuffix: true,
				Suffix:    "-sm",
			},
		},
		{
			name:         "无损存档",
			quality:      90,
			png:          compress.PNGOptions{MinQuality: 85, MaxQuality: 100, Colors: 256, Speed: 1},
			lossless:     true,
			metadata:     compress.MetadataKeepAll,
			scalePercent: 80,
		},
	}
}

func TestPresetBundleRoundTrip(t *testing.T) {
	presets := testTransferPresets(t)

	data, err := marshalPresetBundle(presets)
	if err != nil {
		t.Fatalf("marshalPresetBundle failed: %v", err)
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		t.Fatalf("expected pretty JSON with trailing newline, got %q", data)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("marshaled bundle is not valid JSON: %v", err)
	}
	if string(raw["version"]) != "1" || len(raw) != 2 {
		t.Fatalf("unexpected bundle top-level schema: %s", data)
	}

	decoded, err := unmarshalPresetBundle(data)
	if err != nil {
		t.Fatalf("unmarshalPresetBundle failed: %v", err)
	}
	if !reflect.DeepEqual(decoded, presets) {
		t.Fatalf("round-trip mismatch:\n got: %+v\nwant: %+v", decoded, presets)
	}

	// File read/write round-trip and atomic temp cleanup.
	outDir := filepath.Join(t.TempDir(), "nested")
	bundlePath := filepath.Join(outDir, "presets.json")
	if err := writePresetBundleFile(bundlePath, presets); err != nil {
		t.Fatalf("writePresetBundleFile failed: %v", err)
	}
	fromFile, err := readPresetBundleFile(bundlePath)
	if err != nil {
		t.Fatalf("readPresetBundleFile failed: %v", err)
	}
	if !reflect.DeepEqual(fromFile, presets) {
		t.Fatalf("file round-trip mismatch:\n got: %+v\nwant: %+v", fromFile, presets)
	}

	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".presets-") {
			t.Fatalf("unexpected leftover temp file: %s", entry.Name())
		}
	}
}

func TestPresetBundleImportFromFullSettingsJSON(t *testing.T) {
	presets := testTransferPresets(t)
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	downloadDir := filepath.Join(t.TempDir(), "downloads")

	cfg := appSettings{
		presets:        presets,
		selected:       presets[1].name,
		batchOutputDir: filepath.Join(t.TempDir(), "batch"),
		theme:          themeDark,
		autoCompress:   true,
		backupToTrash:  true,
		downloadDir:    downloadDir,
		watch: folderWatchSettings{
			InputDir:  filepath.Join(t.TempDir(), "in"),
			OutputDir: filepath.Join(t.TempDir(), "out"),
			Preset:    presets[0].name,
		},
	}
	if err := saveFullSettings(settingsPath, cfg); err != nil {
		t.Fatalf("saveFullSettings failed: %v", err)
	}

	imported, err := readPresetBundleFile(settingsPath)
	if err != nil {
		t.Fatalf("readPresetBundleFile on full settings.json failed: %v", err)
	}
	if !reflect.DeepEqual(imported, presets) {
		t.Fatalf("imported presets from settings.json mismatch:\n got: %+v\nwant: %+v", imported, presets)
	}

	// Also ensure legacy/optional "favorite" field in storedPreset is accepted.
	withFavorite := `{"version":1,"selected":"均衡","presets":[{"name":"均衡","quality":80,"png":{"MinQuality":65,"MaxQuality":85,"Colors":256,"Speed":3},"favorite":true}]}`
	favImported, err := unmarshalPresetBundle([]byte(withFavorite))
	if err != nil {
		t.Fatalf("expected settings.json with favorite field to import: %v", err)
	}
	if len(favImported) != 1 || favImported[0].name != "均衡" {
		t.Fatalf("unexpected imported presets: %+v", favImported)
	}
}

func TestPresetBundleRejectsInvalidInput(t *testing.T) {
	const validBundle = `{"version":1,"presets":[{"name":"均衡","quality":80,"png":{"MinQuality":65,"MaxQuality":85,"Colors":256,"Speed":3}}]}`

	t.Run("marshal rejects empty and invalid presets", func(t *testing.T) {
		if _, err := marshalPresetBundle(nil); err == nil {
			t.Fatal("expected error for nil presets")
		}
		if _, err := marshalPresetBundle([]compressionPreset{}); err == nil {
			t.Fatal("expected error for empty presets")
		}
		invalid := []compressionPreset{{name: "无效质量", quality: 101, png: compress.DefaultPNGOptions()}}
		if _, err := marshalPresetBundle(invalid); err == nil {
			t.Fatal("expected error for invalid preset quality")
		}
	})

	t.Run("rejects invalid version", func(t *testing.T) {
		for name, raw := range map[string]string{
			"version 0":       strings.Replace(validBundle, `"version":1`, `"version":0`, 1),
			"version 2":       strings.Replace(validBundle, `"version":1`, `"version":2`, 1),
			"missing version": `{"presets":[{"name":"均衡","quality":80,"png":{"MinQuality":65,"MaxQuality":85,"Colors":256,"Speed":3}}]}`,
			"settings v2":     `{"version":2,"selected":"均衡","presets":[{"name":"均衡","quality":80,"png":{"MinQuality":65,"MaxQuality":85,"Colors":256,"Speed":3}}]}`,
		} {
			t.Run(name, func(t *testing.T) {
				if _, err := unmarshalPresetBundle([]byte(raw)); err == nil || !strings.Contains(err.Error(), "版本") {
					t.Fatalf("expected version error for %s, got %v", name, err)
				}
			})
		}
	})

	t.Run("rejects unknown fields", func(t *testing.T) {
		for name, raw := range map[string]string{
			"top level": strings.Replace(validBundle, `"version":1`, `"version":1,"extra":true`, 1),
			"in preset": strings.Replace(validBundle, `"quality":80`, `"quality":80,"bogus":1`, 1),
			"in settings": `{"version":1,"selected":"均衡","unknown_setting":1,"presets":[{"name":"均衡","quality":80,"png":{"MinQuality":65,"MaxQuality":85,"Colors":256,"Speed":3}}]}`,
		} {
			t.Run(name, func(t *testing.T) {
				if _, err := unmarshalPresetBundle([]byte(raw)); err == nil {
					t.Fatalf("expected unknown field error for %s", name)
				}
			})
		}
	})

	t.Run("rejects duplicate keys", func(t *testing.T) {
		for name, raw := range map[string]string{
			"top level":      strings.Replace(validBundle, `"version":1`, `"version":2,"version":1`, 1),
			"case folded":    strings.Replace(validBundle, `"version":1`, `"VERSION":2,"version":1`, 1),
			"escaped":        strings.Replace(validBundle, `"presets":[`, `"presets":[],"\u0070resets":[`, 1),
			"in preset":      strings.Replace(validBundle, `"quality":80`, `"quality":50,"quality":80`, 1),
			"in png options": strings.Replace(validBundle, `"Colors":256`, `"Colors":64,"colors":256`, 1),
			"in output":      strings.Replace(validBundle, `"quality":80`, `"output":{"mode":"ask","mode":"fixed"},"quality":80`, 1),
		} {
			t.Run(name, func(t *testing.T) {
				if _, err := unmarshalPresetBundle([]byte(raw)); err == nil || !strings.Contains(err.Error(), "重复字段") {
					t.Fatalf("expected duplicate field error for %s, got %v", name, err)
				}
			})
		}
	})

	t.Run("rejects bad unicode", func(t *testing.T) {
		for name, raw := range map[string][]byte{
			"invalid utf8":         []byte(strings.Replace(validBundle, "均衡", string([]byte{0xff, 0xfe}), 1)),
			"unpaired high escape": []byte(strings.Replace(validBundle, "均衡", `\ud800`, 1)),
			"unpaired low escape":  []byte(strings.Replace(validBundle, "均衡", `\udc00`, 1)),
		} {
			t.Run(name, func(t *testing.T) {
				if _, err := unmarshalPresetBundle(raw); err == nil || !strings.Contains(err.Error(), "Unicode") {
					t.Fatalf("expected Unicode error for %s, got %v", name, err)
				}
			})
		}
	})

	t.Run("rejects trailing tokens and non-object roots", func(t *testing.T) {
		for name, raw := range map[string]string{
			"trailing object": validBundle + `{}`,
			"trailing number": validBundle + ` 42`,
			"null root":       `null`,
			"array root":      `[]`,
			"empty":           ``,
		} {
			t.Run(name, func(t *testing.T) {
				if _, err := unmarshalPresetBundle([]byte(raw)); err == nil {
					t.Fatalf("expected error for %s", name)
				}
			})
		}
	})

	t.Run("rejects oversized payload", func(t *testing.T) {
		oversized := append([]byte(validBundle), bytes.Repeat([]byte(" "), maxSettingsBytes)...)
		if _, err := unmarshalPresetBundle(oversized); err == nil || !strings.Contains(err.Error(), "1 MiB") {
			t.Fatalf("expected size limit error, got %v", err)
		}
	})

	t.Run("rejects invalid preset parameters", func(t *testing.T) {
		longName := strings.Repeat("长", 41)
		for name, raw := range map[string]string{
			"empty presets":      `{"version":1,"presets":[]}`,
			"quality out of range": strings.Replace(validBundle, `"quality":80`, `"quality":101`, 1),
			"fractional quality": strings.Replace(validBundle, `"quality":80`, `"quality":80.5`, 1),
			"missing png":        `{"version":1,"presets":[{"name":"均衡","quality":80}]}`,
			"invalid png colors": strings.Replace(validBundle, `"Colors":256`, `"Colors":1`, 1),
			"invalid metadata":   strings.Replace(validBundle, `"quality":80`, `"quality":80,"metadata":"bogus"`, 1),
			"invalid format":     strings.Replace(validBundle, `"quality":80`, `"quality":80,"output_format":"bmp"`, 1),
			"conflicting resize": strings.Replace(validBundle, `"quality":80`, `"quality":80,"max_dimension":1000,"max_width":800`, 1),
			"invalid target":     strings.Replace(validBundle, `"quality":80`, `"quality":80,"target_size_bytes":-1`, 1),
			"name too long":      strings.Replace(validBundle, "均衡", longName, 1),
			"name leading space": strings.Replace(validBundle, "均衡", " 均衡", 1),
			"duplicate preset":   `{"version":1,"presets":[{"name":"重复","quality":80,"png":{"MinQuality":65,"MaxQuality":85,"Colors":256,"Speed":3}},{"name":"重复","quality":70,"png":{"MinQuality":65,"MaxQuality":85,"Colors":256,"Speed":3}}]}`,
			"relative fixed dir": strings.Replace(validBundle, `"quality":80`, `"quality":80,"output":{"mode":"fixed","directory":"relative/dir"}`, 1),
		} {
			t.Run(name, func(t *testing.T) {
				if _, err := unmarshalPresetBundle([]byte(raw)); err == nil {
					t.Fatalf("expected validation error for %s", name)
				}
			})
		}
	})

	t.Run("file helpers validate paths and file types", func(t *testing.T) {
		if _, err := readPresetBundleFile(""); err == nil {
			t.Fatal("expected error for empty read path")
		}
		if _, err := readPresetBundleFile(t.TempDir()); err == nil {
			t.Fatal("expected error when reading a directory")
		}
		if err := writePresetBundleFile("", defaultPresets()); err == nil {
			t.Fatal("expected error for empty write path")
		}
	})
}

func TestMergeImportedPresetsRenameConflicts(t *testing.T) {
	basePNG := compress.DefaultPNGOptions()
	long40 := strings.Repeat("超", 40)
	// 40-rune name whose 35th rune (index 34) is a space so truncation hits a space boundary.
	spaceBoundary40 := strings.Repeat("a", 34) + " " + strings.Repeat("b", 5)
	if utf8.RuneCountInString(spaceBoundary40) != 40 {
		t.Fatalf("expected 40 runes, got %d", utf8.RuneCountInString(spaceBoundary40))
	}

	existing := []compressionPreset{
		{name: "均衡", quality: 80, png: basePNG},
		{name: "均衡 (导入)", quality: 75, png: basePNG},
		{name: long40, quality: 70, png: basePNG},
		{name: spaceBoundary40, quality: 65, png: basePNG},
	}
	existingSnapshot := append([]compressionPreset(nil), existing...)

	imported := []compressionPreset{
		{name: "均衡", quality: 60, png: basePNG},
		{name: "全新预设", quality: 85, png: basePNG},
		{name: "全新预设", quality: 88, png: basePNG},
		{name: long40, quality: 55, png: basePNG},
		{name: long40, quality: 52, png: basePNG},
		{name: spaceBoundary40, quality: 50, png: basePNG},
	}
	importedSnapshot := append([]compressionPreset(nil), imported...)

	merged, added, updated, firstIdx := mergeImportedPresets(existing, imported, false)

	if added != len(imported) || updated != 0 {
		t.Fatalf("counts = (added=%d, updated=%d), want (added=%d, updated=0)", added, updated, len(imported))
	}
	if firstIdx != len(existing) {
		t.Fatalf("firstImportedIndex = %d, want %d", firstIdx, len(existing))
	}
	if len(merged) != len(existing)+len(imported) {
		t.Fatalf("len(merged) = %d, want %d", len(merged), len(existing)+len(imported))
	}

	// Input slices must not be mutated.
	if !reflect.DeepEqual(existing, existingSnapshot) {
		t.Fatal("existing slice was mutated")
	}
	if !reflect.DeepEqual(imported, importedSnapshot) {
		t.Fatal("imported slice was mutated")
	}

	// Existing presets stay intact at their original indices.
	for i := range existing {
		if !reflect.DeepEqual(merged[i], existing[i]) {
			t.Fatalf("existing[%d] changed: got %+v, want %+v", i, merged[i], existing[i])
		}
	}

	// Check specific renamed outputs.
	if merged[4].name != "均衡 (导入 2)" || merged[4].quality != 60 {
		t.Fatalf("merged[4] = %+v, want name '均衡 (导入 2)' quality 60", merged[4])
	}
	if merged[5].name != "全新预设" || merged[5].quality != 85 {
		t.Fatalf("merged[5] = %+v, want name '全新预设' quality 85", merged[5])
	}
	if merged[6].name != "全新预设 (导入)" || merged[6].quality != 88 {
		t.Fatalf("merged[6] = %+v, want name '全新预设 (导入)' quality 88", merged[6])
	}

	wantLong1 := strings.Repeat("超", 35) + " (导入)"
	wantLong2 := strings.Repeat("超", 33) + " (导入 2)"
	if merged[7].name != wantLong1 {
		t.Fatalf("merged[7].name = %q (%d runes), want %q", merged[7].name, utf8.RuneCountInString(merged[7].name), wantLong1)
	}
	if merged[8].name != wantLong2 {
		t.Fatalf("merged[8].name = %q (%d runes), want %q", merged[8].name, utf8.RuneCountInString(merged[8].name), wantLong2)
	}
	wantSpace := strings.Repeat("a", 34) + " (导入)"
	if merged[9].name != wantSpace {
		t.Fatalf("merged[9].name = %q, want %q", merged[9].name, wantSpace)
	}

	// Every merged preset must pass presetNameProblem and validatePresets.
	for i, preset := range merged {
		if runes := utf8.RuneCountInString(preset.name); runes > 40 {
			t.Fatalf("merged[%d].name exceeds 40 runes: %q (%d)", i, preset.name, runes)
		}
		if problem := presetNameProblem(preset.name); problem != "" {
			t.Fatalf("merged[%d].name failed presetNameProblem: %q (%s)", i, preset.name, problem)
		}
	}
	if err := validatePresets(merged, merged[0].name); err != nil {
		t.Fatalf("merged presets failed validatePresets: %v", err)
	}
}

func TestMergeImportedPresetsOverwrite(t *testing.T) {
	basePNG := compress.DefaultPNGOptions()
	existing := []compressionPreset{
		{name: "均衡", quality: 80, png: basePNG},
		{name: "网页 WebP", quality: 80, png: basePNG, outputFormat: compress.OutputFormatWebP},
		{name: "无损优化", quality: 80, png: basePNG, lossless: true},
	}
	existingSnapshot := append([]compressionPreset(nil), existing...)

	t.Run("replaces matching in place and appends new ones", func(t *testing.T) {
		imported := []compressionPreset{
			{name: "网页 WebP", quality: 72, png: basePNG, outputFormat: compress.OutputFormatWebP, maxDimension: 1600},
			{name: "电商主图", quality: 85, png: basePNG, targetSizeBytes: 200 << 10},
			{name: "均衡", quality: 76, png: basePNG},
		}

		merged, added, updated, firstIdx := mergeImportedPresets(existing, imported, true)
		if added != 1 || updated != 2 {
			t.Fatalf("counts = (added=%d, updated=%d), want (added=1, updated=2)", added, updated)
		}
		if firstIdx != 1 {
			t.Fatalf("firstImportedIndex = %d, want 1", firstIdx)
		}
		if len(merged) != 4 {
			t.Fatalf("len(merged) = %d, want 4", len(merged))
		}
		if merged[0].name != "均衡" || merged[0].quality != 76 {
			t.Fatalf("merged[0] = %+v, expected updated 均衡", merged[0])
		}
		if merged[1].name != "网页 WebP" || merged[1].quality != 72 || merged[1].maxDimension != 1600 {
			t.Fatalf("merged[1] = %+v, expected updated 网页 WebP in place", merged[1])
		}
		if merged[2].name != "无损优化" || !merged[2].lossless {
			t.Fatalf("merged[2] = %+v, expected untouched 无损优化", merged[2])
		}
		if merged[3].name != "电商主图" || merged[3].targetSizeBytes != 200<<10 {
			t.Fatalf("merged[3] = %+v, expected appended 电商主图", merged[3])
		}
		if !reflect.DeepEqual(existing, existingSnapshot) {
			t.Fatal("existing slice was mutated")
		}
		if err := validatePresets(merged, merged[0].name); err != nil {
			t.Fatalf("merged presets failed validatePresets: %v", err)
		}
	})

	t.Run("firstImportedIndex when first imported is newly appended", func(t *testing.T) {
		imported := []compressionPreset{
			{name: "新预设", quality: 88, png: basePNG},
			{name: "均衡", quality: 74, png: basePNG},
		}
		merged, added, updated, firstIdx := mergeImportedPresets(existing, imported, true)
		if added != 1 || updated != 1 || firstIdx != 3 {
			t.Fatalf("got (added=%d, updated=%d, firstIdx=%d), want (1, 1, 3)", added, updated, firstIdx)
		}
		if merged[firstIdx].name != "新预设" {
			t.Fatalf("merged[firstIdx] = %+v, want 新预设", merged[firstIdx])
		}
	})

	t.Run("empty imported returns independent copy and -1 index", func(t *testing.T) {
		merged, added, updated, firstIdx := mergeImportedPresets(existing, nil, true)
		if added != 0 || updated != 0 || firstIdx != -1 {
			t.Fatalf("got (added=%d, updated=%d, firstIdx=%d), want (0, 0, -1)", added, updated, firstIdx)
		}
		if !reflect.DeepEqual(merged, existing) {
			t.Fatalf("merged = %+v, want %+v", merged, existing)
		}
		merged[0].name = "mutated"
		if existing[0].name != "均衡" {
			t.Fatal("merged aliased existing slice")
		}
	})
}

func TestMergeImportedPresetsTrimsDraftNames(t *testing.T) {
	basePNG := compress.DefaultPNGOptions()
	existing := []compressionPreset{
		{name: "  均衡  ", quality: 80, png: basePNG},
		{name: "  均衡 (导入)  ", quality: 75, png: basePNG},
	}
	imported := []compressionPreset{
		{name: " 均衡 ", quality: 65, png: basePNG},
	}

	t.Run("non-overwrite deduplicates against untrimmed draft names", func(t *testing.T) {
		merged, added, updated, firstIdx := mergeImportedPresets(existing, imported, false)
		if added != 1 || updated != 0 || firstIdx != 2 {
			t.Fatalf("got (added=%d, updated=%d, firstIdx=%d), want (1, 0, 2)", added, updated, firstIdx)
		}
		if merged[2].name != "均衡 (导入 2)" {
			t.Fatalf("merged[2].name = %q, want %q", merged[2].name, "均衡 (导入 2)")
		}
		trimmed := make([]compressionPreset, len(merged))
		copy(trimmed, merged)
		for i := range trimmed {
			trimmed[i].name = strings.TrimSpace(trimmed[i].name)
		}
		if err := validatePresets(trimmed, trimmed[0].name); err != nil {
			t.Fatalf("trimmed merged presets failed validatePresets: %v", err)
		}
	})

	t.Run("overwrite matches untrimmed draft names and normalizes imported name", func(t *testing.T) {
		merged, added, updated, firstIdx := mergeImportedPresets(existing, imported, true)
		if added != 0 || updated != 1 || firstIdx != 0 {
			t.Fatalf("got (added=%d, updated=%d, firstIdx=%d), want (0, 1, 0)", added, updated, firstIdx)
		}
		if len(merged) != 2 {
			t.Fatalf("len(merged) = %d, want 2", len(merged))
		}
		if merged[0].name != "均衡" || merged[0].quality != 65 {
			t.Fatalf("merged[0] = %+v, want trimmed 均衡 quality 65", merged[0])
		}
		trimmed := make([]compressionPreset, len(merged))
		copy(trimmed, merged)
		for i := range trimmed {
			trimmed[i].name = strings.TrimSpace(trimmed[i].name)
		}
		if err := validatePresets(trimmed, trimmed[0].name); err != nil {
			t.Fatalf("trimmed merged presets failed validatePresets: %v", err)
		}
	})
}
