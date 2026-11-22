package main

import (
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"zpic/internal/compress"
)

var (
	presetPlusIcon       = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"><path d="M8 3.25v9.5M3.25 8h9.5"/></svg>`))
	presetCheckIcon      = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 12 12" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="M2.5 6.2 4.9 8.6 9.5 3.6"/></svg>`))
	presetQualityIcon    = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"><path d="M2 5h2.5m3.5 0h6M2 11h6m3.5 0H14"/><circle cx="6.25" cy="5" r="1.75"/><circle cx="9.75" cy="11" r="1.75"/></svg>`))
	presetSaveIcon       = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="M2.5 4.25A1.25 1.25 0 0 1 3.75 3h2.6l1.4 1.5h4.5A1.25 1.25 0 0 1 13.5 5.75v6A1.25 1.25 0 0 1 12.25 13h-8.5A1.25 1.25 0 0 1 2.5 11.75Z"/></svg>`))
	presetProcessingIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><rect x="2.5" y="2.5" width="11" height="11" rx="2"/><path d="M5.5 8h5M8 5.5v5"/></svg>`))
	presetInfoIcon       = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"><circle cx="8" cy="8" r="6"/><path d="M8 7.25v3.5M8 5.1v.15"/></svg>`))
)

func presetActiveIndicator(c *ui.Context) {
	ui.Icon(c, presetCheckIcon).Size(16, 16).Shrink(0).TextColor(accentForeground(c.Theme())).
		AccessHidden(true).Tooltip("当前预设")
}

func presetQualityLabel(preset compressionPreset) string {
	if preset.lossless {
		return "无损压缩"
	}
	level := qualityLevel(preset)
	if preset.quality != imageQualityLevels[level].quality || preset.png != imageQualityLevels[level].png {
		return "自定义质量"
	}
	return fmt.Sprintf("%d · %s", level, imageQualityLevels[level].label)
}

func presetMetadataLabel(policy compress.MetadataPolicy) string {
	switch policy {
	case compress.MetadataKeepICC:
		return "仅色彩配置"
	case compress.MetadataStrip:
		return "移除全部"
	default:
		return "保留全部"
	}
}

func presetOutputFormatLabel(format compress.OutputFormat) string {
	switch format {
	case compress.OutputFormatJPEG:
		return "转为 JPEG"
	case compress.OutputFormatPNG:
		return "转为 PNG"
	case compress.OutputFormatWebP:
		return "转为 WebP"
	case compress.OutputFormatAVIF:
		return "转为 AVIF"
	default:
		return "保持原格式"
	}
}

func presetResizeLabel(limits resizeLimits) string {
	mode, value := limits.selection()
	switch mode {
	case resizeLongEdge:
		return fmt.Sprintf("长边 ≤ %dpx", value)
	case resizeWidth:
		return fmt.Sprintf("宽度 ≤ %dpx", value)
	case resizeHeight:
		return fmt.Sprintf("高度 ≤ %dpx", value)
	case resizePercent:
		return fmt.Sprintf("缩至 %d%%", value)
	default:
		return "原尺寸"
	}
}

func presetProcessingSummary(preset compressionPreset) string {
	var parts []string
	if preset.outputFormat != compress.OutputFormatKeep {
		parts = append(parts, presetOutputFormatLabel(preset.outputFormat))
	}
	if limits := preset.resizeLimits(); limits != (resizeLimits{}) {
		parts = append(parts, presetResizeLabel(limits))
	}
	if preset.targetSizeBytes > 0 {
		parts = append(parts, "体积 ≤ "+compress.FormatTargetSize(preset.targetSizeBytes))
		if preset.effectiveTargetResizeMinDimension() != 0 {
			parts = append(parts, "自动缩小")
		}
	}
	if preset.metadata != compress.MetadataKeepAll {
		parts = append(parts, presetMetadataLabel(preset.metadata))
	}
	return strings.Join(parts, " · ")
}

func presetDisplayName(preset compressionPreset) string {
	if name := strings.TrimSpace(preset.name); name != "" {
		return name
	}
	return "未命名预设"
}

func presetOutputExample(options presetSaveOptions) string {
	return presetOutputExampleForFormat(options, compress.OutputFormatKeep)
}

func presetOutputExampleForFormat(options presetSaveOptions, targetFormat compress.OutputFormat) string {
	return presetOutputNameForFormat(options, "example.png", targetFormat)
}

func presetOutputName(options presetSaveOptions, filename string) string {
	return presetOutputNameForFormat(options, filename, compress.OutputFormatKeep)
}

func presetOutputNameForFormat(options presetSaveOptions, filename string, targetFormat compress.OutputFormat) string {
	return presetOutputNameForResolved(options, filename, "", targetFormat, false)
}

