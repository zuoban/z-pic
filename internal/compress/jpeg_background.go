package compress

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"strings"
)

var defaultJPEGBackground = color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}

// ParseJPEGBackground parses an optional background color used to flatten
// transparency when encoding JPEG. Empty or "white" resolves to #ffffff,
// "black" resolves to #000000, and custom values must use #RRGGBB hex syntax.
func ParseJPEGBackground(raw string) (color.RGBA, error) {
	trimmed := strings.TrimSpace(raw)
	switch {
	case trimmed == "" || strings.EqualFold(trimmed, "white"):
		return defaultJPEGBackground, nil
	case strings.EqualFold(trimmed, "black"):
		return color.RGBA{R: 0x00, G: 0x00, B: 0x00, A: 0xff}, nil
	case len(trimmed) == 7 && trimmed[0] == '#':
		r, okR := parseHexByte(trimmed[1], trimmed[2])
		g, okG := parseHexByte(trimmed[3], trimmed[4])
		b, okB := parseHexByte(trimmed[5], trimmed[6])
		if okR && okG && okB {
			return color.RGBA{R: r, G: g, B: b, A: 0xff}, nil
		}
	}
	return color.RGBA{}, fmt.Errorf("JPEG 背景色格式无效：%q，仅支持 white、black 或 #RRGGBB", raw)
}

// ValidateJPEGBackground reports whether raw is a valid JPEG background setting.
func ValidateJPEGBackground(raw string) error {
	_, err := ParseJPEGBackground(raw)
	return err
}

// NormalizeJPEGBackground canonicalizes a valid JPEG background setting:
// empty/white/#ffffff becomes "", black becomes "#000000", and custom #RRGGBB
// values are lowercased. Invalid inputs are returned trimmed unchanged.
func NormalizeJPEGBackground(raw string) string {
	bg, err := ParseJPEGBackground(raw)
	if err != nil {
		return strings.TrimSpace(raw)
	}
	if bg == defaultJPEGBackground {
		return ""
	}
	return fmt.Sprintf("#%02x%02x%02x", bg.R, bg.G, bg.B)
}

func parseHexByte(hi, lo byte) (uint8, bool) {
	h, okH := parseHexNibble(hi)
	l, okL := parseHexNibble(lo)
	if !okH || !okL {
		return 0, false
	}
	return h<<4 | l, true
}

func parseHexNibble(c byte) (uint8, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	default:
		return 0, false
	}
}

// flattenAlphaForJPEG composites non-opaque images over bg for JPEG output.
func flattenAlphaForJPEG(ctx context.Context, img image.Image, bg color.RGBA) (image.Image, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if o, ok := img.(interface{ Opaque() bool }); ok && o.Opaque() {
		return img, nil
	}
	switch img.(type) {
	case *image.YCbCr, *image.Gray, *image.Gray16, *image.CMYK:
		return img, nil
	}
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	bgR := uint32(bg.R) * 0x101
	bgG := uint32(bg.G) * 0x101
	bgB := uint32(bg.B) * 0x101
	for y := range h {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sy := bounds.Min.Y + y
		rowBase := y * out.Stride
		for x := range w {
			r, g, b, a := img.At(bounds.Min.X+x, sy).RGBA()
			off := rowBase + x*4
			// RGBA returns premultiplied channels. Clamp them to alpha so the
			// composite over bg cannot exceed 0xffff.
			a = min(a, 0xffff)
			invA := 0xffff - a
			out.Pix[off] = uint8((min(r, a) + (invA*bgR+0x7fff)/0xffff) >> 8)
			out.Pix[off+1] = uint8((min(g, a) + (invA*bgG+0x7fff)/0xffff) >> 8)
			out.Pix[off+2] = uint8((min(b, a) + (invA*bgB+0x7fff)/0xffff) >> 8)
			out.Pix[off+3] = 0xff
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
