package dropimport

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"zpic/internal/compress"
)

// Resolve leaves local paths untouched. Remote and byte inputs are saved
// unchanged only after header inspection and full static JPEG/PNG/WebP decoding.
// The caller owns successful downloaded files and their eventual removal.
func Resolve(ctx context.Context, item Item, dir string) (path string, downloaded bool, err error) {
	return ResolveWithTransport(ctx, item, dir, nil)
}

func NewTransport() *http.Transport {
	return &http.Transport{Proxy: http.ProxyFromEnvironment, DisableCompression: true}
}

func ResolveWithTransport(ctx context.Context, item Item, dir string, sharedTransport *http.Transport) (path string, downloaded bool, err error) {
	return NewResolver(dir, sharedTransport).Resolve(ctx, item)
}

type Resolver struct {
	dir       string
	transport *http.Transport
	saved     map[string]bool
	sequences map[string]filenameSequence
}

func NewResolver(dir string, sharedTransport *http.Transport) *Resolver {
	return &Resolver{dir: dir, transport: sharedTransport}
}

type filenameSequence map[int]int

func (sequence filenameSequence) next(index int) int {
	available := index
	for sequence[available] > available {
		available = sequence[available]
	}
	for sequence[index] > index {
		following := sequence[index]
		sequence[index] = available
		index = following
	}
	return available
}

func (resolver *Resolver) Resolve(ctx context.Context, item Item) (path string, downloaded bool, err error) {
	dir := resolver.dir
	if item.Path != "" {
		return item.Path, false, nil
	}
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	var reader io.Reader
	name := "image"
	if item.URL != "" {
		parsed, err := httpURL(item.URL)
		if err != nil {
			return "", false, err
		}
		name = filename(parsed)
		transport := resolver.transport
		if transport == nil {
			transport = NewTransport()
			defer transport.CloseIdleConnections()
		}
		client := &http.Client{
			Transport: transport,
			Timeout:   30 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return fmt.Errorf("图片下载重定向次数过多")
				}
				_, err := httpURL(req.URL.String())
				return err
			},
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
		if err != nil {
			return "", false, err
		}
		response, err := client.Do(request)
		if err != nil {
			return "", false, fmt.Errorf("图片下载失败：%w", err)
		}
		defer response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return "", false, fmt.Errorf("图片下载失败：HTTP %d", response.StatusCode)
		}
		if response.ContentLength > maxImage {
			return "", false, fmt.Errorf("图片超过 64 MiB 限制")
		}
		reader = response.Body
	} else {
		if len(item.Data) == 0 {
			return "", false, unsupported()
		}
		if len(item.Data) > maxImage {
			return "", false, fmt.Errorf("图片超过 64 MiB 限制")
		}
		reader = bytes.NewReader(item.Data)
	}
	if dir == "" {
		return "", false, fmt.Errorf("图片保存目录不能为空")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", false, fmt.Errorf("无法创建图片目录：%w", err)
	}
	temp, err := os.CreateTemp(dir, ".drop-*")
	if err != nil {
		return "", false, err
	}
	tempName := temp.Name()
	tempClosed := false
	defer func() {
		if !tempClosed {
			_ = temp.Close()
		}
		_ = os.Remove(tempName)
	}()
	size, err := io.Copy(temp, io.LimitReader(cancelReader{ctx, reader}, maxImage+1))
	if err != nil {
		return "", false, err
	}
	if size > maxImage {
		return "", false, fmt.Errorf("图片超过 64 MiB 限制")
	}
	info, err := compress.Inspect(ctx, tempName)
	if err != nil {
		return "", false, err
	}
	if info.Format == "png" {
		if err := checkPNGChunks(ctx, temp, info.Size); err != nil {
			return "", false, err
		}
	}
	if _, err := temp.Seek(0, io.SeekStart); err != nil {
		return "", false, err
	}
	if _, _, err := image.Decode(cancelReader{ctx, temp}); err != nil {
		return "", false, fmt.Errorf("图片数据损坏：%w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	if err := temp.Close(); err != nil {
		tempClosed = true
		return "", false, err
	}
	tempClosed = true
	extension := ".png"
	if info.Format == "jpeg" {
		extension = ".jpg"
	} else if info.Format == "webp" {
		extension = ".webp"
	}
	if resolver.saved == nil {
		resolver.saved = make(map[string]bool)
	}
	key := name + extension
	sequence := resolver.sequences[key]
	for suffix := 0; suffix < 10000; suffix++ {
		if err := ctx.Err(); err != nil {
			return "", false, err
		}
		suffix = sequence.next(suffix)
		if suffix >= 10000 {
			break
		}
		stem := name
		if suffix > 0 {
			stem = fmt.Sprintf("%s-%d", name, suffix)
		}
		target := filepath.Join(dir, stem+extension)
		if resolver.saved[target] {
			if sequence == nil {
				sequence = make(filenameSequence)
				if resolver.sequences == nil {
					resolver.sequences = make(map[string]filenameSequence)
				}
				resolver.sequences[key] = sequence
			}
			sequence[suffix] = suffix + 1
			continue
		}
		linkErr := os.Link(tempName, target)
		if linkErr == nil {
			if err := ctx.Err(); err != nil {
				_ = os.Remove(target)
				return "", false, err
			}
			resolver.saved[target] = true
			return target, true, nil
		}
		if os.IsExist(linkErr) {
			continue
		}
		output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			return "", false, err
		}
		sourceFile, openErr := os.Open(tempName)
		if openErr != nil {
			_ = output.Close()
			_ = os.Remove(target)
			return "", false, openErr
		}
		_, err = io.Copy(output, cancelReader{ctx, sourceFile})
		sourceCloseErr := sourceFile.Close()
		if err == nil {
			err = sourceCloseErr
		}
		closeErr := output.Close()
		if err == nil {
			err = closeErr
		}
		if err == nil {
			err = ctx.Err()
		}
		if err != nil {
			os.Remove(target)
			return "", false, err
		}
		resolver.saved[target] = true
		return target, true, nil
	}
	return "", false, fmt.Errorf("同名图片过多，请选择其他目录")
}

func checkPNGChunks(ctx context.Context, file *os.File, size int64) error {
	for offset := int64(8); offset < size; {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := file.Seek(offset, io.SeekStart); err != nil {
			return err
		}
		var header [8]byte
		if _, err := io.ReadFull(file, header[:]); err != nil {
			return fmt.Errorf("PNG 数据损坏：%w", err)
		}
		length := int64(binary.BigEndian.Uint32(header[:4]))
		if size-offset < 12 || length > size-offset-12 {
			return fmt.Errorf("PNG 数据损坏")
		}
		if string(header[4:]) == "acTL" {
			return fmt.Errorf("不支持动画 PNG，仅支持静态 JPEG/PNG/WebP")
		}
		if string(header[4:]) == "IEND" {
			return nil
		}
		offset += 12 + length
	}
	return fmt.Errorf("PNG 数据损坏：缺少 IEND")
}

func filename(parsed *url.URL) string {
	base := filepath.Base(parsed.Path)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	var safe strings.Builder
	for _, character := range base {
		if safe.Len() >= 80 {
			break
		}
		if unicode.IsLetter(character) || unicode.IsDigit(character) || character == '-' || character == '_' {
			safe.WriteRune(character)
		}
	}
	if safe.Len() == 0 {
		return "image"
	}
	return safe.String()
}

type cancelReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader cancelReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(buffer)
}
