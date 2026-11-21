package main

import (
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"zpic/internal/compress"
)

const (
	floatingCompactWidth   = 176
	floatingCompactHeight  = 184
	floatingExpandedWidth  = 480
	floatingExpandedHeight = 360
)

type floatingSyncSnapshot struct {
	batch           *batchState
	batchLen        int
	batchPending    int
	batchDone       int
	batchFailed     int
	batchOrigTotal  int64
	batchOutTotal   int64
	batchFirstPath  string
	batchLastPath   string
	file            *selectedImage
	result          *compress.Result
	lastSavedPath   string
	overwritten     bool
	busy            bool
	canceling       bool
	phase           string
	dialogOpen      bool
	dropQueueLen    int
	dropImporting   bool
	dropMessage     string
	dropFailed      bool
	dropFailuresLen int
}

type floatingState struct {
	expanded   bool
	listView   bool
	filter     int
	sort       int
	cursor     int
	selected   string
	paths      []string
	grid       ui.GridState
	list       ui.ListState
	lastSynced floatingSyncSnapshot
	synced     bool
}

func (s *application) floatingSyncSnapshot() floatingSyncSnapshot {
	snap := floatingSyncSnapshot{
		batch:           s.batch,
		file:            s.file,
		result:          s.result,
		lastSavedPath:   s.lastSavedPath,
		overwritten:     s.overwritten,
		busy:            s.busy,
		canceling:       s.canceling,
		phase:           s.phase,
		dialogOpen:      s.dialogOpen(),
		dropQueueLen:    len(s.dropQueue),
		dropImporting:   s.dropImporting,
		dropMessage:     s.dropMessage,
		dropFailed:      s.dropFailed,
		dropFailuresLen: len(s.dropFailures),
	}
	if s.batch != nil {
		total, pending, done, failed, origTotal, outTotal, _ := s.batchStats()
		snap.batchLen = total
		snap.batchPending = pending
		snap.batchDone = done
		snap.batchFailed = failed
		snap.batchOrigTotal = origTotal
		snap.batchOutTotal = outTotal
		if len(s.batch.items) > 0 {
			snap.batchFirstPath = s.batch.items[0].path
			snap.batchLastPath = s.batch.items[len(s.batch.items)-1].path
		}
	}
	return snap
}

func floatingPanelBounds(current, area mygo.Rectangle, expanded bool) mygo.Rectangle {
	width, height := floatingCompactWidth, floatingCompactHeight
	if expanded {
		width, height = floatingExpandedWidth, floatingExpandedHeight
	}
	width, height = min(width, area.Width), min(height, area.Height)
	left := current.X
	if current.X+current.Width/2 >= area.X+area.Width/2 {
		left = current.X + current.Width - width
	}
	return mygo.Rectangle{
		X:     max(area.X, min(left, area.X+area.Width-width)),
		Y:     max(area.Y, min(current.Y, area.Y+area.Height-height)),
		Width: width, Height: height,
	}
}

func (s *application) setFloatingExpanded(expanded bool) {
	s.floating.expanded = expanded
	if s.floatingWindow != nil {
		bounds := s.floatingWindow.Bounds()
		area := mygo.Screen.DisplayMatching(bounds).WorkArea
		s.floatingWindow.SetBounds(floatingPanelBounds(bounds, area, expanded))
		s.floatingWindow.Invalidate()
	}
}

func (s *application) floatingImageCount() int {
	if s.batch != nil {
		return len(s.batch.items)
	}
	if s.file != nil {
		return 1
	}
	return 0
}

func (s *application) floatingImage(index int) batchItem {
	if s.batch != nil {
		return s.batch.items[index]
	}
	if s.file == nil {
		return batchItem{}
	}
	item := batchItem{
		path: s.file.path, relPath: filepath.Base(s.file.path),
		format: s.file.format, width: s.file.width, height: s.file.height,
		originalSize: s.file.size, status: batchPending,
	}
	if s.result != nil {
		item.status, item.outputSize = batchDone, s.result.OutputSize
		item.savedPath, item.overwritten = s.lastSavedPath, s.overwritten
	}
	return item
}