func presetOutputNameForResolved(options presetSaveOptions, filename, sourceFormat string, targetFormat compress.OutputFormat, lossless bool) string {
	if options.Mode == presetSaveOriginal {
		filename = filepath.Base(filename)
	}
	filename = replaceFilenameExtensionFor(filename, sourceFormat, targetFormat, lossless)
	ext := filepath.Ext(filename)
	name := strings.TrimSuffix(filename, ext)
	if options.UseSuffix {
		suffix := options.Suffix
		if suffix == "" {
			suffix = defaultFilenameSuffix
		}
		name += suffix
	}
	name += ext
	if options.supportsSubfolder() && options.UseSubfolder {
		folder := options.Subfolder
		if folder == "" {
			folder = defaultSubfolderName
		}
		name = filepath.Join(folder, name)
	}
	return name
}

func presetSaveDescription(options presetSaveOptions) string {
	if options.replacesOriginal() {
		return "当前设置将替换原图"
	}
	switch options.Mode {
	case presetSaveAsk:
		return "保存时选择输出位置"
	case presetSaveFixed:
		if options.Directory == "" {
			return "请选择固定输出目录"
		}
		return options.Directory
	default:
		return "在原文件夹中保存副本"
	}
}

func (s *application) presetSettingsContent(c *ui.Context) {
	editor := s.editor
	locked := s.settingsDraftLocked()
	bodyPad := settingsPagePadding(c)
	ui.Column(c).FillWidth().Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).FillWidth().Padding(6, bodyPad, 6, bodyPad).Background(sidebarSurface(c)).Children(func() { s.presetEditorTabs(c) })
		ui.Box(c).FillWidth().Height(1).Background(hairlineDivider(c))
		ui.Scroll(c.Key("preset-editor")).Grow(1).MinHeight(0).Padding(10, bodyPad, 16, bodyPad).Gap(8).Children(func() {
			preset := editor.selectedPreset()
			settingsPanel(c, "settings-preset-name-panel").Padding(9, 14).Gap(6).Children(func() {
				var name ui.Element
				ui.Row(c).FillWidth().MinHeight(settingsControlHeight(c, 32)).Gap(10).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "预设名称").FontSize(settingsFontSize(c, 13)).FontWeight(600).SingleLine().Shrink(0)
					name = ui.TextInput(c.Key("preset-name"), &preset.name).Label("预设名称").
						Description("最多 40 个字符，名称不能重复；保存时去除首尾空格").Height(settingsControlHeight(c, 32)).
						Width(0).Grow(1).MinWidth(0).FontSize(settingsFontSize(c, 13)).Padding(0, 10).Disabled(locked)
					styleGlassField(name, c, 32)
					if editor.focusField == "preset-name" && !locked {
						name.Focus()
						if editor.selectName {
							name.SetTextSelection(0, utf8.RuneCountInString(preset.name))
						}
						editor.focusField, editor.selectName = "", false
					}
					if name.Changed() {
						editor.message = ""
					}
					color := chromeMetadataColor(c)
					if utf8.RuneCountInString(preset.name) > 40 {
						color = c.Theme().Danger
					}
					ui.Textf(c, "%d / 40", utf8.RuneCountInString(preset.name)).FontSize(settingsFontSize(c, 11)).
						FontFeatures("tnum").TextColor(color).SingleLine().Shrink(0).Description("预设名称最多 40 个字符")
					if editor.active != editor.selected {
						settingsButton(c, "activate-preset", "设为当前预设", 30, s.busy || editor.confirmDelete).
							FontSize(settingsFontSize(c, 12)).Padding(0, 10).
							OnClick(func() { s.selectSettingsPreset(editor.selected) })
					}
				})
				if problem := editor.nameProblem(); problem != "" {
					name.Invalid(true).ErrorMessage(ui.Text(c, problem).FontSize(settingsFontSize(c, 12)).TextColor(c.Theme().Danger).
						FillWidth().MaxLines(2))
				}
			})
			s.presetQualityCard(c, preset)
			s.presetSaveCard(c, preset)
			s.presetProcessingCard(c, preset)
		})
	})
}

