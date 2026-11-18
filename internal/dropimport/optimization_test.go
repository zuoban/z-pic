package dropimport

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDownloadResponseHeadersAreBounded(t *testing.T) {
	payload := imageBytes(t, "png")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		size := 4096
		if r.URL.Path == "/oversized" {
			size = (1 << 20) + 1024
		}
		w.Header().Set("X-Padding", strings.Repeat("a", size))
		_, _ = w.Write(payload)
	}))
	defer server.Close()
	dir := t.TempDir()
	path, saved, err := Resolve(context.Background(), Item{URL: server.URL + "/oversized"}, dir)
	if err == nil || saved || path != "" {
		t.Fatalf("oversized response headers accepted: %q, %v, %v", path, saved, err)
	}
	assertFileCount(t, dir, 0)
	if _, saved, err := Resolve(context.Background(), Item{URL: server.URL + "/normal"}, dir); err != nil || !saved {
		t.Fatalf("ordinary response rejected: saved=%v, err=%v", saved, err)
	}
}

func TestResolveRejectsNonImageBeforeReadingResponseTail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Serve a prefix, then leave the remainder unavailable until canceled.
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(bytes.Repeat([]byte("<html>login</html>"), 32))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	directory := filepath.Join(t.TempDir(), "not-created")
	_, saved, err := Resolve(ctx, Item{URL: server.URL}, directory)
	if saved || err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("non-image response consumed tail: saved=%v, error=%v", saved, err)
	}
	if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unsupported response created download directory: %v", err)
	}
}

func TestDownloadTransportReusesConnectionsAndBoundsIdlePool(t *testing.T) {
	payload := imageBytes(t, "png")
	var connections atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.Start()
	defer server.Close()
	transport := NewTransport()
	defer transport.CloseIdleConnections()
	if transport.MaxIdleConns <= 0 || transport.MaxIdleConns > 32 ||
		transport.IdleConnTimeout <= 0 || transport.TLSHandshakeTimeout <= 0 ||
		transport.ResponseHeaderTimeout <= 0 || transport.DialContext == nil {
		t.Fatal("download transport lacks connection/timeout bounds")
	}
	resolver := NewResolver(t.TempDir(), transport)
	for range 3 {
		if _, saved, err := resolver.Resolve(context.Background(), Item{URL: server.URL}); err != nil || !saved {
			t.Fatalf("valid response rejected: %v", err)
		}
	}
	if connections.Load() != 1 {
		t.Fatalf("sequential imports opened %d connections", connections.Load())
	}
}
