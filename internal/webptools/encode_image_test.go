package webptools

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"os"
	"runtime"
	"testing"

	"golang.org/x/image/webp"
)

func hasRIFFChunk(data []byte, want string) bool {
	if len(data) < 20 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return false
	}
	for offset := 12; offset <= len(data)-8; {
		kind := string(data[offset : offset+4])
		size := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		if size < 0 || size > len(data)-offset-8 {
			return false
		}
		if kind == want {
			return true
		}
		offset += 8 + size + size%2
	}
	return false
}

func TestEncodeOptionsValidation(t *testing.T) {
	if _, err := buildEncodeArgs(EncodeOptions{Quality: 80, Metadata: MetadataMode("bad")}, true); err == nil {
		t.Fatal("invalid metadata mode must fail")
	}
	if _, err := buildEncodeArgs(EncodeOptions{Lossless: true}, true); err != nil {
		t.Fatalf("lossless mode must not require nonzero quality: %v", err)
	}
	if _, err := EncodeImage(context.Background(), nil, EncodeOptions{Quality: 80}); err == nil {
		t.Fatal("nil image must fail")
	}
}

func TestEncodeImageAndMetadataModes(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("bundled cwebp currently ships only for darwin/arm64")
	}
	// 1. Opaque RGB lossless round-trip via P6 PPM
	opaque := image.NewNRGBA(image.Rect(0, 0, 24, 18))
	for y := range 18 {
		for x := range 24 {
			opaque.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 10), G: uint8(y * 12), B: uint8((x + y) * 5), A: 255})
		}
	}
	outOpaque, err := EncodeImage(context.Background(), opaque, EncodeOptions{Lossless: true})
	if err != nil {
		t.Fatalf("EncodeImage opaque lossless failed: %v", err)
	}
	decodedOpaque, err := webp.Decode(bytes.NewReader(outOpaque))
	if err != nil {
		t.Fatalf("decode opaque webp failed: %v", err)
	}
	for y := range 18 {
		for x := range 24 {
			want := opaque.NRGBAAt(x, y)
			got := color.NRGBAModel.Convert(decodedOpaque.At(x, y)).(color.NRGBA)
			if got != want {
				t.Fatalf("opaque lossless mismatch at (%d,%d): got %v want %v", x, y, got, want)
			}
		}
	}

	// 2. Semi-transparent premultiplied *image.RGBA lossless round-trip via P7 PAM
	alphaImg := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := range 16 {
		for x := range 16 {
			// Set premultiplied values representing straight RGBA(200, 100, 40, 128) and transparent pixels.
			if (x+y)%3 == 0 {
				alphaImg.SetRGBA(x, y, color.RGBA{R: 0, G: 0, B: 0, A: 0})
			} else {
				alphaImg.Set(x, y, color.NRGBA{R: 200, G: 100, B: 40, A: 128})
			}
		}
	}
	outAlpha, err := EncodeImage(context.Background(), alphaImg, EncodeOptions{Lossless: true})
	if err != nil {
		t.Fatalf("EncodeImage alpha lossless failed: %v", err)
	}
	decodedAlpha, err := webp.Decode(bytes.NewReader(outAlpha))
	if err != nil {
		t.Fatalf("decode alpha webp failed: %v", err)
	}
	for y := range 16 {
		for x := range 16 {
			want := color.RGBAModel.Convert(alphaImg.At(x, y)).(color.RGBA)
			got := color.RGBAModel.Convert(decodedAlpha.At(x, y)).(color.RGBA)
			if absDiff(got.A, want.A) > 1 || (want.A > 0 && (absDiff(got.R, want.R) > 2 || absDiff(got.G, want.G) > 2 || absDiff(got.B, want.B) > 2)) {
				t.Fatalf("alpha lossless mismatch at (%d,%d): got %v want %v", x, y, got, want)
			}
		}
	}

	// 3. Metadata modes on fixture with ICCP + EXIF + XMP
	fixture, err := os.ReadFile("testdata/alpha-metadata.webp")
	if err != nil {
		t.Fatal(err)
	}
	iccOnly, err := EncodeWithOptions(context.Background(), fixture, EncodeOptions{
		Quality:  80,
		Metadata: MetadataICC,
	})
	if err != nil {
		t.Fatalf("MetadataICC failed: %v", err)
	}
	if !hasRIFFChunk(iccOnly, "ICCP") || hasRIFFChunk(iccOnly, "EXIF") || hasRIFFChunk(iccOnly, "XMP ") {
		t.Fatal("MetadataICC must keep ICCP and strip EXIF/XMP")
	}

	stripped, err := EncodeWithOptions(context.Background(), fixture, EncodeOptions{
		Quality:  80,
		Metadata: MetadataNone,
	})
	if err != nil {
		t.Fatalf("MetadataNone failed: %v", err)
	}
	if hasRIFFChunk(stripped, "ICCP") || hasRIFFChunk(stripped, "EXIF") || hasRIFFChunk(stripped, "XMP ") {
		t.Fatal("MetadataNone must strip ICCP, EXIF, and XMP")
	}
}

func absDiff(a, b uint8) uint8 {
	if a > b {
		return a - b
	}
	return b - a
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
			if err := writePNM(context.Background(), &got, img, true); err != nil {
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