func (s *application) floatingSelectedImage() (batchItem, bool) {
	if s.floating.selected == "" {
		return batchItem{}, false
	}
	if s.batch != nil && s.batch.floatingReady &&
		s.floating.cursor >= 0 && s.floating.cursor < len(s.batch.cachedFloating) {
		candidate := s.batch.items[s.batch.cachedFloating[s.floating.cursor]]
		if candidate.path == s.floating.selected {
			return candidate, true
		}
	}
	for index := range s.floatingImageCount() {
		item := s.floatingImage(index)
		if item.path == s.floating.selected {
			return item, true
		}
	}
	return batchItem{}, false
}

func (s *application) floatingIndices() []int {
	if s.batch != nil {
		return s.batch.floatingIndices(s.floating.filter, s.floating.sort)
	}
	indices := make([]int, 0, s.floatingImageCount())
	for index := range s.floatingImageCount() {
		if matchesBatchFilter(s.floatingImage(index), s.floating.filter, "") {
			indices = append(indices, index)
		}
	}
	return indices
}

func (s *application) prepareFloatingSelection(indices []int) {
	same := len(indices) == len(s.floating.paths)
	if same {
		for position, index := range indices {
			if s.floatingImage(index).path != s.floating.paths[position] {
				same = false
				break
			}
		}
	}
	if !same {
		paths := make([]string, len(indices))
		for position, index := range indices {
			paths[position] = s.floatingImage(index).path
		}
		s.floating.cursor = slices.Index(paths, s.floating.selected)
		if s.floating.cursor < 0 && len(paths) > 0 {
			s.floating.cursor = 0
		}
		s.floating.list.Choose(s.floating.cursor)
		s.floating.paths = paths
	}
	if s.floating.cursor >= 0 && s.floating.cursor < len(s.floating.paths) {
		s.floating.selected = s.floating.paths[s.floating.cursor]
	} else if len(s.floating.paths) == 0 {
		s.floating.selected = ""
	}
}

func (s *application) revealFloatingSelection() {
	if s.modalOpen() {
		return
	}
	item, ok := s.floatingSelectedImage()
	if !ok {
		return
	}
	path := item.path
	if output := batchItemOutputPath(item); output != "" {
		path = output
	}
	previousMessage := s.message
	s.revealFileInFolder(path)
	if s.failed && s.message != previousMessage {
		s.rememberDropFailure(s.message)
	}
	if s.window != nil {
		s.window.Invalidate()
	}
}

func (s *application) quickLookFloatingSelection() {
	if s.modalOpen() || !s.supportsQuickLook() {
		return
	}
	item, ok := s.floatingSelectedImage()
	if !ok {
		return
	}
	path := item.path
	if output := batchItemOutputPath(item); output != "" {
		path = output
	}
	previousMessage := s.message
	s.previewInQuickLook(path)
	if s.failed && s.message != previousMessage {
		s.rememberDropFailure(s.message)
	}
}

func (s *application) openFloatingSelection() {
	if s.modalOpen() {
		return
	}
	item, ok := s.floatingSelectedImage()
	if !ok || item.invalidInput {
		return
	}
	if s.batch != nil {
		for index, candidate := range s.batch.items {
			if candidate.path == item.path {
				s.openBatchPreview(index)
				break
			}
		}
	}
	s.showMainWindow()
}

func (s *application) floatingCompressAction() (string, bool) {
	if s.modalOpen() {
		return "开始压缩", false
	}
	if s.batch != nil {
		_, pending, done, _, _, _, canRun := s.batchStats()
		if !canRun {
			return "开始压缩", false
		}
		switch {
		case pending > 0 && done > 0:
			return "继续压缩", true
		case pending == 0 && done > 0:
			return "重新压缩", true
		default:
			return "开始压缩", true
		}
	}
	if s.file != nil {
		if s.result != nil {
			return "重新压缩", true
		}
		return "开始压缩", true
	}
	return "开始压缩", false
}

func (s *application) compressFromFloating() {
	if s.modalOpen() {
		return
	}
	if s.batch != nil {
		_, pending, done, _, _, _, canRun := s.batchStats()
		if !canRun {
			return
		}
		mode := batchRunPending
		if pending == 0 && done > 0 {
			mode = batchRunRestart
		}
		needsMainPrompt := (s.batch.outputMode == batchOutputOverwrite && s.batchRunWillOverwriteAny(mode, -1)) ||
			s.output.Mode == presetSaveAsk
		if needsMainPrompt {
			s.showMainWindow()
		}
		s.beginBatchRunMode(mode, -1)
		return
	}
	if s.file != nil {
		s.startCompression()
	}
}

