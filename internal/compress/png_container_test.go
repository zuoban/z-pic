package compress

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/png"
	"testing"
)

func TestPNGContainerValidationAcrossEntrypoints(t *testing.T) {
	data := pngFixture(t, image.NewGray(image.Rect(0, 0, 3, 2)), png.DefaultCompression)
	end := len(data) - 12
	badLength := bytes.Clone(data)
	binary.BigEndian.PutUint32(badLength[end:], 0xffffffff)
	for name, input := range map[string][]byte{
		"missing IEND":   data[:end],
		"partial IEND":   data[:len(data)-1],
		"invalid length": badLength,
		"nonempty IEND":  append(bytes.Clone(data[:end]), pngTestChunk("IEND", []byte{0})...),
		"animated":       insertPNGChunk(data, "acTL", make([]byte, 8)),
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			if err := ValidatePNG(ctx, input); err == nil {
				t.Fatal("invalid PNG container accepted")
			}
			path := writeFixture(t, "invalid.png", input)
			if _, err := Inspect(ctx, path); err == nil {
				t.Fatal("Inspect accepted invalid PNG")
			}
			if _, err := Thumbnail(ctx, path, 8); err == nil {
				t.Fatal("Thumbnail accepted invalid PNG")
			}
			if _, err := PNGBytes(ctx, input); err == nil {
				t.Fatal("PNGBytes accepted invalid PNG")
			}
			if _, err := Bytes(ctx, input, path, Options{Quality: 80}); err == nil {
				t.Fatal("Bytes accepted invalid PNG")
			}
			if _, err := decodePNGOutput(ctx, input, image.Config{Width: 3, Height: 2}); err == nil {
				t.Fatal("output validation accepted invalid PNG")
			}
		})
	}
	for name, input := range map[string][]byte{
		"static":         data,
		"text metadata":  insertPNGChunk(data, "tEXt", []byte("note\x00acTL")),
		"data after end": append(bytes.Clone(data), pngTestChunk("acTL", make([]byte, 8))...),
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidatePNG(context.Background(), input); err != nil || IsAnimatedPNG(input) {
				t.Fatalf("static PNG rejected: %v", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ValidatePNG(ctx, data); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}
