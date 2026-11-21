//go:build darwin && !ios

package main

import (
	"bytes"
	"errors"
	"fmt"
	"hash/maphash"
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"github.com/egoist/mygo/transfer"

	"zpic/internal/imageinput"
)

const (
	clipboardNativePNGType      = "public.png"
	clipboardNativePNGSignature = "\x89PNG\r\n\x1a\n"
)

var (
	errNativeClipboardUnavailable = errors.New("macOS 剪贴板不可用")
	errNativeClipboardPNGTooLarge = fmt.Errorf("剪贴板 PNG 图片超过 %d MiB 限制", imageinput.MaxSize>>20)

	clipboardNativeRevisionSeed      = maphash.MakeSeed()
	clipboardNativeGeneralPasteboard = objc.RegisterName("generalPasteboard")
	clipboardNativeChangeCount       = objc.RegisterName("changeCount")
	clipboardNativePasteboardItems   = objc.RegisterName("pasteboardItems")
	clipboardNativeCount             = objc.RegisterName("count")
	clipboardNativeFirstObject       = objc.RegisterName("firstObject")
	clipboardNativeObjectAtIndex     = objc.RegisterName("objectAtIndex:")
	clipboardNativeTypes             = objc.RegisterName("types")
	clipboardNativeUTF8String        = objc.RegisterName("UTF8String")
	clipboardNativeDataForType       = objc.RegisterName("dataForType:")
	clipboardNativeLength            = objc.RegisterName("length")
	clipboardNativeBytes             = objc.RegisterName("bytes")
	clipboardNativeStringWithUTF8    = objc.RegisterName("stringWithUTF8String:")
	clipboardNativeDataWithBytes     = objc.RegisterName("dataWithBytes:length:")
	clipboardNativeArrayWithObject   = objc.RegisterName("arrayWithObject:")
	clipboardNativeNew               = objc.RegisterName("new")
	clipboardNativeAutorelease       = objc.RegisterName("autorelease")
	clipboardNativeDrain             = objc.RegisterName("drain")
	clipboardNativeSetDataForType    = objc.RegisterName("setData:forType:")
	clipboardNativeClearContents     = objc.RegisterName("clearContents")
	clipboardNativeWriteObjects      = objc.RegisterName("writeObjects:")
)

var clipboardNativePasteboardClass = sync.OnceValues(func() (objc.ID, error) {
	// Keep AppKit loaded for the lifetime of its Objective-C classes.
	if _, err := purego.Dlopen("/System/Library/Frameworks/AppKit.framework/AppKit", purego.RTLD_NOW|purego.RTLD_LOCAL); err != nil {
		return 0, fmt.Errorf("无法加载 macOS 剪贴板：%w", err)
	}
	class := objc.ID(objc.GetClass("NSPasteboard"))
	if class == 0 {
		return 0, errNativeClipboardUnavailable
	}
	return class, nil
})

func supportsClipboardPNGCompression() bool { return runtime.GOARCH == "arm64" }

// readClipboardPNGSnapshot returns a Go-owned PNG and a revision token for conditional write-back.
func readClipboardPNGSnapshot() ([]byte, int64, error) {
	class, err := clipboardNativePasteboardClass()
	if err != nil {
		return nil, 0, err
	}
	closePool := openClipboardNativePool()
	defer closePool()
	return readNativeClipboardPNGSnapshot(class.Send(clipboardNativeGeneralPasteboard))
}

func writeClipboardPNGIfUnchanged(data []byte, revision int64) error {
	class, err := clipboardNativePasteboardClass()
	if err != nil {
		return err
	}
	closePool := openClipboardNativePool()
	defer closePool()
	return writeNativeClipboardPNGIfUnchanged(class.Send(clipboardNativeGeneralPasteboard), data, revision)
}

func openClipboardNativePool() func() {
	// Autorelease pools must be drained on the thread they belong to.
	runtime.LockOSThread()
	pool := objc.ID(objc.GetClass("NSAutoreleasePool")).Send(clipboardNativeNew)
	return func() {
		pool.Send(clipboardNativeDrain)
		runtime.UnlockOSThread()
	}
}

func readNativeClipboardPNGSnapshot(board objc.ID) ([]byte, int64, error) {
	if board == 0 {
		return nil, 0, errNativeClipboardUnavailable
	}
	count := int64(board.Send(clipboardNativeChangeCount))
	data, err := nativeClipboardPNGBytes(board)
	var snapshot []byte
	var revision int64
	if err == nil {
		snapshot = bytes.Clone(data)
		revision = nativeClipboardRevision(count, snapshot)
	}
	if int64(board.Send(clipboardNativeChangeCount)) != count {
		return nil, 0, errClipboardChanged
	}
	return snapshot, revision, err
}

