package compress

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"

	xdraw "golang.org/x/image/draw"

	"zpic/internal/toolio"
)

const (
	MinResizeDimension = 64
	MaxResizeDimension = 16384
)

// MetadataPolicy controls how non-pixel metadata is preserved or stripped.
// The zero value keeps existing per-format default behavior.
type MetadataPolicy string

const (
	MetadataKeepAll MetadataPolicy = ""
	MetadataKeepICC MetadataPolicy = "icc"
	MetadataStrip   MetadataPolicy = "strip"
)

func (p MetadataPolicy) Validate() error {
	switch p {
	case MetadataKeepAll, MetadataKeepICC, MetadataStrip:
		return nil
	default:
		return fmt.Errorf("不支持的元数据策略：%q", string(p))
	}
}

// OutputFormat selects a JPEG, PNG, WebP, or AVIF output container.
// The zero value keeps the input format, with HEIC exported as PNG in lossless
// mode and JPEG otherwise.
type OutputFormat string

const (
	OutputFormatKeep OutputFormat = ""
	OutputFormatJPEG OutputFormat = "jpeg"
	OutputFormatPNG  OutputFormat = "png"
	OutputFormatWebP OutputFormat = "webp"
	OutputFormatAVIF OutputFormat = "avif"
)

func (f OutputFormat) Validate() error {
	switch f {
	case OutputFormatKeep, OutputFormatJPEG, OutputFormatPNG, OutputFormatWebP, OutputFormatAVIF:
		return nil
	default:
		return fmt.Errorf("不支持的输出格式：%q", string(f))
	}
}

// ValidateMaxDimension checks an optional long-edge downscale cap.
// Zero disables resizing; nonzero values must fall within [MinResizeDimension, MaxResizeDimension].
func ValidateMaxDimension(maxDim int) error {
	if maxDim == 0 {
		return nil
	}
	if maxDim < MinResizeDimension || maxDim > MaxResizeDimension {
		return fmt.Errorf("最大边长必须为 0（不缩放）或 %d 到 %d 之间", MinResizeDimension, MaxResizeDimension)
	}
	return nil
}

// ValidateResizeOptions accepts at most one nonzero setting. Pixel limits must
// be within [MinResizeDimension, MaxResizeDimension], and percentages within [1, 100].
func ValidateResizeOptions(maxDimension, maxWidth, maxHeight, scalePercent int) error {
	if err := ValidateMaxDimension(maxDimension); err != nil {
		return err
	}
	if maxWidth != 0 && (maxWidth < MinResizeDimension || maxWidth > MaxResizeDimension) {
		return fmt.Errorf("最大宽度必须为 0（不缩放）或 %d 到 %d 之间", MinResizeDimension, MaxResizeDimension)
	}
	if maxHeight != 0 && (maxHeight < MinResizeDimension || maxHeight > MaxResizeDimension) {
		return fmt.Errorf("最大高度必须为 0（不缩放）或 %d 到 %d 之间", MinResizeDimension, MaxResizeDimension)
	}
	if scalePercent < 0 || scalePercent > 100 {
		return fmt.Errorf("缩放百分比必须为 0（不缩放）或 1 到 100 之间")
	}
	enabled := 0
	for _, value := range [...]int{maxDimension, maxWidth, maxHeight, scalePercent} {
		if value != 0 {
			enabled++
		}
	}
	if enabled > 1 {
		return fmt.Errorf("最大边长、最大宽度、最大高度和缩放百分比只能设置一项")
	}
	return nil
}

func resizedDimensions(width, height, maxDim int) (int, int, bool) {
	if maxDim <= 0 || width <= 0 || height <= 0 {
		return width, height, false
	}
	longest := max(width, height)
	if longest <= maxDim {
		return width, height, false
	}
	scale := float64(maxDim) / float64(longest)
	if width >= height {
		return maxDim, max(1, int(math.Round(float64(height)*scale))), true
	}
	return max(1, int(math.Round(float64(width)*scale))), maxDim, true
}

