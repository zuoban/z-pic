package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"image"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"zpic/internal/compress"
	"zpic/internal/imageinput"
)

const (
	maxInputBytes         = imageinput.MaxSize
	maxPixels             = 32_000_000
	openFileCoalesceDelay = 60 * time.Millisecond
	// A save dialog suggestion is advisory; actual saving uses O_EXCL.
	maxSuggestedPathChecks = 1000
)

type openFileCoalescer struct {
	mu      sync.Mutex
	closed  bool
	paths   []string
	seen    map[string]struct{}
	timer   *time.Timer
	seq     uint64
	firstAt time.Time
	delay   time.Duration
}

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
	lossless       bool
	metadata       compress.MetadataPolicy
	outputFormat   compress.OutputFormat
	jpegBackground string
	resizeLimits
	targetSizeBytes          int64
	targetResizeMinDimension int
	autoCompress             bool
	backupToTrash            bool
	keepInMenuBar            bool
	quitting                 bool
	tray                     *appTray
	onTrayRefresh            func()
	cliInstallEnv            cliInstallEnv
	quickActionEnv           quickActionEnv
	updateChecker            updateCheckerConfig
	updateState              updateCheckState
	updateResult             updateCheckResult
	updateMessage            string
	updateMessageFailed      bool
	updateGen                int
	updateCancel             context.CancelFunc
	openExternalURL          func(string) error
	openAtLoginState         func() bool
	setOpenAtLogin           func(bool) error
	openAtLoginSupported     func() bool
	setWindowProgress        func(mygo.ProgressBar)
	lastWindowProgress       mygo.ProgressBar
	sendNotification         func(title, body string)
	output                   presetSaveOptions
	settingsOpen             bool
	settingsFile             string
	settingsError            string
	presets                  []compressionPreset
	editor                   *settingsEditor
	watch                    desktopFolderWatch
	result                   *compress.Result
	preview                  *ui.Bitmap
	comparison               comparisonView
	busy                     bool
	compressingSingle        bool
	phase                    string
	message                  string
	failed                   bool
	cancel                   context.CancelFunc
	canceling                bool
	closed                   bool
	saves                    sync.WaitGroup

	confirmOverwrite      bool
	overwritten           bool
	batch                 *batchState
	thumbnails            batchThumbnailCache
	imageMemory           *imageMemoryBudget
	confirmBatchOverwrite bool
	pendingBatchRestart   bool
	pendingBatchRun       batchRunMode
	pendingBatchIndex     int
	batchSearchRef        ui.Handle
	batchOutputDir        string
	lastSavedPath         string
	dispatchUpdate        func(func())
	showInFolder          func(string)
	trashItem             func(string) error
	previewItem           func(string) error
	applyNativeTheme      func(mygo.ThemeSource)
	writeClipboardImage   func([]byte) error
	readClipboardImages   func() (transfer.Data, error)
	readClipboardPNG      func() ([]byte, int64, error)
	replaceClipboardPNG   func([]byte, int64) error
	clipboardToast        *ui.Toast
	clipboardImporting    bool
	clipboardOutcome      clipboardImportOutcome
	floatingWindow        *mygo.Window
	onFloatingInvalidate  func()
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
	downloadedURLOrder    []string
	dropTransport         *http.Transport
	draggingOut           bool
	dragTempDir           string
	dragStagedResult      *compress.Result
	dragStagedPath        string
	openFiles             openFileCoalescer
}

func newApplication() *application {
	return &application{
		quality: 80, scheme: "均衡", theme: themeSystem, png: compress.DefaultPNGOptions(),
		presets:           defaultPresets(),
		comparison:        comparisonView{split: 50},
		pendingBatchIndex: -1,
		imageMemory:       newImageMemoryBudget(imageMemoryLimit),
	}
}

func (s *application) setMessage(message string, failed bool) {
	s.message, s.failed = message, failed
}

func (s *application) dialogOpen() bool {
	return s.settingsOpen || s.confirmOverwrite || s.confirmBatchOverwrite
}