func nativeClipboardRevision(count int64, data []byte) int64 {
	// An owner can update the PNG in place without changing its ownership count.
	h := maphash.Bytes(clipboardNativeRevisionSeed, data)
	return int64(maphash.Comparable(clipboardNativeRevisionSeed, struct {
		count int64
		hash  uint64
	}{count: count, hash: h}))
}

// nativeClipboardPNGBytes borrows bytes from an NSData in the caller's autorelease pool.
func nativeClipboardPNGBytes(board objc.ID) ([]byte, error) {
	items := board.Send(clipboardNativePasteboardItems)
	if items.Send(clipboardNativeCount) != 1 {
		return nil, transfer.ErrFormat
	}
	item := items.Send(clipboardNativeFirstObject)
	types := item.Send(clipboardNativeTypes)
	var pngType objc.ID
	for i, count := objc.ID(0), types.Send(clipboardNativeCount); i < count; i++ {
		typ := types.Send(clipboardNativeObjectAtIndex, i)
		switch objc.Send[string](typ, clipboardNativeUTF8String) {
		case clipboardNativePNGType:
			pngType = typ
		case "public.file-url", "public.url", "dev.mygo.file-list", "text/uri-list",
			"com.apple.pasteboard.promised-file-url", "com.apple.pasteboard.promised-file-content-type",
			"NSFilenamesPboardType", "NSFilesPromisePboardType", "Apple URL pasteboard type",
			"public.text", "public.plain-text", "public.utf8-plain-text", "public.utf16-plain-text",
			"public.html", "public.rtf", "com.apple.rtfd", "NSStringPboardType", "NSHTMLPboardType", "NSRTFPboardType":
			return nil, transfer.ErrFormat
		}
	}
	if pngType == 0 {
		return nil, transfer.ErrFormat
	}
	// Request only the exact PNG representation, with no conversion or file reads.
	nativeData := item.Send(clipboardNativeDataForType, pngType)
	length := nativeData.Send(clipboardNativeLength)
	if length > imageinput.MaxSize {
		return nil, errNativeClipboardPNGTooLarge
	}
	ptr := objc.Send[unsafe.Pointer](nativeData, clipboardNativeBytes)
	if ptr == nil {
		return nil, transfer.ErrFormat
	}
	data := unsafe.Slice((*byte)(ptr), int(length))
	if !bytes.HasPrefix(data, []byte(clipboardNativePNGSignature)) {
		return nil, transfer.ErrFormat
	}
	return data, nil
}

func writeNativeClipboardPNGIfUnchanged(board objc.ID, data []byte, revision int64) error {
	if board == 0 {
		return errNativeClipboardUnavailable
	}
	if len(data) > imageinput.MaxSize {
		return errNativeClipboardPNGTooLarge
	}
	if !bytes.HasPrefix(data, []byte(clipboardNativePNGSignature)) {
		return transfer.ErrFormat
	}
	pngType := objc.ID(objc.GetClass("NSString")).Send(clipboardNativeStringWithUTF8, clipboardNativePNGType)
	nativeData := nativeClipboardData(data)
	item := objc.ID(objc.GetClass("NSPasteboardItem")).Send(clipboardNativeNew).Send(clipboardNativeAutorelease)
	if pngType == 0 || nativeData == 0 || !objc.Send[bool](item, clipboardNativeSetDataForType, nativeData, pngType) {
		return errors.New("无法准备剪贴板 PNG 数据")
	}
	items := objc.ID(objc.GetClass("NSArray")).Send(clipboardNativeArrayWithObject, item)
	if items == 0 {
		return errors.New("无法准备剪贴板条目")
	}

	count := int64(board.Send(clipboardNativeChangeCount))
	current, err := nativeClipboardPNGBytes(board)
	if err != nil || nativeClipboardRevision(count, current) != revision {
		return errClipboardChanged
	}
	// AppKit appends with writeObjects: and has no atomic conditional replacement.
	// Check the ownership count as the last operation before taking ownership.
	if int64(board.Send(clipboardNativeChangeCount)) != count {
		return errClipboardChanged
	}
	clearedCount := int64(board.Send(clipboardNativeClearContents))
	if clearedCount == count {
		return errors.New("无法清空剪贴板")
	}
	if int64(board.Send(clipboardNativeChangeCount)) != clearedCount {
		return errClipboardChanged
	}
	if !objc.Send[bool](board, clipboardNativeWriteObjects, items) {
		return errors.New("无法写入剪贴板 PNG 图片")
	}
	return nil
}

func nativeClipboardData(data []byte) objc.ID {
	nativeData := objc.ID(objc.GetClass("NSData")).Send(clipboardNativeDataWithBytes, unsafe.SliceData(data), uintptr(len(data)))
	runtime.KeepAlive(data)
	return nativeData
}