func (s *application) presetEditorTabs(c *ui.Context) {
	t := c.Theme()
	editor := s.editor
	locked := s.settingsDraftLocked()
	tabH := settingsControlHeight(c, 32)
	ui.Row(c).FillWidth().Height(tabH).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.ScrollHorizontal(c.Key("preset-list")).Height(tabH).Width(0).Grow(1).Children(func() {
			tabs := ui.TabsBase(c.Key("preset-tabs"), &editor.selected, len(editor.presets))
			shown := ui.Local(tabs.List, "shown-selection", func() int { return -1 })
			tabs.List.Label("编辑预设").Gap(4).Height(tabH).AlignItems(ui.Center).Disabled(locked).Children(func() {
				for index, preset := range editor.presets {
					name := presetDisplayName(preset)
					button := tabs.Tab(index).
						Label("编辑预设："+name).Tooltip(name).Height(tabH).
						MinWidth(84).MaxWidth(192).Padding(0, 11).Gap(6).Radius(innerRadius(c)).
						Border(1, ui.Transparent).AlignItems(ui.Center).
						TextColor(chromeMutedColor(c)).Disabled(locked).AccessSelected(editor.selected == index)
					if editor.active == index {
						button.Description("当前使用的预设")
					}
					if editor.selected == index {
						button.Background(glassSelectionBackground(c)).Border(1, selectionBorder(c)).TextColor(t.Text)
						settingsGlassControl(button, c, 32, locked, t.Accent.Alpha(0.14))
						if *shown != index {
							button.ScrollIntoView()
							*shown = index
						}
					} else if !locked {
						if button.Pressed() {
							button.Background(glassControlPressed(c)).TextColor(t.Text)
						} else if button.Hovered() {
							button.Background(glassControlHover(c)).TextColor(t.Text)
						}
					}
					button.Children(func() {
						weight := 400
						if editor.selected == index {
							weight = 500
						}
						ui.Text(c, name).FontSize(settingsFontSize(c, 13)).FontWeight(weight).SingleLine().TruncateMiddle().
							MaxWidth(144).MinWidth(0).Shrink(1)
						if editor.active == index {
							presetActiveIndicator(c)
						}
					})
				}
			})
			if tabs.List.Changed() {
				editor.message = ""
			}
		})
		addBtn := ui.ButtonBase(c.Key("add-preset")).Size(tabH, tabH).Center().Radius(innerRadius(c)).
			Background(glassControlSurface(c)).Border(1, controlBorder(c)).
			Label("新增预设").Tooltip("新增预设").Disabled(locked).OnClick(s.addPreset)
		if !locked {
			if addBtn.Pressed() {
				addBtn.Background(glassControlPressed(c)).Border(1, controlHoverBorder(c))
			} else {
				controlElevation(addBtn, c)
				if addBtn.Hovered() {
					addBtn.Background(glassControlHover(c)).Border(1, controlHoverBorder(c))
				}
			}
		}
		settingsGlassControl(addBtn, c, 32, locked, ui.Transparent)
		addBtn.Children(func() { ui.Icon(c, presetPlusIcon).Size(15, 15).TextColor(t.Text) })
	})
}

func lossyQualityFormats() string {
	if compress.SupportsAVIF() {
		return "JPEG / WebP / AVIF"
	}
	return "JPEG / WebP"
}

func (s *application) presetQualityCard(c *ui.Context, preset *compressionPreset) {
	t := c.Theme()
	locked := s.settingsDraftLocked()
	losslessFormats := "PNG、WebP"
	if compress.SupportsAVIF() {
		losslessFormats += "、AVIF"
	}
	losslessNote := losslessFormats + " 支持无损。同格式 JPEG 可无损优化，转为 JPEG 使用有损编码。"
	settingsPanel(c, "settings-quality-panel").Padding(11, 14).Gap(6).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, presetQualityIcon).Size(15, 15).TextColor(metadataColor(c))
			ui.Text(c, "图片质量").FontSize(settingsFontSize(c, 14)).FontWeight(600).Role(ui.RoleHeading).Level(2)
			ui.Icon(c, presetInfoIcon).Size(14, 14).TextColor(metadataColor(c)).
				Tooltip("压缩参数随图片质量自动调整。质量越高，越接近原图；质量越低，文件通常越小。")
			ui.Box(c).Grow(1)
			if ui.Checkbox(c.Key("preset-lossless"), &preset.lossless, "无损").
				FontSize(settingsFontSize(c, 12)).Disabled(locked).
				Tooltip(losslessNote).Changed() {
				s.editor.message = ""
			}
			ui.Box(c).Size(1, 14).Background(hairlineDivider(c))
			if preset.lossless {
				neutralCapsule(c, presetQualityLabel(*preset)).FontSize(settingsFontSize(c, 12))
			} else {
				accentCapsule(c, presetQualityLabel(*preset)).FontSize(settingsFontSize(c, 12))
			}
		})
		value := float64(qualityLevel(*preset))
		description := fmt.Sprintf("%s 质量 %.0f，PNG 质量 %d–%d，最多 %d 色。左右方向键调整，Home / End 跳到两端。", lossyQualityFormats(), preset.quality,
			preset.png.MinQuality, preset.png.MaxQuality, preset.png.Colors)
		if preset.lossless {
			description = losslessNote
		} else if presetQualityLabel(*preset) == "自定义质量" {
			description = "已保留自定义参数；调整滑块后将使用对应档位。" + description
		}
		sliderDisabled := locked || preset.lossless
		slider := ui.SliderBase(c.Key("quality-level"), &value, 0, float64(len(imageQualityLevels)-1)).Step(1).
			Label("图片质量").Value(presetQualityLabel(*preset)).Description(description).Tooltip(description).
			Height(settingsControlHeight(c, 26)).PaddingX(12).Disabled(sliderDisabled)
		s.focusSettingsField(slider, "quality-level")
		at := float32(value / float64(len(imageQualityLevels)-1))
		slider.Draw(func(p *ui.Painter, r ui.Rect) {
			centerY := r.Y + r.H/2
			track := ui.Rect{X: r.X + 12, Y: centerY - 2, W: max(0, r.W-24), H: 4}
			p.Fill(track, meterTrackColor(c), 2)
			fill := track
			fill.W *= at
			fillColor := t.Accent
			if sliderDisabled {
				fillColor = t.TextMuted.Alpha(0.4)
			}
			p.Fill(fill, fillColor, 2)
			activeIndex := int(math.Round(value))
			for tick := range len(imageQualityLevels) {
				x := track.X + track.W*float32(tick)/float32(len(imageQualityLevels)-1)
				pip := ui.Rect{X: x - 3, Y: centerY - 3, W: 6, H: 6}
				pipColor := meterTrackColor(c)
				if tick <= activeIndex {
					pipColor = fillColor
				}
				p.Fill(pip, pipColor, 3)
			}
			thumbX := track.X + track.W*at
			thumb := ui.Rect{X: thumbX - 10, Y: centerY - 10, W: 20, H: 20}
			p.Shadow(thumb, 10, 0, 2, 5, 0, ui.RGBA(0, 0, 0, 0.22))
			p.Fill(thumb, ui.Hex("#ffffff"), 10)
			p.Stroke(thumb, selectionBorder(c), 10, 1.25)
			p.Fill(ui.Rect{X: thumbX - 3, Y: centerY - 3, W: 6, H: 6}, fillColor, 3)
		})
		if slider.Changed() {
			s.editQualityLevel(int(math.Round(value)))
		}
		if preset.lossless {
			note := losslessNote
			if compress.SupportsHEIC() {
				note += "HEIC 选择保持原格式时输出无损 PNG。"
			}
			ui.Text(c, note).
				FontSize(settingsFontSize(c, 12)).TextColor(metadataColor(c)).MaxLines(2)
		} else {
			ui.Row(c).Children(func() {
				ui.Text(c, "文件更小").FontSize(settingsFontSize(c, 12)).TextColor(metadataColor(c)).Grow(1)
				ui.Text(c, "质量更高").FontSize(settingsFontSize(c, 12)).TextColor(metadataColor(c))
			})
		}
	})
}