func (s *application) canCopyFloatingSelection() bool {
	if s.modalOpen() {
		return false
	}
	item, ok := s.floatingSelectedImage()
	if !ok {
		return false
	}
	if s.batch == nil {
		return s.canCopyResult()
	}
	return s.canCopyBatchItem(item)
}

func (s *application) copyFloatingSelection() {
	if !s.canCopyFloatingSelection() {
		return
	}
	if s.batch == nil {
		s.copyResult()
		return
	}
	item, ok := s.floatingSelectedImage()
	if !ok {
		return
	}
	for index, candidate := range s.batch.items {
		if candidate.path == item.path {
			s.copyBatchItem(index)
			return
		}
	}
}
func (s *application) floatingBitmap(item batchItem) *ui.Bitmap {
	if s.batch == nil && s.file != nil && s.file.path == item.path {
		return s.file.preview
	}
	if entry := s.floatingThumbnail(item); entry != nil {
		return entry.bitmap
	}
	return nil
}

func (s *application) floatingView(c *ui.Context) {
	s.applyTheme(c)
	theme := c.Theme()
	theme.Radius = 8
	c.SetTheme(theme)
	s.prepareFloatingThumbnails()
	indices := s.floatingIndices()
	s.prepareFloatingSelection(indices)
	c.OnWindowShortcut(0, ui.KeyEscape, func() {
		if s.floating.expanded {
			s.setFloatingExpanded(false)
		} else {
			s.setFloatingOpen(false)
		}
	})
	if s.floating.expanded && !s.modalOpen() && s.supportsQuickLook() {
		c.OnWindowShortcut(0, ui.KeySpace, s.quickLookFloatingSelection)
	}
	options := transfer.DropOptions{
		Formats: []transfer.Format{
			transfer.FileList, transfer.URIList, transfer.HTML, transfer.Text,
			transfer.PNG, transfer.Format("image/jpeg"), transfer.Format("image/webp"),
		},
		Operations: transfer.Copy,
	}
	if compress.SupportsAVIF() {
		options.Formats = append(options.Formats, "image/avif")
	}
	background := windowRootBackground(c)
	root := ui.Column(c).Fill().Padding(8).Gap(4).Background(background)
	over := false
	if !s.draggingOut {
		_, over = ui.DataDragOver(root, options)
		if drop, ok := ui.DropData(root, options); ok {
			s.receiveDrop(drop.Data)
		}
	}
	if over {
		root.DrawOver(func(p *ui.Painter, bounds ui.Rect) {
			p.Stroke(ui.Rect{X: bounds.X + 2, Y: bounds.Y + 2, W: bounds.W - 4, H: bounds.H - 4},
				accentForeground(theme), 14, 1.5)
		})
	}
	root.Children(func() {
		if s.floating.expanded {
			s.floatingHeader(c)
			s.floatingGallery(c, indices)
			s.floatingFeedback(c)
			ui.Row(c).Height(28).Gap(6).AlignItems(ui.Center).Children(func() {
				compressLabel, canCompress := s.floatingCompressAction()
				primaryAction(c, "floating-compress", compressLabel, !canCompress).Height(28).Padding(0, 10).
					Tooltip("直接按当前预设压缩队列图片").
					OnClick(s.compressFromFloating)
				quietButton(c, "floating-show-main", "查看队列", 28, false).
					OnClick(s.showMainWindow)
				item, selected := s.floatingSelectedImage()
				quietButton(c, "floating-preview", "预览选中", 28, !selected || item.invalidInput || s.modalOpen()).
					Tooltip("在主窗口查看所选图片，也可双击图片或按 Enter").
					OnClick(s.openFloatingSelection)
				if s.canCopyFloatingSelection() {
					quietButton(c, "floating-copy", "复制", 28, false).
						Label("复制图片").Tooltip("复制所选图片的压缩结果到剪贴板").
						OnClick(s.copyFloatingSelection)
					dragChip := ui.Row(c.Key("floating-drag")).Height(28).Padding(0, 8).Gap(4).
						AlignItems(ui.Center).Radius(innerRadius(c)).
						Background(glassControlSurface(c)).Border(1, controlBorder(c)).
						Label("拖出图片").Tooltip("按住将所选压缩结果拖到 Finder 或其他应用").
						DragDataFrom(func() transfer.Data {
							sel, ok := s.floatingSelectedImage()
							if !ok {
								return transfer.Data{}
							}
							return s.floatingItemDragExportData(sel)
						}, s.dragExportOptions())
					if dragChip.Hovered() {
						dragChip.Background(glassControlHover(c)).Border(1, controlHoverBorder(c))
					}
					dragChip.Children(func() {
						ui.Icon(c, toolbarDragIcon).Size(13, 13).TextColor(theme.TextMuted).Shrink(0)
						ui.Text(c, "拖出").FontSize(12).TextColor(theme.Text).SingleLine()
					})
				}
				ui.Box(c).Grow(1)
				quietButton(c, "floating-downloads", "下载位置", 28, false).
					Tooltip("打开网页原图下载文件夹").
					OnClick(s.openDownloadedImagesDir)
			})
			return
		}
		ui.Row(c).Height(28).DragWindow().Children(func() {
			floatingIconButton(c, "floating-close", "收起", "close",
				func() { s.setFloatingOpen(false) })
			ui.Box(c).Grow(1)
			floatingIconButton(c, "floating-expand", "展开图片", "down",
				func() { s.setFloatingExpanded(true) })
		})
		s.floatingStack(c)
		label := fmt.Sprintf("%d 张图片", s.floatingImageCount())
		if s.floatingImageCount() == 0 {
			label = "添加图片"
		}
		if over {
			label = "松开加入队列"
		}
		ui.Row(c).FillWidth().Center().Children(func() {
			countBtn := ui.ButtonBase(c.Key("floating-count")).Height(28).Padding(0, 12).Gap(4).
				Radius(badgeRadius(28)).Background(glassControlSurface(c)).Border(1, controlBorder(c)).Label(label).
				Tooltip("展开图片 · 支持本地 " + strings.Join(supportedImageFormats(), " / ") + " 或网页图片").
				OnClick(func() { s.setFloatingExpanded(true) })
			if over {
				countBtn.Background(glassSelectionBackground(c)).Border(1, selectionBorder(c))
			} else if countBtn.Hovered() {
				countBtn.Background(glassControlHover(c)).Border(1, controlHoverBorder(c))
			}
			countBtn.Children(func() {
				ui.Text(c, label).FontSize(12).FontWeight(500).FontFeatures("tnum")
				floatingIcon(c, "right", 16, theme.TextMuted)
			})
		})
		s.floatingFeedback(c)
	})
	s.startNextBatchThumbnail()
}

