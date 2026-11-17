package dropimport

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/transfer"
)

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
			first, downloaded, err := Resolve(context.Background(), Item{URL: server.URL + "/no-extension"}, dir)
			if err != nil || !downloaded || filepath.Base(first) != "no-extension"+extension {
				t.Fatalf("Resolve = %q, %v, %v", first, downloaded, err)
			}
			second, _, err := Resolve(context.Background(), Item{URL: server.URL + "/no-extension"}, dir)
			if err != nil || second == first {
				t.Fatalf("collision = %q, %v", second, err)
			}
			for _, path := range []string{first, second} {
				actual, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(actual, payload) {
					t.Fatalf("original changed: %v", err)
				}
				info, _ := os.Stat(path)
				if info.Mode().Perm() != 0600 {
					t.Fatalf("permissions: %v", info.Mode())
				}
			}
			info, _ := os.Stat(dir)
			if info.Mode().Perm() != 0700 {
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
			path, downloaded, err := Resolve(context.Background(), items[0], dir)
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
			path, downloaded, err := Resolve(context.Background(), Item{URL: server.URL}, dir)
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
	path, _, err := Resolve(context.Background(), Item{URL: server.URL + "/redirect"}, t.TempDir())
	if err != nil || filepath.Ext(path) != ".jpg" {
		t.Fatalf("redirect: %q, %v", path, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	finished := make(chan error, 1)
	go func() {
		_, _, err := Resolve(ctx, Item{URL: server.URL + "/slow"}, dir)
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
	path, _, err := Resolve(context.Background(), Item{URL: server.URL + "/%2E%2E%2F%2E%2E%2F%3F.jpg"}, dir)
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
	transport := NewTransport()
	defer transport.CloseIdleConnections()

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
