package compress

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"io"

	"golang.org/x/image/vp8"
	"golang.org/x/image/vp8l"

	"zpic/internal/contextio"
)

func ValidateWebP(ctx context.Context, data []byte) error {
	return validateWebPContainer(ctx, bytes.NewReader(data), int64(len(data)))
}

func validateWebPContainer(ctx context.Context, reader io.ReadSeeker, size int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if size < 20 || size > maxInputSize {
		return fmt.Errorf("WebP 文件为空、损坏或超过 64 MiB 限制")
	}
	if _, err := reader.Seek(0, io.SeekStart); err != nil {
		return err
	}
	var header [12]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return fmt.Errorf("无法读取 WebP 头部：%w", err)
	}
	if string(header[:4]) != "RIFF" || string(header[8:]) != "WEBP" ||
		int64(binary.LittleEndian.Uint32(header[4:8]))+8 != size {
		return fmt.Errorf("WebP 容器头部或文件长度无效")
	}
	foundImage, foundExtended := false, false
	var canvasWidth, canvasHeight int
	for offset := int64(12); offset < size; {
		if err := ctx.Err(); err != nil {
			return err
		}
		if size-offset < 8 {
			return fmt.Errorf("WebP 数据块头部不完整")
		}
		if _, err := reader.Seek(offset, io.SeekStart); err != nil {
			return err
		}
		var chunk [8]byte
		if _, err := io.ReadFull(reader, chunk[:]); err != nil {
			return fmt.Errorf("无法读取 WebP 数据块：%w", err)
		}
		length := int64(binary.LittleEndian.Uint32(chunk[4:]))
		padded := length + length%2
		if padded > size-offset-8 {
			return fmt.Errorf("WebP 数据块长度无效")
		}
		switch string(chunk[:4]) {
		case "ANIM", "ANMF":
			return fmt.Errorf("不支持动画 WebP，仅支持静态 JPEG/PNG/WebP")
		case "VP8X":
			if foundExtended || offset != 12 || length != 10 {
				return fmt.Errorf("WebP 扩展头部无效")
			}
			foundExtended = true
			var extended [10]byte
			if _, err := io.ReadFull(reader, extended[:]); err != nil {
				return err
			}
			if extended[0]&2 != 0 {
				return fmt.Errorf("不支持动画 WebP，仅支持静态 JPEG/PNG/WebP")
			}
			canvasWidth = 1 + int(extended[4]) + int(extended[5])<<8 + int(extended[6])<<16
			canvasHeight = 1 + int(extended[7]) + int(extended[8])<<8 + int(extended[9])<<16
		case "VP8 ", "VP8L":
			if foundImage || length == 0 {
				return fmt.Errorf("WebP 图片数据无效")
			}
			// DecodeConfig stops at VP8X. Cross-check the actual frame header
			// before any decoder or native preview allocates pixel buffers.
			width, height, err := webPFrameSize(reader, string(chunk[:4]), length)
			if err != nil {
				return fmt.Errorf("WebP 图片头部损坏：%w", err)
			}
			if width <= 0 || height <= 0 || uint64(width) > uint64(maxPixels)/uint64(height) {
				return fmt.Errorf("WebP 图片超过 3200 万像素限制或尺寸无效")
			}
			if foundExtended && (width != canvasWidth || height != canvasHeight) {
				return fmt.Errorf("WebP 画布与图片尺寸不一致")
			}
			foundImage = true
		}
		offset += 8 + padded
	}
	if !foundImage {
		return fmt.Errorf("WebP 缺少静态图片数据")
	}
	return ctx.Err()
}

func webPFrameSize(reader io.Reader, kind string, length int64) (int, int, error) {
	frame := io.LimitReader(reader, length)
	if kind == "VP8L" {
		config, err := vp8l.DecodeConfig(frame)
		return config.Width, config.Height, err
	}
	decoder := vp8.NewDecoder()
	decoder.Init(frame, int(length))
	header, err := decoder.DecodeFrameHeader()
	return header.Width, header.Height, err
}

func validateWebPOutput(ctx context.Context, data []byte, input image.Config) error {
	_, err := decodeWebPOutput(ctx, data, input)
	return err
}

func decodeWebPOutput(ctx context.Context, data []byte, input image.Config) (image.Image, error) {
	if err := ValidateWebP(ctx, data); err != nil {
		return nil, err
	}
	config, format, err := image.DecodeConfig(contextio.NewReader(ctx, bytes.NewReader(data)))
	if err != nil {
		return nil, fmt.Errorf("无法读取 WebP 头部：%w", err)
	}
	if format != "webp" || config.Width != input.Width || config.Height != input.Height {
		return nil, fmt.Errorf("输出格式或图片尺寸发生了变化")
	}
	decoded, _, err := image.Decode(contextio.NewReader(ctx, bytes.NewReader(data)))
	if err != nil {
		return nil, fmt.Errorf("WebP 数据损坏：%w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return decoded, nil
}
