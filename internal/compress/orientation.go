package compress

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
)

// jpegOrientation reads only Exif TIFF IFD0 in APP1 segments before image data.
// Missing or malformed metadata is treated as the normal display orientation.
func jpegOrientation(data []byte) int {
	if len(data) < 2 || data[0] != 0xff || data[1] != 0xd8 {
		return 1
	}
	for offset := 2; offset < len(data); {
		if data[offset] != 0xff {
			return 1
		}
		for offset < len(data) && data[offset] == 0xff {
			offset++
		}
		if offset >= len(data) {
			return 1
		}
		marker := data[offset]
		offset++
		switch {
		case marker == 0xda || marker == 0xd9:
			return 1
		case marker == 0x01 || marker >= 0xd0 && marker <= 0xd7:
			continue
		case marker == 0 || marker == 0xd8:
			return 1
		}
		if len(data)-offset < 2 {
			return 1
		}
		length := int(binary.BigEndian.Uint16(data[offset : offset+2]))
		if length < 2 || length > len(data)-offset {
			return 1
		}
		segment := data[offset+2 : offset+length]
		if marker == 0xe1 && bytes.HasPrefix(segment, []byte("Exif\x00\x00")) {
			return tiffOrientation(segment[6:])
		}
		offset += length
	}
	return 1
}

func tiffOrientation(data []byte) int {
	if len(data) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(data[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	if order.Uint16(data[2:4]) != 42 {
		return 1
	}
	offset := uint64(order.Uint32(data[4:8]))
	// Include the count and trailing next-IFD offset. Use unsigned arithmetic
	// and check before converting to int or slicing.
	if offset < 8 || offset > uint64(len(data)) || uint64(len(data))-offset < 6 {
		return 1
	}
	ifd := data[int(offset):]
	count := int(order.Uint16(ifd[:2]))
	if count > (len(ifd)-6)/12 {
		return 1
	}
	for i := 0; i < count; i++ {
		entry := ifd[2+i*12 : 2+(i+1)*12]
		if order.Uint16(entry[:2]) != 0x0112 {
			continue
		}
		// Orientation is one inline SHORT, never an external value pointer.
		if order.Uint16(entry[2:4]) != 3 || order.Uint32(entry[4:8]) != 1 {
			return 1
		}
		value := int(order.Uint16(entry[8:10]))
		if value >= 1 && value <= 8 {
			return value
		}
		return 1
	}
	return 1
}

// orientedImage maps output pixels to the decoded image without allocating
// another full-size pixel buffer. JPEG encoding strips the old EXIF metadata.
type orientedImage struct {
	image.Image
	orientation int
}

func normalizeOrientation(img image.Image, orientation int) image.Image {
	if orientation < 2 || orientation > 8 {
		return img
	}
	return orientedImage{Image: img, orientation: orientation}
}

func (img orientedImage) Bounds() image.Rectangle {
	bounds := img.Image.Bounds()
	if img.orientation >= 5 {
		return image.Rect(0, 0, bounds.Dy(), bounds.Dx())
	}
	return image.Rect(0, 0, bounds.Dx(), bounds.Dy())
}

func (img orientedImage) At(x, y int) color.Color {
	bounds := img.Image.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	switch img.orientation {
	case 2:
		x = w - 1 - x
	case 3:
		x, y = w-1-x, h-1-y
	case 4:
		y = h - 1 - y
	case 5:
		x, y = y, x
	case 6:
		x, y = y, h-1-x
	case 7:
		x, y = w-1-y, h-1-x
	case 8:
		x, y = w-1-y, x
	}
	return img.Image.At(x+bounds.Min.X, y+bounds.Min.Y)
}
