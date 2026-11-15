package main

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"zpic/internal/compress"
)

const validStoredSettings = `{"version":1,"selected":"均衡","presets":[{"name":"均衡","quality":80,"png":{"MinQuality":65,"MaxQuality":85,"Colors":256,"Speed":3}}]}`

func TestSettingsFilePathUsesOverrideOrUserDirectory(test *testing.T) {
	override := filepath.Join(test.TempDir(), "portable", "schemes.json")
	test.Setenv("ZPIC_CONFIG_FILE", override)
	path, err := settingsFilePath()
	if err != nil || path != override {
		test.Fatalf("configuration override was not used: path=%q err=%v", path, err)
	}
	test.Setenv("ZPIC_CONFIG_FILE", "")
	directory, err := os.UserConfigDir()
	if err != nil {
		test.Fatal(err)
	}
	path, err = settingsFilePath()
	if err != nil || path != filepath.Join(directory, "z-pic", "settings.json") {
		test.Fatalf("unexpected default configuration path: path=%q err=%v", path, err)
	}
}

func TestLoadSettingsMissingFileReturnsIndependentDefaults(test *testing.T) {
	path := filepath.Join(test.TempDir(), "missing", "settings.json")
	presets, selected, err := loadSettings(path)
	if err != nil || selected != "均衡" || !reflect.DeepEqual(presets, compressionPresets) {
		test.Fatalf("missing configuration must use defaults: presets=%+v selected=%q err=%v", presets, selected, err)
	}
	presets[0].name, presets[0].quality, presets[0].png.Colors = "changed", 1, 2
	again, _, err := loadSettings(path)
	if err != nil || !reflect.DeepEqual(again, compressionPresets) {
		test.Fatal("default presets must not share a mutable slice with loaded settings")
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		test.Fatalf("loading defaults must not create directories: %v", err)
	}
}

