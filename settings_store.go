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
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"zpic/internal/compress"
)

const maxSettingsBytes = 1 << 20

type storedSettings struct {
	Version        int            `json:"version"`
	Selected       string         `json:"selected"`
	Presets        []storedPreset `json:"presets"`
	BatchOutputDir string         `json:"batch_output_dir,omitempty"`
	Theme          string         `json:"theme,omitempty"`
	AutoCompress   bool           `json:"auto_compress,omitempty"`
	BackupToTrash  bool           `json:"backup_to_trash,omitempty"`
	DownloadDir    string         `json:"download_dir,omitempty"`
}

type appSettings struct {
	presets        []compressionPreset
	selected       string
	batchOutputDir string
	theme          themeMode
	autoCompress   bool
	backupToTrash  bool
	downloadDir    string
}

type storedPreset struct {
	Name            string                  `json:"name"`
	Quality         float64                 `json:"quality"`
	PNG             compress.PNGOptions     `json:"png"`
	Lossless        bool                    `json:"lossless,omitempty"`
	Metadata        compress.MetadataPolicy `json:"metadata,omitempty"`
	OutputFormat    compress.OutputFormat   `json:"output_format,omitempty"`
	MaxDimension    int                     `json:"max_dimension,omitempty"`
	TargetSizeBytes int64                   `json:"target_size_bytes,omitempty"`
	Output          *presetSaveOptions      `json:"output,omitempty"`
	Favorite        bool                    `json:"favorite,omitempty"`
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

// Keep validation authoritative while allowing the editor to reveal the control
// that needs attention. Error text also remains useful when loading JSON.
type presetValidationError struct {
	index int
	field string
	err   error
}

func (e *presetValidationError) Error() string { return e.err.Error() }
func (e *presetValidationError) Unwrap() error { return e.err }

func presetNameProblem(name string) string {
	if !utf8.ValidString(name) || name == "" ||
		name != strings.TrimSpace(name) || utf8.RuneCountInString(name) > 40 {
		return "预设名称不能为空、首尾不能有空格，且不能超过 40 个字符"
	}
	if strings.ContainsFunc(name, unicode.IsControl) {
		return "预设名称不能包含换行或控制字符"
	}
	return ""
}

func validatePresets(presets []compressionPreset, selected string) error {
	if len(presets) == 0 {
		return errors.New("至少需要保留一个压缩预设")
	}
	names := make(map[string]bool, len(presets))
	for index, preset := range presets {
		invalid := func(field string, err error) error {
			return &presetValidationError{index: index, field: field, err: err}
		}
		if problem := presetNameProblem(preset.name); problem != "" {
			return invalid("preset-name", errors.New(problem))
		}
		if names[preset.name] {
			return invalid("preset-name", fmt.Errorf("预设名称不能重复：%s", preset.name))
		}
		names[preset.name] = true
		if math.IsNaN(preset.quality) || math.IsInf(preset.quality, 0) ||
			preset.quality < 1 || preset.quality > 100 || math.Trunc(preset.quality) != preset.quality {
			return invalid("quality-level", fmt.Errorf("预设「%s」的有损质量必须是 1 到 100 之间的整数", preset.name))
		}
		if preset.png == (compress.PNGOptions{}) {
			return invalid("quality-level", fmt.Errorf("预设「%s」必须指定完整的 PNG 参数", preset.name))
		}
		if err := preset.png.Validate(); err != nil {
			return invalid("quality-level", fmt.Errorf("预设「%s」的 PNG 参数无效：%w", preset.name, err))
		}
		if err := preset.metadata.Validate(); err != nil {
			return invalid("preset-metadata", fmt.Errorf("预设「%s」的元数据策略无效：%w", preset.name, err))
		}
		if err := preset.outputFormat.Validate(); err != nil {
			return invalid("preset-output-format", fmt.Errorf("预设「%s」的输出格式无效：%w", preset.name, err))
		}
		if err := compress.ValidateMaxDimension(preset.maxDimension); err != nil {
			return invalid("preset-max-dimension", fmt.Errorf("预设「%s」的缩放尺寸无效：%w", preset.name, err))
		}
		if err := compress.ValidateTargetSizeBytes(preset.targetSizeBytes); err != nil {
			return invalid("preset-target-size", fmt.Errorf("预设「%s」的目标体积无效：%w", preset.name, err))
		}
		if field, err := preset.output.validateField(); err != nil {
			return invalid(field, fmt.Errorf("预设「%s」的保存设置无效：%w", preset.name, err))
		}
	}
	if !names[selected] {
		return errors.New("请选择一个有效的压缩预设")
	}
	return nil
}

func loadSettings(path string) ([]compressionPreset, string, error) {
	presets, selected, _, err := loadAppSettings(path)
	return presets, selected, err
}

func sanitizeStoredBatchOutputDir(dir string) string {
	if dir == "" || !utf8.ValidString(dir) {
		return ""
	}
	for _, character := range dir {
		if unicode.IsControl(character) {
			return ""
		}
	}
	return dir
}

func validateDownloadDir(dir string) error {
	if dir == "" {
		return nil
	}
	if !utf8.ValidString(dir) {
		return errors.New("下载目录包含无效的 UTF-8 字符")
	}
	if strings.ContainsFunc(dir, unicode.IsControl) {
		return errors.New("下载目录不能包含控制字符")
	}
	if !filepath.IsAbs(dir) {
		return errors.New("下载目录必须是绝对路径")
	}
	return nil
}

func normalizeDownloadDir(dir string) string {
	if dir == "" {
		return ""
	}
	return filepath.Clean(dir)
}

func loadAppSettings(path string) ([]compressionPreset, string, string, error) {
	cfg, err := loadFullSettings(path)
	if err != nil {
		return nil, "", "", err
	}
	return cfg.presets, cfg.selected, cfg.batchOutputDir, nil
}

func loadFullSettings(path string) (appSettings, error) {
	if path == "" {
		return appSettings{}, errors.New("设置文件路径不能为空")
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return appSettings{presets: defaultPresets(), selected: "均衡", theme: themeSystem}, nil
	}
	if err != nil {
		return appSettings{}, fmt.Errorf("无法读取设置文件信息：%w", err)
	}
	if !info.Mode().IsRegular() {
		return appSettings{}, errors.New("设置文件必须是普通文件")
	}
	file, err := os.Open(path)
	if err != nil {
		return appSettings{}, fmt.Errorf("无法打开设置文件：%w", err)
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil {
		return appSettings{}, fmt.Errorf("无法读取设置文件信息：%w", err)
	}
	if !info.Mode().IsRegular() {
		return appSettings{}, errors.New("设置文件必须是普通文件")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxSettingsBytes+1))
	if err != nil {
		return appSettings{}, fmt.Errorf("无法读取设置文件：%w", err)
	}
	if len(data) > maxSettingsBytes {
		return appSettings{}, errors.New("设置文件超过 1 MiB 限制")
	}
	if !validSettingsUnicode(data) {
		return appSettings{}, errors.New("设置文件包含无效的 Unicode 字符")
	}
	var stored storedSettings
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&stored); err != nil {
		return appSettings{}, fmt.Errorf("设置文件格式无效：%w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return appSettings{}, errors.New("设置文件包含多余内容")
	}
	if err := uniqueSettingsFields(data); err != nil {
		return appSettings{}, err
	}
	if stored.Version != 1 {
		return appSettings{}, fmt.Errorf("不支持的设置文件版本：%d", stored.Version)
	}
	mode := themeMode(stored.Theme)
	if err := mode.validate(); err != nil {
		return appSettings{}, fmt.Errorf("设置文件内容无效：%w", err)
	}
	if err := validateDownloadDir(stored.DownloadDir); err != nil {
		return appSettings{}, fmt.Errorf("设置文件内容无效：%w", err)
	}
	presets := make([]compressionPreset, len(stored.Presets))
	for index, preset := range stored.Presets {
		presets[index] = compressionPreset{
			name:            preset.Name,
			quality:         preset.Quality,
			png:             preset.PNG,
			lossless:        preset.Lossless,
			metadata:        preset.Metadata,
			outputFormat:    preset.OutputFormat,
			maxDimension:    preset.MaxDimension,
			targetSizeBytes: preset.TargetSizeBytes,
		}
		if preset.Output != nil {
			presets[index].output = *preset.Output
		}
	}
	if err := validatePresets(presets, stored.Selected); err != nil {
		return appSettings{}, fmt.Errorf("设置文件内容无效：%w", err)
	}
	return appSettings{
		presets:        presets,
		selected:       stored.Selected,
		batchOutputDir: sanitizeStoredBatchOutputDir(stored.BatchOutputDir),
		theme:          mode.normalized(),
		autoCompress:   stored.AutoCompress,
		backupToTrash:  stored.BackupToTrash,
		downloadDir:    normalizeDownloadDir(stored.DownloadDir),
	}, nil
}

// Run after schema/syntax decoding: encoding/json accepts duplicate fields and
// matches struct fields using Unicode simple folding. Check each object
// separately, including escaped keys and nested PNG/output options.
func uniqueSettingsFields(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var value func() error
	value = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, compound := token.(json.Delim)
		if !compound {
			return nil
		}
		switch delimiter {
		case '{':
			seen := make(map[string]bool)
			for decoder.More() {
				token, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := token.(string)
				if !ok {
					return errors.New("设置文件对象字段无效")
				}
				folded := strings.Map(func(character rune) rune {
					smallest := character
					for next := unicode.SimpleFold(character); next != character; next = unicode.SimpleFold(next) {
						smallest = min(smallest, next)
					}
					return smallest
				}, key)
				if seen[folded] {
					return fmt.Errorf("设置文件包含重复字段：%s", key)
				}
				seen[folded] = true
				if err := value(); err != nil {
					return err
				}
			}
		case '[':
			for decoder.More() {
				if err := value(); err != nil {
					return err
				}
			}
		default:
			return errors.New("设置文件结构无效")
		}
		_, err = decoder.Token()
		return err
	}
	return value()
}

