package compress

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/color"
)

// jpegOrientation reads only Exif TIFF IFD0 in APP1 segments before image data.
// Missing or malformed metadata is treated as the normal display orientation.
func jpegOrientation(data []byte) int {
	return tiffOrientation(jpegAPP1Payload(data, "Exif\x00\x00"))
}

func jpegAPP1Payload(data []byte, signature string) []byte {
	if len(data) < 2 || data[0] != 0xff || data[1] != 0xd8 {
		return nil
	}
	for offset := 2; offset < len(data); {
		if data[offset] != 0xff {
			return nil
		}
		for offset < len(data) && data[offset] == 0xff {
			offset++
		}
		if offset >= len(data) {
			return nil
		}
		marker := data[offset]
		offset++
		switch {
		case marker == 0xda || marker == 0xd9:
			return nil
		case marker == 0x01 || marker >= 0xd0 && marker <= 0xd7:
			continue
		case marker == 0 || marker == 0xd8:
			return nil
		}
		if len(data)-offset < 2 {
			return nil
		}
		length := int(binary.BigEndian.Uint16(data[offset : offset+2]))
		if length < 2 || length > len(data)-offset {
			return nil
		}
		segment := data[offset+2 : offset+length]
		if marker == 0xe1 && bytes.HasPrefix(segment, []byte(signature)) {
			return segment[len(signature):]
		}
		offset += length
	}
	return nil
}

func exifPayloadOrientation(payload []byte) int {
	if bytes.HasPrefix(payload, []byte("Exif\x00\x00")) {
		payload = payload[6:]
	}
	return tiffOrientation(payload)
}

// WebPOrientation reads the first EXIF chunk in a RIFF/WEBP container and
// returns its TIFF IFD0 orientation in [1, 8], defaulting to 1 when absent or malformed.
func WebPOrientation(data []byte) int {
	return webpOrientation(data)
}

func webpOrientation(data []byte) int {
	return exifPayloadOrientation(webpChunkPayload(data, "EXIF"))
}

func webpChunkPayload(data []byte, name string) []byte {
	if len(data) < 20 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return nil
	}
	for offset := 12; offset <= len(data)-8; {
		kind := string(data[offset : offset+4])
		length := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		if length < 0 || length > len(data)-offset-8 {
			return nil
		}
		if kind == name {
			return data[offset+8 : offset+8+length]
		}
		padded := length + length%2
		if padded > len(data)-offset-8 {
			return nil
		}
		offset += 8 + padded
	}
	return nil
}

