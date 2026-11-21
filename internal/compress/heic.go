package compress

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"io"
	"strings"

	"zpic/internal/aviftools"
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

// ResolveOutputFormat returns an explicit output format choice as-is.
// With OutputFormatKeep it preserves the source format, except HEIC is exported
// as PNG in lossless mode and JPEG otherwise.
func ResolveOutputFormat(sourceFormat string, outputFormat OutputFormat, lossless bool) string {
	switch outputFormat {
	case OutputFormatJPEG, OutputFormatPNG, OutputFormatWebP, OutputFormatAVIF:
		return string(outputFormat)
	}
	if sourceFormat == "heic" {
		if lossless {
			return "png"
		}
		return "jpeg"
	}
	return sourceFormat
}

// DecodePreviewImage decodes a static HEIC or AVIF into an upright raster.
func DecodePreviewImage(ctx context.Context, data []byte) (image.Image, error) {
	pngBytes, format, err := decodeBMFFPNG(ctx, data, 0)
	if err != nil {
		return nil, err
	}
	decoded, _, err := image.Decode(contextio.NewReader(ctx, bytes.NewReader(pngBytes)))
	if cancelErr := ctx.Err(); cancelErr != nil {
		return nil, cancelErr
	}
	if err != nil {
		return nil, fmt.Errorf("%s 图片数据损坏或无法解码：%w", strings.ToUpper(format), err)
	}
	if format == "heic" {
		decoded = normalizeOrientation(decoded, pngOrientation(pngBytes))
	}
	return decoded, nil
}

// VerifyImageData validates headers and full pixel decodability in memory.
func VerifyImageData(ctx context.Context, data []byte) (string, error) {
	info, err := InspectBytes(ctx, data)
	if err != nil {
		return "", err
	}
	if err := verifyImagePixels(ctx, data, info.Format); err != nil {
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
	if info.Format == "heic" || info.Format == "avif" {
		data := bytes.NewBuffer(make([]byte, 0, size))
		n, err := io.Copy(data, io.LimitReader(contextio.NewReader(ctx, r), maxInputSize+1))
		if err != nil {
			return ImageInfo{}, err
		}
		if n > maxInputSize {
			return ImageInfo{}, fmt.Errorf("图片超过 64 MiB 限制")
		}
		if err := verifyImagePixels(ctx, data.Bytes(), info.Format); err != nil {
			return ImageInfo{}, err
		}
		return info, nil
	}
	if err := verifyImageDecode(ctx, r); err != nil {
		return ImageInfo{}, err
	}
	return info, nil
}

func verifyImagePixels(ctx context.Context, data []byte, format string) error {
	var err error
	switch format {
	case "heic":
		data, err = heictools.DecodePNG(ctx, data)
	case "avif":
		data, err = aviftools.DecodePNG(ctx, data, 0)
	}
	if err != nil {
		return err
	}
	return verifyImageDecode(ctx, bytes.NewReader(data))
}

func verifyImageDecode(ctx context.Context, r io.Reader) error {
	if _, _, err := image.Decode(contextio.NewReader(ctx, r)); err != nil {
		return fmt.Errorf("图片数据损坏：%w", err)
	}
	return ctx.Err()
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

// flattenAlphaForJPEG composites non-opaque images over white for JPEG output.
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
			// RGBA returns premultiplied channels. Clamp them to alpha so the
			// composite over white cannot exceed 0xffff.
			a = min(a, 0xffff)
			white := 0xffff - a
			out.Pix[off] = uint8((min(r, a) + white) >> 8)
			out.Pix[off+1] = uint8((min(g, a) + white) >> 8)
			out.Pix[off+2] = uint8((min(b, a) + white) >> 8)
			out.Pix[off+3] = 0xff
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
