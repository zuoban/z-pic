package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"zpic/internal/compress"
)

type compressionPreset struct {
	name                     string
	quality                  float64
	png                      compress.PNGOptions
	lossless                 bool
	metadata                 compress.MetadataPolicy
	outputFormat             compress.OutputFormat
	jpegBackground           string
	maxDimension             int
	maxWidth                 int
	maxHeight                int
	scalePercent             int
	targetSizeBytes          int64
	targetResizeMinDimension int
	output                   presetSaveOptions
}

func (p compressionPreset) compressionOptions() compress.Options {
	return compress.Options{
		Quality:                  int(p.quality),
		PNG:                      p.png,
		Lossless:                 p.lossless,
		Metadata:                 p.metadata,
		OutputFormat:             p.outputFormat,
		JPEGBackground:           p.jpegBackground,
		MaxDimension:             p.maxDimension,
		MaxWidth:                 p.maxWidth,
		MaxHeight:                p.maxHeight,
		ScalePercent:             p.scalePercent,
		TargetSizeBytes:          p.targetSizeBytes,
		TargetResizeMinDimension: p.targetResizeMinDimension,
	}
}

type resizeLimits struct {
	maxDimension int
	maxWidth     int
	maxHeight    int
	scalePercent int
}

const (
	resizeNone = iota
	resizeLongEdge
	resizeWidth
	resizeHeight
	resizePercent

	presetResizeModeField  = "preset-resize-mode"
	presetResizeValueField = "preset-resize-value"
)

func (r resizeLimits) selection() (mode, value int) {
	switch {
	case r.maxDimension != 0:
		return resizeLongEdge, r.maxDimension
	case r.maxWidth != 0:
		return resizeWidth, r.maxWidth
	case r.maxHeight != 0:
		return resizeHeight, r.maxHeight
	case r.scalePercent != 0:
		return resizePercent, r.scalePercent
	default:
		return resizeNone, 0
	}
}

func (r resizeLimits) validate() error {
	return compress.ValidateResizeOptions(r.maxDimension, r.maxWidth, r.maxHeight, r.scalePercent)
}

func (p compressionPreset) resizeLimits() resizeLimits {
	return resizeLimits{p.maxDimension, p.maxWidth, p.maxHeight, p.scalePercent}
}

func (p *compressionPreset) setResizeLimits(limits resizeLimits) {
	p.maxDimension, p.maxWidth, p.maxHeight, p.scalePercent = limits.maxDimension, limits.maxWidth, limits.maxHeight, limits.scalePercent
}

func resizeLimitsFor(mode, value int) resizeLimits {
	switch mode {
	case resizeLongEdge:
		return resizeLimits{maxDimension: value}
	case resizeWidth:
		return resizeLimits{maxWidth: value}
	case resizeHeight:
		return resizeLimits{maxHeight: value}
	case resizePercent:
		return resizeLimits{scalePercent: value}
	default:
		return resizeLimits{}
	}
}

func parseResizeValue(mode int, text string) (resizeLimits, error) {
	value, err := strconv.Atoi(strings.TrimSpace(text))
	limits := resizeLimitsFor(mode, value)
	if err == nil && limits != (resizeLimits{}) && limits.validate() == nil {
		return limits, nil
	}
	if mode == resizePercent {
		return resizeLimits{}, errors.New("缩放比例须为 1–100 之间的整数")
	}
	return resizeLimits{}, fmt.Errorf("尺寸上限须为 %d–%d px 的整数", compress.MinResizeDimension, compress.MaxResizeDimension)
}

func (r resizeLimits) text() string {
	if _, value := r.selection(); value != 0 {
		return strconv.Itoa(value)
	}
	return ""
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
	settingsFolderWatch
	settingsIntegrations
	settingsAbout
)

type settingsEditor struct {
	section              settingsSection
	theme                themeMode
	autoCompress         bool
	backupToTrash        bool
	keepInMenuBar        bool
	openAtLogin          bool
	openAtLoginInitial   bool
	openAtLoginSupported bool
	downloadDir          string
	watch                folderWatchSettings
	watchRules           []folderWatchRule
	watchRuleIDs         []uint64
	watchMessage         string
	cliStatus            cliInstallStatus
	cliMessage           string
	cliFailed            bool
	quickActionStatus    quickActionStatus
	quickActionMessage   string
	quickActionFailed    bool
	quickActionBusy      bool
	presets              []compressionPreset
	targets              []*presetTargetDraft
	selected             int
	active               int
	message              string
	notice               string
	confirmDelete        bool
	focusField           string
	selectName           bool
}