var resizeDimensionPresets = [...]struct {
	label string
	value int
}{
	{"原尺寸", 0},
	{"1280", 1280},
	{"1920", 1920},
	{"2560", 2560},
	{"3840", 3840},
}

func (s *application) presetProcessingCard(c *ui.Context, preset *compressionPreset) {
	locked := s.settingsDraftLocked()
	settingsPanel(c, "settings-processing-panel").Padding(11, 14).Gap(9).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, presetProcessingIcon).Size(15, 15).TextColor(metadataColor(c))
			ui.Text(c, "格式与优化").FontSize(settingsFontSize(c, 14)).FontWeight(600).Role(ui.RoleHeading).Level(2)
		})
		// Output format
		ui.Column(c).FillWidth().Gap(8).Children(func() {
			ui.Text(c, "输出格式").FontSize(settingsFontSize(c, 13)).FontWeight(500)
			formats := []compress.OutputFormat{
				compress.OutputFormatKeep, compress.OutputFormatJPEG, compress.OutputFormatPNG, compress.OutputFormatWebP,
			}
			if compress.SupportsAVIF() || preset.outputFormat == compress.OutputFormatAVIF {
				formats = append(formats, compress.OutputFormatAVIF)
			}
			fmtIdx := 0
			labels := make([]string, len(formats))
			for i, format := range formats {
				labels[i] = presetOutputFormatLabel(format)
				if preset.outputFormat == format {
					fmtIdx = i
				}
			}
			seg := settingsSegments(c, "preset-output-format", "输出格式", &fmtIdx, locked, labels...).FillWidth()
			s.focusSettingsField(seg, "preset-output-format")
			if seg.Changed() && fmtIdx >= 0 && fmtIdx < len(formats) {
				preset.outputFormat = formats[fmtIdx]
				s.editor.message = ""
			}
			var note string
			switch preset.outputFormat {
			case compress.OutputFormatJPEG:
				note = "透明区域填充白色；转换为 JPEG 有损且保留原文件，同格式图片可覆盖。"
			case compress.OutputFormatPNG:
				note = "转换为 PNG 时保留原文件，同格式图片可覆盖。"
			case compress.OutputFormatWebP:
				note = "转换为 WebP 时保留原文件，同格式图片可覆盖。"
			case compress.OutputFormatAVIF:
				note = "转换为 AVIF 时保留原文件。同格式图片在体积减小时可覆盖。"
				if !compress.SupportsAVIF() {
					note = "当前平台暂不支持 AVIF 编码。"
				}
			default:
				formats := "JPEG / PNG / WebP"
				if compress.SupportsAVIF() {
					formats += " / AVIF"
				}
				note = formats + " 保持原格式。"
				if compress.SupportsHEIC() {
					note += "HEIC 自动转为 JPEG（无损时为 PNG），并保留原文件。"
				}
			}
			ui.Text(c, note).FontSize(settingsFontSize(c, 12)).TextColor(metadataColor(c)).MaxLines(2)
		})
		ui.Box(c).FillWidth().Height(1).Background(subtleRowDivider(c))
		s.presetResizeSection(c, preset)
		ui.Box(c).FillWidth().Height(1).Background(subtleRowDivider(c))
		s.presetTargetSizeSection(c, preset)
		ui.Box(c).FillWidth().Height(1).Background(subtleRowDivider(c))
		// Metadata policy
		ui.Column(c).FillWidth().Gap(8).Children(func() {
			ui.Text(c, "元数据").FontSize(settingsFontSize(c, 13)).FontWeight(500)
			metaIdx := 0
			switch preset.metadata {
			case compress.MetadataKeepICC:
				metaIdx = 1
			case compress.MetadataStrip:
				metaIdx = 2
			}
			seg := settingsSegments(c, "preset-metadata", "元数据", &metaIdx, locked, "保留全部", "仅色彩配置", "移除全部").FillWidth()
			s.focusSettingsField(seg, "preset-metadata")
			if seg.Changed() {
				preset.metadata = [...]compress.MetadataPolicy{
					compress.MetadataKeepAll,
					compress.MetadataKeepICC,
					compress.MetadataStrip,
				}[metaIdx]
				s.editor.message = ""
			}
			var metaNote string
			switch preset.metadata {
			case compress.MetadataKeepICC:
				metaNote = "移除 EXIF、GPS 与文本注释，保留色彩配置（ICC / sRGB），适合分享与上传。"
			case compress.MetadataStrip:
				metaNote = "移除全部元数据与色彩配置，以换取最小体积。"
			default:
				formats := "PNG、WebP"
				if compress.SupportsAVIF() {
					formats += "、AVIF"
				}
				metaNote = "同格式 " + formats + " 尽量保留元数据。转换时保留兼容色彩配置并校正方向。"
			}
			ui.Text(c, metaNote).FontSize(settingsFontSize(c, 12)).TextColor(metadataColor(c)).MaxLines(2)
		})
	})
}

