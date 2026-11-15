package main

import (
	"fmt"
	"math"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
)

func (s *application) view(c *ui.Context) {
	theme := c.Theme().WithAccent(ui.Hex("#2563eb"))
	c.SetTheme(theme)
	ui.Column(c).Fill().Padding(28).Gap(24).Background(theme.Background).Children(func() {
		ui.Row(c).Gap(16).Children(func() {
			ui.Column(c).Gap(4).Grow(1).Children(func() {
				ui.Text(c, "Z Pic").FontSize(28).Bold()
				ui.Text(c, "让图片轻一点").FontSize(13).TextColor(theme.TextMuted)
			})
			ui.Text(c, "JPEG / PNG · 本地处理").FontSize(12).TextColor(theme.TextMuted)
		})
		ui.Row(c).Grow(1).FillWidth().Gap(24).AlignItems(ui.Stretch).Children(func() {
			s.previewPanel(c)
			s.controlsPanel(c)
		})
		ui.Text(c, "所有图片均在本机处理，无需联网或上传。").
			FontSize(12).TextColor(theme.TextMuted)
	})
	s.overwriteConfirmation(c)
}

func (s *application) previewPanel(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Grow(1).MinWidth(340).Gap(12).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			caption := "图片预览"
			if s.result != nil {
				caption = "压缩后预览"
			}
			ui.Text(c, caption).FontSize(14).Bold().Grow(1)
			if s.file != nil {
				ui.Button(c.Key("replace"), "更换图片").Disabled(s.busy).OnClick(s.chooseFile)
			}
		})
		ui.Box(c).Grow(1).FillWidth().MinHeight(260).Radius(12).
			Background(t.Surface).Border(1, t.Border).Clip().Children(func() {
			if s.preview == nil {
				ui.Column(c).Fill().Center().Gap(12).Padding(24).Children(func() {
					ui.Text(c, "从一张图片开始").FontSize(22).Bold()
					ui.Text(c, "拖入图片，或从文件中选择").TextColor(t.TextMuted)
					ui.Text(c, "JPEG / PNG · 最大 64 MiB / 3200 万像素").
						FontSize(12).TextColor(t.TextMuted)
					ui.PrimaryButton(c.Key("select"), "选择图片").Margin(8, 0, 0, 0).
						Disabled(s.busy).OnClick(s.chooseFile)
				})
				return
			}
			ui.Box(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) {
				const tile = float32(16)
				for row, y := 0, r.Y; y < r.Y+r.H; row, y = row+1, y+tile {
					for col, x := 0, r.X; x < r.X+r.W; col, x = col+1, x+tile {
						if (row+col)%2 == 0 {
							p.Fill(ui.Rect{X: x, Y: y, W: tile, H: tile}, t.Border.Alpha(0.35), 0)
						}
					}
				}
			}).Children(func() {
				ui.Image(c, s.preview).Fill().Fit(ui.Contain).Label("图片预览")
			})
		})
		if s.file != nil {
			ui.Text(c, filepath.Base(s.file.path)).Bold().SingleLine().TruncateMiddle().
				Tooltip(s.file.path)
			ui.Textf(c, "%s  ·  %d × %d  ·  %s",
				strings.ToUpper(s.file.format), s.file.width, s.file.height, formatSize(s.file.size)).
				FontSize(12).TextColor(t.TextMuted)
		} else {
			ui.Text(c, "保留图片尺寸，可另存副本或覆盖原图。").FontSize(12).TextColor(t.TextMuted)
		}
	})
}

func (s *application) controlsPanel(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Width(300).Shrink(0).Padding(20).Gap(10).Radius(12).
		Background(t.Surface).Border(1, t.Border).Children(func() {
		ui.Text(c, "压缩设置").FontSize(17).Bold()
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "压缩方案")
			scheme := s.scheme
			if ui.Select(c.Key("scheme"), &scheme, []string{"体积优先", "均衡", "画质优先", "自定义"}).
				Label("压缩方案").Grow(1).Disabled(s.busy || s.file == nil).Changed() {
				s.setScheme(scheme)
			}
		})
		ui.Scroll(c.Key("settings")).Grow(1).MinHeight(64).Gap(10).
			KeepScrollbars(true).Padding(0, 8, 0, 0).Children(func() {
			if s.file != nil && s.file.format == "png" {
				s.pngSettings(c)
			} else {
				s.settingSlider(c, "quality", "JPEG 质量", s.quality, 1, 100, s.setQuality)
				ui.Text(c, "质量越低，文件通常越小；照片细节也会减少。默认 80，兼顾画质与体积。").
					FontSize(12).TextColor(t.TextMuted)
			}
		})
		ui.Divider(c)
		s.resultSummary(c)
		if s.busy {
			ui.Row(c).Gap(8).Children(func() {
				ui.Spinner(c).Size(16, 16)
				ui.Text(c, s.phase).FontSize(12).Role(ui.RoleStatus).Grow(1)
			})
			if s.cancel != nil {
				ui.Button(c.Key("cancel"), "取消操作").Disabled(s.canceling).OnClick(s.cancelWork)
			}
		} else if s.message != "" {
			color := t.TextMuted
			role := ui.RoleStatus
			if s.failed {
				color, role = t.Danger, ui.RoleAlert
			}
			ui.Text(c, s.message).FontSize(12).TextColor(color).Role(role).
				MaxLines(3).Tooltip(s.message)
		}
		if s.result != nil {
			ui.Row(c).Gap(8).Children(func() {
				ui.Button(c.Key("compress"), "重新压缩").Grow(1).Height(36).
					Disabled(s.busy).OnClick(s.startCompression)
				ui.PrimaryButton(c.Key("save"), "另存副本").Grow(1).Height(36).
					Disabled(s.busy).OnClick(s.saveCopy)
			})
			label := "覆盖原图"
			if s.overwritten {
				label = "已覆盖原图"
			}
			ui.Button(c.Key("overwrite"), label).Height(32).
				TextColor(t.Danger).Disabled(!s.canOverwrite()).OnClick(s.requestOverwrite)
		} else {
			ui.PrimaryButton(c.Key("compress"), "开始压缩").Height(40).
				Disabled(s.busy || s.file == nil).OnClick(s.startCompression)
		}
		note := "不缩放图片。JPEG 重新编码可能移除元数据，请保留原图。"
		if s.file != nil && s.file.format == "png" {
			note = "不缩放图片。有损压缩可能改变颜色和元数据，请保留原图。"
		}
		ui.Text(c, note).
			FontSize(11).TextColor(t.TextMuted)
	})
}

