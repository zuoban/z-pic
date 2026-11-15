package compress

import (
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"io"
	"os"
)

var pngSignature = [8]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

// ImageInfo describes validated static JPEG, PNG or WebP metadata read from headers.
type ImageInfo struct {
	Path   string
	Format string
	Width  int
	Height int
	Size   int64
}

// Inspect reads image headers from path without retaining file payloads or
// decoding pixel buffers in memory.
func Inspect(ctx context.Context, path string) (ImageInfo, error) {
	if err := ctx.Err(); err != nil {
		return ImageInfo{}, fmt.Errorf("读取图片已取消：%w", err)
	}
	if path == "" {
		return ImageInfo{}, fmt.Errorf("图片路径不能为空")
	}
	info, err := os.Stat(path)
	if err != nil {
		return ImageInfo{}, fmt.Errorf("无法打开图片：%w", err)
	}
	if !info.Mode().IsRegular() {
		return ImageInfo{}, fmt.Errorf("图片输入必须是普通文件")
	}
	f, err := os.Open(path)
	if err != nil {
		return ImageInfo{}, fmt.Errorf("无法打开图片：%w", err)
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return ImageInfo{}, fmt.Errorf("无法读取图片信息：%w", err)
	}
	if !opened.Mode().IsRegular() {
		return ImageInfo{}, fmt.Errorf("图片输入必须是普通文件")
	}
	if opened.Size() > maxInputSize {
		return ImageInfo{}, fmt.Errorf("图片超过 64 MiB 限制")
	}
	config, format, err := image.DecodeConfig(io.LimitReader(contextReader{ctx: ctx, reader: f}, maxInputSize))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ImageInfo{}, fmt.Errorf("读取图片已取消：%w", ctxErr)
		}
		return ImageInfo{}, fmt.Errorf("无法识别图片，仅支持静态 JPEG/PNG/WebP：%w", err)
	}
	if format != "jpeg" && format != "png" && format != "webp" {
		return ImageInfo{}, fmt.Errorf("不支持 %s 图片，仅支持静态 JPEG/PNG/WebP", format)
	}
	if config.Width <= 0 || config.Height <= 0 ||
		uint64(config.Width) > uint64(maxPixels)/uint64(config.Height) {
		return ImageInfo{}, fmt.Errorf("图片超过 3200 万像素限制或尺寸无效")
	}
	if format == "png" {
		animated, err := isAnimatedPNGFile(ctx, f, opened.Size())
		if err != nil {
			return ImageInfo{}, err
		}
		if animated {
			return ImageInfo{}, fmt.Errorf("不支持动画 PNG，仅支持静态 JPEG/PNG/WebP")
		}
	}
	if format == "webp" {
		if err := validateWebPContainer(ctx, f, opened.Size()); err != nil {
			return ImageInfo{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return ImageInfo{}, fmt.Errorf("读取图片已取消：%w", err)
	}
	return ImageInfo{
		Path:   path,
		Format: format,
		Width:  config.Width,
		Height: config.Height,
		Size:   opened.Size(),
	}, nil
}

func isAnimatedPNGFile(ctx context.Context, r io.ReadSeeker, fileSize int64) (bool, error) {
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return false, fmt.Errorf("无法读取 PNG 头部：%w", err)
	}
	var sig [8]byte
	if _, err := io.ReadFull(r, sig[:]); err != nil || sig != pngSignature {
		return false, nil
	}
	offset := int64(len(sig))
	var header [8]byte
	for offset <= fileSize-12 {
		if err := ctx.Err(); err != nil {
			return false, fmt.Errorf("读取图片已取消：%w", err)
		}
		if _, err := io.ReadFull(r, header[:]); err != nil {
			return false, nil
		}
		length := uint64(binary.BigEndian.Uint32(header[:4]))
		if length > uint64(fileSize-offset-12) {
			return false, nil
		}
		chunkType := string(header[4:8])
		if chunkType == "acTL" {
			return true, nil
		}
		if chunkType == "IDAT" || chunkType == "IEND" {
			return false, nil
		}
		skip := int64(length) + 4
		if _, err := r.Seek(skip, io.SeekCurrent); err != nil {
			return false, nil
		}
		offset += 12 + int64(length)
	}
	return false, nil
}
