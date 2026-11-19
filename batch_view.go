package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"

	"zpic/internal/compress"
)

func (s *application) batchListPanel(c *ui.Context) {
	t := c.Theme()
	width, _ := c.Size()
	mobile := width < 720
	narrow := width < 560
	compactQueue := narrow || width >= 720 && width < 1100
	visible := s.filteredBatchIndices()
	minW := float32(340)
	if mobile {
		minW = 0
	}
	panel := ui.Column(c).Grow(1).MinWidth(minW).Padding(16).Gap(12).
		Radius(panelRadius(c)).Background(panelSurface(c)).Border(1, glassBorder(c))
	if mobile {
		panel.FillWidth().MinHeight(360)
	}
	panel.Children(func() {
		headerRow := ui.Row(c).MinHeight(32).Gap(8).AlignItems(ui.Center)
		if narrow {
			headerRow.Wrap()
		}
		headerRow.Children(func() {
			headerText := fmt.Sprintf("批量队列 · %d 张", len(s.batch.items))
			if s.batchFilterActive() {
				headerText = fmt.Sprintf("批量队列 · %d / %d 张", len(visible), len(s.batch.items))
			}
			ui.Text(c, headerText).
				FontSize(16).FontWeight(600).FontFeatures("tnum").
				SingleLine().TruncateMiddle().Width(0).Grow(1).MinWidth(140).Tooltip(headerText)
			quietButton(c, "batch-add-files", "添加图片", 32, s.modalOpen()).
				KeyShortcuts(ui.Cmd, ui.KeyO).
				Tooltip("添加更多图片 · " + shortcutLabel("O")).
				OnClick(s.chooseFile)
			quietButton(c, "batch-add-folder", "添加文件夹", 32, s.modalOpen()).
				Tooltip("从文件夹添加图片").
				OnClick(s.chooseFolder)
			quietButton(c, "batch-clear", "清空", 32, s.modalOpen()).
				Tooltip("清空当前批量队列").
				OnClick(s.clearBatch)
		})
		filterRow := ui.Row(c).MinHeight(32).Gap(8).AlignItems(ui.Center)
		if narrow {
			filterRow.Wrap()
		}
		filterRow.Children(func() {
			total, pending, done, failed, _, _, _ := s.batchStats()
			labels := []string{
				fmt.Sprintf("全部 %d", total), fmt.Sprintf("待处理 %d", pending),
				fmt.Sprintf("已完成 %d", done), fmt.Sprintf("失败 %d", failed),
			}
			seg := segmentedControl(c, "batch-filter", "队列筛选", &s.batch.filter, 32, false, labels...).
				FillEqually().FillWidth().
				Tooltip("筛选按整个队列计数；搜索在所选状态内查找")
			if seg.Changed() {
				visible = s.filteredBatchIndices()
			}
		})
		ui.Row(c).Height(32).Gap(8).AlignItems(ui.Center).Children(func() {
			searchField := ui.SearchField(c.Key("batch-search"), &s.batch.query).
				Bind(&s.batchSearchRef).
				Label("筛选文件名").Placeholder("搜索文件名").
				Tooltip("搜索相对路径或文件名 · " + shortcutLabel("F")).
				Height(32).Grow(1)
			styleGlassField(searchField, c, 32)
			queryChanged := searchField.Changed()
			sortChanged := s.batchSortControl(c)
			if queryChanged || sortChanged {
				visible = s.filteredBatchIndices()
			}
		})
		retryFailed, invalidCount := s.batchFailureCounts()
		if retryFailed > 0 || invalidCount > 0 {
			var bannerText string
			switch {
			case retryFailed > 0 && invalidCount > 0:
				bannerText = fmt.Sprintf("%d 张失败（%d 无效）", retryFailed+invalidCount, invalidCount)
			case retryFailed > 0:
				bannerText = fmt.Sprintf("%d 张图片处理失败", retryFailed)
			default:
				bannerText = fmt.Sprintf("%d 张无效图片无法处理", invalidCount)
			}
			bannerRow := ui.Row(c).MinHeight(36).Padding(4, 12).Gap(8).Radius(innerRadius(c)).
				Background(statusTintSurface(c, t.Danger)).Border(1, statusTintBorder(c, t.Danger)).AlignItems(ui.Center)
			if compactQueue {
				bannerRow.Column().Padding(8, 12).AlignItems(ui.Stretch)
			}
			bannerRow.Children(func() {
				title := ui.Text(c, bannerText).
					FontSize(12).FontWeight(500).FontFeatures("tnum").TextColor(t.Danger).
					SingleLine().Width(0).Grow(1).MinWidth(120).Tooltip(bannerText)
				if compactQueue {
					title.FillWidth().Grow(0).MinWidth(0)
				}
				actions := func() {
					if invalidCount > 0 {
						quietButton(c, "batch-remove-invalid", "移除无效项", 28, s.modalOpen()).
							Tooltip(fmt.Sprintf("从队列移除 %d 张无法处理的图片", invalidCount)).
							OnClick(s.removeInvalidBatchItems)
					}
					if retryFailed > 0 {
						quietButton(c, "batch-remove-failed", "移除失败项", 28, s.modalOpen()).
							Tooltip("从队列移除全部失败的图片").
							OnClick(s.removeFailedBatchItems)
						quietButton(c, "batch-retry-failed", "重试失败项", 28, s.modalOpen()).
							Tooltip(fmt.Sprintf("重新压缩 %d 张失败的图片", retryFailed)).
							OnClick(s.retryFailedBatchItems)
					}
				}
				if compactQueue {
					ui.Row(c).FillWidth().Gap(8).Wrap().Children(actions)
				} else {
					actions()
				}
			})
		}
		ui.Box(c).Grow(1).FillWidth().MinHeight(180).Radius(innerRadius(c)).
			Background(panelSubtleSurface(c)).Border(1, glassBorder(c)).Clip().Children(func() {
			if len(visible) == 0 {
				s.batch.selected = -1
				s.batch.listSelected = -1
				s.batch.listSelectedKey = ""
				ui.Column(c).Fill().Center().Gap(8).Padding(20).Children(func() {
					ui.Text(c, s.emptyBatchFilterMessage()).
						FontSize(13).FontWeight(500).TextColor(t.TextMuted)
					if query := strings.TrimSpace(s.batch.query); query != "" {
						ui.Text(c, "搜索：“"+query+"”").FontSize(12).TextColor(metadataColor(c)).
							SingleLine().TruncateMiddle().FillWidth().TextAlign(ui.Center).Tooltip(query)
						quietButton(c, "batch-clear-search", "清除搜索", 32, false).
							Tooltip("保留当前状态筛选和排序，仅清除搜索词").
							OnClick(func() {
								s.batch.query = ""
								s.batchSearchRef.Focus(c)
							})
					}
					if s.batchFilterActive() {
						quietButton(c, "batch-reset-filter", "显示全部", 32, false).
							OnClick(s.resetBatchFilter)
					}
				})
				return
			}
			var wantKey string
			if s.batch.selected >= 0 && s.batch.selected < len(s.batch.items) {
				wantKey = s.batch.items[s.batch.selected].absPath
			}
			row := slices.Index(visible, s.batch.selected)
			s.batch.list.Selected = &s.batch.listSelected
			if row != s.batch.listSelected || (row >= 0 && wantKey != s.batch.listSelectedKey) {
				s.batch.list.Choose(row)
			}
			s.batch.list.Key = func(i int) any { return s.batch.items[visible[i]].absPath }
			s.batch.list.Label = func(i int) string { return s.batch.items[visible[i]].relPath }
			list := ui.List(c, &s.batch.list, len(visible)).
				Label("批量图片列表").Fill().Padding(8).Gap(8)
			list.Rows(func(r ui.ListRow) {
				s.batchListRow(r, visible[r.Index])
			})
			if s.batch.listSelected >= 0 && s.batch.listSelected < len(visible) {
				s.batch.selected = visible[s.batch.listSelected]
				s.batch.listSelectedKey = s.batch.items[s.batch.selected].absPath
			} else {
				s.batch.selected = -1
				s.batch.listSelectedKey = ""
			}
			if list.Submitted() && !s.modalOpen() && s.batch.selected >= 0 &&
				s.batch.selected < len(s.batch.items) && !s.batch.items[s.batch.selected].invalidInput {
				s.openBatchPreview(s.batch.selected)
			}
			if !s.modalOpen() && !c.FocusTakesText() && s.batch.selected >= 0 {
				backspace := s.batch.list.Shortcut(c, 0, ui.KeyBackspace)
				del := s.batch.list.Shortcut(c, 0, ui.KeyDelete)
				if backspace || del {
					s.removeSelectedBatchItem()
					c.Invalidate()
					if s.batch == nil {
						return
					}
				}
			}
		})
		if s.batch == nil {
			return
		}
		s.batchSelectedBar(c)
	})
}

