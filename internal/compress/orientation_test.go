package compress

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func exifTIFF(order binary.ByteOrder, orientation uint16) []byte {
	data := make([]byte, 26)
	if order == binary.LittleEndian {
		copy(data, "II")
	} else {
		copy(data, "MM")
	}
	order.PutUint16(data[2:4], 42)
	order.PutUint32(data[4:8], 8)
	order.PutUint16(data[8:10], 1)
	order.PutUint16(data[10:12], 0x0112)
	order.PutUint16(data[12:14], 3)
	order.PutUint32(data[14:18], 1)
	order.PutUint16(data[18:20], orientation)
	return data
}

func withEXIF(jpegData, tiff []byte) []byte {
	payload := append([]byte("Exif\x00\x00"), tiff...)
	out := append([]byte(nil), jpegData[:2]...)
	out = append(out, 0xff, 0xe1, byte((len(payload)+2)>>8), byte(len(payload)+2))
	out = append(out, payload...)
	return append(out, jpegData[2:]...)
}

func TestAllOrientationPixelMappings(t *testing.T) {
	img := image.NewGray(image.Rect(7, 9, 10, 11))
	for y := 0; y < 2; y++ {
		for x := 0; x < 3; x++ {
			img.SetGray(x+7, y+9, color.Gray{Y: uint8(y*3 + x + 1)})
		}
	}
	expected := []string{"123456", "321654", "654321", "456123",
		"142536", "415263", "635241", "362514"}
	for orientation := 1; orientation <= 8; orientation++ {
		t.Run(strconv.Itoa(orientation), func(t *testing.T) {
			normalized := normalizeOrientation(img, orientation)
			bounds := normalized.Bounds()
			var got []byte
			for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
				for x := bounds.Min.X; x < bounds.Max.X; x++ {
					value := color.GrayModel.Convert(normalized.At(x, y)).(color.Gray).Y
					got = append(got, '0'+value)
				}
			}
			if string(got) != expected[orientation-1] {
				t.Fatalf("got %s, want %s", got, expected[orientation-1])
			}
			if orientation >= 5 && (bounds.Dx() != 2 || bounds.Dy() != 3) {
				t.Fatalf("dimensions not swapped: %v", bounds)
			}
		})
	}
}

func TestJPEGExportNormalizesOrientation(t *testing.T) {
	// Four large gray blocks allow robust position assertions after lossy
	// encoding; EXIF padding ensures a smaller re-encoded output.
	img := image.NewGray(image.Rect(0, 0, 80, 48))
	levels := []uint8{30, 90, 150, 220}
	for y := 0; y < 48; y++ {
		for x := 0; x < 80; x++ {
			img.SetGray(x, y, color.Gray{Y: levels[(y/24)*2+x/40]})
		}
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, img, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		orientation   uint16
		width, height int
		corners       []uint8
	}{
		{6, 48, 80, []uint8{150, 30, 220, 90}},
		{2, 80, 48, []uint8{90, 30, 220, 150}},
	} {
		t.Run(strconv.Itoa(int(tc.orientation)), func(t *testing.T) {
			tiff := append(exifTIFF(binary.LittleEndian, tc.orientation), make([]byte, 8192)...)
			source := withEXIF(encoded.Bytes(), tiff)
			result, err := File(context.Background(), writeFixture(t, "photo.jpg", source), Options{Quality: 85})
			if err != nil {
				t.Fatal(err)
			}
			if result.Unchanged || result.Width != tc.width || result.Height != tc.height {
				t.Fatalf("unexpected dimensions or fallback: %dx%d unchanged=%v",
					result.Width, result.Height, result.Unchanged)
			}
			target := filepath.Join(t.TempDir(), "export.jpg")
			if err := result.Save(target); err != nil {
				t.Fatal(err)
			}
			saved, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := jpeg.Decode(bytes.NewReader(saved))
			if err != nil {
				t.Fatal(err)
			}
			if decoded.Bounds().Dx() != tc.width || decoded.Bounds().Dy() != tc.height {
				t.Fatalf("encoded dimensions: %v", decoded.Bounds())
			}
			points := []image.Point{{8, 8}, {tc.width - 9, 8},
				{8, tc.height - 9}, {tc.width - 9, tc.height - 9}}
			for i, point := range points {
				got := color.GrayModel.Convert(decoded.At(point.X, point.Y)).(color.Gray).Y
				delta := int(got) - int(tc.corners[i])
				if delta < -5 || delta > 5 {
					t.Fatalf("corner %d: got %d, want approximately %d", i, got, tc.corners[i])
				}
			}
			if bytes.Contains(saved, []byte("Exif\x00\x00")) {
				t.Fatal("old EXIF orientation remains in normalized output")
			}
		})
	}
}

