package aviftools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"zpic/internal/contextio"
	"zpic/internal/imageinput"
)

const (
	maxAVIFBytes            = imageinput.MaxSize
	maxIntermediatePNGBytes = imageinput.MaxIntermediatePNGSize
	maxAVIFPixels           = 32_000_000
	maxFTYPBytes            = 64 << 10
	maxBoxCount             = 1 << 16
)

var (
	ErrNotAVIF             = errors.New("不是有效的 AVIF 图片")
	ErrAnimatedAVIF        = errors.New("不支持动画或序列 AVIF，仅支持静态图片")
	ErrUnsupportedPlatform = errors.New("当前平台暂不支持 AVIF 图片（仅支持 Apple 芯片的 macOS）")
	errInvalidAVIF         = errors.New("AVIF 图片容器损坏")
)

// HeaderInfo describes a static AVIF. Width and Height are the display
// dimensions after the primary item's clean aperture and rotation.
type HeaderInfo struct {
	Width, Height int
	Brand         string
	HasEXIF       bool
	HasXMP        bool
	HasICC        bool
}

// SniffFTYP reports whether head begins with an ISOBMFF ftyp box header.
func SniffFTYP(head []byte) bool {
	if len(head) < 12 || string(head[4:8]) != "ftyp" {
		return false
	}
	size := binary.BigEndian.Uint32(head[:4])
	return size == 0 || size == 1 || size >= 16
}

func InspectBytes(ctx context.Context, data []byte) (HeaderInfo, error) {
	return Inspect(ctx, bytes.NewReader(data), int64(len(data)))
}

// Inspect validates static AVIF headers without reading the image payloads.
func Inspect(ctx context.Context, r io.ReadSeeker, size int64) (HeaderInfo, error) {
	if err := ctx.Err(); err != nil {
		return HeaderInfo{}, err
	}
	if size < 16 || size > maxAVIFBytes {
		return HeaderInfo{}, fmt.Errorf("%w：文件大小无效或超过 64 MiB 限制", errInvalidAVIF)
	}
	reader := boxReader{seeker: r, input: contextio.NewReader(ctx, r)}
	var head [8]byte
	if err := reader.read(box{typ: "文件头", size: size}, head[:]); err != nil {
		return HeaderInfo{}, err
	}
	if string(head[4:]) != "ftyp" {
		return HeaderInfo{}, ErrNotAVIF
	}

	var info HeaderInfo
	var meta box
	hasMdat := false
	err := reader.walk(0, size, func(b box) error {
		if info.Brand == "" {
			if b.size < 8 || b.size > maxFTYPBytes || (b.size-8)%4 != 0 {
				return fmt.Errorf("%w：ftyp 块大小无效", errInvalidAVIF)
			}
			ftyp := make([]byte, int(b.size))
			if err := reader.read(b, ftyp); err != nil {
				return err
			}
			var err error
			info.Brand, err = classifyFTYP(ftyp)
			return err
		}
		switch b.typ {
		case "moov":
			return ErrAnimatedAVIF
		case "meta":
			if meta.typ != "" || b.size < 4 {
				return fmt.Errorf("%w：meta 块重复或过短", errInvalidAVIF)
			}
			meta = b
		case "mdat":
			hasMdat = hasMdat || b.size > 0
		}
		return nil
	})
	if err != nil {
		return HeaderInfo{}, err
	}
	if meta.typ == "" {
		return HeaderInfo{}, fmt.Errorf("%w：缺少 meta 块", errInvalidAVIF)
	}
	hasIdat, err := reader.inspectMeta(meta, &info)
	if err != nil {
		return HeaderInfo{}, err
	}
	if !hasMdat && !hasIdat {
		return HeaderInfo{}, fmt.Errorf("%w：缺少图像数据", errInvalidAVIF)
	}
	return info, ctx.Err()
}

