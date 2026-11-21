package main

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"zpic/internal/compress"
)

const maxBatchFiles = 2000

const (
	batchOutputFolder = iota
	batchOutputOverwrite
)

const (
	batchFilterAll = iota
	batchFilterPending
	batchFilterDone
	batchFilterFailed
)

const (
	batchSortDefault = iota
	batchSortName
	batchSortOriginalSize
	batchSortSavings
)

type batchRunMode int

const (
	batchRunPending batchRunMode = iota
	batchRunRestart
	batchRunRetryFailed
	batchRunSingle
)

type batchStatus int

const (
	batchPending batchStatus = iota
	batchProcessing
	batchDone
	batchFailed
)

type batchItem struct {
	path             string
	absPath          string
	relPath          string
	sourceDir        string
	sourceBase       string
	prefixedDir      bool
	sourceFormat     string
	sourceWidth      int
	sourceHeight     int
	format           string
	width            int
	height           int
	originalSize     int64
	outputSize       int64
	status           batchStatus
	unchanged        bool
	targetSizeMissed bool
	targetResized    bool
	targetSizeBytes  int64
	overwritten      bool
	invalidInput     bool
	savedPath        string
	errMsg           string
}

func (item batchItem) effectiveSourceFormat() string {
	if item.sourceFormat != "" {
		return item.sourceFormat
	}
	return item.format
}

type batchState struct {
	items           []batchItem
	outputMode      int
	outputDir       string
	filter          int
	sort            int
	query           string
	list            ui.ListState
	selected        int
	listSelected    int
	listSelectedKey string
	previewOpen     bool
	previewIndex    int
	done            int
	total           int
	running         bool
	currentName     string

	cacheReady     bool
	cachedStates   []batchItemSnapshot
	cachedNameKeys []batchNameKey
	cachedStats    batchStatsSummary
	visibleReady   bool
	visibleFilter  int
	visibleSort    int
	visibleQuery   string
	cachedVisible  []int
	floatingReady  bool
	floatingFilter int
	floatingSort   int
	cachedFloating []int
}

type batchNameKey struct {
	raw   string
	slash string
	lower string
}

type batchItemSnapshot struct {
	relPath          string
	originalSize     int64
	outputSize       int64
	status           batchStatus
	unchanged        bool
	targetSizeMissed bool
	targetResized    bool
	invalidInput     bool
}

type batchStatsSummary struct {
	total            int
	pending          int
	done             int
	failed           int
	retryableFailed  int
	invalidFailed    int
	unchangedDone    int
	targetSizeMissed int
	targetResized    int
	origTotal        int64
	outTotal         int64
	doneOrigTotal    int64
	canRun           bool
}

type batchScanResult struct {
	items      []batchItem
	duplicates int
	truncated  bool
}

type batchTask struct {
	index        int
	path         string
	relPath      string
	sourceFormat string
	outputFormat compress.OutputFormat
	lossless     bool
	output       presetSaveOptions
}

type batchItemUpdate struct {
	index            int
	source           compress.ImageInfo
	status           batchStatus
	format           string
	width            int
	height           int
	originalSize     int64
	outputSize       int64
	unchanged        bool
	targetSizeMissed bool
	targetResized    bool
	targetSizeBytes  int64
	overwritten      bool
	savedPath        string
	errMsg           string
	advanceDone      bool
}

var errBatchLimit = errors.New("已达到单次批处理数量上限")

func newBatchState(items []batchItem) *batchState {
	selected := -1
	if len(items) > 0 {
		selected = 0
	}
	return &batchState{
		items:        items,
		selected:     selected,
		listSelected: -1,
		previewIndex: -1,
	}
}

func (s *application) chooseFolder() {
	if s.modalOpen() {
		return
	}
	s.busy, s.phase = true, "正在选择文件夹"
	appendMode := s.batch != nil
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:      s.window,
			Title:       "选择包含图片的文件夹",
			ButtonLabel: "选择文件夹",
			Directory:   true,
			Multiple:    true,
		})
		s.update(func() {
			s.busy, s.phase = false, ""
			if err != nil {
				s.setMessage("无法打开文件夹选择器："+err.Error(), true)
			} else if len(paths) != 0 {
				s.loadBatch(paths, appendMode)
			}
		})
	}()
}

