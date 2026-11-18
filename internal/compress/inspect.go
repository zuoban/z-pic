package compress

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"sync"

	"zpic/internal/contextio"
)

var jpegInspectBufPool = sync.Pool{
	New: func() any {
		return new([4096]byte)
	},
}

// ImageInfo describes validated static JPEG, PNG or WebP metadata read from headers.
type ImageInfo struct {
	Path   string
	Format string
	Width  int
	Height int
	Size   int64
}

// Inspect reads image headers from path without retaining file payloads or
// decoding pixel buffers in memory.
func Inspect(ctx context.Context, path string) (ImageInfo, error) {
	f, info, err := openAndInspect(ctx, path)
	if err != nil {
		return ImageInfo{}, err
	}
	_ = f.Close()
	return info, nil
}

// InspectReader validates static JPEG/PNG/WebP headers on an already-open
// seekable stream of known byte length without reopening the path.
func InspectReader(ctx context.Context, r io.ReadSeeker, size int64) (ImageInfo, error) {
	if err := ctx.Err(); err != nil {
		return ImageInfo{}, fmt.Errorf("读取图片已取消：%w", err)
	}
	if size < 0 || size > maxInputSize {
		return ImageInfo{}, fmt.Errorf("图片超过 64 MiB 限制")
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return ImageInfo{}, fmt.Errorf("无法读取图片头部：%w", err)
	}
	format, width, height, err := inspectStream(ctx, r, size)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ImageInfo{}, fmt.Errorf("读取图片已取消：%w", ctxErr)
		}
		return ImageInfo{}, err
	}
	if err := ctx.Err(); err != nil {
		return ImageInfo{}, fmt.Errorf("读取图片已取消：%w", err)
	}
	return ImageInfo{
		Format: format,
		Width:  width,
		Height: height,
		Size:   size,
	}, nil
}

func openAndInspect(ctx context.Context, path string) (*os.File, ImageInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, ImageInfo{}, fmt.Errorf("读取图片已取消：%w", err)
	}
	if path == "" {
		return nil, ImageInfo{}, fmt.Errorf("图片路径不能为空")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, ImageInfo{}, fmt.Errorf("无法打开图片：%w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, ImageInfo{}, fmt.Errorf("图片输入必须是普通文件")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, ImageInfo{}, fmt.Errorf("无法打开图片：%w", err)
	}
	closeOnErr := true
	defer func() {
		if closeOnErr {
			_ = f.Close()
		}
	}()
	opened, err := f.Stat()
	if err != nil {
		return nil, ImageInfo{}, fmt.Errorf("无法读取图片信息：%w", err)
	}
	if !opened.Mode().IsRegular() {
		return nil, ImageInfo{}, fmt.Errorf("图片输入必须是普通文件")
	}
	if opened.Size() > maxInputSize {
		return nil, ImageInfo{}, fmt.Errorf("图片超过 64 MiB 限制")
	}
	format, width, height, err := inspectStream(ctx, f, opened.Size())
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ImageInfo{}, fmt.Errorf("读取图片已取消：%w", ctxErr)
		}
		return nil, ImageInfo{}, err
	}
	if err := ctx.Err(); err != nil {
		return nil, ImageInfo{}, fmt.Errorf("读取图片已取消：%w", err)
	}
	closeOnErr = false
	return f, ImageInfo{
		Path:   path,
		Format: format,
		Width:  width,
		Height: height,
		Size:   opened.Size(),
	}, nil
}

