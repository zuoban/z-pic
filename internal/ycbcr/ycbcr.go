// Package ycbcr provides fast YCbCr pixel conversion helpers shared by image
// encoders and visual diff utilities.
package ycbcr

import (
	"bufio"
	"context"
	"image"
	"image/color"
)

// WriteRGBScanlines writes 8-bit RGB scanlines for bounds of src to w using
// rowBuf (which must have length >= bounds.Dx()*3), stopping if ctx is canceled.
func WriteRGBScanlines(ctx context.Context, w *bufio.Writer, src *image.YCbCr, bounds image.Rectangle, rowBuf []byte) error {
	width := bounds.Dx()
	minX := bounds.Min.X
	rectMinX, rectMinY := src.Rect.Min.X, src.Rect.Min.Y
	rowBuf = rowBuf[:width*3]
	switch src.SubsampleRatio {
	case image.YCbCrSubsampleRatio444:
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			yRow := (y-rectMinY)*src.YStride + (minX - rectMinX)
			cRow := (y-rectMinY)*src.CStride + (minX - rectMinX)
			for x := range width {
				r, g, b := color.YCbCrToRGB(src.Y[yRow+x], src.Cb[cRow+x], src.Cr[cRow+x])
				rowBuf[x*3] = r
				rowBuf[x*3+1] = g
				rowBuf[x*3+2] = b
			}
			if _, err := w.Write(rowBuf); err != nil {
				return err
			}
		}
	case image.YCbCrSubsampleRatio422:
		xBias := rectMinX / 2
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			yRow := (y-rectMinY)*src.YStride + (minX - rectMinX)
			cRow := (y - rectMinY) * src.CStride
			for x := range width {
				ci := cRow + (minX+x)/2 - xBias
				r, g, b := color.YCbCrToRGB(src.Y[yRow+x], src.Cb[ci], src.Cr[ci])
				rowBuf[x*3] = r
				rowBuf[x*3+1] = g
				rowBuf[x*3+2] = b
			}
			if _, err := w.Write(rowBuf); err != nil {
				return err
			}
		}
	case image.YCbCrSubsampleRatio420:
		xBias := rectMinX / 2
		yBias := rectMinY / 2
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			yRow := (y-rectMinY)*src.YStride + (minX - rectMinX)
			cRow := (y/2 - yBias) * src.CStride
			for x := range width {
				ci := cRow + (minX+x)/2 - xBias
				r, g, b := color.YCbCrToRGB(src.Y[yRow+x], src.Cb[ci], src.Cr[ci])
				rowBuf[x*3] = r
				rowBuf[x*3+1] = g
				rowBuf[x*3+2] = b
			}
			if _, err := w.Write(rowBuf); err != nil {
				return err
			}
		}
	case image.YCbCrSubsampleRatio440:
		yBias := rectMinY / 2
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			yRow := (y-rectMinY)*src.YStride + (minX - rectMinX)
			cRow := (y/2-yBias)*src.CStride + (minX - rectMinX)
			for x := range width {
				r, g, b := color.YCbCrToRGB(src.Y[yRow+x], src.Cb[cRow+x], src.Cr[cRow+x])
				rowBuf[x*3] = r
				rowBuf[x*3+1] = g
				rowBuf[x*3+2] = b
			}
			if _, err := w.Write(rowBuf); err != nil {
				return err
			}
		}
	default:
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			yi := src.YOffset(minX, y)
			for x := range width {
				ci := src.COffset(minX+x, y)
				r, g, b := color.YCbCrToRGB(src.Y[yi+x], src.Cb[ci], src.Cr[ci])
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

// ToNRGBAInto converts bounds of src into dst starting at dst.Rect.Min.
// dst must be at least bounds.Dx() by bounds.Dy().
func ToNRGBAInto(dst *image.NRGBA, src *image.YCbCr, bounds image.Rectangle) {
	w, h := bounds.Dx(), bounds.Dy()
	minX, minY := bounds.Min.X, bounds.Min.Y
	rectMinX, rectMinY := src.Rect.Min.X, src.Rect.Min.Y
	dstMinX, dstMinY := dst.Rect.Min.X, dst.Rect.Min.Y
	switch src.SubsampleRatio {
	case image.YCbCrSubsampleRatio444:
		for y := range h {
			sy := minY + y
			yRow := (sy-rectMinY)*src.YStride + (minX - rectMinX)
			cRow := (sy-rectMinY)*src.CStride + (minX - rectMinX)
			dOff := dst.PixOffset(dstMinX, dstMinY+y)
			for x := range w {
				r, g, bl := color.YCbCrToRGB(src.Y[yRow+x], src.Cb[cRow+x], src.Cr[cRow+x])
				dst.Pix[dOff] = r
				dst.Pix[dOff+1] = g
				dst.Pix[dOff+2] = bl
				dst.Pix[dOff+3] = 0xff
				dOff += 4
			}
		}
	case image.YCbCrSubsampleRatio422:
		xBias := rectMinX / 2
		for y := range h {
			sy := minY + y
			yRow := (sy-rectMinY)*src.YStride + (minX - rectMinX)
			cRow := (sy - rectMinY) * src.CStride
			dOff := dst.PixOffset(dstMinX, dstMinY+y)
			for x := range w {
				ci := cRow + (minX+x)/2 - xBias
				r, g, bl := color.YCbCrToRGB(src.Y[yRow+x], src.Cb[ci], src.Cr[ci])
				dst.Pix[dOff] = r
				dst.Pix[dOff+1] = g
				dst.Pix[dOff+2] = bl
				dst.Pix[dOff+3] = 0xff
				dOff += 4
			}
		}
	case image.YCbCrSubsampleRatio420:
		xBias := rectMinX / 2
		yBias := rectMinY / 2
		for y := range h {
			sy := minY + y
			yRow := (sy-rectMinY)*src.YStride + (minX - rectMinX)
			cRow := (sy/2 - yBias) * src.CStride
			dOff := dst.PixOffset(dstMinX, dstMinY+y)
			for x := range w {
				ci := cRow + (minX+x)/2 - xBias
				r, g, bl := color.YCbCrToRGB(src.Y[yRow+x], src.Cb[ci], src.Cr[ci])
				dst.Pix[dOff] = r
				dst.Pix[dOff+1] = g
				dst.Pix[dOff+2] = bl
				dst.Pix[dOff+3] = 0xff
				dOff += 4
			}
		}
	case image.YCbCrSubsampleRatio440:
		yBias := rectMinY / 2
		for y := range h {
			sy := minY + y
			yRow := (sy-rectMinY)*src.YStride + (minX - rectMinX)
			cRow := (sy/2-yBias)*src.CStride + (minX - rectMinX)
			dOff := dst.PixOffset(dstMinX, dstMinY+y)
			for x := range w {
				r, g, bl := color.YCbCrToRGB(src.Y[yRow+x], src.Cb[cRow+x], src.Cr[cRow+x])
				dst.Pix[dOff] = r
				dst.Pix[dOff+1] = g
				dst.Pix[dOff+2] = bl
				dst.Pix[dOff+3] = 0xff
				dOff += 4
			}
		}
	default:
		for y := range h {
			sy := minY + y
			yRow := src.YOffset(minX, sy)
			dOff := dst.PixOffset(dstMinX, dstMinY+y)
			for x := range w {
				ci := src.COffset(minX+x, sy)
				r, g, bl := color.YCbCrToRGB(src.Y[yRow+x], src.Cb[ci], src.Cr[ci])
				dst.Pix[dOff] = r
				dst.Pix[dOff+1] = g
				dst.Pix[dOff+2] = bl
				dst.Pix[dOff+3] = 0xff
				dOff += 4
			}
		}
	}
}
