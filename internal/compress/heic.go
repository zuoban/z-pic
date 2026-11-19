package compress

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"io"

	"zpic/internal/contextio"
	"zpic/internal/heictools"
)

// SupportsHEIC reports whether HEIC input decoding is available on the current platform.
func SupportsHEIC() bool {
	return heictools.Available()
}

// SniffHEICHeader reports whether head begins with an ISOBMFF ftyp header.
func SniffHEICHeader(head []byte) bool {
	return heictools.SniffFTYP(head)
}

// ResolveOutputFormat returns the concrete container format produced for sourceFormat
// under the given output format and lossless settings.
func ResolveOutputFormat(sourceFormat string, outputFormat OutputFormat, lossless bool) string {
	if outputFormat == OutputFormatWebP {
		return "webp"
	}
	if sourceFormat == "heic" {
		if lossless {
			return "png"
		}
		return "jpeg"
	}
	return sourceFormat
}

// DecodePreviewImage decodes a static HEIC image into an upright raster for UI preview.
func DecodePreviewImage(ctx context.Context, data []byte) (image.Image, error) {
	pngBytes, err := heictools.DecodePNG(ctx, data)
	if err != nil {
		return nil, err
	}
	decoded, _, err := image.Decode(contextio.NewReader(ctx, bytes.NewReader(pngBytes)))
	if cancelErr := ctx.Err(); cancelErr != nil {
		return nil, cancelErr
	}
	if err != nil {
		return nil, fmt.Errorf("HEIC 图片数据损坏或无法解码：%w", err)
	}
	orient := pngOrientation(pngBytes)
	return normalizeOrientation(decoded, orient), nil
}

// VerifyImageData validates headers and full pixel decodability for any supported
// static image format (JPEG, PNG, WebP, or HEIC on macOS) in memory.
func VerifyImageData(ctx context.Context, data []byte) (string, error) {
	info, err := InspectBytes(ctx, data)
	if err != nil {
		return "", err
	}
	if info.Format == "heic" {
		pngBytes, err := heictools.DecodePNG(ctx, data)
		if err != nil {
			return "", err
		}
		if _, _, err := image.Decode(contextio.NewReader(ctx, bytes.NewReader(pngBytes))); err != nil {
			return "", fmt.Errorf("图片数据损坏：%w", err)
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		return "heic", nil
	}
	if _, _, err := image.Decode(contextio.NewReader(ctx, bytes.NewReader(data))); err != nil {
		return "", fmt.Errorf("图片数据损坏：%w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return info.Format, nil
}

// VerifyImageReader validates headers and full pixel decodability on an open
// seekable stream of known byte length.
func VerifyImageReader(ctx context.Context, r io.ReadSeeker, size int64) (ImageInfo, error) {
	info, err := InspectReader(ctx, r, size)
	if err != nil {
		return ImageInfo{}, err
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return ImageInfo{}, err
	}
	if info.Format == "heic" {
		buf := make([]byte, 0, size)
		dataBuf := bytes.NewBuffer(buf)
		n, err := io.Copy(dataBuf, io.LimitReader(contextio.NewReader(ctx, r), maxInputSize+1))
		if err != nil {
			return ImageInfo{}, err
		}
		if n > maxInputSize {
			return ImageInfo{}, fmt.Errorf("图片超过 64 MiB 限制")
		}
		pngBytes, err := heictools.DecodePNG(ctx, dataBuf.Bytes())
		if err != nil {
			return ImageInfo{}, err
		}
		if _, _, err := image.Decode(contextio.NewReader(ctx, bytes.NewReader(pngBytes))); err != nil {
			return ImageInfo{}, fmt.Errorf("图片数据损坏：%w", err)
		}
		if err := ctx.Err(); err != nil {
			return ImageInfo{}, err
		}
		return info, nil
	}
	if _, _, err := image.Decode(contextio.NewReader(ctx, r)); err != nil {
		return ImageInfo{}, fmt.Errorf("图片数据损坏：%w", err)
	}
	if err := ctx.Err(); err != nil {
		return ImageInfo{}, err
	}
	return info, nil
}

// pngOrientation reads the TIFF IFD0 Orientation tag from a PNG eXIf chunk,
// returning 1 when absent or malformed.
func pngOrientation(data []byte) int {
	if len(data) < len(pngSignature)+12 || [8]byte(data[:8]) != pngSignature {
		return 1
	}
	for offset := len(pngSignature); offset <= len(data)-12; {
		length := uint64(binary.BigEndian.Uint32(data[offset : offset+4]))
		if length > uint64(len(data)-offset-12) {
			return 1
		}
		kind := string(data[offset+4 : offset+8])
		if kind == "eXIf" {
			return exifPayloadOrientation(data[offset+8 : offset+8+int(length)])
		}
		if kind == "IEND" {
			return 1
		}
		offset += 12 + int(length)
	}
	return 1
}

// flattenAlphaForJPEG composites non-opaque images over white so transparent
// HEIC inputs produce clean JPEG output rather than black or uninitialized RGB.
func flattenAlphaForJPEG(ctx context.Context, img image.Image) (image.Image, error) {
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
	for y := range h {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sy := bounds.Min.Y + y
		rowBase := y * out.Stride
		for x := range w {
			r, g, b, a := img.At(bounds.Min.X+x, sy).RGBA()
			off := rowBase + x*4
			if a == 0xffff {
				out.Pix[off] = uint8(r >> 8)
				out.Pix[off+1] = uint8(g >> 8)
				out.Pix[off+2] = uint8(b >> 8)
				out.Pix[off+3] = 0xff
				continue
			}
			inv := 0xffff - a
			out.Pix[off] = uint8((r + inv) >> 8)
			out.Pix[off+1] = uint8((g + inv) >> 8)
			out.Pix[off+2] = uint8((b + inv) >> 8)
			out.Pix[off+3] = 0xff
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
