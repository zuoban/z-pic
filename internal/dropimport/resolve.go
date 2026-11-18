package dropimport

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"image"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"zpic/internal/compress"
	"zpic/internal/contextio"
	"zpic/internal/nameseq"
)

// Resolve honors cancellation before leaving local paths untouched.
// Remote and byte inputs are saved
// unchanged only after header inspection and full static JPEG/PNG/WebP decoding.
// The caller owns successful downloaded files and their eventual removal.
func Resolve(ctx context.Context, item Item, dir string) (path string, downloaded bool, err error) {
	return ResolveWithTransport(ctx, item, dir, nil)
}

func NewTransport() *http.Transport {
	return &http.Transport{
		Proxy:                  http.ProxyFromEnvironment,
		DisableCompression:     true,
		DialContext:            (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:      true,
		MaxIdleConns:           16,
		MaxIdleConnsPerHost:    2,
		IdleConnTimeout:        30 * time.Second,
		TLSHandshakeTimeout:    10 * time.Second,
		ResponseHeaderTimeout:  15 * time.Second,
		MaxResponseHeaderBytes: 1 << 20,
	}
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

type filenameSequence = nameseq.Sequence

func (resolver *Resolver) Resolve(ctx context.Context, item Item) (path string, downloaded bool, err error) {
	dir := resolver.dir
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	if item.Path != "" {
		return item.Path, false, nil
	}
	if dir == "" {
		return "", false, fmt.Errorf("图片保存目录不能为空")
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
	// Reject login/error pages before downloading the whole response or
	// creating a temporary file. The complete validation below remains required.
	buffered := bufio.NewReaderSize(contextio.NewReader(ctx, reader), 512)
	header, err := buffered.Peek(12)
	if err != nil && err != io.EOF {
		return "", false, err
	}
	if !bytes.HasPrefix(header, []byte("\xff\xd8\xff")) &&
		!bytes.HasPrefix(header, []byte("\x89PNG\r\n\x1a\n")) &&
		!(len(header) >= 12 && string(header[:4]) == "RIFF" && string(header[8:12]) == "WEBP") {
		return "", false, fmt.Errorf("无法识别图片，仅支持静态 JPEG/PNG/WebP")
	}
	reader = buffered
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
	size, err := io.Copy(temp, io.LimitReader(contextio.NewReader(ctx, reader), maxImage+1))
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
	if _, err := temp.Seek(0, io.SeekStart); err != nil {
		return "", false, err
	}
	if _, _, err := image.Decode(contextio.NewReader(ctx, temp)); err != nil {
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
		suffix = sequence.Next(suffix)
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
		_, err = io.Copy(output, contextio.NewReader(ctx, sourceFile))
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
