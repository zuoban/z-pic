package webptools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/image/webp"
)

func TestAssetChecksum(t *testing.T) {
	data, err := tools.ReadFile("assets/darwin_arm64/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Version      string `json:"version"`
		SourceSHA256 string `json:"source_sha256"`
		SHA256       string `json:"sha256"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Version != "1.6.0" || manifest.SourceSHA256 != "e4ab7009bf0629fd11982d4c2aa83964cf244cffba7347ecd39019a9e38c4564" {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
	asset, err := tools.ReadFile("assets/darwin_arm64/cwebp")
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(asset)
	if hex.EncodeToString(digest[:]) != manifest.SHA256 {
		t.Fatal("asset checksum mismatch")
	}
}

func TestNativeEncode(t *testing.T) {
	if len(bundledTool()) == 0 {
		t.Skip("native asset unavailable for this platform")
	}
	temp := t.TempDir()
	t.Setenv("TMPDIR", temp)
	t.Setenv("PATH", "")
	input, err := os.ReadFile("testdata/alpha-metadata.webp")
	if err != nil {
		t.Fatal(err)
	}
	original, err := webp.Decode(bytes.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	for _, quality := range []int{0, 73, 100} {
		output, err := Encode(context.Background(), input, quality)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := webp.Decode(bytes.NewReader(output))
		if err != nil {
			t.Fatal(err)
		}
		if decoded.Bounds() != original.Bounds() || decoded.Bounds().Dx() != 19 || decoded.Bounds().Dy() != 13 {
			t.Fatalf("changed dimensions: %v", decoded.Bounds())
		}
		for row := 0; row < 13; row++ {
			for column := 0; column < 19; column++ {
				_, _, _, before := original.At(column, row).RGBA()
				_, _, _, after := decoded.At(column, row).RGBA()
				if before != after || after != uint32((column*17+row*23)%256)*257 {
					t.Fatalf("changed alpha at %d,%d: %d -> %d", column, row, before, after)
				}
			}
		}
		for _, kind := range []string{"EXIF", "ICCP", "XMP "} {
			before, after := webPChunk(t, input, kind), webPChunk(t, output, kind)
			if len(before) == 0 || !bytes.Equal(before, after) {
				t.Fatalf("changed %s metadata: %x -> %x", kind, before, after)
			}
		}
		if bytes.Equal(input, output) {
			t.Fatal("expected actual lossy recompression")
		}
	}
	if _, err := Encode(context.Background(), []byte("RIFFxxxxWEBPbroken"), 75); err == nil {
		t.Fatal("expected native decoding failure")
	}
	entries, err := os.ReadDir(temp)
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary executables remain: %v err=%v", entries, err)
	}
}

func TestExtractionVersionAndCleanup(t *testing.T) {
	if len(bundledTool()) == 0 {
		t.Skip("native asset unavailable for this platform")
	}
	path, cleanup, err := extractTool()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	for _, target := range []string{path, filepath.Dir(path)} {
		info, err := os.Stat(target)
		if err != nil || info.Mode().Perm() != 0700 {
			t.Fatalf("target=%s info=%v err=%v", target, info, err)
		}
	}
	version, err := runCommand(context.Background(), path, []string{"-version"}, nil, 1024)
	if err != nil || strings.Split(string(version), "\n")[0] != "1.6.0" {
		t.Fatalf("version=%q err=%v", version, err)
	}
	cleanup()
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("cleanup error=%v", err)
	}
}

func webPChunk(t *testing.T, data []byte, kind string) []byte {
	t.Helper()
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" || uint64(binary.LittleEndian.Uint32(data[4:8]))+8 != uint64(len(data)) {
		t.Fatal("invalid WebP container")
	}
	for offset := 12; offset < len(data); {
		if len(data)-offset < 8 {
			t.Fatal("truncated WebP chunk")
		}
		size := uint64(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		end := uint64(offset) + 8 + size
		if end+size%2 > uint64(len(data)) {
			t.Fatal("invalid WebP chunk length")
		}
		if string(data[offset:offset+4]) == kind {
			return data[offset+8 : int(end)]
		}
		offset = int(end + size%2)
	}
	return nil
}