type batchSortOption struct {
	mode      int
	label     string
	menuLabel string
}

var batchSortOptions = []batchSortOption{
	{batchSortDefault, "默认顺序", "按添加顺序"},
	{batchSortName, "按名称", "按名称 (A–Z)"},
	{batchSortOriginalSize, "原大小 ↓", "原大小（从大到小）"},
	{batchSortSavings, "节省率 ↓", "节省比例（从高到低）"},
}

func batchSortOptionFor(mode int) batchSortOption {
	for _, opt := range batchSortOptions {
		if opt.mode == mode {
			return opt
		}
	}
	return batchSortOptions[0]
}

func (s *application) batchSortControl(c *ui.Context) bool {
	current := batchSortOptionFor(s.batch.sort)
	changed := false
	ui.PopoverMenuButton(c.Key("batch-sort"), current.label, func(m *ui.Menu) {
		for _, opt := range batchSortOptions {
			if m.Item(opt.menuLabel).Radio(s.batch.sort == opt.mode).Chosen() {
				if s.batch.sort != opt.mode {
					s.batch.sort = opt.mode
					changed = true
				}
			}
		}
	}, ui.PopoverMenuButtonOptions{
		At:   ui.AnchorBottomEnd,
		Self: ui.AnchorTopEnd,
		Panel: func(panel ui.Element) {
			panel.Radius(12).Background(c.Theme().Surface).Border(1, glassBorder(c)).
				NoShadow().Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.18))
		},
	}).Label("队列排序").Height(32).Radius(pillRadius(32, c)).Background(glassControlSurface(c)).Border(1, controlBorder(c)).
		Tooltip("排序方式：" + current.menuLabel)
	return changed
}

