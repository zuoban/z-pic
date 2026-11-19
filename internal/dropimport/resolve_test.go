package dropimport

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/transfer"
)

func TestResolveRejectsInvalidRequestBeforeWork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if path, saved, err := Resolve(ctx, Item{Path: "/tmp/local.png"}, ""); path != "" || saved || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled local input accepted: %q, %v, %v", path, saved, err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	if path, saved, err := Resolve(context.Background(), Item{URL: server.URL}, ""); path != "" || saved || err == nil || !strings.Contains(err.Error(), "目录不能为空") {
		t.Fatalf("invalid destination not rejected first: %q, %v, %v", path, saved, err)
	}
	if requests.Load() != 0 {
		t.Fatal("invalid destination caused a network request")
	}
}

func newLoopbackTransport(tb testing.TB) *http.Transport {
	tb.Helper()
	transport := NewTransport()
	transport.Proxy = nil
	transport.DialContext = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	tb.Cleanup(transport.CloseIdleConnections)
	return transport
}

func imageBytes(t *testing.T, format string) []byte {
	t.Helper()
	if format == "webp" {
		payload, err := os.ReadFile("testdata/fixture.webp")
		if err != nil {
			t.Fatal(err)
		}
		return payload
	}
	pixels := image.NewRGBA(image.Rect(0, 0, 3, 2))
	pixels.Set(0, 0, color.RGBA{255, 0, 0, 255})
	var output bytes.Buffer
	var err error
	if format == "jpeg" {
		err = jpeg.Encode(&output, pixels, nil)
	} else {
		err = png.Encode(&output, pixels)
	}
	if err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestResolveOriginalsAndCollisions(t *testing.T) {
	for _, format := range []string{"jpeg", "png", "webp"} {
		t.Run(format, func(t *testing.T) {
			payload := imageBytes(t, format)
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Header.Get("Cookie") != "" || request.Header.Get("Authorization") != "" {
					t.Error("unexpected credentials")
				}
				writer.Header().Set("Content-Type", "application/octet-stream")
				writer.Header().Set("Set-Cookie", "session=secret")
				writer.Write(payload)
			}))
			defer server.Close()
			dir := filepath.Join(t.TempDir(), "private")
			extension := ".png"
			if format == "jpeg" {
				extension = ".jpg"
			} else if format == "webp" {
				extension = ".webp"
			}
			transport := newLoopbackTransport(t)
			first, downloaded, err := ResolveWithTransport(context.Background(), Item{URL: server.URL + "/no-extension"}, dir, transport)
			if err != nil || !downloaded || filepath.Base(first) != "no-extension"+extension {
				t.Fatalf("Resolve = %q, %v, %v", first, downloaded, err)
			}
			second, _, err := ResolveWithTransport(context.Background(), Item{URL: server.URL + "/no-extension"}, dir, transport)
			if err != nil || second == first {
				t.Fatalf("collision = %q, %v", second, err)
			}
			for _, path := range []string{first, second} {
				actual, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(actual, payload) {
					t.Fatalf("original changed: %v", err)
				}
				info, _ := os.Stat(path)
				if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
					t.Fatalf("permissions: %v", info.Mode())
				}
			}
			info, _ := os.Stat(dir)
			if runtime.GOOS != "windows" && info.Mode().Perm() != 0700 {
				t.Fatalf("directory permissions: %v", info.Mode())
			}
			assertFileCount(t, dir, 2)
		})
	}
}

