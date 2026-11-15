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
	name    string
	quality float64
	png     compress.PNGOptions
	builtin bool
}

var compressionPresets = []compressionPreset{
	{name: "体积优先", quality: 60, png: compress.PNGOptions{MinQuality: 40, MaxQuality: 65, Colors: 128, Speed: 3}, builtin: true},
	{name: "均衡", quality: 80, png: compress.DefaultPNGOptions(), builtin: true},
	{name: "画质优先", quality: 92, png: compress.PNGOptions{MinQuality: 85, MaxQuality: 100, Colors: 256, Speed: 1}, builtin: true},
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

func findBuiltinPreset(name string) (compressionPreset, bool) {
	for _, preset := range compressionPresets {
		if preset.name == name {
			return preset, true
		}
	}
	return compressionPreset{}, false
}

func (e *presetEditor) selectedPreset() *compressionPreset {
	if e == nil || e.selected < 0 || e.selected >= len(e.presets) {
		return nil
	}
	return &e.presets[e.selected]
}

func (e *presetEditor) selectedBuiltin() bool {
	preset := e.selectedPreset()
	return preset != nil && preset.builtin
}

func (s *application) restoreSettings() {
	path, err := settingsFilePath()
	if err != nil {
		s.settingsError = "无法读取设置目录：" + err.Error()
		s.setMessage(s.settingsError, true)
		return
	}
	s.settingsFile = path
	presets, selected, batchDir, err := loadAppSettings(path)
	if err != nil {
		s.settingsError = "读取压缩方案失败，暂用默认方案：" + err.Error()
		s.setMessage(s.settingsError, true)
		return
	}
	s.settingsError = ""
	s.presets = presets
	s.batchOutputDir = usableBatchOutputDir(batchDir)
	s.applyScheme(selected)
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
		Width: 820, Height: 650, MinWidth: 760, MinHeight: 560,
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
				if err := saveAppSettings(s.settingsFile, s.presets, name, usableBatchOutputDir(s.batchOutputDir)); err != nil {
					s.setMessage("保存方案选择失败："+err.Error(), true)
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
			return
		}
	}
}

func (s *application) editQuality(quality float64) {
	if s.busy || s.editor == nil || s.editor.selectedBuiltin() {
		return
	}
	s.editor.presets[s.editor.selected].quality = math.Round(min(100, max(1, quality)))
	s.editor.message = ""
}

func (s *application) editPNGOptions(options compress.PNGOptions) {
	if s.busy || s.editor == nil || s.editor.selectedBuiltin() || options.Validate() != nil {
		return
	}
	if options == (compress.PNGOptions{}) {
		options = compress.DefaultPNGOptions()
	}
	s.editor.presets[s.editor.selected].png = options
	s.editor.message = ""
}

func (s *application) addPreset() {
	if s.busy || s.editor == nil {
		return
	}
	editor := s.editor
	preset := compressionPresets[1]
	preset.builtin = false
	preset.name = "新方案"
	for number := 2; slices.ContainsFunc(editor.presets, func(existing compressionPreset) bool {
		return existing.name == preset.name
	}); number++ {
		preset.name = fmt.Sprintf("新方案 %d", number)
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
	preset.builtin = false
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
	if s.busy || s.editor == nil || s.editor.selectedBuiltin() || len(s.editor.presets) <= 1 {
		return
	}
	s.editor.confirmDelete = true
}

func (s *application) deletePreset() {
	if s.busy || s.editor == nil || !s.editor.confirmDelete || s.editor.selectedBuiltin() || len(s.editor.presets) <= 1 {
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
		if !presets[index].builtin {
			presets[index].name = strings.TrimSpace(presets[index].name)
		}
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
	if err := saveAppSettings(s.settingsFile, presets, selected, usableBatchOutputDir(s.batchOutputDir)); err != nil {
		s.editor.message = "无法保存设置：" + err.Error()
		return
	}
	s.presets = presets
	if s.settingsError != "" {
		s.setMessage("压缩方案已保存。", false)
	}
	s.settingsError = ""
	s.applyScheme(selected)
	s.setSettingsOpen(false)
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
	_ = saveAppSettings(s.settingsFile, s.presets, s.scheme, s.batchOutputDir)
}