func (s *application) floatingStack(c *ui.Context) {
	theme, emptyIconColor := c.Theme(), chromeMutedColor(c)
	var bitmaps []*ui.Bitmap
	for index := s.floatingImageCount() - 1; index >= 0 && len(bitmaps) < 3; index-- {
		bitmaps = append(bitmaps, s.floatingBitmap(s.floatingImage(index)))
	}
	if len(bitmaps) > 1 {
		slices.Reverse(bitmaps)
	}
	ui.ButtonBase(c.Key("floating-stack")).FillWidth().Grow(1).MinHeight(40).
		Label("图片缩略图堆叠，展开图片").Tooltip("拖入本地或网页图片，也可点击展开").
		OnClick(func() { s.setFloatingExpanded(true) }).
		Draw(func(p *ui.Painter, bounds ui.Rect) {
			centerX, centerY := bounds.X+bounds.W/2, bounds.Y+bounds.H/2
			if len(bitmaps) == 0 {
				drawFloatingIcon(p, ui.Rect{X: centerX - 18, Y: centerY - 18, W: 36, H: 36},
					"photo", emptyIconColor)
				return
			}
			for index, bitmap := range bitmaps {
				angle := []float32{-9, 7, 0}[3-len(bitmaps)+index]
				width, height := min(float32(92), bounds.W-24), min(float32(64), bounds.H-16)
				rect := ui.Rect{X: centerX - width/2, Y: centerY - height/2, W: width, H: height}
				if bitmap != nil {
					width, height := bitmap.Size()
					scale := min(rect.W/float32(width), rect.H/float32(height))
					rect.W, rect.H = float32(width)*scale, float32(height)*scale
					rect.X, rect.Y = centerX-rect.W/2, centerY-rect.H/2
				}
				p.Transform(ui.Rotation(angle), centerX, centerY, func() {
					p.Shadow(rect, 8, 0, 2, 6, 0, ui.Hex("#000000").Alpha(0.12))
					p.Fill(rect, panelSurface(c), 8)
					if bitmap != nil {
						p.Clip(rect, 8, func() { p.Image(bitmap, rect, ui.Contain) })
					} else {
						drawFloatingIcon(p, ui.Rect{X: centerX - 12, Y: centerY - 12, W: 24, H: 24},
							"photo", theme.TextMuted)
					}
					p.Stroke(rect, glassBorder(c), 8, 1)
				})
			}
		})
	if s.floatingImageCount() == 0 {
		ui.Text(c, "拖入本地或网页图片").FontSize(12).TextColor(chromeMetadataColor(c)).
			FillWidth().TextAlign(ui.Center)
	}
}