func TestResolveBytesAndLocal(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not-created")
	path, downloaded, err := Resolve(context.Background(), Item{Path: "/unchanged/missing.png"}, dir)
	if err != nil || downloaded || path != "/unchanged/missing.png" {
		t.Fatalf("local = %q, %v, %v", path, downloaded, err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("local resolution created directory")
	}
	if _, _, err := Resolve(context.Background(), Item{Data: []byte("not an image")}, dir); err == nil {
		t.Fatal("non-image in-memory payload accepted")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("non-image in-memory payload created destination directory")
	}
	payload := imageBytes(t, "png")
	path, downloaded, err = Resolve(context.Background(), Item{Data: payload}, dir)
	if err != nil || !downloaded || filepath.Ext(path) != ".png" {
		t.Fatalf("data = %q, %v, %v", path, downloaded, err)
	}
	actual, _ := os.ReadFile(path)
	if !bytes.Equal(actual, payload) {
		t.Fatal("data changed")
	}
}

func TestResolveWebPRepresentations(t *testing.T) {
	payload := imageBytes(t, "webp")
	pixels, format, err := image.Decode(bytes.NewReader(payload))
	if err != nil || format != "webp" {
		t.Fatalf("fixture decode = %q, %v", format, err)
	}
	hasAlpha := false
	for row := pixels.Bounds().Min.Y; row < pixels.Bounds().Max.Y; row++ {
		for column := pixels.Bounds().Min.X; column < pixels.Bounds().Max.X; column++ {
			_, _, _, alpha := pixels.At(column, row).RGBA()
			hasAlpha = hasAlpha || alpha < 0xffff
		}
	}
	if !hasAlpha {
		t.Fatal("WebP fixture has no transparency")
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "image/png")
		writer.Write(payload)
	}))
	defer server.Close()
	raw := "data:image/webp;base64," + base64.StdEncoding.EncodeToString(payload)
	tests := []struct {
		name string
		rep  transfer.Representation
		want string
	}{
		{"raw", transfer.Bytes("image/webp", payload), "image-1.webp"},
		{"text data URL", representation(transfer.Text, raw), "image-2.webp"},
		{"HTML data URL", representation(transfer.HTML, `<img src="`+raw+`">`), "image-3.webp"},
		{"misleading URL and MIME", representation(transfer.HTML, `<img src="`+server.URL+`/photo.jpg">`), "photo.webp"},
	}
	dir := t.TempDir()
	sentinel := filepath.Join(dir, "image.webp")
	if err := os.WriteFile(sentinel, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			items, err := Parse(transfer.New(transfer.NewItem(test.rep)))
			if err != nil || len(items) != 1 {
				t.Fatalf("Parse = %#v, %v", items, err)
			}
			path, downloaded, err := ResolveWithTransport(context.Background(), items[0], dir, newLoopbackTransport(t))
			if err != nil || !downloaded || filepath.Base(path) != test.want {
				t.Fatalf("Resolve = %q, %v, %v", path, downloaded, err)
			}
			actual, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(actual, payload) {
				t.Fatalf("WebP source bytes changed: %v", err)
			}
		})
	}
	kept, err := os.ReadFile(sentinel)
	if err != nil || string(kept) != "keep" {
		t.Fatalf("existing WebP overwritten: %v", err)
	}
	assertFileCount(t, dir, len(tests)+1)
}

func TestResolveWebPValidationAndCleanup(t *testing.T) {
	payload := imageBytes(t, "webp")
	extended := make([]byte, 18)
	copy(extended, "VP8X")
	binary.LittleEndian.PutUint32(extended[4:], 10)
	extended[8] = 0x12
	extended[12] = 2
	extended[15] = 1
	animatedFlag := append([]byte(nil), payload[:12]...)
	animatedFlag = append(animatedFlag, extended...)
	animatedFlag = append(animatedFlag, payload[12:]...)
	binary.LittleEndian.PutUint32(animatedFlag[4:], uint32(len(animatedFlag)-8))
	tests := []struct {
		name    string
		payload []byte
	}{
		{"VP8X animation flag", animatedFlag},
		{"ANIM chunk", appendWebPChunk(payload, "ANIM", make([]byte, 6))},
		{"ANMF chunk", appendWebPChunk(payload, "ANMF", make([]byte, 16))},
		{"truncated", payload[:len(payload)-1]},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			path, downloaded, err := Resolve(context.Background(), Item{Data: test.payload}, dir)
			if err == nil || path != "" || downloaded {
				t.Fatalf("invalid WebP accepted: %q, %v, %v", path, downloaded, err)
			}
			if test.name != "truncated" && !strings.Contains(err.Error(), "动画") {
				t.Fatalf("animation rejection missing: %v", err)
			}
			assertFileCount(t, dir, 0)
		})
	}
}

