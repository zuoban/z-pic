package webptools

import (
	"bufio"
	"context"
	"fmt"
	"image"
	"io"

	"zpic/internal/ycbcr"
)

const (
	maxWebPDimension = 16383
	maxWebPPixels    = 32_000_000
)

// EncodeImage encodes any decoded Go image to WebP via cwebp's PNM (P6 PPM / P7 PAM)
// stdin support, avoiding extra native decoder dependencies or full-frame intermediate copies.
func EncodeImage(ctx context.Context, img image.Image, options EncodeOptions) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("WebP 处理已取消：%w", err)
	}
	if img == nil {
		return nil, fmt.Errorf("WebP 输入图片不能为空")
	}
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("WebP 输入图片尺寸无效")
	}
	if width > maxWebPDimension || height > maxWebPDimension {
		return nil, fmt.Errorf("WebP 图片单边超过最大限制（%d 像素）", maxWebPDimension)
	}
	if uint64(width) > uint64(maxWebPPixels)/uint64(height) {
		return nil, fmt.Errorf("WebP 图片超过 3200 万像素限制")
	}
	args, err := buildEncodeArgs(options, false)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, toolTimeout)
	defer cancel()
	path, cleanup, err := acquireTool()
	if err != nil {
		return nil, err
	}
	defer cleanup()

	opaque := imageIsOpaque(img)
	pr, pw := io.Pipe()
	writeErrCh := make(chan error, 1)
	go func() {
		err := writePNM(ctx, pw, img, opaque)
		_ = pw.CloseWithError(err)
		writeErrCh <- err
	}()

	out, runErr := runCommandWithReader(ctx, path, args, pr, maxWebPBytes, nil)
	_ = pr.Close()
	writeErr := <-writeErrCh
	if ctx.Err() != nil {
		return nil, fmt.Errorf("cwebp 已取消或超时：%w", ctx.Err())
	}
	if runErr != nil {
		return nil, runErr
	}
	if writeErr != nil {
		return nil, fmt.Errorf("写入 WebP 像素流失败：%w", writeErr)
	}
	return out, nil
}

func imageIsOpaque(img image.Image) bool {
	if o, ok := img.(interface{ Opaque() bool }); ok {
		return o.Opaque()
	}
	switch img.(type) {
	case *image.YCbCr, *image.Gray, *image.Gray16, *image.CMYK:
		return true
	}
	return false
}

func writePNM(ctx context.Context, w io.Writer, img image.Image, opaque bool) error {
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	bw := bufio.NewWriterSize(w, 64<<10)
	if opaque {
		if _, err := fmt.Fprintf(bw, "P6\n%d %d\n255\n", width, height); err != nil {
			return err
		}
		if err := writeRGBScanlines(ctx, bw, img, bounds); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintf(bw, "P7\nWIDTH %d\nHEIGHT %d\nDEPTH 4\nMAXVAL 255\nTUPLTYPE RGB_ALPHA\nENDHDR\n", width, height); err != nil {
			return err
		}
		if err := writeRGBAScanlines(ctx, bw, img, bounds); err != nil {
			return err
		}
	}
	return bw.Flush()
}

func writeRGBScanlines(ctx context.Context, w *bufio.Writer, img image.Image, bounds image.Rectangle) error {
	width := bounds.Dx()
	rowBuf := make([]byte, width*3)
	switch src := img.(type) {
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
	case *image.YCbCr:
		return ycbcr.WriteRGBScanlines(ctx, w, src, bounds, rowBuf)
	case *image.Gray:
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			off := src.PixOffset(bounds.Min.X, y)
			for x := range width {
				v := src.Pix[off+x]
				rowBuf[x*3] = v
				rowBuf[x*3+1] = v
				rowBuf[x*3+2] = v
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

func writeRGBAScanlines(ctx context.Context, w *bufio.Writer, img image.Image, bounds image.Rectangle) error {
	width := bounds.Dx()
	rowBuf := make([]byte, width*4)
	switch src := img.(type) {
	case *image.NRGBA:
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			off := src.PixOffset(bounds.Min.X, y)
			if _, err := w.Write(src.Pix[off : off+width*4]); err != nil {
				return err
			}
		}
	case *image.RGBA:
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			off := src.PixOffset(bounds.Min.X, y)
			for x := range width {
				r, g, b, a := src.Pix[off], src.Pix[off+1], src.Pix[off+2], src.Pix[off+3]
				off += 4
				switch a {
				case 0:
					rowBuf[x*4], rowBuf[x*4+1], rowBuf[x*4+2], rowBuf[x*4+3] = 0, 0, 0, 0
				case 255:
					rowBuf[x*4], rowBuf[x*4+1], rowBuf[x*4+2], rowBuf[x*4+3] = r, g, b, 255
				default:
					aa := uint32(a)
					rowBuf[x*4] = uint8(min(255, (uint32(r)*255+aa/2)/aa))
					rowBuf[x*4+1] = uint8(min(255, (uint32(g)*255+aa/2)/aa))
					rowBuf[x*4+2] = uint8(min(255, (uint32(b)*255+aa/2)/aa))
					rowBuf[x*4+3] = a
				}
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
				var rr, gg, bb, aa uint32
				if hasRGBA64 {
					px := rgba64Img.RGBA64At(bounds.Min.X+x, y)
					rr, gg, bb, aa = uint32(px.R), uint32(px.G), uint32(px.B), uint32(px.A)
				} else {
					rr, gg, bb, aa = img.At(bounds.Min.X+x, y).RGBA()
				}
				switch aa {
				case 0:
					rowBuf[x*4], rowBuf[x*4+1], rowBuf[x*4+2], rowBuf[x*4+3] = 0, 0, 0, 0
				case 0xffff:
					rowBuf[x*4] = uint8(rr >> 8)
					rowBuf[x*4+1] = uint8(gg >> 8)
					rowBuf[x*4+2] = uint8(bb >> 8)
					rowBuf[x*4+3] = 255
				default:
					rowBuf[x*4] = uint8(((rr * 0xffff) / aa) >> 8)
					rowBuf[x*4+1] = uint8(((gg * 0xffff) / aa) >> 8)
					rowBuf[x*4+2] = uint8(((bb * 0xffff) / aa) >> 8)
					rowBuf[x*4+3] = uint8(aa >> 8)
				}
			}
			if _, err := w.Write(rowBuf); err != nil {
				return err
			}
		}
	}
	return nil
}