// NormalizeOrientation wraps img with an upright coordinate mapping when orientation is in [2, 8].
func NormalizeOrientation(img image.Image, orientation int) image.Image {
	return normalizeOrientation(img, orientation)
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

func orientForJPEGEncode(ctx context.Context, img image.Image, orientation int) (image.Image, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if orientation < 2 || orientation > 8 {
		return img, nil
	}
	oriented := orientedImage{Image: img, orientation: orientation}
	bounds := oriented.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	switch source := img.(type) {
	case *image.YCbCr:
		ratio := orientedYCbCrRatio(source)
		output := image.NewYCbCr(bounds, ratio)
		if err := copyOrientedYCbCrPlanes(ctx, output, source, oriented, ratio); err != nil {
			return nil, err
		}
		return output, nil
	case *image.NYCbCrA:
		ratio := orientedYCbCrRatio(&source.YCbCr)
		output := image.NewNYCbCrA(bounds, ratio)
		if err := copyOrientedYCbCrPlanes(ctx, &output.YCbCr, &source.YCbCr, oriented, ratio); err != nil {
			return nil, err
		}
		if err := copyOrientedPlane(ctx, output.A, output.AStride, source.A, source.AStride, oriented, 0); err != nil {
			return nil, err
		}
		return output, nil
	case *image.Gray:
		output := image.NewGray(bounds)
		if err := copyOrientedPlane(ctx, output.Pix, output.Stride, source.Pix, source.Stride, oriented, 0); err != nil {
			return nil, err
		}
		return output, nil
	default:
		output := image.NewRGBA(bounds)
		for row := 0; row < height; row++ {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
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
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return output, nil
	}
}

func orientedYCbCrRatio(source *image.YCbCr) image.YCbCrSubsampleRatio {
	if source.SubsampleRatio == image.YCbCrSubsampleRatio420 &&
		source.Rect.Min == (image.Point{}) &&
		source.Rect.Dx()%2 == 0 && source.Rect.Dy()%2 == 0 {
		return image.YCbCrSubsampleRatio420
	}
	return image.YCbCrSubsampleRatio444
}

func copyOrientedYCbCrPlanes(ctx context.Context, output, source *image.YCbCr, oriented orientedImage, ratio image.YCbCrSubsampleRatio) error {
	if err := copyOrientedPlane(ctx, output.Y, output.YStride, source.Y, source.YStride, oriented, 0); err != nil {
		return err
	}
	if ratio == image.YCbCrSubsampleRatio420 {
		if err := copyOrientedPlane(ctx, output.Cb, output.CStride, source.Cb, source.CStride, oriented, 1); err != nil {
			return err
		}
		return copyOrientedPlane(ctx, output.Cr, output.CStride, source.Cr, source.CStride, oriented, 1)
	}
	bounds := oriented.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	originX, originY := oriented.mapCoords(0, 0)
	nextX, nextY := oriented.mapCoords(1, 0)
	stepX, stepY := nextX-originX, nextY-originY
	for row := range height {
		if err := ctx.Err(); err != nil {
			return err
		}
		rowBase := row * output.CStride
		mappedX, mappedY := oriented.mapCoords(0, row)
		for column := range width {
			sourceIndex := source.COffset(mappedX, mappedY)
			output.Cb[rowBase+column] = source.Cb[sourceIndex]
			output.Cr[rowBase+column] = source.Cr[sourceIndex]
			mappedX += stepX
			mappedY += stepY
		}
	}
	return ctx.Err()
}

func copyOrientedPlane(ctx context.Context, output []byte, outputStride int, source []byte, sourceStride int, oriented orientedImage, subsampleShift int) error {
	bounds := oriented.Bounds()
	width, height := bounds.Dx()>>subsampleShift, bounds.Dy()>>subsampleShift
	sourceBounds := oriented.Image.Bounds()
	originX, originY := oriented.mapCoords(0, 0)
	nextX, nextY := oriented.mapCoords(1, 0)
	nextRowX, nextRowY := oriented.mapCoords(0, 1)
	columnStep := nextX - originX + (nextY-originY)*sourceStride
	rowStep := nextRowX - originX + (nextRowY-originY)*sourceStride
	sourceX := (originX - sourceBounds.Min.X) >> subsampleShift
	sourceY := (originY - sourceBounds.Min.Y) >> subsampleShift
	origin := sourceX + sourceY*sourceStride
	blockWidth, blockHeight := max(1, width), max(1, height)
	if nextY != originY {
		blockWidth, blockHeight = 32, 32
	}
	for top := 0; top < height; top += blockHeight {
		bottom := min(top+blockHeight, height)
		for left := 0; left < width; left += blockWidth {
			right := min(left+blockWidth, width)
			for row := top; row < bottom; row++ {
				if err := ctx.Err(); err != nil {
					return err
				}
				outputRow := output[row*outputStride+left : row*outputStride+right]
				sourceIndex := origin + row*rowStep + left*columnStep
				for column := range outputRow {
					outputRow[column] = source[sourceIndex]
					sourceIndex += columnStep
				}
			}
		}
	}
	return ctx.Err()
}

func (img orientedImage) Bounds() image.Rectangle {
	bounds := img.Image.Bounds()
	if img.orientation >= 5 {
		return image.Rect(0, 0, bounds.Dy(), bounds.Dx())
	}
	return image.Rect(0, 0, bounds.Dx(), bounds.Dy())
}

func (img orientedImage) Opaque() bool {
	if source, ok := img.Image.(interface{ Opaque() bool }); ok {
		return source.Opaque()
	}
	bounds := img.Bounds()
	for row := bounds.Min.Y; row < bounds.Max.Y; row++ {
		for column := bounds.Min.X; column < bounds.Max.X; column++ {
			if img.RGBA64At(column, row).A != 0xffff {
				return false
			}
		}
	}
	return true
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
