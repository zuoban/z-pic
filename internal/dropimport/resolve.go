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

type (
	lookupNetIPFunc func(ctx context.Context, network, host string) ([]netip.Addr, error)
	dialContextFunc func(ctx context.Context, network, addr string) (net.Conn, error)
	proxyLookupFunc func(req *http.Request) (*url.URL, error)
)

type proxyDialContextKey struct{}

type proxyDialTarget struct {
	mu   sync.RWMutex
	addr string
}

func (t *proxyDialTarget) set(addr string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.addr = addr
	t.mu.Unlock()
}

func (t *proxyDialTarget) matches(addr string) bool {
	if t == nil {
		return false
	}
	t.mu.RLock()
	matched := t.addr != "" && t.addr == addr
	t.mu.RUnlock()
	return matched
}

func NewTransport() *http.Transport {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	return newTransport(net.DefaultResolver.LookupNetIP, dialer.DialContext)
}

func newTransport(lookup lookupNetIPFunc, baseDial dialContextFunc) *http.Transport {
	return newTransportWithProxy(lookup, baseDial, http.ProxyFromEnvironment)
}

func newTransportWithProxy(lookup lookupNetIPFunc, baseDial dialContextFunc, proxyLookup proxyLookupFunc) *http.Transport {
	var proxyMu sync.RWMutex
	var allowedProxyAddrs map[string]struct{}

	isAllowedProxy := func(addr string) bool {
		proxyMu.RLock()
		_, ok := allowedProxyAddrs[strings.ToLower(addr)]
		proxyMu.RUnlock()
		return ok
	}

	proxyFunc := func(req *http.Request) (*url.URL, error) {
		target, hasTarget := req.Context().Value(proxyDialContextKey{}).(*proxyDialTarget)
		if hasTarget {
			target.set("")
		}
		host := req.URL.Hostname()
		if isLocalOrPrivateHost(host) {
			return nil, fmt.Errorf("拒绝连接本地或内网地址：%s", host)
		}
		var proxyURL *url.URL
		var err error
		if proxyLookup != nil {
			proxyURL, err = proxyLookup(req)
		}
		if err != nil {
			return nil, err
		}
		if proxyURL == nil {
			if !hasTarget {
				if reqAddr := urlDialAddr(req.URL); reqAddr != "" && isAllowedProxy(reqAddr) {
					if _, err := resolveAllowedIPs(req.Context(), lookup, host); err != nil {
						return nil, err
					}
				}
			}
			return nil, nil
		}
		// DialContext connects to the proxy rather than the origin when a proxy is active,
		// so resolve and validate the target host here for both first-hop and redirects.
		if _, err := resolveAllowedIPs(req.Context(), lookup, host); err != nil {
			return nil, err
		}
		proxyAddr := urlDialAddr(proxyURL)
		if hasTarget {
			target.set(proxyAddr)
		} else if proxyAddr != "" {
			proxyMu.Lock()
			if allowedProxyAddrs == nil {
				allowedProxyAddrs = make(map[string]struct{})
			}
			allowedProxyAddrs[proxyAddr] = struct{}{}
			proxyMu.Unlock()
		}
		return proxyURL, nil
	}

	return &http.Transport{
		Proxy:                  proxyFunc,
		DisableCompression:     true,
		DialContext:            newSafeDialContext(lookup, baseDial, isAllowedProxy),
		ForceAttemptHTTP2:      true,
		MaxIdleConns:           16,
		MaxIdleConnsPerHost:    2,
		IdleConnTimeout:        30 * time.Second,
		TLSHandshakeTimeout:    10 * time.Second,
		ResponseHeaderTimeout:  15 * time.Second,
		MaxResponseHeaderBytes: 1 << 20,
	}
}

func urlDialAddr(u *url.URL) string {
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return ""
	}
	port := u.Port()
	if port == "" {
		switch strings.ToLower(u.Scheme) {
		case "http":
			port = "80"
		case "https":
			port = "443"
		case "socks5", "socks5h":
			port = "1080"
		default:
			return ""
		}
	}
	return net.JoinHostPort(host, port)
}

