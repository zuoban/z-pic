package compress

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestThumbnailDimensionsAndIndependentRaster(t *testing.T) {
	for _, format := range []string{"png", "jpeg"} {
		for _, size := range []image.Point{{80, 48}, {48, 80}, {1, 80}, {80, 1}} {
			for _, edge := range []int{1, 24, 100} {
				name := format + "/" + size.String() + "/" + strconv.Itoa(edge)
				t.Run(name, func(t *testing.T) {
					source := image.NewNRGBA(image.Rect(0, 0, size.X, size.Y))
					for y := 0; y < size.Y; y++ {
						for x := 0; x < size.X; x++ {
							source.SetNRGBA(x, y, color.NRGBA{R: 160, G: 80, B: 40, A: 255})
						}
					}
					var encoded bytes.Buffer
					var err error
					if format == "png" {
						err = png.Encode(&encoded, source)
					} else {
						err = jpeg.Encode(&encoded, source, &jpeg.Options{Quality: 100})
					}
					if err != nil {
						t.Fatal(err)
					}
					path := writeFixture(t, "source", encoded.Bytes())
					result, err := Thumbnail(context.Background(), path, edge)
					if err != nil {
						t.Fatal(err)
					}
					width, height := size.X, size.Y
					if longest := max(width, height); longest > edge {
						width = max(1, width*edge/longest)
						height = max(1, height*edge/longest)
					}
					raster, ok := result.(*image.RGBA)
					if !ok {
						t.Fatalf("result retains a wrapper or decoder raster: %T", result)
					}
					if raster.Bounds() != image.Rect(0, 0, width, height) ||
						len(raster.Pix) != width*height*4 || cap(raster.Pix) != len(raster.Pix) {
						t.Fatalf("unexpected raster storage: %v, %d/%d bytes", raster.Bounds(), len(raster.Pix), cap(raster.Pix))
					}
					got := raster.RGBAAt(0, 0)
					if absThumbnailDelta(got.R, 160) > 2 || absThumbnailDelta(got.G, 80) > 2 ||
						absThumbnailDelta(got.B, 40) > 2 || got.A != 255 {
						t.Fatalf("unexpected pixels: %v", got)
					}
					before := append([]byte(nil), raster.Pix...)
					clear(source.Pix)
					clear(encoded.Bytes())
					if err := os.WriteFile(path, []byte("replaced"), 0o600); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(raster.Pix, before) {
						t.Fatal("source changes affected thumbnail")
					}
				})
			}
		}
	}
}

func absThumbnailDelta(first, second uint8) int {
	delta := int(first) - int(second)
	if delta < 0 {
		return -delta
	}
	return delta
}

