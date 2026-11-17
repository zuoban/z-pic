package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"zpic/internal/compress"
)

const (
	maxInputBytes = 64 << 20
	maxPixels     = 32_000_000
)

type selectedImage struct {
	path    string
	format  string
	width   int
	height  int
	size    int64
	data    []byte
	preview *ui.Bitmap
}

// Application state is read and changed on MyGo's main thread. Background work
// receives immutable values and delivers its result through Window.Update.
type application struct {
	window         *mygo.Window
	settingsWindow *mygo.Window
	file           *selectedImage
	quality        float64
	scheme         string
	theme          themeMode
	png            compress.PNGOptions
	output         presetSaveOptions
	settingsOpen   bool
	settingsFile   string
	settingsError  string
	presets        []compressionPreset
	editor         *settingsEditor
	result         *compress.Result
	preview        *ui.Bitmap
	comparison     comparisonView
	busy           bool
	phase          string
	message        string
	failed         bool
	cancel         context.CancelFunc
	canceling      bool
	closed         bool
	saves          sync.WaitGroup

	confirmOverwrite      bool
	overwritten           bool
	batch                 *batchState
	thumbnails            batchThumbnailCache
	confirmBatchOverwrite bool
	pendingBatchRestart   bool
	pendingBatchRun       batchRunMode
	pendingBatchIndex     int
	batchSearchRef        ui.Handle
	batchOutputDir        string
	lastSavedPath         string
	dispatchUpdate        func(func())
	showInFolder          func(string)
	applyNativeTheme      func(mygo.ThemeSource)
	writeClipboardImage   func([]byte) error
	clipboardToast        *ui.Toast
	floatingWindow        *mygo.Window
	floatingOpen          bool
	floating              floatingState
	dropQueue             []transfer.Data
	dropImporting         bool
	dropScanning          bool
	dropMessage           string
	dropFailed            bool
	dropFailures          []string
	dropImportErrors      string
	downloadDir           string
	downloadedURLs        map[string]string
}

func newApplication() *application {
	return &application{
		quality: 80, scheme: "均衡", theme: themeSystem, png: compress.DefaultPNGOptions(),
		presets:           slices.Clone(compressionPresets),
		comparison:        comparisonView{split: 50},
		pendingBatchIndex: -1,
	}
}

func (s *application) setMessage(message string, failed bool) {
	s.message, s.failed = message, failed
}

func (s *application) modalOpen() bool {
	return s.busy || s.settingsOpen || s.confirmOverwrite || s.confirmBatchOverwrite
}

func (s *application) update(fn func()) {
	apply := func() {
		if s.closed {
			return
		}
		fn()
		s.startNextDropImport()
		s.invalidateFloatingWindow()
	}
	if s.dispatchUpdate != nil {
		s.dispatchUpdate(apply)
		return
	}
	if s.window == nil {
		return
	}
	s.window.Update(func() {
		if !s.closed {
			apply()
		}
	})
}

func (s *application) chooseFile() {
	if s.modalOpen() {
		return
	}
	s.busy, s.phase = true, "正在选择图片"
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:      s.window,
			Title:       "选择要压缩的图片",
			ButtonLabel: "选择图片",
			Multiple:    true,
			Filters: []mygo.FileFilter{
				{Name: "JPEG / PNG / WebP 图片", Extensions: []string{"jpg", "jpeg", "png", "webp"}},
			},
		})
		s.update(func() {
			s.busy, s.phase = false, ""
			if err != nil {
				s.setMessage("无法打开文件选择器："+err.Error(), true)
			} else if len(paths) != 0 {
				s.openPaths(paths)
			}
		})
	}()
}

func (s *application) dropFiles(paths []string) {
	if s.modalOpen() {
		return
	}
	s.openPaths(paths)
}

func (s *application) shouldOpenAsBatch(paths []string) bool {
	if s.batch != nil || len(paths) > 1 {
		return true
	}
	if len(paths) == 1 {
		info, err := os.Stat(paths[0])
		return err == nil && info.IsDir()
	}
	return false
}

func (s *application) openPaths(paths []string) {
	if len(paths) == 0 {
		return
	}
	if !s.shouldOpenAsBatch(paths) {
		s.load(paths[0])
		return
	}
	s.loadBatch(paths, s.batch != nil)
}

