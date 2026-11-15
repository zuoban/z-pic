package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"zpic/internal/compress"
)

const maxBatchFiles = 2000

const (
	batchOutputFolder = iota
	batchOutputOverwrite
)

type batchStatus int

const (
	batchPending batchStatus = iota
	batchProcessing
	batchDone
	batchFailed
)

type batchItem struct {
	path         string
	absPath      string
	relPath      string
	format       string
	width        int
	height       int
	originalSize int64
	outputSize   int64
	status       batchStatus
	unchanged    bool
	overwritten  bool
	invalidInput bool
	savedPath    string
	errMsg       string
}

type batchState struct {
	items        []batchItem
	outputMode   int
	outputDir    string
	list         ui.ListState
	selected     int
	previewOpen  bool
	previewIndex int
	done         int
	total        int
	currentName  string
}

type batchScanResult struct {
	items      []batchItem
	duplicates int
	truncated  bool
}

type batchTask struct {
	index   int
	path    string
	relPath string
}

type batchItemUpdate struct {
	index        int
	status       batchStatus
	format       string
	width        int
	height       int
	originalSize int64
	outputSize   int64
	unchanged    bool
	overwritten  bool
	savedPath    string
	errMsg       string
	advanceDone  bool
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
				if prev != paths[0] && s.resetBatchRunState() {
					s.setMessage("输出位置已更改，请重新压缩。", false)
				} else {
					s.setMessage("输出位置："+paths[0], false)
				}
			}
		})
	}()
}

func (s *application) loadStartupPaths(paths []string) {
	s.openPaths(paths)
}

func (s *application) loadBatch(paths []string, appendMode bool) {
	if s.modalOpen() || len(paths) == 0 {
		return
	}
	var existing map[string]bool
	existingCount := 0
	if appendMode && s.batch != nil {
		existing = make(map[string]bool, len(s.batch.items))
		for _, item := range s.batch.items {
			existing[item.absPath] = true
		}
		existingCount = len(s.batch.items)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.busy, s.phase, s.cancel = true, "正在扫描图片文件", cancel
	s.setMessage("", false)
	go func() {
		scan, err := scanBatchPaths(ctx, paths, existing, existingCount)
		s.update(func() {
			defer cancel()
			s.completeBatchScan(ctx, scan, appendMode, err)
		})
	}()
}

func (s *application) completeBatchScan(ctx context.Context, scan batchScanResult, appendMode bool, err error) {
	if canceled := ctx.Err(); canceled != nil {
		err = canceled
	}
	s.busy, s.phase, s.cancel, s.canceling = false, "", nil, false
	if err != nil {
		s.reportError(err, "扫描图片失败")
		return
	}
	if len(scan.items) == 0 {
		if appendMode && scan.duplicates > 0 {
			s.setMessage("所选图片均已在批量队列中。", false)
		} else {
			s.setMessage("未找到可处理的 JPEG 或 PNG 图片。", true)
		}
		return
	}
	if appendMode && s.batch != nil {
		s.closeBatchPreview()
		s.batch.items = append(s.batch.items, scan.items...)
		if s.batch.selected < 0 {
			s.batch.selected = 0
		}
	} else {
		s.file, s.result, s.preview = nil, nil, nil
		s.comparison = comparisonView{split: 50}
		s.confirmOverwrite, s.overwritten = false, false
		s.batch = newBatchState(scan.items)
	}
	s.setMessage(batchScanMessage(len(scan.items), scan.duplicates, scan.truncated, appendMode), false)
}

func batchScanMessage(added, duplicates int, truncated, appendMode bool) string {
	verb := "已加载"
	if appendMode {
		verb = "已加入"
	}
	msg := fmt.Sprintf("%s %d 张图片。", verb, added)
	if duplicates > 0 {
		msg += fmt.Sprintf("已忽略 %d 张重复项。", duplicates)
	}
	if truncated {
		msg += fmt.Sprintf("单次最多处理 %d 张，其余文件未加入。", maxBatchFiles)
	}
	return msg
}

func scanBatchPaths(ctx context.Context, inputs []string, existing map[string]bool, existingCount int) (batchScanResult, error) {
	var out batchScanResult
	seen := make(map[string]bool, len(existing))
	maps.Copy(seen, existing)

	dirCount := 0
	isDir := make([]bool, len(inputs))
	for i, input := range inputs {
		if info, err := os.Stat(input); err == nil && info.IsDir() {
			isDir[i] = true
			dirCount++
		}
	}

	addFile := func(path, relPath string, explicit bool) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if existingCount+len(out.items) >= maxBatchFiles {
			out.truncated = true
			return errBatchLimit
		}
		absPath := canonicalBatchKey(path)
		if seen[absPath] {
			out.duplicates++
			return nil
		}
		seen[absPath] = true
		relPath = sanitizeRelPath(relPath, filepath.Base(path))
		info, err := compress.Inspect(ctx, path)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if !explicit && !isSupportedImageExt(path) {
				return nil
			}
			out.items = append(out.items, batchItem{
				path:         path,
				absPath:      absPath,
				relPath:      relPath,
				status:       batchFailed,
				invalidInput: true,
				errMsg:       err.Error(),
			})
			return nil
		}
		out.items = append(out.items, batchItem{
			path:         path,
			absPath:      absPath,
			relPath:      relPath,
			format:       info.Format,
			width:        info.Width,
			height:       info.Height,
			originalSize: info.Size,
			status:       batchPending,
		})
		return nil
	}

	for i, input := range inputs {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if !isDir[i] {
			if err := addFile(input, filepath.Base(input), true); err != nil {
				if errors.Is(err, errBatchLimit) {
					return out, nil
				}
				return out, err
			}
			continue
		}
		rootClean := filepath.Clean(input)
		rootBase := filepath.Base(rootClean)
		err := filepath.WalkDir(rootClean, func(p string, d fs.DirEntry, walkErr error) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if walkErr != nil {
				if p == rootClean {
					return walkErr
				}
				return nil
			}
			name := d.Name()
			if p != rootClean && strings.HasPrefix(name, ".") {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.Type()&os.ModeSymlink != 0 {
				return nil
			}
			if d.IsDir() {
				return nil
			}
			if !isSupportedImageExt(name) {
				return nil
			}
			rel, relErr := filepath.Rel(rootClean, p)
			if relErr != nil {
				rel = name
			}
			if dirCount > 1 {
				rel = filepath.Join(rootBase, rel)
			}
			return addFile(p, rel, false)
		})
		if err != nil {
			if errors.Is(err, errBatchLimit) {
				return out, nil
			}
			if errors.Is(err, context.Canceled) {
				return out, err
			}
			return out, err
		}
	}
	return out, nil
}

