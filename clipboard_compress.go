package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/egoist/mygo/transfer"

	"zpic/internal/compress"
)

var errClipboardChanged = errors.New("剪贴板内容已更新")

func (s *application) clipboardPNGCompressionAvailable() bool {
	return supportsClipboardPNGCompression() || s.readClipboardPNG != nil && s.replaceClipboardPNG != nil
}

func (s *application) canCompressClipboardPNG() bool {
	return s.clipboardPNGCompressionAvailable() && s.canPasteImages() && !s.dropImporting && !s.dropScanning
}

func (s *application) compressClipboardPNG() {
	if !s.canCompressClipboardPNG() {
		return
	}
	read := s.readClipboardPNG
	if read == nil {
		read = readClipboardPNGSnapshot
	}
	data, revision, err := read()
	if err != nil {
		if errors.Is(err, transfer.ErrFormat) {
			s.setMessage("请先复制一张 PNG 图片或截图。", true)
			s.showClipboardToast("无法压缩剪贴板图片", s.message, true)
		} else {
			s.reportClipboardPNGError(err)
		}
		return
	}

	options := s.compressionOptions()
	ctx, cancel := context.WithCancel(context.Background())
	s.busy, s.phase, s.cancel = true, "正在压缩剪贴板图片", cancel
	s.setMessage("", false)
	go func() {
		result, err := s.encodeClipboardPNG(ctx, data, options)
		s.update(func() {
			defer cancel()
			s.busy, s.phase, s.cancel, s.canceling = false, "", nil, false
			if ctx.Err() != nil {
				err = ctx.Err()
			}
			if err != nil {
				s.reportClipboardPNGError(err)
				return
			}
			s.finishClipboardPNGCompression(result, revision)
		})
	}()
}

func (s *application) encodeClipboardPNG(ctx context.Context, data []byte, options compress.Options) (*compress.Result, error) {
	info, err := compress.InspectBytes(ctx, data)
	if err != nil {
		return nil, err
	}
	release, err := s.acquireImageMemory(ctx, info, false)
	if err != nil {
		return nil, err
	}
	defer release()
	return compress.CompressClipboardPNG(ctx, data, options)
}

func (s *application) finishClipboardPNGCompression(result *compress.Result, revision int64) {
	original := formatSize(result.OriginalSize)
	finalSize := result.OriginalSize
	title := "剪贴板图片未变小"
	message := fmt.Sprintf("保留原有内容 · %s", original)
	if int64(len(result.Data)) < result.OriginalSize {
		write := s.replaceClipboardPNG
		if write == nil {
			write = writeClipboardPNGIfUnchanged
		}
		if err := write(result.Data, revision); err != nil {
			s.reportClipboardPNGError(err)
			return
		}
		title = "剪贴板图片已压缩"
		finalSize = int64(len(result.Data))
		message = fmt.Sprintf("%s → %s · 可直接粘贴", original, formatSize(finalSize))
		if result.TargetResized {
			message += fmt.Sprintf(" · 已缩小至 %d × %d", result.Width, result.Height)
		}
	}
	if result.TargetSizeBytes > 0 {
		if finalSize > result.TargetSizeBytes {
			message += " · 未达目标 ≤ " + compress.FormatTargetSize(result.TargetSizeBytes)
		} else {
			message += " · 已达目标 ≤ " + compress.FormatTargetSize(result.TargetSizeBytes)
		}
	}
	s.setMessage(message, false)
	s.showClipboardToast(title, message, false)
}

func (s *application) reportClipboardPNGError(err error) {
	if errors.Is(err, errClipboardChanged) {
		s.setMessage("剪贴板已更新，保留了新内容。", false)
		s.showClipboardToast("未替换剪贴板图片", s.message, false)
		return
	}
	s.reportError(err, "压缩剪贴板图片失败")
	if s.failed {
		s.showClipboardToast("压缩剪贴板图片失败", s.message, true)
	}
}