func (s *application) chooseOutputDir() {
	if s.modalOpen() || s.batch == nil {
		return
	}
	s.busy, s.phase = true, "正在选择输出文件夹"
	defaultPath := s.batch.outputDir
	if defaultPath == "" {
		defaultPath = usableBatchOutputDir(s.batchOutputDir)
	}
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:            s.window,
			Title:             "选择压缩结果输出文件夹",
			ButtonLabel:       "选择此文件夹",
			DefaultPath:       defaultPath,
			Directory:         true,
			CreateDirectories: true,
		})
		s.update(func() {
			s.busy, s.phase = false, ""
			if err != nil {
				s.setMessage("无法打开文件夹选择器："+err.Error(), true)
			} else if len(paths) != 0 && s.batch != nil {
				prev := s.batch.outputDir
				s.batch.outputDir = paths[0]
				s.rememberBatchOutputDir(paths[0])
				if prev != paths[0] && s.resetBatchRunState() {
					s.setMessage("输出位置已更改，请重新压缩。", false)
				} else {
					s.setMessage("输出位置："+paths[0], false)
				}
			}
		})
	}()
}

func (s *application) clearBatch() {
	if s.modalOpen() || s.batch == nil {
		return
	}
	s.closeBatchPreview()
	s.clearPendingBatchConfirmation()
	s.batch = nil
	s.prepareThumbnailCache()
	s.setMessage("", false)
}

func batchItemOutputPath(item batchItem) string {
	if item.status != batchDone {
		return ""
	}
	if item.savedPath != "" {
		return item.savedPath
	}
	if item.overwritten {
		return item.path
	}
	return ""
}

func (s *application) removeSelectedBatchItem() {
	if s.batch == nil {
		return
	}
	s.removeBatchItem(s.batch.selected)
}

func (s *application) removeBatchItem(index int) {
	if s.batch == nil || index < 0 || index >= len(s.batch.items) {
		return
	}
	if removed := s.deleteBatchItems(func(i int, _ batchItem) bool { return i == index }); removed > 0 && s.batch == nil {
		s.setMessage("", false)
	}
}

func (s *application) removeInvalidBatchItems() {
	removed := s.deleteBatchItems(func(_ int, item batchItem) bool {
		return item.status == batchFailed && item.invalidInput
	})
	s.finishBulkFailureRemoval(removed, "无效")
}

func (s *application) removeFailedBatchItems() {
	removed := s.deleteBatchItems(func(_ int, item batchItem) bool {
		return item.status == batchFailed
	})
	s.finishBulkFailureRemoval(removed, "失败")
}

func (s *application) finishBulkFailureRemoval(removed int, kind string) {
	if removed == 0 {
		return
	}
	if s.batch == nil {
		s.setMessage(fmt.Sprintf("已移除 %d 张%s图片，队列已清空。", removed, kind), false)
		return
	}
	retryable, invalid := s.batchFailureCounts()
	if s.batch.filter == batchFilterFailed && retryable+invalid == 0 {
		s.batch.filter = batchFilterAll
		if visible := s.filteredBatchIndices(); len(visible) > 0 && s.batch.selected < 0 {
			s.batch.selected = visible[0]
		}
	}
	s.setMessage(fmt.Sprintf("已移除 %d 张%s图片。", removed, kind), false)
}

func (s *application) deleteBatchItems(shouldRemove func(index int, item batchItem) bool) int {
	if s.modalOpen() || s.batch == nil {
		return 0
	}
	visPos := slices.Index(s.filteredBatchIndices(), s.batch.selected)
	oldSelected := s.batch.selected
	oldPreview := -1
	if s.batch.previewOpen {
		oldPreview = s.batch.previewIndex
	}

	kept := make([]batchItem, 0, len(s.batch.items))
	newIndex := make([]int, len(s.batch.items))
	removed := 0
	for i, item := range s.batch.items {
		if shouldRemove(i, item) {
			s.invalidateBatchThumbnail(item.path)
			newIndex[i] = -1
			removed++
			continue
		}
		newIndex[i] = len(kept)
		kept = append(kept, item)
	}
	if removed == 0 {
		return 0
	}
	if len(kept) == 0 {
		s.closeBatchPreview()
		s.clearPendingBatchConfirmation()
		s.batch = nil
		s.prepareThumbnailCache()
		return removed
	}
	s.batch.items = kept
	if oldPreview >= 0 && oldPreview < len(newIndex) {
		if mapped := newIndex[oldPreview]; mapped >= 0 {
			s.batch.previewIndex = mapped
		} else {
			s.closeBatchPreview()
		}
	}
	s.batch.done, s.batch.total, s.batch.currentName = 0, 0, ""
	if oldSelected >= 0 && oldSelected < len(newIndex) {
		if mapped := newIndex[oldSelected]; mapped >= 0 && slices.Index(s.filteredBatchIndices(), mapped) >= 0 {
			s.batch.selected = mapped
			return removed
		}
	}
	visible := s.filteredBatchIndices()
	if visPos >= 0 && len(visible) > 0 {
		s.batch.selected = visible[min(visPos, len(visible)-1)]
	} else {
		s.batch.selected = -1
	}
	return removed
}