func canonicalBatchKey(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	// Resolve symlinks on the parent directory (e.g. /tmp -> /private/tmp on macOS)
	// while keeping the file leaf intact so symlinked files stay identifiable.
	dir, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return filepath.Clean(abs)
	}
	return filepath.Join(dir, filepath.Base(abs))
}

func sanitizeRelPath(rel, fallback string) string {
	cleaned := filepath.Clean(rel)
	if cleaned == "." || cleaned == "" || filepath.IsAbs(cleaned) || strings.HasPrefix(cleaned, "..") {
		return fallback
	}
	return cleaned
}

func isSupportedImageExt(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg", ".png":
		return true
	default:
		return false
	}
}

func (s *application) clearBatch() {
	if s.modalOpen() || s.batch == nil {
		return
	}
	s.closeBatchPreview()
	s.batch = nil
	s.setMessage("", false)
}

func (s *application) removeSelectedBatchItem() {
	if s.modalOpen() || s.batch == nil {
		return
	}
	index := s.batch.selected
	if index < 0 || index >= len(s.batch.items) {
		return
	}
	if s.batch.previewOpen && s.batch.previewIndex == index {
		s.closeBatchPreview()
	}
	s.batch.items = slices.Delete(s.batch.items, index, index+1)
	if len(s.batch.items) == 0 {
		s.batch = nil
		s.setMessage("", false)
		return
	}
	s.batch.selected = min(index, len(s.batch.items)-1)
	if s.batch.previewOpen && s.batch.previewIndex > index {
		s.batch.previewIndex--
	}
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
		item := &s.batch.items[i]
		if item.invalidInput {
			continue
		}
		if item.status != batchPending {
			hadProcessed = true
		}
		item.status = batchPending
		item.outputSize = 0
		item.unchanged = false
		item.overwritten = false
		item.savedPath = ""
		item.errMsg = ""
	}
	s.batch.done, s.batch.total, s.batch.currentName = 0, 0, ""
	return hadProcessed
}