func inspectStream(ctx context.Context, f io.ReadSeeker, size int64) (string, int, int, error) {
	var head [33]byte
	n, err := io.ReadFull(contextio.NewReader(ctx, f), head[:])
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return "", 0, 0, fmt.Errorf("无法识别图片，仅支持静态 JPEG/PNG/WebP：%w", err)
	}
	switch {
	case n >= 8 && [8]byte(head[:8]) == pngSignature:
		width, height, err := parsePNGIHDR(head[:n])
		if err != nil {
			return "", 0, 0, err
		}
		if err := validatePNGContainer(ctx, f, size); err != nil {
			return "", 0, 0, err
		}
		return "png", width, height, nil
	case n >= 12 && string(head[:4]) == "RIFF" && string(head[8:12]) == "WEBP":
		width, height, err := validateWebPContainerSize(ctx, f, size)
		if err != nil {
			return "", 0, 0, err
		}
		return "webp", width, height, nil
	case n >= 3 && head[0] == 0xff && head[1] == 0xd8 && head[2] == 0xff:
		width, height, err := inspectJPEGStream(ctx, f, size)
		if err != nil {
			return "", 0, 0, err
		}
		return "jpeg", width, height, nil
	case n >= 6 && (string(head[:6]) == "GIF87a" || string(head[:6]) == "GIF89a"):
		return "", 0, 0, fmt.Errorf("不支持 gif 图片，仅支持静态 JPEG/PNG/WebP")
	default:
		return "", 0, 0, errors.New("无法识别图片，仅支持静态 JPEG/PNG/WebP")
	}
}

func parsePNGIHDR(head []byte) (int, int, error) {
	if len(head) < 33 ||
		binary.BigEndian.Uint32(head[8:12]) != 13 ||
		string(head[12:16]) != "IHDR" ||
		crc32.ChecksumIEEE(head[12:29]) != binary.BigEndian.Uint32(head[29:33]) {
		return 0, 0, errors.New("无法识别图片，仅支持静态 JPEG/PNG/WebP：PNG IHDR 损坏")
	}
	width := int(int32(binary.BigEndian.Uint32(head[16:20])))
	height := int(int32(binary.BigEndian.Uint32(head[20:24])))
	bitDepth, colorType := head[24], head[25]
	compression, filter, interlace := head[26], head[27], head[28]
	if compression != 0 || filter != 0 || interlace > 1 || !validPNGColorBitDepth(colorType, bitDepth) {
		return 0, 0, errors.New("无法识别图片，仅支持静态 JPEG/PNG/WebP：PNG 参数不支持")
	}
	if width <= 0 || height <= 0 || uint64(width) > uint64(maxPixels)/uint64(height) {
		return 0, 0, errors.New("图片超过 3200 万像素限制或尺寸无效")
	}
	return width, height, nil
}

func validPNGColorBitDepth(colorType, bitDepth byte) bool {
	switch colorType {
	case 0:
		return bitDepth == 1 || bitDepth == 2 || bitDepth == 4 || bitDepth == 8 || bitDepth == 16
	case 2, 4, 6:
		return bitDepth == 8 || bitDepth == 16
	case 3:
		return bitDepth == 1 || bitDepth == 2 || bitDepth == 4 || bitDepth == 8
	default:
		return false
	}
}

type pooledByteReader struct {
	r   io.Reader
	buf *[4096]byte
	pos int
	end int
}

func (b *pooledByteReader) nextByte() (byte, error) {
	if b.pos >= b.end {
		n, err := b.r.Read(b.buf[:])
		for n == 0 && err == nil {
			n, err = b.r.Read(b.buf[:])
		}
		if n == 0 {
			if err == nil {
				err = io.EOF
			}
			return 0, err
		}
		b.pos = 0
		b.end = n
	}
	v := b.buf[b.pos]
	b.pos++
	return v, nil
}

func (b *pooledByteReader) readFull(dst []byte) error {
	for len(dst) > 0 {
		if b.pos < b.end {
			copied := copy(dst, b.buf[b.pos:b.end])
			b.pos += copied
			dst = dst[copied:]
			continue
		}
		if len(dst) >= len(b.buf) {
			_, err := io.ReadFull(b.r, dst)
			return err
		}
		if _, err := b.nextByte(); err != nil {
			return err
		}
		b.pos--
	}
	return nil
}

func (b *pooledByteReader) discard(count int) error {
	for count > 0 {
		if avail := b.end - b.pos; avail > 0 {
			step := min(count, avail)
			b.pos += step
			count -= step
			continue
		}
		n, err := b.r.Read(b.buf[:min(count, len(b.buf))])
		count -= n
		if err != nil {
			if count == 0 && err == io.EOF {
				return nil
			}
			return err
		}
	}
	return nil
}