func defaultPresets() []compressionPreset {
	presets := make([]compressionPreset, 0, 5)
	presets = append(presets,
		compressionPreset{
			name:    "均衡",
			quality: 80,
			png:     compress.DefaultPNGOptions(),
		},
		compressionPreset{
			name:         "网页 WebP",
			quality:      80,
			png:          compress.DefaultPNGOptions(),
			outputFormat: compress.OutputFormatWebP,
			metadata:     compress.MetadataKeepICC,
		},
	)
	if compress.SupportsAVIF() {
		presets = append(presets, compressionPreset{
			name:         "极致 AVIF",
			quality:      imageQualityLevels[2].quality,
			png:          imageQualityLevels[2].png,
			outputFormat: compress.OutputFormatAVIF,
			metadata:     compress.MetadataKeepICC,
		})
	}
	presets = append(presets,
		compressionPreset{
			name:     "无损优化",
			quality:  80,
			png:      compress.DefaultPNGOptions(),
			lossless: true,
		},
		compressionPreset{
			name:            "≤ 500 KiB",
			quality:         80,
			png:             compress.DefaultPNGOptions(),
			metadata:        compress.MetadataKeepICC,
			targetSizeBytes: 500 << 10,
		},
	)
	return presets
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

func presetTargetSizeText(size int64) string {
	text := compress.FormatTargetSize(size)
	if parsed, _ := compress.ParseTargetSize(text); parsed == size {
		return text
	}
	return fmt.Sprintf("%d B", size)
}

const (
	defaultPresetTargetResizeMinDimension = 640
	presetTargetResizeMinField            = "preset-target-resize-min"
)

type presetTargetDraft struct {
	resizeText    string
	sizeText      string
	resizeMinText string
	jpegBgText    string
}

func newPresetTargetDraft(preset compressionPreset) *presetTargetDraft {
	floor := preset.targetResizeMinDimension
	if floor == 0 {
		floor = defaultPresetTargetResizeMinDimension
	}
	jpegBg := compress.NormalizeJPEGBackground(preset.jpegBackground)
	if jpegBg == "" {
		jpegBg = "#ffffff"
	}
	return &presetTargetDraft{
		resizeText:    preset.resizeLimits().text(),
		sizeText:      presetTargetSizeText(preset.targetSizeBytes),
		resizeMinText: strconv.Itoa(floor),
		jpegBgText:    jpegBg,
	}
}

func parseTargetResizeMinDimension(text string) (int, error) {
	floor, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil || floor == 0 || compress.ValidateMaxDimension(floor) != nil {
		return 0, fmt.Errorf("长边下限须为 %d–%d px 的整数", compress.MinResizeDimension, compress.MaxResizeDimension)
	}
	return floor, nil
}

func (p compressionPreset) effectiveTargetResizeMinDimension() int {
	if p.lossless || p.targetSizeBytes == 0 {
		return 0
	}
	return p.targetResizeMinDimension
}

func (s *application) settingsHaveChanges() bool {
	e := s.editor
	if e == nil {
		return false
	}
	if e.theme.normalized() != s.effectiveThemeMode() ||
		e.autoCompress != s.autoCompress ||
		e.backupToTrash != s.backupToTrash ||
		e.keepInMenuBar != s.keepInMenuBar ||
		(e.openAtLoginSupported && e.openAtLogin != e.openAtLoginInitial) ||
		normalizeDownloadDir(e.downloadDir) != normalizeDownloadDir(s.downloadDir) ||
		e.watch != s.watch.settings ||
		!slices.Equal(e.watchRules, s.watch.extraRules) ||
		!slices.Equal(e.presets, s.presets) || e.presets[e.active].name != s.scheme {
		return true
	}
	for index, target := range e.targets {
		preset := s.presets[index]
		expectedBg := compress.NormalizeJPEGBackground(preset.jpegBackground)
		if expectedBg == "" {
			expectedBg = "#ffffff"
		}
		if target.resizeText != preset.resizeLimits().text() ||
			target.sizeText != presetTargetSizeText(preset.targetSizeBytes) ||
			(preset.targetResizeMinDimension != 0 && target.resizeMinText != strconv.Itoa(preset.targetResizeMinDimension)) ||
			(preset.outputFormat == compress.OutputFormatJPEG && strings.ToLower(strings.TrimSpace(target.jpegBgText)) != expectedBg) {
			return true
		}
	}
	return false
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
	s.backupToTrash = cfg.backupToTrash
	s.keepInMenuBar = cfg.keepInMenuBar
	s.downloadDir = normalizeDownloadDir(cfg.downloadDir)
	s.watch.settings = cfg.watch
	s.watch.extraRules = slices.Clone(cfg.watchRules)
	s.watch.extraRuleIDs = nil
	s.ensureWatchRuleIDs()
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
	targets := make([]*presetTargetDraft, len(s.presets))
	for index, preset := range s.presets {
		targets[index] = newPresetTargetDraft(preset)
		if preset.name == s.scheme {
			active = index
		}
	}
	s.ensureWatchRuleIDs()
	watch := s.watch.settings
	if session := s.watch.session; session != nil {
		watch = session.settings
	}
	watchRules := slices.Clone(s.watch.extraRules)
	watchRuleIDs := slices.Clone(s.watch.extraRuleIDs[:len(watchRules)])
	seenRuleIDs := make(map[uint64]bool, len(watchRuleIDs))
	for i, id := range watchRuleIDs {
		seenRuleIDs[id] = true
		if session := s.watchSessionForRuleID(id); session != nil {
			watchRules[i] = session.rule
		}
	}
	if len(s.watch.ruleSessions) > 0 {
		extraIDs := make([]uint64, 0, len(s.watch.ruleSessions))
		for id, session := range s.watch.ruleSessions {
			if session != nil && !seenRuleIDs[id] {
				extraIDs = append(extraIDs, id)
			}
		}
		slices.Sort(extraIDs)
		for _, id := range extraIDs {
			watchRules = append(watchRules, s.watch.ruleSessions[id].rule)
			watchRuleIDs = append(watchRuleIDs, id)
		}
	}
	openAtLoginSupported := s.effectiveOpenAtLoginSupported()
	openAtLogin := false
	if openAtLoginSupported {
		openAtLogin = s.effectiveOpenAtLogin()
	}
	s.editor = &settingsEditor{
		theme:                s.effectiveThemeMode(),
		autoCompress:         s.autoCompress,
		backupToTrash:        s.backupToTrash,
		keepInMenuBar:        s.keepInMenuBar,
		openAtLogin:          openAtLogin,
		openAtLoginInitial:   openAtLogin,
		openAtLoginSupported: openAtLoginSupported,
		downloadDir:          s.downloadDir,
		watch:                watch,
		watchRules:           watchRules,
		watchRuleIDs:         watchRuleIDs,
		cliStatus:            inspectCLIInstall(s.effectiveCLIInstallEnv()),
		quickActionStatus:    inspectQuickAction(s.effectiveQuickActionEnv()),
		presets:              slices.Clone(s.presets),
		targets:              targets,
		selected:             active,
		active:               active,
		message:              s.settingsError,
	}
	if s.window == nil {
		return
	}
	s.settingsWindow = mygo.NewWindow(mygo.WindowOptions{
		Title: "设置 · Z Pic", Parent: s.window, Modal: true,
		Width: 780, Height: 680, MinWidth: 640, MinHeight: 600,
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

func (s *application) activeSavedPreset() (compressionPreset, bool) {
	for _, preset := range s.presets {
		if preset.name == s.scheme {
			return preset, true
		}
	}
	return compressionPreset{}, false
}

func (s *application) hasQuickOverrides() bool {
	saved, ok := s.activeSavedPreset()
	if !ok {
		return false
	}
	return s.quality != saved.quality ||
		s.png != saved.png ||
		s.lossless != saved.lossless ||
		s.metadata != saved.metadata ||
		s.outputFormat != saved.outputFormat ||
		s.jpegBackground != saved.jpegBackground ||
		s.resizeLimits != saved.resizeLimits() ||
		s.targetSizeBytes != saved.targetSizeBytes ||
		s.targetResizeMinDimension != saved.targetResizeMinDimension
}

func (s *application) applyQuickAdjustment(formatChanged bool) {
	s.invalidateResult()
	if formatChanged {
		s.configureBatchPresetOutput()
	}
	if s.autoCompress && s.batch == nil && s.file != nil && !s.modalOpen() {
		s.startCompression()
	}
}

func (s *application) quickSetQualityLevel(level int) {
	if s.modalOpen() || level < 0 || level >= len(imageQualityLevels) {
		return
	}
	target := imageQualityLevels[level]
	if !s.lossless && s.quality == target.quality && s.png == target.png {
		return
	}
	wasLossless := s.lossless
	s.lossless = false
	s.quality, s.png = target.quality, target.png
	s.applyQuickAdjustment(wasLossless)
}

func (s *application) quickSetLossless() {
	if s.modalOpen() || s.lossless {
		return
	}
	s.lossless = true
	s.applyQuickAdjustment(true)
}

func (s *application) quickSetOutputFormat(format compress.OutputFormat) {
	if s.modalOpen() || format.Validate() != nil || s.outputFormat == format {
		return
	}
	if format == compress.OutputFormatAVIF && !compress.SupportsAVIF() {
		return
	}
	s.outputFormat = format
	s.applyQuickAdjustment(true)
}

func (s *application) quickSetMaxDimension(dim int) {
	s.quickSetResize(resizeLimits{maxDimension: dim})
}

func (s *application) quickSetResize(limits resizeLimits) {
	if s.modalOpen() || limits.validate() != nil || s.resizeLimits == limits {
		return
	}
	s.resizeLimits = limits
	s.applyQuickAdjustment(false)
}

func (s *application) resetQuickOverrides() {
	if s.modalOpen() || !s.hasQuickOverrides() {
		return
	}
	paramsChanged := s.applyScheme(s.scheme)
	if paramsChanged && s.autoCompress && s.batch == nil && s.file != nil && !s.modalOpen() {
		s.startCompression()
	}
}

func (s *application) openSaveQuickPreset() {
	if s.busy || s.confirmOverwrite || s.confirmBatchOverwrite {
		return
	}
	snapshot := s.activePresetSnapshot()
	s.setSettingsOpen(true)
	if s.editor == nil || s.editor.confirmDelete {
		return
	}
	editor := s.editor
	editor.section = settingsPresets
	baseName := []rune(strings.TrimSpace(snapshot.name))
	if len(baseName) == 0 {
		baseName = []rune("自定义")
	}
	for number := 1; ; number++ {
		suffix := " 微调"
		if number > 1 {
			suffix = fmt.Sprintf(" 微调 %d", number)
		}
		snapshot.name = string(baseName[:min(len(baseName), 40-len([]rune(suffix)))]) + suffix
		if !slices.ContainsFunc(editor.presets, func(existing compressionPreset) bool {
			return strings.TrimSpace(existing.name) == strings.TrimSpace(snapshot.name)
		}) {
			break
		}
	}
	editor.presets = append(editor.presets, snapshot)
	editor.targets = append(editor.targets, newPresetTargetDraft(snapshot))
	editor.selected = len(editor.presets) - 1
	editor.active = editor.selected
	editor.message, editor.confirmDelete = "", false
	editor.focusField, editor.selectName = "preset-name", true
	if s.settingsWindow != nil {
		s.settingsWindow.Invalidate()
	}
}

func (s *application) setScheme(name string) {
	if s.busy || s.settingsOpen {
		return
	}
	for _, preset := range s.presets {
		if preset.name == name {
			if name != s.scheme && s.settingsFile != "" && s.settingsError == "" {
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
				s.outputFormat != preset.outputFormat ||
				s.jpegBackground != preset.jpegBackground ||
				s.resizeLimits != preset.resizeLimits() ||
				s.targetSizeBytes != preset.targetSizeBytes ||
				s.activePresetSnapshot().effectiveTargetResizeMinDimension() != preset.effectiveTargetResizeMinDimension()
			formatChanged := s.outputFormat != preset.outputFormat || s.lossless != preset.lossless
			s.scheme = name
			s.quality, s.png = preset.quality, preset.png
			s.lossless, s.metadata = preset.lossless, preset.metadata
			s.outputFormat = preset.outputFormat
			s.jpegBackground = preset.jpegBackground
			s.resizeLimits = preset.resizeLimits()
			s.targetSizeBytes = preset.targetSizeBytes
			s.targetResizeMinDimension = preset.targetResizeMinDimension
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
	editor.targets = append(editor.targets, newPresetTargetDraft(preset))
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
	draft := *editor.targets[editor.selected]
	editor.targets = append(editor.targets, &draft)
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
	editor.targets = slices.Delete(editor.targets, index, index+1)
	if editor.active > index {
		editor.active--
	} else if editor.active == index {
		editor.active = min(index, len(editor.presets)-1)
	}
	editor.selected = min(index, len(editor.presets)-1)
	editor.message, editor.confirmDelete = "", false
	editor.focusField = "preset-name"
}

func (s *application) showPresetValidationError(prefix string, err error) {
	s.editor.section = settingsPresets
	var invalid *presetValidationError
	if errors.As(err, &invalid) {
		s.editor.selected = invalid.index
		s.editor.focusField, s.editor.selectName = invalid.field, false
	}
	s.editor.message = prefix + err.Error()
	s.editor.notice = ""
}

func (e *settingsEditor) validatedPresets() ([]compressionPreset, string, error) {
	presets := slices.Clone(e.presets)
	for index := range presets {
		preset := &presets[index]
		preset.name = strings.TrimSpace(preset.name)
		if index >= len(e.targets) || e.targets[index] == nil {
			preset.jpegBackground = compress.NormalizeJPEGBackground(preset.jpegBackground)
			continue
		}
		target := e.targets[index]
		if mode, _ := preset.resizeLimits().selection(); mode != resizeNone {
			limits, err := parseResizeValue(mode, target.resizeText)
			if err != nil {
				return nil, "", &presetValidationError{index: index, field: presetResizeValueField, err: err}
			}
			if preset.resizeLimits().validate() == nil {
				preset.setResizeLimits(limits)
			}
		}
		size, err := compress.ParseTargetSize(target.sizeText)
		if err != nil {
			return nil, "", &presetValidationError{index: index, field: "preset-target-size-input", err: err}
		}
		preset.targetSizeBytes = size
		if preset.targetResizeMinDimension != 0 {
			floor, err := parseTargetResizeMinDimension(target.resizeMinText)
			if err != nil {
				return nil, "", &presetValidationError{index: index, field: presetTargetResizeMinField, err: err}
			}
			preset.targetResizeMinDimension = floor
		}
		if preset.outputFormat == compress.OutputFormatJPEG {
			if err := compress.ValidateJPEGBackground(target.jpegBgText); err != nil {
				return nil, "", &presetValidationError{index: index, field: "preset-jpeg-bg", err: err}
			}
			preset.jpegBackground = compress.NormalizeJPEGBackground(target.jpegBgText)
		} else {
			preset.jpegBackground = compress.NormalizeJPEGBackground(preset.jpegBackground)
		}
	}
	if len(presets) == 0 {
		return nil, "", errors.New("至少需要保留一个压缩预设")
	}
	selectedIdx := e.active
	if selectedIdx < 0 || selectedIdx >= len(presets) {
		selectedIdx = 0
	}
	selected := presets[selectedIdx].name
	if err := validatePresets(presets, selected); err != nil {
		return nil, "", err
	}
	return presets, selected, nil
}

func (s *application) saveSettingsEdits() {
	if s.busy || s.editor == nil || s.editor.confirmDelete {
		return
	}
	presets, selected, err := s.editor.validatedPresets()
	if err != nil {
		s.showPresetValidationError("无法保存设置：", err)
		return
	}
	downloadDir := normalizeDownloadDir(s.editor.downloadDir)
	if err := validateDownloadDir(downloadDir); err != nil {
		s.editor.section = settingsGeneral
		s.editor.message = "无法保存设置：" + err.Error()
		s.editor.notice = ""
		return
	}
	s.ensureEditorWatchRuleIDs(s.editor)
	watch := s.editor.watch
	if watch.Preset == "" && (watch.InputDir != "" || watch.OutputDir != "") {
		watch.Preset = s.scheme
	}
	watchRules := slices.Clone(s.editor.watchRules)
	watchRuleIDs := slices.Clone(s.editor.watchRuleIDs)
	for i := range watchRules {
		if watchRules[i].Preset == "" && (watchRules[i].InputDir != "" || watchRules[i].OutputDir != "") {
			watchRules[i].Preset = s.scheme
		}
	}
	if err := s.validateFolderWatchRules(watch.primaryRule(), watchRules, watchRuleIDs); err != nil {
		s.editor.section = settingsFolderWatch
		s.editor.watchMessage = err.Error()
		s.editor.message = "无法保存设置：" + err.Error()
		s.editor.notice = ""
		return
	}
	s.editor.watchMessage = ""
	if s.settingsFile == "" {
		path, err := settingsFilePath()
		if err != nil {
			s.editor.message = "无法保存设置：" + err.Error()
			return
		}
		s.settingsFile = path
	}
	if err := s.saveSettingsSnapshotFull(presets, selected, s.batchOutputDir, s.editor.theme, s.editor.autoCompress, s.editor.backupToTrash, s.editor.keepInMenuBar, downloadDir, watch, watchRules); err != nil {
		s.editor.message = "无法保存设置：" + err.Error()
		return
	}
	s.presets = presets
	s.autoCompress = s.editor.autoCompress
	s.backupToTrash = s.editor.backupToTrash
	s.keepInMenuBar = s.editor.keepInMenuBar
	s.downloadDir = downloadDir
	s.watch.settings = watch
	s.watch.extraRules = watchRules
	s.watch.extraRuleIDs = watchRuleIDs
	if s.settingsError != "" {
		s.setMessage("设置已保存。", false)
	}
	s.settingsError = ""
	s.applyScheme(selected)
	s.syncTheme(s.editor.theme)
	s.refreshTrayMenu()
	if s.editor.openAtLoginSupported && s.editor.openAtLogin != s.editor.openAtLoginInitial {
		if err := s.applyOpenAtLogin(s.editor.openAtLogin); err != nil {
			s.editor.message = "设置已保存，但更新开机启动失败：" + err.Error()
			s.editor.notice = ""
			return
		}
		s.editor.openAtLoginInitial = s.editor.openAtLogin
	}
	s.setSettingsOpen(false)
}

func (s *application) saveCurrentSettings(presets []compressionPreset, selected, batchOutputDir string, theme themeMode) error {
	return s.saveSettingsSnapshotFull(presets, selected, batchOutputDir, theme, s.autoCompress, s.backupToTrash, s.keepInMenuBar, s.downloadDir, s.watch.settings, s.watch.extraRules)
}

func (s *application) saveSettingsSnapshot(presets []compressionPreset, selected, batchOutputDir string, theme themeMode, autoCompress, backupToTrash bool, downloadDir string, watch folderWatchSettings) error {
	return s.saveSettingsSnapshotFull(presets, selected, batchOutputDir, theme, autoCompress, backupToTrash, s.keepInMenuBar, downloadDir, watch, s.watch.extraRules)
}

func (s *application) saveSettingsSnapshotFull(presets []compressionPreset, selected, batchOutputDir string, theme themeMode, autoCompress, backupToTrash, keepInMenuBar bool, downloadDir string, watch folderWatchSettings, watchRules []folderWatchRule) error {
	return saveFullSettings(s.settingsFile, appSettings{
		presets:        presets,
		selected:       selected,
		batchOutputDir: usableBatchOutputDir(batchOutputDir),
		theme:          theme,
		autoCompress:   autoCompress,
		backupToTrash:  backupToTrash,
		keepInMenuBar:  keepInMenuBar,
		downloadDir:    normalizeDownloadDir(downloadDir),
		watch:          watch,
		watchRules:     watchRules,
	})
}

func (s *application) effectiveCLIInstallEnv() cliInstallEnv {
	env := s.cliInstallEnv
	if env.executable == nil && env.candidateDirs == nil && env.pathEnv == nil && env.commandName == "" && env.goos == "" {
		return defaultCLIInstallEnv()
	}
	return env
}

func (s *application) runInstallCLI() {
	if s.settingsDraftLocked() {
		return
	}
	editor := s.editor
	status, err := installCLI(s.effectiveCLIInstallEnv())
	editor.cliStatus = status
	if err != nil {
		editor.cliMessage = err.Error()
		editor.cliFailed = true
		return
	}
	editor.cliFailed = false
	if status.InPATH {
		editor.cliMessage = "已安装命令行工具：" + status.LinkPath
	} else {
		editor.cliMessage = fmt.Sprintf("已创建链接 %s（请将 %s 加入 PATH）", status.LinkPath, filepath.Dir(status.LinkPath))
	}
}

func (s *application) refreshCLIInstallStatus() {
	if s.settingsDraftLocked() {
		return
	}
	editor := s.editor
	editor.cliStatus = inspectCLIInstall(s.effectiveCLIInstallEnv())
	editor.cliMessage = ""
	editor.cliFailed = false
}

func (s *application) runUninstallCLI() {
	if s.settingsDraftLocked() {
		return
	}
	editor := s.editor
	env := s.effectiveCLIInstallEnv()
	if err := uninstallCLI(env); err != nil {
		editor.cliMessage = err.Error()
		editor.cliFailed = true
		editor.cliStatus = inspectCLIInstall(env)
		return
	}
	editor.cliStatus = inspectCLIInstall(env)
	editor.cliMessage = "已卸载命令行工具链接"
	editor.cliFailed = false
}

func (s *application) effectiveOpenAtLoginSupported() bool {
	if s.openAtLoginSupported != nil {
		return s.openAtLoginSupported()
	}
	return runtime.GOOS == "darwin" || runtime.GOOS == "windows"
}

func (s *application) effectiveOpenAtLogin() bool {
	if s.openAtLoginState != nil {
		return s.openAtLoginState()
	}
	if !mygo.App.IsReady() {
		return false
	}
	return mygo.App.OpenAtLogin()
}

func (s *application) applyOpenAtLogin(enabled bool) error {
	if s.setOpenAtLogin != nil {
		return s.setOpenAtLogin(enabled)
	}
	if !mygo.App.IsReady() {
		return errors.New("当前运行环境暂不支持修改登录启动项")
	}
	return mygo.App.SetOpenAtLogin(enabled)
}

func (s *application) effectiveQuickActionEnv() quickActionEnv {
	env := s.quickActionEnv
	if env.executable == nil && env.servicesDir == nil && env.refreshServices == nil && env.goos == "" {
		return defaultQuickActionEnv()
	}
	return env
}

func (s *application) runInstallQuickAction() {
	if s.settingsDraftLocked() || s.editor.quickActionBusy {
		return
	}
	editor := s.editor
	env := s.effectiveQuickActionEnv()
	editor.quickActionBusy = true
	editor.quickActionFailed = false
	editor.quickActionMessage = "正在安装访达快捷操作…"
	if s.settingsWindow != nil {
		s.settingsWindow.Invalidate()
	}
	go func() {
		status, err := installQuickAction(env)
		s.updateWithInvalidate(false, func() {
			if s.editor != editor {
				return
			}
			editor.quickActionBusy = false
			editor.quickActionStatus = status
			if err != nil {
				editor.quickActionMessage = err.Error()
				editor.quickActionFailed = true
			} else {
				editor.quickActionFailed = false
				editor.quickActionMessage = "已安装访达右键快捷操作，选中图片或文件夹右键「快速操作」即可使用"
			}
			if s.settingsWindow != nil {
				s.settingsWindow.Invalidate()
			}
		})
	}()
}

func (s *application) refreshQuickActionStatus() {
	if s.settingsDraftLocked() || s.editor.quickActionBusy {
		return
	}
	editor := s.editor
	editor.quickActionStatus = inspectQuickAction(s.effectiveQuickActionEnv())
	editor.quickActionMessage = ""
	editor.quickActionFailed = false
}

func (s *application) runUninstallQuickAction() {
	if s.settingsDraftLocked() || s.editor.quickActionBusy {
		return
	}
	editor := s.editor
	env := s.effectiveQuickActionEnv()
	editor.quickActionBusy = true
	editor.quickActionFailed = false
	editor.quickActionMessage = "正在卸载访达快捷操作…"
	if s.settingsWindow != nil {
		s.settingsWindow.Invalidate()
	}
	go func() {
		err := uninstallQuickAction(env)
		status := inspectQuickAction(env)
		s.updateWithInvalidate(false, func() {
			if s.editor != editor {
				return
			}
			editor.quickActionBusy = false
			editor.quickActionStatus = status
			if err != nil {
				editor.quickActionMessage = err.Error()
				editor.quickActionFailed = true
			} else {
				editor.quickActionFailed = false
				editor.quickActionMessage = "已卸载访达右键快捷操作"
			}
			if s.settingsWindow != nil {
				s.settingsWindow.Invalidate()
			}
		})
	}()
}

func (s *application) cancelUpdateCheck() {
	s.updateGen++
	if s.updateCancel != nil {
		s.updateCancel()
		s.updateCancel = nil
	}
	if s.updateState == updateChecking {
		s.updateState = updateIdle
		s.updateMessage = ""
		s.updateMessageFailed = false
	}
}

func (s *application) checkAppUpdates() {
	if s.closed {
		return
	}
	if s.updateCancel != nil {
		s.updateCancel()
		s.updateCancel = nil
	}
	s.updateGen++
	gen := s.updateGen
	ctx, cancel := context.WithCancel(context.Background())
	s.updateCancel = cancel
	s.updateState = updateChecking
	s.updateMessage = "正在检查最新版本…"
	s.updateMessageFailed = false
	cfg := s.updateChecker
	if s.settingsWindow != nil {
		s.settingsWindow.Invalidate()
	}
	go func() {
		defer cancel()
		res, err := checkForUpdates(ctx, cfg)
		s.updateWithInvalidate(false, func() {
			if s.updateGen != gen {
				return
			}
			s.updateCancel = nil
			s.updateMessageFailed = false
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return
				}
				s.updateState = updateFailed
				s.updateMessage = err.Error()
			} else if res.HasUpdate {
				s.updateState = updateAvailable
				s.updateResult = res
				s.updateMessage = fmt.Sprintf("发现新版本 v%s（当前 v%s）", res.LatestVersion, res.CurrentVersion)
			} else if res.NoReleaseYet {
				s.updateState = updateUpToDate
				s.updateResult = res
				s.updateMessage = fmt.Sprintf("当前 v%s 已是最新（暂无已发布版本）", res.CurrentVersion)
			} else {
				s.updateState = updateUpToDate
				s.updateResult = res
				s.updateMessage = fmt.Sprintf("当前 v%s 已是最新版本", res.CurrentVersion)
			}
			if s.settingsWindow != nil {
				s.settingsWindow.Invalidate()
			}
		})
	}()
}

func (s *application) openReleaseDownloadPage() {
	target := sanitizeReleaseURL(s.updateResult.ReleaseURL)
	openFn := s.openExternalURL
	if openFn == nil {
		openFn = mygo.Shell.OpenExternal
	}
	if err := openFn(target); err != nil {
		s.updateMessage = "无法打开下载页面：" + err.Error()
		s.updateMessageFailed = true
		if s.settingsWindow != nil {
			s.settingsWindow.Invalidate()
		}
		return
	}
	if s.updateMessageFailed && s.updateState == updateAvailable && s.updateResult.LatestVersion != "" {
		s.updateMessageFailed = false
		s.updateMessage = fmt.Sprintf("发现新版本 v%s（当前 v%s）", s.updateResult.LatestVersion, s.updateResult.CurrentVersion)
		if s.settingsWindow != nil {
			s.settingsWindow.Invalidate()
		}
	}
}

func (s *application) exportPresetsToDialog() {
	if s.settingsDraftLocked() {
		return
	}
	editor, parent := s.editor, s.settingsWindow
	presets, _, err := editor.validatedPresets()
	if err != nil {
		s.showPresetValidationError("无法导出预设：", err)
		return
	}
	s.busy = true
	go func() {
		dest, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
			Parent:            parent,
			Title:             "导出预设",
			ButtonLabel:       "导出",
			DefaultPath:       "z-pic-presets.json",
			CreateDirectories: true,
			Filters:           []mygo.FileFilter{{Name: "JSON 预设文件", Extensions: []string{"json"}}},
		})
		var writeErr error
		if err == nil && dest != "" {
			writeErr = writePresetBundleFile(dest, presets)
		}
		s.updateWithInvalidate(false, func() {
			s.busy = false
			if s.editor != editor {
				return
			}
			switch {
			case err != nil:
				editor.message = "导出预设失败：" + err.Error()
				editor.notice = ""
			case writeErr != nil:
				editor.message = "导出预设失败：" + writeErr.Error()
				editor.notice = ""
			case dest != "":
				editor.message = ""
				editor.notice = fmt.Sprintf("已导出 %d 个预设到 %s", len(presets), filepath.Base(dest))
			}
			if s.settingsWindow != nil {
				s.settingsWindow.Invalidate()
			}
		})
	}()
}

func (s *application) importPresetsFromDialog() {
	if s.settingsDraftLocked() {
		return
	}
	editor, parent := s.editor, s.settingsWindow
	s.busy = true
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:      parent,
			Title:       "导入预设",
			ButtonLabel: "导入",
			Filters:     []mygo.FileFilter{{Name: "JSON 预设文件", Extensions: []string{"json"}}},
		})
		var imported []compressionPreset
		var readErr error
		if err == nil && len(paths) > 0 {
			imported, readErr = readPresetBundleFile(paths[0])
		}
		s.updateWithInvalidate(false, func() {
			s.busy = false
			if s.editor != editor {
				return
			}
			switch {
			case err != nil:
				editor.message = "导入预设失败：" + err.Error()
				editor.notice = ""
			case readErr != nil:
				editor.message = "导入预设失败：" + readErr.Error()
				editor.notice = ""
			case len(imported) > 0:
				merged, added, _, firstIdx := mergeImportedPresets(editor.presets, imported, false)
				editor.presets = merged
				targets := make([]*presetTargetDraft, len(merged))
				for i, preset := range merged {
					if i < len(editor.targets) {
						targets[i] = editor.targets[i]
					} else {
						targets[i] = newPresetTargetDraft(preset)
					}
				}
				editor.targets = targets
				if firstIdx >= 0 && firstIdx < len(merged) {
					editor.selected = firstIdx
				}
				editor.message = ""
				editor.notice = fmt.Sprintf("已导入 %d 个预设（点击保存后生效）", added)
			}
			if s.settingsWindow != nil {
				s.settingsWindow.Invalidate()
			}
		})
	}()
}

