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

func orientForJPEGEncode(img image.Image, orientation int) image.Image {
	if orientation < 2 || orientation > 8 {
		return img
	}
	oriented := orientedImage{Image: img, orientation: orientation}
	bounds := oriented.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	switch source := img.(type) {
	case *image.YCbCr:
		output := image.NewYCbCr(bounds, image.YCbCrSubsampleRatio444)
		for row := 0; row < height; row++ {
			rowBase := row * output.YStride
			chromaBase := row * output.CStride
			for column := 0; column < width; column++ {
				mappedX, mappedY := oriented.mapCoords(column, row)
				yIndex := source.YOffset(mappedX, mappedY)
				cIndex := source.COffset(mappedX, mappedY)
				output.Y[rowBase+column] = source.Y[yIndex]
				output.Cb[chromaBase+column] = source.Cb[cIndex]
				output.Cr[chromaBase+column] = source.Cr[cIndex]
			}
		}
		return output
	case *image.Gray:
		output := image.NewGray(bounds)
		for row := 0; row < height; row++ {
			rowBase := row * output.Stride
			for column := 0; column < width; column++ {
				mappedX, mappedY := oriented.mapCoords(column, row)
				output.Pix[rowBase+column] = source.GrayAt(mappedX, mappedY).Y
			}
		}
		return output
	default:
		output := image.NewRGBA(bounds)
		for row := 0; row < height; row++ {
			rowBase := row * output.Stride
			for column := 0; column < width; column++ {
				pixel := oriented.RGBA64At(column, row)
				offset := rowBase + column*4
				output.Pix[offset] = uint8(pixel.R >> 8)
				output.Pix[offset+1] = uint8(pixel.G >> 8)
				output.Pix[offset+2] = uint8(pixel.B >> 8)
				output.Pix[offset+3] = uint8(pixel.A >> 8)
			}
		}
		return output
	}
}

func (img orientedImage) Bounds() image.Rectangle {
	bounds := img.Image.Bounds()
	if img.orientation >= 5 {
		return image.Rect(0, 0, bounds.Dy(), bounds.Dx())
	}
	return image.Rect(0, 0, bounds.Dx(), bounds.Dy())
}

func (img orientedImage) mapCoords(column, row int) (int, int) {
	bounds := img.Image.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	switch img.orientation {
	case 2:
		column = width - 1 - column
	case 3:
		column, row = width-1-column, height-1-row
	case 4:
		row = height - 1 - row
	case 5:
		column, row = row, column
	case 6:
		column, row = row, height-1-column
	case 7:
		column, row = width-1-row, height-1-column
	case 8:
		column, row = width-1-row, column
	}
	return column + bounds.Min.X, row + bounds.Min.Y
}

func (img orientedImage) At(column, row int) color.Color {
	mappedX, mappedY := img.mapCoords(column, row)
	return img.Image.At(mappedX, mappedY)
}

func (img orientedImage) RGBA64At(column, row int) color.RGBA64 {
	mappedX, mappedY := img.mapCoords(column, row)
	if rgba64Image, ok := img.Image.(image.RGBA64Image); ok {
		return rgba64Image.RGBA64At(mappedX, mappedY)
	}
	red, green, blue, alpha := img.Image.At(mappedX, mappedY).RGBA()
	return color.RGBA64{
		R: uint16(red),
		G: uint16(green),
		B: uint16(blue),
		A: uint16(alpha),
	}
}