func appendWebPChunk(payload []byte, kind string, data []byte) []byte {
	result := append([]byte(nil), payload...)
	chunk := make([]byte, 8+len(data)+(len(data)%2))
	copy(chunk, kind)
	binary.LittleEndian.PutUint32(chunk[4:], uint32(len(data)))
	copy(chunk[8:], data)
	result = append(result, chunk...)
	binary.LittleEndian.PutUint32(result[4:], uint32(len(result)-8))
	return result
}

func TestResolveHTTPFailuresAndCleanup(t *testing.T) {
	payload := imageBytes(t, "png")
	tests := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"html", func(writer http.ResponseWriter, request *http.Request) { writer.Write([]byte("<html>login</html>")) }},
		{"error", func(writer http.ResponseWriter, request *http.Request) { writer.WriteHeader(http.StatusUnauthorized) }},
		{"invalid image", func(writer http.ResponseWriter, request *http.Request) { writer.Write(payload[:len(payload)-15]) }},
		{"redirect loop", func(writer http.ResponseWriter, request *http.Request) {
			http.Redirect(writer, request, "/loop", http.StatusFound)
		}},
		{"redirect scheme", func(writer http.ResponseWriter, request *http.Request) {
			http.Redirect(writer, request, "file:///tmp/a.png", http.StatusFound)
		}},
		{"redirect credentials", func(writer http.ResponseWriter, request *http.Request) {
			http.Redirect(writer, request, "http://user:password@127.0.0.1/a", http.StatusFound)
		}},
		{"declared oversize", func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Length", "67108865")
		}},
		{"streamed oversize", func(writer http.ResponseWriter, request *http.Request) {
			writer.WriteHeader(http.StatusOK)
			writer.(http.Flusher).Flush()
			block := make([]byte, 1<<20)
			for index := 0; index < 65; index++ {
				if _, err := writer.Write(block); err != nil {
					return
				}
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(test.handler)
			defer server.Close()
			dir := t.TempDir()
			path, downloaded, err := ResolveWithTransport(context.Background(), Item{URL: server.URL}, dir, newLoopbackTransport(t))
			if err == nil || path != "" || downloaded {
				t.Fatalf("accepted bad response: %q, %v, %v", path, downloaded, err)
			}
			assertFileCount(t, dir, 0)
		})
	}
}

func TestResolveRedirectAndCancel(t *testing.T) {
	payload := imageBytes(t, "jpeg")
	entered := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/redirect":
			http.Redirect(writer, request, "/image", http.StatusFound)
		case "/image":
			writer.Write(payload)
		default:
			writer.WriteHeader(http.StatusOK)
			writer.(http.Flusher).Flush()
			entered <- struct{}{}
			<-request.Context().Done()
		}
	}))
	defer server.Close()
	loopbackTransport := newLoopbackTransport(t)
	path, _, err := ResolveWithTransport(context.Background(), Item{URL: server.URL + "/redirect"}, t.TempDir(), loopbackTransport)
	if err != nil || filepath.Ext(path) != ".jpg" {
		t.Fatalf("redirect: %q, %v", path, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	finished := make(chan error, 1)
	go func() {
		_, _, err := ResolveWithTransport(ctx, Item{URL: server.URL + "/slow"}, dir, loopbackTransport)
		finished <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not start")
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled download hung")
	}
	assertFileCount(t, dir, 0)
	_, _, err = Resolve(ctx, Item{Data: payload}, dir)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancel: %v", err)
	}
}