func TestSaveSettingsRoundTripAndReplaceMaintainedPresets(test *testing.T) {
	path := filepath.Join(test.TempDir(), "profile", "settings.json")
	presets := append([]compressionPreset(nil), compressionPresets...)
	presets = append(presets, compressionPreset{
		name: "论坛分享", quality: 73,
		png: compress.PNGOptions{MinQuality: 30, MaxQuality: 70, Colors: 96, Speed: 5},
	})
	if err := saveSettings(path, presets, "论坛分享"); err != nil {
		test.Fatal(err)
	}
	loaded, selected, err := loadSettings(path)
	if err != nil || selected != "论坛分享" || !reflect.DeepEqual(loaded, presets) {
		test.Fatalf("saved configuration did not round-trip: presets=%+v selected=%q err=%v", loaded, selected, err)
	}
	presets = presets[1:]
	presets[0].name, presets[0].quality = "日常", 82
	presets[0].png.MaxQuality = 88
	if err := saveSettings(path, presets, "日常"); err != nil {
		test.Fatal(err)
	}
	loaded, selected, err = loadSettings(path)
	if err != nil || selected != "日常" || !reflect.DeepEqual(loaded, presets) {
		test.Fatalf("renaming, editing and deletion must survive reload: presets=%+v selected=%q err=%v", loaded, selected, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		test.Fatal(err)
	}
	var encoded map[string]json.RawMessage
	if err := json.Unmarshal(data, &encoded); err != nil {
		test.Fatal(err)
	}
	if string(encoded["version"]) != "1" || len(encoded) != 3 {
		test.Fatalf("saved configuration must use the versioned schema: %s", data)
	}
	assertNoSettingsTemporaryFiles(test, filepath.Dir(path))
}

func TestValidatePresetsRejectsInvalidDefinitions(test *testing.T) {
	mutations := map[string]func(*compressionPreset){
		"empty-name":              func(preset *compressionPreset) { preset.name = "" },
		"blank-name":              func(preset *compressionPreset) { preset.name = " \t " },
		"untrimmed-name":          func(preset *compressionPreset) { preset.name = " 均衡" },
		"long-unicode-name":       func(preset *compressionPreset) { preset.name = strings.Repeat("图", 41) },
		"newline-name":            func(preset *compressionPreset) { preset.name = "均\n衡" },
		"control-name":            func(preset *compressionPreset) { preset.name = "均\x00衡" },
		"invalid-utf8-name":       func(preset *compressionPreset) { preset.name = string([]byte{0xff}) },
		"zero-quality":            func(preset *compressionPreset) { preset.quality = 0 },
		"too-high-quality":        func(preset *compressionPreset) { preset.quality = 101 },
		"fractional-quality":      func(preset *compressionPreset) { preset.quality = 80.5 },
		"nan-quality":             func(preset *compressionPreset) { preset.quality = math.NaN() },
		"infinite-quality":        func(preset *compressionPreset) { preset.quality = math.Inf(1) },
		"unspecified-png-options": func(preset *compressionPreset) { preset.png = compress.PNGOptions{} },
		"invalid-png-quality":     func(preset *compressionPreset) { preset.png.MaxQuality = 101 },
		"invalid-png-range":       func(preset *compressionPreset) { preset.png.MinQuality = 90 },
		"invalid-png-colors":      func(preset *compressionPreset) { preset.png.Colors = 1 },
		"invalid-png-speed":       func(preset *compressionPreset) { preset.png.Speed = 12 },
	}
	for name, mutate := range mutations {
		test.Run(name, func(test *testing.T) {
			preset := compressionPresets[1]
			mutate(&preset)
			if err := validatePresets([]compressionPreset{preset}, preset.name); err == nil {
				test.Fatal("invalid scheme was accepted")
			}
		})
	}
	for name, presets := range map[string][]compressionPreset{
		"empty-list":     nil,
		"duplicate-name": {compressionPresets[1], compressionPresets[1]},
	} {
		test.Run(name, func(test *testing.T) {
			if err := validatePresets(presets, "均衡"); err == nil {
				test.Fatal("invalid scheme list was accepted")
			}
		})
	}
	if err := validatePresets(compressionPresets, "不存在"); err == nil {
		test.Fatal("a selected scheme missing from the list was accepted")
	}
}

func TestValidatePresetsAcceptsBoundaryValuesAndUnicodeNames(test *testing.T) {
	presets := []compressionPreset{
		{name: strings.Repeat("图", 40), quality: 1, png: compress.PNGOptions{MinQuality: 0, MaxQuality: 1, Colors: 2, Speed: 1}},
		{name: "照片 / Print 📷", quality: 100, png: compress.PNGOptions{MinQuality: 100, MaxQuality: 100, Colors: 256, Speed: 11}},
	}
	if err := validatePresets(presets, presets[0].name); err != nil {
		test.Fatalf("valid boundary options and unicode names must be accepted: %v", err)
	}
}

func TestLoadSettingsRejectsInvalidFilesWithoutChangingThem(test *testing.T) {
	files := map[string]string{
		"empty":               "",
		"null":                "null",
		"missing-version":     strings.Replace(validStoredSettings, `"version":1,`, "", 1),
		"unsupported-version": strings.Replace(validStoredSettings, `"version":1`, `"version":2`, 1),
		"truncated":           validStoredSettings[:len(validStoredSettings)-1],
		"trailing-json":       validStoredSettings + "\n{}",
		"trailing-garbage":    validStoredSettings + "\ninvalid",
		"unknown-field":       strings.Replace(validStoredSettings, `"quality":80`, `"quality":80,"unknown":true`, 1),
		"oversized":           strings.Repeat(" ", maxSettingsBytes+1),
		"missing-selected":    strings.Replace(validStoredSettings, `"selected":"均衡",`, "", 1),
		"unknown-selected":    strings.Replace(validStoredSettings, `"selected":"均衡"`, `"selected":"missing"`, 1),
		"fractional-quality":  strings.Replace(validStoredSettings, `"quality":80`, `"quality":80.5`, 1),
		"invalid-png":         strings.Replace(validStoredSettings, `"Colors":256`, `"Colors":1`, 1),
		"missing-png":         strings.Replace(validStoredSettings, `"png":{"MinQuality":65,"MaxQuality":85,"Colors":256,"Speed":3}`, `"png":{}`, 1),
		"empty-presets":       `{"version":1,"selected":"均衡","presets":[]}`,
		"duplicate-name":      `{"version":1,"selected":"均衡","presets":[{"name":"均衡","quality":80,"png":{"MinQuality":65,"MaxQuality":85,"Colors":256,"Speed":3}},{"name":"均衡","quality":60,"png":{"MinQuality":40,"MaxQuality":65,"Colors":128,"Speed":3}}]}`,
	}
	for name, contents := range files {
		test.Run(name, func(test *testing.T) {
			path := filepath.Join(test.TempDir(), "settings.json")
			original := []byte(contents)
			if err := os.WriteFile(path, original, 0o600); err != nil {
				test.Fatal(err)
			}
			presets, selected, err := loadSettings(path)
			if err == nil || presets != nil || selected != "" {
				test.Fatalf("invalid settings must return an error without usable partial data: presets=%+v selected=%q err=%v", presets, selected, err)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(after, original) {
				test.Fatal("loading invalid settings must retain the original file")
			}
		})
	}
}

func TestSettingsRejectEmptyPathAndNonFileInput(test *testing.T) {
	if _, _, err := loadSettings(""); err == nil {
		test.Fatal("an empty settings path must be rejected")
	}
	if err := saveSettings("", compressionPresets, "均衡"); err == nil {
		test.Fatal("an empty save path must be rejected")
	}
	if _, _, err := loadSettings(test.TempDir()); err == nil {
		test.Fatal("a directory must be rejected as a settings file")
	}
}

func TestSaveSettingsValidationPreservesExistingFile(test *testing.T) {
	path := filepath.Join(test.TempDir(), "settings.json")
	original := []byte(validStoredSettings)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		test.Fatal(err)
	}
	presets := append([]compressionPreset(nil), compressionPresets...)
	presets[0].quality = 80.5
	if err := saveSettings(path, presets, "均衡"); err == nil {
		test.Fatal("invalid settings must not be saved")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, original) {
		test.Fatal("validation failure must preserve the existing configuration bytes")
	}
	assertNoSettingsTemporaryFiles(test, filepath.Dir(path))
}

func TestSaveSettingsOversizePreservesExistingFile(test *testing.T) {
	path := filepath.Join(test.TempDir(), "settings.json")
	original := []byte(validStoredSettings)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		test.Fatal(err)
	}
	presets := make([]compressionPreset, 7000)
	for index := range presets {
		presets[index] = compressionPresets[1]
		presets[index].name = "方案-" + strconv.Itoa(index)
	}
	if err := saveSettings(path, presets, presets[0].name); err == nil {
		test.Fatal("settings larger than the supported read limit must not be saved")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, original) {
		test.Fatal("oversize settings must preserve existing configuration")
	}
	assertNoSettingsTemporaryFiles(test, filepath.Dir(path))
}

