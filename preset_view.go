package main

import (
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

var (
	presetPlusIcon    = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"><path d="M8 3.25v9.5M3.25 8h9.5"/></svg>`))
	presetCheckIcon   = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 12 12" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="M2.5 6.2 4.9 8.6 9.5 3.6"/></svg>`))
	presetQualityIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"><path d="M2 5h2.5m3.5 0h6M2 11h6m3.5 0H14"/><circle cx="6.25" cy="5" r="1.75"/><circle cx="9.75" cy="11" r="1.75"/></svg>`))
	presetSaveIcon    = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="M2.5 4.25A1.25 1.25 0 0 1 3.75 3h2.6l1.4 1.5h4.5A1.25 1.25 0 0 1 13.5 5.75v6A1.25 1.25 0 0 1 12.25 13h-8.5A1.25 1.25 0 0 1 2.5 11.75Z"/></svg>`))
	presetInfoIcon    = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"><circle cx="8" cy="8" r="6"/><path d="M8 7.25v3.5M8 5.1v.15"/></svg>`))
)

func presetActiveIndicator(c *ui.Context) {
	ui.Icon(c, presetCheckIcon).Size(16, 16).Shrink(0).TextColor(accentForeground(c.Theme())).
		AccessHidden(true).Tooltip("当前预设")
}

func presetQualityLabel(preset compressionPreset) string {
	level := qualityLevel(preset)
	if preset.quality != imageQualityLevels[level].quality || preset.png != imageQualityLevels[level].png {
		return "自定义质量"
	}
	return fmt.Sprintf("%d · %s", level, imageQualityLevels[level].label)
}

func presetDisplayName(preset compressionPreset) string {
	if name := strings.TrimSpace(preset.name); name != "" {
		return name
	}
	return "未命名预设"
}

func presetOutputExample(options presetSaveOptions) string {
	return presetOutputName(options, "example.png")
}

func presetOutputName(options presetSaveOptions, filename string) string {
	if options.Mode == presetSaveOriginal {
		filename = filepath.Base(filename)
	}
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
		ui.Column(c).FillWidth().Padding(12, bodyPad, 0, bodyPad).Children(func() { s.presetEditorTabs(c) })
		ui.Scroll(c.Key("preset-editor")).Grow(1).MinHeight(0).Padding(16, bodyPad, 20, bodyPad).Gap(20).Children(func() {
			preset := editor.selectedPreset()
			ui.Column(c).FillWidth().Gap(8).Children(func() {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
					ui.Text(c, "预设名称").FontSize(settingsFontSize(c, 13)).FontWeight(500).Grow(1)
					color := metadataColor(c)
					if utf8.RuneCountInString(preset.name) > 40 {
						color = c.Theme().Danger
					}
					ui.Textf(c, "%d / 40", utf8.RuneCountInString(preset.name)).FontSize(settingsFontSize(c, 12)).
						FontFeatures("tnum").TextColor(color).Description("预设名称最多 40 个字符")
				})
				var name ui.Element
				ui.Row(c).FillWidth().Height(settingsControlHeight(c, 36)).Gap(8).AlignItems(ui.Center).Children(func() {
					name = ui.TextInput(c.Key("preset-name"), &preset.name).Label("预设名称").
						Placeholder("输入预设名称").Description("最多 40 个字符，名称不能重复；保存时去除首尾空格").Height(settingsControlHeight(c, 36)).
						Width(0).Grow(1).MinWidth(0).FontSize(settingsFontSize(c, 14)).Padding(0, 12).Disabled(locked)
					styleGlassField(name, c, 36)
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
					if editor.active == editor.selected {
						ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
							presetActiveIndicator(c)
							ui.Text(c, "当前预设").FontSize(settingsFontSize(c, 12)).TextColor(c.Theme().TextMuted).SingleLine()
						})
					} else {
						settingsButton(c, "activate-preset", "设为当前预设", 32, s.busy || editor.confirmDelete).
							OnClick(func() { s.selectSettingsPreset(editor.selected) })
					}
				})
				if problem := editor.nameProblem(); problem != "" {
					name.Invalid(true).ErrorMessage(ui.Text(c, problem).FontSize(settingsFontSize(c, 12)).TextColor(c.Theme().Danger).
						FillWidth().MaxLines(2))
				}
			})
			s.presetQualityCard(c, *preset)
			s.presetSaveCard(c, preset)
		})
	})
}

