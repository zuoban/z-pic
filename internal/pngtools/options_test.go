package pngtools

import (
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"strings"
	"testing"
)

type cancelDuringMetadata struct {
	context.Context
	cancel context.CancelFunc
	checks int
}

func (ctx *cancelDuringMetadata) Err() error {
	ctx.checks++
	if ctx.checks == 4 {
		ctx.cancel()
	}
	return ctx.Context.Err()
}

func TestMetadataScanObservesCancellation(t *testing.T) {
	original := encodePNG(t, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	data := append([]byte(nil), original[:33]...)
	appendChunk := func(name string) {
		chunk := make([]byte, 12)
		copy(chunk[4:8], name)
		binary.BigEndian.PutUint32(chunk[8:], crc32.ChecksumIEEE(chunk[4:8]))
		data = append(data, chunk...)
	}
	for range 64 {
		appendChunk("teST")
	}
	appendChunk("sBIT")
	data = append(data, original[33:]...)
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &cancelDuringMetadata{Context: base, cancel: cancel}
	out, err := Optimize(ctx, data)
	if out != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("metadata scan ignored cancellation: output=%d err=%v checks=%d", len(out), err, ctx.checks)
	}
}

func TestQuantizeOptionsValidation(t *testing.T) {
	defaults := DefaultQuantizeOptions()
	if defaults != (QuantizeOptions{MinQuality: 65, MaxQuality: 85, Colors: 256, Speed: 3}) {
		t.Fatalf("unexpected defaults: %+v", defaults)
	}
	for name, options := range map[string]QuantizeOptions{
		"zero-defaults": {},
		"defaults":      defaults,
		"lower-bounds":  {MinQuality: 0, MaxQuality: 1, Colors: 2, Speed: 1},
		"upper-bounds":  {MinQuality: 100, MaxQuality: 100, Colors: 256, Speed: 11},
	} {
		t.Run(name, func(t *testing.T) {
			before := options
			if err := options.Validate(); err != nil {
				t.Fatal(err)
			}
			if options != before {
				t.Fatal("Validate modified its input")
			}
		})
	}
	for _, test := range []struct {
		name    string
		options QuantizeOptions
		message string
	}{
		{"min-negative", QuantizeOptions{-1, 85, 256, 3}, "最低质量"},
		{"min-too-high", QuantizeOptions{101, 100, 256, 3}, "最低质量"},
		{"max-zero", QuantizeOptions{0, 0, 256, 3}, "最高质量"},
		{"max-negative", QuantizeOptions{0, -1, 256, 3}, "最高质量"},
		{"max-too-high", QuantizeOptions{0, 101, 256, 3}, "最高质量"},
		{"reversed-quality", QuantizeOptions{86, 85, 256, 3}, "不能大于"},
		{"colors-zero", QuantizeOptions{65, 85, 0, 3}, "颜色数量"},
		{"colors-negative", QuantizeOptions{65, 85, -1, 3}, "颜色数量"},
		{"colors-one", QuantizeOptions{65, 85, 1, 3}, "颜色数量"},
		{"colors-too-high", QuantizeOptions{65, 85, 257, 3}, "颜色数量"},
		{"speed-zero", QuantizeOptions{65, 85, 256, 0}, "量化速度"},
		{"speed-negative", QuantizeOptions{65, 85, 256, -1}, "量化速度"},
		{"speed-too-high", QuantizeOptions{65, 85, 256, 12}, "量化速度"},
		{"partial-options", QuantizeOptions{Colors: 16}, "最高质量"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := test.options.Validate()
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("expected Chinese validation error containing %q, got %v", test.message, err)
			}
			out, err := QuantizeWithOptions(context.Background(), nil, test.options)
			if out != nil || err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("invalid options reached execution: out=%v err=%v", out, err)
			}
		})
	}
}

func TestQuantizeWithOptionsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, options := range []QuantizeOptions{{}, DefaultQuantizeOptions(), {MinQuality: 0, MaxQuality: 100, Colors: 16, Speed: 3}} {
		out, err := QuantizeWithOptions(ctx, nil, options)
		if out != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("out=%v err=%v", out, err)
		}
	}
}
