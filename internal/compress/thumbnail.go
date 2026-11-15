package compress

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"math"
)

// Thumbnail decodes a static JPEG, PNG or WebP into an independent raster, fitting
// its display orientation within maxEdge without upscaling.
func Thumbnail(ctx context.Context, path string, maxEdge int) (image.Image, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if maxEdge <= 0 {
		return nil, fmt.Errorf("缩略图尺寸必须大于零")
	}
	if path == "" {
		return nil, fmt.Errorf("图片路径不能为空")
	}
	data, err := readInput(ctx, path)
	if err != nil {
		return nil, err
	}
	config, format, err := image.DecodeConfig(contextReader{ctx: ctx, reader: bytes.NewReader(data)})
	if err != nil {
		return nil, fmt.Errorf("无法识别图片，仅支持静态 JPEG/PNG/WebP：%w", err)
	}
	if format != "jpeg" && format != "png" && format != "webp" {
		return nil, fmt.Errorf("不支持 %s 图片，仅支持静态 JPEG/PNG/WebP", format)
	}
	if config.Width <= 0 || config.Height <= 0 ||
		uint64(config.Width) > uint64(maxPixels)/uint64(config.Height) {
		return nil, fmt.Errorf("图片超过 3200 万像素限制或尺寸无效")
	}
	if format == "png" && animatedPNG(data) {
		return nil, fmt.Errorf("不支持动画 PNG，仅支持静态 JPEG/PNG/WebP")
	}
	if format == "webp" {
		if err := ValidateWebP(ctx, data); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	decoded, _, err := image.Decode(contextReader{ctx: ctx, reader: bytes.NewReader(data)})
	if cancelErr := ctx.Err(); cancelErr != nil {
		return nil, cancelErr
	}
	if err != nil {
		return nil, fmt.Errorf("图片数据损坏或无法解码：%w", err)
	}
	if format == "jpeg" {
		decoded = normalizeOrientation(decoded, jpegOrientation(data))
	}
	output, err := thumbnailRaster(ctx, decoded, maxEdge)
	if err != nil {
		return nil, err
	}
	return output, nil
}

func thumbnailRaster(ctx context.Context, source image.Image, maxEdge int) (*image.RGBA, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if longest := max(width, height); longest > maxEdge {
		width = max(1, int(int64(width)*int64(maxEdge)/int64(longest)))
		height = max(1, int(int64(height)*int64(maxEdge)/int64(longest)))
	}
	output := image.NewRGBA(image.Rect(0, 0, width, height))
	scaleX := float64(bounds.Dx()) / float64(width)
	scaleY := float64(bounds.Dy()) / float64(height)
	for y := 0; y < height; y++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sourceY := max(0, (float64(y)+0.5)*scaleY-0.5)
		top := int(sourceY)
		bottom := min(top+1, bounds.Dy()-1)
		fractionY := sourceY - float64(top)
		for x := 0; x < width; x++ {
			if x%256 == 0 {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
			}
			sourceX := max(0, (float64(x)+0.5)*scaleX-0.5)
			left := int(sourceX)
			right := min(left+1, bounds.Dx()-1)
			fractionX := sourceX - float64(left)
			red00, green00, blue00, alpha00 := source.At(bounds.Min.X+left, bounds.Min.Y+top).RGBA()
			red10, green10, blue10, alpha10 := source.At(bounds.Min.X+right, bounds.Min.Y+top).RGBA()
			red01, green01, blue01, alpha01 := source.At(bounds.Min.X+left, bounds.Min.Y+bottom).RGBA()
			red11, green11, blue11, alpha11 := source.At(bounds.Min.X+right, bounds.Min.Y+bottom).RGBA()
			offset := output.PixOffset(x, y)
			output.Pix[offset] = thumbnailChannel(red00, red10, red01, red11, fractionX, fractionY)
			output.Pix[offset+1] = thumbnailChannel(green00, green10, green01, green11, fractionX, fractionY)
			output.Pix[offset+2] = thumbnailChannel(blue00, blue10, blue01, blue11, fractionX, fractionY)
			output.Pix[offset+3] = thumbnailChannel(alpha00, alpha10, alpha01, alpha11, fractionX, fractionY)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return output, nil
}

func thumbnailChannel(topLeft, topRight, bottomLeft, bottomRight uint32, fractionX, fractionY float64) uint8 {
	top := float64(topLeft)*(1-fractionX) + float64(topRight)*fractionX
	bottom := float64(bottomLeft)*(1-fractionX) + float64(bottomRight)*fractionX
	return uint8(math.Round((top*(1-fractionY) + bottom*fractionY) / 257))
}