func TestThumbnailTransparencyAndInterpolation(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	source.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	source.SetNRGBA(1, 0, color.NRGBA{G: 255})
	source.SetNRGBA(0, 1, color.NRGBA{B: 255})
	source.SetNRGBA(1, 1, color.NRGBA{R: 255, A: 255})
	path := writeFixture(t, "alpha.png", pngFixture(t, source, png.DefaultCompression))
	result, err := Thumbnail(context.Background(), path, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.(*image.RGBA).RGBAAt(0, 0); got != (color.RGBA{R: 128, A: 128}) {
		t.Fatalf("expected premultiplied bilinear blend, got %v", got)
	}
	result, err = Thumbnail(context.Background(), path, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, alpha := result.At(1, 0).RGBA(); alpha != 0 {
		t.Fatalf("transparent pixel became opaque: %d", alpha)
	}
}

func TestThumbnailJPEGOrientation(t *testing.T) {
	source := image.NewGray(image.Rect(0, 0, 80, 48))
	levels := []uint8{30, 90, 150, 220}
	for y := 0; y < 48; y++ {
		for x := 0; x < 80; x++ {
			source.SetGray(x, y, color.Gray{Y: levels[(y/24)*2+x/40]})
		}
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, source, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatal(err)
	}
	corners := [8][4]uint8{
		{30, 90, 150, 220}, {90, 30, 220, 150},
		{220, 150, 90, 30}, {150, 220, 30, 90},
		{30, 150, 90, 220}, {150, 30, 220, 90},
		{220, 90, 150, 30}, {90, 220, 30, 150},
	}
	for orientation := 1; orientation <= 8; orientation++ {
		t.Run(strconv.Itoa(orientation), func(t *testing.T) {
			data := withEXIF(encoded.Bytes(), exifTIFF(binary.LittleEndian, uint16(orientation)))
			result, err := Thumbnail(context.Background(), writeFixture(t, "exif.jpg", data), 40)
			if err != nil {
				t.Fatal(err)
			}
			width, height := 40, 24
			if orientation >= 5 {
				width, height = height, width
			}
			if result.Bounds() != image.Rect(0, 0, width, height) {
				t.Fatalf("unexpected oriented dimensions: %v", result.Bounds())
			}
			for index, point := range []image.Point{{3, 3}, {width - 4, 3}, {3, height - 4}, {width - 4, height - 4}} {
				got := color.GrayModel.Convert(result.At(point.X, point.Y)).(color.Gray).Y
				if absThumbnailDelta(got, corners[orientation-1][index]) > 3 {
					t.Fatalf("corner %d: got %d, want %d", index, got, corners[orientation-1][index])
				}
			}
		})
	}
}

func TestThumbnailRejectsInvalidInputs(t *testing.T) {
	valid := pngFixture(t, image.NewGray(image.Rect(0, 0, 2, 2)), png.DefaultCompression)
	var gifData bytes.Buffer
	if err := gif.Encode(&gifData, image.NewGray(image.Rect(0, 0, 2, 2)), nil); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"invalid":   []byte("not an image"),
		"truncated": valid[:33],
		"APNG":      insertPNGChunk(valid, "acTL", make([]byte, 8)),
		"GIF":       gifData.Bytes(),
	} {
		t.Run(name, func(t *testing.T) {
			result, err := Thumbnail(context.Background(), writeFixture(t, name, data), 24)
			if err == nil || result != nil {
				t.Fatalf("expected rejection, got %T, %v", result, err)
			}
		})
	}
	for _, edge := range []int{0, -1} {
		if result, err := Thumbnail(context.Background(), writeFixture(t, "valid.png", valid), edge); err == nil || result != nil {
			t.Fatalf("invalid edge %d: %T, %v", edge, result, err)
		}
	}
	for _, path := range []string{"", t.TempDir(), t.TempDir() + "/missing"} {
		if result, err := Thumbnail(context.Background(), path, 24); err == nil || result != nil {
			t.Fatalf("invalid path %q: %T, %v", path, result, err)
		}
	}
}

func TestThumbnailLimits(t *testing.T) {
	original := pngFixture(t, image.NewGray(image.Rect(0, 0, 1, 1)), png.DefaultCompression)
	for _, size := range [][2]uint32{{8001, 4000}, {0x7fffffff, 0x7fffffff}} {
		data := append([]byte(nil), original[:33]...)
		binary.BigEndian.PutUint32(data[16:20], size[0])
		binary.BigEndian.PutUint32(data[20:24], size[1])
		binary.BigEndian.PutUint32(data[29:33], crc32.ChecksumIEEE(data[12:29]))
		result, err := Thumbnail(context.Background(), writeFixture(t, "huge.png", data), 24)
		if result != nil || err == nil || (!strings.Contains(err.Error(), "像素") && !strings.Contains(err.Error(), "dimension overflow")) {
			t.Fatalf("expected header limit rejection, got %T, %v", result, err)
		}
	}
	path := writeFixture(t, "large.png", original)
	file, err := os.OpenFile(path, os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	err = file.Truncate(maxInputSize + 1)
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("create oversized file: %v, %v", err, closeErr)
	}
	result, err := Thumbnail(context.Background(), path, 24)
	if result != nil || err == nil || !strings.Contains(err.Error(), "64 MiB") {
		t.Fatalf("expected file limit rejection, got %T, %v", result, err)
	}
}

type thumbnailCancelImage struct {
	image.Image
	cancel context.CancelFunc
}

func (source thumbnailCancelImage) At(x, y int) color.Color {
	source.cancel()
	return source.Image.At(x, y)
}

func TestThumbnailCancellation(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		var ctx context.Context
		if cause == context.Canceled {
			canceled, cancel := context.WithCancel(context.Background())
			cancel()
			ctx = canceled
		} else {
			expired, cancel := context.WithTimeout(context.Background(), 0)
			defer cancel()
			ctx = expired
		}
		result, err := Thumbnail(ctx, "missing", 24)
		if result != nil || !errors.Is(err, cause) {
			t.Fatalf("expected %v, got %T, %v", cause, result, err)
		}
	}
	for _, size := range []image.Point{{2, 2}, {1024, 2}} {
		ctx, cancel := context.WithCancel(context.Background())
		source := thumbnailCancelImage{Image: image.NewRGBA(image.Rect(0, 0, size.X, size.Y)), cancel: cancel}
		result, err := thumbnailRaster(ctx, source, size.X)
		cancel()
		if result != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("expected scaling cancellation for %v, got %T, %v", size, result, err)
		}
	}
}

