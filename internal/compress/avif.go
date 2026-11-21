package compress

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"

	"zpic/internal/aviftools"
	"zpic/internal/contextio"
	"zpic/internal/heictools"
)

// SupportsAVIF reports whether static AVIF encoding and decoding are available.
func SupportsAVIF() bool {
	return aviftools.Available()
}

// SniffAVIFHeader reports whether head begins with an ISOBMFF ftyp header.
// InspectBytes identifies the actual image format.
func SniffAVIFHeader(head []byte) bool {
	return aviftools.SniffFTYP(head)
}

func decodeBMFFPNG(ctx context.Context, data []byte, threads int) ([]byte, string, error) {
	pngData, err := aviftools.DecodePNG(ctx, data, threads)
	if !errors.Is(err, aviftools.ErrNotAVIF) {
		return pngData, "avif", err
	}
	pngData, err = heictools.DecodePNG(ctx, data)
	return pngData, "heic", err
}

func validateAVIFOutput(ctx context.Context, data []byte, width, height int, mode aviftools.MetadataMode) error {
	info, err := aviftools.InspectBytes(ctx, data)
	if err != nil {
		return fmt.Errorf("avifenc 输出无效：%w", err)
	}
	if info.Width != width || info.Height != height {
		return fmt.Errorf("avifenc 输出尺寸异常（%dx%d != %dx%d）", info.Width, info.Height, width, height)
	}
	if mode != aviftools.MetadataAll && (info.HasEXIF || info.HasXMP || mode == aviftools.MetadataNone && info.HasICC) {
		return fmt.Errorf("avifenc 输出仍含有需要移除的元数据")
	}
	return nil
}

func avifMetadataMode(policy MetadataPolicy) aviftools.MetadataMode {
	switch policy {
	case MetadataKeepICC:
		return aviftools.MetadataICC
	case MetadataStrip:
		return aviftools.MetadataNone
	default:
		return aviftools.MetadataAll
	}
}

func prepareAVIFPNG(ctx context.Context, pixelSource []byte, sourceFormat string, orientation int, needsResize bool, options Options) ([]byte, []byte, error) {
	pngSource := sourceFormat == "png" || sourceFormat == "heic" || sourceFormat == "avif"
	if pngSource && orientation == 1 && !needsResize {
		return pixelSource, nil, nil
	}

	decoded, _, err := image.Decode(contextio.NewReader(ctx, bytes.NewReader(pixelSource)))
	if err != nil {
		return nil, nil, fmt.Errorf("图片数据损坏或无法解码：%w", err)
	}
	resized, _, err := resizeImage(ctx, normalizeOrientation(decoded, orientation), options.MaxDimension)
	if err != nil {
		return nil, nil, fmt.Errorf("缩放图片已取消：%w", err)
	}

	var chunks [][]byte
	var icc []byte
	if options.Metadata != MetadataStrip {
		switch sourceFormat {
		case "jpeg":
			components := 3
			if resized.ColorModel() == color.GrayModel {
				components = 1
			}
			icc = extractJPEGICCProfile(pixelSource, components)
		case "webp":
			icc = extractWebPICCProfile(pixelSource)
		default:
			chunks = extractPNGColorChunks(pixelSource)
		}
	}
	if options.Metadata == MetadataKeepAll {
		chunks = append(chunks, avifSourceMetadataChunks(pixelSource, sourceFormat)...)
	}
	pngData, err := encodeResizedPNG(ctx, aviftools.ConvertYCbCrToNRGBA(resized), chunks)
	return pngData, icc, err
}

func avifSourceMetadataChunks(data []byte, sourceFormat string) [][]byte {
	var exif, xmp []byte
	switch sourceFormat {
	case "jpeg":
		exif = jpegAPP1Payload(data, "Exif\x00\x00")
		xmp = jpegAPP1Payload(data, "http://ns.adobe.com/xap/1.0/\x00")
	case "webp":
		exif = bytes.TrimPrefix(webpChunkPayload(data, "EXIF"), []byte("Exif\x00\x00"))
		xmp = webpChunkPayload(data, "XMP ")
	default:
		return avifPNGMetadataChunks(data)
	}
	var chunks [][]byte
	if len(exif) > 0 {
		// avifenc normalizes the EXIF orientation after reading the upright PNG.
		chunks = append(chunks, makePNGMetadataChunk("eXIf", nil, exif))
	}
	if len(xmp) > 0 {
		// iTXt keyword, uncompressed text, and empty language and translated keywords.
		chunks = append(chunks, makePNGMetadataChunk("iTXt", []byte("XML:com.adobe.xmp\x00\x00\x00\x00\x00"), xmp))
	}
	return chunks
}

func avifPNGMetadataChunks(data []byte) [][]byte {
	var chunks [][]byte
	for offset := len(pngSignature); offset <= len(data)-12; {
		size := int(binary.BigEndian.Uint32(data[offset:]))
		if size < 0 || size > len(data)-offset-12 {
			return nil
		}
		end := offset + 12 + size
		switch string(data[offset+4 : offset+8]) {
		case "IEND":
			return chunks
		case "eXIf":
			chunks = append(chunks, data[offset:end])
		case "iTXt", "tEXt", "zTXt":
			payload := data[offset+8 : end-4]
			key, _, _ := bytes.Cut(payload, []byte{0})
			switch string(key) {
			case "XML:com.adobe.xmp", "Raw profile type exif", "Raw profile type xmp", "Raw profile type APP1", "Raw profile type app1":
				chunks = append(chunks, data[offset:end])
			}
		}
		offset = end
	}
	return chunks
}

func makePNGMetadataChunk(kind string, prefix, payload []byte) []byte {
	chunk := make([]byte, 0, 12+len(prefix)+len(payload))
	chunk = binary.BigEndian.AppendUint32(chunk, uint32(len(prefix)+len(payload)))
	chunk = append(chunk, kind...)
	chunk = append(chunk, prefix...)
	chunk = append(chunk, payload...)
	return binary.BigEndian.AppendUint32(chunk, crc32.ChecksumIEEE(chunk[4:]))
}