func (s *application) desiredWindowProgress() mygo.ProgressBar {
	if s.closed || !s.busy {
		return mygo.ProgressBar{}
	}
	if s.batch != nil && s.batch.running && s.batch.total > 0 {
		if s.batch.done <= 0 {
			return mygo.ProgressBar{State: mygo.ProgressIndeterminate}
		}
		ratio := float64(s.batch.done) / float64(s.batch.total)
		if ratio > 1 {
			ratio = 1
		}
		return mygo.ProgressBar{State: mygo.ProgressNormal, Value: ratio}
	}
	if s.batch == nil && s.compressingSingle {
		return mygo.ProgressBar{State: mygo.ProgressIndeterminate}
	}
	return mygo.ProgressBar{}
}

func (s *application) applyWindowProgress(bar mygo.ProgressBar) {
	s.lastWindowProgress = bar
	if s.setWindowProgress != nil {
		s.setWindowProgress(bar)
		return
	}
	if s.window != nil {
		s.window.SetProgressBar(bar)
	}
}

func (s *application) syncWindowProgressIfChanged() {
	want := s.desiredWindowProgress()
	if want == s.lastWindowProgress {
		return
	}
	s.applyWindowProgress(want)
}

func (s *application) clearWindowProgress() {
	if s.lastWindowProgress == (mygo.ProgressBar{}) {
		return
	}
	s.applyWindowProgress(mygo.ProgressBar{})
}

func (s *application) modalOpen() bool {
	return s.busy || s.dialogOpen()
}

func (s *application) update(fn func()) {
	s.updateWithInvalidate(true, fn)
}

