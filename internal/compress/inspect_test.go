package compress

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestInspectValidJPEGAndPNG(t *testing.T) {
	dir := t.TempDir()
	img := image.NewNRGBA(image.Rect(0, 0, 24, 16))

	jpgPath := filepath.Join(dir, "sample.jpg")
	var jpgBuf bytes.Buffer
	if err := jpeg.Encode(&jpgBuf, img, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jpgPath, jpgBuf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	jpgInfo, err := Inspect(context.Background(), jpgPath)
	if err != nil {
		t.Fatalf("Inspect JPEG failed: %v", err)
	}
	if jpgInfo.Format != "jpeg" || jpgInfo.Width != 24 || jpgInfo.Height != 16 || jpgInfo.Size != int64(jpgBuf.Len()) {
		t.Fatalf("unexpected JPEG info: %+v", jpgInfo)
	}

	pngPath := filepath.Join(dir, "sample.png")
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pngPath, pngBuf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	pngInfo, err := Inspect(context.Background(), pngPath)
	if err != nil {
		t.Fatalf("Inspect PNG failed: %v", err)
	}
	if pngInfo.Format != "png" || pngInfo.Width != 24 || pngInfo.Height != 16 || pngInfo.Size != int64(pngBuf.Len()) {
		t.Fatalf("unexpected PNG info: %+v", pngInfo)
	}
}

func TestInspectRejectsAnimatedPNGAndInvalidInputs(t *testing.T) {
	dir := t.TempDir()
	img := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		t.Fatal(err)
	}
	// Inject an acTL chunk right after IHDR (at offset 8 + 25 = 33).
	raw := pngBuf.Bytes()
	actlPayload := make([]byte, 8)
	binary.BigEndian.PutUint32(actlPayload[0:4], 2)
	var chunk bytes.Buffer
	_ = binary.Write(&chunk, binary.BigEndian, uint32(len(actlPayload)))
	chunk.WriteString("acTL")
	chunk.Write(actlPayload)
	crc := crc32.ChecksumIEEE(append([]byte("acTL"), actlPayload...))
	_ = binary.Write(&chunk, binary.BigEndian, crc)

	apngBytes := append(append([]byte{}, raw[:33]...), chunk.Bytes()...)
	apngBytes = append(apngBytes, raw[33:]...)
	apngPath := filepath.Join(dir, "anim.png")
	if err := os.WriteFile(apngPath, apngBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(context.Background(), apngPath); err == nil {
		t.Fatal("animated PNG must be rejected by Inspect")
	}

	badPath := filepath.Join(dir, "corrupt.jpg")
	if err := os.WriteFile(badPath, []byte("not an image"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(context.Background(), badPath); err == nil {
		t.Fatal("corrupted file must be rejected by Inspect")
	}

	if _, err := Inspect(context.Background(), dir); err == nil {
		t.Fatal("directory must be rejected by Inspect")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Inspect(ctx, apngPath); err == nil {
		t.Fatal("canceled context must abort Inspect")
	}
}
