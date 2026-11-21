//go:build darwin && !ios

package main

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/ebitengine/purego/objc"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"zpic/internal/compress"
)

func TestNativeClipboardPNGSnapshot(t *testing.T) {
	pngData := nativeClipboardTestPNG(t, 0)
	for _, tt := range []struct {
		name  string
		items []map[string][]byte
		ok    bool
	}{
		{"empty", nil, false},
		{"PNG", []map[string][]byte{{"public.png": pngData}}, true},
		{"alternate representations", []map[string][]byte{{"public.png": pngData, "public.tiff": {1}}}, true},
		{"TIFF only", []map[string][]byte{{"public.tiff": {1}}}, false},
		{"mislabeled PNG", []map[string][]byte{{"public.png": {1}}}, false},
		{"second text item", []map[string][]byte{{"public.png": pngData}, {"public.utf8-plain-text": {1}}}, false},
		{"second file item", []map[string][]byte{{"public.png": pngData}, {"public.file-url": {1}}}, false},
		{"PNG with file URL", []map[string][]byte{{"public.png": pngData, "public.file-url": {1}}}, false},
		{"PNG with plain text", []map[string][]byte{{"public.png": pngData, "public.utf8-plain-text": {1}}}, false},
		{"PNG with URL", []map[string][]byte{{"public.png": pngData, "public.url": {1}}}, false},
		{"PNG with file promise", []map[string][]byte{{"public.png": pngData, "com.apple.pasteboard.promised-file-url": {1}}}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			board := nativeClipboardTestPasteboard(t)
			if len(tt.items) > 0 {
				setNativeClipboardTestItems(t, board, tt.items...)
			}
			data, revision, err := readNativeClipboardPNGSnapshot(board)
			if !tt.ok {
				if data != nil || !errors.Is(err, transfer.ErrFormat) {
					t.Fatalf("snapshot = %d bytes, %v, want transfer.ErrFormat", len(data), err)
				}
				return
			}
			if err != nil || !bytes.Equal(data, pngData) {
				t.Fatalf("snapshot = %d bytes, revision %d, %v", len(data), revision, err)
			}
		})
	}
}

func TestNativeClipboardPNGConditionalWrite(t *testing.T) {
	board := nativeClipboardTestPasteboard(t)
	original := nativeClipboardTestPNG(t, 0)
	replacement := nativeClipboardTestPNG(t, 255)
	setNativeClipboardTestItems(t, board, map[string][]byte{"public.png": original, "public.tiff": {1}})
	_, revision, err := readNativeClipboardPNGSnapshot(board)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeNativeClipboardPNGIfUnchanged(board, nil, revision); !errors.Is(err, transfer.ErrFormat) {
		t.Fatalf("invalid write = %v, want transfer.ErrFormat", err)
	}
	if data, current, err := readNativeClipboardPNGSnapshot(board); err != nil || current != revision || !bytes.Equal(data, original) {
		t.Fatalf("invalid write modified original snapshot: revision %d, %v", current, err)
	}
	if err := writeNativeClipboardPNGIfUnchanged(board, replacement, revision); err != nil {
		t.Fatal(err)
	}
	data, current, err := readNativeClipboardPNGSnapshot(board)
	if err != nil || current == revision || !bytes.Equal(data, replacement) {
		t.Fatalf("replacement snapshot = %d bytes, revision %d, %v", len(data), current, err)
	}
	// Re-copying identical bytes still changes pasteboard ownership.
	setNativeClipboardTestItems(t, board, map[string][]byte{"public.png": original})
	if err := writeNativeClipboardPNGIfUnchanged(board, replacement, revision); !errors.Is(err, errClipboardChanged) {
		t.Fatalf("stale write = %v, want errClipboardChanged", err)
	}
	if data, restored, err := readNativeClipboardPNGSnapshot(board); err != nil || restored == revision || !bytes.Equal(data, original) {
		t.Fatalf("stale write modified re-copied snapshot: revision %d, %v", restored, err)
	}
}