func (s *application) floatingHeader(c *ui.Context) {
	ui.Row(c).Height(40).Gap(8).AlignItems(ui.Center).DragWindow().Children(func() {
		floatingIconButton(c, "floating-collapse", "收起图片", "back",
			func() { s.setFloatingExpanded(false) })
		ui.Column(c).Grow(1).Width(0).Gap(4).Children(func() {
			count := fmt.Sprintf("%d 张图片", s.floatingImageCount())
			if s.floating.filter != batchFilterAll {
				count = fmt.Sprintf("%d / %d 张图片", len(s.floating.paths), s.floatingImageCount())
			}
			ui.Text(c, count).FontSize(15).FontWeight(500).FontFeatures("tnum").SingleLine()
			if item, ok := s.floatingSelectedImage(); ok {
				ui.Text(c, "已选 1 张 · "+formatSize(item.originalSize)).
					FontSize(12).FontFeatures("tnum").TextColor(chromeMetadataColor(c)).SingleLine().TruncateMiddle()
			}
		})
		filterLabels := []string{"全部图片", "待处理", "已完成", "失败"}
		filter := floatingIconButton(c, "floating-filter", "筛选图片", "filter", nil).
			Tooltip("当前筛选："+filterLabels[max(0, min(s.floating.filter, len(filterLabels)-1))]).
			PopoverMenuButton(func(menu *ui.Menu) {
				for index, label := range filterLabels {
					if menu.Item(label).Radio(s.floating.filter == index).Chosen() {
						s.floating.filter = index
					}
				}
			}, ui.PopoverMenuButtonOptions{At: ui.AnchorBottomEnd, Self: ui.AnchorTopEnd})
		if s.floating.filter != batchFilterAll {
			filter.Selected(true).Background(glassSelectionBackground(c)).Border(1, selectionBorder(c))
		}
		floatingIconButton(c, "floating-sort", "图片排序", "sort", nil).
			PopoverMenuButton(func(menu *ui.Menu) {
				for _, option := range batchSortOptions {
					if menu.Item(option.menuLabel).Radio(s.floating.sort == option.mode).Chosen() {
						s.floating.sort = option.mode
					}
				}
			}, ui.PopoverMenuButtonOptions{At: ui.AnchorBottomEnd, Self: ui.AnchorTopEnd})
		ui.Row(c).Gap(2).Padding(2).Radius(innerRadius(c)).Background(panelSubtleSurface(c)).Border(1, glassBorder(c)).Children(func() {
			for _, mode := range []struct {
				list bool
				name string
				icon string
			}{{false, "网格视图", "grid"}, {true, "列表视图", "list"}} {
				button := floatingIconButton(c, "floating-"+mode.icon, mode.name, mode.icon,
					func() { s.floating.listView = mode.list })
				button.Radius(6).Selected(s.floating.listView == mode.list)
				if s.floating.listView == mode.list {
					button.Background(segmentedThumbSurface(c)).Border(1, segmentedThumbBorder(c))
				} else if button.Hovered() {
					button.Background(glassControlHover(c)).Border(1, ui.Transparent)
				} else {
					button.Background(ui.Transparent).Border(1, ui.Transparent)
				}
			}
		})
	})
}

