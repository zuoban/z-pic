package dropimport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolverKeepsMixedNamesAndFormats(test *testing.T) {
	payloads := map[string][]byte{
		"jpeg": imageBytes(test, "jpeg"),
		"png":  imageBytes(test, "png"),
		"webp": imageBytes(test, "webp"),
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write(payloads[request.URL.Query().Get("format")])
	}))
	defer server.Close()
	transport := NewTransport()
	defer transport.CloseIdleConnections()
	dir := test.TempDir()
	resolver := NewResolver(dir, transport)
	cases := []struct {
		path   string
		format string
		want   string
	}{
		{"/first/photo.jpg", "png", "photo.png"},
		{"/second/photo.jpg", "png", "photo-1.png"},
		{"/third/photo-1.jpg", "png", "photo-1-1.png"},
		{"/fourth/photo.jpg", "png", "photo-2.png"},
		{"/fifth/photo.png", "jpeg", "photo.jpg"},
		{"/sixth/photo.png", "webp", "photo.webp"},
		{"/seventh/photo.jpg", "jpeg", "photo-1.jpg"},
		{"/eighth/photo.jpg", "webp", "photo-1.webp"},
		{"/ninth/photo.jpg", "png", "photo-3.png"},
	}
	for _, scenario := range cases {
		item := Item{URL: server.URL + scenario.path + "?format=" + scenario.format}
		path, saved, err := resolver.Resolve(context.Background(), item)
		if err != nil || !saved || path != filepath.Join(dir, scenario.want) {
			test.Fatalf("Resolve(%s) = %q, %v, %v; want %s", item.URL, path, saved, err, scenario.want)
		}
		actual, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(actual, payloads[scenario.format]) {
			test.Fatalf("source bytes changed: %s, %v", path, err)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			test.Fatalf("saved file permissions: %v, %v", info, err)
		}
	}
	assertFileCount(test, dir, len(cases))
}

func TestResolverRechecksExternalNames(test *testing.T) {
	payload := imageBytes(test, "png")
	for _, kind := range []string{"file", "directory", "symlink"} {
		test.Run(kind, func(test *testing.T) {
			dir := test.TempDir()
			external := filepath.Join(dir, "image.png")
			var err error
			switch kind {
			case "file":
				err = os.WriteFile(external, []byte("keep"), 0o600)
			case "directory":
				err = os.Mkdir(external, 0o700)
			case "symlink":
				err = os.Symlink("missing.png", external)
			}
			if err != nil {
				test.Fatal(err)
			}
			resolver := NewResolver(dir, nil)
			resolve := func(want string) {
				test.Helper()
				path, saved, err := resolver.Resolve(context.Background(), Item{Data: payload})
				if err != nil || !saved || path != filepath.Join(dir, want) {
					test.Fatalf("Resolve = %q, %v, %v; want %s", path, saved, err, want)
				}
				actual, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(actual, payload) {
					test.Fatalf("source bytes changed: %s, %v", path, err)
				}
			}
			resolve("image-1.png")
			if info, err := os.Lstat(external); err != nil ||
				kind == "directory" && !info.IsDir() ||
				kind == "symlink" && info.Mode()&os.ModeSymlink == 0 {
				test.Fatalf("external entry changed: %v, %v", info, err)
			}
			if kind == "file" {
				actual, err := os.ReadFile(external)
				if err != nil || string(actual) != "keep" {
					test.Fatalf("existing file overwritten: %q, %v", actual, err)
				}
			}
			if err := os.Remove(external); err != nil {
				test.Fatal(err)
			}
			resolve("image.png")
			external = filepath.Join(dir, "image-2.png")
			if err := os.WriteFile(external, []byte("keep"), 0o600); err != nil {
				test.Fatal(err)
			}
			resolve("image-3.png")
			actual, err := os.ReadFile(external)
			if err != nil || string(actual) != "keep" {
				test.Fatalf("newly occupied name overwritten: %q, %v", actual, err)
			}
			if err := os.Remove(external); err != nil {
				test.Fatal(err)
			}
			resolve("image-2.png")
			assertFileCount(test, dir, 4)
		})
	}
}