func (s *application) emptyBatchFilterMessage() string {
	if strings.TrimSpace(s.batch.query) != "" {
		return "没有找到匹配的图片"
	}
	switch s.batch.filter {
	case batchFilterPending:
		return "没有待处理的图片"
	case batchFilterDone:
		return "没有已完成压缩的图片"
	case batchFilterFailed:
		if s.busy {
			return "处理中，当前没有失败的图片"
		}
		return "没有失败的图片"
	default:
		return "没有符合条件的图片"
	}
}

func (s *application) batchListRow(r ui.ListRow, itemIndex int) {
	c := r.Context
	t := c.Theme()
	item := s.batch.items[itemIndex]
	statusText, statusColor := batchItemStatus(item, t)
	detailText, detailColor := batchItemDetail(item, t)
	background, border := glassCardSurface(c), glassBorder(c)
	if r.Selected() {
		background, border = glassSelectionBackground(c), selectionBorder(c)
	} else if r.Hovered() {
		background, border = glassControlHover(c), controlHoverBorder(c)
	}
	ui.Box(c).FillWidth().Children(func() {
		ui.Row(c).FillWidth().Padding(12).Gap(12).AlignItems(ui.Center).
			Radius(innerRadius(c)).Background(background).Border(1, border).Children(func() {
			s.batchThumbnailView(c, item)
			ui.Column(c).Width(0).Grow(1).Gap(4).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, item.relPath).FontSize(13).FontWeight(500).
						TextColor(t.Text).SingleLine().TruncateMiddle().
						Width(0).Grow(1).Tooltip(item.path)
					badge := ui.Text(c, statusText).FontSize(12).FontWeight(500).
						FontFeatures("tnum").TextColor(statusColor).SingleLine().Shrink(0)
					if item.status == batchDone && !item.unchanged && item.outputSize < item.originalSize {
						badge.Padding(2, 8).Radius(badgeRadius(20)).
							Background(statusTintSurface(c, t.Success)).Border(1, statusTintBorder(c, t.Success))
					} else if item.status == batchFailed {
						badge.Padding(2, 8).Radius(badgeRadius(20)).
							Background(statusTintSurface(c, t.Danger)).Border(1, statusTintBorder(c, t.Danger))
					} else if item.status == batchProcessing {
						badge.Padding(2, 8).Radius(badgeRadius(20)).
							Background(glassSelectionBackground(c)).Border(1, selectionBorder(c))
					}
				})
				meta := strings.ToUpper(item.format)
				if item.width > 0 && item.height > 0 {
					meta += fmt.Sprintf(" · %d × %d", item.width, item.height)
				}
				width, _ := c.Size()
				metaRow := ui.Row(c).Gap(8).Wrap().AlignItems(ui.Center)
				stackMetadata := width >= 720 && width < 960
				if stackMetadata {
					metaRow.Column().Gap(4).AlignItems(ui.Stretch)
				}
				metaRow.Children(func() {
					ui.Text(c, meta).FontSize(12).FontFeatures("tnum").
						TextColor(metadataColor(c)).SingleLine().Shrink(0)
					if !stackMetadata {
						ui.Text(c, "·").FontSize(12).TextColor(metadataColor(c)).Shrink(0)
					}
					detail := ui.Text(c, batchItemSizeDetail(item)).FontSize(12).FontFeatures("tnum").
						TextColor(detailColor).SingleLine().TruncateMiddle().
						Tooltip(detailText)
					if stackMetadata {
						detail.FillWidth()
					} else {
						detail.Width(0).Grow(1).MinWidth(100)
					}
				})
			})
		})
	})
}