func TestResolveValidationAndSafeName(t *testing.T) {
	payload := imageBytes(t, "png")
	animated := append([]byte(nil), payload[:33]...)
	animated = append(animated, pngChunk("acTL", []byte{0, 0, 0, 1, 0, 0, 0, 0})...)
	animated = append(animated, payload[33:]...)
	lateAnimation := append([]byte(nil), payload[:len(payload)-12]...)
	lateAnimation = append(lateAnimation, pngChunk("acTL", []byte{0, 0, 0, 1, 0, 0, 0, 0})...)
	lateAnimation = append(lateAnimation, payload[len(payload)-12:]...)
	huge := append([]byte(nil), payload...)
	binary.BigEndian.PutUint32(huge[16:20], 100000)
	binary.BigEndian.PutUint32(huge[20:24], 100000)
	binary.BigEndian.PutUint32(huge[29:33], crc32.ChecksumIEEE(huge[12:29]))
	for _, bad := range [][]byte{animated, lateAnimation, huge, []byte("<html>"), payload[:40], make([]byte, maxImage+1)} {
		dir := t.TempDir()
		if _, _, err := Resolve(context.Background(), Item{Data: bad}, dir); err == nil {
			t.Fatal("invalid bytes accepted")
		}
		assertFileCount(t, dir, 0)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) { writer.Write(payload) }))
	defer server.Close()
	dir := t.TempDir()
	sentinel := filepath.Join(dir, "image.png")
	if err := os.WriteFile(sentinel, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	path, _, err := ResolveWithTransport(context.Background(), Item{URL: server.URL + "/%2E%2E%2F%2E%2E%2F%3F.jpg"}, dir, newLoopbackTransport(t))
	if err != nil || filepath.Dir(path) != dir || !strings.HasSuffix(path, ".png") {
		t.Fatalf("unsafe name: %q, %v", path, err)
	}
	kept, _ := os.ReadFile(sentinel)
	if string(kept) != "keep" {
		t.Fatal("overwrote existing file")
	}
	for _, raw := range []string{"file:///tmp/image", "https://user:pass@localhost/image", "blob:https://localhost/image"} {
		if _, _, err := Resolve(context.Background(), Item{URL: raw}, dir); err == nil {
			t.Fatalf("accepted URL %q", raw)
		}
	}
}

func TestResolveBytesRejectsMalformedPNGChunks(t *testing.T) {
	payload := imageBytes(t, "png")
	end := len(payload) - 12
	lateAnimation := append(bytes.Clone(payload[:end]), pngChunk("acTL", make([]byte, 8))...)
	lateAnimation = append(lateAnimation, payload[end:]...)
	badLength := bytes.Clone(payload)
	binary.BigEndian.PutUint32(badLength[end:], 0xffffffff)
	for _, input := range [][]byte{payload[:end], payload[:len(payload)-1], badLength, lateAnimation} {
		dir := t.TempDir()
		if path, saved, err := Resolve(context.Background(), Item{Data: input}, dir); err == nil || saved || path != "" {
			t.Fatalf("malformed PNG saved: %q, %v, %v", path, saved, err)
		}
		assertFileCount(t, dir, 0)
	}
}

func TestResolveWithSharedTransportPreservesOriginals(test *testing.T) {
	payload := imageBytes(test, "png")
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		writer.Header().Set("Content-Type", "image/png")
		_, _ = writer.Write(payload)
	}))
	defer server.Close()

	dir := filepath.Join(test.TempDir(), "downloads")
	transport := newLoopbackTransport(test)

	first, firstSaved, err := ResolveWithTransport(context.Background(), Item{URL: server.URL + "/batch.png"}, dir, transport)
	if err != nil || !firstSaved {
		test.Fatalf("first ResolveWithTransport = %q, %v, %v", first, firstSaved, err)
	}
	second, secondSaved, err := ResolveWithTransport(context.Background(), Item{URL: server.URL + "/batch.png"}, dir, transport)
	if err != nil || !secondSaved || second == first {
		test.Fatalf("second ResolveWithTransport = %q, %v, %v", second, secondSaved, err)
	}
	if requests != 2 {
		test.Fatalf("expected 2 HTTP requests, got %d", requests)
	}
	for _, resolvedPath := range []string{first, second} {
		actual, readErr := os.ReadFile(resolvedPath)
		if readErr != nil || !bytes.Equal(actual, payload) {
			test.Fatalf("resolved payload mismatch for %s: %v", resolvedPath, readErr)
		}
	}
	assertFileCount(test, dir, 2)
}