// encoding/json replaces invalid UTF-8 and unpaired UTF-16 escapes. Check both
// before decoding so names and output paths cannot silently change. The JSON
// decoder remains responsible for syntax and schema validation.
func validSettingsUnicode(data []byte) bool {
	if !utf8.Valid(data) {
		return false
	}
	for index := 0; index < len(data); index++ {
		if data[index] != '\\' {
			continue
		}
		index++
		if index >= len(data) {
			return false
		}
		// Consuming a complete escape also skips escaped backslashes: the
		// literal text "\\ud800" must not be mistaken for a surrogate.
		if data[index] != 'u' {
			continue
		}
		if index+5 > len(data) {
			return false
		}
		unit, err := strconv.ParseUint(string(data[index+1:index+5]), 16, 16)
		if err != nil {
			return false
		}
		index += 4
		if unit < 0xd800 || unit > 0xdfff {
			continue
		}
		if unit > 0xdbff || index+7 > len(data) || string(data[index+1:index+3]) != `\u` {
			return false
		}
		low, err := strconv.ParseUint(string(data[index+3:index+7]), 16, 16)
		if err != nil || low < 0xdc00 || low > 0xdfff {
			return false
		}
		index += 6
	}
	return true
}

func saveSettings(path string, presets []compressionPreset, selected string) error {
	return saveAppSettings(path, presets, selected, "")
}