func (s *application) batchThumbnailView(c *ui.Context, item batchItem) {
	t := c.Theme()
	entry := s.batchThumbnail(item)
	label := item.relPath + " 缩略图"
	unavailable := item.invalidInput || entry == nil || entry.failed
	thumbRadius := float32(4)
	if usesGlass(c) {
		thumbRadius = 6
	}
	ui.Box(c).Size(64, 64).Shrink(0).Radius(thumbRadius).Background(panelStageBackground(c)).Border(1, glassBorder(c)).
		Clip().Role(ui.RoleImage).Label(label).Children(func() {
		if entry != nil && entry.bitmap != nil {
			ui.Image(c, entry.bitmap).Fill().Fit(ui.Contain)
			return
		}
		placeholder := "加载中"
		if unavailable {
			placeholder = "无预览"
		}
		ui.Column(c).Fill().Center().Gap(4).Children(func() {
			ui.Box(c).Size(16, 16).Draw(func(p *ui.Painter, bounds ui.Rect) {
				frame := ui.Rect{X: bounds.X + 1, Y: bounds.Y + 2, W: 14, H: 12}
				p.Stroke(frame, t.TextMuted.Alpha(0.7), 2, 1.5)
				p.StrokePolyline([][2]float32{
					{bounds.X + 3, bounds.Y + 12},
					{bounds.X + 7, bounds.Y + 8},
					{bounds.X + 10, bounds.Y + 11},
					{bounds.X + 13, bounds.Y + 9},
				}, 1.5, t.TextMuted)
			})
			ui.Text(c, placeholder).FontSize(11).TextColor(metadataColor(c))
		})
	})
}

func batchItemStatus(item batchItem, t *ui.Theme) (string, ui.Color) {
	switch item.status {
	case batchProcessing:
		return "处理中", accentForeground(t)
	case batchDone:
		if item.unchanged {
			return "未减小", t.TextMuted
		}
		if item.outputSize > item.originalSize && item.originalSize > 0 {
			grew := float64(item.outputSize-item.originalSize) / float64(item.originalSize) * 100
			return fmt.Sprintf("+%0.1f%%", grew), t.TextMuted
		}
		if item.originalSize <= 0 {
			return "已完成", t.Success
		}
		saved := float64(item.originalSize-item.outputSize) / float64(item.originalSize) * 100
		return fmt.Sprintf("-%0.1f%%", saved), t.Success
	case batchFailed:
		return "失败", t.Danger
	default:
		return "等待压缩", t.TextMuted
	}
}

func batchItemDetail(item batchItem, t *ui.Theme) (string, ui.Color) {
	if item.status == batchFailed {
		return item.errMsg, t.Danger
	}
	meta := fmt.Sprintf("%s · %d × %d", strings.ToUpper(item.format), item.width, item.height)
	return fmt.Sprintf("%s · %s", meta, batchItemSizeDetail(item)), t.TextMuted
}