func pngChunk(kind string, payload []byte) []byte {
	chunk := make([]byte, 12+len(payload))
	binary.BigEndian.PutUint32(chunk, uint32(len(payload)))
	copy(chunk[4:], kind)
	copy(chunk[8:], payload)
	binary.BigEndian.PutUint32(chunk[len(chunk)-4:], crc32.ChecksumIEEE(chunk[4:len(chunk)-4]))
	return chunk
}

func assertFileCount(t *testing.T, dir string, want int) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != want {
		t.Fatalf("directory contents = %v, %v; want %d files", entries, err, want)
	}
}

func TestResolveBlocksPublicToLocalOrPrivateRedirect(t *testing.T) {
	var localHits atomic.Int32
	localTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		localHits.Add(1)
		_, _ = w.Write(imageBytes(t, "png"))
	}))
	defer localTarget.Close()

	for _, redirectURL := range []string{
		localTarget.URL + "/secret.png",
		"http://localhost/secret.png",
		"http://[::1]/secret.png",
		"http://169.254.169.254/latest/meta-data",
		"http://192.168.1.1/admin",
	} {
		publicRedirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, redirectURL, http.StatusFound)
		}))
		redirectorAddr := publicRedirector.Listener.Addr().String()
		transport := NewTransport()
		transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			if strings.HasPrefix(addr, "cdn.example.com:") {
				return (&net.Dialer{}).DialContext(ctx, network, redirectorAddr)
			}
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		}
		dir := t.TempDir()
		path, saved, err := ResolveWithTransport(context.Background(), Item{URL: "http://cdn.example.com/photo.png"}, dir, transport)
		transport.CloseIdleConnections()
		publicRedirector.Close()
		if err == nil || saved || path != "" || !strings.Contains(err.Error(), "重定向到本地或内网") {
			t.Fatalf("redirect to %s should be blocked: path=%q saved=%v err=%v", redirectURL, path, saved, err)
		}
		if localHits.Load() != 0 {
			t.Fatalf("redirect to %s reached local target", redirectURL)
		}
	}
}

