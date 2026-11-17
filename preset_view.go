package main

import (
	"fmt"
	"math"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

var (
	presetPlusIcon        = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"><path d="M8 3.25v9.5M3.25 8h9.5"/></svg>`))
	presetCheckIcon       = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 12 12" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="M2.5 6.2 4.9 8.6 9.5 3.6"/></svg>`))
	presetQualityIcon     = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"><path d="M2 5h2.5m3.5 0h6M2 11h6m3.5 0H14"/><circle cx="6.25" cy="5" r="1.75"/><circle cx="9.75" cy="11" r="1.75"/></svg>`))
	presetSaveIcon        = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="M2.5 4.25A1.25 1.25 0 0 1 3.75 3h2.6l1.4 1.5h4.5A1.25 1.25 0 0 1 13.5 5.75v6A1.25 1.25 0 0 1 12.25 13h-8.5A1.25 1.25 0 0 1 2.5 11.75Z"/></svg>`))
	presetInfoIcon        = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"><circle cx="8" cy="8" r="6"/><path d="M8 7.25v3.5M8 5.1v.15"/></svg>`))
	presetHeartIcon       = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round"><path d="M13.6 3.4a3.5 3.5 0 0 0-4.95 0L8 4.05l-.65-.65a3.5 3.5 0 0 0-4.95 4.95L8 13.95l5.6-5.6a3.5 3.5 0 0 0 0-4.95Z"/></svg>`))
	presetHeartFilledIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="currentColor"><path d="M13.6 3.4a3.5 3.5 0 0 0-4.95 0L8 4.05l-.65-.65a3.5 3.5 0 0 0-4.95 4.95L8 13.95l5.6-5.6a3.5 3.5 0 0 0 0-4.95Z"/></svg>`))
)

func presetActiveIndicator(c *ui.Context) {
	t := c.Theme()
	ui.Box(c).Size(16, 16).Radius(4).Background(selectionBackground(c)).
		Border(1, selectionBorder(c)).Center().Children(func() {
		ui.Icon(c, presetCheckIcon).Size(12, 12).TextColor(t.Accent)
	})
}

func presetQualityLabel(preset compressionPreset) string {
	level := qualityLevel(preset)
	if preset.quality != imageQualityLevels[level].quality || preset.png != imageQualityLevels[level].png {
		return "自定义质量"
	}
	return fmt.Sprintf("%d · %s", level, imageQualityLevels[level].label)
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

func (s *application) settingsView(c *ui.Context) {
	s.applyTheme(c)
	editor := s.editor
	if editor == nil || editor.selectedPreset() == nil {
		return
	}
	t := c.Theme()
	width, _ := c.Size()
	compact := width < 560
	bodyPad := float32(24)
	if compact {
		bodyPad = 16
	}
	c.OnWindowShortcut(0, ui.KeyEscape, func() { s.setSettingsOpen(false) })
	c.OnWindowShortcut(ui.Cmd, ui.KeyS, s.savePresetEdits)
	ui.Column(c).Fill().Background(t.Background).Role(ui.RoleDialog).Label("预设设置").Children(func() {
		s.presetEditorTabs(c)
		ui.Box(c).FillWidth().Height(1).Background(t.Border)
		ui.Scroll(c.Key("preset-editor")).Grow(1).MinHeight(0).Padding(bodyPad).Gap(16).Children(func() {
			preset := editor.selectedPreset()
			ui.Row(c).Height(36).Gap(8).AlignItems(ui.Center).Children(func() {
				if ui.TextInput(c.Key("preset-name"), &preset.name).Label("预设名称").
					Placeholder("输入预设名称").Tooltip("预设名称，点击重命名").Height(36).
					Width(0).Grow(1).FontSize(18).FontWeight(600).Padding(0).
					Background(ui.Transparent).Border(0, ui.Transparent).Disabled(s.busy).Changed() {
					editor.message = ""
				}
				icon, label := presetHeartIcon, "收藏预设"
				if preset.favorite {
					icon, label = presetHeartFilledIcon, "取消收藏预设"
				}
				button := ui.ButtonBase(c.Key("favorite-preset")).Size(32, 32).Center().Radius(8).
					Background(t.Surface).Border(1, t.Border).
					Label(label).Tooltip(label).Disabled(s.busy).OnClick(func() { preset.favorite = !preset.favorite })
				if button.Hovered() && !s.busy {
					button.Background(t.SurfaceHover)
				}
				if preset.favorite {
					button.TextColor(t.Accent).Background(selectionBackground(c)).Border(1, selectionBorder(c))
				} else {
					button.TextColor(t.TextMuted)
				}
				button.Children(func() { ui.Icon(c, icon).Size(16, 16) })
			})
			s.presetQualityCard(c, *preset)
			s.presetSaveCard(c, preset, compact)
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				if editor.active == editor.selected {
					presetActiveIndicator(c)
					ui.Text(c, "当前使用的预设").FontSize(12).TextColor(t.TextMuted)
				} else {
					quietButton(c, "activate-preset", "设为当前预设", 32, s.busy).
						OnClick(func() { editor.active = editor.selected })
				}
			})
		})
		ui.Box(c).FillWidth().Height(1).Background(t.Border)
		if editor.confirmDelete {
			ui.Row(c).Padding(12, bodyPad).Gap(8).Background(subtleSurface(c)).AlignItems(ui.Center).Children(func() {
				ui.Textf(c, "删除「%s」？保存后生效。", editor.selectedPreset().name).
					FontSize(12).Width(0).Grow(1).SingleLine().TruncateMiddle()
				quietButton(c, "cancel-delete", "保留预设", 32, false).
					OnClick(func() { editor.confirmDelete = false })
				quietButton(c, "confirm-delete", "确认删除", 32, false).TextColor(t.Danger).
					OnClick(s.deletePreset)
			})
			ui.Box(c).FillWidth().Height(1).Background(t.Border)
		}
		ui.Column(c).Padding(12, bodyPad).Gap(8).Background(t.Surface).Children(func() {
			message, color, role := "保存后生效，关闭应用仍会保留", t.TextMuted, ui.RoleStatus
			if editor.message != "" {
				message, color, role = editor.message, t.Danger, ui.RoleAlert
			}
			ui.Text(c, message).FontSize(12).TextColor(color).Role(role).MaxLines(2).Tooltip(message)
			ui.Row(c).Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
				quietButton(c, "duplicate-preset", "复制预设", 36, s.busy).
					OnClick(s.duplicatePreset)
				quietButton(c, "delete-preset", "删除预设", 36, s.busy || len(editor.presets) <= 1).
					TextColor(t.Danger).OnClick(s.requestDeletePreset)
				ui.Box(c).Grow(1)
				quietButton(c, "cancel-settings", "取消", 36, s.busy).Width(72).
					OnClick(func() { s.setSettingsOpen(false) })
				primaryAction(c, "save-settings", "保存", s.busy || editor.confirmDelete).Width(88).Height(36).
					KeyShortcuts(ui.Cmd, ui.KeyS).OnClick(s.savePresetEdits)
			})
		})
	})
}

func (s *application) presetEditorTabs(c *ui.Context) {
	t := c.Theme()
	editor := s.editor
	ui.Row(c).Height(52).Padding(8, 16).Gap(8).Background(t.SurfaceHover).AlignItems(ui.Center).
		Border(0, ui.Transparent).Children(func() {
		ui.Text(c, "预设").FontSize(13).FontWeight(500).TextColor(metadataColor(c)).Padding(0, 4)
		ui.ScrollHorizontal(c.Key("preset-list")).Height(36).Width(0).Grow(1).Children(func() {
			ui.Row(c).Gap(4).Height(36).AlignItems(ui.Center).Children(func() {
				for index, preset := range editor.presets {
					button := ui.ButtonBase(c.Key(fmt.Sprintf("edit-preset-%d", index))).
						Label("编辑预设："+preset.name).Tooltip(preset.name).Height(36).
						MinWidth(88).MaxWidth(196).Padding(0, 12).Gap(8).Radius(8).
						Border(1, ui.Transparent).AlignItems(ui.Center).
						TextColor(t.TextMuted).Disabled(s.busy).OnClick(func() {
						editor.selected = index
						editor.message, editor.confirmDelete = "", false
					})
					if editor.selected == index {
						button.Background(t.Surface).Border(1, t.Border).TextColor(t.Text)
					} else if button.Hovered() {
						button.Background(t.SurfacePressed.Alpha(0.65)).TextColor(t.Text)
					}
					button.Children(func() {
						weight := 400
						if editor.selected == index {
							weight = 500
						}
						ui.Text(c, preset.name).FontSize(13).FontWeight(weight).SingleLine().TruncateMiddle()
						if editor.active == index {
							presetActiveIndicator(c)
						}
					})
				}
			})
		})
		addBtn := ui.ButtonBase(c.Key("add-preset")).Size(32, 32).Center().Radius(8).
			Background(t.Surface).Border(1, t.Border).
			Label("新增预设").Tooltip("新增预设").Disabled(s.busy).OnClick(s.addPreset)
		if addBtn.Hovered() && !s.busy {
			addBtn.Background(t.SurfacePressed)
		}
		addBtn.Children(func() { ui.Icon(c, presetPlusIcon).Size(16, 16).TextColor(t.Text) })
	})
}

func (s *application) presetQualityCard(c *ui.Context, preset compressionPreset) {
	t := c.Theme()
	ui.Column(c).FillWidth().Padding(16).Gap(12).Radius(12).
		Background(t.Surface).Border(1, t.Border).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, presetQualityIcon).Size(16, 16).TextColor(t.TextMuted)
			ui.Text(c, "图片质量").FontSize(15).FontWeight(500)
			ui.Icon(c, presetInfoIcon).Size(16, 16).TextColor(t.TextMuted).
				Tooltip("压缩参数随图片质量自动调整。质量越高，越接近原图；质量越低，文件通常越小。")
			ui.Box(c).Grow(1)
			ui.Text(c, presetQualityLabel(preset)).FontSize(13).FontWeight(500).
				FontFeatures("tnum").TextColor(t.Text).SingleLine()
		})
		value := float64(qualityLevel(preset))
		slider := ui.SliderBase(c.Key("quality-level"), &value, 0, float64(len(imageQualityLevels)-1)).Step(1).
			Label("图片质量").Height(32).PaddingX(12).Disabled(s.busy)
		at := float32(value / float64(len(imageQualityLevels)-1))
		slider.Draw(func(p *ui.Painter, r ui.Rect) {
			track := ui.Rect{X: r.X + 12, Y: r.Y + 12, W: max(0, r.W-24), H: 4}
			p.Fill(track, t.SurfacePressed, 2)
			fill := track
			fill.W *= at
			p.Fill(fill, t.Accent, 2)
			for tick := range len(imageQualityLevels) {
				x := track.X + track.W*float32(tick)/float32(len(imageQualityLevels)-1)
				p.Line(x, r.Y+22, x, r.Y+26, 1.5, t.TextMuted.Alpha(0.45))
			}
			thumb := ui.Rect{X: track.X + track.W*at - 10, Y: r.Y + 4, W: 20, H: 20}
			p.Fill(thumb, ui.Hex("#ffffff"), 10)
			p.Stroke(thumb, t.Text.Alpha(0.18), 10, 1)
		})
		if slider.Changed() {
			s.editQualityLevel(int(math.Round(value)))
		}
		ui.Row(c).Children(func() {
			ui.Text(c, "文件更小").FontSize(12).TextColor(metadataColor(c)).Grow(1)
			ui.Text(c, "质量更高").FontSize(12).TextColor(metadataColor(c))
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
	t := c.Theme()
	parts := ui.SegmentedBase(c.Key("preset-save-mode"), &mode, 3)
	parts.Track.Label("保存到").FillWidth().Height(36).Padding(4).Radius(8).
		Background(stageBackground(c)).Border(1, t.Border).AlignItems(ui.Stretch).Disabled(disabled).Children(func() {
		for index, label := range [...]string{"原文件夹", "每次询问", "固定输出目录"} {
			segment := parts.Segment(index).Label(label).Radius(4).Center().
				Border(1, ui.Transparent).TextColor(t.TextMuted)
			weight := 400
			if mode == index {
				segment.Background(t.Surface).Border(1, selectionBorder(c)).TextColor(t.Text)
				weight = 500
			}
			segment.Children(func() { ui.Text(c, label).FontSize(13).FontWeight(weight).SingleLine() })
		}
	})
	parts.Track.FillEqually()
	if !parts.Track.Changed() {
		return false
	}
	options.Mode = [...]presetSaveMode{presetSaveOriginal, presetSaveAsk, presetSaveFixed}[mode]
	return true
}

func (s *application) presetSaveCard(c *ui.Context, preset *compressionPreset, compact bool) {
	t := c.Theme()
	options := &preset.output
	ui.Column(c).FillWidth().Padding(16).Gap(12).Radius(12).
		Background(t.Surface).Border(1, t.Border).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, presetSaveIcon).Size(16, 16).TextColor(t.TextMuted)
			ui.Text(c, "保存到").FontSize(15).FontWeight(500)
			ui.Box(c).Grow(1)
			ui.Text(c, presetSaveDescription(*options)).FontSize(12).TextColor(metadataColor(c)).
				MaxWidth(380).SingleLine().TruncateMiddle().Tooltip(presetSaveDescription(*options))
		})
		if presetSaveSegments(c, options, s.busy) {
			s.editor.message = ""
		}
		if options.Mode == presetSaveFixed {
			ui.Row(c).Gap(8).Padding(8, 12).Radius(8).Background(subtleSurface(c)).Border(1, t.Border).
				AlignItems(ui.Center).Children(func() {
				path := options.Directory
				if path == "" {
					path = "尚未选择输出目录"
				}
				ui.Text(c, path).FontSize(12).TextColor(t.TextMuted).Width(0).Grow(1).
					SingleLine().TruncateMiddle().Tooltip(path)
				quietButton(c, "preset-output-directory", "选择文件夹…", 32, s.busy).
					OnClick(s.choosePresetOutputDirectory)
			})
		}
		if options.supportsSubfolder() {
			row := ui.Row(c).Gap(12).AlignItems(ui.Center)
			if compact {
				row.Wrap().Gap(8)
			}
			row.Children(func() {
				if ui.Checkbox(c.Key("preset-subfolder"), &options.UseSubfolder, "保存到子文件夹").
					Width(156).Disabled(s.busy).Changed() {
					if options.UseSubfolder && options.Subfolder == "" {
						options.Subfolder = defaultSubfolderName
					}
					s.editor.message = ""
				}
				folder := options.Subfolder
				if folder == "" {
					folder = defaultSubfolderName
				}
				ui.Text(c, folder+"/example.png").FontSize(12).FontFeatures("tnum").
					TextColor(metadataColor(c)).Width(0).Grow(1).MinWidth(120).SingleLine().TruncateMiddle()
				if ui.TextInput(c.Key("preset-subfolder-name"), &options.Subfolder).Label("子文件夹名称").
					Placeholder(defaultSubfolderName).Width(148).Height(32).Disabled(s.busy || !options.UseSubfolder).Changed() {
					s.editor.message = ""
				}
			})
		}
		suffixRow := ui.Row(c).Gap(12).AlignItems(ui.Center)
		if compact {
			suffixRow.Wrap().Gap(8)
		}
		suffixRow.Children(func() {
			if ui.Checkbox(c.Key("preset-suffix"), &options.UseSuffix, "添加后缀").
				Width(156).Disabled(s.busy).Changed() {
				if options.UseSuffix && options.Suffix == "" {
					options.Suffix = defaultFilenameSuffix
				}
				s.editor.message = ""
			}
			suffix := options.Suffix
			if suffix == "" {
				suffix = defaultFilenameSuffix
			}
			ui.Text(c, "example"+suffix+".png").FontSize(12).FontFeatures("tnum").
				TextColor(metadataColor(c)).Width(0).Grow(1).MinWidth(120).SingleLine().TruncateMiddle()
			if ui.TextInput(c.Key("preset-suffix-value"), &options.Suffix).Label("文件名后缀").
				Placeholder(defaultFilenameSuffix).Width(148).Height(32).Disabled(s.busy || !options.UseSuffix).Changed() {
				s.editor.message = ""
			}
		})
	})
}

func (s *application) choosePresetOutputDirectory() {
	if s.busy || s.editor == nil || s.editor.selectedPreset() == nil {
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