func (s *application) floatingGallery(c *ui.Context, indices []int) {
	if len(indices) == 0 {
		ui.Column(c).FillWidth().Grow(1).Center().Gap(8).Children(func() {
			floatingIcon(c, "photo", 40, chromeMutedColor(c))
			if s.floatingImageCount() == 0 {
				ui.Text(c, "拖入图片").FontSize(14).FontWeight(500)
				formats := "本地 " + strings.Join(supportedImageFormats(), " / ") + " 或网页图片"
				ui.Text(c, formats).FontSize(12).TextColor(chromeMetadataColor(c))
			} else {
				ui.Text(c, "没有符合筛选条件的图片").FontSize(14)
				quietButton(c, "floating-reset-filter", "显示全部", 32, false).
					OnClick(func() { s.floating.filter = batchFilterAll })
			}
		})
		return
	}
	key := func(position int) any {
		if position == len(indices) {
			return "floating-reveal"
		}
		return s.floatingImage(indices[position]).path
	}
	label := func(position int) string {
		if position == len(indices) {
			return floatingRevealLabel()
		}
		return s.floatingImage(indices[position]).relPath
	}
	var gallery ui.Element
	if s.floating.listView {
		state := &s.floating.list
		state.Key, state.Label, state.Selected = key, label, &s.floating.cursor
		gallery = ui.List(c.Key("floating-gallery-list"), state, len(indices)+1).
			Label("悬浮窗图片列表").FillWidth().Grow(1).Gap(4)
		gallery.Rows(func(row ui.ListRow) {
			s.floatingGalleryItem(row.Context, indices, row.Index, true)
		})
	} else {
		state := &s.floating.grid
		state.Key, state.Label, state.Selected = key, label, &s.floating.cursor
		gallery = ui.GridView(c.Key("floating-gallery-grid"), state, len(indices)+1, 136, 128,
			func(position int) { s.floatingGalleryItem(c, indices, position, false) }).
			Label("悬浮窗图片网格").FillWidth().Grow(1)
	}
	if s.floating.cursor >= 0 && s.floating.cursor < len(indices) {
		s.floating.selected = s.floatingImage(indices[s.floating.cursor]).path
	}
	if gallery.Submitted() {
		if s.floating.cursor == len(indices) {
			s.revealFloatingSelection()
		} else if s.floating.cursor >= 0 && s.floating.cursor < len(indices) {
			s.openFloatingSelection()
		}
	}
}

