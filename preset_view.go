package main

import (
	"fmt"
	"math"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

var (
	presetPlusIcon        = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M12 5v14M5 12h14"/></svg>`))
	presetCheckIcon       = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="m6 12 4 4 8-9"/></svg>`))
	presetQualityIcon     = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="10" width="18" height="4" rx="2"/><path d="M12 2v5m-3-3 3 3 3-3M12 22v-5m-3 3 3-3 3 3"/></svg>`))
	presetSaveIcon        = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3v11m-4-4 4 4 4-4M7 7H5l-2 7v6h18v-6l-2-7h-2M3 14h5l2 3h4l2-3h5"/></svg>`))
	presetInfoIcon        = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round"><circle cx="12" cy="12" r="9"/><path d="M12 11v6m0-10v.1"/></svg>`))
	presetHeartIcon       = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linejoin="round"><path d="M20.8 4.7a5.5 5.5 0 0 0-7.8 0L12 5.8l-1.1-1.1a5.5 5.5 0 0 0-7.8 7.8L12 21l8.8-8.5a5.5 5.5 0 0 0 0-7.8Z"/></svg>`))
	presetHeartFilledIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor"><path d="M20.8 4.7a5.5 5.5 0 0 0-7.8 0L12 5.8l-1.1-1.1a5.5 5.5 0 0 0-7.8 7.8L12 21l8.8-8.5a5.5 5.5 0 0 0 0-7.8Z"/></svg>`))
)