func (s *application) batchStats() (total, pending, done, failed int, origTotal, outTotal int64, canRun bool) {
	if s.batch == nil {
		return
	}
	total = len(s.batch.items)
	for _, item := range s.batch.items {
		origTotal += item.originalSize
		if !item.invalidInput {
			canRun = true
		}
		switch item.status {
		case batchPending, batchProcessing:
			pending++
		case batchDone:
			done++
			outTotal += item.outputSize
		case batchFailed:
			failed++
		}
	}
	return
}

func (s *application) startBatchCompression() {
	s.beginBatchRun(false)
}

func (s *application) restartBatchCompression() {
	s.beginBatchRun(true)
}

func (s *application) beginBatchRun(restart bool) {
	if s.modalOpen() || s.batch == nil {
		return
	}
	_, pending, _, _, _, _, canRun := s.batchStats()
	if !canRun {
		return
	}
	if !restart && pending == 0 {
		restart = true
	}
	if s.batch.outputMode == batchOutputOverwrite {
		s.pendingBatchRestart = restart
		s.confirmBatchOverwrite = true
		return
	}
	if s.batch.outputDir == "" {
		s.promptOutputDirAndStart(restart)
		return
	}
	s.launchBatchWorker(restart)
}

func (s *application) promptOutputDirAndStart(restart bool) {
	s.busy, s.phase = true, "请选择输出文件夹"
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:            s.window,
			Title:             "选择压缩结果输出文件夹",
			ButtonLabel:       "选择此文件夹",
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
			s.batch.outputDir = paths[0]
			s.launchBatchWorker(restart)
		})
	}()
}

func (s *application) executeConfirmedBatchOverwrite() {
	if s.busy || s.settingsOpen || s.confirmOverwrite || s.batch == nil {
		return
	}
	restart := s.pendingBatchRestart
	s.pendingBatchRestart = false
	s.confirmBatchOverwrite = false
	s.launchBatchWorker(restart)
}

func (s *application) launchBatchWorker(restart bool) {
	if s.busy || s.batch == nil {
		return
	}
	if restart {
		s.resetBatchRunState()
	} else {
		s.closeBatchPreview()
	}
	tasks, usedPaths := s.collectBatchTasks()
	if len(tasks) == 0 {
		return
	}
	options := s.compressionOptions()
	outputMode := s.batch.outputMode
	outputDir := s.batch.outputDir
	ctx, cancel := context.WithCancel(context.Background())
	s.batch.done = 0
	s.batch.total = len(tasks)
	s.batch.currentName = ""
	s.busy, s.phase, s.cancel, s.canceling = true, fmt.Sprintf("正在压缩 0 / %d", len(tasks)), cancel, false
	s.setMessage("", false)

	// Register shutdown wait on the main thread so App.Run exit cannot race with Add.
	s.saves.Add(1)
	go func() {
		defer s.saves.Done()
		s.runBatchTasks(ctx, tasks, options, outputMode, outputDir, usedPaths, s.update)
		s.update(func() {
			defer cancel()
			s.completeBatchRun(ctx)
		})
	}()
}

func (s *application) collectBatchTasks() ([]batchTask, map[string]bool) {
	var tasks []batchTask
	usedPaths := make(map[string]bool)
	for i, item := range s.batch.items {
		if item.savedPath != "" {
			usedPaths[item.savedPath] = true
		}
		if item.status == batchPending {
			tasks = append(tasks, batchTask{index: i, path: item.path, relPath: item.relPath})
		}
	}
	return tasks, usedPaths
}