// Capture all settings on the UI thread before launching compression.
func (s *application) compressionOptions() compress.Options {
	preset := s.activePresetSnapshot()
	options := preset.compressionOptions()
	options.TargetResizeMinDimension = preset.effectiveTargetResizeMinDimension()
	return options
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

func (s *application) chooseDownloadDirectory() {
	if s.settingsDraftLocked() {
		return
	}
	editor, parent := s.editor, s.settingsWindow
	defaultPath := editor.downloadDir
	if defaultPath == "" {
		if dir, err := s.downloadedImagesDir(); err == nil {
			defaultPath = dir
		}
	}
	s.busy = true
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent: parent, Title: "选择网页图片下载目录", ButtonLabel: "选择此文件夹",
			DefaultPath: defaultPath, Directory: true, CreateDirectories: true,
		})
		s.update(func() {
			s.busy = false
			if s.editor != editor {
				return
			}
			if err != nil {
				editor.message = "无法选择下载目录：" + err.Error()
				editor.notice = ""
			} else if len(paths) != 0 {
				editor.downloadDir = normalizeDownloadDir(paths[0])
				editor.message, editor.notice = "", ""
			}
			if s.settingsWindow != nil {
				s.settingsWindow.Invalidate()
			}
		})
	}()
}

func (s *application) resetDownloadDirectory() {
	if s.settingsDraftLocked() {
		return
	}
	s.editor.downloadDir = ""
	s.editor.message, s.editor.notice = "", ""
}