func TestNativeClipboardPNGConditionalWriteRefusesAppendedItems(t *testing.T) {
	for _, typ := range []string{"public.utf8-plain-text", "public.file-url"} {
		t.Run(typ, func(t *testing.T) {
			board := nativeClipboardTestPasteboard(t)
			original := nativeClipboardTestPNG(t, 0)
			setNativeClipboardTestItems(t, board, map[string][]byte{"public.png": original})
			_, revision, err := readNativeClipboardPNGSnapshot(board)
			if err != nil {
				t.Fatal(err)
			}
			appendNativeClipboardTestItems(t, board, map[string][]byte{typ: {1}})
			if err := writeNativeClipboardPNGIfUnchanged(board, nativeClipboardTestPNG(t, 255), revision); !errors.Is(err, errClipboardChanged) {
				t.Fatalf("write after append = %v, want errClipboardChanged", err)
			}
			if count := board.Send(clipboardNativePasteboardItems).Send(clipboardNativeCount); count != 2 {
				t.Fatalf("write after append left %d items, want 2", count)
			}
		})
	}
}

func TestNativeClipboardPNGConditionalWriteRefusesInPlaceUpdate(t *testing.T) {
	board := nativeClipboardTestPasteboard(t)
	original := nativeClipboardTestPNG(t, 0)
	updated := nativeClipboardTestPNG(t, 255)
	setNativeClipboardTestItems(t, board, map[string][]byte{"public.png": original})
	_, revision, err := readNativeClipboardPNGSnapshot(board)
	if err != nil {
		t.Fatal(err)
	}
	pngType := objc.ID(objc.GetClass("NSString")).Send(clipboardNativeStringWithUTF8, clipboardNativePNGType)
	if !objc.Send[bool](board, clipboardNativeSetDataForType, nativeClipboardData(updated), pngType) {
		t.Fatal("cannot update PNG in isolated pasteboard")
	}
	if err := writeNativeClipboardPNGIfUnchanged(board, original, revision); !errors.Is(err, errClipboardChanged) {
		t.Fatalf("write after in-place update = %v, want errClipboardChanged", err)
	}
	if data, _, err := readNativeClipboardPNGSnapshot(board); err != nil || !bytes.Equal(data, updated) {
		t.Fatalf("write after in-place update modified the clipboard: got %d bytes, %v", len(data), err)
	}
}

func nativeClipboardTestPasteboard(t *testing.T) objc.ID {
	t.Helper()
	class, err := clipboardNativePasteboardClass()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(openClipboardNativePool())
	board := class.Send(objc.RegisterName("pasteboardWithUniqueName"))
	if board == 0 {
		t.Fatal("cannot open an isolated pasteboard")
	}
	t.Cleanup(func() { board.Send(objc.RegisterName("releaseGlobally")) })
	return board
}

func setNativeClipboardTestItems(t *testing.T, board objc.ID, representations ...map[string][]byte) {
	t.Helper()
	board.Send(clipboardNativeClearContents)
	appendNativeClipboardTestItems(t, board, representations...)
}

func appendNativeClipboardTestItems(t *testing.T, board objc.ID, representations ...map[string][]byte) {
	t.Helper()
	items := objc.ID(objc.GetClass("NSMutableArray")).Send(clipboardNativeNew).Send(clipboardNativeAutorelease)
	for _, reps := range representations {
		item := objc.ID(objc.GetClass("NSPasteboardItem")).Send(clipboardNativeNew).Send(clipboardNativeAutorelease)
		for typ, data := range reps {
			name := objc.ID(objc.GetClass("NSString")).Send(clipboardNativeStringWithUTF8, typ)
			if !objc.Send[bool](item, clipboardNativeSetDataForType, nativeClipboardData(data), name) {
				t.Fatalf("cannot prepare test type %s", typ)
			}
		}
		items.Send(objc.RegisterName("addObject:"), item)
	}
	if !objc.Send[bool](board, clipboardNativeWriteObjects, items) {
		t.Fatal("cannot write to isolated pasteboard")
	}
}