func classifyFTYP(payload []byte) (string, error) {
	major := string(payload[:4])
	switch major {
	case "avis":
		return "", ErrAnimatedAVIF
	case "avif":
		return major, nil
	}
	var hasStill, hasAVIS, hasMSF1 bool
	for i := 8; i < len(payload); i += 4 {
		switch string(payload[i : i+4]) {
		case "avif":
			hasStill = true
		case "avis":
			hasAVIS = true
		case "msf1":
			hasMSF1 = true
		}
	}
	if (major == "msf1" && hasStill) || (!hasStill && hasAVIS) {
		return "", ErrAnimatedAVIF
	}
	if (major == "mif1" || major == "miaf") && hasStill && !hasAVIS && !hasMSF1 {
		return major, nil
	}
	return "", ErrNotAVIF
}

// HasStrippableMetadata reports whether applying mode would remove metadata.
func HasStrippableMetadata(ctx context.Context, data []byte, mode MetadataMode) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	switch mode {
	case MetadataAll:
		return false, nil
	case MetadataICC, MetadataNone:
		info, err := InspectBytes(ctx, data)
		if err != nil {
			return false, err
		}
		return info.HasEXIF || info.HasXMP || (mode == MetadataNone && info.HasICC), nil
	default:
		return false, fmt.Errorf("不支持的 AVIF 元数据策略：%q", mode)
	}
}

// A box describes the payload range, excluding the box header.
type box struct {
	typ          string
	offset, size int64
}

type boxReader struct {
	seeker io.ReadSeeker
	input  io.Reader
}

func (r boxReader) read(b box, dst []byte) error {
	if int64(len(dst)) > b.size {
		return fmt.Errorf("%w：%s 块过短", errInvalidAVIF, b.typ)
	}
	if _, err := r.seeker.Seek(b.offset, io.SeekStart); err != nil {
		return fmt.Errorf("无法定位 AVIF %s 块：%w", b.typ, err)
	}
	if _, err := io.ReadFull(r.input, dst); err != nil {
		return fmt.Errorf("无法读取 AVIF %s 块：%w", b.typ, err)
	}
	return nil
}

func (r boxReader) walk(offset, size int64, visit func(box) error) error {
	end := offset + size
	var head [8]byte
	for count := 0; offset < end; count++ {
		if count == maxBoxCount || end-offset < 8 {
			return fmt.Errorf("%w：数据块数量过多或头部不完整", errInvalidAVIF)
		}
		if err := r.read(box{typ: "头部", offset: offset, size: end - offset}, head[:]); err != nil {
			return err
		}
		typ := string(head[4:8])
		boxSize := uint64(binary.BigEndian.Uint32(head[:4]))
		headerSize := int64(8)
		switch boxSize {
		case 0:
			boxSize = uint64(end - offset)
		case 1:
			if err := r.read(box{typ: typ, offset: offset + 8, size: end - offset - 8}, head[:]); err != nil {
				return err
			}
			boxSize = binary.BigEndian.Uint64(head[:])
			headerSize = 16
		}
		if boxSize < uint64(headerSize) || boxSize > uint64(end-offset) {
			return fmt.Errorf("%w：%s 块长度越界", errInvalidAVIF, typ)
		}
		if err := visit(box{typ: typ, offset: offset + headerSize, size: int64(boxSize) - headerSize}); err != nil {
			return err
		}
		offset += int64(boxSize)
	}
	return nil
}

