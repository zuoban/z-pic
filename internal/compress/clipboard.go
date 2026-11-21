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
	"image/color"
	"image/png"
	"io"
	"sync"

	"zpic/internal/aviftools"
	"zpic/internal/contextio"
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
		if format == "avif" {
			return pngBytes, nil
		}
		orient := pngOrientation(pngBytes)
		if orient == 1 && len(pngBytes) <= maxInputSize {
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
		return encodeClipboardPNG(ctx, decoded, maxInputSize)
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
	bounds := src.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("无法生成剪贴板图片：尺寸无效")
	}
	const bpp = 3
	return stream8BitPNG(ctx, width, height, 2, bpp, limit, func(row int, raw []byte) {
		if src.SubsampleRatio == image.YCbCrSubsampleRatio420 && bounds.Min.X == 0 && bounds.Min.Y == 0 {
			yRow := src.Y[row*src.YStride : row*src.YStride+width]
			cOff := (row >> 1) * src.CStride
			cbRow := src.Cb[cOff:]
			crRow := src.Cr[cOff:]
			col := 0
			for ; col+1 < width; col += 2 {
				ci := col >> 1
				cb, cr := cbRow[ci], crRow[ci]
				r0, g0, b0 := color.YCbCrToRGB(yRow[col], cb, cr)
				r1, g1, b1 := color.YCbCrToRGB(yRow[col+1], cb, cr)
				p := raw[col*3 : col*3+6 : col*3+6]
				p[0], p[1], p[2] = r0, g0, b0
				p[3], p[4], p[5] = r1, g1, b1
			}
			if col < width {
				ci := col >> 1
				r0, g0, b0 := color.YCbCrToRGB(yRow[col], cbRow[ci], crRow[ci])
				p := raw[col*3 : col*3+3 : col*3+3]
				p[0], p[1], p[2] = r0, g0, b0
			}
			return
		}
		sy := bounds.Min.Y + row
		for col := range width {
			sx := bounds.Min.X + col
			yi := src.YOffset(sx, sy)
			ci := src.COffset(sx, sy)
			r, g, b := color.YCbCrToRGB(src.Y[yi], src.Cb[ci], src.Cr[ci])
			p := raw[col*3 : col*3+3 : col*3+3]
			p[0], p[1], p[2] = r, g, b
		}
	})
}

func encodeNYCbCrAClipboardPNG(ctx context.Context, src *image.NYCbCrA, limit int) ([]byte, error) {
	bounds := src.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("无法生成剪贴板图片：尺寸无效")
	}
	if src.Opaque() {
		return encodeYCbCrClipboardPNG(ctx, &src.YCbCr, limit)
	}
	const bpp = 4
	return stream8BitPNG(ctx, width, height, 6, bpp, limit, func(row int, raw []byte) {
		sy := bounds.Min.Y + row
		for col := range width {
			sx := bounds.Min.X + col
			ai := src.AOffset(sx, sy)
			a := src.A[ai]
			p := raw[col*4 : col*4+4 : col*4+4]
			if a == 0 {
				p[0], p[1], p[2], p[3] = 0, 0, 0, 0
				continue
			}
			yi := src.YOffset(sx, sy)
			ci := src.COffset(sx, sy)
			r, g, b := color.YCbCrToRGB(src.Y[yi], src.Cb[ci], src.Cr[ci])
			p[0], p[1], p[2], p[3] = r, g, b, a
		}
	})
}

func stream8BitPNG(
	ctx context.Context,
	width, height int,
	colorType byte,
	bpp int,
	limit int,
	fillRow func(row int, raw []byte),
) ([]byte, error) {
	output := clipboardPNGWriter{ctx: ctx, limit: limit}
	if _, err := output.Write(pngSignature[:]); err != nil {
		return nil, fmt.Errorf("无法生成剪贴板图片：%w", err)
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
		return nil, fmt.Errorf("无法生成剪贴板图片：%w", err)
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
			return nil, fmt.Errorf("无法生成剪贴板图片：%w", err)
		}
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("无法生成剪贴板图片：%w", err)
	}
	if err := idat.Flush(); err != nil {
		return nil, fmt.Errorf("无法生成剪贴板图片：%w", err)
	}
	if _, err := output.Write(pngIENDChunk[:]); err != nil {
		return nil, fmt.Errorf("无法生成剪贴板图片：%w", err)
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
	crc := crc32.NewIEEE()
	_, _ = crc.Write(header[4:8])
	_, _ = crc.Write(payload)
	var trailer [4]byte
	binary.BigEndian.PutUint32(trailer[:], crc.Sum32())
	_, err := w.dst.Write(trailer[:])
	return err
}

var errClipboardLimit = errors.New("剪贴板 PNG 图片超过 64 MiB 限制")

// Check each encoder write so cancellation and expansion limits apply during
// encoding, including when a small JPEG expands into a much larger PNG.
type clipboardPNGWriter struct {
	ctx    context.Context
	limit  int
	buffer bytes.Buffer
}

func (w *clipboardPNGWriter) Write(data []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		w.buffer = bytes.Buffer{}
		return 0, err
	}
	if len(data) > w.limit-w.buffer.Len() {
		w.buffer = bytes.Buffer{}
		return 0, errClipboardLimit
	}
	return w.buffer.Write(data)
}