func saveAppSettings(path string, presets []compressionPreset, selected, batchOutputDir string) error {
	return saveFullSettings(path, appSettings{
		presets:        presets,
		selected:       selected,
		batchOutputDir: batchOutputDir,
		theme:          themeSystem,
	})
}

func saveFullSettings(path string, cfg appSettings) error {
	if path == "" {
		return errors.New("设置文件路径不能为空")
	}
	if err := validatePresets(cfg.presets, cfg.selected); err != nil {
		return err
	}
	if err := cfg.theme.validate(); err != nil {
		return err
	}
	if err := validateDownloadDir(cfg.downloadDir); err != nil {
		return err
	}
	batchOutputDir := sanitizeStoredBatchOutputDir(cfg.batchOutputDir)
	stored := storedSettings{
		Version:        1,
		Selected:       cfg.selected,
		Presets:        make([]storedPreset, len(cfg.presets)),
		BatchOutputDir: batchOutputDir,
		Theme:          cfg.theme.storedValue(),
		AutoCompress:   cfg.autoCompress,
		BackupToTrash:  cfg.backupToTrash,
		DownloadDir:    normalizeDownloadDir(cfg.downloadDir),
	}
	for index, preset := range cfg.presets {
		stored.Presets[index] = storedPreset{
			Name:            preset.name,
			Quality:         preset.quality,
			PNG:             preset.png,
			Lossless:        preset.lossless,
			Metadata:        preset.metadata,
			OutputFormat:    preset.outputFormat,
			MaxDimension:    preset.maxDimension,
			TargetSizeBytes: preset.targetSizeBytes,
		}
		if preset.output != (presetSaveOptions{}) {
			output := preset.output
			stored.Presets[index].Output = &output
		}
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
