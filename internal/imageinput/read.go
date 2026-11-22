package imageinput

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"

	"zpic/internal/contextio"
)

const (
	MaxSize                = 64 << 20
	MaxIntermediatePNGSize = 128 << 20
)

var errTooLarge = errors.New("图片文件超过 64 MiB 限制")

func tooLargeError(maxSize int64) error {
	if maxSize == MaxSize {
		return errTooLarge
	}
	if maxSize%(1<<20) == 0 {
		return fmt.Errorf("图片文件超过 %d MiB 限制", maxSize/(1<<20))
	}
	return fmt.Errorf("图片文件超过 %d 字节限制", maxSize)
}

func Read(ctx context.Context, path string) ([]byte, error) {
	return ReadLimit(ctx, path, MaxSize)
}

// ReadLimit reads a regular file bounded by maxSize bytes.
func ReadLimit(ctx context.Context, path string, maxSize int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if maxSize <= 0 || maxSize > math.MaxInt-1 {
		return nil, errors.New("图片文件大小限制无效")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("无法读取图片信息：%w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("图片输入必须是普通文件")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("无法打开图片：%w", err)
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil {
		return nil, fmt.Errorf("无法读取图片信息：%w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("图片输入必须是普通文件")
	}
	data, err := readFileLimit(ctx, file, info.Size(), maxSize)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// ReadFile reads up to MaxSize bytes from the start of an already-opened regular file.
func ReadFile(ctx context.Context, file *os.File, sizeHint int64) ([]byte, error) {
	return readFileLimit(ctx, file, sizeHint, MaxSize)
}

func readFileLimit(ctx context.Context, file *os.File, sizeHint, maxSize int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if maxSize <= 0 || maxSize > math.MaxInt-1 {
		return nil, errors.New("图片文件大小限制无效")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("读取图片失败：%w", err)
	}
	data, err := readSizedLimit(ctx, file, sizeHint, maxSize)
	if err != nil {
		return nil, fmt.Errorf("读取图片失败：%w", err)
	}
	return data, nil
}

func readSized(ctx context.Context, source io.Reader, size int64) ([]byte, error) {
	return readSizedLimit(ctx, source, size, MaxSize)
}

func readSizedLimit(ctx context.Context, source io.Reader, size, maxSize int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if maxSize <= 0 || maxSize > math.MaxInt-1 {
		return nil, errors.New("图片文件大小限制无效")
	}
	if size > maxSize {
		return nil, tooLargeError(maxSize)
	}
	if size < 0 {
		return nil, errors.New("图片文件大小无效")
	}
	limit := int(maxSize)
	reader := io.LimitReader(contextio.NewReader(ctx, source), maxSize+1)
	data := make([]byte, int(size)+1)
	count, err := io.ReadFull(reader, data)
	data = data[:count]
	var probe [1]byte
	for {
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return nil, err
		}
		if len(data) > limit {
			return nil, tooLargeError(maxSize)
		}
		if err != nil {
			break
		}
		// Probe EOF before growing, especially when the buffer is already at
		// the limit. Accumulate growth in one slice, capped at maxSize+1.
		_, err = io.ReadFull(reader, probe[:])
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(data) == limit {
			return nil, tooLargeError(maxSize)
		}
		grown := make([]byte, min(limit+1, max(32<<10, 2*len(data))))
		copy(grown, data)
		grown[len(data)] = probe[0]
		count, err = io.ReadFull(reader, grown[len(data)+1:])
		data = grown[:len(data)+1+count]
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return data[:len(data):len(data)], nil
}