func (s *application) invalidateBatchResults() {
	if s.resetBatchRunState() {
		s.setMessage("参数已更新，请重新压缩。", false)
	}
}

func (s *application) resetBatchRunState() bool {
	if s.batch == nil {
		return false
	}
	s.closeBatchPreview()
	hadProcessed := false
	for i := range s.batch.items {
		if resetBatchItemState(&s.batch.items[i]) {
			hadProcessed = true
		}
	}
	s.batch.done, s.batch.total, s.batch.currentName = 0, 0, ""
	if hadProcessed && (s.batch.filter == batchFilterDone || s.batch.filter == batchFilterFailed) &&
		len(s.filteredBatchIndices()) == 0 {
		s.batch.filter = batchFilterAll
	}
	return hadProcessed
}

func resetBatchItemState(item *batchItem) bool {
	if item.invalidInput {
		return false
	}
	hadProcessed := item.status != batchPending
	if item.sourceFormat != "" {
		item.format = item.sourceFormat
	}
	if item.sourceWidth > 0 && item.sourceHeight > 0 {
		item.width, item.height = item.sourceWidth, item.sourceHeight
	}
	if item.overwritten {
		item.originalSize = item.outputSize
	}
	item.status = batchPending
	item.outputSize = 0
	item.unchanged = false
	item.targetSizeMissed, item.targetSizeBytes = false, 0
	item.targetResized = false
	item.overwritten = false
	item.savedPath = ""
	item.errMsg = ""
	return hadProcessed
}

func (s *application) startBatchCompression() {
	s.beginBatchRunMode(batchRunPending, -1)
}

func (s *application) restartBatchCompression() {
	s.beginBatchRunMode(batchRunRestart, -1)
}

func (s *application) retryFailedBatchItems() {
	s.beginBatchRunMode(batchRunRetryFailed, -1)
}

func (s *application) compressBatchItem(index int) {
	s.beginBatchRunMode(batchRunSingle, index)
}

func (s *application) clearPendingBatchConfirmation() {
	s.pendingBatchRestart = false
	s.pendingBatchRun = batchRunPending
	s.pendingBatchIndex = -1
}

func (s *application) effectivePendingBatchRun() (batchRunMode, int) {
	mode := s.pendingBatchRun
	if s.pendingBatchRestart && mode == batchRunPending {
		mode = batchRunRestart
	}
	return mode, s.pendingBatchIndex
}

func itemMatchesBatchRunMode(index int, item batchItem, mode batchRunMode, singleIndex int) bool {
	if item.invalidInput {
		return false
	}
	switch mode {
	case batchRunRestart:
		return true
	case batchRunRetryFailed:
		return item.status == batchFailed
	case batchRunSingle:
		return index == singleIndex && (item.status == batchPending || item.status == batchFailed)
	default:
		return item.status == batchPending
	}
}

func (s *application) batchRunTargetCount(mode batchRunMode, singleIndex int) int {
	if s.batch == nil {
		return 0
	}
	count := 0
	for i, item := range s.batch.items {
		if itemMatchesBatchRunMode(i, item, mode, singleIndex) {
			count++
		}
	}
	return count
}

func (s *application) beginBatchRun(restart bool) {
	mode := batchRunPending
	if restart {
		mode = batchRunRestart
	}
	s.beginBatchRunMode(mode, -1)
}

func (s *application) beginBatchRunMode(mode batchRunMode, singleIndex int) {
	if s.modalOpen() || s.batch == nil {
		return
	}
	_, pending, _, _, _, _, canRun := s.batchStats()
	if !canRun {
		return
	}
	if mode == batchRunPending && pending == 0 {
		mode = batchRunRestart
	}
	if s.batchRunTargetCount(mode, singleIndex) == 0 {
		return
	}
	if err := s.output.validate(); err != nil {
		s.reportError(err, "无法保存批量结果")
		return
	}
	if s.output.Mode == presetSaveFixed && s.output.Directory == "" {
		s.setMessage("请选择固定输出目录", true)
		return
	}
	if s.batch.outputMode == batchOutputOverwrite && s.batchRunWillOverwriteAny(mode, singleIndex) {
		s.pendingBatchRun = mode
		s.pendingBatchIndex = singleIndex
		s.pendingBatchRestart = mode == batchRunRestart
		s.confirmBatchOverwrite = true
		return
	}
	if s.output.Mode == presetSaveAsk {
		s.promptOutputDirAndStartMode(mode, singleIndex)
		return
	}
	s.launchBatchWorkerMode(mode, singleIndex)
}