func (r boxReader) inspectMeta(meta box, info *HeaderInfo) (bool, error) {
	var hasPict, hasPrimary, hasIdat bool
	var primaryID uint32
	var iinf, iprp box
	var head [12]byte
	err := r.walk(meta.offset+4, meta.size-4, func(b box) error {
		switch b.typ {
		case "hdlr":
			if err := r.read(b, head[:]); err != nil {
				return err
			}
			hasPict = string(head[8:12]) == "pict"
		case "pitm":
			if err := r.read(b, head[:min(8, b.size)]); err != nil {
				return err
			}
			switch {
			case b.size >= 6 && head[0] == 0:
				primaryID = uint32(binary.BigEndian.Uint16(head[4:6]))
			case b.size >= 8 && head[0] == 1:
				primaryID = binary.BigEndian.Uint32(head[4:8])
			default:
				return fmt.Errorf("%w：pitm 块无效", errInvalidAVIF)
			}
			hasPrimary = true
		case "iinf":
			if iinf.typ != "" {
				return fmt.Errorf("%w：iinf 块重复", errInvalidAVIF)
			}
			iinf = b
		case "iprp":
			if iprp.typ != "" {
				return fmt.Errorf("%w：iprp 块重复", errInvalidAVIF)
			}
			iprp = b
		case "idat":
			hasIdat = hasIdat || b.size > 0
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	if !hasPict || !hasPrimary || iprp.typ == "" {
		return false, fmt.Errorf("%w：缺少静态主图像或尺寸属性", errInvalidAVIF)
	}
	if iinf.typ != "" {
		if err := r.inspectItems(iinf, primaryID, info); err != nil {
			return false, err
		}
	}
	return hasIdat, r.inspectProperties(iprp, primaryID, info)
}

func (r boxReader) inspectItems(iinf box, primaryID uint32, info *HeaderInfo) error {
	var head [14]byte
	if err := r.read(iinf, head[:min(8, iinf.size)]); err != nil {
		return err
	}
	var entries uint32
	var prefix int64
	switch {
	case iinf.size >= 6 && head[0] == 0:
		prefix = 6
		entries = uint32(binary.BigEndian.Uint16(head[4:6]))
	case iinf.size >= 8 && head[0] == 1:
		prefix = 8
		entries = binary.BigEndian.Uint32(head[4:8])
	default:
		return fmt.Errorf("%w：iinf 块无效", errInvalidAVIF)
	}
	var count uint32
	err := r.walk(iinf.offset+prefix, iinf.size-prefix, func(b box) error {
		if b.typ != "infe" {
			return nil
		}
		count++
		if err := r.read(b, head[:min(14, b.size)]); err != nil {
			return err
		}
		if b.size < 4 {
			return fmt.Errorf("%w：infe 块过短", errInvalidAVIF)
		}
		var id uint32
		var typ string
		switch head[0] {
		case 2:
			if b.size < 12 {
				return fmt.Errorf("%w：infe v2 块过短", errInvalidAVIF)
			}
			id = uint32(binary.BigEndian.Uint16(head[4:6]))
			typ = string(head[8:12])
		case 3:
			if b.size < 14 {
				return fmt.Errorf("%w：infe v3 块过短", errInvalidAVIF)
			}
			id = binary.BigEndian.Uint32(head[4:8])
			typ = string(head[10:14])
		default:
			return nil // Older entries have no item_type.
		}
		if id == primaryID && typ != "av01" && typ != "grid" {
			return ErrNotAVIF
		}
		info.HasEXIF = info.HasEXIF || typ == "Exif"
		// Include any MIME metadata item when deciding whether to strip metadata.
		info.HasXMP = info.HasXMP || typ == "mime"
		return nil
	})
	if err == nil && count != entries {
		return fmt.Errorf("%w：iinf 项数量无效", errInvalidAVIF)
	}
	return err
}

func (r boxReader) inspectProperties(iprp box, primaryID uint32, info *HeaderInfo) error {
	var props []box
	var indexes []uint16
	err := r.walk(iprp.offset, iprp.size, func(b box) error {
		switch b.typ {
		case "ipco":
			return r.walk(b.offset, b.size, func(p box) error {
				props = append(props, p)
				if p.typ == "colr" {
					var colourType [4]byte
					if err := r.read(p, colourType[:]); err != nil {
						return err
					}
					info.HasICC = info.HasICC || string(colourType[:]) == "prof" || string(colourType[:]) == "rICC"
				}
				return nil
			})
		case "ipma":
			var err error
			indexes, err = r.primaryAssociations(b, primaryID, indexes)
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}

	var width, height uint32
	var aperture box
	var rotation byte
	var head [32]byte
	for _, index := range indexes {
		if index == 0 {
			continue
		}
		if int(index) > len(props) {
			return fmt.Errorf("%w：主图像的属性索引越界", errInvalidAVIF)
		}
		prop := props[index-1]
		switch prop.typ {
		case "ispe":
			if err := r.read(prop, head[:12]); err != nil {
				return err
			}
			width = binary.BigEndian.Uint32(head[4:8])
			height = binary.BigEndian.Uint32(head[8:12])
		case "clap":
			aperture = prop
		case "irot":
			if err := r.read(prop, head[:1]); err != nil {
				return err
			}
			rotation = head[0] & 3
		}
	}
	if width == 0 || height == 0 || uint64(width)*uint64(height) > maxAVIFPixels {
		return errors.New("AVIF 图片超过 3200 万像素限制或尺寸无效")
	}
	if aperture.typ != "" {
		if err := r.read(aperture, head[:]); err != nil {
			return err
		}
		var validW, validH bool
		width, validW = apertureDimension(width, head[:8], head[16:24])
		height, validH = apertureDimension(height, head[8:16], head[24:32])
		if !validW || !validH {
			return fmt.Errorf("%w：clap 裁切范围无效", errInvalidAVIF)
		}
	}
	if rotation&1 != 0 {
		width, height = height, width
	}
	info.Width, info.Height = int(width), int(height)
	return nil
}

func apertureDimension(full uint32, length, centerOffset []byte) (uint32, bool) {
	lengthN := int64(int32(binary.BigEndian.Uint32(length[:4])))
	lengthD := int64(int32(binary.BigEndian.Uint32(length[4:])))
	offsetN := int64(int32(binary.BigEndian.Uint32(centerOffset[:4])))
	offsetD := int64(int32(binary.BigEndian.Uint32(centerOffset[4:])))
	if lengthN <= 0 || lengthD <= 0 || lengthN%lengthD != 0 || offsetD <= 0 {
		return 0, false
	}
	crop := lengthN / lengthD
	margin := int64(full) - crop
	// The start of the aperture is (full-crop)/2 + offsetN/offsetD.
	startN := margin*offsetD + 2*offsetN
	startD := 2 * offsetD
	return uint32(crop), startN >= 0 && startN%startD == 0 && startN/startD <= margin
}

func (r boxReader) primaryAssociations(b box, primaryID uint32, indexes []uint16) ([]uint16, error) {
	var head [8]byte
	if err := r.read(b, head[:]); err != nil {
		return nil, err
	}
	idBytes, propBytes := 2, 1
	if head[0] > 0 {
		idBytes = 4
	}
	if head[3]&1 != 0 {
		propBytes = 2
	}
	entries := binary.BigEndian.Uint32(head[4:8])
	input := bufio.NewReader(io.LimitReader(r.input, b.size-8))
	var assoc [255 * 2]byte
	for range entries {
		if _, err := io.ReadFull(input, head[:idBytes+1]); err != nil {
			return nil, fmt.Errorf("无法读取 AVIF ipma 项：%w", err)
		}
		id := uint32(binary.BigEndian.Uint16(head[:2]))
		if idBytes == 4 {
			id = binary.BigEndian.Uint32(head[:4])
		}
		count := int(head[idBytes])
		if _, err := io.ReadFull(input, assoc[:count*propBytes]); err != nil {
			return nil, fmt.Errorf("无法读取 AVIF ipma 关联索引：%w", err)
		}
		if id != primaryID {
			continue
		}
		for i := 0; i < count*propBytes; i += propBytes {
			index := uint16(assoc[i] & 0x7f)
			if propBytes == 2 {
				index = binary.BigEndian.Uint16(assoc[i:i+2]) & 0x7fff
			}
			indexes = append(indexes, index)
		}
	}
	return indexes, nil
}
