package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"slices"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"zpic/internal/compress"
)

type compressionPreset struct {
	name         string
	quality      float64
	png          compress.PNGOptions
	lossless     bool
	metadata     compress.MetadataPolicy
	outputFormat compress.OutputFormat
	maxDimension int
	output       presetSaveOptions
}

var compressionPresets = []compressionPreset{
	{name: "均衡", quality: 80, png: compress.DefaultPNGOptions()},
}

type imageQualityLevel struct {
	label   string
	quality float64
	png     compress.PNGOptions
}

var imageQualityLevels = [...]imageQualityLevel{
	{label: "最小体积", quality: 40, png: compress.PNGOptions{MinQuality: 20, MaxQuality: 40, Colors: 64, Speed: 5}},
	{label: "体积优先", quality: 60, png: compress.PNGOptions{MinQuality: 40, MaxQuality: 65, Colors: 128, Speed: 3}},
	{label: "适中质量", quality: 70, png: compress.PNGOptions{MinQuality: 50, MaxQuality: 75, Colors: 192, Speed: 3}},
	{label: "均衡质量", quality: 80, png: compress.DefaultPNGOptions()},
	{label: "高质量", quality: 92, png: compress.PNGOptions{MinQuality: 85, MaxQuality: 100, Colors: 256, Speed: 1}},
	{label: "最高质量", quality: 100, png: compress.PNGOptions{MinQuality: 100, MaxQuality: 100, Colors: 256, Speed: 1}},
}

func qualityLevel(preset compressionPreset) int {
	closest, bestDistance := 0, math.Inf(1)
	for index, level := range imageQualityLevels {
		quality := (preset.quality - level.quality) / 100
		minimum := float64(preset.png.MinQuality-level.png.MinQuality) / 100
		maximum := float64(preset.png.MaxQuality-level.png.MaxQuality) / 100
		colors := float64(preset.png.Colors-level.png.Colors) / 254
		speed := float64(preset.png.Speed-level.png.Speed) / 10
		distance := quality*quality + minimum*minimum + maximum*maximum + colors*colors + speed*speed
		if distance < bestDistance {
			closest, bestDistance = index, distance
		}
	}
	return closest
}

type settingsSection int

const (
	settingsGeneral settingsSection = iota
	settingsPresets
)

type settingsEditor struct {
	section       settingsSection
	theme         themeMode
	autoCompress  bool
	presets       []compressionPreset
	selected      int
	active        int
	message       string
	confirmDelete bool
	focusField    string
	selectName    bool
}

func defaultPresets() []compressionPreset {
	return slices.Clone(compressionPresets)
}

func (e *settingsEditor) selectedPreset() *compressionPreset {
	if e == nil || e.selected < 0 || e.selected >= len(e.presets) {
		return nil
	}
	return &e.presets[e.selected]
}

func (s *application) settingsDraftLocked() bool {
	return s.busy || s.editor == nil || s.editor.confirmDelete
}

func (e *settingsEditor) nameProblem() string {
	preset := e.selectedPreset()
	if preset == nil {
		return ""
	}
	name := strings.TrimSpace(preset.name)
	if problem := presetNameProblem(name); problem != "" {
		return problem
	}
	for index, other := range e.presets {
		if index != e.selected && strings.TrimSpace(other.name) == name {
			return "预设名称已存在，请换一个名称"
		}
	}
	return ""
}

func (s *application) settingsHaveChanges() bool {
	e := s.editor
	return e != nil && (e.theme.normalized() != s.effectiveThemeMode() ||
		e.autoCompress != s.autoCompress ||
		!slices.Equal(e.presets, s.presets) || e.presets[e.active].name != s.scheme)
}

func (s *application) restoreSettings() {
	path, err := settingsFilePath()
	if err != nil {
		s.settingsError = "无法读取设置目录：" + err.Error()
		s.setMessage(s.settingsError, true)
		return
	}
	s.settingsFile = path
	cfg, err := loadFullSettings(path)
	if err != nil {
		s.syncTheme(themeSystem)
		s.settingsError = "读取压缩预设失败，暂用默认预设：" + err.Error()
		s.setMessage(s.settingsError, true)
		return
	}
	s.settingsError = ""
	s.presets = cfg.presets
	s.batchOutputDir = usableBatchOutputDir(cfg.batchOutputDir)
	s.autoCompress = cfg.autoCompress
	s.syncTheme(cfg.theme)
	s.applyScheme(cfg.selected)
}

