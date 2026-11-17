package main

import (
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
	name     string
	quality  float64
	png      compress.PNGOptions
	output   presetSaveOptions
	favorite bool
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

type presetEditor struct {
	presets       []compressionPreset
	selected      int
	active        int
	message       string
	confirmDelete bool
}

func defaultPresets() []compressionPreset {
	return slices.Clone(compressionPresets)
}

func (e *presetEditor) selectedPreset() *compressionPreset {
	if e == nil || e.selected < 0 || e.selected >= len(e.presets) {
		return nil
	}
	return &e.presets[e.selected]
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
		s.settingsOpen = false
		s.editor = nil
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
	s.editor = &presetEditor{
		presets: slices.Clone(s.presets), selected: active, active: active,
		message: s.settingsError,
	}
	if s.window == nil {
		return
	}
	s.settingsWindow = mygo.NewWindow(mygo.WindowOptions{
		Title: "设置 · Z Pic", Parent: s.window, Modal: true,
		Width: 724, Height: 700, MinWidth: 640, MinHeight: 600,
		UseContentSize: true, Content: ui.View(s.settingsView),
	})
	s.settingsWindow.OnClosed(func() {
		s.settingsWindow = nil
		s.settingsOpen, s.editor = false, nil
		if !s.closed {
			s.window.Update(func() {})
		}
	})
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
			s.applyScheme(name)
			return
		}
	}
}

func (s *application) applyScheme(name string) {
	for _, preset := range s.presets {
		if preset.name == name {
			changed := s.quality != preset.quality || s.png != preset.png
			s.scheme, s.quality, s.png = name, preset.quality, preset.png
			if changed {
				s.invalidateResult()
			}
			s.applyPresetOutput(preset.output)
			return
		}
	}
}

func (s *application) editQualityLevel(level int) {
	if s.busy || s.editor.selectedPreset() == nil || level < 0 || level >= len(imageQualityLevels) {
		return
	}
	quality := imageQualityLevels[level]
	preset := s.editor.selectedPreset()
	preset.quality, preset.png = quality.quality, quality.png
	s.editor.message = ""
}

func (s *application) addPreset() {
	if s.busy || s.editor == nil {
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
}

func (s *application) duplicatePreset() {
	if s.busy || s.editor == nil {
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
}

func (s *application) requestDeletePreset() {
	if s.busy || s.editor.selectedPreset() == nil || len(s.editor.presets) <= 1 {
		return
	}
	s.editor.confirmDelete = true
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
}

func (s *application) savePresetEdits() {
	if s.busy || s.editor == nil || s.editor.confirmDelete {
		return
	}
	presets := slices.Clone(s.editor.presets)
	for index := range presets {
		presets[index].name = strings.TrimSpace(presets[index].name)
	}
	selected := presets[s.editor.active].name
	if s.settingsFile == "" {
		path, err := settingsFilePath()
		if err != nil {
			s.editor.message = "无法保存设置：" + err.Error()
			return
		}
		s.settingsFile = path
	}
	if err := s.saveCurrentSettings(presets, selected, s.batchOutputDir, s.effectiveThemeMode()); err != nil {
		s.editor.message = "无法保存设置：" + err.Error()
		return
	}
	s.presets = presets
	if s.settingsError != "" {
		s.setMessage("压缩预设已保存。", false)
	}
	s.settingsError = ""
	s.applyScheme(selected)
	s.setSettingsOpen(false)
}

func (s *application) saveCurrentSettings(presets []compressionPreset, selected, batchOutputDir string, theme themeMode) error {
	return saveFullSettings(s.settingsFile, appSettings{
		presets:        presets,
		selected:       selected,
		batchOutputDir: usableBatchOutputDir(batchOutputDir),
		theme:          theme,
	})
}

// Capture all settings on the UI thread before launching compression.
func (s *application) compressionOptions() compress.Options {
	return compress.Options{Quality: int(s.quality), PNG: s.png}
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