func (s *application) presetEditorTabs(c *ui.Context) {
	t := c.Theme()
	editor := s.editor
	locked := s.settingsDraftLocked()
	ui.Row(c).FillWidth().Height(settingsControlHeight(c, 36)).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.ScrollHorizontal(c.Key("preset-list")).Height(settingsControlHeight(c, 36)).Width(0).Grow(1).Children(func() {
			tabs := ui.TabsBase(c.Key("preset-tabs"), &editor.selected, len(editor.presets))
			shown := ui.Local(tabs.List, "shown-selection", func() int { return -1 })
			tabs.List.Label("编辑预设").Gap(4).Height(settingsControlHeight(c, 36)).AlignItems(ui.Center).Disabled(locked).Children(func() {
				for index, preset := range editor.presets {
					name := presetDisplayName(preset)
					button := tabs.Tab(index).
						Label("编辑预设："+name).Tooltip(name).Height(settingsControlHeight(c, 36)).
						MinWidth(88).MaxWidth(196).Padding(0, 12).Gap(8).Radius(innerRadius(c)).
						Border(1, ui.Transparent).AlignItems(ui.Center).
						TextColor(t.TextMuted).Disabled(locked).AccessSelected(editor.selected == index)
					if editor.active == index {
						button.Description("当前使用的预设")
					}
					if editor.selected == index {
						button.Background(glassSelectionBackground(c)).Border(1, selectionBorder(c)).TextColor(t.Text)
						settingsGlassControl(button, c, 36, locked, t.Accent.Alpha(0.14))
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
		addBtn := ui.ButtonBase(c.Key("add-preset")).Size(settingsControlHeight(c, 32), settingsControlHeight(c, 32)).Center().Radius(innerRadius(c)).
			Background(glassControlSurface(c)).Border(1, controlBorder(c)).
			Label("新增预设").Tooltip("新增预设").Disabled(locked).OnClick(s.addPreset)
		if !locked {
			if addBtn.Pressed() {
				addBtn.Background(glassControlPressed(c))
			} else if addBtn.Hovered() {
				addBtn.Background(glassControlHover(c))
			}
		}
		settingsGlassControl(addBtn, c, 32, locked, ui.Transparent)
		addBtn.Children(func() { ui.Icon(c, presetPlusIcon).Size(16, 16).TextColor(t.Text) })
	})
}

func (s *application) presetQualityCard(c *ui.Context, preset compressionPreset) {
	t := c.Theme()
	settingsPanel(c, "settings-quality-panel").Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, presetQualityIcon).Size(16, 16).TextColor(t.TextMuted)
			ui.Text(c, "图片质量").FontSize(settingsFontSize(c, 16)).FontWeight(600).Role(ui.RoleHeading).Level(2)
			ui.Icon(c, presetInfoIcon).Size(16, 16).TextColor(t.TextMuted).
				Tooltip("压缩参数随图片质量自动调整。质量越高，越接近原图；质量越低，文件通常越小。")
			ui.Box(c).Grow(1)
			ui.Text(c, presetQualityLabel(preset)).FontSize(settingsFontSize(c, 13)).FontWeight(500).
				FontFeatures("tnum").TextColor(t.Text).SingleLine()
		})
		value := float64(qualityLevel(preset))
		description := fmt.Sprintf("JPEG / WebP 质量 %.0f；PNG 质量 %d–%d，最多 %d 色。左右方向键调整，Home / End 跳到两端。", preset.quality,
			preset.png.MinQuality, preset.png.MaxQuality, preset.png.Colors)
		if presetQualityLabel(preset) == "自定义质量" {
			description = "已保留自定义参数；调整滑块后将使用对应档位。" + description
		}
		slider := ui.SliderBase(c.Key("quality-level"), &value, 0, float64(len(imageQualityLevels)-1)).Step(1).
			Label("图片质量").Value(presetQualityLabel(preset)).Description(description).Tooltip(description).
			Height(settingsControlHeight(c, 32)).PaddingX(12).Disabled(s.settingsDraftLocked())
		s.focusSettingsField(slider, "quality-level")
		at := float32(value / float64(len(imageQualityLevels)-1))
		slider.Draw(func(p *ui.Painter, r ui.Rect) {
			centerY := r.Y + r.H/2
			track := ui.Rect{X: r.X + 12, Y: centerY - 2, W: max(0, r.W-24), H: 4}
			p.Fill(track, meterTrackColor(c), 2)
			fill := track
			fill.W *= at
			p.Fill(fill, t.Accent, 2)
			activeIndex := int(math.Round(value))
			for tick := range len(imageQualityLevels) {
				x := track.X + track.W*float32(tick)/float32(len(imageQualityLevels)-1)
				tickColor := meterTrackColor(c)
				if tick <= activeIndex {
					tickColor = accentForeground(t).Alpha(0.6)
				}
				p.Line(x, centerY+8, x, centerY+12, 1.5, tickColor)
			}
			thumb := ui.Rect{X: track.X + track.W*at - 10, Y: centerY - 10, W: 20, H: 20}
			p.Shadow(thumb, 10, 0, 1, 3, 0, ui.RGBA(0, 0, 0, 0.20))
			p.Fill(thumb, ui.Hex("#ffffff"), 10)
			p.Stroke(thumb, selectionBorder(c), 10, 1.25)
		})
		if slider.Changed() {
			s.editQualityLevel(int(math.Round(value)))
		}
		ui.Row(c).Children(func() {
			ui.Text(c, "文件更小").FontSize(settingsFontSize(c, 12)).TextColor(metadataColor(c)).Grow(1)
			ui.Text(c, "质量更高").FontSize(settingsFontSize(c, 12)).TextColor(metadataColor(c))
		})
	})
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
	settingsPanel(c, "settings-output-panel").Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, presetSaveIcon).Size(16, 16).TextColor(t.TextMuted)
			ui.Text(c, "保存到").FontSize(settingsFontSize(c, 16)).FontWeight(600).Role(ui.RoleHeading).Level(2)
		})
		if options.replacesOriginal() {
			ui.Text(c, presetSaveDescription(*options)).FontSize(settingsFontSize(c, 12)).
				TextColor(t.Danger).FillWidth().MaxLines(2)
		}
		if presetSaveSegments(c, options, locked) {
			s.editor.message = ""
		}
		if options.Mode == presetSaveFixed {
			ui.Row(c).Gap(8).Padding(8, 12).Radius(innerRadius(c)).Background(panelSubtleSurface(c)).Border(1, glassBorder(c)).
				AlignItems(ui.Center).Children(func() {
				path := options.Directory
				if path == "" {
					path = "尚未选择；保存图片前需选择目录"
				}
				ui.Text(c, path).FontSize(settingsFontSize(c, 12)).TextColor(t.TextMuted).Width(0).Grow(1).
					SingleLine().TruncateMiddle().Tooltip(path)
				directory := settingsButton(c, "preset-output-directory", "选择文件夹…", 32, locked).
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
		description := presetSaveDescription(*options)
		if options.Mode != presetSaveFixed && !options.replacesOriginal() {
			ui.Text(c, description).FontSize(settingsFontSize(c, 12)).TextColor(metadataColor(c)).
				FillWidth().MaxLines(2).Tooltip(description)
		}
		example := presetOutputExample(*options)
		ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "保存示例").FontSize(settingsFontSize(c, 12)).TextColor(metadataColor(c)).Shrink(0)
			ui.Text(c, example).Label("保存示例：" + example).FontSize(settingsFontSize(c, 12)).FontFeatures("tnum").
				Width(0).Grow(1).MinWidth(0).SingleLine().TruncateMiddle().Tooltip(example)
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