func (s *application) activeImagePaths() map[string]bool {
	active := make(map[string]bool)
	record := func(path string) {
		if path == "" {
			return
		}
		if abs, err := filepath.Abs(path); err == nil {
			active[abs] = true
		}
		active[path] = true
	}
	if s.file != nil {
		record(s.file.path)
	}
	record(s.lastSavedPath)
	if s.batch != nil {
		for _, item := range s.batch.items {
			record(item.path)
			record(item.absPath)
			record(item.savedPath)
		}
	}
	return active
}

func isCleanableDownloadImage(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg", ".png", ".webp", ".avif", ".heic", ".heif":
		return true
	default:
		return false
	}
}

type downloadCleanupReport struct {
	trashed  int
	tempGone int
	skipped  int
	removed  map[string]bool
}

func cleanupDownloadDirFiles(dir string, active map[string]bool, trash func(string) error) (downloadCleanupReport, error) {
	var report downloadCleanupReport
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return report, nil
		}
		return report, err
	}
	report.removed = make(map[string]bool)
	for _, entry := range entries {
		name := entry.Name()
		full := filepath.Join(dir, name)
		info, err := os.Lstat(full)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		abs, err := filepath.Abs(full)
		if err != nil {
			abs = full
		}
		if active[abs] || active[full] {
			if isCleanableDownloadImage(name) {
				report.skipped++
			}
			continue
		}
		if strings.HasPrefix(name, ".zpic-drop-") {
			if err := os.Remove(full); err == nil {
				report.tempGone++
			}
			continue
		}
		if !isCleanableDownloadImage(name) {
			continue
		}
		if err := trash(full); err != nil {
			return report, err
		}
		report.trashed++
		report.removed[abs] = true
	}
	return report, nil
}

