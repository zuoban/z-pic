package compress

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
)

var errJPEGNoSavings = errors.New("JPEG 编码输出未减小")

var jpegICCSignature = []byte("ICC_PROFILE\x00")

const maxJPEGICCBytes = 4 << 20

type jpegOutput struct {
	ctx           context.Context
	buf           bytes.Buffer
	limit         int
	discarded     bool
	stopOnDiscard bool
}

func (output *jpegOutput) Write(data []byte) (int, error) {
	if output.ctx != nil {
		if err := output.ctx.Err(); err != nil {
			output.buf = bytes.Buffer{}
			return 0, err
		}
	}
	if output.discarded || len(data) == 0 {
		return len(data), nil
	}
	if output.limit <= 0 || len(data) >= output.limit-output.buf.Len() {
		output.buf = bytes.Buffer{}
		output.discarded = true
		if output.stopOnDiscard {
			return 0, errJPEGNoSavings
		}
		return len(data), nil
	}
	return output.buf.Write(data)
}

// jpegICCSegments extracts complete, well-formed APP2 ICC_PROFILE marker segments
// before SOS so wide-gamut (e.g. Display P3) JPEGs preserve color fidelity.
// Malformed or incomplete multi-part sequences return nil rather than corrupting output.
func jpegICCSegments(data []byte) ([][]byte, int) {
	if len(data) < 4 || data[0] != 0xff || data[1] != 0xd8 {
		return nil, 0
	}
	var slots [][]byte
	expectedCount := 0
	found := 0
	totalBytes := 0
	for offset := 2; offset < len(data); {
		if data[offset] != 0xff {
			return nil, 0
		}
		markerStart := offset
		for offset < len(data) && data[offset] == 0xff {
			offset++
		}
		if offset >= len(data) {
			break
		}
		marker := data[offset]
		offset++
		switch {
		case marker == 0xda || marker == 0xd9:
			offset = len(data)
			continue
		case marker == 0x01 || (marker >= 0xd0 && marker <= 0xd7):
			continue
		case marker == 0 || marker == 0xd8:
			return nil, 0
		}
		if len(data)-offset < 2 {
			return nil, 0
		}
		length := int(binary.BigEndian.Uint16(data[offset : offset+2]))
		if length < 2 || length > len(data)-offset {
			return nil, 0
		}
		segment := data[offset+2 : offset+length]
		if marker == 0xe2 && bytes.HasPrefix(segment, jpegICCSignature) {
			if len(segment) < 15 {
				return nil, 0
			}
			seq := int(segment[12])
			count := int(segment[13])
			if count < 1 || seq < 1 || seq > count {
				return nil, 0
			}
			if expectedCount == 0 {
				expectedCount = count
				slots = make([][]byte, count)
			} else if count != expectedCount {
				return nil, 0
			}
			if slots[seq-1] != nil {
				return nil, 0
			}
			// Canonicalize marker prefix to 2-byte 0xFF 0xE2 even if padded with fill 0xFFs.
			raw := data[offset-2 : offset+length]
			if markerStart < offset-2 {
				raw = append([]byte{0xff, 0xe2}, data[offset:offset+length]...)
			}
			totalBytes += len(raw)
			if totalBytes > maxJPEGICCBytes {
				return nil, 0
			}
			slots[seq-1] = raw
			found++
		}
		offset += length
	}
	if expectedCount == 0 || found != expectedCount {
		return nil, 0
	}
	return slots, totalBytes
}

// jpegCompatibleICCSegments returns extracted ICC_PROFILE segments only when
// the source JPEG framing and ICC color space match the encoded JPEG component
// count (1 for *image.Gray, 3 for YCbCr). For example, 4-component CMYK JPEGs
// are re-encoded by standard library image/jpeg as 3-component YCbCr, so their
// source CMYK ICC profiles must never be re-injected.
func jpegCompatibleICCSegments(data []byte, outputComponents int) ([][]byte, int) {
	if outputComponents != 1 && outputComponents != 3 {
		return nil, 0
	}
	if srcComp := jpegSOFComponents(data); srcComp != 0 && srcComp != outputComponents {
		return nil, 0
	}
	segments, totalBytes := jpegICCSegments(data)
	if len(segments) == 0 {
		return nil, 0
	}
	var header [40]byte
	headerLen := 0
	payloadBytes := 0
	for _, seg := range segments {
		if len(seg) < 18 {
			return nil, 0
		}
		chunk := seg[18:]
		payloadBytes += len(chunk)
		if headerLen < len(header) {
			headerLen += copy(header[headerLen:], chunk)
		}
	}
	if !compatibleICCColorSpace(header[:headerLen], payloadBytes, outputComponents) {
		return nil, 0
	}
	return segments, totalBytes
}

func compatibleICCColorSpace(header []byte, payloadBytes, outputComponents int) bool {
	if len(header) >= 40 && string(header[36:40]) == "acsp" {
		if payloadBytes < 128 {
			return false
		}
		cs := string(header[16:20])
		if outputComponents == 1 {
			return cs == "GRAY"
		}
		return cs == "RGB " || cs == "YCbr"
	}
	if len(header) >= 20 {
		cs := string(header[16:20])
		switch cs {
		case "CMYK", "Lab ", "XYZ ":
			return false
		case "GRAY":
			return outputComponents == 1
		case "RGB ", "YCbr":
			return outputComponents == 3
		}
	}
	return true
}

func jpegSOFComponents(data []byte) int {
	if len(data) < 4 || data[0] != 0xff || data[1] != 0xd8 {
		return 0
	}
	for offset := 2; offset < len(data); {
		if data[offset] != 0xff {
			return 0
		}
		for offset < len(data) && data[offset] == 0xff {
			offset++
		}
		if offset >= len(data) {
			return 0
		}
		marker := data[offset]
		offset++
		switch {
		case marker == 0xda || marker == 0xd9:
			return 0
		case marker == 0x01 || (marker >= 0xd0 && marker <= 0xd7):
			continue
		case marker == 0 || marker == 0xd8:
			return 0
		}
		if len(data)-offset < 2 {
			return 0
		}
		length := int(binary.BigEndian.Uint16(data[offset : offset+2]))
		if length < 2 || length > len(data)-offset {
			return 0
		}
		if marker >= 0xc0 && marker <= 0xcf && marker != 0xc4 && marker != 0xc8 && marker != 0xcc {
			if length < 8 {
				return 0
			}
			return int(data[offset+7])
		}
		offset += length
	}
	return 0
}

func injectJPEGICCSegments(encoded []byte, segments [][]byte, iccBytes int) []byte {
	if len(segments) == 0 || len(encoded) < 2 || encoded[0] != 0xff || encoded[1] != 0xd8 {
		return encoded
	}
	out := make([]byte, 0, len(encoded)+iccBytes)
	out = append(out, 0xff, 0xd8)
	for _, seg := range segments {
		out = append(out, seg...)
	}
	out = append(out, encoded[2:]...)
	return out
}
