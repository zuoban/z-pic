package main

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"zpic/internal/dropimport"
)

const (
	maxPendingDrops       = 16
	maxCachedDownloadURLs = 512
)

func (s *application) setFloatingOpen(open bool) {
	if !open {
		s.floatingOpen = false
		s.floating.expanded = false
		s.floating.synced = false
		s.thumbnails.floatingWanted = nil
		if window := s.floatingWindow; window != nil {
			window.Close()
		}
		return
	}
	if s.closed {
		return
	}
	s.floatingOpen = true
	if s.floatingWindow != nil {
		s.floatingWindow.ShowInactive()
		return
	}
	if s.window == nil {
		return
	}
	area := mygo.Screen.DisplayNearestPoint(mygo.Screen.CursorScreenPoint()).WorkArea
	s.floatingWindow = mygo.NewWindow(mygo.WindowOptions{
		Title: "拖图入队 · Z Pic", Width: floatingCompactWidth, Height: floatingCompactHeight,
		X: area.X + max(0, area.Width-floatingCompactWidth-24), Y: area.Y + 80,
		UseContentSize: true, Frameless: true, CornerRadius: 20,
		Transparent: true, Vibrancy: mygo.VibrancyGlass, VibrancyTint: windowVibrancyTintCSS,
		Panel: true, AlwaysOnTop: true, SkipTaskbar: true,
		DisableResize: true, DisableMaximize: true, DisableMinimize: true,
		Hidden: true, Content: ui.View(s.floatingView),
	})
	s.floatingWindow.OnClosed(func() {
		s.floatingWindow = nil
		s.floatingOpen = false
		s.floating.expanded = false
		s.floating.synced = false
		s.thumbnails.floatingWanted = nil
		if !s.closed && s.window != nil {
			s.window.Invalidate()
		}
	})
	s.floating.lastSynced = s.floatingSyncSnapshot()
	s.floating.synced = true
	s.floatingWindow.ShowInactive()
}

func (s *application) invalidateFloatingWindow() {
	if s.closed {
		return
	}
	if s.floatingWindow == nil && s.onFloatingInvalidate == nil {
		return
	}
	s.floating.lastSynced = s.floatingSyncSnapshot()
	s.floating.synced = true
	if s.floatingWindow != nil {
		s.floatingWindow.Invalidate()
	}
	if s.onFloatingInvalidate != nil {
		s.onFloatingInvalidate()
	}
}

func (s *application) syncFloatingWindowIfChanged() {
	if s.closed || (s.floatingWindow == nil && s.onFloatingInvalidate == nil) {
		return
	}
	snapshot := s.floatingSyncSnapshot()
	if s.floating.synced && snapshot == s.floating.lastSynced {
		return
	}
	s.floating.lastSynced = snapshot
	s.floating.synced = true
	if s.floatingWindow != nil {
		s.floatingWindow.Invalidate()
	}
	if s.onFloatingInvalidate != nil {
		s.onFloatingInvalidate()
	}
}

func (s *application) showMainWindow() {
	if s.window != nil && !s.closed {
		s.window.Restore()
		s.window.Show()
	}
}

func (s *application) downloadedImagesDir() (string, error) {
	if s.downloadDir != "" {
		return s.downloadDir, nil
	}
	dir, err := mygo.App.Path(mygo.PathDownloads)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "Z Pic"), nil
}

func (s *application) openDownloadedImagesDir() {
	dir, err := s.downloadedImagesDir()
	if err == nil {
		err = os.MkdirAll(dir, 0o700)
	}
	if err == nil {
		err = mygo.Shell.OpenPath(dir)
	}
	if err != nil {
		s.dropMessage, s.dropFailed = "无法打开下载位置："+err.Error(), true
		s.rememberDropFailure(s.dropMessage)
	}
}

func (s *application) receiveDrop(data transfer.Data) {
	if s.closed {
		return
	}
	if len(s.dropQueue) >= maxPendingDrops {
		s.dropMessage, s.dropFailed = "待接收内容较多，请稍后再拖入。", true
		s.rememberDropFailure(s.dropMessage)
		return
	}
	s.dropQueue = append(s.dropQueue, data)
	s.startNextDropImport()
	if s.window != nil {
		s.window.Invalidate()
	}
}

