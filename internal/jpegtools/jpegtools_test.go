package jpegtools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestToolProcess(t *testing.T) {
	var mode string
	for index, arg := range os.Args {
		if arg == "--" && index+1 < len(os.Args) {
			mode = os.Args[index+1]
			break
		}
	}
	if mode == "" {
		return
	}
	switch mode {
	case "success":
		_, _ = io.Copy(os.Stdout, os.Stdin)
	case "larger":
		_, _ = io.Copy(io.Discard, os.Stdin)
		_, _ = os.Stdout.Write(bytes.Repeat([]byte("x"), 4096))
	case "error":
		fmt.Fprint(os.Stdout, "partial output")
		fmt.Fprint(os.Stderr, "diagnostic from child")
		os.Exit(42)
	case "empty":
	case "overflow":
		_, _ = os.Stdout.Write(bytes.Repeat([]byte("x"), 4096))
		time.Sleep(30 * time.Second)
	case "stderr", "stderr-error":
		_, _ = os.Stderr.Write(bytes.Repeat([]byte("e"), maxStderr*4))
		if mode == "stderr-error" {
			os.Exit(42)
		}
		fmt.Fprint(os.Stdout, "ok")
	case "wait":
		time.Sleep(30 * time.Second)
	default:
		os.Exit(41)
	}
	os.Exit(0)
}

func TestCommandExecution(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	invoke := func(ctx context.Context, mode string, limit, discardAt int) ([]byte, error) {
		return runCommandWithReader(ctx, "cjpeg", executable,
			[]string{"-test.run=^TestToolProcess$", "--", mode},
			bytes.NewReader([]byte("input")), limit, discardAt)
	}
	t.Run("success", func(t *testing.T) {
		output, err := invoke(context.Background(), "success", 5, 0)
		if err != nil || string(output) != "input" {
			t.Fatalf("output=%q err=%v", output, err)
		}
	})
	t.Run("discard-at-threshold", func(t *testing.T) {
		output, err := invoke(context.Background(), "larger", 8192, 1024)
		if output != nil || !errors.Is(err, ErrNoSavings) {
			t.Fatalf("output=%q err=%v", output, err)
		}
	})
	for _, mode := range []string{"error", "empty", "stderr-error"} {
		t.Run(mode, func(t *testing.T) {
			output, err := invoke(context.Background(), mode, 1024, 0)
			if output != nil || err == nil {
				t.Fatalf("output=%q err=%v", output, err)
			}
			if mode == "error" && !strings.Contains(err.Error(), "diagnostic from child") {
				t.Fatal(err)
			}
			if len(err.Error()) > maxStderr+512 {
				t.Fatalf("unbounded diagnostic: %d", len(err.Error()))
			}
		})
	}
	t.Run("overflow-kills-child", func(t *testing.T) {
		start := time.Now()
		output, err := invoke(context.Background(), "overflow", 128, 0)
		if output != nil || !errors.Is(err, errOutputLimit) || time.Since(start) > 5*time.Second {
			t.Fatalf("output=%q err=%v elapsed=%v", output, err, time.Since(start))
		}
	})
	t.Run("stderr-drains", func(t *testing.T) {
		output, err := invoke(context.Background(), "stderr", 1024, 0)
		if err != nil || string(output) != "ok" {
			t.Fatalf("output=%q err=%v", output, err)
		}
	})
	t.Run("cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		timer := time.AfterFunc(100*time.Millisecond, cancel)
		defer timer.Stop()
		start := time.Now()
		output, err := invoke(ctx, "wait", 1024, 0)
		if output != nil || !errors.Is(err, context.Canceled) || time.Since(start) > 5*time.Second {
			t.Fatalf("output=%q err=%v elapsed=%v", output, err, time.Since(start))
		}
	})
	t.Run("deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		output, err := invoke(ctx, "wait", 1024, 0)
		if output != nil || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("output=%q err=%v", output, err)
		}
	})
}

