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
	"strings"
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

func TestInspectReportsVisualUprightDimensionsForJPEGAndWebP(t *testing.T) {
	jpgData := withEXIF(jpegFixture(t, 85), exifTIFF(binary.LittleEndian, 6))
	jpgInfo, err := Inspect(context.Background(), writeFixture(t, "portrait.jpg", jpgData))
	if err != nil {
		t.Fatalf("Inspect oriented JPEG failed: %v", err)
	}
	if jpgInfo.Width != 96 || jpgInfo.Height != 128 {
		t.Fatalf("Inspect JPEG visual dimensions = %dx%d, want 96x128", jpgInfo.Width, jpgInfo.Height)
	}

	webpBase := webpFixture(t, "alpha") // 96x64
	webpOriented := appendWebPChunk(extendedWebP(webpBase, 0x18, 96, 64), "EXIF", exifTIFF(binary.BigEndian, 6))
	webpInfo, err := Inspect(context.Background(), writeFixture(t, "portrait.webp", webpOriented))
	if err != nil {
		t.Fatalf("Inspect oriented WebP failed: %v", err)
	}
	if webpInfo.Width != 64 || webpInfo.Height != 96 {
		t.Fatalf("Inspect WebP visual dimensions = %dx%d, want 64x96", webpInfo.Width, webpInfo.Height)
	}
}

func TestInspectPNGChecksChunksAfterImageData(t *testing.T) {
	data := pngFixture(t, image.NewNRGBA(image.Rect(0, 0, 3, 2)), png.DefaultCompression)
	end := len(data) - 12
	lateAnimation := append(bytes.Clone(data[:end]), pngTestChunk("acTL", make([]byte, 8))...)
	lateAnimation = append(lateAnimation, data[end:]...)
	oversized := bytes.Clone(data)
	binary.BigEndian.PutUint32(oversized[end:], 0xffffffff)
	badEnd := bytes.Clone(data)
	binary.BigEndian.PutUint32(badEnd[end:], 1)
	for name, input := range map[string][]byte{
		"late animation":  lateAnimation,
		"missing IEND":    data[:end],
		"partial header":  append(bytes.Clone(data[:end]), 0, 0, 0),
		"oversized chunk": oversized,
		"invalid IEND":    badEnd,
	} {
		t.Run(name, func(t *testing.T) {
			path := writeFixture(t, "invalid.png", input)
			if _, err := Inspect(context.Background(), path); err == nil ||
				name == "late animation" && !strings.Contains(err.Error(), "动画") {
				t.Fatalf("invalid PNG accepted or wrong reason: %v", err)
			}
		})
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

	// Bad IHDR CRC and unsupported bitDepth/colorType must be rejected.
	badCRC := bytes.Clone(raw)
	badCRC[29] ^= 0xff
	if _, err := Inspect(context.Background(), writeFixture(t, "badcrc.png", badCRC)); err == nil {
		t.Fatal("corrupt PNG IHDR CRC must be rejected")
	}
	badDepth := bytes.Clone(raw)
	badDepth[24] = 3
	binary.BigEndian.PutUint32(badDepth[29:33], crc32.ChecksumIEEE(badDepth[12:29]))
	if _, err := Inspect(context.Background(), writeFixture(t, "baddepth.png", badDepth)); err == nil {
		t.Fatal("invalid PNG bit depth must be rejected")
	}

	// Cancellation during multi-segment JPEG header scan must abort promptly.
	baseJPEG := jpegFixture(t, 85)
	paddedJPEG := bytes.Clone(baseJPEG[:2])
	for range 8 {
		paddedJPEG = append(paddedJPEG, 0xff, 0xe2, 0xff, 0xff)
		paddedJPEG = append(paddedJPEG, make([]byte, 65533)...)
	}
	paddedJPEG = append(paddedJPEG, baseJPEG[2:]...)
	paddedPath := writeFixture(t, "padded.jpg", paddedJPEG)
	info, err := Inspect(context.Background(), paddedPath)
	if err != nil || info.Format != "jpeg" || info.Width != 128 || info.Height != 96 {
		t.Fatalf("padded JPEG Inspect = %+v, %v", info, err)
	}
	cBase, cCancel := context.WithCancel(context.Background())
	defer cCancel()
	cCtx := &cancelAtCheckpoint{Context: cBase, cancel: cCancel, limit: 6}
	if _, err := Inspect(cCtx, paddedPath); err == nil {
		t.Fatal("JPEG header scan ignored mid-stream cancellation")
	}
}