func resizeDimensionsForOptions(width, height int, options Options) (int, int, bool) {
	if width <= 0 || height <= 0 {
		return width, height, false
	}
	switch {
	case options.MaxWidth > 0 && options.MaxWidth < width:
		return options.MaxWidth, scaledDimension(height, options.MaxWidth, width), true
	case options.MaxHeight > 0 && options.MaxHeight < height:
		return scaledDimension(width, options.MaxHeight, height), options.MaxHeight, true
	case options.ScalePercent > 0 && options.ScalePercent < 100:
		w := scaledDimension(width, options.ScalePercent, 100)
		h := scaledDimension(height, options.ScalePercent, 100)
		return w, h, w != width || h != height
	default:
		return resizedDimensions(width, height, options.MaxDimension)
	}
}

func scaledDimension(size, numerator, denominator int) int {
	// Round to the nearest pixel with half-pixels rounded up. The product
	// can exceed 32 bits even with a validated input and resize settings.
	return max(1, int((int64(size)*int64(numerator)+int64(denominator)/2)/int64(denominator)))
}

func resizeImage(ctx context.Context, img image.Image, options Options) (image.Image, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bounds := img.Bounds()
	dstW, dstH, changed := resizeDimensionsForOptions(bounds.Dx(), bounds.Dy(), options)
	if !changed {
		return img, nil
	}
	dstBounds := image.Rect(0, 0, dstW, dstH)
	var dst xdraw.Image
	switch img.ColorModel() {
	case color.GrayModel:
		dst = image.NewGray(dstBounds)
	case color.Gray16Model:
		dst = image.NewGray16(dstBounds)
	default:
		dst = image.NewRGBA(dstBounds)
	}
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, bounds, xdraw.Src, nil)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return dst, nil
}

func encodeResizedPNG(ctx context.Context, img image.Image, chunks [][]byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	output := pngTransformOutput{ctx: ctx, limit: maxIntermediatePNGSize}
	for _, chunk := range chunks {
		output.limit -= len(chunk)
	}
	if output.limit <= 0 {
		return nil, fmt.Errorf("中间 PNG 超过 128 MiB 限制")
	}
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := encoder.Encode(&output, img); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return injectPNGChunksAfterIHDR(output.buf.Bytes(), chunks), nil
}

type pngTransformOutput struct {
	ctx   context.Context
	buf   toolio.BoundedBuffer
	limit int
}

func (out *pngTransformOutput) Write(data []byte) (int, error) {
	if err := out.ctx.Err(); err != nil {
		return 0, err
	}
	if len(data) > out.limit-out.buf.Len() {
		return 0, fmt.Errorf("中间 PNG 超过 128 MiB 限制")
	}
	out.buf.AppendBounded(nil, data, out.limit)
	return len(data), nil
}

// extractPNGColorChunks returns raw display/color chunks (iCCP, sRGB, cICP, pHYs)
// so resized PNGs retain color fidelity.
func extractPNGColorChunks(data []byte) [][]byte {
	if len(data) < len(pngSignature)+12 || [8]byte(data[:8]) != pngSignature {
		return nil
	}
	var chunks [][]byte
	total := 0
	for offset := len(pngSignature); offset <= len(data)-12; {
		size := uint64(binary.BigEndian.Uint32(data[offset:]))
		if size > uint64(len(data)-offset-12) {
			return nil
		}
		end := offset + 12 + int(size)
		kind := string(data[offset+4 : offset+8])
		if kind == "IEND" {
			break
		}
		switch kind {
		case "iCCP", "sRGB", "cICP", "pHYs":
			total += end - offset
			if total > maxJPEGICCBytes {
				return nil
			}
			chunks = append(chunks, data[offset:end])
		}
		offset = end
	}
	return chunks
}

func injectPNGChunksAfterIHDR(encoded []byte, chunks [][]byte) []byte {
	if len(chunks) == 0 || len(encoded) < 33 || [8]byte(encoded[:8]) != pngSignature {
		return encoded
	}
	ihdrSize := int(binary.BigEndian.Uint32(encoded[8:12]))
	insertAt := 8 + 12 + ihdrSize
	if insertAt < 33 || insertAt > len(encoded) || string(encoded[12:16]) != "IHDR" {
		return encoded
	}
	extra := 0
	for _, chunk := range chunks {
		extra += len(chunk)
	}
	out := make([]byte, 0, len(encoded)+extra)
	out = append(out, encoded[:insertAt]...)
	for _, chunk := range chunks {
		out = append(out, chunk...)
	}
	out = append(out, encoded[insertAt:]...)
	return out
}