func presetActiveIndicator(c *ui.Context) {
	ui.Box(c).Size(16, 16).Radius(8).Background(c.Theme().Accent).Center().Children(func() {
		ui.Icon(c, presetCheckIcon).Size(12, 12).TextColor(ui.Hex("#ffffff"))
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
	applyTheme(c)
	editor := s.editor
	if editor == nil || editor.selectedPreset() == nil {
		return
	}
	t := c.Theme()
	c.OnWindowShortcut(0, ui.KeyEscape, func() { s.setSettingsOpen(false) })
	c.OnWindowShortcut(ui.Cmd, ui.KeyS, s.savePresetEdits)
	ui.Column(c).Fill().Background(t.Background).Role(ui.RoleDialog).Label("预设设置").Children(func() {
		s.presetEditorTabs(c)
		ui.Scroll(c.Key("preset-editor")).Grow(1).MinHeight(0).Padding(24).Gap(16).Children(func() {
			preset := editor.selectedPreset()
			ui.Row(c).Height(36).Gap(12).AlignItems(ui.Center).Children(func() {
				if ui.TextInput(c.Key("preset-name"), &preset.name).Label("预设名称").
					Placeholder("输入预设名称").Tooltip("预设名称，点击重命名").Height(36).
					Width(0).Grow(1).FontSize(18).FontWeight(600).Padding(0).
					Background(ui.Transparent).Border(0, ui.Transparent).Disabled(s.busy).Changed() {
					editor.message = ""
				}
				ui.Text(c, presetQualityLabel(*preset)).FontSize(13).TextColor(metadataColor(c)).SingleLine()
				icon, label := presetHeartIcon, "收藏预设"
				if preset.favorite {
					icon, label = presetHeartFilledIcon, "取消收藏预设"
				}
				button := ui.ButtonBase(c.Key("favorite-preset")).Size(32, 32).Center().Radius(8).
					Label(label).Tooltip(label).Disabled(s.busy).OnClick(func() { preset.favorite = !preset.favorite })
				if preset.favorite {
					button.TextColor(t.Accent)
				} else {
					button.TextColor(t.TextMuted)
				}
				button.Children(func() { ui.Icon(c, icon).Size(22, 22) })
			})
			s.presetQualityCard(c, *preset)
			s.presetSaveCard(c, preset)
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				if editor.active == editor.selected {
					presetActiveIndicator(c)
					ui.Text(c, "当前使用的预设").FontSize(12).TextColor(t.TextMuted)
				} else {
					ui.Button(c.Key("activate-preset"), "设为当前预设").Height(32).Disabled(s.busy).
						OnClick(func() { editor.active = editor.selected })
				}
			})
		})
		if editor.confirmDelete {
			ui.Row(c).Padding(12, 24).Gap(8).Background(t.Surface).AlignItems(ui.Center).Children(func() {
				ui.Textf(c, "删除「%s」？保存后生效。", editor.selectedPreset().name).
					FontSize(12).Width(0).Grow(1).SingleLine().TruncateMiddle()
				ui.Button(c.Key("cancel-delete"), "保留预设").Height(32).
					OnClick(func() { editor.confirmDelete = false })
				ui.Button(c.Key("confirm-delete"), "确认删除").Height(32).TextColor(t.Danger).
					OnClick(s.deletePreset)
			})
		}
		ui.Column(c).Padding(16, 24).Gap(12).Background(t.Surface).Children(func() {
			message, color, role := "保存后生效，关闭应用仍会保留", t.TextMuted, ui.RoleStatus
			if editor.message != "" {
				message, color, role = editor.message, t.Danger, ui.RoleAlert
			}
			ui.Text(c, message).FontSize(12).TextColor(color).Role(role).MaxLines(2).Tooltip(message)
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Button(c.Key("duplicate-preset"), "复制预设").Height(36).Disabled(s.busy).
					OnClick(s.duplicatePreset)
				ui.Button(c.Key("delete-preset"), "删除预设").Height(36).TextColor(t.Danger).
					Disabled(s.busy || len(editor.presets) <= 1).OnClick(s.requestDeletePreset)
				ui.Box(c).Grow(1)
				ui.Button(c.Key("cancel-settings"), "取消").Width(72).Height(36).Disabled(s.busy).
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
	ui.Row(c).Height(64).Padding(8, 16, 0).Gap(12).Background(t.SurfaceHover).AlignItems(ui.End).
		Border(0, ui.Transparent).Children(func() {
		ui.Text(c, "预设").FontSize(14).FontWeight(500).TextColor(t.TextMuted).Height(48).Padding(12, 8)
		ui.ScrollHorizontal(c.Key("preset-list")).Height(56).Width(0).Grow(1).Children(func() {
			ui.Row(c).Gap(4).Height(56).AlignItems(ui.End).Children(func() {
				for index, preset := range editor.presets {
					button := ui.ButtonBase(c.Key(fmt.Sprintf("edit-preset-%d", index))).
						Label("编辑预设："+preset.name).Tooltip(preset.name).Height(56).
						MinWidth(100).MaxWidth(200).Padding(12, 16).Gap(8).Radius(14).
						AlignItems(ui.Center).TextColor(t.TextMuted).Disabled(s.busy).OnClick(func() {
						editor.selected = index
						editor.message, editor.confirmDelete = "", false
					})
					if editor.selected == index {
						button.Background(t.Background).TextColor(t.Text)
					} else if button.Hovered() {
						button.Background(t.SurfacePressed)
					}
					button.Children(func() {
						ui.Text(c, preset.name).FontSize(14).FontWeight(600).SingleLine().TruncateMiddle()
						if editor.active == index {
							presetActiveIndicator(c)
						}
					})
				}
			})
		})
		ui.ButtonBase(c.Key("add-preset")).Size(40, 48).Center().Radius(8).
			Label("新增预设").Tooltip("新增预设").Disabled(s.busy).OnClick(s.addPreset).
			Children(func() { ui.Icon(c, presetPlusIcon).Size(24, 24) })
	})
}

func (s *application) presetQualityCard(c *ui.Context, preset compressionPreset) {
	t := c.Theme()
	ui.Column(c).FillWidth().Padding(20).Gap(12).Radius(20).Border(1, t.Border).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, presetQualityIcon).Size(20, 20)
			ui.Text(c, "图片质量").FontSize(16).FontWeight(600)
			ui.Icon(c, presetInfoIcon).Size(16, 16).TextColor(t.TextMuted).
				Tooltip("压缩参数随图片质量自动调整。质量越高，越接近原图；质量越低，文件通常越小。")
			ui.Box(c).Grow(1)
			ui.Text(c, presetQualityLabel(preset)).FontSize(14).FontWeight(500).TextColor(t.TextMuted).SingleLine()
		})
		value := float64(qualityLevel(preset))
		slider := ui.SliderBase(c.Key("quality-level"), &value, 0, float64(len(imageQualityLevels)-1)).Step(1).
			Label("图片质量").Height(36).PaddingX(12).Disabled(s.busy)
		at := float32(value / float64(len(imageQualityLevels)-1))
		slider.Draw(func(p *ui.Painter, r ui.Rect) {
			track := ui.Rect{X: r.X + 12, Y: r.Y + 12, W: max(0, r.W-24), H: 6}
			p.Fill(track, t.SurfacePressed, 3)
			fill := track
			fill.W *= at
			p.Fill(fill, t.Accent, 3)
			for tick := range len(imageQualityLevels) {
				x := track.X + track.W*float32(tick)/float32(len(imageQualityLevels)-1)
				p.Fill(ui.Rect{X: x - 1.5, Y: r.Y + 28, W: 3, H: 3}, t.TextMuted.Alpha(0.5), 1.5)
			}
			thumb := ui.Rect{X: track.X + track.W*at - 12, Y: r.Y + 3, W: 24, H: 24}
			p.Fill(thumb, ui.Hex("#ffffff"), 12)
			p.Stroke(thumb, t.Border, 12, 1)
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
	parts.Track.Label("保存到").FillWidth().Height(44).Padding(3).Radius(12).
		Background(t.SurfaceHover).AlignItems(ui.Stretch).Disabled(disabled).Children(func() {
		for index, label := range [...]string{"原文件夹", "每次询问", "固定输出目录"} {
			segment := parts.Segment(index).Label(label).Radius(10).Center().TextColor(t.TextMuted)
			if mode == index {
				segment.Background(t.Accent).TextColor(ui.Hex("#ffffff"))
			}
			segment.Children(func() { ui.Text(c, label).FontSize(14).FontWeight(600).SingleLine() })
		}
	})
	parts.Track.FillEqually()
	if !parts.Track.Changed() {
		return false
	}
	options.Mode = [...]presetSaveMode{presetSaveOriginal, presetSaveAsk, presetSaveFixed}[mode]
	return true
}

func (s *application) presetSaveCard(c *ui.Context, preset *compressionPreset) {
	t := c.Theme()
	options := &preset.output
	ui.Column(c).FillWidth().Padding(20).Gap(16).Radius(20).Border(1, t.Border).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, presetSaveIcon).Size(20, 20)
			ui.Text(c, "保存到").FontSize(16).FontWeight(600)
			ui.Box(c).Grow(1)
			ui.Text(c, presetSaveDescription(*options)).FontSize(13).TextColor(metadataColor(c)).
				MaxWidth(380).SingleLine().TruncateMiddle().Tooltip(presetSaveDescription(*options))
		})
		if presetSaveSegments(c, options, s.busy) {
			s.editor.message = ""
		}
		if options.Mode == presetSaveFixed {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				path := options.Directory
				if path == "" {
					path = "尚未选择输出目录"
				}
				ui.Text(c, path).FontSize(13).TextColor(t.TextMuted).Width(0).Grow(1).
					SingleLine().TruncateMiddle().Tooltip(path)
				ui.Button(c.Key("preset-output-directory"), "选择文件夹…").Height(32).
					Disabled(s.busy).OnClick(s.choosePresetOutputDirectory)
			})
		}
		if options.supportsSubfolder() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				if ui.Checkbox(c.Key("preset-subfolder"), &options.UseSubfolder, "保存到子文件夹").
					Width(180).Disabled(s.busy).Changed() {
					if options.UseSubfolder && options.Subfolder == "" {
						options.Subfolder = defaultSubfolderName
					}
					s.editor.message = ""
				}
				folder := options.Subfolder
				if folder == "" {
					folder = defaultSubfolderName
				}
				ui.Text(c, folder+"/example.png").FontSize(13).Width(0).Grow(1).SingleLine().TruncateMiddle()
				if ui.TextInput(c.Key("preset-subfolder-name"), &options.Subfolder).Label("子文件夹名称").
					Placeholder(defaultSubfolderName).Width(144).Height(36).Disabled(s.busy || !options.UseSubfolder).Changed() {
					s.editor.message = ""
				}
			})
		}
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			if ui.Checkbox(c.Key("preset-suffix"), &options.UseSuffix, "添加后缀").
				Width(180).Disabled(s.busy).Changed() {
				if options.UseSuffix && options.Suffix == "" {
					options.Suffix = defaultFilenameSuffix
				}
				s.editor.message = ""
			}
			suffix := options.Suffix
			if suffix == "" {
				suffix = defaultFilenameSuffix
			}
			ui.Text(c, "example"+suffix+".png").FontSize(13).Width(0).Grow(1).SingleLine().TruncateMiddle()
			if ui.TextInput(c.Key("preset-suffix-value"), &options.Suffix).Label("文件名后缀").
				Placeholder(defaultFilenameSuffix).Width(144).Height(36).Disabled(s.busy || !options.UseSuffix).Changed() {
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