func batchItemSizeDetail(item batchItem) string {
	if item.status == batchFailed {
		return item.errMsg
	}
	if item.status == batchDone {
		if item.unchanged {
			note := "保留原数据"
			if item.overwritten {
				note = "未覆盖"
			}
			return fmt.Sprintf("%s（%s）", formatSize(item.originalSize), note)
		}
		suffix := ""
		if item.overwritten {
			suffix = " · 已覆盖"
		}
		return fmt.Sprintf("%s → %s%s", formatSize(item.originalSize), formatSize(item.outputSize), suffix)
	}
	return formatSize(item.originalSize)
}

func (s *application) batchSelectedBar(c *ui.Context) {
	t := c.Theme()
	width, _ := c.Size()
	if s.batch == nil {
		return
	}
	if s.batch.selected < 0 || s.batch.selected >= len(s.batch.items) {
		return
	}
	item := s.batch.items[s.batch.selected]
	selected := s.batch.selected
	subText, subColor := batchItemDetail(item, t)
	subTooltip := item.path
	if item.status == batchDone && item.savedPath != "" {
		subText = "已输出：" + item.savedPath
		subTooltip = item.savedPath
	} else if item.status == batchFailed {
		subTooltip = item.errMsg
	}
	bar := ui.Row(c).Gap(8).AlignItems(ui.Center)
	compact := width < 560 || width >= 720 && width < 1100
	if compact {
		bar.Column().AlignItems(ui.Stretch)
	}
	bar.Children(func() {
		details := ui.Column(c).Width(0).Grow(1).MinWidth(140).Gap(4)
		if compact {
			details.FillWidth().Grow(0).MinWidth(0)
		}
		details.Children(func() {
			ui.Text(c, filepath.Base(item.path)).FontSize(13).FontWeight(500).
				SingleLine().TruncateMiddle().Tooltip(item.path)
			detail := ui.Text(c, subText).FontSize(12).FontFeatures("tnum").TextColor(subColor).
				FillWidth().Tooltip(subTooltip)
			if item.status == batchFailed {
				detail.MaxLines(3).Role(ui.RoleAlert)
			} else {
				detail.SingleLine().TruncateMiddle()
			}
		})
		actions := func() {
			if item.status == batchPending && !item.invalidInput {
				quietButton(c, "batch-compress-item", "压缩此张", 32, s.modalOpen()).
					Tooltip("仅压缩此张图片").
					OnClick(func() { s.compressBatchItem(selected) })
			}
			if item.status == batchFailed && !item.invalidInput {
				quietButton(c, "batch-retry-item", "重试", 32, s.modalOpen()).
					Tooltip("重新压缩此失败图片").
					OnClick(func() { s.compressBatchItem(selected) })
			}
			if outPath := batchItemOutputPath(item); outPath != "" {
				quietButton(c, "batch-reveal-item", "定位文件", 32, s.modalOpen()).
					Tooltip("在文件夹中显示输出文件").
					OnClick(func() { s.revealFileInFolder(outPath) })
				quietButton(c, "batch-copy-item", "复制", 32, !s.canCopyBatchItem(item)).
					Label("复制图片").Tooltip("将压缩结果复制到剪贴板").
					OnClick(func() { s.copyBatchItem(selected) })
			}
			quietButton(c, "batch-preview", "预览", 32, s.modalOpen() || item.invalidInput).
				Tooltip("查看或对比选中图片").
				OnClick(func() { s.openBatchPreview(selected) })
			quietButton(c, "batch-remove", "移除", 32, s.modalOpen()).
				Tooltip("从批量队列移除此图片 · Delete / Backspace").
				OnClick(s.removeSelectedBatchItem)
		}
		if compact {
			ui.Row(c).FillWidth().Gap(8).Wrap().AlignItems(ui.Center).Children(actions)
		} else {
			actions()
		}
	})
}

func (s *application) hasAdjacentBatchPreview(delta int) bool {
	return s.adjacentBatchPreviewIndex(delta) >= 0
}

