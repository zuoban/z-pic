package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
)

func (s *application) batchListPanel(c *ui.Context) {
	t := c.Theme()
	visible := s.filteredBatchIndices()
	ui.Column(c).Grow(1).MinWidth(340).Padding(16).Gap(12).
		Radius(16).Background(t.Surface).Border(1, t.Border).Children(func() {
		ui.Row(c).Height(32).Gap(8).AlignItems(ui.Center).Children(func() {
			headerText := fmt.Sprintf("批量队列 · %d 张", len(s.batch.items))
			if s.batchFilterActive() {
				headerText = fmt.Sprintf("批量队列 · %d / %d 张", len(visible), len(s.batch.items))
			}
			ui.Text(c, headerText).
				FontSize(14).FontWeight(500).FontFeatures("tnum").
				SingleLine().Grow(1)
			ui.Button(c.Key("batch-add-files"), "添加图片").Height(32).
				KeyShortcuts(ui.Cmd, ui.KeyO).
				Tooltip("添加更多图片 · " + shortcutLabel("O")).
				Disabled(s.busy).OnClick(s.chooseFile)
			ui.Button(c.Key("batch-add-folder"), "添加文件夹").Height(32).
				Tooltip("从文件夹添加图片").
				Disabled(s.busy).OnClick(s.chooseFolder)
			ui.Button(c.Key("batch-clear"), "清空").Height(32).
				Tooltip("清空当前批量队列").
				Disabled(s.busy).OnClick(s.clearBatch)
		})
		ui.Row(c).Height(32).Gap(8).AlignItems(ui.Center).Children(func() {
			filterChanged := ui.Segmented(c.Key("batch-filter"), &s.batch.filter, "全部", "待处理", "已完成", "失败").
				Label("队列筛选").Height(32).Changed()
			ui.Box(c).Grow(1)
			sortChanged := s.batchSortControl(c)
			if filterChanged || sortChanged {
				visible = s.filteredBatchIndices()
			}
		})
		ui.Row(c).Height(32).Gap(8).AlignItems(ui.Center).Children(func() {
			queryChanged := ui.SearchField(c.Key("batch-search"), &s.batch.query).
				Bind(&s.batchSearchRef).
				Label("筛选文件名").Placeholder("搜索文件名").
				Tooltip("搜索相对路径或文件名 · " + shortcutLabel("F")).
				Height(32).Grow(1).Changed()
			if queryChanged {
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
			ui.Row(c).Height(36).Padding(0, 12).Gap(8).Radius(8).
				Background(t.Background).AlignItems(ui.Center).Children(func() {
				ui.Text(c, bannerText).
					FontSize(12).FontFeatures("tnum").TextColor(t.Danger).
					SingleLine().Width(0).Grow(1).Tooltip(bannerText)
				if invalidCount > 0 {
					ui.Button(c.Key("batch-remove-invalid"), "移除无效项").Height(28).
						Disabled(s.busy).
						Tooltip(fmt.Sprintf("从队列移除 %d 张无法处理的图片", invalidCount)).
						OnClick(s.removeInvalidBatchItems)
				}
				if retryFailed > 0 {
					ui.Button(c.Key("batch-remove-failed"), "移除失败项").Height(28).
						Disabled(s.busy).
						Tooltip("从队列移除全部失败的图片").
						OnClick(s.removeFailedBatchItems)
					ui.Button(c.Key("batch-retry-failed"), "重试失败项").Height(28).
						Disabled(s.busy).
						Tooltip(fmt.Sprintf("重新压缩 %d 张失败的图片", retryFailed)).
						OnClick(s.retryFailedBatchItems)
				}
			})
		}
		ui.Box(c).Grow(1).FillWidth().MinHeight(180).Radius(8).
			Background(t.Background).Clip().Children(func() {
			if len(visible) == 0 {
				s.batch.selected = -1
				s.batch.listSelected = -1
				s.batch.listSelectedKey = ""
				ui.Column(c).Fill().Center().Gap(8).Padding(20).Children(func() {
					ui.Text(c, s.emptyBatchFilterMessage()).
						FontSize(13).FontWeight(500).TextColor(t.TextMuted)
					if s.batchFilterActive() {
						ui.Button(c.Key("batch-reset-filter"), "显示全部").Height(32).
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
				Label("批量图片列表").Fill().Padding(4)
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
	}).Label("队列排序").Height(32).Tooltip("排序方式：" + current.menuLabel)
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
	t := r.Context.Theme()
	item := s.batch.items[itemIndex]
	titleColor := t.Text
	statusText, statusColor := batchItemStatus(item, t)
	detailText, detailColor := batchItemDetail(item, t)
	if r.Selected() {
		titleColor = t.AccentText
		statusColor = t.AccentText
		detailColor = t.AccentText.Alpha(0.85)
	}
	ui.Column(r.Context).FillWidth().Padding(8, 12).Gap(4).Children(func() {
		ui.Row(r.Context).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(r.Context, item.relPath).FontSize(13).FontWeight(500).
				TextColor(titleColor).SingleLine().TruncateMiddle().
				Grow(1).Tooltip(item.path)
			ui.Text(r.Context, statusText).FontSize(12).FontWeight(500).
				FontFeatures("tnum").TextColor(statusColor).
				SingleLine().Shrink(0)
		})
		ui.Text(r.Context, detailText).FontSize(12).FontFeatures("tnum").
			TextColor(detailColor).SingleLine().Tooltip(detailText)
	})
}

func batchItemStatus(item batchItem, t *ui.Theme) (string, ui.Color) {
	switch item.status {
	case batchProcessing:
		return "处理中", t.Accent
	case batchDone:
		if item.unchanged {
			return "未减小", t.TextMuted
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
	if item.status == batchDone {
		if item.unchanged {
			note := "保留原数据"
			if item.overwritten {
				note = "未覆盖"
			}
			return fmt.Sprintf("%s · %s（%s）", meta, formatSize(item.originalSize), note), t.TextMuted
		}
		suffix := ""
		if item.overwritten {
			suffix = " · 已覆盖"
		}
		return fmt.Sprintf("%s · %s → %s%s", meta, formatSize(item.originalSize), formatSize(item.outputSize), suffix), t.TextMuted
	}
	return fmt.Sprintf("%s · %s", meta, formatSize(item.originalSize)), t.TextMuted
}

func (s *application) batchSelectedBar(c *ui.Context) {
	t := c.Theme()
	if s.batch == nil {
		return
	}
	if s.batch.selected < 0 || s.batch.selected >= len(s.batch.items) {
		ui.Column(c).Gap(4).Children(func() {
			ui.Text(c, "点击列表项查看详情或预览").FontSize(13).FontWeight(500)
			ui.Text(c, "可继续拖入图片或文件夹加入批量队列").FontSize(12).TextColor(t.TextMuted)
		})
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
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Width(0).Grow(1).Gap(4).Children(func() {
			ui.Text(c, filepath.Base(item.path)).FontSize(13).FontWeight(500).
				SingleLine().TruncateMiddle().Tooltip(item.path)
			ui.Text(c, subText).FontSize(12).FontFeatures("tnum").TextColor(subColor).
				SingleLine().TruncateMiddle().Tooltip(subTooltip)
		})
		if item.status == batchFailed && !item.invalidInput {
			ui.Button(c.Key("batch-retry-item"), "重试").Height(32).
				Disabled(s.busy).
				Tooltip("重新压缩此失败图片").
				OnClick(func() { s.compressBatchItem(selected) })
		}
		if outPath := batchItemOutputPath(item); outPath != "" {
			ui.Button(c.Key("batch-reveal-item"), "定位文件").Height(32).
				Disabled(s.busy).
				Tooltip("在文件夹中显示输出文件").
				OnClick(func() { s.revealFileInFolder(outPath) })
		}
		ui.Button(c.Key("batch-preview"), "预览").Height(32).
			Disabled(s.busy || item.invalidInput).
			Tooltip("查看或对比选中图片").
			OnClick(func() { s.openBatchPreview(selected) })
		ui.Button(c.Key("batch-remove"), "移除").Height(32).
			Disabled(s.busy).
			Tooltip("从批量队列移除此图片 · Delete / Backspace").
			OnClick(s.removeSelectedBatchItem)
	})
}

func (s *application) hasAdjacentBatchPreview(delta int) bool {
	return s.adjacentBatchPreviewIndex(delta) >= 0
}

func (s *application) batchPreviewPanel(c *ui.Context) {
	t := c.Theme()
	previewIdx := s.batch.previewIndex
	var previewItem *batchItem
	if previewIdx >= 0 && previewIdx < len(s.batch.items) {
		previewItem = &s.batch.items[previewIdx]
	}
	ui.Column(c).Grow(1).MinWidth(340).Padding(16).Gap(12).
		Radius(16).Background(t.Surface).Border(1, t.Border).Children(func() {
		ui.Row(c).Height(32).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Button(c.Key("batch-back"), "返回列表").Height(32).
				KeyShortcuts(0, ui.KeyEscape).Tooltip("返回批量列表 · Esc").
				Disabled(s.busy).OnClick(s.closeBatchPreview)
			visible := s.filteredBatchIndices()
			posCurrent, posTotal := s.batch.previewIndex+1, len(s.batch.items)
			if pos := slices.Index(visible, s.batch.previewIndex); pos >= 0 {
				posCurrent, posTotal = pos+1, len(visible)
			}
			ui.Textf(c, "%d / %d", posCurrent, posTotal).
				FontSize(12).FontFeatures("tnum").TextColor(t.TextMuted).SingleLine().Grow(1)
			ui.Button(c.Key("batch-prev"), "上一张").Height(32).
				KeyShortcuts(ui.Cmd, ui.KeyBracketLeft).
				Tooltip("上一张 · " + shortcutLabel("[")).
				Disabled(s.busy || !s.hasAdjacentBatchPreview(-1)).
				OnClick(func() { s.stepBatchPreview(-1) })
			ui.Button(c.Key("batch-next"), "下一张").Height(32).
				KeyShortcuts(ui.Cmd, ui.KeyBracketRight).
				Tooltip("下一张 · " + shortcutLabel("]")).
				Disabled(s.busy || !s.hasAdjacentBatchPreview(1)).
				OnClick(func() { s.stepBatchPreview(1) })
			if s.result != nil && !s.overwritten {
				ui.Segmented(c.Key("preview-mode"), &s.comparison.mode, "对比", "原图", "结果").
					Label("预览模式").FillEqually().Width(176).Height(32).
					Disabled(s.previewLocked())
			}
		})
		if s.preview != nil {
			s.previewToolbar(c)
		}
		ui.Box(c).Grow(1).FillWidth().MinHeight(220).Radius(8).
			Background(t.Background).Clip().Children(func() {
			if s.preview != nil {
				s.previewCanvas(c)
			}
		})
		if s.file != nil {
			meta := fmt.Sprintf("%s  ·  %d × %d  ·  %s",
				strings.ToUpper(s.file.format), s.file.width, s.file.height, formatSize(s.file.size))
			metaColor := t.TextMuted
			metaTooltip := meta
			if s.result != nil {
				if s.result.Unchanged {
					meta += "  ·  未减小"
				} else {
					saved := float64(s.result.OriginalSize-s.result.OutputSize) / float64(s.result.OriginalSize) * 100
					meta += fmt.Sprintf(" → %s（减少 %.1f%%）", formatSize(s.result.OutputSize), saved)
				}
				metaTooltip = meta
			} else if previewItem != nil && previewItem.status == batchFailed {
				meta += "  ·  失败：" + previewItem.errMsg
				metaColor = t.Danger
				metaTooltip = previewItem.errMsg
			}
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Width(0).Grow(1).Gap(4).Children(func() {
					ui.Text(c, filepath.Base(s.file.path)).FontSize(13).FontWeight(500).
						SingleLine().TruncateMiddle().Tooltip(s.file.path)
					ui.Text(c, meta).FontSize(12).FontFeatures("tnum").TextColor(metaColor).
						SingleLine().TruncateMiddle().Tooltip(metaTooltip)
				})
				if previewItem != nil && !previewItem.invalidInput {
					switch previewItem.status {
					case batchPending:
						ui.Button(c.Key("batch-preview-compress"), "压缩此张").Height(32).
							Disabled(s.busy).
							Tooltip("仅压缩此图片，完成后可在队列中预览结果").
							OnClick(func() { s.compressBatchItem(previewIdx) })
					case batchFailed:
						ui.Button(c.Key("batch-preview-retry"), "重试").Height(32).
							Disabled(s.busy).
							Tooltip("重新压缩此失败图片").
							OnClick(func() { s.compressBatchItem(previewIdx) })
					}
				}
				if previewItem != nil {
					if outPath := batchItemOutputPath(*previewItem); outPath != "" {
						ui.Button(c.Key("batch-preview-reveal"), "定位文件").Height(32).
							Disabled(s.busy).
							Tooltip("在文件夹中显示输出文件").
							OnClick(func() { s.revealFileInFolder(outPath) })
					}
					ui.Button(c.Key("batch-preview-remove"), "移除").Height(32).
						Disabled(s.busy).
						Tooltip("从批量队列移除此图片").
						OnClick(func() { s.removeBatchItem(previewIdx) })
				}
			})
		}
	})
}

func (s *application) batchControls(c *ui.Context) {
	t := c.Theme()
	s.batchOutputCard(c)
	s.batchResultSummary(c)
	if s.busy {
		if s.batch.total > 0 {
			progress := float64(s.batch.done) / float64(s.batch.total)
			ui.Progress(c, progress).Label("批量压缩进度").FillWidth()
		}
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c).Size(16, 16)
			ui.Text(c, s.phase).FontSize(12).Role(ui.RoleStatus).Grow(1).MaxLines(2)
		})
		if s.cancel != nil {
			ui.Button(c.Key("cancel"), "取消操作").Height(32).
				Disabled(s.canceling).OnClick(s.cancelWork)
		}
	} else if s.message != "" {
		color, role := t.TextMuted, ui.RoleStatus
		if s.failed {
			color, role = t.Danger, ui.RoleAlert
		}
		ui.Text(c, s.message).FontSize(12).TextColor(color).Role(role).
			MaxLines(2).Tooltip(s.message)
	}
	_, pending, done, _, _, _, canRun := s.batchStats()
	switch {
	case (pending > 0 && done == 0) || !canRun:
		ui.PrimaryButton(c.Key("batch-start"), "开始压缩").Height(40).
			KeyShortcuts(ui.Cmd, ui.KeyEnter).
			Tooltip("开始批量压缩 · " + shortcutLabel("↵")).
			Disabled(s.busy || !canRun).OnClick(s.startBatchCompression)
	case pending > 0 && done > 0:
		ui.Row(c).Gap(8).Children(func() {
			ui.Button(c.Key("batch-restart"), "重新压缩").Grow(1).Height(36).
				Disabled(s.busy || !canRun).OnClick(s.restartBatchCompression)
			ui.PrimaryButton(c.Key("batch-continue"), "继续压缩").Grow(1).Height(36).
				KeyShortcuts(ui.Cmd, ui.KeyEnter).
				Tooltip("继续压缩剩余图片 · " + shortcutLabel("↵")).
				Disabled(s.busy).OnClick(s.startBatchCompression)
		})
	default:
		if s.batch.outputMode == batchOutputFolder && s.batch.outputDir != "" && done > 0 {
			ui.Row(c).Gap(8).Children(func() {
				ui.Button(c.Key("batch-restart"), "重新压缩").Grow(1).Height(36).
					KeyShortcuts(ui.Cmd, ui.KeyEnter).
					Disabled(s.busy || !canRun).OnClick(s.restartBatchCompression)
				ui.PrimaryButton(c.Key("batch-open-output"), "打开输出文件夹").Grow(1).Height(36).
					Disabled(s.busy).OnClick(s.openOutputDir)
			})
		} else {
			ui.Button(c.Key("batch-restart"), "重新压缩").Height(36).
				KeyShortcuts(ui.Cmd, ui.KeyEnter).
				Disabled(s.busy || !canRun).OnClick(s.restartBatchCompression)
		}
	}
	ui.Text(c, "不缩放尺寸 · 请保留原图").FontSize(12).TextColor(t.TextMuted)
}

