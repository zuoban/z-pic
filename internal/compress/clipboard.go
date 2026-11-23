package compress

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/png"
	"io"
	"sync"

	"zpic/internal/aviftools"
	"zpic/internal/contextio"
	"zpic/internal/toolio"
	"zpic/internal/ycbcr"
)

type pngEncoderPool struct {
	pool sync.Pool
}

func (p *pngEncoderPool) Get() *png.EncoderBuffer {
	if buf, ok := p.pool.Get().(*png.EncoderBuffer); ok {
		return buf
	}
	return nil
}

func (p *pngEncoderPool) Put(buf *png.EncoderBuffer) {
	p.pool.Put(buf)
}

var (
	clipboardEncoderBuffers pngEncoderPool
	clipboardZlibWriters    = sync.Pool{
		New: func() any {
			zw, _ := zlib.NewWriterLevel(io.Discard, zlib.BestSpeed)
			return zw
		},
	}
	pngIENDChunk = [12]byte{0, 0, 0, 0, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82}
	idatTagCRC   = crc32.ChecksumIEEE([]byte("IDAT"))
)

// CompressClipboardPNG compresses a static PNG snapshot using the preset's
// PNG, lossless, metadata, and size settings. The result reports the selected
// PNG's actual byte count. Keep data immutable while using the result, which
// may retain the input bytes.
func CompressClipboardPNG(ctx context.Context, data []byte, options Options) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("压缩已取消：%w", err)
	}
	options.OutputFormat = OutputFormatPNG
	options.SkipOverwriteSnapshot = true
	if err := validateOptions(options); err != nil {
		return nil, err
	}
	if !bytes.HasPrefix(data, pngSignature[:]) {
		return nil, errors.New("仅支持压缩剪贴板中的静态 PNG 图片")
	}
	return compressData(ctx, data, "", "", options, nil)
}