func (s *application) presetResizeSection(c *ui.Context, preset *compressionPreset) {
	editor := s.editor
	locked := s.settingsDraftLocked()
	target := editor.targets[editor.selected]
	text := &target.resizeText
	mode, _ := preset.resizeLimits().selection()
	setLimits := func(limits resizeLimits) {
		preset.setResizeLimits(limits)
		*text = limits.text()
		editor.message = ""
	}
	reveal := editor.focusField == presetResizeModeField && !locked
	section := ui.Column(c).FillWidth().Gap(8)
	section.Children(func() {
		ui.Text(c, "尺寸缩放").FontSize(settingsFontSize(c, 13)).FontWeight(500)
		modes, selected := settingsSegmentsWithSelection(c, presetResizeModeField, "尺寸缩放", &mode, locked,
			"原尺寸", "按长边", "按宽度", "按高度", "按百分比")
		if reveal {
			selected.Focus(ui.FocusOptions{PreventScroll: true})
			editor.focusField = ""
		}
		if modes.Changed() {
			value := [...]int{0, 1920, 1920, 1080, 50}[mode]
			setLimits(resizeLimitsFor(mode, value))
			reveal = true
		}
		if mode == resizeNone || mode == resizeLongEdge {
			ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "长边快捷值（px）").FontSize(settingsFontSize(c, 12)).TextColor(metadataColor(c)).Shrink(0)
				for _, choice := range resizeDimensionPresets[1:] {
					active := mode == resizeLongEdge && preset.maxDimension == choice.value
					button := settingsButton(c, "resize-quick-"+choice.label, choice.label, 28, locked).
						Width(0).MinWidth(0).Grow(1).PaddingX(6).FontSize(settingsFontSize(c, 12)).FontFeatures("tnum").Selected(active)
					if active {
						button.FontWeight(500).Background(glassSelectionBackground(c)).Border(1, selectionBorder(c)).TextColor(c.Theme().Text)
						settingsGlassControl(button, c, 28, locked, c.Theme().Accent.Alpha(0.14))
					}
					if button.Clicked() {
						mode = resizeLongEdge
						setLimits(resizeLimits{maxDimension: choice.value})
						reveal = true
					}
				}
			})
		}

		note := fmt.Sprintf("超过上限时等比缩小。像素上限可设为 %d–%d px。", compress.MinResizeDimension, compress.MaxResizeDimension)
		if mode == resizePercent {
			note = "按原尺寸的百分比等比缩小。100% 保持原尺寸。"
		}
		var problem error
		var input ui.Element
		if mode != resizeNone {
			caption := [...]string{"", "长边上限（px）", "宽度上限（px）", "高度上限（px）", "缩放比例（%）"}[mode]
			_, problem = parseResizeValue(mode, *text)
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				label := ui.Text(c, caption).FontSize(settingsFontSize(c, 12)).TextColor(metadataColor(c)).Grow(1)
				// Undo history belongs to this draft and mode.
				key := struct {
					draft *presetTargetDraft
					mode  int
				}{target, mode}
				input = ui.TextInput(c.Key(key), text).LabelledBy(label).Description(note).
					Width(settingsFontSize(c, 112)).Height(settingsControlHeight(c, 32)).
					FontSize(settingsFontSize(c, 13)).FontFeatures("tnum").Padding(0, 12).Disabled(locked)
				styleGlassField(input, c, 32)
				s.focusSettingsField(input, presetResizeValueField)
				if input.Changed() {
					var limits resizeLimits
					limits, problem = parseResizeValue(mode, *text)
					if problem == nil {
						preset.setResizeLimits(limits)
					}
					editor.message = ""
				}
			})
		}
		if problem != nil {
			danger := c.Theme().Danger
			input.Invalid(true).Border(1, danger).ErrorMessage(ui.Text(c, problem.Error()).
				FontSize(settingsFontSize(c, 12)).TextColor(danger).FillWidth().MaxLines(2))
		} else {
			ui.Text(c, note).FontSize(settingsFontSize(c, 12)).TextColor(metadataColor(c)).MaxLines(2)
		}
	})
	if reveal {
		section.ScrollIntoView()
	}
}