func TestThumbnailRasterDoesNotRetainSource(t *testing.T) {
	for _, edge := range []int{2, 8} {
		source := image.NewRGBA(image.Rect(7, 9, 11, 13))
		source.SetRGBA(8, 10, color.RGBA{R: 200, A: 255})
		result, err := thumbnailRaster(context.Background(), source, edge)
		if err != nil {
			t.Fatal(err)
		}
		before := append([]byte(nil), result.Pix...)
		clear(source.Pix)
		if !bytes.Equal(before, result.Pix) {
			t.Fatal("raster shares source pixels")
		}
		sourceBefore := append([]byte(nil), source.Pix...)
		for index := range result.Pix {
			result.Pix[index] = 255
		}
		if !bytes.Equal(sourceBefore, source.Pix) {
			t.Fatal("thumbnail mutation changed source")
		}
	}
}

func TestThumbnailRasterRGBA64FastPathMatchesFallback(test *testing.T) {
	parent := image.Rect(-4, -3, 30, 26)
	bounds := image.Rect(4, 6, 20, 18)
	nrgbaParent := image.NewNRGBA(parent)
	rgbaParent := image.NewRGBA(parent)
	for row := parent.Min.Y; row < parent.Max.Y; row++ {
		for column := parent.Min.X; column < parent.Max.X; column++ {
			a := uint8((column*31 + row*19) & 0xff)
			c := color.NRGBA{
				R: uint8(column * 13),
				G: uint8(row * 17),
				B: uint8((column ^ row) * 23),
				A: a,
			}
			nrgbaParent.SetNRGBA(column, row, c)
			rgbaParent.Set(column, row, c)
		}
	}
	for _, tc := range []struct {
		name string
		img  image.Image
	}{
		{"SubNRGBA", nrgbaParent.SubImage(bounds)},
		{"SubRGBA", rgbaParent.SubImage(bounds)},
	} {
		for _, edge := range []int{7, 64} {
			fastThumb, err := thumbnailRaster(context.Background(), tc.img, edge)
			if err != nil {
				test.Fatal(err)
			}
			slowThumb, err := thumbnailRaster(context.Background(), colorOnlyImage{tc.img}, edge)
			if err != nil {
				test.Fatal(err)
			}
			if fastThumb.Rect != slowThumb.Rect || !bytes.Equal(fastThumb.Pix, slowThumb.Pix) {
				test.Fatalf("%s (edge=%d) thumbnail fast path diverged from Color.RGBA fallback", tc.name, edge)
			}
		}
	}
}