func extractJPEGICCProfile(data []byte, outputComponents int) []byte {
	segments, _ := jpegCompatibleICCSegments(data, outputComponents)
	if len(segments) == 0 {
		return nil
	}
	total := 0
	for _, seg := range segments {
		if len(seg) < 18 {
			return nil
		}
		total += len(seg) - 18
	}
	if total == 0 || total > maxJPEGICCBytes {
		return nil
	}
	profile := make([]byte, 0, total)
	for _, seg := range segments {
		profile = append(profile, seg[18:]...)
	}
	return profile
}

func extractPNGICCProfile(data []byte) []byte {
	return extractPNGICCProfileForComponents(data, 3)
}

// extractPNGICCProfileForComponents reads a bounded iCCP profile for grayscale
// or RGB output.
func extractPNGICCProfileForComponents(data []byte, components int) []byte {
	if components != 1 && components != 3 {
		return nil
	}
	if len(data) < len(pngSignature)+12 || [8]byte(data[:8]) != pngSignature {
		return nil
	}
	for offset := len(pngSignature); offset <= len(data)-12; {
		size := uint64(binary.BigEndian.Uint32(data[offset:]))
		if size > uint64(len(data)-offset-12) {
			return nil
		}
		kind := string(data[offset+4 : offset+8])
		if kind == "IEND" {
			return nil
		}
		if kind == "iCCP" {
			payload := data[offset+8 : offset+8+int(size)]
			nul := bytes.IndexByte(payload, 0)
			if nul <= 0 || nul+2 >= len(payload) || payload[nul+1] != 0 {
				return nil
			}
			zr, err := zlib.NewReader(bytes.NewReader(payload[nul+2:]))
			if err != nil {
				return nil
			}
			icc, err := io.ReadAll(io.LimitReader(zr, maxJPEGICCBytes+1))
			closeErr := zr.Close()
			if err != nil || closeErr != nil || len(icc) == 0 || len(icc) > maxJPEGICCBytes {
				return nil
			}
			headerLen := min(40, len(icc))
			if !compatibleICCColorSpace(icc[:headerLen], len(icc), components) {
				return nil
			}
			return icc
		}
		offset += 12 + int(size)
	}
	return nil
}

// buildPNGICCPChunk wraps a compatible profile in a complete PNG iCCP chunk.
func buildPNGICCPChunk(icc []byte, components int) []byte {
	if len(icc) == 0 || len(icc) > maxJPEGICCBytes || (components != 1 && components != 3) {
		return nil
	}
	if !compatibleICCColorSpace(icc[:min(len(icc), 40)], len(icc), components) {
		return nil
	}

	var buf bytes.Buffer
	buf.WriteString("\x00\x00\x00\x00iCCP") // Length filled in after compression.
	buf.WriteString("ICC Profile\x00\x00")  // Profile name and zlib compression method.
	zw := zlib.NewWriter(&buf)
	_, writeErr := zw.Write(icc)
	if closeErr := zw.Close(); writeErr != nil || closeErr != nil {
		return nil
	}
	chunk := buf.Bytes()
	if len(chunk)+4 > maxJPEGICCBytes {
		return nil
	}
	binary.BigEndian.PutUint32(chunk[:4], uint32(len(chunk)-8))
	return binary.BigEndian.AppendUint32(chunk, crc32.ChecksumIEEE(chunk[4:]))
}

func extractWebPICCProfile(data []byte) []byte {
	icc := webpChunkPayload(data, "ICCP")
	if len(icc) == 0 || len(icc) > maxJPEGICCBytes {
		return nil
	}
	return bytes.Clone(icc)
}