func (s *application) presetTargetSizeSection(c *ui.Context, preset *compressionPreset) {
	editor := s.editor
	target := editor.targets[editor.selected]
	text := &target.sizeText
	_, problem := compress.ParseTargetSize(*text)
	locked := s.settingsDraftLocked()
	ui.Column(c).FillWidth().Gap(8).Children(func() {
		limit := "不限制"
		if preset.targetSizeBytes > 0 {
			limit = "≤ " + compress.FormatTargetSize(preset.targetSizeBytes)
		}
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Text(c, "目标体积").FontSize(settingsFontSize(c, 13)).FontWeight(500).Grow(1)
			if preset.targetSizeBytes > 0 {
				accentCapsule(c, limit).FontSize(settingsFontSize(c, 12))
			} else {
				neutralCapsule(c, limit).FontSize(settingsFontSize(c, 12))
			}
		})
		sizes := [...]int64{0, 200 << 10, 500 << 10, 1 << 20, 2 << 20}
		choice := -1
		if problem == nil {
			choice = slices.Index(sizes[:], preset.targetSizeBytes)
		}
		seg := settingsSegments(c, "preset-target-size", "目标体积", &choice, locked,
			"不限", "200 KiB", "500 KiB", "1 MiB", "2 MiB").FillWidth()
		s.focusSettingsField(seg, "preset-target-size")
		pickedSize := seg.Changed() && choice >= 0 && choice < len(sizes)
		if pickedSize {
			preset.targetSizeBytes = sizes[choice]
			*text = compress.FormatTargetSize(preset.targetSizeBytes)
			problem = nil
			editor.message = ""
		}
		var input ui.Element
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			label := ui.Text(c, "自定义上限").FontSize(settingsFontSize(c, 12)).TextColor(metadataColor(c)).Grow(1)
			input = ui.TextInput(c.Key("preset-target-size-input"), text).LabelledBy(label).
				Placeholder("如 300KB / 1.5MiB").Width(settingsFontSize(c, 168)).Height(settingsControlHeight(c, 32)).
				FontSize(settingsFontSize(c, 13)).FontFeatures("tnum").Padding(0, 12).Disabled(locked)
			styleGlassField(input, c, 32)
			s.focusSettingsField(input, "preset-target-size-input")
			if pickedSize {
				input.ScrollIntoView()
			}
			if input.Changed() {
				var size int64
				size, problem = compress.ParseTargetSize(*text)
				if problem == nil {
					preset.targetSizeBytes = size
				}
				editor.message = ""
			}
		})
		if problem != nil {
			danger := c.Theme().Danger
			input.Invalid(true).Border(1, danger).ErrorMessage(ui.Text(c, problem.Error()).
				FontSize(settingsFontSize(c, 12)).TextColor(danger).FillWidth().MaxLines(2))
		}
		note := "超出上限时自动降低 " + lossyQualityFormats() + " 质量或 PNG 色彩档位。留空表示不限制。"
		if preset.lossless {
			note = "无损模式下不会降低画质；若结果仍超出目标体积，完成后会给出提示。"
		}
		ui.Text(c, note).FontSize(settingsFontSize(c, 12)).TextColor(metadataColor(c)).MaxLines(2)
		s.presetTargetResizeFields(c, preset, &target.resizeMinText)
	})
}