func (s *application) promptOutputDirAndStartMode(mode batchRunMode, singleIndex int) {
	s.busy, s.phase = true, "请选择输出文件夹"
	defaultPath := s.batch.outputDir
	if defaultPath == "" {
		defaultPath = usableBatchOutputDir(s.batchOutputDir)
	}
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:            s.window,
			Title:             "选择压缩结果输出文件夹",
			ButtonLabel:       "选择此文件夹",
			DefaultPath:       defaultPath,
			Directory:         true,
			CreateDirectories: true,
		})
		s.update(func() {
			s.busy, s.phase = false, ""
			if err != nil {
				s.setMessage("无法打开文件夹选择器："+err.Error(), true)
				return
			}
			if len(paths) == 0 || s.batch == nil {
				return
			}
			s.startBatchWithPickedOutputDir(paths[0], mode, singleIndex)
		})
	}()
}

func (s *application) startBatchWithPickedOutputDir(dir string, mode batchRunMode, singleIndex int) {
	if dir == "" || s.batch == nil {
		return
	}
	prevDir := s.batch.outputDir
	s.batch.outputDir = dir
	s.rememberBatchOutputDir(dir)
	if prevDir != "" && prevDir != dir {
		s.resetBatchRunState()
		if mode != batchRunSingle {
			mode = batchRunPending
		}
	}
	tasks, usedPaths := s.prepareBatchTasks(mode, singleIndex)
	s.launchPreparedBatchWorker(tasks, usedPaths)
}

func (s *application) executeConfirmedBatchOverwrite() {
	if s.busy || s.settingsOpen || s.confirmOverwrite || s.batch == nil {
		return
	}
	mode, index := s.effectivePendingBatchRun()
	s.clearPendingBatchConfirmation()
	s.confirmBatchOverwrite = false
	s.launchBatchWorkerMode(mode, index)
}

func (s *application) launchBatchWorker(restart bool) {
	mode := batchRunPending
	if restart {
		mode = batchRunRestart
	}
	s.launchBatchWorkerMode(mode, -1)
}

func (s *application) launchBatchWorkerMode(mode batchRunMode, singleIndex int) {
	if s.busy || s.batch == nil {
		return
	}
	tasks, usedPaths := s.prepareBatchTasks(mode, singleIndex)
	s.launchPreparedBatchWorker(tasks, usedPaths)
}

func (s *application) launchPreparedBatchWorker(tasks []batchTask, usedPaths map[string]bool) {
	if len(tasks) == 0 {
		return
	}
	options := s.compressionOptions()
	outputMode := s.batch.outputMode
	outputDir := s.batch.outputDir
	backup := s.overwriteBackupFunc()
	ctx, cancel := context.WithCancel(context.Background())
	s.batch.total = len(tasks)
	s.batch.running = true
	s.batch.currentName = ""
	s.busy, s.phase, s.cancel, s.canceling = true, fmt.Sprintf("正在压缩 0 / %d", len(tasks)), cancel, false
	s.setMessage("", false)

	// Register shutdown wait on the main thread so App.Run exit cannot race with Add.
	s.saves.Add(1)
	go func() {
		defer s.saves.Done()
		s.runBatchTasksWithBackup(ctx, tasks, options, outputMode, outputDir, usedPaths, backup, s.update)
		s.update(func() {
			defer cancel()
			s.completeBatchRun(ctx)
		})
	}()
}

func (s *application) collectBatchTasks() ([]batchTask, map[string]bool) {
	return s.collectBatchTasksWhere(func(_ int, item batchItem) bool {
		return item.status == batchPending
	})
}

func (s *application) prepareBatchTasks(mode batchRunMode, singleIndex int) ([]batchTask, map[string]bool) {
	if mode == batchRunRestart {
		s.resetBatchRunState()
		return s.collectBatchTasks()
	}
	s.closeBatchPreview()
	if mode == batchRunPending {
		return s.collectBatchTasks()
	}
	return s.collectBatchTasksWhere(func(i int, item batchItem) bool {
		if !itemMatchesBatchRunMode(i, item, mode, singleIndex) {
			return false
		}
		resetBatchItemState(&s.batch.items[i])
		return true
	})
}