func (s *application) floatingGalleryItem(c *ui.Context, indices []int, position int, list bool) {
	theme := c.Theme()
	if position == len(indices) {
		_, selected := s.floatingSelectedImage()
		foreground, iconColor := theme.Text, theme.TextMuted
		button := ui.ButtonBase(c.Key("floating-reveal")).Fill().Column().Center().Gap(8).Radius(innerRadius(c)).
			Background(panelSubtleSurface(c)).Border(1, glassBorder(c)).
			Label(floatingRevealLabel()).Disabled(!selected || s.modalOpen()).
			Tooltip("在文件夹中定位选中图片，有压缩输出时优先显示输出文件").
			OnClick(s.revealFloatingSelection)
		if button.Hovered() && selected && !s.modalOpen() {
			button.Background(glassControlHover(c))
		}
		if s.floating.cursor == position {
			button.Background(glassSelectionBackground(c)).Border(1, selectionBorder(c))
		}
		if list {
			button.Height(56).Row().Gap(8)
		}
		button.Children(func() {
			floatingIcon(c, "reveal", 16, iconColor)
			ui.Text(c, floatingRevealLabel()).FontSize(13).TextColor(foreground)
		})
		return
	}
	item := s.floatingImage(indices[position])
	bitmap := s.floatingBitmap(item)
	selected := s.floating.cursor == position
	background, border := glassCardSurface(c), glassBorder(c)
	foreground, secondary := theme.Text, metadataColor(c)
	if selected {
		background, border = glassSelectionBackground(c), selectionBorder(c)
	}
	tile := ui.Column(c).Fill().Padding(8).Gap(4).Radius(innerRadius(c)).Background(background).
		Border(1, border).Tooltip(item.path)
	if tile.Hovered() && !selected {
		tile.Background(glassControlHover(c)).Border(1, controlHoverBorder(c))
	}
	if list {
		tile.Row().Height(56).Gap(8).AlignItems(ui.Center)
	}
	tile.Children(func() {
		image := ui.Column(c).Grow(1).FillWidth().Center()
		if list {
			image.Grow(0).Size(56, 40).Shrink(0)
		} else {
			image.Height(48).Grow(0).Shrink(0)
		}
		image.Children(func() {
			if bitmap != nil {
				maxWidth, maxHeight := float32(96), float32(48)
				if list {
					maxWidth, maxHeight = 56, 40
				}
				width, height := bitmap.Size()
				scale := min(maxWidth/float32(width), maxHeight/float32(height))
				ui.Image(c, bitmap).Size(float32(width)*scale, float32(height)*scale).
					Fit(ui.Contain).Radius(4).Clip().Role(ui.RoleImage).Label(item.relPath + " 缩略图")
			} else {
				floatingIcon(c, "photo", 32, secondary)
				if item.invalidInput {
					ui.Text(c, "无预览").FontSize(12).TextColor(secondary)
				}
			}
		})
		details := ui.Column(c).FillWidth().Gap(4)
		if list {
			details.Width(0).Grow(1)
		}
		details.Children(func() {
			filename := ui.Text(c, filepath.Base(item.relPath)).FontSize(13).FontWeight(500).
				TextColor(foreground).SingleLine().TruncateMiddle().FillWidth()
			detail := formatSize(item.originalSize)
			if item.status == batchDone && item.outputSize > 0 {
				detail += " → " + formatSize(item.outputSize)
			} else if item.width > 0 && item.height > 0 {
				detail += fmt.Sprintf(" · %d×%d", item.width, item.height)
			}
			if item.status == batchFailed {
				detail = "失败 · " + detail
				secondary = theme.Danger
			}
			detailTooltip := detail
			if item.errMsg != "" {
				detailTooltip += "\n" + item.errMsg
			}
			displayDetail := detail
			if !list {
				// Keep each measured size and its unit together in narrow cells.
				displayDetail = formatSize(item.originalSize)
				if item.status == batchDone && item.outputSize > 0 {
					displayDetail += "\n→ " + formatSize(item.outputSize)
				} else if item.width > 0 && item.height > 0 {
					displayDetail += fmt.Sprintf("\n%d×%d", item.width, item.height)
				}
				if item.status == batchFailed {
					displayDetail = "失败 · " + displayDetail
				}
			}
			metadata := ui.Text(c, displayDetail).FontSize(12).FontFeatures("tnum").TextColor(secondary).
				FillWidth().Tooltip(detailTooltip)
			if list {
				metadata.SingleLine().TruncateMiddle()
			} else {
				filename.TextAlign(ui.Center)
				metadata.MaxLines(2).TextAlign(ui.Center)
			}
		})
	})
}

func floatingRevealLabel() string {
	if runtime.GOOS == "darwin" {
		return "在 Finder 中显示"
	}
	return "在文件夹中显示"
}

func (s *application) floatingFeedback(c *ui.Context) {
	message, failed := s.floatingStatus()
	if !failed && !s.busy && len(s.dropQueue) == 0 {
		return
	}
	theme := c.Theme()
	color, role := chromeMutedColor(c), ui.RoleStatus
	if failed {
		color, role = theme.Danger, ui.RoleAlert
	}
	ui.Row(c).Height(32).Gap(8).AlignItems(ui.Center).Children(func() {
		tooltip := message
		if len(s.dropFailures) > 0 {
			tooltip = strings.Join(s.dropFailures, "\n\n")
		}
		ui.Text(c, message).FontSize(12).TextColor(color).Role(role).
			Width(0).Grow(1).MaxLines(2).Tooltip(tooltip)
		if len(s.dropFailures) > 0 {
			floatingIconButton(c, "floating-clear-errors", "清除提示", "close", func() {
				s.dropFailures = nil
				s.dropMessage, s.dropFailed = "", false
			})
		}
		if s.dropImporting {
			quietButton(c, "floating-cancel", "取消", 28, s.canceling).
				OnClick(s.cancelWork)
		}
	})
}

