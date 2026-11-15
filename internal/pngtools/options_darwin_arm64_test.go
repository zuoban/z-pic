package pngtools

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestEmbeddedQuantizeOptions(t *testing.T) {
	requireBundle(t)
	t.Setenv("PATH", "")
	img := image.NewNRGBA(image.Rect(0, 0, 128, 128))
	for y := 0; y < 128; y++ {
		for x := 0; x < 128; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: byte(x * 2), G: byte(y * 2), B: byte(x + y), A: 255})
		}
	}
	data := encodePNG(t, img)
	original := bytes.Clone(data)
	defaultOutput, err := Quantize(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(defaultOutput, data) {
		t.Fatal("default quantization did not produce a real result")
	}
	for name, options := range map[string]QuantizeOptions{
		"zero-defaults":     {},
		"explicit-defaults": DefaultQuantizeOptions(),
	} {
		t.Run(name, func(t *testing.T) {
			out, err := QuantizeWithOptions(context.Background(), data, options)
			if err != nil || !bytes.Equal(out, defaultOutput) {
				t.Fatalf("default wrapper mismatch: %v", err)
			}
		})
	}
	for _, speed := range []int{1, 3, 11} {
		t.Run(fmt.Sprintf("16-colors-speed-%d", speed), func(t *testing.T) {
			options := QuantizeOptions{MinQuality: 0, MaxQuality: 100, Colors: 16, Speed: speed}
			out, err := QuantizeWithOptions(context.Background(), data, options)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Equal(out, data) || len(out) >= len(data) {
				t.Fatal("expected actual quantization with a size saving")
			}
			decoded, err := png.Decode(bytes.NewReader(out))
			if err != nil {
				t.Fatal(err)
			}
			paletted, ok := decoded.(*image.Paletted)
			if !ok || len(paletted.Palette) > options.Colors || decoded.Bounds() != img.Bounds() {
				t.Fatalf("expected indexed PNG with <=16 colors and original dimensions; got %T", decoded)
			}
			colors := make(map[color.NRGBA64]bool)
			for y := 0; y < 128; y++ {
				for x := 0; x < 128; x++ {
					sample := color.NRGBA64Model.Convert(decoded.At(x, y)).(color.NRGBA64)
					colors[sample] = true
				}
			}
			if len(colors) < 2 || len(colors) > options.Colors {
				t.Fatalf("unexpected decoded color count: %d", len(colors))
			}
			if !bytes.Equal(data, original) {
				t.Fatal("quantization mutated source bytes")
			}
		})
	}
	if !bytes.Equal(data, original) {
		t.Fatal("default quantization mutated source bytes")
	}
}