func (s *application) updateWithInvalidate(invalidateFloating bool, fn func()) {
	apply := func() {
		if s.closed {
			return
		}
		fn()
		s.startNextDropImport()
		if invalidateFloating {
			s.invalidateFloatingWindow()
		}
		s.syncWindowProgressIfChanged()
		s.syncTrayMenuIfChanged()
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

func (s *application) enqueueOpenFile(path string) {
	if path == "" {
		return
	}
	s.openFiles.mu.Lock()
	defer s.openFiles.mu.Unlock()
	if s.openFiles.closed {
		return
	}
	if s.openFiles.seen == nil {
		s.openFiles.seen = make(map[string]struct{})
	}
	if _, dup := s.openFiles.seen[path]; dup {
		return
	}
	s.openFiles.seen[path] = struct{}{}
	s.openFiles.paths = append(s.openFiles.paths, path)

	delay := s.openFiles.delay
	if delay <= 0 {
		delay = openFileCoalesceDelay
	}
	now := time.Now()
	if s.openFiles.firstAt.IsZero() {
		s.openFiles.firstAt = now
	}
	maxWait := 5 * delay
	remaining := maxWait - now.Sub(s.openFiles.firstAt)
	if remaining <= 0 {
		if s.openFiles.timer != nil {
			return
		}
		remaining = 0
	}
	wait := min(delay, remaining)
	if s.openFiles.timer != nil {
		s.openFiles.timer.Stop()
	}
	s.openFiles.seq++
	seq := s.openFiles.seq
	s.openFiles.timer = time.AfterFunc(wait, func() {
		s.flushOpenFiles(seq)
	})
}

func (s *application) flushOpenFiles(seq uint64) {
	s.openFiles.mu.Lock()
	if s.openFiles.closed || s.openFiles.seq != seq || len(s.openFiles.paths) == 0 {
		s.openFiles.mu.Unlock()
		return
	}
	paths := s.openFiles.paths
	s.openFiles.paths = nil
	s.openFiles.seen = nil
	s.openFiles.timer = nil
	s.openFiles.firstAt = time.Time{}
	s.openFiles.mu.Unlock()

	s.update(func() {
		s.showMainWindow()
		s.dropFiles(paths)
	})
}

func (s *application) cancelOpenFiles() {
	s.openFiles.mu.Lock()
	s.openFiles.closed = true
	s.openFiles.seq++
	if s.openFiles.timer != nil {
		s.openFiles.timer.Stop()
		s.openFiles.timer = nil
	}
	s.openFiles.paths = nil
	s.openFiles.seen = nil
	s.openFiles.firstAt = time.Time{}
	s.openFiles.mu.Unlock()
}

func supportedImageFormats() []string {
	formats := []string{"JPEG", "PNG", "WebP"}
	if compress.SupportsAVIF() {
		formats = append(formats, "AVIF")
	}
	if compress.SupportsHEIC() {
		formats = append(formats, "HEIC")
	}
	return formats
}

func noSupportedImagesMessage() string {
	return "未找到可处理的 " + strings.Join(supportedImageFormats(), " / ") + " 图片。"
}

func supportedFileFilter() mygo.FileFilter {
	extensions := []string{"jpg", "jpeg", "png", "webp"}
	if compress.SupportsAVIF() {
		extensions = append(extensions, "avif")
	}
	if compress.SupportsHEIC() {
		extensions = append(extensions, "heic", "heif")
	}
	return mygo.FileFilter{Name: strings.Join(supportedImageFormats(), " / ") + " 图片", Extensions: extensions}
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
			Filters:     []mygo.FileFilter{supportedFileFilter()},
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

func fileDropData(paths []string) (transfer.Data, bool) {
	cleaned := make([]string, 0, len(paths))
	for _, path := range paths {
		if path == "" || strings.ContainsRune(path, 0) {
			continue
		}
		if !filepath.IsAbs(path) {
			abs, err := filepath.Abs(path)
			if err != nil {
				continue
			}
			path = abs
		}
		cleaned = append(cleaned, path)
	}
	if len(cleaned) == 0 {
		return transfer.Data{}, false
	}
	data, err := transfer.FileData(cleaned...)
	return data, err == nil
}

func (s *application) dropFiles(paths []string) {
	if s.closed || s.dialogOpen() || s.draggingOut || len(paths) == 0 {
		return
	}
	filtered := make([]string, 0, len(paths))
	for _, path := range paths {
		if s.isDragStagedPath(path) {
			continue
		}
		filtered = append(filtered, path)
	}
	if len(filtered) == 0 {
		return
	}
	if s.busy || len(s.dropQueue) > 0 {
		if data, ok := fileDropData(filtered); ok {
			s.receiveDrop(data)
		}
		return
	}
	s.openPaths(filtered)
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
		selected, err := s.readSelectionWithBudget(ctx, path, nil)
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
	s.prepareThumbnailCache()
	s.file, s.preview, s.result = selected, selected.preview, nil
	s.lastSavedPath = ""
	s.resetComparison()
	s.confirmOverwrite, s.overwritten = false, false
	s.setMessage("", false)
	if s.autoCompress && !s.closed {
		s.startCompression()
	}
}

func (s *application) clearSingleImage() {
	if s.modalOpen() || s.batch != nil || s.file == nil {
		return
	}
	s.file, s.preview, s.result = nil, nil, nil
	s.lastSavedPath = ""
	s.resetComparison()
	s.confirmOverwrite, s.overwritten = false, false
	s.setMessage("", false)
}

func readSelection(ctx context.Context, path string) (*selectedImage, error) {
	return readSelectionWithPreview(ctx, path, nil)
}

func readSelectionWithPreview(ctx context.Context, path string, reusable *selectedImage) (*selectedImage, error) {
	return (*application)(nil).readSelectionWithBudget(ctx, path, reusable)
}

func (s *application) readSelectionWithBudget(ctx context.Context, path string, reusable *selectedImage) (*selectedImage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := imageinput.Read(ctx, path)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if reusable != nil && reusable.preview != nil && bytes.Equal(data, reusable.data) {
		selected := *reusable
		selected.path = path
		return &selected, nil
	}
	var (
		format string
		width  int
		height int
	)
	if compress.SniffAVIFHeader(data) || compress.SniffHEICHeader(data) {
		info, inspectErr := compress.InspectBytes(ctx, data)
		if inspectErr != nil {
			return nil, inspectErr
		}
		format, width, height = info.Format, info.Width, info.Height
	} else {
		config, fmtName, decErr := image.DecodeConfig(bytes.NewReader(data))
		if decErr != nil {
			return nil, errors.New("无法识别图片，文件可能已经损坏")
		}
		if fmtName != "jpeg" && fmtName != "png" && fmtName != "webp" {
			return nil, fmt.Errorf("目前仅支持 %s 图片", strings.Join(supportedImageFormats(), " / "))
		}
		if config.Width <= 0 || config.Height <= 0 || config.Width > maxPixels/config.Height {
			return nil, errors.New("图片超过 3200 万像素，请选择较小的图片")
		}
		if fmtName == "png" {
			if err := compress.ValidatePNG(ctx, data); err != nil {
				return nil, err
			}
		}
		if fmtName == "webp" {
			if err := compress.ValidateWebP(ctx, data); err != nil {
				return nil, err
			}
		}
		format, width, height = fmtName, config.Width, config.Height
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	release, err := s.acquirePreviewMemory(ctx, compress.ImageInfo{
		Path: path, Format: format, Width: width, Height: height, Size: int64(len(data)),
	})
	if err != nil {
		return nil, err
	}
	preview, err := decodePreviewBitmap(ctx, data, format)
	release()
	if err != nil {
		return nil, fmt.Errorf("无法生成预览：%w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	width, height = preview.Size()
	return &selectedImage{
		path: path, format: format, width: width, height: height,
		size: int64(len(data)), data: data, preview: preview,
	}, nil
}

func decodePreviewBitmap(ctx context.Context, data []byte, format string) (*ui.Bitmap, error) {
	if format == "heic" || format == "avif" {
		decoded, err := compress.DecodePreviewImage(ctx, data)
		if err != nil {
			return nil, err
		}
		return ui.NewBitmap(decoded), nil
	}
	if format == "webp" {
		if orient := compress.WebPOrientation(data); orient > 1 {
			decoded, _, err := image.Decode(bytes.NewReader(data))
			if err != nil {
				return nil, err
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return ui.NewBitmap(compress.NormalizeOrientation(decoded, orient)), nil
		}
	}
	return ui.DecodeBitmap(data)
}

func (s *application) invalidateResult() {
	s.result = nil
	s.lastSavedPath = ""
	s.resetComparison()
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
		if s.batch.previewOpen {
			s.compressBatchItem(s.batch.previewIndex)
		} else {
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
	file, options := *s.file, s.compressionOptions()
	ctx, cancel := context.WithCancel(context.Background())
	s.busy, s.compressingSingle, s.phase, s.cancel = true, true, "正在压缩图片", cancel
	s.result, s.preview, s.lastSavedPath = nil, s.file.preview, ""
	s.resetComparison()
	s.confirmOverwrite, s.overwritten = false, false
	s.setMessage("", false)
	s.syncWindowProgressIfChanged()
	go func() {
		release, err := s.acquireImageMemory(ctx, compress.ImageInfo{
			Path: file.path, Format: file.format, Width: file.width, Height: file.height, Size: file.size,
		}, false)
		var result *compress.Result
		var preview *ui.Bitmap
		if err == nil {
			var decoded image.Image
			result, decoded, err = compress.BytesWithPreview(ctx, file.data, file.path, options)
			if err == nil {
				if result.Unchanged && file.preview != nil {
					preview = file.preview
				} else if decoded != nil {
					preview = ui.NewBitmap(decoded)
				} else {
					preview, err = decodePreviewBitmap(ctx, result.Data, result.Format)
				}
			}
			release()
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
	s.busy, s.compressingSingle, s.phase, s.cancel, s.canceling = false, false, "", nil, false
	if err != nil {
		s.reportError(err, "压缩失败")
		return
	}
	s.result, s.preview = result, preview
	s.resetComparison()
	s.confirmOverwrite, s.overwritten = false, false
	s.setMessage("", false)
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
		ext := outputExtensionForFormat(result.Format)
		if ext == "" {
			ext = ".jpg"
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
		(s.result.Format == "" || s.result.Format == s.file.format) &&
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
func (s *application) overwriteBackupFunc() func(string) error {
	if !s.backupToTrash {
		return nil
	}
	if s.trashItem != nil {
		return s.trashItem
	}
	return mygo.Shell.TrashItem
}

func (s *application) overwriteOriginal() {
	if !s.canOverwrite() {
		return
	}
	result := s.result
	backup := s.overwriteBackupFunc()
	backedUp := backup != nil
	s.confirmOverwrite = false
	s.busy, s.phase = true, "正在覆盖原图"
	s.setMessage("", false)
	s.saves.Add(1)
	go func() {
		defer s.saves.Done()
		err := result.OverwriteWithBackup(backup)
		s.update(func() { s.completeOverwriteWithBackup(result, backedUp, err) })
	}()
}

func (s *application) completeOverwrite(result *compress.Result, err error) {
	s.completeOverwriteWithBackup(result, false, err)
}

func (s *application) completeOverwriteWithBackup(result *compress.Result, backedUp bool, err error) {
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
	s.clearComparisonDiff()
	s.comparison.mode = previewResult
	if backedUp {
		s.setMessage("已覆盖原图（原件已移入废纸篓）："+s.file.path, false)
		return
	}
	s.setMessage("已覆盖原图："+s.file.path, false)
}

func (s *application) supportsQuickLook() bool {
	return s.previewItem != nil || runtime.GOOS == "darwin"
}

func (s *application) quickLookPath() string {
	if s.modalOpen() {
		return ""
	}
	if s.batch != nil {
		index := s.batch.selected
		if s.batch.previewOpen {
			index = s.batch.previewIndex
		}
		if index < 0 || index >= len(s.batch.items) {
			return ""
		}
		item := s.batch.items[index]
		if outPath := batchItemOutputPath(item); outPath != "" {
			return outPath
		}
		return item.path
	}
	if s.lastSavedPath != "" {
		if _, err := os.Lstat(s.lastSavedPath); err == nil {
			return s.lastSavedPath
		}
	}
	if s.file != nil {
		return s.file.path
	}
	return ""
}

func (s *application) quickLookCurrent() {
	s.previewInQuickLook(s.quickLookPath())
}

func (s *application) previewInQuickLook(path string) {
	if path == "" || s.modalOpen() || !s.supportsQuickLook() {
		return
	}
	if _, err := os.Lstat(path); err != nil {
		s.setMessage("找不到要预览的文件："+path, true)
		return
	}
	preview := s.previewItem
	if preview == nil {
		preview = mygo.Shell.PreviewItem
	}
	if err := preview(path); err != nil && !errors.Is(err, errors.ErrUnsupported) {
		s.setMessage("无法打开快速预览："+err.Error(), true)
	}
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
	for i := 2; i <= maxSuggestedPathChecks+1; i++ {
		_, err := os.Lstat(path)
		if err != nil {
			return path
		}
		path = fmt.Sprintf("%s-%d%s", base, i, ext)
	}
	// Stop scanning crowded directories. The user can edit this suggestion,
	// and Result.Save will still refuse any existing destination.
	return path
}

func formatSize(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	if size < 1024*1024 {
		return fmt.Sprintf("%.1f KiB", float64(size)/1024)
	}
	if size >= 1024*1024*1024 {
		return fmt.Sprintf("%.2f GiB", float64(size)/(1024*1024*1024))
	}
	if size >= 100*1024*1024 {
		return fmt.Sprintf("%.1f MiB", float64(size)/(1024*1024))
	}
	return fmt.Sprintf("%.2f MiB", float64(size)/(1024*1024))
}
