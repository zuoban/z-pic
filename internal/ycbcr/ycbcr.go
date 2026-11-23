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

// FillRGBRow converts a single row (0-indexed relative to bounds.Min.Y) of src
// into 8-bit RGB triplets in dst (which must have length >= bounds.Dx()*3).
func FillRGBRow(dst []byte, src *image.YCbCr, bounds image.Rectangle, row int) {
	width := bounds.Dx()
	if width <= 0 {
		return
	}
	dst = dst[:width*3]
	minX := bounds.Min.X
	sy := bounds.Min.Y + row
	rectMinX, rectMinY := src.Rect.Min.X, src.Rect.Min.Y
	yRow := (sy-rectMinY)*src.YStride + (minX - rectMinX)
	switch src.SubsampleRatio {
	case image.YCbCrSubsampleRatio420:
		cRow := (sy/2 - rectMinY/2) * src.CStride
		if minX >= 0 && rectMinX >= 0 && minX%2 == 0 {
			cBase := cRow + minX/2 - rectMinX/2
			yPix := src.Y[yRow : yRow+width]
			cbRow := src.Cb[cBase:]
			crRow := src.Cr[cBase:]
			x := 0
			for ; x+1 < width; x += 2 {
				ci := x >> 1
				cb, cr := cbRow[ci], crRow[ci]
				r0, g0, b0 := color.YCbCrToRGB(yPix[x], cb, cr)
				r1, g1, b1 := color.YCbCrToRGB(yPix[x+1], cb, cr)
				p := dst[x*3 : x*3+6 : x*3+6]
				p[0], p[1], p[2] = r0, g0, b0
				p[3], p[4], p[5] = r1, g1, b1
			}
			if x < width {
				ci := x >> 1
				r0, g0, b0 := color.YCbCrToRGB(yPix[x], cbRow[ci], crRow[ci])
				p := dst[x*3 : x*3+3 : x*3+3]
				p[0], p[1], p[2] = r0, g0, b0
			}
			return
		}
		xBias := rectMinX / 2
		for x := range width {
			ci := cRow + (minX+x)/2 - xBias
			r, g, b := color.YCbCrToRGB(src.Y[yRow+x], src.Cb[ci], src.Cr[ci])
			dst[x*3] = r
			dst[x*3+1] = g
			dst[x*3+2] = b
		}
	case image.YCbCrSubsampleRatio444:
		cRow := (sy-rectMinY)*src.CStride + (minX - rectMinX)
		for x := range width {
			r, g, b := color.YCbCrToRGB(src.Y[yRow+x], src.Cb[cRow+x], src.Cr[cRow+x])
			dst[x*3] = r
			dst[x*3+1] = g
			dst[x*3+2] = b
		}
	case image.YCbCrSubsampleRatio422:
		cRow := (sy - rectMinY) * src.CStride
		xBias := rectMinX / 2
		for x := range width {
			ci := cRow + (minX+x)/2 - xBias
			r, g, b := color.YCbCrToRGB(src.Y[yRow+x], src.Cb[ci], src.Cr[ci])
			dst[x*3] = r
			dst[x*3+1] = g
			dst[x*3+2] = b
		}
	case image.YCbCrSubsampleRatio440:
		cRow := (sy/2-rectMinY/2)*src.CStride + (minX - rectMinX)
		for x := range width {
			r, g, b := color.YCbCrToRGB(src.Y[yRow+x], src.Cb[cRow+x], src.Cr[cRow+x])
			dst[x*3] = r
			dst[x*3+1] = g
			dst[x*3+2] = b
		}
	default:
		for x := range width {
			ci := src.COffset(minX+x, sy)
			r, g, b := color.YCbCrToRGB(src.Y[yRow+x], src.Cb[ci], src.Cr[ci])
			dst[x*3] = r
			dst[x*3+1] = g
			dst[x*3+2] = b
		}
	}
}

// FillNYCbCrARow converts a single row (0-indexed relative to bounds.Min.Y) of
// src into 8-bit non-premultiplied RGBA bytes in dst (length >= bounds.Dx()*4).
// Fully transparent pixels (A == 0) are written as zeroed RGBA.
func FillNYCbCrARow(dst []byte, src *image.NYCbCrA, bounds image.Rectangle, row int) {
	width := bounds.Dx()
	if width <= 0 {
		return
	}
	dst = dst[:width*4]
	minX := bounds.Min.X
	sy := bounds.Min.Y + row
	rectMinX, rectMinY := src.Rect.Min.X, src.Rect.Min.Y
	yRow := (sy-rectMinY)*src.YStride + (minX - rectMinX)
	aRow := (sy-rectMinY)*src.AStride + (minX - rectMinX)
	switch src.SubsampleRatio {
	case image.YCbCrSubsampleRatio420:
		cRow := (sy/2 - rectMinY/2) * src.CStride
		xBias := rectMinX / 2
		for x := range width {
			a := src.A[aRow+x]
			p := dst[x*4 : x*4+4 : x*4+4]
			if a == 0 {
				p[0], p[1], p[2], p[3] = 0, 0, 0, 0
				continue
			}
			ci := cRow + (minX+x)/2 - xBias
			r, g, b := color.YCbCrToRGB(src.Y[yRow+x], src.Cb[ci], src.Cr[ci])
			p[0], p[1], p[2], p[3] = r, g, b, a
		}
	case image.YCbCrSubsampleRatio444:
		cRow := (sy-rectMinY)*src.CStride + (minX - rectMinX)
		for x := range width {
			a := src.A[aRow+x]
			p := dst[x*4 : x*4+4 : x*4+4]
			if a == 0 {
				p[0], p[1], p[2], p[3] = 0, 0, 0, 0
				continue
			}
			r, g, b := color.YCbCrToRGB(src.Y[yRow+x], src.Cb[cRow+x], src.Cr[cRow+x])
			p[0], p[1], p[2], p[3] = r, g, b, a
		}
	default:
		for x := range width {
			a := src.A[aRow+x]
			p := dst[x*4 : x*4+4 : x*4+4]
			if a == 0 {
				p[0], p[1], p[2], p[3] = 0, 0, 0, 0
				continue
			}
			ci := src.COffset(minX+x, sy)
			r, g, b := color.YCbCrToRGB(src.Y[yRow+x], src.Cb[ci], src.Cr[ci])
			p[0], p[1], p[2], p[3] = r, g, b, a
		}
	}
}

// NYCbCrAToNRGBAInto converts bounds of src into dst starting at dst.Rect.Min.
// dst must be at least bounds.Dx() by bounds.Dy().
func NYCbCrAToNRGBAInto(dst *image.NRGBA, src *image.NYCbCrA, bounds image.Rectangle) {
	w, h := bounds.Dx(), bounds.Dy()
	if w <= 0 || h <= 0 {
		return
	}
	dstMinX, dstMinY := dst.Rect.Min.X, dst.Rect.Min.Y
	for y := range h {
		dOff := dst.PixOffset(dstMinX, dstMinY+y)
		FillNYCbCrARow(dst.Pix[dOff:dOff+w*4], src, bounds, y)
	}
}