func (s *application) batchPreviewPanel(c *ui.Context) {
	t := c.Theme()
	width, _ := c.Size()
	mobile := width < 720
	previewIdx := s.batch.previewIndex
	var previewItem *batchItem
	if previewIdx >= 0 && previewIdx < len(s.batch.items) {
		previewItem = &s.batch.items[previewIdx]
	}
	minW := float32(340)
	if mobile {
		minW = 0
	}
	panel := ui.Column(c).Grow(1).MinWidth(minW).Padding(16).Gap(12).
		Radius(panelRadius(c)).Background(panelSurface(c)).Border(1, glassBorder(c))
	if mobile {
		panel.FillWidth()
	}
	panel.Children(func() {
		ui.Row(c).MinHeight(32).Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
			quietButton(c, "batch-back", "返回列表", 32, s.modalOpen()).
				KeyShortcuts(0, ui.KeyEscape).Tooltip("返回批量列表 · Esc").
				OnClick(s.closeBatchPreview)
			visible := s.filteredBatchIndices()
			posCurrent, posTotal := s.batch.previewIndex+1, len(s.batch.items)
			if pos := slices.Index(visible, s.batch.previewIndex); pos >= 0 {
				posCurrent, posTotal = pos+1, len(visible)
			}
			ui.Textf(c, "%d / %d", posCurrent, posTotal).
				FontSize(12).FontFeatures("tnum").TextColor(metadataColor(c)).SingleLine().Grow(1)
			quietButton(c, "batch-prev", "上一张", 32, s.modalOpen() || !s.hasAdjacentBatchPreview(-1)).
				KeyShortcuts(ui.Cmd, ui.KeyBracketLeft).
				Tooltip("上一张 · " + shortcutLabel("[")).
				OnClick(func() { s.stepBatchPreview(-1) })
			quietButton(c, "batch-next", "下一张", 32, s.modalOpen() || !s.hasAdjacentBatchPreview(1)).
				KeyShortcuts(ui.Cmd, ui.KeyBracketRight).
				Tooltip("下一张 · " + shortcutLabel("]")).
				OnClick(func() { s.stepBatchPreview(1) })
		})
		if s.preview != nil {
			s.previewToolbar(c)
		}
		ui.Box(c).Grow(1).FillWidth().MinHeight(220).Radius(innerRadius(c)).
			Background(panelStageBackground(c)).Border(1, glassBorder(c)).Clip().Children(func() {
			if s.preview != nil {
				s.previewCanvas(c)
			}
		})
		if s.file != nil {
			meta := fmt.Sprintf("%s  ·  %d × %d  ·  %s",
				strings.ToUpper(s.file.format), s.file.width, s.file.height, formatSize(s.file.size))
			metaColor := metadataColor(c)
			metaTooltip := meta
			if s.result != nil {
				if s.result.Unchanged {
					meta += "  ·  未减小"
				} else {
					meta += " → " + formatSize(s.result.OutputSize)
				}
				metaTooltip = meta
			} else if previewItem != nil && previewItem.status == batchFailed {
				meta += "  ·  失败：" + previewItem.errMsg
				metaColor = t.Danger
				metaTooltip = previewItem.errMsg
			}
			footerRow := ui.Row(c).Gap(8).AlignItems(ui.Center)
			compactFooter := width < 1100
			if compactFooter {
				footerRow.Column().AlignItems(ui.Stretch)
			}
			footerRow.Children(func() {
				details := ui.Column(c).Width(0).Grow(1).MinWidth(140).Gap(4)
				if compactFooter {
					details.FillWidth().Grow(0).MinWidth(0)
				}
				details.Children(func() {
					ui.Text(c, filepath.Base(s.file.path)).FontSize(13).FontWeight(500).
						SingleLine().TruncateMiddle().Tooltip(s.file.path)
					detail := ui.Text(c, meta).FontSize(12).FontFeatures("tnum").TextColor(metaColor).
						FillWidth().Tooltip(metaTooltip)
					if previewItem != nil && previewItem.status == batchFailed {
						detail.MaxLines(2)
					} else {
						detail.SingleLine().TruncateMiddle()
					}
				})
				if previewItem != nil {
					actions := func() {
						if outPath := batchItemOutputPath(*previewItem); outPath != "" {
							quietButton(c, "batch-preview-reveal", "定位文件", 32, s.modalOpen()).
								Tooltip("在文件夹中显示输出文件").
								OnClick(func() { s.revealFileInFolder(outPath) })
						}
						quietButton(c, "batch-preview-remove", "移除", 32, s.modalOpen()).
							Tooltip("从批量队列移除此图片").
							OnClick(func() { s.removeBatchItem(previewIdx) })
					}
					if compactFooter {
						ui.Row(c).FillWidth().Gap(8).Children(actions)
					} else {
						actions()
					}
				}
			})
		}
	})
}