func (s *application) runBatchTasks(
	ctx context.Context,
	tasks []batchTask,
	options compress.Options,
	outputMode int,
	outputDir string,
	usedPaths map[string]bool,
	deliver func(func()),
) {
	if outputMode == batchOutputFolder {
		info, err := os.Stat(outputDir)
		if err != nil || !info.IsDir() {
			errText := "输出文件夹不存在或不可访问"
			if err != nil {
				errText = "无法读取输出文件夹：" + err.Error()
			}
			deliver(func() {
				for _, task := range tasks {
					s.applyBatchItemUpdate(batchItemUpdate{
						index: task.index, status: batchFailed, errMsg: errText, advanceDone: true,
					})
				}
			})
			return
		}
	}
	if usedPaths == nil {
		usedPaths = make(map[string]bool)
	}
	total := len(tasks)
	for step, task := range tasks {
		if ctx.Err() != nil {
			return
		}
		name := filepath.Base(task.path)
		deliver(func() {
			if s.batch != nil && task.index < len(s.batch.items) {
				s.batch.items[task.index].status = batchProcessing
				s.batch.currentName = name
				if !s.canceling {
					s.phase = fmt.Sprintf("正在压缩 %d / %d · %s", step+1, total, name)
				}
			}
		})
		res, err := compress.File(ctx, task.path, options)
		if ctx.Err() != nil {
			deliver(func() {
				if s.batch != nil && task.index < len(s.batch.items) && s.batch.items[task.index].status == batchProcessing {
					s.batch.items[task.index].status = batchPending
				}
			})
			return
		}
		if err != nil {
			deliver(func() {
				s.applyBatchItemUpdate(batchItemUpdate{
					index: task.index, status: batchFailed, errMsg: err.Error(), advanceDone: true,
				})
			})
			continue
		}
		if outputMode == batchOutputFolder {
			dest, pathErr := resolveBatchOutputPath(outputDir, task.path, task.relPath, usedPaths)
			if pathErr == nil {
				pathErr = os.MkdirAll(filepath.Dir(dest), 0o755)
			}
			if pathErr == nil {
				pathErr = res.Save(dest)
			}
			if pathErr != nil {
				deliver(func() {
					s.applyBatchItemUpdate(batchItemUpdate{
						index: task.index, status: batchFailed, errMsg: pathErr.Error(), advanceDone: true,
					})
				})
				continue
			}
			deliver(func() {
				s.applyBatchItemUpdate(batchItemUpdate{
					index:        task.index,
					status:       batchDone,
					format:       res.Format,
					width:        res.Width,
					height:       res.Height,
					originalSize: res.OriginalSize,
					outputSize:   res.OutputSize,
					unchanged:    res.Unchanged,
					savedPath:    dest,
					advanceDone:  true,
				})
			})
			continue
		}
		// batchOutputOverwrite
		if res.Unchanged || res.OutputSize >= res.OriginalSize {
			deliver(func() {
				s.applyBatchItemUpdate(batchItemUpdate{
					index:        task.index,
					status:       batchDone,
					format:       res.Format,
					width:        res.Width,
					height:       res.Height,
					originalSize: res.OriginalSize,
					outputSize:   res.OriginalSize,
					unchanged:    true,
					overwritten:  false,
					advanceDone:  true,
				})
			})
			continue
		}
		if owErr := res.Overwrite(); owErr != nil {
			deliver(func() {
				s.applyBatchItemUpdate(batchItemUpdate{
					index: task.index, status: batchFailed, errMsg: owErr.Error(), advanceDone: true,
				})
			})
			continue
		}
		deliver(func() {
			s.applyBatchItemUpdate(batchItemUpdate{
				index:        task.index,
				status:       batchDone,
				format:       res.Format,
				width:        res.Width,
				height:       res.Height,
				originalSize: res.OriginalSize,
				outputSize:   res.OutputSize,
				unchanged:    false,
				overwritten:  true,
				advanceDone:  true,
			})
		})
	}
}

func (s *application) applyBatchItemUpdate(update batchItemUpdate) {
	if s.batch == nil || update.index < 0 || update.index >= len(s.batch.items) {
		return
	}
	item := &s.batch.items[update.index]
	item.status = update.status
	if update.format != "" {
		item.format = update.format
	}
	if update.width > 0 && update.height > 0 {
		item.width, item.height = update.width, update.height
	}
	if update.originalSize > 0 {
		item.originalSize = update.originalSize
	}
	item.outputSize = update.outputSize
	item.unchanged = update.unchanged
	item.overwritten = update.overwritten
	item.savedPath = update.savedPath
	item.errMsg = update.errMsg
	if update.advanceDone {
		s.batch.done++
	}
}

