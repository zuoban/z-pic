package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"zpic/internal/compress"
)

const (
	presetBundleVersion = 1
	maxPresetNameRunes  = 40
)

type storedPresetBundle struct {
	Version int            `json:"version"`
	Presets []storedPreset `json:"presets"`
}

func presetToStored(preset compressionPreset) storedPreset {
	stored := storedPreset{
		Name:                     preset.name,
		Quality:                  preset.quality,
		PNG:                      preset.png,
		Lossless:                 preset.lossless,
		Metadata:                 preset.metadata,
		OutputFormat:             preset.outputFormat,
		JPEGBackground:           compress.NormalizeJPEGBackground(preset.jpegBackground),
		MaxDimension:             preset.maxDimension,
		MaxWidth:                 preset.maxWidth,
		MaxHeight:                preset.maxHeight,
		ScalePercent:             preset.scalePercent,
		TargetSizeBytes:          preset.targetSizeBytes,
		TargetResizeMinDimension: preset.targetResizeMinDimension,
	}
	if preset.output != (presetSaveOptions{}) {
		output := preset.output
		stored.Output = &output
	}
	return stored
}

func storedToPreset(stored storedPreset) compressionPreset {
	preset := compressionPreset{
		name:                     stored.Name,
		quality:                  stored.Quality,
		png:                      stored.PNG,
		lossless:                 stored.Lossless,
		metadata:                 stored.Metadata,
		outputFormat:             stored.OutputFormat,
		jpegBackground:           stored.JPEGBackground,
		maxDimension:             stored.MaxDimension,
		maxWidth:                 stored.MaxWidth,
		maxHeight:                stored.MaxHeight,
		scalePercent:             stored.ScalePercent,
		targetSizeBytes:          stored.TargetSizeBytes,
		targetResizeMinDimension: stored.TargetResizeMinDimension,
	}
	if stored.Output != nil {
		preset.output = *stored.Output
	}
	return preset
}

func marshalPresetBundle(presets []compressionPreset) ([]byte, error) {
	if len(presets) == 0 {
		return nil, errors.New("至少需要导出一个压缩预设")
	}
	if err := validatePresets(presets, presets[0].name); err != nil {
		return nil, err
	}
	bundle := storedPresetBundle{
		Version: presetBundleVersion,
		Presets: make([]storedPreset, len(presets)),
	}
	for i, preset := range presets {
		bundle.Presets[i] = presetToStored(preset)
	}
	data, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("无法编码预设文件：%w", err)
	}
	data = append(data, '\n')
	if len(data) > maxSettingsBytes {
		return nil, errors.New("预设内容超过 1 MiB 限制")
	}
	return data, nil
}

func decodeStrictPresetJSON(data []byte, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("预设文件格式无效：%w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("预设文件包含多余内容")
	}
	return nil
}

func unmarshalPresetBundle(data []byte) ([]compressionPreset, error) {
	if len(data) > maxSettingsBytes {
		return nil, errors.New("预设文件超过 1 MiB 限制")
	}
	if !validSettingsUnicode(data) {
		return nil, errors.New("预设文件包含无效的 Unicode 字符")
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, errors.New("预设文件格式无效：必须为 JSON 对象")
	}

	var (
		version       int
		storedPresets []storedPreset
	)

	var bundle storedPresetBundle
	if err := decodeStrictPresetJSON(data, &bundle); err == nil {
		version = bundle.Version
		storedPresets = bundle.Presets
	} else {
		var settings storedSettings
		if settingsErr := decodeStrictPresetJSON(data, &settings); settingsErr != nil {
			return nil, err
		}
		version = settings.Version
		storedPresets = settings.Presets
	}

	if err := uniqueSettingsFields(data); err != nil {
		return nil, err
	}
	if version != presetBundleVersion {
		return nil, fmt.Errorf("不支持的预设文件版本：%d", version)
	}
	if len(storedPresets) == 0 {
		return nil, errors.New("预设文件至少需要包含一个压缩预设")
	}

	presets := make([]compressionPreset, len(storedPresets))
	for i, preset := range storedPresets {
		presets[i] = storedToPreset(preset)
	}
	if err := validatePresets(presets, presets[0].name); err != nil {
		return nil, fmt.Errorf("预设文件内容无效：%w", err)
	}
	for i := range presets {
		presets[i].jpegBackground = compress.NormalizeJPEGBackground(presets[i].jpegBackground)
	}
	return presets, nil
}