func (s *application) batchControls(c *ui.Context, showOutput, mobile bool) {
	t := c.Theme()
	summaryBody := func() {
		if s.batch.previewOpen {
			s.imageResultSummary(c, "当前图片结果")
		} else {
			s.batchResultSummary(c)
		}
		s.batchOutputCard(c)
	}
	if mobile {
		ui.Column(c).FillWidth().Gap(12).Children(summaryBody)
	} else {
		ui.Scroll(c.Key("batch-compression-summary")).Grow(1).MinHeight(0).FillWidth().Gap(12).Children(summaryBody)
	}
	if s.busy {
		s.workFeedback(c)
	} else if s.message != "" {
		color, role := t.TextMuted, ui.RoleStatus
		if s.failed {
			color, role = t.Danger, ui.RoleAlert
		}
		ui.Text(c, s.message).FontSize(12).TextColor(color).Role(role).
			MaxLines(2).Tooltip(s.message)
	}
	if showOutput {
		primaryAction(c, "batch-open-output", "打开输出文件夹", s.modalOpen()).Height(36).
			OnClick(s.openOutputDir)
	}
	if s.batch.previewOpen && s.result != nil {
		quietButton(c, "copy-result", "复制图片", 32, !s.canCopyResult()).FillWidth().
			KeyShortcuts(ui.Cmd, ui.KeyC).
			Tooltip("将当前图片的压缩结果复制到剪贴板，可直接粘贴图片 · " + shortcutLabel("C")).OnClick(s.copyResult)
	}
}

func (s *application) batchOutputCard(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Padding(12).Gap(8).Radius(innerRadius(c)).Background(panelSubtleSurface(c)).Border(1, glassBorder(c)).Children(func() {
		if s.batch.previewOpen && s.batch.previewIndex >= 0 && s.batch.previewIndex < len(s.batch.items) {
			item := s.batch.items[s.batch.previewIndex]
			if path := batchItemOutputPath(item); path != "" {
				ui.Text(c, "输出文件").FontSize(12).FontWeight(500).TextColor(metadataColor(c))
				ui.Text(c, path).FontSize(12).SingleLine().TruncateMiddle().FillWidth().Tooltip(path)
				if item.overwritten {
					ui.Text(c, "已覆盖原图，无法撤销").FontSize(12).TextColor(t.TextMuted).MaxLines(2)
				}
				return
			}
		}
		ui.Text(c, "保存方式").FontSize(12).FontWeight(500).TextColor(metadataColor(c))
		if s.batch.outputMode == batchOutputFolder {
			dirText, dirColor, dirTooltip := "压缩时选择保存位置", metadataColor(c), "将在开始压缩时选择保存文件夹"
			btnLabel := "选择位置"
			if s.output.Mode == presetSaveOriginal {
				dirText, dirTooltip = "各原文件所在文件夹", "每张图片保存到自己的原文件夹"
			}
			if s.output.Mode == presetSaveFixed {
				dirText, dirTooltip = "请选择预设的固定输出目录", "在预设设置中选择输出目录"
			}
			if s.batch.outputDir != "" {
				dirText = s.batch.outputDir
				dirColor = t.Text
				dirTooltip = s.batch.outputDir
				btnLabel = "更改"
			}
			ui.Row(c).Height(32).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, dirText).FontSize(12).TextColor(dirColor).
					SingleLine().TruncateMiddle().Width(0).Grow(1).Tooltip(dirTooltip)
				if s.output.Mode == presetSaveAsk {
					quietButton(c, "batch-output-dir", btnLabel, 32, s.modalOpen()).
						OnClick(s.chooseOutputDir)
				}
			})
			name := "example.png"
			if s.batch.selected >= 0 && s.batch.selected < len(s.batch.items) {
				name = s.batch.items[s.batch.selected].relPath
			}
			outputFilenamePreviewForFormat(c, s.output, name, s.outputFormat, false)
		} else {
			warning := "缩小后的文件将直接替换原图，未减小则跳过，无法撤销。"
			if s.outputFormat == compress.OutputFormatWebP {
				warning = "原 WebP 缩小后将直接替换，JPEG / PNG 将在原文件夹另存为 .webp。"
			}
			ui.Text(c, warning).
				FontSize(12).TextColor(t.Danger).MaxLines(2)
		}
	})
}