// inspectJPEGStream parses JPEG markers up to SOF0 (baseline) or SOF2 (progressive),
// matching standard library image/jpeg support with zero heap allocations.
func inspectJPEGStream(ctx context.Context, f io.ReadSeeker, size int64) (int, int, error) {
	if size < 4 {
		return 0, 0, errors.New("无法识别图片，仅支持静态 JPEG/PNG/WebP")
	}
	if _, err := f.Seek(2, io.SeekStart); err != nil {
		return 0, 0, fmt.Errorf("无法读取图片头部：%w", err)
	}
	buf := jpegInspectBufPool.Get().(*[4096]byte)
	defer jpegInspectBufPool.Put(buf)
	reader := pooledByteReader{
		r:   io.LimitReader(contextio.NewReader(ctx, f), size-2),
		buf: buf,
	}
	var lenBytes [2]byte
	var sof [18]byte
	for {
		b, err := reader.nextByte()
		if err != nil {
			return 0, 0, fmt.Errorf("无法识别图片，仅支持静态 JPEG/PNG/WebP：%w", err)
		}
		if b != 0xff {
			return 0, 0, errors.New("无法识别图片，仅支持静态 JPEG/PNG/WebP：JPEG 标记损坏")
		}
		for b == 0xff {
			b, err = reader.nextByte()
			if err != nil {
				return 0, 0, fmt.Errorf("无法识别图片，仅支持静态 JPEG/PNG/WebP：%w", err)
			}
		}
		if b == 0x00 || b == 0xd8 || b == 0xd9 || b == 0xda {
			return 0, 0, errors.New("无法识别图片，仅支持静态 JPEG/PNG/WebP：JPEG 结构无效")
		}
		if b == 0x01 || (b >= 0xd0 && b <= 0xd7) {
			continue
		}
		if err := reader.readFull(lenBytes[:]); err != nil {
			return 0, 0, fmt.Errorf("无法识别图片，仅支持静态 JPEG/PNG/WebP：%w", err)
		}
		payloadLen := int(binary.BigEndian.Uint16(lenBytes[:])) - 2
		if payloadLen < 0 {
			return 0, 0, errors.New("无法识别图片，仅支持静态 JPEG/PNG/WebP：JPEG 段长度无效")
		}
		if b == 0xc0 || b == 0xc2 {
			if payloadLen < 6 || payloadLen > len(sof) {
				return 0, 0, errors.New("无法识别图片，仅支持静态 JPEG/PNG/WebP：JPEG 帧头无效")
			}
			if err := reader.readFull(sof[:payloadLen]); err != nil {
				return 0, 0, fmt.Errorf("无法识别图片，仅支持静态 JPEG/PNG/WebP：%w", err)
			}
			precision := sof[0]
			height := int(binary.BigEndian.Uint16(sof[1:3]))
			width := int(binary.BigEndian.Uint16(sof[3:5]))
			nComp := int(sof[5])
			if precision != 8 || (nComp != 1 && nComp != 3 && nComp != 4) || payloadLen != 6+3*nComp {
				return 0, 0, errors.New("无法识别图片，仅支持静态 JPEG/PNG/WebP：JPEG 采样不支持")
			}
			var seenIDs [256]bool
			for i := 0; i < nComp; i++ {
				id := sof[6+3*i]
				hv := sof[6+3*i+1]
				h, v := hv>>4, hv&0x0f
				if seenIDs[id] || h < 1 || h > 4 || v < 1 || v > 4 {
					return 0, 0, errors.New("无法识别图片，仅支持静态 JPEG/PNG/WebP：JPEG 分量无效")
				}
				seenIDs[id] = true
			}
			if width <= 0 || height <= 0 || uint64(width) > uint64(maxPixels)/uint64(height) {
				return 0, 0, errors.New("图片超过 3200 万像素限制或尺寸无效")
			}
			return width, height, nil
		}
		if b >= 0xc1 && b <= 0xcf && b != 0xc4 && b != 0xc8 && b != 0xcc {
			return 0, 0, errors.New("无法识别图片，仅支持静态 JPEG/PNG/WebP：不支持的 JPEG 编码模式")
		}
		if err := reader.discard(payloadLen); err != nil {
			return 0, 0, fmt.Errorf("无法识别图片，仅支持静态 JPEG/PNG/WebP：%w", err)
		}
	}
}
