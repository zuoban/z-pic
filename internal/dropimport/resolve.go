package dropimport

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
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
	return compress.VerifyImageData(ctx, payload)
}

func hasSupportedHeader(header []byte) bool {
	if bytes.HasPrefix(header, []byte("\xff\xd8\xff")) ||
		bytes.HasPrefix(header, []byte("\x89PNG\r\n\x1a\n")) ||
		(len(header) >= 12 && string(header[:4]) == "RIFF" && string(header[8:12]) == "WEBP") {
		return true
	}
	return (compress.SupportsHEIC() && compress.SniffHEICHeader(header)) ||
		(compress.SupportsAVIF() && compress.SniffAVIFHeader(header))
}

func unsupportedStaticImageError() error {
	formats := "JPEG/PNG/WebP"
	if compress.SupportsAVIF() {
		formats += "/AVIF"
	}
	if compress.SupportsHEIC() {
		formats += "/HEIC"
	}
	return fmt.Errorf("无法识别图片，仅支持静态 %s", formats)
}

// Remote and byte inputs are saved verbatim after header inspection and
// full decoding of the supported static image.
// The caller owns successful downloaded files and their eventual removal.
func Resolve(ctx context.Context, item Item, dir string) (path string, downloaded bool, err error) {
	return ResolveWithTransport(ctx, item, dir, nil)
}

type (
	lookupNetIPFunc func(ctx context.Context, network, host string) ([]netip.Addr, error)
	dialContextFunc func(ctx context.Context, network, addr string) (net.Conn, error)
	proxyLookupFunc func(req *http.Request) (*url.URL, error)
	fakeIPRouteFunc func(addr netip.Addr) bool
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
	return newTransportWithOptions(lookup, baseDial, proxyLookup, hasLocalFakeIPv4Route)
}

func newTransportWithFakeIPRoute(lookup lookupNetIPFunc, baseDial dialContextFunc, hasFakeIPRoute fakeIPRouteFunc) *http.Transport {
	return newTransportWithOptions(lookup, baseDial, http.ProxyFromEnvironment, hasFakeIPRoute)
}