func (s *application) presetTargetResizeFields(c *ui.Context, preset *compressionPreset, text *string) {
	locked := s.settingsDraftLocked()
	enabled := preset.targetResizeMinDimension != 0
	toggledOn := false
	if ui.Checkbox(c.Key("preset-target-resize"), &enabled, "超出目标时自动缩小").
		FontSize(settingsFontSize(c, 13)).Disabled(locked).Changed() {
		if enabled {
			toggledOn = true
			preset.targetResizeMinDimension = defaultPresetTargetResizeMinDimension
			if floor, err := parseTargetResizeMinDimension(*text); err == nil {
				preset.targetResizeMinDimension = floor
			}
		} else {
			preset.targetResizeMinDimension = 0
		}
		s.editor.message = ""
	}
	if !enabled {
		return
	}

	floor, problem := parseTargetResizeMinDimension(*text)
	var input ui.Element
	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
		label := ui.Text(c, "长边下限（px）").FontSize(settingsFontSize(c, 12)).TextColor(metadataColor(c)).Grow(1)
		input = ui.TextInput(c.Key(presetTargetResizeMinField), text).LabelledBy(label).
			Width(settingsFontSize(c, 112)).Height(settingsControlHeight(c, 32)).
			FontSize(settingsFontSize(c, 13)).FontFeatures("tnum").Padding(0, 12).Disabled(locked)
		styleGlassField(input, c, 32)
		s.focusSettingsField(input, presetTargetResizeMinField)
		if toggledOn {
			input.ScrollIntoView()
		}
		if input.Changed() {
			floor, problem = parseTargetResizeMinDimension(*text)
			if problem == nil {
				preset.targetResizeMinDimension = floor
			}
			s.editor.message = ""
		}
	})
	if problem != nil {
		danger := c.Theme().Danger
		input.Invalid(true).Border(1, danger).ErrorMessage(ui.Text(c, problem.Error()).
			FontSize(settingsFontSize(c, 12)).TextColor(danger).FillWidth().MaxLines(2))
	}
	note := "手动缩放后，若体积仍超出目标且长边高于下限，会尝试继续等比缩小。"
	switch {
	case preset.lossless:
		note = "无损模式下暂停自动缩小。"
	case preset.targetSizeBytes == 0:
		note = "设置目标体积后生效。"
	case problem == nil && preset.maxDimension > 0:
		editor := s.editor
		limits, err := parseResizeValue(resizeLongEdge, editor.targets[editor.selected].resizeText)
		if err == nil && limits.maxDimension <= floor {
			note = "手动缩放后的长边已达到或低于下限，自动缩小将保留该尺寸。"
		}
	}
	ui.Text(c, note).FontSize(settingsFontSize(c, 12)).TextColor(metadataColor(c)).MaxLines(2)
}

func presetSaveSegments(c *ui.Context, options *presetSaveOptions, disabled bool) bool {
	mode := 0
	if options.Mode == presetSaveAsk {
		mode = 1
	} else if options.Mode == presetSaveFixed {
		mode = 2
	}
	segments := settingsSegments(c, "preset-save-mode", "保存到", &mode, disabled, "原文件夹", "每次询问", "固定输出目录").
		FillWidth()
	if !segments.Changed() {
		return false
	}
	options.Mode = [...]presetSaveMode{presetSaveOriginal, presetSaveAsk, presetSaveFixed}[mode]
	return true
}

