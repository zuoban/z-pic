package main

import (
	"fmt"
	"math"
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
}

var compressionPresets = []compressionPreset{
	{"体积优先", 60, compress.PNGOptions{MinQuality: 40, MaxQuality: 65, Colors: 128, Speed: 3}},
	{"均衡", 80, compress.DefaultPNGOptions()},
	{"画质优先", 92, compress.PNGOptions{MinQuality: 85, MaxQuality: 100, Colors: 256, Speed: 1}},
}

type presetEditor struct {
	presets       []compressionPreset
	selected      int
	active        int
	message       string
	confirmDelete bool
}

func (s *application) restoreSettings() {
	path, err := settingsFilePath()
	if err != nil {
		s.settingsError = "无法读取设置目录：" + err.Error()
		s.setMessage(s.settingsError, true)
		return
	}
	s.settingsFile = path
	presets, selected, err := loadSettings(path)
	if err != nil {
		s.settingsError = "读取压缩方案失败，暂用默认方案：" + err.Error()
		s.setMessage(s.settingsError, true)
		return
	}
	s.settingsError = ""
	s.presets = presets
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
		Width: 820, Height: 620, MinWidth: 760, MinHeight: 560,
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
				if err := saveSettings(s.settingsFile, s.presets, name); err != nil {
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
	if s.busy || s.editor == nil {
		return
	}
	s.editor.presets[s.editor.selected].quality = math.Round(min(100, max(1, quality)))
	s.editor.message = ""
}

func (s *application) editPNGOptions(options compress.PNGOptions) {
	if s.busy || s.editor == nil || options.Validate() != nil {
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

func (s *application) deletePreset() {
	if s.busy || s.editor == nil || !s.editor.confirmDelete || len(s.editor.presets) <= 1 {
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
	if err := saveSettings(s.settingsFile, presets, selected); err != nil {
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