func (s *application) batchRunWillOverwriteAny(mode batchRunMode, singleIndex int) bool {
	if s.batch == nil || !s.output.replacesOriginal() {
		return false
	}
	for i, item := range s.batch.items {
		if itemMatchesBatchRunMode(i, item, mode, singleIndex) &&
			s.output.replacesFile(item.effectiveSourceFormat(), s.outputFormat) {
			return true
		}
	}
	return false
}

func (s *application) collectBatchTasksWhere(match func(i int, item batchItem) bool) ([]batchTask, map[string]bool) {
	var tasks []batchTask
	usedPaths := make(map[string]bool)
	for i, item := range s.batch.items {
		if item.savedPath != "" {
			usedPaths[item.savedPath] = true
		}
		if match(i, item) {
			tasks = append(tasks, batchTask{
				index:        i,
				path:         item.path,
				relPath:      item.relPath,
				sourceFormat: item.effectiveSourceFormat(),
				outputFormat: s.outputFormat,
				lossless:     s.lossless,
				output:       s.output,
			})
		}
	}
	return tasks, usedPaths
}

func (s *application) applyBatchItemUpdate(update batchItemUpdate) {
	if s.batch == nil || update.index < 0 || update.index >= len(s.batch.items) {
		return
	}
	item := &s.batch.items[update.index]
	thumbnail := s.thumbnails.entries[item.path]
	if update.overwritten || (update.status == batchDone && thumbnail != nil && thumbnail.failed) {
		s.invalidateBatchThumbnail(item.path)
	}
	item.status = update.status
	if update.source.Width > 0 && update.source.Height > 0 {
		item.sourceWidth, item.sourceHeight = update.source.Width, update.source.Height
	}
	if update.format != "" {
		item.format = update.format
	}
	if update.width > 0 && update.height > 0 {
		item.width, item.height = update.width, update.height
		if update.overwritten {
			item.sourceWidth, item.sourceHeight = update.width, update.height
		}
	}
	if update.originalSize > 0 {
		item.originalSize = update.originalSize
	}
	item.outputSize = update.outputSize
	item.unchanged = update.unchanged
	item.targetSizeMissed, item.targetSizeBytes = update.targetSizeMissed, update.targetSizeBytes
	item.targetResized = update.targetResized
	item.overwritten = update.overwritten
	item.savedPath = update.savedPath
	item.errMsg = update.errMsg
	if update.advanceDone {
		s.batch.done++
		if s.busy && !s.canceling && s.batch.done < s.batch.total {
			if s.batch.currentName != "" {
				s.phase = fmt.Sprintf("正在压缩 %d / %d · %s", s.batch.done+1, s.batch.total, s.batch.currentName)
			} else {
				s.phase = fmt.Sprintf("正在压缩 %d / %d", s.batch.done+1, s.batch.total)
			}
		}
	}
}

func (s *application) completeBatchRun(ctx context.Context) {
	s.busy, s.phase, s.cancel, s.canceling = false, "", nil, false
	if s.batch == nil {
		return
	}
	s.batch.running = false
	s.batch.currentName = ""
	for i := range s.batch.items {
		if s.batch.items[i].status == batchProcessing {
			s.batch.items[i].status = batchPending
		}
	}
	_, pending, done, failed, _, _, _ := s.batchStats()
	if ctx.Err() != nil {
		s.setMessage(fmt.Sprintf("已取消，已完成 %d 张，剩余 %d 张。", done, pending), false)
		return
	}
	if pending > 0 {
		if failed > 0 {
			s.setMessage(fmt.Sprintf("当前已完成 %d 张，失败 %d 张，剩余 %d 张待压缩。", done, failed, pending), true)
			return
		}
		s.setMessage(fmt.Sprintf("当前已完成 %d 张，剩余 %d 张待压缩。", done, pending), false)
		return
	}
	if failed > 0 && done == 0 {
		s.setMessage(fmt.Sprintf("批处理完成，%d 张图片处理失败。", failed), true)
		return
	}
	if failed > 0 {
		s.setMessage(fmt.Sprintf("批处理完成：成功 %d 张，失败 %d 张。", done, failed), true)
		return
	}
	if s.batch.outputMode == batchOutputOverwrite {
		s.setMessage(fmt.Sprintf("批处理完成，已处理 %d 张图片。", done), false)
		return
	}
	s.setMessage(fmt.Sprintf("批处理完成，已保存 %d 张到目标文件夹。", done), false)
}