func (s *application) load(path string) {
	if s.modalOpen() {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.busy, s.phase, s.cancel = true, "正在读取图片", cancel
	s.setMessage("", false)
	go func() {
		selected, err := readSelection(ctx, path)
		s.update(func() {
			defer cancel()
			s.completeSelection(ctx, selected, err)
		})
	}()
}

func (s *application) completeSelection(ctx context.Context, selected *selectedImage, err error) {
	if canceled := ctx.Err(); canceled != nil {
		err = canceled
	}
	s.busy, s.phase, s.cancel, s.canceling = false, "", nil, false
	if err != nil {
		s.reportError(err, "读取图片失败")
		return
	}
	s.batch = nil
	s.file, s.preview, s.result = selected, selected.preview, nil
	s.lastSavedPath = ""
	s.comparison = comparisonView{split: 50}
	s.confirmOverwrite, s.overwritten = false, false
	s.setMessage("图片已就绪，可以开始压缩。", false)
}

func readSelection(ctx context.Context, path string) (*selectedImage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("无法打开图片：%w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("请选择普通图片文件")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("无法打开图片：%w", err)
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("请选择图片文件")
	}
	if info.Size() > maxInputBytes {
		return nil, errors.New("图片超过 64 MiB，请选择较小的文件")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxInputBytes+1))
	if err != nil {
		return nil, fmt.Errorf("无法读取图片：%w", err)
	}
	if len(data) > maxInputBytes {
		return nil, errors.New("图片超过 64 MiB，请选择较小的文件")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("无法识别图片，文件可能已经损坏")
	}
	if format != "jpeg" && format != "png" && format != "webp" {
		return nil, errors.New("目前仅支持 JPEG、PNG 和 WebP 图片")
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > maxPixels/config.Height {
		return nil, errors.New("图片超过 3200 万像素，请选择较小的图片")
	}
	if format == "png" && compress.IsAnimatedPNG(data) {
		return nil, errors.New("不支持动画 PNG，仅支持静态 JPEG/PNG/WebP")
	}
	if format == "webp" {
		if err := compress.ValidateWebP(ctx, data); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	preview, err := ui.DecodeBitmap(data)
	if err != nil {
		return nil, fmt.Errorf("无法生成预览：%w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	width, height := preview.Size()
	return &selectedImage{
		path: path, format: format, width: width, height: height,
		size: int64(len(data)), data: data, preview: preview,
	}, nil
}

func (s *application) invalidateResult() {
	s.result = nil
	s.lastSavedPath = ""
	s.comparison = comparisonView{split: 50}
	s.confirmOverwrite, s.confirmBatchOverwrite, s.overwritten = false, false, false
	s.clearPendingBatchConfirmation()
	if s.batch != nil {
		s.invalidateBatchResults()
		return
	}
	if s.file != nil {
		s.preview = s.file.preview
		s.setMessage("参数已更新，请重新压缩", false)
	}
}

func (s *application) startActiveCompression() {
	if s.batch != nil {
		if !s.batch.previewOpen {
			s.startBatchCompression()
		}
		return
	}
	s.startCompression()
}

func (s *application) startCompression() {
	if s.modalOpen() || s.batch != nil || s.file == nil {
		return
	}
	path, data, sourcePreview, options := s.file.path, s.file.data, s.file.preview, s.compressionOptions()
	ctx, cancel := context.WithCancel(context.Background())
	s.busy, s.phase, s.cancel = true, "正在压缩图片", cancel
	s.result, s.preview, s.lastSavedPath = nil, s.file.preview, ""
	s.comparison = comparisonView{split: 50}
	s.confirmOverwrite, s.overwritten = false, false
	s.setMessage("", false)
	go func() {
		result, err := compress.Bytes(ctx, data, path, options)
		var preview *ui.Bitmap
		if err == nil {
			if result.Unchanged && sourcePreview != nil {
				preview = sourcePreview
			} else {
				preview, err = ui.DecodeBitmap(result.Data)
			}
		}
		if err == nil {
			err = ctx.Err()
		}
		s.update(func() {
			defer cancel()
			s.completeCompression(ctx, result, preview, err)
		})
	}()
}

func (s *application) completeCompression(ctx context.Context, result *compress.Result, preview *ui.Bitmap, err error) {
	if canceled := ctx.Err(); canceled != nil {
		err = canceled
	}
	s.busy, s.phase, s.cancel, s.canceling = false, "", nil, false
	if err != nil {
		s.reportError(err, "压缩失败")
		return
	}
	s.result, s.preview = result, preview
	s.comparison = comparisonView{split: 50}
	s.confirmOverwrite, s.overwritten = false, false
	if result.Unchanged {
		s.setMessage("未减小，已保留原始数据", false)
	} else {
		s.setMessage("压缩完成，可以按预设保存结果", false)
	}
}

func (s *application) cancelWork() {
	if s.cancel != nil && !s.canceling {
		s.canceling = true
		s.phase = "正在取消，请等待当前编解码步骤结束"
		s.cancel()
	}
}

func (s *application) reportError(err error, prefix string) {
	if errors.Is(err, context.Canceled) {
		s.setMessage("操作已取消。", false)
		return
	}
	s.setMessage(prefix+"："+err.Error(), true)
}

func (s *application) saveActiveAction() {
	if s.batch != nil {
		if !s.batch.previewOpen && s.output.Mode == presetSaveAsk {
			s.chooseOutputDir()
		}
		return
	}
	s.savePresetResult()
}

func (s *application) saveCopy() {
	if s.modalOpen() || s.batch != nil || s.result == nil {
		return
	}
	result := s.result
	s.busy, s.phase = true, "请选择保存位置"
	go func() {
		ext := ".jpg"
		if result.Format == "png" {
			ext = ".png"
		} else if result.Format == "webp" {
			ext = ".webp"
		}
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
			Parent:            s.window,
			Title:             "另存压缩副本",
			ButtonLabel:       "保存副本",
			DefaultPath:       suggestedPath(result.SourcePath, ext),
			CreateDirectories: true,
			Message:           "请选择新文件名，原图与已有文件不会被覆盖。",
			Filters: []mygo.FileFilter{
				{Name: strings.ToUpper(result.Format) + " 图片", Extensions: []string{ext[1:]}},
			},
		})
		s.update(func() {
			if err != nil {
				s.busy, s.phase = false, ""
				s.reportError(err, "保存失败")
				return
			}
			if path == "" {
				s.busy, s.phase = false, ""
				return
			}
			if !strings.EqualFold(filepath.Ext(path), ext) &&
				!(ext == ".jpg" && strings.EqualFold(filepath.Ext(path), ".jpeg")) {
				path += ext
			}
			s.writeCopy(result, path)
		})
	}()
}

// Called on the main thread, so Add cannot race with the Wait after App.Run.
// Only disk I/O is tracked: closing a file picker must not hold up shutdown.
func (s *application) writeCopy(result *compress.Result, path string) {
	s.phase = "正在保存副本"
	s.saves.Add(1)
	go func() {
		defer s.saves.Done()
		err := result.Save(path)
		s.update(func() {
			s.busy, s.phase = false, ""
			if err != nil {
				s.lastSavedPath = ""
				s.reportError(err, "保存失败")
			} else {
				s.lastSavedPath = path
				s.setMessage("已保存："+path, false)
			}
		})
	}()
}

func (s *application) canOverwrite() bool {
	return !s.busy && !s.settingsOpen && !s.confirmBatchOverwrite && s.batch == nil &&
		s.file != nil && s.result != nil &&
		!s.result.Unchanged && s.result.OutputSize > 0 &&
		s.result.OutputSize < s.result.OriginalSize && !s.overwritten
}

func (s *application) requestOverwrite() {
	if s.canOverwrite() {
		s.confirmOverwrite = true
	}
}

// The confirmation invokes this on the UI thread. Started writes participate
// in the same shutdown wait as saving a separate copy.
func (s *application) overwriteOriginal() {
	if !s.canOverwrite() {
		return
	}
	result := s.result
	s.confirmOverwrite = false
	s.busy, s.phase = true, "正在覆盖原图"
	s.setMessage("", false)
	s.saves.Add(1)
	go func() {
		defer s.saves.Done()
		err := result.Overwrite()
		s.update(func() { s.completeOverwrite(result, err) })
	}()
}

func (s *application) completeOverwrite(result *compress.Result, err error) {
	s.busy, s.phase = false, ""
	if err != nil {
		s.lastSavedPath = ""
		s.reportError(err, "覆盖失败")
		return
	}
	// Future compression and parameter resets must use the image now on disk.
	// Keep this result visible for comparison and for saving another copy.
	s.file = &selectedImage{
		path: s.file.path, format: result.Format, width: result.Width, height: result.Height,
		size: result.OutputSize, data: result.Data, preview: s.preview,
	}
	s.overwritten = true
	s.lastSavedPath = s.file.path
	s.comparison.mode = previewResult
	s.setMessage("已覆盖原图："+s.file.path, false)
}

func (s *application) revealFileInFolder(path string) {
	if path == "" || s.modalOpen() {
		return
	}
	if _, err := os.Lstat(path); err != nil {
		s.setMessage("找不到输出文件："+path, true)
		return
	}
	if s.showInFolder != nil {
		s.showInFolder(path)
		return
	}
	mygo.Shell.ShowItemInFolder(path)
}

func suggestedPath(source, ext string) string {
	base := strings.TrimSuffix(source, filepath.Ext(source)) + "-compressed"
	path := base + ext
	for i := 2; ; i++ {
		_, err := os.Lstat(path)
		if err != nil {
			return path
		}
		path = fmt.Sprintf("%s-%d%s", base, i, ext)
	}
}

func formatSize(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	if size < 1024*1024 {
		return fmt.Sprintf("%.1f KiB", float64(size)/1024)
	}
	return fmt.Sprintf("%.2f MiB", float64(size)/(1024*1024))
}