func TestResolveBlocksFirstHopPrivateLoopbackAndDNSRebinding(t *testing.T) {
	var localHits atomic.Int32
	localServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		localHits.Add(1)
		_, _ = w.Write(imageBytes(t, "png"))
	}))
	defer localServer.Close()

	for _, blockedURL := range []string{
		localServer.URL + "/secret.png",
		"http://localhost/secret.png",
		"http://sub.localhost/secret.png",
		"http://127.0.0.1/secret.png",
		"http://[::1]/secret.png",
		"http://[::ffff:127.0.0.1]/secret.png",
		"http://0.0.0.0/secret.png",
		"http://0.0.0.1/secret.png",
		"http://10.0.0.1/secret.png",
		"http://100.100.100.200/latest/meta-data",
		"http://169.254.169.254/latest/meta-data",
		"http://172.16.0.1/secret.png",
		"http://192.168.1.1/admin",
		"http://[64:ff9b::7f00:1]/secret.png",
		"http://[fe80::1]/secret.png",
		"http://[fd00::1]/secret.png",
	} {
		dir := t.TempDir()
		path, saved, err := Resolve(context.Background(), Item{URL: blockedURL}, dir)
		if err == nil || saved || path != "" || !strings.Contains(err.Error(), "本地或内网") {
			t.Fatalf("first-hop %s must be blocked: path=%q saved=%v err=%v", blockedURL, path, saved, err)
		}
		assertFileCount(t, dir, 0)
	}
	if localHits.Load() != 0 {
		t.Fatalf("default Resolve reached loopback server %d times", localHits.Load())
	}

	// Verify dial-layer DNS resolution rejects private/dual-record responses and
	// dials validated literal public IPs directly (preventing DNS rebinding TOCTOU).
	payload := imageBytes(t, "png")
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect-rebind" {
			http.Redirect(w, r, "http://rebind.example.test/secret.png", http.StatusFound)
			return
		}
		_, _ = w.Write(payload)
	}))
	defer origin.Close()
	originAddr := origin.Listener.Addr().String()

	for _, dnsRecords := range [][]netip.Addr{
		{netip.MustParseAddr("127.0.0.1")},
		{netip.MustParseAddr("100.100.100.200")},
		{netip.MustParseAddr("169.254.169.254")},
		{netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("127.0.0.1")},
	} {
		var dialedAddrs []string
		transport := newTransport(
			func(_ context.Context, _, host string) ([]netip.Addr, error) {
				if host == "cdn.example.test" {
					return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
				}
				return dnsRecords, nil
			},
			func(ctx context.Context, network, addr string) (net.Conn, error) {
				dialedAddrs = append(dialedAddrs, addr)
				if addr == "93.184.216.34:80" {
					return (&net.Dialer{}).DialContext(ctx, network, originAddr)
				}
				return (&net.Dialer{}).DialContext(ctx, network, localServer.Listener.Addr().String())
			},
		)
		dir := t.TempDir()
		if path, saved, err := ResolveWithTransport(context.Background(), Item{URL: "http://rebind.example.test/secret.png"}, dir, transport); err == nil || saved || path != "" || !strings.Contains(err.Error(), "本地或内网") {
			t.Fatalf("DNS rebinding %v on first hop not blocked: %q %v %v", dnsRecords, path, saved, err)
		}
		if len(dialedAddrs) != 0 {
			t.Fatalf("blocked DNS response still dialed %v", dialedAddrs)
		}
		if path, saved, err := ResolveWithTransport(context.Background(), Item{URL: "http://cdn.example.test/redirect-rebind"}, dir, transport); err == nil || saved || path != "" || !strings.Contains(err.Error(), "本地或内网") {
			t.Fatalf("DNS rebinding %v on redirect hop not blocked: %q %v %v", dnsRecords, path, saved, err)
		}
		if len(dialedAddrs) != 1 || dialedAddrs[0] != "93.184.216.34:80" {
			t.Fatalf("expected literal public IP dial only once, got %v", dialedAddrs)
		}
		transport.CloseIdleConnections()
	}
	if localHits.Load() != 0 {
		t.Fatalf("DNS rebinding reached local server %d times", localHits.Load())
	}
}