// injectWebPICC adds or replaces an ICCP chunk in a static WebP container,
// upgrading simple VP8/VP8L bitstreams to extended VP8X format per RFC 9649.
func injectWebPICC(webpData, icc []byte, width, height int) []byte {
	if len(icc) == 0 || len(icc) > maxJPEGICCBytes ||
		len(webpData) < 20 || string(webpData[:4]) != "RIFF" || string(webpData[8:12]) != "WEBP" ||
		width <= 0 || height <= 0 || width > 1<<24 || height > 1<<24 {
		return webpData
	}
	var vp8xPayload [10]byte
	hasVP8X := false
	hasAlpha := false
	type rawChunk struct {
		kind string
		raw  []byte
	}
	var chunks []rawChunk
	for offset := 12; offset <= len(webpData)-8; {
		kind := string(webpData[offset : offset+4])
		length := int(binary.LittleEndian.Uint32(webpData[offset+4 : offset+8]))
		if length < 0 || length > len(webpData)-offset-8 {
			return webpData
		}
		padded := length + length%2
		if padded > len(webpData)-offset-8 {
			return webpData
		}
		end := offset + 8 + padded
		switch kind {
		case "VP8X":
			if length != 10 {
				return webpData
			}
			copy(vp8xPayload[:], webpData[offset+8:offset+18])
			hasVP8X = true
		case "ICCP":
			// Replaced with the caller's profile below.
		default:
			if kind == "ALPH" {
				hasAlpha = true
			} else if kind == "VP8L" && length >= 5 && webpData[offset+8] == 0x2f && (webpData[offset+12]&0x10) != 0 {
				hasAlpha = true
			}
			chunks = append(chunks, rawChunk{kind: kind, raw: webpData[offset:end]})
		}
		offset = end
	}
	if len(chunks) == 0 {
		return webpData
	}
	if !hasVP8X {
		if hasAlpha {
			vp8xPayload[0] |= 0x10
		}
		wMinus1 := width - 1
		hMinus1 := height - 1
		vp8xPayload[4] = byte(wMinus1)
		vp8xPayload[5] = byte(wMinus1 >> 8)
		vp8xPayload[6] = byte(wMinus1 >> 16)
		vp8xPayload[7] = byte(hMinus1)
		vp8xPayload[8] = byte(hMinus1 >> 8)
		vp8xPayload[9] = byte(hMinus1 >> 16)
	}
	vp8xPayload[0] |= 0x20 // ICCP flag

	iccPad := len(icc) % 2
	bodyLen := 4 + (8 + 10) + (8 + len(icc) + iccPad)
	for _, c := range chunks {
		bodyLen += len(c.raw)
	}
	if bodyLen+8 > maxInputSize {
		return webpData
	}
	out := make([]byte, 0, bodyLen+8)
	out = append(out, "RIFF"...)
	out = binary.LittleEndian.AppendUint32(out, uint32(bodyLen))
	out = append(out, "WEBP"...)
	out = append(out, "VP8X"...)
	out = binary.LittleEndian.AppendUint32(out, 10)
	out = append(out, vp8xPayload[:]...)
	out = append(out, "ICCP"...)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(icc)))
	out = append(out, icc...)
	if iccPad == 1 {
		out = append(out, 0)
	}
	for _, c := range chunks {
		out = append(out, c.raw...)
	}
	return out
}

func webpHasStrippableMetadata(data []byte, policy MetadataPolicy) bool {
	if policy == MetadataKeepAll || len(data) < 20 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return false
	}
	for offset := 12; offset <= len(data)-8; {
		kind := string(data[offset : offset+4])
		length := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		if length < 0 || length > len(data)-offset-8 {
			return false
		}
		switch kind {
		case "EXIF", "XMP ":
			return true
		case "ICCP":
			if policy == MetadataStrip {
				return true
			}
		}
		padded := length + length%2
		if padded > len(data)-offset-8 {
			return false
		}
		offset += 8 + padded
	}
	return false
}

func jpegHasStrippableMetadata(data []byte, policy MetadataPolicy) bool {
	if policy == MetadataKeepAll || len(data) < 4 || data[0] != 0xff || data[1] != 0xd8 {
		return false
	}
	for offset := 2; offset < len(data); {
		if data[offset] != 0xff {
			return false
		}
		for offset < len(data) && data[offset] == 0xff {
			offset++
		}
		if offset >= len(data) {
			return false
		}
		marker := data[offset]
		offset++
		switch {
		case marker == 0xda || marker == 0xd9:
			return false
		case marker == 0x01 || (marker >= 0xd0 && marker <= 0xd7):
			continue
		case marker == 0 || marker == 0xd8:
			return false
		}
		if len(data)-offset < 2 {
			return false
		}
		length := int(binary.BigEndian.Uint16(data[offset : offset+2]))
		if length < 2 || length > len(data)-offset {
			return false
		}
		segment := data[offset+2 : offset+length]
		switch {
		case marker == 0xfe: // COM
			return true
		case marker == 0xe2 && bytes.HasPrefix(segment, jpegICCSignature):
			if policy == MetadataStrip {
				return true
			}
		case marker >= 0xe1 && marker <= 0xef:
			return true
		}
		offset += length
	}
	return false
}