func nativeClipboardTestPNG(t *testing.T, gray uint8) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, 1, 1))
	img.SetGray(0, 0, color.Gray{Y: gray})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestNativeClipboardPNGCompressThroughWindow(t *testing.T) {
	if !supportsClipboardPNGCompression() {
		t.Skip("requires the bundled PNG tools")
	}
	var input bytes.Buffer
	screenshot := image.NewNRGBA(image.Rect(0, 0, 128, 96))
	screenshot.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 128})
	if err := (&png.Encoder{CompressionLevel: png.NoCompression}).Encode(&input, screenshot); err != nil {
		t.Fatal(err)
	}

	for _, replaceDuringCompression := range []bool{false, true} {
		name := "write smaller PNG"
		if replaceDuringCompression {
			name = "preserve newly copied PNG"
		}
		t.Run(name, func(t *testing.T) {
			board := nativeClipboardTestPasteboard(t)
			setNativeClipboardTestItems(t, board, map[string][]byte{"public.png": input.Bytes()})
			app := newApplication()
			app.outputFormat, app.lossless = compress.OutputFormatJPEG, true
			app.targetSizeBytes = compress.MinTargetSizeBytes
			app.readClipboardPNG = func() ([]byte, int64, error) { return readNativeClipboardPNGSnapshot(board) }
			app.replaceClipboardPNG = func(data []byte, revision int64) error {
				return writeNativeClipboardPNGIfUnchanged(board, data, revision)
			}
			updates := make(chan func(), 1)
			app.dispatchUpdate = func(fn func()) { updates <- fn }
			tester := ui.NewTester(app.view, 980, 720)
			if replaceDuringCompression {
				if err := tester.Click("压缩剪贴板图片"); err != nil {
					t.Fatal(err)
				}
			} else {
				tester.Key(ui.Cmd|ui.Shift, ui.KeyV)
			}
			if !app.busy || app.phase != "正在压缩剪贴板图片" {
				t.Fatalf("clipboard compression did not start: %q", app.message)
			}
			newImage := nativeClipboardTestPNG(t, 42)
			if replaceDuringCompression {
				setNativeClipboardTestItems(t, board, map[string][]byte{"public.png": newImage})
			}
			awaitBatchUpdate(t, updates)()
			data, _, err := readNativeClipboardPNGSnapshot(board)
			if err != nil {
				t.Fatal(err)
			}
			if app.busy || app.failed || app.file != nil || app.batch != nil || app.result != nil {
				t.Fatalf("unexpected workspace state or error: %q", app.message)
			}
			if replaceDuringCompression {
				if !bytes.Equal(data, newImage) || !strings.Contains(app.message, "保留了新内容") {
					t.Fatalf("new clipboard content was not preserved: %q", app.message)
				}
				return
			}
			if len(data) >= input.Len() || int64(len(data)) > compress.MinTargetSizeBytes || !strings.Contains(app.message, "已达目标") {
				t.Fatalf("unexpected physical clipboard bytes %d → %d: %s", input.Len(), len(data), app.message)
			}
			decoded, err := png.Decode(bytes.NewReader(data))
			if err != nil || decoded.Bounds() != screenshot.Bounds() || decoded.At(0, 0) != screenshot.At(0, 0) {
				t.Fatalf("clipboard data did not decode with original dimensions and alpha: %v", err)
			}
			consumedImage := objc.ID(objc.GetClass("NSImage")).Send(objc.RegisterName("alloc")).
				Send(objc.RegisterName("initWithPasteboard:"), board).Send(clipboardNativeAutorelease)
			if consumedImage == 0 || !objc.Send[bool](consumedImage, objc.RegisterName("isValid")) {
				t.Fatal("AppKit could not consume the compressed PNG from the pasteboard")
			}
		})
	}
}
