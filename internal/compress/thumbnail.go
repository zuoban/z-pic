package compress

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"math"

	"zpic/internal/contextio"
	"zpic/internal/imageinput"
)

// Thumbnail decodes a static JPEG, PNG or WebP into an independent raster, fitting
// its display orientation within maxEdge without upscaling.
func Thumbnail(ctx context.Context, path string, maxEdge int) (image.Image, error) {
	return ThumbnailWithAcquire(ctx, path, maxEdge, nil)
}

// ThumbnailWithAcquire inspects headers once, acquires caller memory budget
// before reading full payload bytes, and decodes the thumbnail from the same file.
func ThumbnailWithAcquire(ctx context.Context, path string, maxEdge int, acquire func(ImageInfo) (func(), error)) (image.Image, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if maxEdge <= 0 {
		return nil, fmt.Errorf("缩略图尺寸必须大于零")
	}
	if path == "" {
		return nil, fmt.Errorf("图片路径不能为空")
	}
	var data []byte
	if acquire == nil {
		var err error
		data, err = readInput(ctx, path)
		if err != nil {
			return nil, err
		}
	} else {
		f, info, err := openAndInspect(ctx, path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		release, err := acquire(info)
		if err != nil {
			return nil, err
		}
		defer release()
		data, err = imageinput.ReadFile(ctx, f, info.Size)
		_ = f.Close()
		if err != nil {
			return nil, err
		}
	}
	config, format, err := inspectBytes(ctx, data)
	if err != nil {
		return nil, err
	}
	_ = config
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	decoded, _, err := image.Decode(contextio.NewReader(ctx, bytes.NewReader(data)))
	if cancelErr := ctx.Err(); cancelErr != nil {
		return nil, cancelErr
	}
	if err != nil {
		return nil, fmt.Errorf("图片数据损坏或无法解码：%w", err)
	}
	switch format {
	case "jpeg":
		decoded = normalizeOrientation(decoded, jpegOrientation(data))
	case "webp":
		decoded = normalizeOrientation(decoded, webpOrientation(data))
	}
	output, err := thumbnailRaster(ctx, decoded, maxEdge)
	if err != nil {
		return nil, err
	}
	return output, nil
}

func inspectBytes(ctx context.Context, data []byte) (image.Config, string, error) {
	format, width, height, err := inspectStream(ctx, bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return image.Config{}, "", err
	}
	return image.Config{Width: width, Height: height}, format, nil
}

type thumbnailSampleX struct {
	left      int
	right     int
	fractionX float64
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
	if width == bounds.Dx() && height == bounds.Dy() {
		if err := copyThumbnailRaster(ctx, output, source); err != nil {
			return nil, err
		}
		return output, nil
	}
	scaleX := float64(bounds.Dx()) / float64(width)
	scaleY := float64(bounds.Dy()) / float64(height)
	samplesX := make([]thumbnailSampleX, width)
	for column := 0; column < width; column++ {
		sourceX := max(0, (float64(column)+0.5)*scaleX-0.5)
		left := int(sourceX)
		right := min(left+1, bounds.Dx()-1)
		samplesX[column] = thumbnailSampleX{
			left:      bounds.Min.X + left,
			right:     bounds.Min.X + right,
			fractionX: sourceX - float64(left),
		}
	}
	rgbaSource, isRGBA := source.(*image.RGBA)
	nrgbaSource, isNRGBA := source.(*image.NRGBA)
	rgba64Source, hasRGBA64 := source.(image.RGBA64Image)
	for row := 0; row < height; row++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sourceY := max(0, (float64(row)+0.5)*scaleY-0.5)
		top := int(sourceY)
		bottom := min(top+1, bounds.Dy()-1)
		fractionY := sourceY - float64(top)
		topY := bounds.Min.Y + top
		bottomY := bounds.Min.Y + bottom
		if isRGBA {
			topRow := (topY - rgbaSource.Rect.Min.Y) * rgbaSource.Stride
			botRow := (bottomY - rgbaSource.Rect.Min.Y) * rgbaSource.Stride
			minX := rgbaSource.Rect.Min.X
			outRow := row * output.Stride
			for column := 0; column < width; column++ {
				if column%256 == 0 {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
				}
				sample := samplesX[column]
				lOff := (sample.left - minX) * 4
				rOff := (sample.right - minX) * 4
				p00 := rgbaSource.Pix[topRow+lOff : topRow+lOff+4 : topRow+lOff+4]
				p10 := rgbaSource.Pix[topRow+rOff : topRow+rOff+4 : topRow+rOff+4]
				p01 := rgbaSource.Pix[botRow+lOff : botRow+lOff+4 : botRow+lOff+4]
				p11 := rgbaSource.Pix[botRow+rOff : botRow+rOff+4 : botRow+rOff+4]
				out := output.Pix[outRow+column*4 : outRow+column*4+4 : outRow+column*4+4]
				out[0] = thumbnailChannel(uint32(p00[0])*0x101, uint32(p10[0])*0x101, uint32(p01[0])*0x101, uint32(p11[0])*0x101, sample.fractionX, fractionY)
				out[1] = thumbnailChannel(uint32(p00[1])*0x101, uint32(p10[1])*0x101, uint32(p01[1])*0x101, uint32(p11[1])*0x101, sample.fractionX, fractionY)
				out[2] = thumbnailChannel(uint32(p00[2])*0x101, uint32(p10[2])*0x101, uint32(p01[2])*0x101, uint32(p11[2])*0x101, sample.fractionX, fractionY)
				out[3] = thumbnailChannel(uint32(p00[3])*0x101, uint32(p10[3])*0x101, uint32(p01[3])*0x101, uint32(p11[3])*0x101, sample.fractionX, fractionY)
			}
			continue
		}
		if isNRGBA {
			topRow := (topY - nrgbaSource.Rect.Min.Y) * nrgbaSource.Stride
			botRow := (bottomY - nrgbaSource.Rect.Min.Y) * nrgbaSource.Stride
			minX := nrgbaSource.Rect.Min.X
			outRow := row * output.Stride
			for column := 0; column < width; column++ {
				if column%256 == 0 {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
				}
				sample := samplesX[column]
				lOff := (sample.left - minX) * 4
				rOff := (sample.right - minX) * 4
				r00, g00, b00, a00 := premultiplyNRGBA16(nrgbaSource.Pix[topRow+lOff : topRow+lOff+4 : topRow+lOff+4])
				r10, g10, b10, a10 := premultiplyNRGBA16(nrgbaSource.Pix[topRow+rOff : topRow+rOff+4 : topRow+rOff+4])
				r01, g01, b01, a01 := premultiplyNRGBA16(nrgbaSource.Pix[botRow+lOff : botRow+lOff+4 : botRow+lOff+4])
				r11, g11, b11, a11 := premultiplyNRGBA16(nrgbaSource.Pix[botRow+rOff : botRow+rOff+4 : botRow+rOff+4])
				out := output.Pix[outRow+column*4 : outRow+column*4+4 : outRow+column*4+4]
				out[0] = thumbnailChannel(r00, r10, r01, r11, sample.fractionX, fractionY)
				out[1] = thumbnailChannel(g00, g10, g01, g11, sample.fractionX, fractionY)
				out[2] = thumbnailChannel(b00, b10, b01, b11, sample.fractionX, fractionY)
				out[3] = thumbnailChannel(a00, a10, a01, a11, sample.fractionX, fractionY)
			}
			continue
		}
		if hasRGBA64 {
			for column := 0; column < width; column++ {
				if column%256 == 0 {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
				}
				sample := samplesX[column]
				pixel00 := rgba64Source.RGBA64At(sample.left, topY)
				pixel10 := rgba64Source.RGBA64At(sample.right, topY)
				pixel01 := rgba64Source.RGBA64At(sample.left, bottomY)
				pixel11 := rgba64Source.RGBA64At(sample.right, bottomY)
				offset := output.PixOffset(column, row)
				output.Pix[offset] = thumbnailChannel(uint32(pixel00.R), uint32(pixel10.R), uint32(pixel01.R), uint32(pixel11.R), sample.fractionX, fractionY)
				output.Pix[offset+1] = thumbnailChannel(uint32(pixel00.G), uint32(pixel10.G), uint32(pixel01.G), uint32(pixel11.G), sample.fractionX, fractionY)
				output.Pix[offset+2] = thumbnailChannel(uint32(pixel00.B), uint32(pixel10.B), uint32(pixel01.B), uint32(pixel11.B), sample.fractionX, fractionY)
				output.Pix[offset+3] = thumbnailChannel(uint32(pixel00.A), uint32(pixel10.A), uint32(pixel01.A), uint32(pixel11.A), sample.fractionX, fractionY)
			}
			continue
		}
		for column := 0; column < width; column++ {
			if column%256 == 0 {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
			}
			sample := samplesX[column]
			red00, green00, blue00, alpha00 := source.At(sample.left, topY).RGBA()
			red10, green10, blue10, alpha10 := source.At(sample.right, topY).RGBA()
			red01, green01, blue01, alpha01 := source.At(sample.left, bottomY).RGBA()
			red11, green11, blue11, alpha11 := source.At(sample.right, bottomY).RGBA()
			offset := output.PixOffset(column, row)
			output.Pix[offset] = thumbnailChannel(red00, red10, red01, red11, sample.fractionX, fractionY)
			output.Pix[offset+1] = thumbnailChannel(green00, green10, green01, green11, sample.fractionX, fractionY)
			output.Pix[offset+2] = thumbnailChannel(blue00, blue10, blue01, blue11, sample.fractionX, fractionY)
			output.Pix[offset+3] = thumbnailChannel(alpha00, alpha10, alpha01, alpha11, sample.fractionX, fractionY)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return output, nil
}

func premultiplyNRGBA16(p []byte) (uint32, uint32, uint32, uint32) {
	a := uint32(p[3])
	if a == 0xff {
		return uint32(p[0]) * 0x101, uint32(p[1]) * 0x101, uint32(p[2]) * 0x101, 0xffff
	}
	if a == 0 {
		return 0, 0, 0, 0
	}
	return (uint32(p[0]) * 0x101 * a) / 0xff,
		(uint32(p[1]) * 0x101 * a) / 0xff,
		(uint32(p[2]) * 0x101 * a) / 0xff,
		a * 0x101
}

// An unscaled thumbnail only needs one sample per pixel. Keep the same
// premultiplied, nearest-8-bit rounding as the bilinear path, including for
// 16-bit sources, and never retain the decoder's backing pixel buffer.
func copyThumbnailRaster(ctx context.Context, output *image.RGBA, source image.Image) error {
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if rgba, ok := source.(*image.RGBA); ok {
		for row := 0; row < height; row++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			srcStart := (bounds.Min.Y-rgba.Rect.Min.Y+row)*rgba.Stride + (bounds.Min.X-rgba.Rect.Min.X)*4
			dstStart := row * output.Stride
			copy(output.Pix[dstStart:dstStart+width*4], rgba.Pix[srcStart:srcStart+width*4])
		}
		return ctx.Err()
	}
	if nrgba, ok := source.(*image.NRGBA); ok {
		for row := 0; row < height; row++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			srcRow := (bounds.Min.Y-nrgba.Rect.Min.Y+row)*nrgba.Stride + (bounds.Min.X-nrgba.Rect.Min.X)*4
			dstRow := row * output.Stride
			for column := 0; column < width; column++ {
				if column%256 == 0 {
					if err := ctx.Err(); err != nil {
						return err
					}
				}
				p := nrgba.Pix[srcRow+column*4 : srcRow+column*4+4 : srcRow+column*4+4]
				out := output.Pix[dstRow+column*4 : dstRow+column*4+4 : dstRow+column*4+4]
				if p[3] == 0xff {
					out[0], out[1], out[2], out[3] = p[0], p[1], p[2], 0xff
				} else if p[3] == 0 {
					out[0], out[1], out[2], out[3] = 0, 0, 0, 0
				} else {
					r, g, b, a := premultiplyNRGBA16(p)
					out[0] = uint8((r + 128) / 257)
					out[1] = uint8((g + 128) / 257)
					out[2] = uint8((b + 128) / 257)
					out[3] = uint8((a + 128) / 257)
				}
			}
		}
		return ctx.Err()
	}
	rgba64Source, hasRGBA64 := source.(image.RGBA64Image)
	for row := 0; row < height; row++ {
		for column := 0; column < width; column++ {
			if column%256 == 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			var red, green, blue, alpha uint32
			if hasRGBA64 {
				pixel := rgba64Source.RGBA64At(bounds.Min.X+column, bounds.Min.Y+row)
				red, green, blue, alpha = uint32(pixel.R), uint32(pixel.G), uint32(pixel.B), uint32(pixel.A)
			} else {
				red, green, blue, alpha = source.At(bounds.Min.X+column, bounds.Min.Y+row).RGBA()
			}
			offset := row*output.Stride + column*4
			output.Pix[offset] = uint8((red + 128) / 257)
			output.Pix[offset+1] = uint8((green + 128) / 257)
			output.Pix[offset+2] = uint8((blue + 128) / 257)
			output.Pix[offset+3] = uint8((alpha + 128) / 257)
		}
	}
	return ctx.Err()
}

func thumbnailChannel(topLeft, topRight, bottomLeft, bottomRight uint32, fractionX, fractionY float64) uint8 {
	top := float64(topLeft)*(1-fractionX) + float64(topRight)*fractionX
	bottom := float64(bottomLeft)*(1-fractionX) + float64(bottomRight)*fractionX
	return uint8(math.Round((top*(1-fractionY) + bottom*fractionY) / 257))
}