func TestOrientationFallbackReportsDisplayDimensions(t *testing.T) {
	for _, orientation := range []uint16{2, 6} {
		data := withEXIF(jpegFixture(t, 1), exifTIFF(binary.BigEndian, orientation))
		result, err := File(context.Background(), writeFixture(t, "photo.jpg", data), Options{Quality: 100})
		if err != nil {
			t.Fatal(err)
		}
		width, height := 128, 96
		if orientation >= 5 {
			width, height = height, width
		}
		if !result.Unchanged || !bytes.Equal(result.Data, data) ||
			result.Width != width || result.Height != height {
			t.Fatalf("orientation %d fallback: %dx%d, unchanged=%v",
				orientation, result.Width, result.Height, result.Unchanged)
		}
	}
}

func TestEXIFReaderBoundsAndByteOrders(t *testing.T) {
	for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		for orientation := uint16(1); orientation <= 8; orientation++ {
			data := withEXIF([]byte{0xff, 0xd8, 0xff, 0xd9}, exifTIFF(order, orientation))
			if got := jpegOrientation(data); got != int(orientation) {
				t.Fatalf("orientation %d: got %d", orientation, got)
			}
			for n := 0; n < len(data)-2; n++ {
				if got := jpegOrientation(data[:n]); got != 1 {
					t.Fatalf("truncated APP1 accepted at length %d: %d", n, got)
				}
			}
		}
	}
	for name, mutate := range map[string]func([]byte){
		"byte order":           func(b []byte) { b[0] = 'X' },
		"magic":                func(b []byte) { b[2] = 0 },
		"IFD offset overflow":  func(b []byte) { binary.LittleEndian.PutUint32(b[4:8], 0xffffffff) },
		"IFD overlaps header":  func(b []byte) { binary.LittleEndian.PutUint32(b[4:8], 2) },
		"entry count overflow": func(b []byte) { binary.LittleEndian.PutUint16(b[8:10], 0xffff) },
		"wrong type":           func(b []byte) { binary.LittleEndian.PutUint16(b[12:14], 4) },
		"wrong count":          func(b []byte) { binary.LittleEndian.PutUint32(b[14:18], 2) },
		"zero orientation":     func(b []byte) { binary.LittleEndian.PutUint16(b[18:20], 0) },
		"invalid orientation":  func(b []byte) { binary.LittleEndian.PutUint16(b[18:20], 9) },
		"no orientation":       func(b []byte) { binary.LittleEndian.PutUint16(b[10:12], 0x0113) },
	} {
		t.Run(name, func(t *testing.T) {
			data := exifTIFF(binary.LittleEndian, 6)
			mutate(data)
			if got := jpegOrientation(withEXIF([]byte{0xff, 0xd8, 0xff, 0xd9}, data)); got != 1 {
				t.Fatalf("invalid EXIF returned %d", got)
			}
		})
	}
}

func TestMalformedEXIFDoesNotRejectJPEG(t *testing.T) {
	tiff := exifTIFF(binary.LittleEndian, 6)
	binary.LittleEndian.PutUint32(tiff[4:8], 0xffffffff)
	data := withEXIF(jpegFixture(t, 100), tiff)
	result, err := File(context.Background(), writeFixture(t, "photo.jpg", data), Options{Quality: 80})
	if err != nil {
		t.Fatal(err)
	}
	if result.Width != 128 || result.Height != 96 {
		t.Fatalf("invalid EXIF changed dimensions: %dx%d", result.Width, result.Height)
	}
}