func resolveAllowedIPs(ctx context.Context, lookup lookupNetIPFunc, host string) ([]netip.Addr, error) {
	trimmed := strings.TrimSuffix(host, ".")
	if isLocalHostName(trimmed) {
		return nil, fmt.Errorf("拒绝连接本地或内网地址：%s", host)
	}
	if ip, parseErr := netip.ParseAddr(trimmed); parseErr == nil {
		if isDisallowedIP(ip) {
			return nil, fmt.Errorf("拒绝连接本地或内网地址：%s", host)
		}
		return []netip.Addr{ip.Unmap()}, nil
	}
	ips, err := lookup(ctx, "ip", trimmed)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("无法解析域名：%s", host)
	}
	allowed := make([]netip.Addr, 0, len(ips))
	for _, ip := range ips {
		if isDisallowedIP(ip) {
			return nil, fmt.Errorf("拒绝连接本地或内网地址：%s", host)
		}
		allowed = append(allowed, ip.Unmap())
	}
	return allowed, nil
}

func newSafeDialContext(lookup lookupNetIPFunc, baseDial dialContextFunc, isAllowedProxy func(string) bool) dialContextFunc {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if target, hasTarget := ctx.Value(proxyDialContextKey{}).(*proxyDialTarget); hasTarget {
			if target.matches(strings.ToLower(addr)) {
				return baseDial(ctx, network, addr)
			}
		} else if isAllowedProxy != nil && isAllowedProxy(addr) {
			return baseDial(ctx, network, addr)
		}
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		allowed, err := resolveAllowedIPs(ctx, lookup, host)
		if err != nil {
			return nil, err
		}
		var lastErr error
		for _, ip := range allowed {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			conn, dialErr := baseDial(ctx, network, net.JoinHostPort(ip.String(), port))
			if dialErr == nil {
				return conn, nil
			}
			lastErr = dialErr
		}
		return nil, lastErr
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
	if isLocalOrPrivateHost(target.Hostname()) {
		for _, prev := range via {
			if !isLocalOrPrivateHost(prev.URL.Hostname()) {
				return fmt.Errorf("拒绝从公网地址重定向到本地或内网地址")
			}
		}
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
		reqCtx := context.WithValue(ctx, proxyDialContextKey{}, &proxyDialTarget{})
		request := (&http.Request{
			Method:     http.MethodGet,
			URL:        parsed,
			Proto:      "HTTP/1.1",
			ProtoMajor: 1,
			ProtoMinor: 1,
			Host:       parsed.Host,
		}).WithContext(reqCtx)
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

var disallowedIPPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/96"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/32"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("fec0::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

func isLocalHostName(host string) bool {
	lower := strings.ToLower(strings.TrimSuffix(host, "."))
	return lower == "localhost" || strings.HasSuffix(lower, ".localhost")
}

func isDisallowedIP(addr netip.Addr) bool {
	if !addr.IsValid() || addr.Zone() != "" {
		return true
	}
	unmapped := addr.Unmap()
	if !unmapped.IsGlobalUnicast() || unmapped.IsLoopback() || unmapped.IsPrivate() ||
		unmapped.IsLinkLocalUnicast() || unmapped.IsLinkLocalMulticast() ||
		unmapped.IsMulticast() || unmapped.IsUnspecified() {
		return true
	}
	for _, prefix := range disallowedIPPrefixes {
		if prefix.Contains(addr) || prefix.Contains(unmapped) {
			return true
		}
	}
	return false
}

func isLocalOrPrivateHost(host string) bool {
	trimmed := strings.TrimSuffix(host, ".")
	if isLocalHostName(trimmed) {
		return true
	}
	addr, err := netip.ParseAddr(trimmed)
	if err != nil {
		return false
	}
	return isDisallowedIP(addr)
}