func PNGBytes(ctx context.Context, data []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(data) > maxInputSize {
		return nil, fmt.Errorf("图片超过 64 MiB 限制")
	}
	if aviftools.SniffFTYP(data) {
		pngBytes, format, err := decodeBMFFPNG(ctx, data, 0)
		if err != nil {
			return nil, err
		}
		return normalizeBMFFClipboardPNG(ctx, pngBytes, format, maxInputSize)
	}
	config, format, err := image.DecodeConfig(contextio.NewReader(ctx, bytes.NewReader(data)))
	if err != nil {
		return nil, fmt.Errorf("无法读取图片：%w", err)
	}
	if format != "jpeg" && format != "png" && format != "webp" {
		return nil, fmt.Errorf("仅支持 JPEG、PNG、WebP、HEIC 和 AVIF 图片")
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > maxPixels/config.Height {
		return nil, fmt.Errorf("图片超过 3200 万像素限制或尺寸无效")
	}
	if format == "png" {
		if err := validatePNGStructureAndCRCs(ctx, data, config); err != nil {
			return nil, err
		}
		return data, nil
	}
	if format == "webp" {
		if err := ValidateWebP(ctx, data); err != nil {
			return nil, err
		}
	}
	decoded, _, err := image.Decode(contextio.NewReader(ctx, bytes.NewReader(data)))
	if err != nil {
		return nil, fmt.Errorf("无法解码图片：%w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch format {
	case "jpeg":
		decoded, err = orientForJPEGEncode(ctx, decoded, jpegOrientation(data))
	case "webp":
		decoded, err = orientForJPEGEncode(ctx, decoded, webpOrientation(data))
	}
	if err != nil {
		return nil, err
	}
	return encodeClipboardPNG(ctx, decoded, maxInputSize)
}

func normalizeBMFFClipboardPNG(ctx context.Context, pngBytes []byte, format string, limit int) ([]byte, error) {
	orient := 1
	if format == "heic" {
		orient = pngOrientation(pngBytes)
	}
	if orient == 1 && len(pngBytes) <= limit {
		return pngBytes, nil
	}
	decoded, _, err := image.Decode(contextio.NewReader(ctx, bytes.NewReader(pngBytes)))
	if err != nil {
		return nil, fmt.Errorf("无法解码图片：%w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	decoded, err = orientForJPEGEncode(ctx, decoded, orient)
	if err != nil {
		return nil, err
	}
	return encodeClipboardPNG(ctx, decoded, limit)
}

func encodeClipboardPNG(ctx context.Context, decoded image.Image, limit int) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch src := decoded.(type) {
	case *image.YCbCr:
		return encodeYCbCrClipboardPNG(ctx, src, limit)
	case *image.NYCbCrA:
		return encodeNYCbCrAClipboardPNG(ctx, src, limit)
	}
	output := clipboardPNGWriter{ctx: ctx, limit: limit}
	encoder := png.Encoder{
		CompressionLevel: png.BestSpeed,
		BufferPool:       &clipboardEncoderBuffers,
	}
	if err := encoder.Encode(&output, decoded); err != nil {
		return nil, fmt.Errorf("无法生成剪贴板图片：%w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return output.buffer.Bytes(), nil
}

// encodeYCbCrClipboardPNG streams 8-bit RGB PNG scanlines directly from planar
// YCbCr samples without boxing pixels through image.Image.At() or allocating a
// full-resolution intermediate buffer.
func encodeYCbCrClipboardPNG(ctx context.Context, src *image.YCbCr, limit int) ([]byte, error) {
	out, err := encodeYCbCrPNG(ctx, src, limit, errClipboardLimit, nil)
	if err != nil {
		if ctx.Err() != nil {
			return nil, err
		}
		return nil, fmt.Errorf("无法生成剪贴板图片：%w", err)
	}
	return out, nil
}

func encodeNYCbCrAClipboardPNG(ctx context.Context, src *image.NYCbCrA, limit int) ([]byte, error) {
	out, err := encodeNYCbCrAPNG(ctx, src, limit, errClipboardLimit, nil)
	if err != nil {
		if ctx.Err() != nil {
			return nil, err
		}
		return nil, fmt.Errorf("无法生成剪贴板图片：%w", err)
	}
	return out, nil
}

func encodeYCbCrPNG(ctx context.Context, src *image.YCbCr, limit int, limitErr error, extraChunks [][]byte) ([]byte, error) {
	bounds := src.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 {
		return nil, errors.New("尺寸无效")
	}
	return stream8BitPNG(ctx, width, height, 2, 3, limit, limitErr, extraChunks, func(row int, raw []byte) {
		ycbcr.FillRGBRow(raw, src, bounds, row)
	})
}

func encodeNYCbCrAPNG(ctx context.Context, src *image.NYCbCrA, limit int, limitErr error, extraChunks [][]byte) ([]byte, error) {
	bounds := src.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 {
		return nil, errors.New("尺寸无效")
	}
	if src.Opaque() {
		return encodeYCbCrPNG(ctx, &src.YCbCr, limit, limitErr, extraChunks)
	}
	return stream8BitPNG(ctx, width, height, 6, 4, limit, limitErr, extraChunks, func(row int, raw []byte) {
		ycbcr.FillNYCbCrARow(raw, src, bounds, row)
	})
}

func stream8BitPNG(
	ctx context.Context,
	width, height int,
	colorType byte,
	bpp int,
	limit int,
	limitErr error,
	extraChunks [][]byte,
	fillRow func(row int, raw []byte),
) ([]byte, error) {
	output := clipboardPNGWriter{ctx: ctx, limit: limit, limitErr: limitErr}
	if _, err := output.Write(pngSignature[:]); err != nil {
		return nil, err
	}
	var ihdr [25]byte
	binary.BigEndian.PutUint32(ihdr[0:4], 13)
	copy(ihdr[4:8], "IHDR")
	binary.BigEndian.PutUint32(ihdr[8:12], uint32(width))
	binary.BigEndian.PutUint32(ihdr[12:16], uint32(height))
	ihdr[16] = 8
	ihdr[17] = colorType
	binary.BigEndian.PutUint32(ihdr[21:25], crc32.ChecksumIEEE(ihdr[4:21]))
	if _, err := output.Write(ihdr[:]); err != nil {
		return nil, err
	}
	for _, chunk := range extraChunks {
		if _, err := output.Write(chunk); err != nil {
			return nil, err
		}
	}
	idat := idatChunkWriter{dst: &output}
	zw := clipboardZlibWriters.Get().(*zlib.Writer)
	zw.Reset(&idat)
	defer func() {
		zw.Reset(io.Discard)
		clipboardZlibWriters.Put(zw)
	}()

	stride := width * bpp
	rawRow := make([]byte, stride)
	filteredRow := make([]byte, 1+stride)
	filteredRow[0] = 1 // PNG Sub filter

	for row := 0; row < height; row++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		fillRow(row, rawRow)
		copy(filteredRow[1:1+bpp], rawRow[:bpp])
		for i := bpp; i < stride; i++ {
			filteredRow[1+i] = rawRow[i] - rawRow[i-bpp]
		}
		if _, err := zw.Write(filteredRow); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	if err := idat.Flush(); err != nil {
		return nil, err
	}
	if _, err := output.Write(pngIENDChunk[:]); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return output.buffer.Bytes(), nil
}

type idatChunkWriter struct {
	dst io.Writer
	buf [32 << 10]byte
	n   int
}

func (w *idatChunkWriter) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		space := len(w.buf) - w.n
		step := min(len(p), space)
		copy(w.buf[w.n:], p[:step])
		w.n += step
		p = p[step:]
		written += step
		if w.n == len(w.buf) {
			if err := w.Flush(); err != nil {
				return written, err
			}
		}
	}
	return written, nil
}

func (w *idatChunkWriter) Flush() error {
	if w.n == 0 {
		return nil
	}
	var header [8]byte
	binary.BigEndian.PutUint32(header[0:4], uint32(w.n))
	copy(header[4:8], "IDAT")
	if _, err := w.dst.Write(header[:]); err != nil {
		return err
	}
	payload := w.buf[:w.n]
	w.n = 0
	if _, err := w.dst.Write(payload); err != nil {
		return err
	}
	var trailer [4]byte
	binary.BigEndian.PutUint32(trailer[:], crc32.Update(idatTagCRC, crc32.IEEETable, payload))
	_, err := w.dst.Write(trailer[:])
	return err
}

var errClipboardLimit = errors.New("剪贴板 PNG 图片超过 64 MiB 限制")

// Check each encoder write so cancellation and expansion limits apply during
// encoding, including when a small JPEG expands into a much larger PNG.
type clipboardPNGWriter struct {
	ctx      context.Context
	limit    int
	limitErr error
	buffer   toolio.BoundedBuffer
}

func (w *clipboardPNGWriter) Write(data []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		w.buffer.Reset()
		return 0, err
	}
	if len(data) > w.limit-w.buffer.Len() {
		w.buffer.Reset()
		if w.limitErr != nil {
			return 0, w.limitErr
		}
		return 0, errClipboardLimit
	}
	w.buffer.AppendBounded(nil, data, w.limit)
	return len(data), nil
}
