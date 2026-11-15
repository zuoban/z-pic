package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"zpic/internal/compress"
)

const maxSettingsBytes = 1 << 20

type storedSettings struct {
	Version  int            `json:"version"`
	Selected string         `json:"selected"`
	Presets  []storedPreset `json:"presets"`
}

type storedPreset struct {
	Name    string              `json:"name"`
	Quality float64             `json:"quality"`
	PNG     compress.PNGOptions `json:"png"`
}

func settingsFilePath() (string, error) {
	if path := os.Getenv("ZPIC_CONFIG_FILE"); path != "" {
		return path, nil
	}
	directory, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("无法定位设置目录：%w", err)
	}
	return filepath.Join(directory, "z-pic", "settings.json"), nil
}

func validatePresets(presets []compressionPreset, selected string) error {
	if len(presets) == 0 {
		return errors.New("至少需要保留一个压缩方案")
	}
	names := make(map[string]bool, len(presets))
	for _, preset := range presets {
		if !utf8.ValidString(preset.name) || preset.name == "" ||
			preset.name != strings.TrimSpace(preset.name) || utf8.RuneCountInString(preset.name) > 40 {
			return errors.New("方案名称不能为空、首尾不能有空格，且不能超过 40 个字符")
		}
		for _, character := range preset.name {
			if unicode.IsControl(character) {
				return errors.New("方案名称不能包含换行或控制字符")
			}
		}
		if names[preset.name] {
			return fmt.Errorf("方案名称不能重复：%s", preset.name)
		}
		names[preset.name] = true
		if math.IsNaN(preset.quality) || math.IsInf(preset.quality, 0) ||
			preset.quality < 1 || preset.quality > 100 || math.Trunc(preset.quality) != preset.quality {
			return fmt.Errorf("方案「%s」的 JPEG 质量必须是 1 到 100 之间的整数", preset.name)
		}
		if preset.png == (compress.PNGOptions{}) {
			return fmt.Errorf("方案「%s」必须指定完整的 PNG 参数", preset.name)
		}
		if err := preset.png.Validate(); err != nil {
			return fmt.Errorf("方案「%s」的 PNG 参数无效：%w", preset.name, err)
		}
	}
	if !names[selected] {
		return errors.New("请选择一个有效的压缩方案")
	}
	return nil
}

func loadSettings(path string) ([]compressionPreset, string, error) {
	if path == "" {
		return nil, "", errors.New("设置文件路径不能为空")
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return append([]compressionPreset(nil), compressionPresets...), "均衡", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("无法读取设置文件信息：%w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, "", errors.New("设置文件必须是普通文件")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, "", fmt.Errorf("无法打开设置文件：%w", err)
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil {
		return nil, "", fmt.Errorf("无法读取设置文件信息：%w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, "", errors.New("设置文件必须是普通文件")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxSettingsBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("无法读取设置文件：%w", err)
	}
	if len(data) > maxSettingsBytes {
		return nil, "", errors.New("设置文件超过 1 MiB 限制")
	}
	var stored storedSettings
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&stored); err != nil {
		return nil, "", fmt.Errorf("设置文件格式无效：%w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, "", errors.New("设置文件包含多余内容")
	}
	if stored.Version != 1 {
		return nil, "", fmt.Errorf("不支持的设置文件版本：%d", stored.Version)
	}
	presets := make([]compressionPreset, len(stored.Presets))
	for index, preset := range stored.Presets {
		presets[index] = compressionPreset{name: preset.Name, quality: preset.Quality, png: preset.PNG}
	}
	if err := validatePresets(presets, stored.Selected); err != nil {
		return nil, "", fmt.Errorf("设置文件内容无效：%w", err)
	}
	return presets, stored.Selected, nil
}

func saveSettings(path string, presets []compressionPreset, selected string) error {
	if path == "" {
		return errors.New("设置文件路径不能为空")
	}
	if err := validatePresets(presets, selected); err != nil {
		return err
	}
	stored := storedSettings{Version: 1, Selected: selected, Presets: make([]storedPreset, len(presets))}
	for index, preset := range presets {
		stored.Presets[index] = storedPreset{Name: preset.name, Quality: preset.quality, PNG: preset.png}
	}
	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return fmt.Errorf("无法编码设置：%w", err)
	}
	data = append(data, '\n')
	if len(data) > maxSettingsBytes {
		return errors.New("设置内容超过 1 MiB 限制")
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("无法创建设置目录：%w", err)
	}
	temporary, err := os.CreateTemp(directory, ".settings-*")
	if err != nil {
		return fmt.Errorf("无法创建临时设置文件：%w", err)
	}
	defer func() {
		temporary.Close()
		os.Remove(temporary.Name())
	}()
	if count, err := temporary.Write(data); err != nil {
		return fmt.Errorf("无法写入设置：%w", err)
	} else if count != len(data) {
		return fmt.Errorf("无法完整写入设置：%w", io.ErrShortWrite)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("无法同步设置文件：%w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("无法关闭设置文件：%w", err)
	}
	if err := os.Rename(temporary.Name(), path); err != nil {
		return fmt.Errorf("无法替换设置文件：%w", err)
	}
	return nil
}
