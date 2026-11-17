package compress

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"io"
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
			var flags [1]byte
			if _, err := io.ReadFull(reader, flags[:]); err != nil {
				return err
			}
			if flags[0]&2 != 0 {
				return fmt.Errorf("不支持动画 WebP，仅支持静态 JPEG/PNG/WebP")
			}
		case "VP8 ", "VP8L":
			if foundImage || length == 0 {
				return fmt.Errorf("WebP 图片数据无效")
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

func validateWebPOutput(ctx context.Context, data []byte, input image.Config) error {
	_, err := decodeWebPOutput(ctx, data, input)
	return err
}

func decodeWebPOutput(ctx context.Context, data []byte, input image.Config) (image.Image, error) {
	if err := ValidateWebP(ctx, data); err != nil {
		return nil, err
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("无法读取 WebP 头部：%w", err)
	}
	if format != "webp" || config.Width != input.Width || config.Height != input.Height {
		return nil, fmt.Errorf("输出格式或图片尺寸发生了变化")
	}
	decoded, _, err := image.Decode(contextReader{ctx: ctx, reader: bytes.NewReader(data)})
	if err != nil {
		return nil, fmt.Errorf("WebP 数据损坏：%w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return decoded, nil
}