func (s *application) setSettingsOpen(open bool) {
	if s.busy || s.confirmOverwrite || s.confirmBatchOverwrite {
		return
	}
	if !open {
		if s.settingsWindow != nil {
			s.settingsWindow.Close()
			return
		}
		s.clearSettingsDraft()
		return
	}
	if s.settingsOpen {
		if s.settingsWindow != nil {
			s.settingsWindow.Show()
			s.settingsWindow.Focus()
		}
		return
	}
	s.settingsOpen = true
	active := 0
	for index, preset := range s.presets {
		if preset.name == s.scheme {
			active = index
			break
		}
	}
	s.editor = &settingsEditor{
		theme:        s.effectiveThemeMode(),
		autoCompress: s.autoCompress,
		presets:      slices.Clone(s.presets), selected: active, active: active,
		message: s.settingsError,
	}
	if s.window == nil {
		return
	}
	s.settingsWindow = mygo.NewWindow(mygo.WindowOptions{
		Title: "设置 · Z Pic", Parent: s.window, Modal: true,
		Width: 724, Height: 700, MinWidth: 640, MinHeight: 600,
		Vibrancy:        mygo.VibrancyGlass,
		VibrancyTint:    windowVibrancyTintCSS,
		BackgroundColor: windowBackgroundCSS,
		UseContentSize:  true, Content: ui.View(s.settingsView),
	})
	s.settingsWindow.OnClosed(func() {
		s.settingsWindow = nil
		s.clearSettingsDraft()
		if !s.closed {
			s.window.Update(func() {})
		}
	})
}

func (s *application) clearSettingsDraft() {
	restoreTheme := s.editor != nil && s.editor.theme.normalized() != s.effectiveThemeMode()
	s.settingsOpen, s.editor = false, nil
	if restoreTheme {
		s.syncThemeAppearance(s.effectiveThemeMode())
	}
}

func (s *application) openPresetSettings() {
	if s.busy || s.confirmOverwrite || s.confirmBatchOverwrite {
		return
	}
	s.setSettingsOpen(true)
	if s.editor != nil && !s.editor.confirmDelete {
		s.editor.section = settingsPresets
		if s.settingsWindow != nil {
			s.settingsWindow.Invalidate()
		}
	}
}

func (s *application) selectSettingsPreset(index int) {
	if s.busy || s.editor == nil || s.editor.confirmDelete || index < 0 || index >= len(s.editor.presets) {
		return
	}
	s.editor.active, s.editor.selected = index, index
	s.editor.message = ""
}

func (s *application) setScheme(name string) {
	if s.busy || s.settingsOpen {
		return
	}
	for _, preset := range s.presets {
		if preset.name == name {
			if s.settingsFile != "" && s.settingsError == "" {
				if err := s.saveCurrentSettings(s.presets, name, s.batchOutputDir, s.effectiveThemeMode()); err != nil {
					s.setMessage("保存预设选择失败："+err.Error(), true)
					return
				}
			}
			paramsChanged := s.applyScheme(name)
			if paramsChanged && s.autoCompress && s.batch == nil && s.file != nil && !s.modalOpen() {
				s.startCompression()
			}
			return
		}
	}
}

func (s *application) applyScheme(name string) bool {
	for _, preset := range s.presets {
		if preset.name == name {
			changed := s.quality != preset.quality || s.png != preset.png ||
				s.lossless != preset.lossless || s.metadata != preset.metadata ||
				s.outputFormat != preset.outputFormat || s.maxDimension != preset.maxDimension
			formatChanged := s.outputFormat != preset.outputFormat
			s.scheme = name
			s.quality, s.png = preset.quality, preset.png
			s.lossless, s.metadata = preset.lossless, preset.metadata
			s.outputFormat, s.maxDimension = preset.outputFormat, preset.maxDimension
			if changed {
				s.invalidateResult()
			}
			if formatChanged && s.output == preset.output {
				s.configureBatchPresetOutput()
			}
			s.applyPresetOutput(preset.output)
			return changed
		}
	}
	return false
}

func (s *application) editQualityLevel(level int) {
	if s.settingsDraftLocked() || s.editor.selectedPreset() == nil || level < 0 || level >= len(imageQualityLevels) {
		return
	}
	quality := imageQualityLevels[level]
	preset := s.editor.selectedPreset()
	preset.quality, preset.png = quality.quality, quality.png
	s.editor.message = ""
}

func (s *application) addPreset() {
	if s.settingsDraftLocked() {
		return
	}
	editor := s.editor
	preset := compressionPresets[0]
	preset.name = "新预设"
	for number := 2; slices.ContainsFunc(editor.presets, func(existing compressionPreset) bool {
		return strings.TrimSpace(existing.name) == preset.name
	}); number++ {
		preset.name = fmt.Sprintf("新预设 %d", number)
	}
	editor.presets = append(editor.presets, preset)
	editor.selected = len(editor.presets) - 1
	editor.message, editor.confirmDelete = "", false
	editor.focusField, editor.selectName = "preset-name", true
}