func TestValidation(t *testing.T) {
	if _, err := EncodeImage(context.Background(), nil, EncodeOptions{Quality: 80}); err == nil {
		t.Fatal("nil image must fail")
	}
	empty := image.NewRGBA(image.Rect(0, 0, 0, 0))
	if _, err := EncodeImage(context.Background(), empty, EncodeOptions{Quality: 80}); err == nil {
		t.Fatal("empty image must fail")
	}
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for _, q := range []int{0, -1, 101} {
		if _, err := EncodeImage(context.Background(), img, EncodeOptions{Quality: q}); err == nil {
			t.Fatalf("quality %d must fail", q)
		}
	}
	if _, err := EncodeImage(context.Background(), img, EncodeOptions{Quality: 80, MaxOutputBytes: 2}); !errors.Is(err, ErrNoSavings) {
		t.Fatalf("tiny MaxOutputBytes must return ErrNoSavings, got %v", err)
	}
	if _, err := OptimizeLossless(context.Background(), []byte("not-jpeg"), OptimizeOptions{}); err == nil {
		t.Fatal("non-JPEG input must fail")
	}
	if _, err := OptimizeLossless(context.Background(), []byte{0xff, 0xd8}, OptimizeOptions{Copy: "invalid"}); err == nil {
		t.Fatal("invalid CopyMode must fail")
	}
	if _, err := OptimizeLossless(context.Background(), []byte{0xff, 0xd8}, OptimizeOptions{Orientation: 9}); err == nil {
		t.Fatal("invalid orientation must fail")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := EncodeImage(ctx, img, EncodeOptions{Quality: 80}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled EncodeImage got %v", err)
	}
	if _, err := OptimizeLossless(ctx, []byte{0xff, 0xd8}, OptimizeOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled OptimizeLossless got %v", err)
	}
}

func TestWritePNMFormats(t *testing.T) {
	gray := image.NewGray(image.Rect(2, 3, 5, 5))
	gray.SetGray(2, 3, color.Gray{Y: 11})
	gray.SetGray(4, 4, color.Gray{Y: 222})
	var buf bytes.Buffer
	if err := writePNM(context.Background(), &buf, gray, true); err != nil {
		t.Fatal(err)
	}
	header := "P5\n3 2\n255\n"
	if !bytes.HasPrefix(buf.Bytes(), []byte(header)) || buf.Len() != len(header)+6 {
		t.Fatalf("unexpected P5 output: %q", buf.Bytes())
	}
	pixels := buf.Bytes()[len(header):]
	if pixels[0] != 11 || pixels[5] != 222 {
		t.Fatalf("unexpected P5 pixels: %v", pixels)
	}

	rgba := image.NewRGBA(image.Rect(0, 0, 2, 1))
	rgba.SetRGBA(0, 0, color.RGBA{R: 10, G: 20, B: 30, A: 255})
	rgba.SetRGBA(1, 0, color.RGBA{R: 40, G: 50, B: 60, A: 255})
	buf.Reset()
	if err := writePNM(context.Background(), &buf, rgba, false); err != nil {
		t.Fatal(err)
	}
	rgbHeader := "P6\n2 1\n255\n"
	if !bytes.Equal(buf.Bytes(), append([]byte(rgbHeader), 10, 20, 30, 40, 50, 60)) {
		t.Fatalf("unexpected P6 output: %v", buf.Bytes())
	}
}

func TestWritePNMYCbCrSubsamplingMatchesOffsets(t *testing.T) {
	ratios := []image.YCbCrSubsampleRatio{
		image.YCbCrSubsampleRatio444,
		image.YCbCrSubsampleRatio422,
		image.YCbCrSubsampleRatio420,
		image.YCbCrSubsampleRatio440,
		image.YCbCrSubsampleRatio411,
		image.YCbCrSubsampleRatio410,
	}
	for _, ratio := range ratios {
		src := image.NewYCbCr(image.Rect(3, 5, 28, 22), ratio)
		for i := range src.Y {
			src.Y[i] = uint8((i*17 + 13) % 256)
		}
		for i := range src.Cb {
			src.Cb[i] = uint8((i*23 + 37) % 256)
			src.Cr[i] = uint8((i*29 + 61) % 256)
		}
		for _, img := range []image.Image{src, src.SubImage(image.Rect(6, 8, 25, 19))} {
			var got bytes.Buffer
			if err := writePNM(context.Background(), &got, img, false); err != nil {
				t.Fatalf("writePNM(%s) failed: %v", ratio, err)
			}
			b := img.Bounds()
			var want bytes.Buffer
			fmt.Fprintf(&want, "P6\n%d %d\n255\n", b.Dx(), b.Dy())
			sub := img.(*image.YCbCr)
			for y := b.Min.Y; y < b.Max.Y; y++ {
				for x := b.Min.X; x < b.Max.X; x++ {
					yi := sub.YOffset(x, y)
					ci := sub.COffset(x, y)
					r, g, bl := color.YCbCrToRGB(sub.Y[yi], sub.Cb[ci], sub.Cr[ci])
					want.Write([]byte{r, g, bl})
				}
			}
			if !bytes.Equal(got.Bytes(), want.Bytes()) {
				t.Fatalf("writePNM YCbCr(%s) bounds=%v mismatch", ratio, b)
			}
		}
	}
}

func TestWritePNMPalettedFastPath(t *testing.T) {
	pal := color.Palette{
		color.NRGBA{R: 12, G: 34, B: 56, A: 255},
		color.NRGBA{R: 200, G: 150, B: 100, A: 255},
		nil, // unused nil palette slot must not panic
		color.NRGBA{R: 45, G: 210, B: 90, A: 255},
	}
	src := image.NewPaletted(image.Rect(2, 3, 10, 9), pal)
	used := []uint8{0, 1, 3}
	for i := range src.Pix {
		src.Pix[i] = used[i%len(used)]
	}
	for _, img := range []image.Image{src, src.SubImage(image.Rect(3, 4, 9, 8))} {
		var got bytes.Buffer
		if err := writePNM(context.Background(), &got, img, false); err != nil {
			t.Fatalf("writePNM(Paletted) failed: %v", err)
		}
		b := img.Bounds()
		var want bytes.Buffer
		fmt.Fprintf(&want, "P6\n%d %d\n255\n", b.Dx(), b.Dy())
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				r, g, bl, _ := img.At(x, y).RGBA()
				want.Write([]byte{uint8(r >> 8), uint8(g >> 8), uint8(bl >> 8)})
			}
		}
		if !bytes.Equal(got.Bytes(), want.Bytes()) {
			t.Fatalf("writePNM Paletted bounds=%v mismatch", b)
		}
	}
}
