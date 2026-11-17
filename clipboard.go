package main

import (
	"context"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"zpic/internal/compress"
)

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
					s.clipboardToast = &ui.Toast{ID: "clipboard-result", Title: "复制图片失败", Description: s.message, Type: "error"}
				}
			} else {
				s.setMessage("已复制压缩图片，可直接粘贴", false)
				s.clipboardToast = &ui.Toast{ID: "clipboard-result", Title: "图片已复制到剪贴板", Description: "可直接粘贴到其他应用", Type: "success"}
			}
		})
	}()
}