func readPresetBundleFile(path string) ([]compressionPreset, error) {
	if path == "" {
		return nil, errors.New("预设文件路径不能为空")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("无法读取预设文件信息：%w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("预设文件必须是普通文件")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("无法打开预设文件：%w", err)
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil {
		return nil, fmt.Errorf("无法读取预设文件信息：%w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("预设文件必须是普通文件")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxSettingsBytes+1))
	if err != nil {
		return nil, fmt.Errorf("无法读取预设文件：%w", err)
	}
	return unmarshalPresetBundle(data)
}

func writePresetBundleFile(path string, presets []compressionPreset) error {
	if path == "" {
		return errors.New("预设文件路径不能为空")
	}
	data, err := marshalPresetBundle(presets)
	if err != nil {
		return err
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("无法创建预设目录：%w", err)
	}
	temporary, err := os.CreateTemp(directory, ".presets-*")
	if err != nil {
		return fmt.Errorf("无法创建临时预设文件：%w", err)
	}
	defer func() {
		temporary.Close()
		os.Remove(temporary.Name())
	}()
	if count, err := temporary.Write(data); err != nil {
		return fmt.Errorf("无法写入预设文件：%w", err)
	} else if count != len(data) {
		return fmt.Errorf("无法完整写入预设文件：%w", io.ErrShortWrite)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("无法同步预设文件：%w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("无法关闭预设文件：%w", err)
	}
	if err := os.Rename(temporary.Name(), path); err != nil {
		return fmt.Errorf("无法保存预设文件：%w", err)
	}
	return nil
}

func mergeImportedPresets(existing, imported []compressionPreset, overwrite bool) (merged []compressionPreset, added int, updated int, firstImportedIndex int) {
	merged = make([]compressionPreset, len(existing), len(existing)+len(imported))
	copy(merged, existing)
	firstImportedIndex = -1
	if len(imported) == 0 {
		return merged, 0, 0, -1
	}

	if overwrite {
		indexByName := make(map[string]int, len(existing)+len(imported))
		isExistingIndex := make(map[int]bool, len(existing))
		countedUpdated := make(map[int]bool)
		for i, preset := range merged {
			key := strings.TrimSpace(preset.name)
			if _, exists := indexByName[key]; !exists {
				indexByName[key] = i
			}
			isExistingIndex[i] = true
		}
		for _, preset := range imported {
			key := strings.TrimSpace(preset.name)
			preset.name = key
			if idx, exists := indexByName[key]; exists {
				merged[idx] = preset
				if isExistingIndex[idx] && !countedUpdated[idx] {
					updated++
					countedUpdated[idx] = true
				}
				if firstImportedIndex == -1 {
					firstImportedIndex = idx
				}
				continue
			}
			idx := len(merged)
			merged = append(merged, preset)
			indexByName[key] = idx
			added++
			if firstImportedIndex == -1 {
				firstImportedIndex = idx
			}
		}
		return merged, added, updated, firstImportedIndex
	}

	usedNames := make(map[string]bool, len(existing)+len(imported))
	for _, preset := range merged {
		usedNames[strings.TrimSpace(preset.name)] = true
	}
	for _, preset := range imported {
		key := strings.TrimSpace(preset.name)
		preset.name = key
		if usedNames[key] {
			preset.name = uniqueImportedPresetName(key, usedNames)
		}
		usedNames[strings.TrimSpace(preset.name)] = true
		if firstImportedIndex == -1 {
			firstImportedIndex = len(merged)
		}
		merged = append(merged, preset)
		added++
	}
	return merged, added, 0, firstImportedIndex
}

func uniqueImportedPresetName(base string, used map[string]bool) string {
	cleanBase := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(base))
	baseRunes := []rune(cleanBase)

	for attempt := 1; ; attempt++ {
		var suffix, fallback string
		if attempt == 1 {
			suffix = " (导入)"
			fallback = "导入"
		} else {
			suffix = fmt.Sprintf(" (导入 %d)", attempt)
			fallback = fmt.Sprintf("导入 %d", attempt)
		}
		maxBase := maxPresetNameRunes - utf8.RuneCountInString(suffix)
		candidate := fallback
		if maxBase > 0 && len(baseRunes) > 0 {
			prefixRunes := baseRunes
			if len(prefixRunes) > maxBase {
				prefixRunes = prefixRunes[:maxBase]
			}
			prefix := strings.TrimSpace(string(prefixRunes))
			if prefix != "" {
				candidate = prefix + suffix
			}
		}
		if !used[candidate] && presetNameProblem(candidate) == "" {
			return candidate
		}
	}
}