func (s *application) duplicatePreset() {
	if s.settingsDraftLocked() {
		return
	}
	editor := s.editor
	source := editor.selectedPreset()
	if source == nil {
		return
	}
	preset := *source
	sourceName := []rune(strings.TrimSpace(source.name))
	for number := 1; ; number++ {
		suffix := " 副本"
		if number > 1 {
			suffix = fmt.Sprintf(" 副本 %d", number)
		}
		preset.name = string(sourceName[:min(len(sourceName), 40-len([]rune(suffix)))]) + suffix
		if !slices.ContainsFunc(editor.presets, func(existing compressionPreset) bool {
			return strings.TrimSpace(existing.name) == strings.TrimSpace(preset.name)
		}) {
			break
		}
	}
	editor.presets = append(editor.presets, preset)
	editor.selected = len(editor.presets) - 1
	editor.message, editor.confirmDelete = "", false
	editor.focusField, editor.selectName = "preset-name", true
}

func (s *application) requestDeletePreset() {
	if s.busy || s.editor.selectedPreset() == nil || len(s.editor.presets) <= 1 {
		return
	}
	s.editor.confirmDelete = true
	s.editor.focusField = "cancel-delete"
}

func (s *application) deletePreset() {
	if s.busy || s.editor.selectedPreset() == nil || !s.editor.confirmDelete || len(s.editor.presets) <= 1 {
		return
	}
	editor := s.editor
	index := editor.selected
	editor.presets = slices.Delete(editor.presets, index, index+1)
	if editor.active > index {
		editor.active--
	} else if editor.active == index {
		editor.active = min(index, len(editor.presets)-1)
	}
	editor.selected = min(index, len(editor.presets)-1)
	editor.message, editor.confirmDelete = "", false
	editor.focusField = "preset-name"
}

func (s *application) saveSettingsEdits() {
	if s.busy || s.editor == nil || s.editor.confirmDelete {
		return
	}
	presets := slices.Clone(s.editor.presets)
	for index := range presets {
		presets[index].name = strings.TrimSpace(presets[index].name)
	}
	selected := presets[s.editor.active].name
	if err := validatePresets(presets, selected); err != nil {
		s.editor.section = settingsPresets
		var invalid *presetValidationError
		if errors.As(err, &invalid) {
			s.editor.selected = invalid.index
			s.editor.focusField, s.editor.selectName = invalid.field, false
		}
		s.editor.message = "无法保存设置：" + err.Error()
		return
	}
	if s.settingsFile == "" {
		path, err := settingsFilePath()
		if err != nil {
			s.editor.message = "无法保存设置：" + err.Error()
			return
		}
		s.settingsFile = path
	}
	if err := s.saveSettingsSnapshot(presets, selected, s.batchOutputDir, s.editor.theme, s.editor.autoCompress); err != nil {
		s.editor.message = "无法保存设置：" + err.Error()
		return
	}
	s.presets = presets
	s.autoCompress = s.editor.autoCompress
	if s.settingsError != "" {
		s.setMessage("设置已保存。", false)
	}
	s.settingsError = ""
	s.applyScheme(selected)
	s.syncTheme(s.editor.theme)
	s.setSettingsOpen(false)
}

func (s *application) saveCurrentSettings(presets []compressionPreset, selected, batchOutputDir string, theme themeMode) error {
	return s.saveSettingsSnapshot(presets, selected, batchOutputDir, theme, s.autoCompress)
}

func (s *application) saveSettingsSnapshot(presets []compressionPreset, selected, batchOutputDir string, theme themeMode, autoCompress bool) error {
	return saveFullSettings(s.settingsFile, appSettings{
		presets:        presets,
		selected:       selected,
		batchOutputDir: usableBatchOutputDir(batchOutputDir),
		theme:          theme,
		autoCompress:   autoCompress,
	})
}

// Capture all settings on the UI thread before launching compression.
func (s *application) compressionOptions() compress.Options {
	return compress.Options{
		Quality:      int(s.quality),
		PNG:          s.png,
		Lossless:     s.lossless,
		Metadata:     s.metadata,
		OutputFormat: s.outputFormat,
		MaxDimension: s.maxDimension,
	}
}

func usableBatchOutputDir(path string) string {
	if path == "" {
		return ""
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return ""
	}
	return path
}

func (s *application) rememberBatchOutputDir(dir string) {
	usable := usableBatchOutputDir(dir)
	if usable == "" {
		return
	}
	s.batchOutputDir = usable
	if s.settingsFile == "" || s.settingsError != "" {
		return
	}
	_ = s.saveCurrentSettings(s.presets, s.scheme, s.batchOutputDir, s.effectiveThemeMode())
}