func newTransportWithOptions(lookup lookupNetIPFunc, baseDial dialContextFunc, proxyLookup proxyLookupFunc, hasFakeIPRoute fakeIPRouteFunc) *http.Transport {
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
					if _, err := resolveAllowedIPs(req.Context(), lookup, host, hasFakeIPRoute); err != nil {
						return nil, err
					}
				}
			}
			return nil, nil
		}
		// DialContext connects to the proxy rather than the origin when a proxy is active,
		// so resolve and validate the target host here for both first-hop and redirects.
		if _, err := resolveAllowedIPs(req.Context(), lookup, host, hasFakeIPRoute); err != nil {
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
		DialContext:            newSafeDialContext(lookup, baseDial, isAllowedProxy, hasFakeIPRoute),
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

func resolveAllowedIPs(ctx context.Context, lookup lookupNetIPFunc, host string, hasFakeIPRoute fakeIPRouteFunc) ([]netip.Addr, error) {
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
	allowFakeIP := isFakeIPCandidateHost(trimmed)
	hasFakeIPv4 := false
	if allowFakeIP {
		for _, ip := range ips {
			if isFakeIPv4(ip) {
				hasFakeIPv4 = true
				break
			}
		}
	}
	if allowFakeIP && hasFakeIPv4 {
		// A legitimate TUN Fake-IP DNS interceptor only returns Fake-IPs for a
		// public domain, never mixing public or other private/internal records.
		// Never dial IPv6 ULA (fc00::/18) directly: keep only verified IPv4
		// Fake-IPs routed to a local TUN interface so the proxy handles upstream
		// IPv4/IPv6 resolution safely.
		routeCheck := hasFakeIPRoute
		if routeCheck == nil {
			routeCheck = hasLocalFakeIPv4Route
		}
		fakeIPv4s := make([]netip.Addr, 0, len(ips))
		for _, ip := range ips {
			switch {
			case isFakeIPv4(ip):
				unmapped := ip.Unmap()
				if !routeCheck(unmapped) {
					return nil, fmt.Errorf("拒绝连接本地或内网地址：%s", host)
				}
				fakeIPv4s = append(fakeIPv4s, unmapped)
			case isFakeIPv6(ip):
				// Ignore paired dual-stack Fake-IPv6 answers rather than dialing
				// IPv6 ULA addresses directly.
				continue
			default:
				return nil, fmt.Errorf("拒绝连接本地或内网地址：%s", host)
			}
		}
		if len(fakeIPv4s) == 0 {
			return nil, fmt.Errorf("拒绝连接本地或内网地址：%s", host)
		}
		return fakeIPv4s, nil
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

func newSafeDialContext(lookup lookupNetIPFunc, baseDial dialContextFunc, isAllowedProxy func(string) bool, hasFakeIPRoute fakeIPRouteFunc) dialContextFunc {
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
		allowed, err := resolveAllowedIPs(ctx, lookup, host, hasFakeIPRoute)
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
	mu        sync.Mutex
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
		resolver.mu.Lock()
		if resolver.client == nil {
			resolver.client = &http.Client{
				Transport:     transport,
				Timeout:       30 * time.Second,
				CheckRedirect: checkImageRedirect,
			}
		}
		client := resolver.client
		resolver.mu.Unlock()
		return client
	}
	return &http.Client{
		Transport:     transport,
		Timeout:       30 * time.Second,
		CheckRedirect: checkImageRedirect,
	}
}

type filenameSequence = nameseq.Sequence

// StagedItem holds a downloaded and validated image awaiting deterministic filename assignment.
type StagedItem struct {
	localPath string
	tempName  string
	name      string
	extension string
}

// Discard removes any staged temporary file if Commit was not completed.
func (s *StagedItem) Discard() {
	if s != nil && s.tempName != "" {
		_ = os.Remove(s.tempName)
		s.tempName = ""
	}
}

func (resolver *Resolver) Resolve(ctx context.Context, item Item) (path string, downloaded bool, err error) {
	staged, err := resolver.Stage(ctx, item)
	if err != nil {
		return "", false, err
	}
	return resolver.Commit(ctx, staged)
}

// Stage downloads and validates item into a temporary file without reserving
// its final filename, allowing callers to stage multiple remote items concurrently
// and then call Commit in deterministic import order.
func (resolver *Resolver) Stage(ctx context.Context, item Item) (*StagedItem, error) {
	dir := resolver.dir
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if item.Path != "" {
		return &StagedItem{localPath: item.Path}, nil
	}
	if dir == "" {
		return nil, fmt.Errorf("图片保存目录不能为空")
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
			return nil, err
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
			return nil, fmt.Errorf("图片下载失败：%w", err)
		}
		defer response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return nil, fmt.Errorf("图片下载失败：HTTP %d", response.StatusCode)
		}
		if response.ContentLength > maxImage {
			return nil, fmt.Errorf("图片超过 64 MiB 限制")
		}
		// Reject login/error pages before downloading the whole response or
		// creating a temporary file. The complete validation below remains required.
		buffered := resolvePeekPool.Get().(*bufio.Reader)
		buffered.Reset(contextio.NewReader(ctx, response.Body))
		pooledReader = buffered
		header, err := buffered.Peek(12)
		if err != nil && err != io.EOF {
			return nil, err
		}
		if !hasSupportedHeader(header) {
			return nil, unsupportedStaticImageError()
		}
		reader = buffered
	} else {
		if len(item.Data) == 0 {
			return nil, unsupported()
		}
		if len(item.Data) > maxImage {
			return nil, fmt.Errorf("图片超过 64 MiB 限制")
		}
		header := item.Data[:min(12, len(item.Data))]
		if !hasSupportedHeader(header) {
			return nil, unsupportedStaticImageError()
		}
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("无法创建图片目录：%w", err)
	}
	if len(item.Data) > 0 {
		var inspectErr error
		prevalidatedFormat, inspectErr = inspectInMemory(ctx, item.Data)
		if inspectErr != nil {
			return nil, inspectErr
		}
	}
	temp, err := os.CreateTemp(dir, ".drop-*")
	if err != nil {
		return nil, err
	}
	tempName := temp.Name()
	tempClosed := false
	keepTemp := false
	defer func() {
		if !tempClosed {
			_ = temp.Close()
		}
		if !keepTemp {
			_ = os.Remove(tempName)
		}
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
		return nil, err
	}
	if size > maxImage {
		return nil, fmt.Errorf("图片超过 64 MiB 限制")
	}
	format := prevalidatedFormat
	if format == "" {
		info, err := compress.VerifyImageReader(ctx, temp, size)
		if err != nil {
			return nil, err
		}
		format = info.Format
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := temp.Close(); err != nil {
		tempClosed = true
		return nil, err
	}
	tempClosed = true
	extension := ".png"
	switch format {
	case "jpeg":
		extension = ".jpg"
	case "webp":
		extension = ".webp"
	case "heic", "avif":
		extension = "." + format
	}
	keepTemp = true
	return &StagedItem{tempName: tempName, name: name, extension: extension}, nil
}

// Commit assigns a collision-free filename in resolver.dir for a staged item and cleans up its temp file.
func (resolver *Resolver) Commit(ctx context.Context, staged *StagedItem) (path string, downloaded bool, err error) {
	if staged == nil {
		return "", false, unsupported()
	}
	defer staged.Discard()
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	if staged.localPath != "" {
		return staged.localPath, false, nil
	}
	dir := resolver.dir
	tempName := staged.tempName
	name := staged.name
	extension := staged.extension
	resolver.mu.Lock()
	defer resolver.mu.Unlock()
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

var (
	// Standard Fake-IP pools used by local TUN proxies (Clash/Mihomo, Surge,
	// Sing-box, Stash, etc.) when intercepting DNS lookups for public domains.
	fakeIPv4Prefix = netip.MustParsePrefix("198.18.0.0/15")
	fakeIPv6Prefix = netip.MustParsePrefix("fc00::/18")
)

func isFakeIPv4(addr netip.Addr) bool {
	if !addr.IsValid() || addr.Zone() != "" {
		return false
	}
	unmapped := addr.Unmap()
	return unmapped.Is4() && fakeIPv4Prefix.Contains(unmapped)
}

func isFakeIPv6(addr netip.Addr) bool {
	if !addr.IsValid() || addr.Zone() != "" {
		return false
	}
	unmapped := addr.Unmap()
	return unmapped.Is6() && fakeIPv6Prefix.Contains(unmapped)
}

func hasLocalFakeIPv4Route(addr netip.Addr) bool {
	if !isFakeIPv4(addr) {
		return false
	}
	unmapped := addr.Unmap()
	conn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: unmapped.AsSlice(), Port: 80})
	if err != nil {
		return false
	}
	localUDP, ok := conn.LocalAddr().(*net.UDPAddr)
	_ = conn.Close()
	if !ok || localUDP == nil {
		return false
	}
	localIP, ok := netip.AddrFromSlice(localUDP.IP)
	if !ok {
		return false
	}
	localIP = localIP.Unmap()
	if !localIP.Is4() {
		return false
	}
	if isFakeIPv4(localIP) {
		return true
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return false
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, ifAddr := range addrs {
			ipNet, ok := ifAddr.(*net.IPNet)
			if !ok {
				continue
			}
			ifIP, ok := netip.AddrFromSlice(ipNet.IP)
			if !ok || ifIP.Unmap() != localIP {
				continue
			}
			return isFakeIPTUNInterface(iface)
		}
	}
	return false
}

func isFakeIPTUNInterface(iface net.Interface) bool {
	if iface.Flags&net.FlagPointToPoint != 0 {
		return true
	}
	name := strings.ToLower(iface.Name)
	for _, prefix := range []string{"utun", "tun", "wintun", "mihomo", "clash", "sing-box", "surge", "stash"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

var fakeIPDisallowedHostSuffixes = []string{
	".local",
	".localhost",
	".localdomain",
	".arpa",
	".lan",
	".home",
	".corp",
	".internal",
	".intranet",
	".private",
	".invalid",
}

func isFakeIPCandidateHost(host string) bool {
	lower := strings.ToLower(strings.TrimSuffix(host, "."))
	if lower == "" || isLocalHostName(lower) || !strings.Contains(lower, ".") {
		return false
	}
	for _, suffix := range fakeIPDisallowedHostSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return false
		}
	}
	return true
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
