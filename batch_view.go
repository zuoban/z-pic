package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"zpic/internal/compress"
)

func (s *application) batchListPanel(c *ui.Context) {
	t := c.Theme()
	width, _ := c.Size()
	mobile := width < 720
	narrow := width < 560
	compactQueue := narrow || width >= 720 && width < 920
	pad := float32(16)
	if width < 960 {
		pad = 12
	}
	visible := s.filteredBatchIndices()
	minW := float32(340)
	if mobile {
		minW = 0
	}
	panel := ui.Column(c).Grow(1).MinWidth(minW)
	if mobile {
		panel.FillWidth().MinHeight(360)
	}
	panel.Children(func() {
		ui.Column(c).FillWidth().Padding(pad, pad, 10, pad).Gap(8).Children(func() {
			headerRow := ui.Row(c).MinHeight(30).Gap(6).AlignItems(ui.Center)
			if compactQueue {
				headerRow.Wrap()
			}
			headerRow.Children(func() {
				headerText := fmt.Sprintf("批量队列 · %d 张", len(s.batch.items))
				if s.batchFilterActive() {
					headerText = fmt.Sprintf("批量队列 · %d / %d 张", len(visible), len(s.batch.items))
				}
				ui.Text(c, headerText).
					FontSize(14).FontWeight(600).FontFeatures("tnum").
					SingleLine().TruncateMiddle().Width(0).Grow(1).MinWidth(110).Tooltip(headerText)
				iconLabelButton(c, quietButton(c, "batch-add-files", "", 30, s.modalOpen()).Padding(0, 9),
					"添加图片", actionImageIcon, 12, 500).
					KeyShortcuts(ui.Cmd, ui.KeyO).
					Tooltip("添加更多图片 · " + shortcutLabel("O")).
					OnClick(s.chooseFile)
				iconLabelButton(c, quietButton(c, "batch-add-folder", "", 30, s.modalOpen()).Padding(0, 9),
					"添加文件夹", actionFolderIcon, 12, 500).
					Tooltip("从文件夹添加图片").
					OnClick(s.chooseFolder)
				if doneCount := s.batch.cachedStats.done; doneCount > 0 {
					quietButton(c, "batch-remove-done", "清除已完成", 30, s.modalOpen() || s.batch.running).
						FontSize(12).FontWeight(500).Padding(0, 9).
						Tooltip(fmt.Sprintf("从队列移除 %d 张已完成图片", doneCount)).
						OnClick(s.removeCompletedBatchItems)
				}
				ui.Box(c).Size(1, 14).Background(hairlineDivider(c))
				iconLabelButton(c, ghostDestructiveButton(c, "batch-clear", "", 30, s.modalOpen()).Padding(0, 8),
					"清空", actionCloseIcon, 12, 500).
					Tooltip("清空当前批量队列").
					OnClick(s.clearBatch)
			})
			filterRow := ui.Row(c).MinHeight(30).Gap(8).AlignItems(ui.Center)
			if narrow {
				filterRow.Wrap()
			}
			filterRow.Children(func() {
				stats := s.batch.cachedStats
				options := [...]struct {
					label string
					count int
					dot   ui.Color
				}{
					{fmt.Sprintf("全部 %d", stats.total), stats.total, ui.Transparent},
					{fmt.Sprintf("待处理 %d", stats.pending), stats.pending, ui.Transparent},
					{fmt.Sprintf("已完成 %d", stats.done), stats.done, ui.Transparent},
					{fmt.Sprintf("失败 %d", stats.failed), stats.failed, t.Danger},
					{fmt.Sprintf("未达目标 %d", stats.targetSizeMissed), stats.targetSizeMissed, t.Warning},
				}
				parts := ui.SegmentedBase(c.Key("batch-filter"), &s.batch.filter, len(options))
				trackRadius, segRadius := innerRadius(c), float32(6)
				if usesGlass(c) {
					trackRadius, segRadius = 15, 13
				}
				trackBg := tonalWellSurface(c)
				if !usesGlass(c) && !t.Dark {
					trackBg = ui.Hex("#e6eaf1")
				}
				parts.Track.Label("队列筛选").Height(30).Padding(2).Gap(2).
					AlignItems(ui.Stretch).Radius(trackRadius).
					Background(trackBg).Border(1, segmentedTrackBorder(c)).
					FillWidth().
					Tooltip("按整个队列计数。未达目标的图片也计入已完成。搜索仅在当前筛选中查找。").
					Children(func() {
						for index, opt := range options {
							active := s.batch.filter == index
							segment := parts.Segment(index).Label(opt.label).Padding(0, 6).Radius(segRadius).
								Center().Border(1, ui.Transparent)
							textColor, weight := t.TextMuted, 400
							if !active && opt.count == 0 && index > 0 {
								textColor = metadataColor(c).Alpha(0.75)
							}
							if active {
								textColor, weight = t.Text, 600
								segment.Background(segmentedThumbSurface(c)).Border(1, segmentedThumbBorder(c))
								if !t.Dark && !c.Preferences().HighContrast {
									segment.Shadow(0, 1, 2, 0, ui.RGBA(17, 20, 26, 0.08))
								}
							} else if segment.Pressed() {
								textColor = t.Text
								segment.Background(glassControlPressed(c))
							} else if segment.Hovered() {
								textColor = t.Text
								segment.Background(glassControlHover(c))
							}
							showDot := opt.count > 0 && opt.dot != ui.Transparent
							segment.Children(func() {
								ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
									if showDot {
										ui.Box(c).Size(5, 5).Radius(2.5).Shrink(0).Background(opt.dot)
									}
									ui.Text(c, opt.label).FontSize(12).FontWeight(weight).FontFeatures("tnum").
										TextColor(textColor).SingleLine().NoWrap()
								})
							})
						}
					}).FillEqually()
				if parts.Track.Changed() {
					visible = s.filteredBatchIndices()
				}
			})
			ui.Row(c).Height(30).Gap(8).AlignItems(ui.Center).Children(func() {
				searchField := ui.SearchField(c.Key("batch-search"), &s.batch.query).
					Bind(&s.batchSearchRef).
					Label("筛选文件名").Placeholder("搜索文件名").
					Tooltip("搜索相对路径或文件名 · " + shortcutLabel("F")).
					Height(30).Grow(1)
				styleGlassField(searchField, c, 30)
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
				bannerRow := ui.Row(c).MinHeight(34).Padding(6, 10).Gap(8).Radius(innerRadius(c)).
					Background(statusTintSurface(c, t.Danger)).Border(1, statusTintBorder(c, t.Danger)).AlignItems(ui.Center)
				if compactQueue {
					bannerRow.Column().Padding(8, 10).AlignItems(ui.Stretch)
				}
				bannerRow.Children(func() {
					titleRow := ui.Row(c).Gap(6).AlignItems(ui.Center).Width(0).Grow(1).MinWidth(120)
					if compactQueue {
						titleRow.FillWidth().Grow(0).MinWidth(0)
					}
					titleRow.Children(func() {
						ui.Box(c).Size(6, 6).Radius(3).Shrink(0).Background(t.Danger)
						ui.Text(c, bannerText).
							FontSize(12).FontWeight(500).FontFeatures("tnum").TextColor(t.Danger).
							SingleLine().Width(0).Grow(1).MinWidth(0).Tooltip(bannerText)
					})
					actions := func() {
						if invalidCount > 0 {
							ghostButton(c, "batch-remove-invalid", "移除无效项", 28, s.modalOpen()).
								FontSize(12).Padding(0, 9).
								Tooltip(fmt.Sprintf("从队列移除 %d 张无法处理的图片", invalidCount)).
								OnClick(s.removeInvalidBatchItems)
						}
						if retryFailed > 0 {
							ghostButton(c, "batch-remove-failed", "移除失败项", 28, s.modalOpen()).
								FontSize(12).Padding(0, 9).
								Tooltip("从队列移除全部失败的图片").
								OnClick(s.removeFailedBatchItems)
							iconLabelButton(c, quietButton(c, "batch-retry-failed", "", 28, s.modalOpen()).Padding(0, 10),
								"重试失败项", toolbarRestartIcon, 12, 500).
								Tooltip(fmt.Sprintf("重新压缩 %d 张失败的图片", retryFailed)).
								OnClick(s.retryFailedBatchItems)
						}
					}
					if compactQueue {
						ui.Row(c).FillWidth().Gap(6).Wrap().Children(actions)
					} else {
						ui.Row(c).Gap(6).AlignItems(ui.Center).Children(actions)
					}
				})
			}
		})
		ui.Box(c).FillWidth().Height(1).Background(hairlineDivider(c))
		ui.Box(c).Grow(1).FillWidth().MinHeight(180).Clip().Children(func() {
			if len(visible) == 0 {
				s.batch.selected = -1
				s.batch.listSelected = -1
				s.batch.listSelectedKey = ""
				ui.Column(c).Fill().Center().Gap(10).Padding(24).Children(func() {
					ui.Text(c, s.emptyBatchFilterMessage()).
						FontSize(13).FontWeight(500).TextColor(t.TextMuted)
					if query := strings.TrimSpace(s.batch.query); query != "" {
						ui.Text(c, "搜索：“"+query+"”").FontSize(12).TextColor(metadataColor(c)).
							SingleLine().TruncateMiddle().FillWidth().TextAlign(ui.Center).Tooltip(query)
					}
					hasQuery := strings.TrimSpace(s.batch.query) != ""
					if hasQuery || s.batchFilterActive() {
						ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
							if hasQuery {
								quietButton(c, "batch-clear-search", "清除搜索", 30, false).Padding(0, 12).
									Tooltip("保留当前状态筛选和排序，仅清除搜索词").
									OnClick(func() {
										s.batch.query = ""
										s.batchSearchRef.Focus(c)
									})
							}
							if s.batchFilterActive() {
								quietButton(c, "batch-reset-filter", "显示全部", 30, false).Padding(0, 12).
									OnClick(s.resetBatchFilter)
							}
						})
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
			baseTheme := c.Theme()
			var list ui.Element
			var rowAction func()
			ui.Themed(c, customSelectionListTheme(c)).Fill().Background(ui.Transparent).Children(func() {
				list = ui.List(c, &s.batch.list, len(visible)).
					Label("批量图片列表").Fill().Padding(6, pad-4).Gap(2)
				list.Rows(func(r ui.ListRow) {
					if s.batch == nil || r.Index >= len(visible) {
						return
					}
					itemIndex := visible[r.Index]
					if itemIndex < 0 || itemIndex >= len(s.batch.items) {
						return
					}
					showDivider := r.Index < len(visible)-1 && visible[r.Index+1] != s.batch.selected
					s.batchListRow(r, itemIndex, showDivider, baseTheme, &rowAction)
				})
			})
			if s.batch.listSelected >= 0 && s.batch.listSelected < len(visible) {
				s.batch.selected = visible[s.batch.listSelected]
				s.batch.listSelectedKey = s.batch.items[s.batch.selected].absPath
			} else {
				s.batch.selected = -1
				s.batch.listSelectedKey = ""
			}
			if rowAction != nil {
				rowAction()
				c.Invalidate()
				if s.batch == nil {
					return
				}
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
	t := c.Theme()
	current := batchSortOptionFor(s.batch.sort)
	changed := false
	highlighted := s.batch.sort != batchSortDefault
	bg := glassControlSurface(c)
	border := controlBorder(c)
	if highlighted {
		bg = glassSelectionBackground(c)
		border = selectionBorder(c)
	}
	picker := ui.ButtonBase(c.Key("batch-sort"))
	picker.PopoverMenuButton(func(m *ui.Menu) {
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
			stylePopoverPanel(panel, c)
		},
	}).Label("队列排序").Value(current.label).Height(30).Padding(0, 10).Gap(5).AlignItems(ui.Center).
		Radius(innerRadius(c)).Background(bg).Border(1, border).
		Tooltip("排序方式：" + current.menuLabel)
	if picker.Pressed() {
		picker.Background(glassControlPressed(c)).Border(1, controlHoverBorder(c))
	} else {
		controlElevation(picker, c)
		if picker.Hovered() {
			picker.Background(glassControlHover(c)).Border(1, controlHoverBorder(c))
		}
	}
	picker.Children(func() {
		ui.Text(c, current.label).FontSize(12).FontWeight(500).SingleLine().NoWrap()
		ui.Icon(c, settingsChevronIcon).Size(14, 14).Shrink(0).TextColor(t.TextMuted)
	})
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
	case batchFilterTargetSizeMissed:
		return "没有未达目标体积的图片"
	default:
		return "没有符合条件的图片"
	}
}

func (s *application) batchListRow(r ui.ListRow, itemIndex int, showDivider bool, baseTheme *ui.Theme, onAction *func()) {
	c := r.Context
	item := s.batch.items[itemIndex]
	ui.Themed(c, baseTheme).FillWidth().Background(ui.Transparent).Children(func() {
		t := c.Theme()
		statusText, _ := batchItemStatus(item, t)
		detailText, detailColor := batchItemDetail(item, t)
		background, border := ui.Transparent, ui.Transparent
		borderWidth := float32(1)
		if r.Selected() {
			background, border = cardSelectionBackground(c), selectionBorder(c)
			if r.ListFocused() {
				borderWidth = 1.5
			}
		} else if r.Pressed() {
			background = glassControlPressed(c)
		} else if r.Hovered() {
			background = glassControlHover(c)
		}
		rowCard := ui.Row(c).FillWidth().Padding(8, 10).Gap(10).AlignItems(ui.Center).
			Radius(10).Background(background).Border(borderWidth, border)
		if showDivider && !r.Selected() && !r.Hovered() && !r.Pressed() {
			divider := subtleRowDivider(c)
			rowCard.DrawOver(func(p *ui.Painter, bounds ui.Rect) {
				p.Line(bounds.X+80, bounds.Y+bounds.H-0.5, bounds.X+bounds.W-10, bounds.Y+bounds.H-0.5, 1, divider)
			})
		}
		rowCard.Children(func() {
			s.batchThumbnailView(c, item)
			ui.Column(c).Width(0).Grow(1).Gap(3).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, item.relPath).FontSize(13).FontWeight(500).
						TextColor(t.Text).SingleLine().TruncateMiddle().
						Width(0).Grow(1).Tooltip(item.path)
					if item.status == batchDone && item.targetSizeMissed {
						statusCapsule(c, "未达目标", t.Warning).SingleLine().
							Tooltip("目标体积 ≤ " + compress.FormatTargetSize(item.targetSizeBytes))
					}
					switch {
					case item.status == batchDone && !item.unchanged && item.outputSize < item.originalSize:
						statusCapsule(c, statusText, t.Success).SingleLine()
					case item.status == batchDone && !item.unchanged && item.outputSize > item.originalSize && item.originalSize > 0:
						statusCapsule(c, statusText, t.Warning).SingleLine()
					case item.status == batchFailed:
						statusCapsule(c, statusText, t.Danger).SingleLine()
					case item.status == batchProcessing:
						accentCapsule(c, statusText)
					default:
						neutralCapsule(c, statusText).Padding(2, 7)
					}
				})
				width, _ := c.Size()
				metaRow := ui.Row(c).Gap(8).Wrap().AlignItems(ui.Center)
				stackMetadata := width >= 720 && width < 960
				if stackMetadata {
					metaRow.Column().Gap(4).AlignItems(ui.Stretch)
				}
				metaRow.Children(func() {
					ui.Text(c, batchItemMetadata(item)).FontSize(12).FontFeatures("tnum").
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
		menuOpen := ui.Local(rowCard, "batch-row-menu", func() bool { return false })
		if s.modalOpen() {
			*menuOpen = false
		} else {
			wasOpen := *menuOpen
			m := ui.ContextMenuBase(c, rowCard, menuOpen)
			if !wasOpen && m.Open() {
				s.batch.selected = itemIndex
				s.batch.list.Choose(r.Index)
				s.batch.listSelectedKey = item.absPath
			}
			m.Popup(func(panel ui.Element) {
				stylePopoverPanel(panel, c).MinWidth(168).Padding(4)
				s.batchRowContextMenuItems(c, m, itemIndex, item, onAction)
			})
		}
	})
}

func (s *application) batchRowContextMenuItems(c *ui.Context, m ui.MenuParts, itemIndex int, item batchItem, onAction *func()) {
	setAction := func(fn func()) {
		if onAction != nil {
			*onAction = fn
		}
	}
	hasPrimary := false
	if !item.invalidInput {
		hasPrimary = true
		if batchContextMenuItem(c, m, "预览", actionPreviewIcon, false, false) {
			idx := itemIndex
			setAction(func() { s.openBatchPreview(idx) })
		}
	}
	if item.status == batchPending && !item.invalidInput && !s.batch.running {
		hasPrimary = true
		if batchContextMenuItem(c, m, "开始压缩", toolbarCompressIcon, false, false) {
			idx := itemIndex
			setAction(func() { s.compressBatchItem(idx) })
		}
	}
	if item.status == batchFailed && !item.invalidInput && !s.batch.running {
		hasPrimary = true
		if batchContextMenuItem(c, m, "重新压缩", toolbarRestartIcon, false, false) {
			idx := itemIndex
			setAction(func() { s.compressBatchItem(idx) })
		}
	}
	revealPath := ""
	if item.status == batchDone && item.savedPath != "" {
		revealPath = item.savedPath
	} else if item.path != "" {
		revealPath = item.path
	}
	if revealPath != "" {
		hasPrimary = true
		if batchContextMenuItem(c, m, "定位文件", actionRevealIcon, false, false) {
			path := revealPath
			setAction(func() { s.revealFileInFolder(path) })
		}
	}
	if s.canCopyBatchItem(item) {
		hasPrimary = true
		if batchContextMenuItem(c, m, "复制图片", actionCopyIcon, false, false) {
			idx := itemIndex
			setAction(func() { s.copyBatchItem(idx) })
		}
	}
	if hasPrimary {
		ui.Box(c).FillWidth().Height(1).Margin(4, 6).Background(hairlineDivider(c)).Shrink(0)
	}
	if batchContextMenuItem(c, m, "从队列移除", actionTrashIcon, true, s.batch.running) {
		idx := itemIndex
		setAction(func() { s.removeBatchItem(idx) })
	}
}

func batchContextMenuItem(c *ui.Context, m ui.MenuParts, label string, icon *ui.SVG, danger, disabled bool) bool {
	t := c.Theme()
	el := m.Item().FillWidth().Height(28).Padding(0, 8).Gap(8).AlignItems(ui.Center).
		Radius(6).Shrink(0).Label(label).Disabled(disabled)
	textColor := t.Text
	iconColor := t.TextMuted
	if danger {
		textColor = t.Danger
		iconColor = t.Danger
	}
	if disabled {
		textColor = t.TextMuted
		iconColor = t.TextMuted
	} else if el.Highlighted() {
		el.Background(t.Accent)
		textColor = t.AccentText
		iconColor = t.AccentText
	}
	el.TextColor(textColor).Children(func() {
		if icon != nil {
			ui.Icon(c, icon).Size(14, 14).TextColor(iconColor).Shrink(0)
		}
		ui.Text(c, label).FontSize(12).FontWeight(500).TextColor(textColor).SingleLine().NoWrap()
	})
	return el.Clicked()
}

func (s *application) batchThumbnailView(c *ui.Context, item batchItem) {
	t := c.Theme()
	entry := s.batchThumbnail(item)
	label := item.relPath + " 缩略图"
	unavailable := item.invalidInput || entry == nil || entry.failed
	failedPlaceholder := (entry == nil || entry.bitmap == nil) && (item.status == batchFailed || item.invalidInput)
	bg, border := canvasWellBackground(c), glassBorder(c)
	if failedPlaceholder {
		bg = statusTintSurface(c, t.Danger)
		border = statusTintBorder(c, t.Danger)
	}
	box := ui.Box(c).Size(60, 60).Shrink(0).Radius(8).Background(bg).Border(1, border).
		Clip().Role(ui.RoleImage).Label(label)
	if entry != nil && entry.bitmap != nil && supportsAlphaFormat(item.format) {
		box.Draw(func(p *ui.Painter, bounds ui.Rect) {
			drawAlphaCheckerboard(p, bounds, t.Dark)
		})
	}
	box.Children(func() {
		if entry != nil && entry.bitmap != nil {
			ui.Image(c, entry.bitmap).Fill().Fit(ui.Contain)
			return
		}
		tooltip := "正在生成缩略图"
		if unavailable {
			tooltip = "无法预览此图片"
		}
		ui.Box(c).Fill().Center().Tooltip(tooltip).Children(func() {
			ui.Box(c).Size(18, 18).Draw(func(p *ui.Painter, bounds ui.Rect) {
				frame := ui.Rect{X: bounds.X + 1, Y: bounds.Y + 2, W: 16, H: 14}
				stroke := metadataColor(c)
				if failedPlaceholder {
					stroke = t.Danger.Alpha(0.78)
				} else if unavailable {
					stroke = t.TextMuted.Alpha(0.68)
				}
				p.Stroke(frame, stroke, 2.5, 1.4)
				p.StrokePolyline([][2]float32{
					{bounds.X + 3.5, bounds.Y + 13},
					{bounds.X + 7.5, bounds.Y + 8.5},
					{bounds.X + 11, bounds.Y + 11.5},
					{bounds.X + 14.5, bounds.Y + 9.5},
				}, 1.4, stroke)
				if failedPlaceholder {
					p.Line(bounds.X+3, bounds.Y+15, bounds.X+15, bounds.Y+3, 1.4, stroke)
				}
			})
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
			return fmt.Sprintf("+%0.1f%%", grew), t.Warning
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

func batchItemMetadata(item batchItem) string {
	meta := strings.ToUpper(item.format)
	if item.width > 0 && item.height > 0 {
		meta += fmt.Sprintf(" · %d × %d", item.width, item.height)
	}
	if item.status == batchDone && item.targetResized {
		meta += " · 自动缩小"
	}
	return meta
}

func batchItemDetail(item batchItem, t *ui.Theme) (string, ui.Color) {
	if item.status == batchFailed {
		return item.errMsg, t.Danger
	}
	return batchItemMetadata(item) + " · " + batchItemSizeDetail(item), t.TextMuted
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
	pad := float32(16)
	if width < 960 {
		pad = 12
	}
	ui.Box(c).FillWidth().Height(1).Background(hairlineDivider(c))
	bar := ui.Row(c).FillWidth().Padding(10, pad).Gap(8).
		Background(sidebarSurface(c)).AlignItems(ui.Center)
	compact := width < 560 || width >= 720 && width < 1100
	if compact {
		bar.Column().AlignItems(ui.Stretch)
	}
	bar.Children(func() {
		details := ui.Column(c).Width(0).Grow(1).MinWidth(140).Gap(2)
		if compact {
			details.FillWidth().Grow(0).MinWidth(0)
		}
		details.Children(func() {
			ui.Text(c, filepath.Base(item.path)).FontSize(13).FontWeight(600).
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
				iconLabelButton(c, quietButton(c, "batch-compress-item", "", 30, s.modalOpen()).Padding(0, 10),
					"压缩此张", toolbarCompressIcon, 12, 500).
					Tooltip("仅压缩此张图片").
					OnClick(func() { s.compressBatchItem(selected) })
			}
			if item.status == batchFailed && !item.invalidInput {
				iconLabelButton(c, quietButton(c, "batch-retry-item", "", 30, s.modalOpen()).Padding(0, 10),
					"重试", toolbarRestartIcon, 12, 500).
					Tooltip("重新压缩此失败图片").
					OnClick(func() { s.compressBatchItem(selected) })
			}
			outPath := batchItemOutputPath(item)
			if outPath != "" {
				iconLabelButton(c, ghostButton(c, "batch-reveal-item", "", 30, s.modalOpen()).Padding(0, 8),
					"定位文件", actionRevealIcon, 12, 500).
					Tooltip("在文件夹中显示输出文件").
					OnClick(func() { s.revealFileInFolder(outPath) })
				iconLabelButton(c, ghostButton(c, "batch-copy-item", "", 30, !s.canCopyBatchItem(item)).Padding(0, 8),
					"复制", actionCopyIcon, 12, 500).
					Label("复制图片").Tooltip("将压缩结果复制到剪贴板").
					OnClick(func() { s.copyBatchItem(selected) })
			}
			iconLabelButton(c, ghostButton(c, "batch-preview", "", 30, s.modalOpen() || item.invalidInput).Padding(0, 8),
				"预览", actionPreviewIcon, 12, 500).
				Tooltip("查看或对比选中图片").
				OnClick(func() { s.openBatchPreview(selected) })
			if outPath != "" {
				idx := selected
				dragExportControl(c, "batch-drag-item", "拖出图片", "拖出",
					"按住将此压缩输出文件拖到 Finder 或其他应用",
					30, 12, 500, s.modalOpen(),
					func() transfer.Data { return s.batchItemDragExportData(idx) }, s.dragExportOptions())
			}
			if compact {
				ui.Box(c).Grow(1).MinWidth(4)
			} else {
				ui.Box(c).Size(1, 14).Background(hairlineDivider(c))
			}
			iconLabelButton(c, ghostDestructiveButton(c, "batch-remove", "", 30, s.modalOpen()).Padding(0, 8),
				"移除", actionTrashIcon, 12, 500).
				Tooltip("从批量队列移除此图片 · Delete / Backspace").
				OnClick(s.removeSelectedBatchItem)
		}
		if compact {
			ui.Row(c).FillWidth().Gap(6).Wrap().AlignItems(ui.Center).Children(actions)
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
	pad := float32(16)
	if width < 960 {
		pad = 12
	}
	minW := float32(340)
	if mobile {
		minW = 0
	}
	panel := ui.Column(c).Grow(1).MinWidth(minW)
	if mobile {
		panel.FillWidth().MinHeight(380)
	}
	panel.Children(func() {
		ui.Column(c).Grow(1).FillWidth().Padding(pad, pad, 12, pad).Gap(10).Children(func() {
			inlinePreviewMode := width >= 960 && s.result != nil && !s.overwritten
			ui.Row(c).MinHeight(30).Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
				iconLabelButton(c, quietButton(c, "batch-back", "", 30, s.modalOpen()).Padding(0, 10),
					"返回列表", actionBackIcon, 12, 500).
					KeyShortcuts(0, ui.KeyEscape).Tooltip("返回批量列表 · Esc").
					OnClick(s.closeBatchPreview)
				visible := s.filteredBatchIndices()
				posCurrent, posTotal := s.batch.previewIndex+1, len(s.batch.items)
				if pos := slices.Index(visible, s.batch.previewIndex); pos >= 0 {
					posCurrent, posTotal = pos+1, len(visible)
				}
				ui.Textf(c, "%d / %d", posCurrent, posTotal).
					FontSize(12).FontWeight(500).FontFeatures("tnum").TextColor(metadataColor(c)).SingleLine().Grow(1)
				if inlinePreviewMode {
					s.previewModeControl(c)
				}
				iconLabelButton(c, quietButton(c, "batch-prev", "", 30, s.modalOpen() || !s.hasAdjacentBatchPreview(-1)).Padding(0, 9),
					"上一张", actionBackIcon, 12, 500).
					KeyShortcuts(ui.Cmd, ui.KeyBracketLeft).
					Tooltip("上一张 · " + shortcutLabel("[")).
					OnClick(func() { s.stepBatchPreview(-1) })
				nextBtn := quietButton(c, "batch-next", "", 30, s.modalOpen() || !s.hasAdjacentBatchPreview(1)).Padding(0, 9).
					Label("下一张").Gap(4).
					KeyShortcuts(ui.Cmd, ui.KeyBracketRight).
					Tooltip("下一张 · " + shortcutLabel("]")).
					OnClick(func() { s.stepBatchPreview(1) })
				nextBtn.Children(func() {
					ui.Text(c, "下一张").FontSize(12).FontWeight(500).SingleLine()
					ui.Icon(c, actionNextIcon).Size(14, 14).Shrink(0)
				})
			})
			if s.preview != nil {
				if inlinePreviewMode {
					s.previewZoomToolbar(c)
				} else {
					s.previewToolbar(c)
				}
			}
			ui.Box(c).Grow(1).FillWidth().MinHeight(220).Radius(innerRadius(c)).
				Background(canvasWellBackground(c)).Border(1, glassBorder(c)).Clip().Children(func() {
				if s.preview != nil {
					s.previewCanvas(c)
				}
			})
		})
		if s.file != nil {
			meta := fmt.Sprintf("%s · %d × %d · %s",
				strings.ToUpper(s.file.format), s.file.width, s.file.height, formatSize(s.file.size))
			metaColor := metadataColor(c)
			metaTooltip := meta
			if result := s.result; result != nil {
				switch {
				case result.TargetResized:
					if s.comparison.mode != previewOriginal {
						meta = outputPreviewMetadata(result)
					}
				case result.Unchanged:
					meta += " · 未减小"
				default:
					meta += " → " + formatSize(result.OutputSize)
				}
				metaTooltip = meta
			} else if previewItem != nil && previewItem.status == batchFailed {
				meta += "  ·  失败：" + previewItem.errMsg
				metaColor = t.Danger
				metaTooltip = previewItem.errMsg
			}
			ui.Box(c).FillWidth().Height(1).Background(hairlineDivider(c))
			footerRow := ui.Row(c).FillWidth().MinHeight(50).Padding(9, pad).Gap(8).
				Background(sidebarSurface(c)).AlignItems(ui.Center)
			compactFooter := width < 1100
			if compactFooter {
				footerRow.Column().AlignItems(ui.Stretch)
			}
			footerRow.Children(func() {
				details := ui.Column(c).Width(0).Grow(1).MinWidth(140).Gap(2)
				if compactFooter {
					details.FillWidth().Grow(0).MinWidth(0)
				}
				details.Children(func() {
					ui.Text(c, filepath.Base(s.file.path)).FontSize(13).FontWeight(600).
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
							iconLabelButton(c, quietButton(c, "batch-preview-reveal", "", 30, s.modalOpen()).Padding(0, 10),
								"定位文件", actionRevealIcon, 12, 500).
								Tooltip("在文件夹中显示输出文件").
								OnClick(func() { s.revealFileInFolder(outPath) })
							idx := previewIdx
							dragExportControl(c, "batch-preview-drag", "拖出图片", "拖出",
								"按住将此压缩输出文件拖到 Finder 或其他应用",
								30, 12, 500, s.modalOpen(),
								func() transfer.Data { return s.batchItemDragExportData(idx) }, s.dragExportOptions())
						}
						iconLabelButton(c, ghostDestructiveButton(c, "batch-preview-remove", "", 30, s.modalOpen()).Padding(0, 8),
							"移除", actionTrashIcon, 12, 500).
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
		ui.Column(c).FillWidth().Gap(8).Children(summaryBody)
	} else {
		ui.Scroll(c.Key("batch-compression-summary")).Grow(1).MinHeight(0).FillWidth().Gap(8).Children(summaryBody)
	}
	if s.busy {
		s.workFeedback(c)
	} else if s.message != "" {
		color, role := t.TextMuted, ui.RoleStatus
		if s.failed {
			color, role = t.Danger, ui.RoleAlert
			ui.Box(c).FillWidth().Padding(8, 10).Radius(innerRadius(c)).
				Background(statusTintSurface(c, t.Danger)).Border(1, statusTintBorder(c, t.Danger)).Children(func() {
				ui.Text(c, s.message).FontSize(12).TextColor(color).Role(role).
					MaxLines(3).Tooltip(s.message)
			})
		} else {
			ui.Text(c, s.message).FontSize(12).TextColor(color).Role(role).
				MaxLines(2).Tooltip(s.message)
		}
	}
	var donePaths []string
	if !s.batch.previewOpen && !s.modalOpen() {
		donePaths = s.batchDoneOutputPaths()
	}
	hasCopyResult := s.batch.previewOpen && s.result != nil
	if showOutput || len(donePaths) > 0 || hasCopyResult {
		ui.Box(c).FillWidth().Height(1).Background(hairlineDivider(c)).Shrink(0)
		ui.Column(c).FillWidth().Gap(8).Shrink(0).Children(func() {
			if showOutput {
				iconLabelButton(c, primaryAction(c, "batch-open-output", "", s.modalOpen()).Height(36).FillWidth(),
					"打开输出文件夹", actionFolderIcon, 13, 500).
					OnClick(s.openOutputDir)
			}
			if len(donePaths) > 0 {
				label := fmt.Sprintf("拖出已完成（%d 张）", len(donePaths))
				dragExportControl(c, "drag-out-batch", label, label,
					"按住将全部已完成的压缩文件直接拖到 Finder 或其他应用",
					32, 12, 500, false, s.batchAllDoneDragExportData, s.dragExportOptions()).FillWidth()
			}
			if hasCopyResult {
				iconLabelButton(c, quietButton(c, "copy-result", "", 32, !s.canCopyResult()).FillWidth().Padding(0, 10),
					"复制图片", actionCopyIcon, 12, 500).
					KeyShortcuts(ui.Cmd, ui.KeyC).
					Tooltip("将当前图片的压缩结果复制到剪贴板，可直接粘贴图片 · " + shortcutLabel("C")).OnClick(s.copyResult)
			}
		})
	}
}

func (s *application) batchOutputCard(c *ui.Context) {
	t := c.Theme()
	isOverwrite := s.batch.outputMode != batchOutputFolder && !(s.batch.previewOpen && s.batch.previewIndex >= 0 && s.batch.previewIndex < len(s.batch.items) && batchItemOutputPath(s.batch.items[s.batch.previewIndex]) != "")
	wellBg := tonalWellSurface(c)
	wellBorder := tonalWellBorder(c)
	if isOverwrite {
		wellBg = statusTintSurface(c, t.Danger)
		wellBorder = statusTintBorder(c, t.Danger)
	}
	ui.Column(c).Padding(10, 12).Gap(5).Radius(innerRadius(c)).Background(wellBg).Border(1, wellBorder).Children(func() {
		if s.batch.previewOpen && s.batch.previewIndex >= 0 && s.batch.previewIndex < len(s.batch.items) {
			item := s.batch.items[s.batch.previewIndex]
			if path := batchItemOutputPath(item); path != "" {
				ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, presetSaveIcon).Size(13, 13).TextColor(metadataColor(c)).Shrink(0)
					ui.Text(c, "输出文件").FontSize(11).FontWeight(600).TextColor(metadataColor(c)).Grow(1)
				})
				ui.Text(c, path).FontSize(12).FontWeight(500).TextColor(t.Text).SingleLine().TruncateMiddle().FillWidth().Tooltip(path)
				if item.overwritten {
					ui.Text(c, "已覆盖原图，无法撤销").FontSize(12).TextColor(t.TextMuted).MaxLines(2)
				}
				return
			}
		}
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			title := "保存方式"
			if s.batch.outputMode == batchOutputFolder && !s.batch.previewOpen && s.output.Mode == presetSaveAsk {
				title = "输出位置"
			}
			ui.Icon(c, presetSaveIcon).Size(13, 13).TextColor(metadataColor(c)).Shrink(0)
			ui.Text(c, title).FontSize(11).FontWeight(600).TextColor(metadataColor(c)).Grow(1)
			if isOverwrite {
				statusCapsule(c, "覆盖原图", t.Danger)
			}
			if s.batch.outputMode == batchOutputFolder && !s.batch.previewOpen && s.output.Mode == presetSaveAsk {
				keyboardKeycap(c, "S").Padding(0, 5)
			}
		})
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
			sourceFormat := ""
			if s.batch.selected >= 0 && s.batch.selected < len(s.batch.items) {
				selected := s.batch.items[s.batch.selected]
				name = selected.relPath
				sourceFormat = selected.effectiveSourceFormat()
			}
			outputFilenamePreviewForResolved(c, s.output, name, sourceFormat, s.outputFormat, s.lossless, false)
		} else {
			warning := "缩小后的文件将直接替换原图，未减小则跳过，无法撤销。"
			if s.backupToTrash {
				warning = "缩小后的文件会先将原图移入废纸篓再替换，未减小则跳过。"
			}
			if ext := outputExtensionForFormat(string(s.outputFormat)); ext != "" {
				warning = "同格式原图缩小后将直接替换，其他格式将在原文件夹另存为 " + ext + "。"
				if s.backupToTrash {
					warning = "同格式原图缩小后会移入废纸篓再替换，其他格式将在原文件夹另存为 " + ext + "。"
				}
			} else {
				for _, item := range s.batch.items {
					if item.effectiveSourceFormat() == "heic" {
						targetExt := outputExtensionForFormat(compress.ResolveOutputFormat("heic", s.outputFormat, s.lossless))
						warning += "HEIC 将在原文件夹另存为 " + targetExt + "。"
						break
					}
				}
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
		} else if doneOrig > 0 && outTotal > doneOrig {
			grew := float64(outTotal-doneOrig) / float64(doneOrig) * 100
			label, color = fmt.Sprintf("增大 %.1f%%", grew), t.Warning
			ratio = float64(outTotal) / float64(doneOrig)
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
	var note, hint string
	if !s.busy && done > 0 {
		var parts []string
		if doneOrig > outTotal {
			parts = append(parts, "共节省 "+formatSize(doneOrig-outTotal))
		} else if doneOrig > 0 && outTotal > doneOrig {
			parts = append(parts, "共增大 "+formatSize(outTotal-doneOrig))
			hint = batchGrowthHint(s.outputFormat)
		}
		if unchanged > 0 {
			if s.batch.outputMode == batchOutputOverwrite {
				parts = append(parts, fmt.Sprintf("跳过 %d 张未减小", unchanged))
			} else {
				parts = append(parts, fmt.Sprintf("%d 张未减小保持原样", unchanged))
			}
		}
		if resized := s.batch.cachedStats.targetResized; resized > 0 {
			parts = append(parts, fmt.Sprintf("%d 张自动缩小", resized))
		}
		note = strings.Join(parts, " · ")
	}
	summaryCardWithHint(c, "批量结果", label, color, firstLabel, original, secondLabel, output, note, hint, ratio, func() {
		missed := s.batch.cachedStats.targetSizeMissed
		if s.busy || missed == 0 {
			return
		}
		quietButton(c, "batch-show-target-missed", fmt.Sprintf("%d 张未达目标体积", missed), 28, s.modalOpen()).
			AlignSelf(ui.Start).FontSize(12).FontFeatures("tnum").
			Controls(c.Resolve(s.batch.list.Handle)).
			Tooltip("显示全部未达标图片并清除文件名搜索").OnClick(func() {
			s.batch.filter, s.batch.query = batchFilterTargetSizeMissed, ""
			if visible := s.filteredBatchIndices(); len(visible) > 0 {
				s.batch.selected = visible[0]
				s.batch.list.ScrollTo(0, ui.Start)
				// Handle.Focus also works when the previous filter hid the list.
				s.batch.list.Handle.Focus(c)
			}
		})
	})
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
	if s.backupToTrash {
		message = fmt.Sprintf("将压缩 %d 张图片；体积减小的文件会先将原件移入废纸篓，再替换为压缩结果，未减小的文件将保持不变。", targetCount)
	}
	if s.compressionOptions().TargetResizeMinDimension > 0 {
		message += "\n\n已启用自动缩小，部分图片的分辨率可能降低。"
	}
	if ui.AlertDialog(c.Key("batch-overwrite-confirmation"), &s.confirmBatchOverwrite,
		"确认批量覆盖原图？", message, "覆盖原图", "取消") == 0 {
		s.executeConfirmedBatchOverwrite()
	} else if !s.confirmBatchOverwrite {
		s.clearPendingBatchConfirmation()
	}
}