func (s *application) presetSaveCard(c *ui.Context, preset *compressionPreset) {
	t := c.Theme()
	options := &preset.output
	locked := s.settingsDraftLocked()
	replaces := options.replacesOriginal()
	inlineReplaceBadge := replaces && preset.outputFormat == compress.OutputFormatKeep
	settingsPanel(c, "settings-output-panel").Padding(11, 14).Gap(8).Children(func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, presetSaveIcon).Size(15, 15).TextColor(metadataColor(c))
			ui.Text(c, "保存到").FontSize(settingsFontSize(c, 14)).FontWeight(600).Role(ui.RoleHeading).Level(2).Grow(1)
			if inlineReplaceBadge {
				statusCapsule(c, presetSaveDescription(*options), t.Danger).FontSize(settingsFontSize(c, 12))
			}
		})
		if replaces && !inlineReplaceBadge {
			format := strings.TrimPrefix(presetOutputFormatLabel(preset.outputFormat), "转为 ")
			warning := fmt.Sprintf("同格式 %s 文件缩小后将被替换；其他格式另存为 %s", format, outputExtensionForFormat(string(preset.outputFormat)))
			ui.Row(c).FillWidth().Padding(7, 10).Radius(innerRadius(c)).
				Background(statusTintSurface(c, t.Danger)).Border(1, statusTintBorder(c, t.Danger)).Children(func() {
				ui.Text(c, warning).FontSize(settingsFontSize(c, 12)).FontWeight(500).
					TextColor(t.Danger).FillWidth().MaxLines(2)
			})
		}
		if presetSaveSegments(c, options, locked) {
			s.editor.message = ""
		}
		if options.Mode == presetSaveFixed {
			ui.Row(c).Gap(8).Padding(6, 10).Radius(innerRadius(c)).Background(tonalWellSurface(c)).Border(1, tonalWellBorder(c)).
				AlignItems(ui.Center).Children(func() {
				path := options.Directory
				pathColor := t.Text
				if path == "" {
					path = "未选择文件夹"
					pathColor = metadataColor(c)
				}
				ui.Text(c, path).FontSize(settingsFontSize(c, 12)).TextColor(pathColor).Width(0).Grow(1).
					SingleLine().TruncateMiddle().Tooltip(path)
				directory := settingsButton(c, "preset-output-directory", "选择文件夹…", 28, locked).
					FontSize(settingsFontSize(c, 12)).Padding(0, 10).
					OnClick(s.choosePresetOutputDirectory)
				s.focusSettingsField(directory, "preset-output-directory")
			})
		}
		if options.supportsSubfolder() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				if ui.Checkbox(c.Key("preset-subfolder"), &options.UseSubfolder, "保存到子文件夹").
					FontSize(settingsFontSize(c, 13)).Width(0).Grow(1).MinWidth(0).Disabled(locked).Changed() {
					if options.UseSubfolder && options.Subfolder == "" {
						options.Subfolder = defaultSubfolderName
					}
					s.editor.message = ""
				}
				subField := ui.TextInput(c.Key("preset-subfolder-name"), &options.Subfolder).Label("子文件夹名称").
					Placeholder(defaultSubfolderName).FontSize(settingsFontSize(c, 13)).Width(148).Height(settingsControlHeight(c, 32)).
					Padding(0, 12).Disabled(locked || !options.UseSubfolder)
				styleGlassField(subField, c, 32)
				s.focusSettingsField(subField, "preset-subfolder-name")
				if subField.Changed() {
					s.editor.message = ""
				}
			})
		}
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			if ui.Checkbox(c.Key("preset-suffix"), &options.UseSuffix, "添加后缀").
				FontSize(settingsFontSize(c, 13)).Width(0).Grow(1).MinWidth(0).Disabled(locked).Changed() {
				if options.UseSuffix && options.Suffix == "" {
					options.Suffix = defaultFilenameSuffix
				}
				s.editor.message = ""
			}
			sufField := ui.TextInput(c.Key("preset-suffix-value"), &options.Suffix).Label("文件名后缀").
				Placeholder(defaultFilenameSuffix).FontSize(settingsFontSize(c, 13)).Width(148).Height(settingsControlHeight(c, 32)).
				Padding(0, 12).Disabled(locked || !options.UseSuffix)
			styleGlassField(sufField, c, 32)
			s.focusSettingsField(sufField, "preset-suffix-value")
			if sufField.Changed() {
				s.editor.message = ""
			}
		})
		example := presetOutputExampleForFormat(*options, preset.outputFormat)
		ui.Row(c).FillWidth().Gap(8).Padding(6, 10).Radius(innerRadius(c)).
			Background(tonalWellSurface(c)).Border(1, tonalWellBorder(c)).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "保存示例").FontSize(settingsFontSize(c, 12)).FontWeight(500).TextColor(metadataColor(c)).Shrink(0)
			ui.Box(c).Size(1, 14).Background(hairlineDivider(c))
			ui.Text(c, example).Label("保存示例：" + example).FontSize(settingsFontSize(c, 12)).FontWeight(500).FontFeatures("tnum").
				TextColor(t.Text).Width(0).Grow(1).MinWidth(0).SingleLine().TruncateMiddle().Tooltip(example)
		})
		if _, err := options.validateField(); err != nil {
			ui.Text(c, err.Error()).FontSize(settingsFontSize(c, 12)).TextColor(t.Danger).FillWidth().MaxLines(2)
		}
	})
}

func (s *application) choosePresetOutputDirectory() {
	if s.settingsDraftLocked() || s.editor.selectedPreset() == nil {
		return
	}
	editor, index, parent := s.editor, s.editor.selected, s.settingsWindow
	defaultPath := editor.presets[index].output.Directory
	s.busy = true
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent: parent, Title: "选择固定输出目录", ButtonLabel: "选择此文件夹",
			DefaultPath: defaultPath, Directory: true, CreateDirectories: true,
		})
		s.update(func() {
			s.busy = false
			if s.editor != editor {
				return
			}
			if err != nil {
				editor.message = "无法选择输出目录：" + err.Error()
			} else if len(paths) != 0 {
				editor.presets[index].output.Directory = paths[0]
				editor.message = ""
			}
			if s.settingsWindow != nil {
				s.settingsWindow.Invalidate()
			}
		})
	}()
}

func (s *application) focusSettingsField(field ui.Element, key string) {
	if s.editor.focusField == key && !s.settingsDraftLocked() {
		field.Focus()
		s.editor.focusField = ""
	}
}