func TestResolverFailureAndCancellationDoNotReserveNames(test *testing.T) {
	dir := filepath.Join(test.TempDir(), "downloads")
	payload := imageBytes(test, "png")
	resolver := NewResolver(dir, nil)
	if path, saved, err := resolver.Resolve(context.Background(), Item{Data: payload[:40]}); err == nil || saved || path != "" {
		test.Fatalf("corrupt image accepted: %q, %v, %v", path, saved, err)
	}
	assertFileCount(test, dir, 0)
	if err := os.Remove(dir); err != nil {
		test.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte("blocked"), 0o600); err != nil {
		test.Fatal(err)
	}
	if path, saved, err := resolver.Resolve(context.Background(), Item{Data: payload}); err == nil || saved || path != "" {
		test.Fatalf("invalid destination accepted: %q, %v, %v", path, saved, err)
	}
	if err := os.Remove(dir); err != nil {
		test.Fatal(err)
	}
	for index, want := range []string{"image.png", "image-1.png"} {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if path, saved, err := resolver.Resolve(ctx, Item{Data: payload}); !errors.Is(err, context.Canceled) || saved || path != "" {
			test.Fatalf("canceled image accepted: %q, %v, %v", path, saved, err)
		}
		local := filepath.Join(dir, "local.png")
		if path, saved, err := resolver.Resolve(context.Background(), Item{Path: local}); err != nil || saved || path != local {
			test.Fatalf("local path changed: %q, %v, %v", path, saved, err)
		}
		path, saved, err := resolver.Resolve(context.Background(), Item{Data: payload})
		if err != nil || !saved || path != filepath.Join(dir, want) {
			test.Fatalf("failure consumed a name: %q, %v, %v; want %s", path, saved, err, want)
		}
		assertFileCount(test, dir, index+1)
	}
}

func TestResolverReservesOnlyWithinImport(test *testing.T) {
	dir := test.TempDir()
	payload := imageBytes(test, "png")
	item := Item{Data: payload}
	first := filepath.Join(dir, "image.png")
	for range 2 {
		resolver := NewResolver(dir, nil)
		for _, name := range []string{"image.png", "image-1.png"} {
			path, saved, err := resolver.Resolve(context.Background(), item)
			if err != nil || !saved || path != filepath.Join(dir, name) {
				test.Fatalf("unexpected import name: %q, %v, %v; want %s", path, saved, err, name)
			}
			if name == "image.png" {
				if err := os.Remove(first); err != nil {
					test.Fatal(err)
				}
			}
		}
		assertFileCount(test, dir, 1)
		if err := os.Remove(filepath.Join(dir, "image-1.png")); err != nil {
			test.Fatal(err)
		}
	}
}

func TestResolverFilenameLimitAndCleanup(test *testing.T) {
	dir := test.TempDir()
	payload := imageBytes(test, "png")
	first := filepath.Join(dir, "image.png")
	if err := os.WriteFile(first, payload, 0o600); err != nil {
		test.Fatal(err)
	}
	for suffix := 1; suffix < 10000; suffix++ {
		if err := os.Link(first, filepath.Join(dir, fmt.Sprintf("image-%d.png", suffix))); err != nil {
			test.Fatal(err)
		}
	}
	resolver := NewResolver(dir, nil)
	path, saved, err := resolver.Resolve(context.Background(), Item{Data: payload})
	if err == nil || !strings.Contains(err.Error(), "同名图片过多") || saved || path != "" {
		test.Fatalf("filename limit ignored: %q, %v, %v", path, saved, err)
	}
	assertFileCount(test, dir, 10000)
	last := filepath.Join(dir, "image-9999.png")
	if err := os.Remove(last); err != nil {
		test.Fatal(err)
	}
	path, saved, err = resolver.Resolve(context.Background(), Item{Data: payload})
	if err != nil || !saved || path != last {
		test.Fatalf("last available name: %q, %v, %v", path, saved, err)
	}
	path, saved, err = resolver.Resolve(context.Background(), Item{Data: payload})
	if err == nil || !strings.Contains(err.Error(), "同名图片过多") || saved || path != "" {
		test.Fatalf("cached filename limit ignored: %q, %v, %v", path, saved, err)
	}
	assertFileCount(test, dir, 10000)
}

func TestResolverSameNameQueueLimit(test *testing.T) {
	dir := test.TempDir()
	payload := imageBytes(test, "png")
	resolver := NewResolver(dir, nil)
	for index := 0; index < maxItems; index++ {
		want := "image.png"
		if index > 0 {
			want = fmt.Sprintf("image-%d.png", index)
		}
		path, saved, err := resolver.Resolve(context.Background(), Item{Data: payload})
		if err != nil || !saved || path != filepath.Join(dir, want) {
			test.Fatalf("item %d: %q, %v, %v; want %s", index, path, saved, err, want)
		}
		actual, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(actual, payload) {
			test.Fatalf("item %d changed source bytes: %v", index, err)
		}
	}
	assertFileCount(test, dir, maxItems)
}
