package main

import (
	"context"
	"errors"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"zpic/internal/compress"
)

func (s *application) canPasteImages() bool {
	return !s.closed && !s.modalOpen() && len(s.dropQueue) == 0
}

func (s *application) pasteImages() {
	if !s.canPasteImages() {
		return
	}
	read := s.readClipboardImages
	if read == nil {
		read = func() (transfer.Data, error) {
			return mygo.Clipboard.Read(transfer.FileList, transfer.URIList, transfer.PNG, "image/jpeg", "image/webp")
		}
	}
	data, err := read()
	if errors.Is(err, transfer.ErrFormat) || err == nil && len(data.Items()) == 0 {
		s.setMessage("剪贴板中没有可导入的图片，请先复制图片或图片文件。", true)
		s.showClipboardToast("无法粘贴图片", s.message, true)
		return
	}
	if err != nil {
		s.reportError(err, "读取剪贴板失败")
		s.showClipboardToast("无法粘贴图片", s.message, true)
		return
	}
	s.clipboardImporting = true
	s.receiveDrop(data)
}

func (s *application) showClipboardToast(title, description string, failed bool) {
	kind := "success"
	if failed {
		kind = "error"
	}
	s.clipboardToast = &ui.Toast{ID: "clipboard-result", Title: title, Description: description, Type: kind}
}

func (s *application) completeClipboardImport() {
	if !s.clipboardImporting {
		return
	}
	s.clipboardImporting = false
	title := "剪贴板图片已加入队列"
	if s.failed {
		title = "剪贴板图片导入失败"
	} else if s.message == "操作已取消。" {
		title = "已取消粘贴图片"
	} else if s.message == "所选图片均已在批量队列中。" {
		title = "图片已在队列中"
	}
	s.showClipboardToast(title, s.message, s.failed)
}

func (s *application) canCopyResult() bool {
	return !s.closed && !s.modalOpen() && s.result != nil && len(s.result.Data) > 0 &&
		(s.batch == nil || s.batch.previewOpen)
}

func (s *application) copyResult() {
	if !s.canCopyResult() {
		return
	}
	data := s.result.Data
	ctx, cancel := context.WithCancel(context.Background())
	s.busy, s.phase, s.cancel = true, "正在准备剪贴板图片", cancel
	s.setMessage("", false)
	go func() {
		pngData, err := compress.PNGBytes(ctx, data)
		s.update(func() {
			defer cancel()
			if canceled := ctx.Err(); canceled != nil {
				err = canceled
			}
			s.busy, s.phase, s.cancel, s.canceling = false, "", nil, false
			if err == nil {
				if s.writeClipboardImage != nil {
					err = s.writeClipboardImage(pngData)
				} else {
					err = mygo.Clipboard.Write(transfer.New(transfer.NewItem(transfer.Bytes(transfer.PNG, pngData))))
				}
			}
			if err != nil {
				s.reportError(err, "复制图片失败")
				if s.failed {
					s.showClipboardToast("复制图片失败", s.message, true)
				}
			} else {
				s.setMessage("已复制压缩图片，可直接粘贴", false)
				s.showClipboardToast("图片已复制到剪贴板", "可直接粘贴到其他应用", false)
			}
		})
	}()
}
