package dropimport

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/transfer"

	"zpic/internal/aviftools"
)

func TestParseAVIFRepresentations(t *testing.T) {
	payload := []byte("\x00\x00\x00\x10ftypavif\x00\x00\x00\x00")
	raw := "data:image/avif;base64," + base64.StdEncoding.EncodeToString(payload)
	for name, rep := range map[string]transfer.Representation{
		"native MIME": transfer.Bytes("image/avif", payload),
		"data URL":    representation(transfer.Text, raw),
		"HTML img":    representation(transfer.HTML, `<img src="`+raw+`">`),
	} {
		t.Run(name, func(t *testing.T) {
			items, err := Parse(transfer.New(transfer.NewItem(rep)))
			if err != nil || len(items) != 1 || !bytes.Equal(items[0].Data, payload) {
				t.Fatalf("Parse = %#v, %v", items, err)
			}
		})
	}
}

func avifFixture(t *testing.T) []byte {
	t.Helper()
	if !aviftools.Available() {
		t.Skip("bundled AVIF tools require macOS on Apple silicon")
	}
	img := image.NewNRGBA(image.Rect(0, 0, 12, 8))
	img.SetNRGBA(2, 3, color.NRGBA{R: 254, A: 127})
	data, err := aviftools.EncodeImage(t.Context(), img, aviftools.EncodeOptions{Lossless: true, Threads: 1})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestResolveStaticAVIF(t *testing.T) {
	data := avifFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType := "image/avif"
		if r.URL.Path == "/photo.png" {
			contentType = "image/png"
		}
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write(data)
	}))
	defer server.Close()
	dir := t.TempDir()
	transport := newLoopbackTransport(t)
	for _, tc := range []struct {
		rep  transfer.Representation
		name string
	}{
		{transfer.Bytes("image/avif", data), "image.avif"},
		{representation(transfer.URIList, server.URL+"/remote.avif"), "remote.avif"},
		{representation(transfer.HTML, `<img src="`+server.URL+`/photo.png">`), "photo.avif"},
	} {
		items, err := Parse(transfer.New(transfer.NewItem(tc.rep)))
		if err != nil || len(items) != 1 {
			t.Fatalf("Parse %s = %#v, %v", tc.name, items, err)
		}
		path, downloaded, err := ResolveWithTransport(t.Context(), items[0], dir, transport)
		if err != nil || !downloaded || filepath.Base(path) != tc.name {
			t.Fatalf("Resolve = %q, %v, %v; want %s", path, downloaded, err, tc.name)
		}
		read, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(read, data) {
			t.Fatalf("saved AVIF differs from input: %v", err)
		}
		files, err := transfer.FileData(path)
		if err != nil {
			t.Fatal(err)
		}
		items, err = Parse(files)
		if err != nil || len(items) != 1 {
			t.Fatalf("Parse local AVIF = %#v, %v", items, err)
		}
		local, downloaded, err := Resolve(t.Context(), items[0], dir)
		if err != nil || downloaded || local != path {
			t.Fatalf("Resolve local AVIF = %q, %v, %v", local, downloaded, err)
		}
	}
	assertFileCount(t, dir, 3)
}

func TestResolveAVIFRejectsAnimationAndCorruptImageData(t *testing.T) {
	data := avifFixture(t)
	animated := bytes.Clone(data)
	copy(animated[8:12], "avis")
	corrupt := bytes.Clone(data)
	mdat := bytes.Index(corrupt, []byte("mdat"))
	if mdat < 0 {
		t.Fatal("fixture has no mdat")
	}
	clear(corrupt[mdat+4:])
	if _, err := aviftools.InspectBytes(t.Context(), corrupt); err != nil {
		t.Fatalf("corrupt fixture must still pass header inspection: %v", err)
	}
	for _, tc := range []struct {
		name string
		data []byte
		want error
	}{
		{"animated", animated, aviftools.ErrAnimatedAVIF},
		{"corrupt", corrupt, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "image/avif")
				_, _ = w.Write(tc.data)
			}))
			defer server.Close()
			dir := t.TempDir()
			transport := newLoopbackTransport(t)
			for _, rep := range []transfer.Representation{
				transfer.Bytes("image/avif", tc.data),
				representation(transfer.URIList, server.URL+"/image.avif"),
			} {
				items, err := Parse(transfer.New(transfer.NewItem(rep)))
				if err != nil || len(items) != 1 {
					t.Fatalf("Parse = %#v, %v", items, err)
				}
				path, downloaded, err := ResolveWithTransport(t.Context(), items[0], dir, transport)
				if err == nil || path != "" || downloaded || tc.want != nil && !errors.Is(err, tc.want) {
					t.Errorf("invalid AVIF gave path=%q, downloaded=%v, error=%v", path, downloaded, err)
				}
				assertFileCount(t, dir, 0)
			}
		})
	}
}