func TestSaveSettingsFailedReplaceCleansTemporaryFile(test *testing.T) {
	directory := test.TempDir()
	path := filepath.Join(directory, "settings.json")
	if err := os.Mkdir(path, 0o700); err != nil {
		test.Fatal(err)
	}
	marker := filepath.Join(path, "previous-data")
	original := []byte("retain previous data")
	if err := os.WriteFile(marker, original, 0o600); err != nil {
		test.Fatal(err)
	}
	if err := saveSettings(path, compressionPresets, "均衡"); err == nil {
		test.Fatal("replacing a nonempty directory with a settings file must fail")
	}
	after, err := os.ReadFile(marker)
	if err != nil || !bytes.Equal(after, original) {
		test.Fatal("failed replacement must preserve existing data")
	}
	assertNoSettingsTemporaryFiles(test, directory)
}

func TestSaveSettingsPermissionFailurePreservesExistingFile(test *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		test.Skip("Unix permission enforcement requires an unprivileged account")
	}
	directory := test.TempDir()
	path := filepath.Join(directory, "settings.json")
	original := []byte(validStoredSettings)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		test.Fatal(err)
	}
	if err := os.Chmod(directory, 0o500); err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() {
		if err := os.Chmod(directory, 0o700); err != nil {
			test.Error(err)
		}
	})
	if err := saveSettings(path, compressionPresets, "画质优先"); err == nil {
		test.Fatal("saving into a directory without write permission must fail")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, original) {
		test.Fatal("a failed write must preserve existing configuration")
	}
	assertNoSettingsTemporaryFiles(test, directory)
}

func TestSaveSettingsCreatesPrivateFilesAndDirectories(test *testing.T) {
	if runtime.GOOS == "windows" {
		test.Skip("Unix file modes are not portable to Windows")
	}
	path := filepath.Join(test.TempDir(), "private", "nested", "settings.json")
	if err := saveSettings(path, compressionPresets, "均衡"); err != nil {
		test.Fatal(err)
	}
	for _, directory := range []string{filepath.Dir(path), filepath.Dir(filepath.Dir(path))} {
		info, err := os.Stat(directory)
		if err != nil || info.Mode().Perm() != 0o700 {
			test.Fatalf("new settings directories must be private: path=%q info=%v err=%v", directory, info, err)
		}
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		test.Fatalf("settings file must be private: info=%v err=%v", info, err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		test.Fatal(err)
	}
	if err := saveSettings(path, compressionPresets, "体积优先"); err != nil {
		test.Fatal(err)
	}
	info, err = os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		test.Fatalf("replacement settings file must retain private permissions: info=%v err=%v", info, err)
	}
}

func assertNoSettingsTemporaryFiles(test *testing.T, directory string) {
	test.Helper()
	matches, err := filepath.Glob(filepath.Join(directory, ".settings-*"))
	if err != nil || len(matches) != 0 {
		test.Fatalf("temporary settings files were not cleaned up: matches=%v err=%v", matches, err)
	}
}