func (s *application) batchResultSummary(c *ui.Context) {
	t := c.Theme()
	total, pending, done, failed, origTotal, outTotal, _ := s.batchStats()
	doneOrig := s.batch.cachedStats.doneOrigTotal
	unchanged := s.batch.cachedStats.unchangedDone
	firstLabel, original := "原始总计", formatSize(origTotal)
	secondLabel, output := "输出总计", "—"
	label, color := fmt.Sprintf("等待压缩 · %d 张", total), t.TextMuted
	ratio := -1.0
	switch {
	case s.batch.running && s.batch.total > 0:
		label, color = fmt.Sprintf("处理中 %d / %d", s.batch.done, s.batch.total), accentForeground(t)
		if done > 0 {
			output = formatSize(outTotal)
			if doneOrig > 0 {
				ratio = min(1, max(0, float64(outTotal)/float64(doneOrig)))
			}
			if pending > 0 || failed > 0 {
				firstLabel, original = "已完成原大小", formatSize(doneOrig)
			}
		}
	case done > 0:
		output = formatSize(outTotal)
		if pending > 0 || failed > 0 {
			firstLabel, original = "已完成原大小", formatSize(doneOrig)
		}
		if doneOrig > 0 && outTotal < doneOrig {
			saved := float64(doneOrig-outTotal) / float64(doneOrig) * 100
			label, color = fmt.Sprintf("减少 %.1f%%", saved), t.Success
			ratio = min(1, max(0, float64(outTotal)/float64(doneOrig)))
		} else {
			label, color = "未减小（0.0%）", t.TextMuted
			ratio = 1
		}
		if failed > 0 {
			label += fmt.Sprintf(" · %d 失败", failed)
			color = t.Danger
		} else if pending > 0 {
			label += fmt.Sprintf(" · 余 %d", pending)
		}
	case failed > 0:
		if pending > 0 {
			label = fmt.Sprintf("等待压缩 · %d 张 · %d 失败", pending, failed)
		} else {
			label = fmt.Sprintf("%d 张失败", failed)
		}
		color = t.Danger
	}
	var note string
	if !s.busy && done > 0 {
		var parts []string
		if doneOrig > outTotal {
			parts = append(parts, "共节省 "+formatSize(doneOrig-outTotal))
		}
		if unchanged > 0 {
			if s.batch.outputMode == batchOutputOverwrite {
				parts = append(parts, fmt.Sprintf("跳过 %d 张未减小", unchanged))
			} else {
				parts = append(parts, fmt.Sprintf("%d 张未减小保持原样", unchanged))
			}
		}
		note = strings.Join(parts, " · ")
	}
	summaryCardWithNote(c, "批量结果", label, color, firstLabel, original, secondLabel, output, note, ratio)
}

func (s *application) batchOverwriteConfirmation(c *ui.Context) {
	if !s.confirmBatchOverwrite || s.batch == nil {
		return
	}
	if s.busy || s.settingsOpen || s.confirmOverwrite {
		s.confirmBatchOverwrite = false
		s.clearPendingBatchConfirmation()
		return
	}
	mode, index := s.effectivePendingBatchRun()
	targetCount := s.batchRunTargetCount(mode, index)
	message := fmt.Sprintf("将压缩 %d 张图片；体积减小的文件会直接替换原文件，未减小的文件将保持不变。\n\n覆盖后无法通过本工具恢复原文件。", targetCount)
	if ui.AlertDialog(c.Key("batch-overwrite-confirmation"), &s.confirmBatchOverwrite,
		"确认批量覆盖原图？", message, "覆盖原图", "取消") == 0 {
		s.executeConfirmedBatchOverwrite()
	} else if !s.confirmBatchOverwrite {
		s.clearPendingBatchConfirmation()
	}
}