func (s *application) startNextDropImport() {
	if s.closed || s.modalOpen() || len(s.dropQueue) == 0 {
		return
	}
	data := s.dropQueue[0]
	s.dropQueue[0] = transfer.Data{}
	s.dropQueue = s.dropQueue[1:]
	dir, dirErr := s.downloadedImagesDir()
	cached := maps.Clone(s.downloadedURLs)
	if s.dropTransport == nil {
		s.dropTransport = dropimport.NewTransport()
	}
	transport := s.dropTransport
	ctx, cancel := context.WithCancel(context.Background())
	s.busy, s.phase, s.cancel, s.canceling = true, "正在接收拖入的图片", cancel, false
	if s.clipboardImporting {
		s.phase = "正在导入剪贴板图片"
	}
	s.dropImporting = true
	pasted := s.clipboardImporting
	s.dropMessage, s.dropFailed = "", len(s.dropFailures) > 0
	s.setMessage("", false)
	s.saves.Add(1)
	go func() {
		defer s.saves.Done()
		resolver := dropimport.NewResolver(dir, transport)
		items, err := dropimport.ParseContext(ctx, data)
		var paths []string
		var failures []string
		savedURLs := make(map[string]string)
		downloaded := 0
		if err != nil {
			failures = append(failures, err.Error())
		}
		for index, item := range items {
			if ctx.Err() != nil {
				break
			}
			phase := fmt.Sprintf("正在接收 %d / %d 张图片", index+1, len(items))
			if item.URL != "" {
				phase = fmt.Sprintf("正在下载 %d / %d 张图片", index+1, len(items))
			}
			s.update(func() {
				if !s.canceling {
					s.phase = phase
				}
			})
			if item.URL != "" && cached[item.URL] != "" {
				if info, statErr := os.Stat(cached[item.URL]); statErr == nil && info.Mode().IsRegular() {
					paths = append(paths, cached[item.URL])
					continue
				}
			}
			if item.Path == "" && dirErr != nil {
				failures = append(failures, "无法读取下载目录："+dirErr.Error())
				continue
			}
			path, saved, resolveErr := resolver.Resolve(ctx, item)
			if resolveErr != nil {
				if ctx.Err() == nil {
					failures = append(failures, resolveErr.Error())
				}
				continue
			}
			paths = append(paths, path)
			if saved {
				downloaded++
				if item.URL != "" {
					savedURLs[item.URL] = path
				}
			}
		}
		if ctx.Err() != nil {
			failures = append(failures, "接收已取消，已下载的原图保留在下载位置。")
		}
		s.update(func() {
			defer cancel()
			s.busy, s.phase, s.cancel, s.canceling = false, "", nil, false
			s.dropImporting = false
			s.recordDownloadedURLs(savedURLs)
			s.dropImportErrors = strings.Join(failures, "\n")
			if downloaded > 0 {
				s.dropMessage = fmt.Sprintf("已下载 %d 张，正在加入队列。", downloaded)
			}
			if len(paths) == 0 {
				if pasted && ctx.Err() != nil {
					s.clipboardOutcome = clipboardOutcomeCanceled
					s.reportError(ctx.Err(), "粘贴图片失败")
					s.completeClipboardImport()
					return
				}
				s.clipboardOutcome = clipboardOutcomeFailed
				s.dropMessage = s.dropImportErrors
				if s.dropMessage == "" {
					s.dropMessage = "请拖入本地 JPEG/PNG/WebP 图片或网页图片。"
				}
				s.dropFailed = true
				s.rememberDropFailure(s.dropMessage)
				s.setMessage(s.dropMessage, true)
				s.completeClipboardImport()
				return
			}
			if s.batch == nil && s.file != nil {
				paths = append([]string{s.file.path}, paths...)
			}
			s.dropScanning = true
			s.loadBatch(paths, s.batch != nil)
		})
	}()
}

func (s *application) completeDropScan() {
	if !s.dropScanning {
		return
	}
	s.dropScanning = false
	s.dropMessage = s.message
	failed := s.failed || s.dropImportErrors != ""
	s.dropFailed = failed || len(s.dropFailures) > 0
	if s.dropImportErrors != "" {
		s.dropMessage += "\n" + s.dropImportErrors
		s.setMessage(s.dropMessage, true)
	}
	if failed {
		s.rememberDropFailure(s.dropMessage)
	}
	s.dropImportErrors = ""
	s.completeClipboardImport()
}

func (s *application) rememberDropFailure(message string) {
	s.dropFailed = true
	if len(s.dropFailures) == maxPendingDrops {
		copy(s.dropFailures, s.dropFailures[1:])
		s.dropFailures[len(s.dropFailures)-1] = message
	} else {
		s.dropFailures = append(s.dropFailures, message)
	}
	s.invalidateFloatingWindow()
}

func (s *application) recordDownloadedURLs(saved map[string]string) {
	if len(saved) == 0 {
		return
	}
	if s.downloadedURLs == nil {
		s.downloadedURLs = make(map[string]string)
	}
	for rawURL, path := range saved {
		if _, exists := s.downloadedURLs[rawURL]; !exists {
			s.downloadedURLOrder = append(s.downloadedURLOrder, rawURL)
		}
		s.downloadedURLs[rawURL] = path
	}
	if excess := len(s.downloadedURLOrder) - maxCachedDownloadURLs; excess > 0 {
		for _, evicted := range s.downloadedURLOrder[:excess] {
			delete(s.downloadedURLs, evicted)
		}
		s.downloadedURLOrder = slices.Clone(s.downloadedURLOrder[excess:])
	}
}

func (s *application) floatingStatus() (string, bool) {
	if len(s.dropFailures) > 0 {
		return s.dropFailures[len(s.dropFailures)-1], true
	}
	if len(s.dropQueue) > 0 {
		return fmt.Sprintf("已暂存 %d 次拖入，当前操作结束后入队。", len(s.dropQueue)), false
	}
	if s.busy {
		return s.phase, false
	}
	if s.dropMessage != "" {
		return s.dropMessage, s.dropFailed
	}
	if s.batch != nil {
		return fmt.Sprintf("队列中已有 %d 张图片", len(s.batch.items)), false
	}
	return "拖入后加入批量队列", false
}
