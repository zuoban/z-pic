package dropimport

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkResolve1000Images(bench *testing.B) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewNRGBA(image.Rect(0, 0, 32, 24))); err != nil {
		bench.Fatal(err)
	}
	payload := encoded.Bytes()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write(payload)
	}))
	defer server.Close()
	ctx := context.Background()
	for _, name := range []string{"UniqueNames", "SameName"} {
		bench.Run(name, func(sub *testing.B) {
			items := make([]Item, 1000)
			for index := range items {
				path := fmt.Sprintf("/album_%04d/photo.png", index)
				if name == "UniqueNames" {
					path = fmt.Sprintf("/photo_%04d.png", index)
				}
				items[index] = Item{URL: server.URL + path}
			}
			root := sub.TempDir()
			transport := NewTransport()
			defer transport.CloseIdleConnections()
			sub.ReportAllocs()
			sub.ResetTimer()
			for iteration := 0; iteration < sub.N; iteration++ {
				sub.StopTimer()
				dir := filepath.Join(root, fmt.Sprintf("run_%d", iteration))
				sub.StartTimer()
				resolver := NewResolver(dir, transport)
				for _, item := range items {
					if _, saved, err := resolver.Resolve(ctx, item); err != nil || !saved {
						sub.Fatalf("Resolve = saved %v, error %v", saved, err)
					}
				}
				sub.StopTimer()
				entries, err := os.ReadDir(dir)
				if err != nil || len(entries) != len(items) {
					sub.Fatalf("resolved %d files, error %v; want %d", len(entries), err, len(items))
				}
				if err := os.RemoveAll(dir); err != nil {
					sub.Fatal(err)
				}
				sub.StartTimer()
			}
		})
	}
}
