package main

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"zpic/internal/compress"
)

func TestClipboardPNGDoesNotReplaceLargerResult(t *testing.T) {
	app := newApplication()
	app.replaceClipboardPNG = func([]byte, int64) error {
		t.Fatal("a result that grows must not replace the clipboard")
		return nil
	}
	app.finishClipboardPNGCompression(&compress.Result{
		OriginalSize: 1000, Data: make([]byte, 1500), TargetSizeBytes: 1024, TargetSizeMissed: true,
	}, 1)
	if app.failed || !strings.Contains(app.message, "保留原有内容") || !strings.Contains(app.message, "已达目标") {
		t.Fatalf("report must describe the PNG actually kept on the clipboard: %q", app.message)
	}
}

func TestClipboardPNGInvalidInputAndTextFocus(t *testing.T) {
	app := newApplication()
	app.batch = newBatchState([]batchItem{{path: "/test/image.png", relPath: "image.png"}})
	reads := 0
	app.readClipboardPNG = func() ([]byte, int64, error) {
		reads++
		return nil, 0, transfer.ErrFormat
	}
	app.replaceClipboardPNG = func([]byte, int64) error {
		t.Fatal("unsupported input must not replace the clipboard")
		return nil
	}
	tester := ui.NewTester(app.view, 980, 720)
	if err := tester.Click("筛选文件名"); err != nil {
		t.Fatal(err)
	}
	tester.Key(ui.Cmd|ui.Shift, ui.KeyV)
	if reads != 0 {
		t.Fatal("clipboard shortcut intercepted text editing")
	}
	if err := tester.Click("压缩剪贴板图片"); err != nil {
		t.Fatal(err)
	}
	if reads != 1 || app.busy || !app.failed || !strings.Contains(app.message, "先复制一张 PNG") {
		t.Fatalf("unsupported input did not report a useful error: %q", app.message)
	}
}
