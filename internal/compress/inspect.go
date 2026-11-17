package compress

import (
	"context"
	"fmt"
	"image"
	"io"
	"os"
)

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
		if err := validatePNGContainer(ctx, f, opened.Size()); err != nil {
			return ImageInfo{}, err
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