func (s *application) completeBatchRun(ctx context.Context) {
	s.busy, s.phase, s.cancel, s.canceling = false, "", nil, false
	if s.batch == nil {
		return
	}
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

// resolveBatchOutputPath chooses a collision-free output file inside outputDir
// without overwriting the source path or any existing file.
func resolveBatchOutputPath(outputDir, sourcePath, relPath string, used map[string]bool) (string, error) {
	rootAbs, err := filepath.Abs(outputDir)
	if err != nil {
		return "", fmt.Errorf("无法解析输出文件夹路径：%w", err)
	}
	sourceAbs, err := filepath.Abs(sourcePath)
	if err != nil {
		return "", fmt.Errorf("无法解析原图路径：%w", err)
	}
	safeRel := sanitizeRelPath(relPath, filepath.Base(sourcePath))
	candidate := filepath.Join(rootAbs, safeRel)
	relCheck, err := filepath.Rel(rootAbs, candidate)
	if err != nil || relCheck == "." || strings.HasPrefix(relCheck, "..") {
		return "", errors.New("输出路径超出目标文件夹范围")
	}
	sourceCanon := canonicalBatchKey(sourceAbs)
	canUse := func(path string) bool {
		if path == sourceAbs || canonicalBatchKey(path) == sourceCanon || used[path] {
			return false
		}
		_, statErr := os.Lstat(path)
		return errors.Is(statErr, os.ErrNotExist)
	}
	if canUse(candidate) {
		used[candidate] = true
		return candidate, nil
	}
	ext := filepath.Ext(candidate)
	stem := strings.TrimSuffix(candidate, ext)
	alt := stem + "-compressed" + ext
	for index := 2; !canUse(alt); index++ {
		alt = fmt.Sprintf("%s-compressed-%d%s", stem, index, ext)
	}
	used[alt] = true
	return alt, nil
}

func (s *application) openOutputDir() {
	if s.batch == nil || s.batch.outputDir == "" {
		return
	}
	if err := mygo.Shell.OpenPath(s.batch.outputDir); err != nil {
		s.setMessage("无法打开输出文件夹："+err.Error(), true)
	}
}

func (s *application) openBatchPreview(index int) {
	if s.modalOpen() || s.batch == nil || index < 0 || index >= len(s.batch.items) {
		return
	}
	item := s.batch.items[index]
	if item.invalidInput {
		s.setMessage("该文件无效，无法预览："+item.errMsg, true)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.busy, s.phase, s.cancel = true, "正在加载图片预览", cancel
	s.setMessage("", false)
	go func() {
		selected, err := readSelection(ctx, item.path)
		var result *compress.Result
		var outputPreview *ui.Bitmap
		if err == nil && item.status == batchDone {
			result = &compress.Result{
				SourcePath:   item.path,
				Format:       item.format,
				Width:        item.width,
				Height:       item.height,
				OriginalSize: item.originalSize,
				OutputSize:   item.outputSize,
				Unchanged:    item.unchanged,
			}
			if item.overwritten {
				outputPreview = selected.preview
			} else if item.savedPath != "" {
				var outSelected *selectedImage
				outSelected, err = readSelection(ctx, item.savedPath)
				if err == nil {
					outputPreview = outSelected.preview
				}
			}
		}
		s.update(func() {
			defer cancel()
			s.completeBatchPreview(ctx, index, item, selected, result, outputPreview, err)
		})
	}()
}

func (s *application) completeBatchPreview(
	ctx context.Context,
	index int,
	item batchItem,
	selected *selectedImage,
	result *compress.Result,
	outputPreview *ui.Bitmap,
	err error,
) {
	if canceled := ctx.Err(); canceled != nil {
		err = canceled
	}
	s.busy, s.phase, s.cancel, s.canceling = false, "", nil, false
	if err != nil {
		s.reportError(err, "加载预览失败")
		return
	}
	if s.batch == nil || index < 0 || index >= len(s.batch.items) {
		return
	}
	s.batch.previewOpen = true
	s.batch.previewIndex = index
	s.batch.selected = index
	s.file = selected
	s.result = result
	s.preview = selected.preview
	if outputPreview != nil {
		s.preview = outputPreview
	}
	s.overwritten = item.overwritten
	s.comparison = comparisonView{split: 50}
	if item.overwritten {
		s.comparison.mode = previewResult
	}
}

func (s *application) stepBatchPreview(delta int) {
	if s.modalOpen() || s.batch == nil || !s.batch.previewOpen || len(s.batch.items) == 0 {
		return
	}
	for next := s.batch.previewIndex + delta; next >= 0 && next < len(s.batch.items); next += delta {
		if !s.batch.items[next].invalidInput {
			s.openBatchPreview(next)
			return
		}
	}
}

func (s *application) closeBatchPreview() {
	if s.batch == nil {
		return
	}
	s.batch.previewOpen = false
	s.batch.previewIndex = -1
	s.file = nil
	s.result = nil
	s.preview = nil
	s.overwritten = false
	s.comparison = comparisonView{split: 50}
}
