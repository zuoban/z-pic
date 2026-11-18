package imageinput

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
)

const MaxSize = 64 << 20

var errTooLarge = errors.New("图片文件超过 64 MiB 限制")

func Read(ctx context.Context, path string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
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
	data, err := readSized(ctx, file, info.Size())
	if err != nil {
		return nil, fmt.Errorf("读取图片失败：%w", err)
	}
	return data, nil
}

func readSized(ctx context.Context, source io.Reader, size int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if size > MaxSize {
		return nil, errTooLarge
	}
	if size < 0 {
		return nil, errors.New("图片文件大小无效")
	}
	reader := io.LimitReader(contextReader{ctx: ctx, reader: source}, MaxSize+1)
	data := make([]byte, int(size)+1)
	count, err := io.ReadFull(reader, data)
	data = data[:count]
	var probe [1]byte
	for {
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return nil, err
		}
		if len(data) > MaxSize {
			return nil, errTooLarge
		}
		if err != nil {
			break
		}
		// Probe EOF before growing, especially when the buffer is already at
		// the limit. Accumulate growth in one slice, capped at MaxSize+1.
		_, err = io.ReadFull(reader, probe[:])
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(data) == MaxSize {
			return nil, errTooLarge
		}
		grown := make([]byte, min(MaxSize+1, max(32<<10, 2*len(data))))
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

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader contextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(buffer)
}
