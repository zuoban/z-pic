package compress

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image"
	"io"

	"zpic/internal/contextio"
)

var pngSignature = [8]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

// IsAnimatedPNG reports an acTL chunk before IEND at a PNG chunk boundary.
// It is a probe, not input validation; use ValidatePNG and decode for that.
func IsAnimatedPNG(data []byte) bool {
	return animatedPNG(data)
}

func animatedPNG(data []byte) bool {
	animated, _ := scanPNGChunks(context.Background(), bytes.NewReader(data), int64(len(data)))
	return animated
}

// ValidatePNG checks static PNG chunk framing through IEND without decoding
// pixels. Callers must still decode to verify CRCs and image data.
func ValidatePNG(ctx context.Context, data []byte) error {
	return validatePNGContainer(ctx, bytes.NewReader(data), int64(len(data)))
}

func validatePNGContainer(ctx context.Context, reader io.ReadSeeker, size int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if size > maxInputSize {
		return fmt.Errorf("PNG 文件超过 64 MiB 限制")
	}
	animated, err := scanPNGChunks(ctx, reader, size)
	if err != nil {
		return err
	}
	if animated {
		return fmt.Errorf("不支持动画 PNG，仅支持静态 JPEG/PNG/WebP")
	}
	return ctx.Err()
}

func scanPNGChunks(ctx context.Context, reader io.ReadSeeker, size int64) (bool, error) {
	if _, err := reader.Seek(0, io.SeekStart); err != nil {
		return false, fmt.Errorf("无法读取 PNG 头部：%w", err)
	}
	var signature [8]byte
	if _, err := io.ReadFull(reader, signature[:]); err != nil || signature != pngSignature {
		return false, fmt.Errorf("PNG 头部损坏")
	}
	var header [8]byte
	for offset := int64(len(signature)); offset <= size-12; {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if _, err := io.ReadFull(reader, header[:]); err != nil {
			return false, fmt.Errorf("PNG 数据块头部损坏：%w", err)
		}
		length := int64(binary.BigEndian.Uint32(header[:4]))
		if length > size-offset-12 {
			return false, fmt.Errorf("PNG 数据块长度无效")
		}
		switch string(header[4:]) {
		case "acTL":
			return true, nil
		case "IEND":
			if length != 0 {
				return false, fmt.Errorf("PNG IEND 数据块长度无效")
			}
			return false, nil
		}
		if _, err := reader.Seek(length+4, io.SeekCurrent); err != nil {
			return false, fmt.Errorf("无法读取 PNG 数据块：%w", err)
		}
		offset += 12 + length
	}
	return false, fmt.Errorf("PNG 数据损坏：缺少 IEND")
}

func validatePNGStructureAndCRCs(ctx context.Context, data []byte, input image.Config) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(data) == 0 || len(data) > maxInputSize {
		return fmt.Errorf("输出为空或超过 64 MiB 限制")
	}
	config, format, err := image.DecodeConfig(contextio.NewReader(ctx, bytes.NewReader(data)))
	if err != nil {
		return fmt.Errorf("无法读取 PNG 头部：%w", err)
	}
	if format != "png" || config.Width != input.Width || config.Height != input.Height {
		return fmt.Errorf("输出格式或图片尺寸发生了变化")
	}
	if len(data) < len(pngSignature)+12 || [8]byte(data[:8]) != pngSignature {
		return fmt.Errorf("PNG 头部损坏")
	}
	for offset := len(pngSignature); offset <= len(data)-12; {
		if err := ctx.Err(); err != nil {
			return err
		}
		length := int(binary.BigEndian.Uint32(data[offset : offset+4]))
		if length < 0 || length > len(data)-offset-12 {
			return fmt.Errorf("PNG 数据块长度无效")
		}
		chunkType := string(data[offset+4 : offset+8])
		if chunkType == "acTL" {
			return fmt.Errorf("不支持动画 PNG，仅支持静态 JPEG/PNG/WebP")
		}
		crcStart := offset + 8 + length
		wantCRC := binary.BigEndian.Uint32(data[crcStart : crcStart+4])
		if crc32.ChecksumIEEE(data[offset+4:crcStart]) != wantCRC {
			return fmt.Errorf("PNG 数据块校验和损坏（%s）", chunkType)
		}
		if chunkType == "IEND" {
			if length != 0 {
				return fmt.Errorf("PNG IEND 数据块长度无效")
			}
			return ctx.Err()
		}
		offset = crcStart + 4
	}
	return fmt.Errorf("PNG 数据损坏：缺少 IEND")
}
