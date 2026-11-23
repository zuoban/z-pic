package jpegtools

import (
	"bufio"
	"context"
	"fmt"
	"image"
	"image/color"
	"io"

	"zpic/internal/ycbcr"
)

// EncodeImage encodes any decoded Go image to a MozJPEG progressive/trellis JPEG
// via cjpeg's P5 (PGM grayscale) or P6 (PPM RGB) stdin support.
func EncodeImage(ctx context.Context, img image.Image, options EncodeOptions) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("JPEG 编码已取消：%w", err)
	}
	if img == nil {
		return nil, fmt.Errorf("JPEG 输入图片不能为空")
	}
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("JPEG 输入图片尺寸无效")
	}
	if uint64(width) > uint64(maxJPEGPixels)/uint64(height) {
		return nil, fmt.Errorf("JPEG 图片超过 3200 万像素限制")
	}
	if options.MaxOutputBytes > 0 && options.MaxOutputBytes <= 2 {
		return nil, ErrNoSavings
	}
	_, grayscale := img.(*image.Gray)
	args, err := buildEncodeArgs(options, grayscale)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, toolTimeout)
	defer cancel()
	path, cleanup, err := acquireTool("cjpeg")
	if err != nil {
		return nil, err
	}
	defer cleanup()

	pr, pw := io.Pipe()
	writeErrCh := make(chan error, 1)
	go func() {
		err := writePNM(ctx, pw, img, grayscale)
		_ = pw.CloseWithError(err)
		writeErrCh <- err
	}()

	out, runErr := runCommandWithReader(ctx, "cjpeg", path, args, pr, maxJPEGBytes, options.MaxOutputBytes)
	_ = pr.Close()
	writeErr := <-writeErrCh
	if ctx.Err() != nil {
		return nil, fmt.Errorf("cjpeg 已取消或超时：%w", ctx.Err())
	}
	if runErr != nil {
		return nil, runErr
	}
	if writeErr != nil {
		return nil, fmt.Errorf("写入 JPEG 像素流失败：%w", writeErr)
	}
	return out, nil
}

func writePNM(ctx context.Context, w io.Writer, img image.Image, grayscale bool) error {
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	bw := bufio.NewWriterSize(w, 64<<10)
	if grayscale {
		if _, err := fmt.Fprintf(bw, "P5\n%d %d\n255\n", width, height); err != nil {
			return err
		}
		if err := writeGrayScanlines(ctx, bw, img.(*image.Gray), bounds); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintf(bw, "P6\n%d %d\n255\n", width, height); err != nil {
			return err
		}
		if err := writeRGBScanlines(ctx, bw, img, bounds); err != nil {
			return err
		}
	}
	return bw.Flush()
}

func writeGrayScanlines(ctx context.Context, w *bufio.Writer, src *image.Gray, bounds image.Rectangle) error {
	width := bounds.Dx()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		off := src.PixOffset(bounds.Min.X, y)
		if _, err := w.Write(src.Pix[off : off+width]); err != nil {
			return err
		}
	}
	return nil
}

func writeRGBScanlines(ctx context.Context, w *bufio.Writer, img image.Image, bounds image.Rectangle) error {
	width := bounds.Dx()
	rowBuf := make([]byte, width*3)
	switch src := img.(type) {
	case *image.YCbCr:
		return ycbcr.WriteRGBScanlines(ctx, w, src, bounds, rowBuf)
	case *image.RGBA:
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			off := src.PixOffset(bounds.Min.X, y)
			for x := range width {
				rowBuf[x*3] = src.Pix[off]
				rowBuf[x*3+1] = src.Pix[off+1]
				rowBuf[x*3+2] = src.Pix[off+2]
				off += 4
			}
			if _, err := w.Write(rowBuf); err != nil {
				return err
			}
		}
	case *image.NRGBA:
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			off := src.PixOffset(bounds.Min.X, y)
			for x := range width {
				rowBuf[x*3] = src.Pix[off]
				rowBuf[x*3+1] = src.Pix[off+1]
				rowBuf[x*3+2] = src.Pix[off+2]
				off += 4
			}
			if _, err := w.Write(rowBuf); err != nil {
				return err
			}
		}
	case *image.Paletted:
		var lut [256][3]byte
		for i, c := range src.Palette {
			if i >= len(lut) {
				break
			}
			if c == nil {
				continue
			}
			r, g, b, _ := c.RGBA()
			lut[i] = [3]byte{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8)}
		}
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			off := src.PixOffset(bounds.Min.X, y)
			for x := range width {
				rgb := lut[src.Pix[off+x]]
				rowBuf[x*3] = rgb[0]
				rowBuf[x*3+1] = rgb[1]
				rowBuf[x*3+2] = rgb[2]
			}
			if _, err := w.Write(rowBuf); err != nil {
				return err
			}
		}
	case *image.CMYK:
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			off := src.PixOffset(bounds.Min.X, y)
			for x := range width {
				r, g, b := color.CMYKToRGB(src.Pix[off], src.Pix[off+1], src.Pix[off+2], src.Pix[off+3])
				rowBuf[x*3] = r
				rowBuf[x*3+1] = g
				rowBuf[x*3+2] = b
				off += 4
			}
			if _, err := w.Write(rowBuf); err != nil {
				return err
			}
		}
	default:
		rgba64Img, hasRGBA64 := img.(image.RGBA64Image)
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			for x := range width {
				var r, g, b uint8
				if hasRGBA64 {
					px := rgba64Img.RGBA64At(bounds.Min.X+x, y)
					r, g, b = uint8(px.R>>8), uint8(px.G>>8), uint8(px.B>>8)
				} else {
					rr, gg, bb, _ := img.At(bounds.Min.X+x, y).RGBA()
					r, g, b = uint8(rr>>8), uint8(gg>>8), uint8(bb>>8)
				}
				rowBuf[x*3] = r
				rowBuf[x*3+1] = g
				rowBuf[x*3+2] = b
			}
			if _, err := w.Write(rowBuf); err != nil {
				return err
			}
		}
	}
	return nil
}
