package compress

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
)

func PNGBytes(ctx context.Context, data []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(data) > maxInputSize {
		return nil, fmt.Errorf("图片超过 64 MiB 限制")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("无法读取图片：%w", err)
	}
	if format != "jpeg" && format != "png" && format != "webp" {
		return nil, fmt.Errorf("仅支持 JPEG、PNG 和 WebP 图片")
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > maxPixels/config.Height {
		return nil, fmt.Errorf("图片超过 3200 万像素限制或尺寸无效")
	}
	if format == "png" {
		if err := ValidatePNG(ctx, data); err != nil {
			return nil, err
		}
	}
	if format == "webp" {
		if err := ValidateWebP(ctx, data); err != nil {
			return nil, err
		}
	}
	decoded, _, err := image.Decode(contextReader{ctx: ctx, reader: bytes.NewReader(data)})
	if err != nil {
		return nil, fmt.Errorf("无法解码图片：%w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if format == "png" {
		return data, nil
	}
	if format == "jpeg" {
		decoded = normalizeOrientation(decoded, jpegOrientation(data))
	}
	var output bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := encoder.Encode(&output, decoded); err != nil {
		return nil, fmt.Errorf("无法生成剪贴板图片：%w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