func TestResolveBlocksSSRFViaProxyAndDirectProxyAddressBypass(t *testing.T) {
	payload := imageBytes(t, "png")
	var proxyHits atomic.Int32
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyHits.Add(1)
		if r.URL.Path == "/redirect-private" {
			http.Redirect(w, r, "http://internal.example.test/secret.png", http.StatusFound)
			return
		}
		_, _ = w.Write(payload)
	}))
	defer proxyServer.Close()
	proxyURL, err := url.Parse(proxyServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxyHostPort := "proxy.internal:" + proxyURL.Port()
	namedProxyURL, err := url.Parse("http://" + proxyHostPort)
	if err != nil {
		t.Fatal(err)
	}

	lookup := func(_ context.Context, _, host string) ([]netip.Addr, error) {
		switch host {
		case "cdn.example.test":
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		case "internal.example.test":
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		case "dual.example.test":
			return []netip.Addr{netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("10.0.0.5")}, nil
		case "proxy.internal":
			return []netip.Addr{netip.MustParseAddr("10.0.0.8")}, nil
		default:
			return nil, fmt.Errorf("unexpected host %s", host)
		}
	}
	var dialed []string
	baseDial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialed = append(dialed, addr)
		if strings.EqualFold(addr, proxyHostPort) {
			return (&net.Dialer{}).DialContext(ctx, network, proxyServer.Listener.Addr().String())
		}
		return nil, fmt.Errorf("unexpected dial %s", addr)
	}
	proxyLookup := func(req *http.Request) (*url.URL, error) {
		// Simulate NO_PROXY for proxy.internal itself.
		if strings.EqualFold(req.URL.Hostname(), "proxy.internal") {
			return nil, nil
		}
		return namedProxyURL, nil
	}
	transport := newTransportWithProxy(lookup, baseDial, proxyLookup)
	defer transport.CloseIdleConnections()

	// 1. Public host through proxy succeeds.
	dir := t.TempDir()
	savedPath, saved, err := ResolveWithTransport(context.Background(), Item{URL: "http://cdn.example.test/ok.png"}, dir, transport)
	if err != nil || !saved || savedPath == "" {
		t.Fatalf("public host via proxy must succeed: path=%q saved=%v err=%v", savedPath, saved, err)
	}
	if proxyHits.Load() != 1 {
		t.Fatalf("expected 1 proxy hit, got %d", proxyHits.Load())
	}

	// 2. Host resolving to private/loopback IP must be blocked before contacting proxy.
	for _, blocked := range []string{
		"http://internal.example.test/secret.png",
		"http://dual.example.test/secret.png",
	} {
		path, ok, err := ResolveWithTransport(context.Background(), Item{URL: blocked}, dir, transport)
		if err == nil || ok || path != "" || !strings.Contains(err.Error(), "本地或内网") {
			t.Fatalf("private DNS host %s via proxy must be blocked: path=%q saved=%v err=%v", blocked, path, ok, err)
		}
	}
	if proxyHits.Load() != 1 {
		t.Fatalf("blocked private hosts reached proxy: hits=%d", proxyHits.Load())
	}

	// 3. Redirect through proxy to a DNS name resolving to private IP must be blocked.
	if path, ok, err := ResolveWithTransport(context.Background(), Item{URL: "http://cdn.example.test/redirect-private"}, dir, transport); err == nil || ok || path != "" || !strings.Contains(err.Error(), "本地或内网") {
		t.Fatalf("redirect to private DNS host via proxy must be blocked: path=%q saved=%v err=%v", path, ok, err)
	}
	if proxyHits.Load() != 2 {
		t.Fatalf("redirect target must not contact proxy: hits=%d", proxyHits.Load())
	}

	// 4. Direct request to the proxy's own internal host:port (via NO_PROXY) must NOT bypass SSRF.
	beforeDials := len(dialed)
	directProxyURL := "http://" + proxyHostPort + "/admin.png"
	if path, ok, err := ResolveWithTransport(context.Background(), Item{URL: directProxyURL}, dir, transport); err == nil || ok || path != "" || !strings.Contains(err.Error(), "本地或内网") {
		t.Fatalf("direct request to proxy address must be blocked: path=%q saved=%v err=%v", path, ok, err)
	}
	if len(dialed) != beforeDials {
		t.Fatalf("direct request to proxy address bypassed dial filter: dialed=%v", dialed[beforeDials:])
	}

	// 5. Even standalone http.Client{Transport} without Resolver context must block direct hit to proxy.internal.
	client := &http.Client{Transport: transport}
	resp, err := client.Get("http://cdn.example.test/ok.png")
	if err != nil {
		t.Fatalf("standalone public request via proxy failed: %v", err)
	}
	_ = resp.Body.Close()
	beforeDials = len(dialed)
	resp, err = client.Get(directProxyURL)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "本地或内网") {
		t.Fatalf("standalone direct request to proxy address must be blocked, got err=%v", err)
	}
	if len(dialed) != beforeDials {
		t.Fatalf("standalone direct request bypassed dial filter: dialed=%v", dialed[beforeDials:])
	}
}