func (s *application) overwriteConfirmation(c *ui.Context) {
	if !s.confirmOverwrite {
		return
	}
	if !s.canOverwrite() {
		s.confirmOverwrite = false
		return
	}
	message := fmt.Sprintf("将用压缩结果替换以下文件：\n%s\n\n%s → %s。覆盖后无法通过本工具恢复原文件。",
		s.file.path, formatSize(s.result.OriginalSize), formatSize(s.result.OutputSize))
	// The last button receives the default focus, so Enter and Escape cancel.
	if ui.AlertDialog(c.Key("overwrite-confirmation"), &s.confirmOverwrite,
		"确认覆盖原图？", message, "覆盖原图", "取消") == 0 {
		s.overwriteOriginal()
	}
}

func (s *application) pngSettings(c *ui.Context) {
	t := c.Theme()
	ui.Text(c, "PNG 有损压缩").Bold()
	s.settingSlider(c, "png-quality", "目标质量", float64(s.png.MaxQuality), 1, 100, func(value float64) {
		options := s.png
		options.MaxQuality = int(value)
		options.MinQuality = min(options.MinQuality, options.MaxQuality)
		s.setPNGOptions(options)
	})
	s.settingSlider(c, "png-colors", "最大颜色数", float64(s.png.Colors), 2, 256, func(value float64) {
		options := s.png
		options.Colors = int(value)
		s.setPNGOptions(options)
	})
	ui.Collapsible(c.Key("advanced"), "高级设置", &s.advanced, func() {
		ui.Column(c).Gap(10).Padding(8, 0).Children(func() {
			s.settingSlider(c, "png-min-quality", "最低质量", float64(s.png.MinQuality), 0, float64(s.png.MaxQuality), func(value float64) {
				options := s.png
				options.MinQuality = int(value)
				s.setPNGOptions(options)
			})
			ui.Text(c, "未达最低质量时跳过量化。质量值是工具评分，并非画质保留百分比。").
				FontSize(12).TextColor(t.TextMuted)
			s.settingSlider(c, "png-speed", "处理速度", float64(s.png.Speed), 1, 11, func(value float64) {
				options := s.png
				options.Speed = int(value)
				s.setPNGOptions(options)
			})
			ui.Text(c, "1 最慢、11 最快。更快可能降低压缩效果。").
				FontSize(12).TextColor(t.TextMuted)
		})
	})
	ui.Text(c, "质量与颜色数越低，文件通常越小。支持透明度，但渐变、阴影与半透明边缘可能变化。").
		FontSize(12).TextColor(t.TextMuted)
}

func (s *application) settingSlider(c *ui.Context, key, label string, value, low, high float64, change func(float64)) {
	ui.Row(c).Children(func() {
		ui.Text(c, label).Grow(1)
		ui.Textf(c, "%.0f", value).Bold().TextColor(c.Theme().Accent)
	})
	if ui.StepSlider(c.Key(key), &value, low, high, 1).
		Label(label).Disabled(s.busy || s.file == nil).Changed() {
		change(math.Round(value))
	}
}

func (s *application) resultSummary(c *ui.Context) {
	t := c.Theme()
	original, output := "—", "—"
	label, color := "等待压缩", t.TextMuted
	if s.file != nil {
		original = formatSize(s.file.size)
	}
	if s.result != nil {
		original = formatSize(s.result.OriginalSize)
		output = formatSize(s.result.OutputSize)
		saved := float64(s.result.OriginalSize-s.result.OutputSize) / float64(s.result.OriginalSize) * 100
		label, color = fmt.Sprintf("减少 %.1f%%", saved), t.Success
		if s.result.Unchanged {
			label, color = "未减小（0.0%）", t.TextMuted
		}
	}
	ui.Column(c).Gap(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, "压缩结果").FontSize(14).Bold().Grow(1)
			ui.Text(c, label).FontSize(18).Bold().TextColor(color)
		})
		for _, row := range []struct{ label, value string }{
			{"原始大小", original}, {"输出大小", output},
		} {
			ui.Row(c).Children(func() {
				ui.Text(c, row.label).TextColor(t.TextMuted).Grow(1)
				ui.Text(c, row.value).Bold()
			})
		}
	})
}