func (s *application) batchOutputCard(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Padding(10, 12).Gap(8).Radius(12).Background(t.Background).Children(func() {
		if ui.Segmented(c.Key("batch-output-mode"), &s.batch.outputMode, "保存到文件夹", "覆盖原图").
			Label("输出方式").FillEqually().Height(30).Disabled(s.busy).Changed() {
			if s.resetBatchRunState() {
				s.setMessage("输出方式已切换，请重新压缩。", false)
			}
		}
		if s.batch.outputMode == batchOutputFolder {
			dirText, dirColor, dirTooltip := "压缩时选择输出文件夹", t.TextMuted, "将在开始压缩时选择保存文件夹"
			btnLabel := "选择位置"
			if s.batch.outputDir != "" {
				dirText = s.batch.outputDir
				dirColor = t.Text
				dirTooltip = s.batch.outputDir
				btnLabel = "更改"
			}
			ui.Row(c).Height(28).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, dirText).FontSize(12).TextColor(dirColor).
					SingleLine().TruncateMiddle().Grow(1).Tooltip(dirTooltip)
				ui.Button(c.Key("batch-output-dir"), btnLabel).Height(28).
					Disabled(s.busy).OnClick(s.chooseOutputDir)
			})
		} else {
			ui.Text(c, "缩小后的文件将直接替换原图，未减小则跳过，无法撤销。").
				FontSize(12).TextColor(t.Danger).MaxLines(2)
		}
	})
}

func (s *application) batchResultSummary(c *ui.Context) {
	t := c.Theme()
	total, pending, done, failed, origTotal, outTotal, _ := s.batchStats()
	var doneOrig int64
	var unchanged int
	for _, item := range s.batch.items {
		if item.status == batchDone {
			doneOrig += item.originalSize
			if item.unchanged {
				unchanged++
			}
		}
	}
	firstLabel, original := "原始总计", formatSize(origTotal)
	secondLabel, output := "输出总计", "—"
	label, color := fmt.Sprintf("等待压缩 · %d 张", total), t.TextMuted
	switch {
	case s.busy && s.batch.total > 0:
		label, color = fmt.Sprintf("处理中 %d / %d", s.batch.done, s.batch.total), t.Accent
		if done > 0 {
			output = formatSize(outTotal)
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
		} else {
			label, color = "未减小（0.0%）", t.TextMuted
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
	summaryCardWithNote(c, "批量结果", label, color, firstLabel, original, secondLabel, output, note)
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
