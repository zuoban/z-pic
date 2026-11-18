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
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"zpic/internal/compress"
	"zpic/internal/contextio"
	"zpic/internal/nameseq"
)

// InspectBytes validates in-memory image bytes before touching the filesystem.
func inspectInMemory(ctx context.Context, payload []byte) (string, error) {
	config, format, err := image.DecodeConfig(contextio.NewReader(ctx, bytes.NewReader(payload)))
	if err != nil {
		return "", fmt.Errorf("无法识别图片，仅支持静态 JPEG/PNG/WebP：%w", err)
	}
	if format != "jpeg" && format != "png" && format != "webp" {
		return "", fmt.Errorf("不支持 %s 图片，仅支持静态 JPEG/PNG/WebP", format)
	}
	const maxPixels = 32_000_000
	if config.Width <= 0 || config.Height <= 0 || uint64(config.Width) > uint64(maxPixels)/uint64(config.Height) {
		return "", fmt.Errorf("图片超过 3200 万像素限制或尺寸无效")
	}
	if format == "png" {
		if err := compress.ValidatePNG(ctx, payload); err != nil {
			return "", err
		}
	}
	if format == "webp" {
		if err := compress.ValidateWebP(ctx, payload); err != nil {
			return "", err
		}
	}
	if _, _, err := image.Decode(contextio.NewReader(ctx, bytes.NewReader(payload))); err != nil {
		return "", fmt.Errorf("图片数据损坏：%w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return format, nil
}

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

var (
	resolveCopyBufPool = sync.Pool{
		New: func() any {
			return new([32 << 10]byte)
		},
	}
	resolvePeekPool = sync.Pool{
		New: func() any {
			return bufio.NewReaderSize(nil, 512)
		},
	}
)

type Resolver struct {
	dir       string
	transport *http.Transport
	client    *http.Client
	saved     map[string]bool
	sequences map[string]filenameSequence
}

func NewResolver(dir string, sharedTransport *http.Transport) *Resolver {
	return &Resolver{dir: dir, transport: sharedTransport}
}

func checkImageRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return fmt.Errorf("图片下载重定向次数过多")
	}
	target, err := httpURL(req.URL.String())
	if err != nil {
		return err
	}
	if len(via) > 0 && !isLocalOrPrivateHost(via[0].URL.Hostname()) && isLocalOrPrivateHost(target.Hostname()) {
		return fmt.Errorf("拒绝从公网地址重定向到本地或内网地址")
	}
	return nil
}

func (resolver *Resolver) httpClient(transport *http.Transport) *http.Client {
	if resolver.transport != nil && resolver.transport == transport {
		if resolver.client == nil {
			resolver.client = &http.Client{
				Transport:     transport,
				Timeout:       30 * time.Second,
				CheckRedirect: checkImageRedirect,
			}
		}
		return resolver.client
	}
	return &http.Client{
		Transport:     transport,
		Timeout:       30 * time.Second,
		CheckRedirect: checkImageRedirect,
	}
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
	var prevalidatedFormat string
	var pooledReader *bufio.Reader
	defer func() {
		if pooledReader != nil {
			pooledReader.Reset(nil)
			resolvePeekPool.Put(pooledReader)
		}
	}()
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
		client := resolver.httpClient(transport)
		request := (&http.Request{
			Method:     http.MethodGet,
			URL:        parsed,
			Proto:      "HTTP/1.1",
			ProtoMajor: 1,
			ProtoMinor: 1,
			Host:       parsed.Host,
		}).WithContext(ctx)
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
		// Reject login/error pages before downloading the whole response or
		// creating a temporary file. The complete validation below remains required.
		buffered := resolvePeekPool.Get().(*bufio.Reader)
		buffered.Reset(contextio.NewReader(ctx, response.Body))
		pooledReader = buffered
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
	} else {
		if len(item.Data) == 0 {
			return "", false, unsupported()
		}
		if len(item.Data) > maxImage {
			return "", false, fmt.Errorf("图片超过 64 MiB 限制")
		}
		header := item.Data[:min(12, len(item.Data))]
		if !bytes.HasPrefix(header, []byte("\xff\xd8\xff")) &&
			!bytes.HasPrefix(header, []byte("\x89PNG\r\n\x1a\n")) &&
			!(len(header) >= 12 && string(header[:4]) == "RIFF" && string(header[8:12]) == "WEBP") {
			return "", false, fmt.Errorf("无法识别图片，仅支持静态 JPEG/PNG/WebP")
		}
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", false, fmt.Errorf("无法创建图片目录：%w", err)
	}
	if len(item.Data) > 0 {
		prevalidatedFormat, err = inspectInMemory(ctx, item.Data)
		if err != nil {
			return "", false, err
		}
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
	var size int64
	if reader == nil {
		written, writeErr := temp.Write(item.Data)
		size, err = int64(written), writeErr
	} else {
		copyBuf := resolveCopyBufPool.Get().(*[32 << 10]byte)
		size, err = io.CopyBuffer(struct{ io.Writer }{temp}, io.LimitReader(contextio.NewReader(ctx, reader), maxImage+1), copyBuf[:])
		resolveCopyBufPool.Put(copyBuf)
	}
	if err != nil {
		return "", false, err
	}
	if size > maxImage {
		return "", false, fmt.Errorf("图片超过 64 MiB 限制")
	}
	format := prevalidatedFormat
	if format == "" {
		info, err := compress.InspectReader(ctx, temp, size)
		if err != nil {
			return "", false, err
		}
		if _, err := temp.Seek(0, io.SeekStart); err != nil {
			return "", false, err
		}
		if _, _, err := image.Decode(contextio.NewReader(ctx, temp)); err != nil {
			return "", false, fmt.Errorf("图片数据损坏：%w", err)
		}
		format = info.Format
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
	if format == "jpeg" {
		extension = ".jpg"
	} else if format == "webp" {
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

func isLocalOrPrivateHost(host string) bool {
	lower := strings.ToLower(strings.TrimSuffix(host, "."))
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") {
		return true
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	return addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() || addr.IsUnspecified()
}