func (s *application) effectiveEditorDownloadDir() (string, error) {
	if s.editor != nil && s.editor.downloadDir != "" {
		return normalizeDownloadDir(s.editor.downloadDir), nil
	}
	return s.downloadedImagesDir()
}

func (s *application) cleanupDownloadedImages() {
	if s.settingsDraftLocked() {
		return
	}
	editor := s.editor
	dir, err := s.effectiveEditorDownloadDir()
	if err != nil {
		editor.message = "无法定位下载目录：" + err.Error()
		editor.notice = ""
		return
	}
	active := s.activeImagePaths()
	trash := s.trashItem
	if trash == nil {
		trash = mygo.Shell.TrashItem
	}
	s.busy = true
	s.saves.Add(1)
	go func() {
		defer s.saves.Done()
		report, cleanupErr := cleanupDownloadDirFiles(dir, active, trash)
		s.update(func() {
			s.busy = false
			s.pruneDownloadedURLPaths(report.removed)
			if s.editor != editor {
				return
			}
			if cleanupErr != nil {
				if report.trashed > 0 {
					editor.message = fmt.Sprintf("已移入废纸篓 %d 张，但清理中断：%v", report.trashed, cleanupErr)
				} else {
					editor.message = "清理下载目录失败：" + cleanupErr.Error()
				}
				editor.notice = ""
			} else {
				editor.message = ""
				switch {
				case report.trashed > 0 && report.skipped > 0:
					editor.notice = fmt.Sprintf("已将 %d 张下载图片移入废纸篓，保留 %d 张队列中的图片", report.trashed, report.skipped)
				case report.trashed > 0:
					editor.notice = fmt.Sprintf("已将 %d 张下载图片移入废纸篓", report.trashed)
				case report.tempGone > 0:
					editor.notice = fmt.Sprintf("已清理 %d 个临时下载文件", report.tempGone)
				case report.skipped > 0:
					editor.notice = fmt.Sprintf("没有可清理的闲置图片（保留 %d 张队列中的图片）", report.skipped)
				default:
					editor.notice = "下载目录中没有可清理的图片"
				}
			}
			if s.settingsWindow != nil {
				s.settingsWindow.Invalidate()
			}
		})
	}()
}
