package pngtools

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"image"
	"image/color"
	"testing"
)

func makeValidSyntheticICC() []byte {
	icc := make([]byte, 128)
	binary.BigEndian.PutUint32(icc[0:4], 128)
	copy(icc[16:20], "RGB ")
	copy(icc[36:40], "acsp")
	return icc
}

func makeICCPChunk(t *testing.T, icc []byte) []byte {
	t.Helper()
	var compressed bytes.Buffer
	zw := zlib.NewWriter(&compressed)
	if _, err := zw.Write(icc); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	payload := append([]byte("TestRGB\x00\x00"), compressed.Bytes()...)
	return chunk("iCCP", payload)
}

func TestOptimizeWithStripModes(t *testing.T) {
	requireBundle(t)
	img := image.NewNRGBA(image.Rect(0, 0, 48, 36))
	for y := range 36 {
		for x := range 48 {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 5), G: uint8(y * 7), B: 120, A: 255})
		}
	}
	src := withChunks(encodePNG(t, img),
		makeICCPChunk(t, makeValidSyntheticICC()),
		chunk("tEXt", []byte("Comment\x00private note")),
		chunk("eXIf", []byte{'I', 'I', 42, 0, 8, 0, 0, 0, 0, 0, 0, 0, 0, 0}),
	)

	if has, err := HasStrippableMetadata(context.Background(), src, StripNone); err != nil || has {
		t.Fatalf("StripNone should report no strippable chunks: has=%v err=%v", has, err)
	}
	if has, err := HasStrippableMetadata(context.Background(), src, StripSafe); err != nil || !has {
		t.Fatalf("StripSafe should detect tEXt/eXIf: has=%v err=%v", has, err)
	}

	safeOut, err := OptimizeWithOptions(context.Background(), src, OptimizeOptions{
		Strip:       StripSafe,
		AllowLarger: true,
	})
	if err != nil {
		t.Fatalf("StripSafe failed: %v", err)
	}
	if hasChunk(safeOut, "tEXt") || hasChunk(safeOut, "eXIf") {
		t.Fatal("StripSafe must strip tEXt and eXIf chunks")
	}
	if !hasChunk(safeOut, "iCCP") {
		t.Fatal("StripSafe must retain iCCP color profile chunk")
	}
	if has, err := HasStrippableMetadata(context.Background(), safeOut, StripSafe); err != nil || has {
		t.Fatalf("safeOut should have no remaining StripSafe chunks: has=%v err=%v", has, err)
	}
	if has, err := HasStrippableMetadata(context.Background(), safeOut, StripAll); err != nil || !has {
		t.Fatalf("StripAll should detect remaining iCCP chunk: has=%v err=%v", has, err)
	}

	allOut, err := OptimizeWithOptions(context.Background(), src, OptimizeOptions{
		Strip:       StripAll,
		AllowLarger: true,
	})
	if err != nil {
		t.Fatalf("StripAll failed: %v", err)
	}
	if hasChunk(allOut, "iCCP") || hasChunk(allOut, "tEXt") || hasChunk(allOut, "eXIf") {
		t.Fatal("StripAll must strip iCCP, tEXt, and eXIf chunks")
	}
	if _, err := OptimizeWithOptions(context.Background(), src, OptimizeOptions{Strip: StripMode("bogus")}); err == nil {
		t.Fatal("invalid StripMode must fail")
	}
}