func floatingIconButton(c *ui.Context, key, label, icon string, action func()) ui.Element {
	theme := c.Theme()
	radius := float32(8)
	if icon == "close" || icon == "back" || icon == "down" {
		radius = 14
	}
	button := ui.ButtonBase(c.Key(key)).Size(28, 28).Shrink(0).Radius(radius).
		Background(glassControlSurface(c)).Border(1, controlBorder(c)).Label(label).Tooltip(label)
	iconColor := theme.TextMuted
	if button.Pressed() {
		iconColor = theme.Text
		button.Background(glassControlPressed(c))
	} else if button.Hovered() {
		iconColor = theme.Text
		button.Background(glassControlHover(c))
	}
	if action != nil {
		button.OnClick(action)
	}
	button.Children(func() { floatingIcon(c, icon, 16, iconColor) })
	return button
}

func floatingIcon(c *ui.Context, icon string, size float32, color ui.Color) {
	ui.Box(c).Size(size, size).Shrink(0).Draw(func(p *ui.Painter, bounds ui.Rect) {
		drawFloatingIcon(p, bounds, icon, color)
	})
}

func drawFloatingIcon(p *ui.Painter, bounds ui.Rect, icon string, color ui.Color) {
	stroke := float32(1.5) * 24 / bounds.W
	p.Transform(ui.Scaling(bounds.W/24, bounds.H/24), bounds.X, bounds.Y, func() {
		line := func(points ...[2]float32) {
			for index := range points {
				points[index][0] += bounds.X
				points[index][1] += bounds.Y
			}
			p.StrokePolyline(points, stroke, color)
		}
		rect := func(x, y, width, height, radius float32) {
			p.Stroke(ui.Rect{X: bounds.X + x, Y: bounds.Y + y, W: width, H: height},
				color, radius, stroke)
		}
		switch icon {
		case "close":
			line([2]float32{6, 6}, [2]float32{18, 18})
			line([2]float32{18, 6}, [2]float32{6, 18})
		case "down":
			line([2]float32{5, 9}, [2]float32{12, 16}, [2]float32{19, 9})
		case "back":
			line([2]float32{15, 5}, [2]float32{8, 12}, [2]float32{15, 19})
		case "right":
			line([2]float32{9, 5}, [2]float32{16, 12}, [2]float32{9, 19})
		case "grid":
			for _, point := range [][2]float32{{4, 4}, {14, 4}, {4, 14}, {14, 14}} {
				rect(point[0], point[1], 6, 6, 1.5)
			}
		case "list":
			for _, vertical := range []float32{6, 12, 18} {
				line([2]float32{9, vertical}, [2]float32{21, vertical})
				rect(3, vertical-1, 2, 2, 1)
			}
		case "filter":
			for index, horizontal := range []float32{8, 16, 10} {
				vertical := float32(6 + index*6)
				line([2]float32{3, vertical}, [2]float32{horizontal - 2, vertical})
				line([2]float32{horizontal + 2, vertical}, [2]float32{21, vertical})
				rect(horizontal-2, vertical-2, 4, 4, 2)
			}
		case "sort":
			line([2]float32{8, 20}, [2]float32{8, 4})
			line([2]float32{3, 9}, [2]float32{8, 4}, [2]float32{13, 9})
			line([2]float32{16, 4}, [2]float32{16, 20})
			line([2]float32{11, 15}, [2]float32{16, 20}, [2]float32{21, 15})
		case "reveal":
			rect(2, 2, 20, 20, 10)
			line([2]float32{7, 16}, [2]float32{16, 7})
			line([2]float32{9, 7}, [2]float32{16, 7}, [2]float32{16, 14})
		default:
			rect(2, 4, 20, 16, 3)
			line([2]float32{4, 17}, [2]float32{9, 11}, [2]float32{13, 15}, [2]float32{16, 12}, [2]float32{20, 17})
			rect(15, 7, 2, 2, 1)
		}
	})
}
