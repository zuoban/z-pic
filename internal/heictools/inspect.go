// Package heictools inspects ISO Base Media File Format (ISOBMFF) HEIC/HEIF
// still-image containers in pure Go and decodes them on macOS via system ImageIO (sips).
package heictools

import (
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
	maxHEICBytes      = imageinput.MaxSize
	maxHEICPixels     = 32_000_000
	maxFTYPBoxBytes   = 64 << 10
	maxMetaBoxBytes   = 4 << 20
	maxTopLevelBoxes  = 4096
	maxNestedBoxCount = 4096
)

var (
	ErrNotHEIC             = errors.New("不是有效的 HEIC 图片")
	ErrAVIFUnsupported     = errors.New("不支持 AVIF 图片，仅支持静态 JPEG/PNG/WebP/HEIC")
	ErrAnimatedHEIC        = errors.New("不支持动画或序列 HEIC，仅支持静态图片")
	ErrUnsupportedPlatform = errors.New("当前平台暂不支持解码 HEIC 图片（仅支持 macOS）")
)

// HeaderInfo describes validated static HEIC container metadata.
// Width and Height reflect visual display dimensions after clean-aperture (clap)
// and rotation (irot) transformative properties on the primary item.
type HeaderInfo struct {
	Width  int
	Height int
	Brand  string
}

// SniffFTYP reports whether head begins with an ISOBMFF ftyp box header.
func SniffFTYP(head []byte) bool {
	if len(head) < 12 || string(head[4:8]) != "ftyp" {
		return false
	}
	size := binary.BigEndian.Uint32(head[0:4])
	return size == 0 || size == 1 || size >= 16
}

// InspectBytes validates a static HEIC container in memory.
func InspectBytes(ctx context.Context, data []byte) (HeaderInfo, error) {
	return Inspect(ctx, bytes.NewReader(data), int64(len(data)))
}

// Inspect validates static HEIC container headers on r without reading mdat
// image payloads into memory.
func Inspect(ctx context.Context, r io.ReadSeeker, size int64) (HeaderInfo, error) {
	if err := ctx.Err(); err != nil {
		return HeaderInfo{}, err
	}
	if size < 16 || size > maxHEICBytes {
		return HeaderInfo{}, fmt.Errorf("%w：文件为空、损坏或超过 64 MiB 限制", ErrNotHEIC)
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return HeaderInfo{}, fmt.Errorf("无法读取 HEIC 头部：%w", err)
	}

	var (
		brand     string
		foundFTYP bool
		foundMeta bool
		foundMdat bool
		metaBytes []byte
		boxCount  int
		offset    int64
	)

	cr := contextio.NewReader(ctx, r)
	for offset < size {
		if err := ctx.Err(); err != nil {
			return HeaderInfo{}, err
		}
		boxCount++
		if boxCount > maxTopLevelBoxes {
			return HeaderInfo{}, fmt.Errorf("%w：容器结构异常", ErrNotHEIC)
		}
		if size-offset < 8 {
			return HeaderInfo{}, fmt.Errorf("%w：头部不完整", ErrNotHEIC)
		}
		if _, err := r.Seek(offset, io.SeekStart); err != nil {
			return HeaderInfo{}, fmt.Errorf("无法读取 HEIC 头部：%w", err)
		}
		var hdr [8]byte
		if _, err := io.ReadFull(cr, hdr[:]); err != nil {
			return HeaderInfo{}, fmt.Errorf("无法读取 HEIC 头部：%w", err)
		}
		boxSize := int64(binary.BigEndian.Uint32(hdr[0:4]))
		boxType := string(hdr[4:8])
		headerLen := int64(8)
		switch boxSize {
		case 0:
			boxSize = size - offset
		case 1:
			if size-offset < 16 {
				return HeaderInfo{}, fmt.Errorf("%w：扩展长度头部不完整", ErrNotHEIC)
			}
			var large [8]byte
			if _, err := io.ReadFull(cr, large[:]); err != nil {
				return HeaderInfo{}, fmt.Errorf("无法读取 HEIC 头部：%w", err)
			}
			uSize := binary.BigEndian.Uint64(large[:])
			if uSize < 16 || uSize > uint64(size-offset) {
				return HeaderInfo{}, fmt.Errorf("%w：数据块长度无效", ErrNotHEIC)
			}
			boxSize = int64(uSize)
			headerLen = 16
		default:
			if boxSize < 8 || boxSize > size-offset {
				return HeaderInfo{}, fmt.Errorf("%w：数据块长度无效", ErrNotHEIC)
			}
		}
		payloadSize := boxSize - headerLen

		if !foundFTYP {
			if boxType != "ftyp" {
				return HeaderInfo{}, ErrNotHEIC
			}
			if payloadSize < 8 || payloadSize > maxFTYPBoxBytes {
				return HeaderInfo{}, fmt.Errorf("%w：ftyp 块无效", ErrNotHEIC)
			}
			ftyp := make([]byte, int(payloadSize))
			if _, err := io.ReadFull(cr, ftyp); err != nil {
				return HeaderInfo{}, fmt.Errorf("无法读取 HEIC ftyp：%w", err)
			}
			parsedBrand, err := classifyFTYP(ftyp)
			if err != nil {
				return HeaderInfo{}, err
			}
			brand = parsedBrand
			foundFTYP = true
		} else {
			switch boxType {
			case "moov":
				return HeaderInfo{}, ErrAnimatedHEIC
			case "meta":
				if foundMeta {
					return HeaderInfo{}, fmt.Errorf("%w：存在重复的 meta 块", ErrNotHEIC)
				}
				if payloadSize < 4 || payloadSize > maxMetaBoxBytes {
					return HeaderInfo{}, fmt.Errorf("%w：meta 块大小无效", ErrNotHEIC)
				}
				metaBytes = make([]byte, int(payloadSize))
				if _, err := io.ReadFull(cr, metaBytes); err != nil {
					return HeaderInfo{}, fmt.Errorf("无法读取 HEIC meta：%w", err)
				}
				foundMeta = true
			case "mdat":
				if payloadSize > 0 {
					foundMdat = true
				}
			}
		}
		offset += boxSize
	}

	if !foundFTYP || !foundMeta {
		return HeaderInfo{}, fmt.Errorf("%w：缺少必要的容器元数据", ErrNotHEIC)
	}
	w, h, hasIdat, err := parseMetaBox(metaBytes)
	if err != nil {
		return HeaderInfo{}, err
	}
	if !foundMdat && !hasIdat {
		return HeaderInfo{}, fmt.Errorf("%w：缺少图像数据", ErrNotHEIC)
	}
	if w <= 0 || h <= 0 || uint64(w) > uint64(maxHEICPixels)/uint64(h) {
		return HeaderInfo{}, errors.New("HEIC 图片超过 3200 万像素限制或尺寸无效")
	}
	return HeaderInfo{
		Width:  w,
		Height: h,
		Brand:  brand,
	}, nil
}

func classifyFTYP(payload []byte) (string, error) {
	if len(payload) < 8 || (len(payload)-8)%4 != 0 {
		return "", fmt.Errorf("%w：ftyp 长度无效", ErrNotHEIC)
	}
	major := string(payload[0:4])
	hasStillHEIC := isStillHEICBrand(major)
	hasMIF1 := major == "mif1" || major == "miaf"
	hasAVIF := major == "avif" || major == "avis"
	hasSequence := major == "msf1" || major == "hevc" || major == "hevx"
	hasHEVCCompat := major == "hevc" || major == "hevx"

	for i := 8; i+4 <= len(payload); i += 4 {
		b := string(payload[i : i+4])
		if isStillHEICBrand(b) {
			hasStillHEIC = true
		}
		if b == "mif1" || b == "miaf" {
			hasMIF1 = true
		}
		if b == "avif" || b == "avis" {
			hasAVIF = true
		}
		if b == "msf1" {
			hasSequence = true
		}
		if b == "hevc" || b == "hevx" {
			hasHEVCCompat = true
		}
	}

	if major == "avif" || major == "avis" {
		return "", ErrAVIFUnsupported
	}
	if major == "msf1" {
		return "", ErrAnimatedHEIC
	}
	if hasStillHEIC {
		return major, nil
	}
	if hasMIF1 && hasHEVCCompat && !hasAVIF {
		return major, nil
	}
	if hasAVIF {
		return "", ErrAVIFUnsupported
	}
	if hasSequence {
		return "", ErrAnimatedHEIC
	}
	return "", ErrNotHEIC
}

func isStillHEICBrand(brand string) bool {
	switch brand {
	case "heic", "heix", "heim", "heis":
		return true
	default:
		return false
	}
}

type isobmffBox struct {
	typ     string
	payload []byte
}

func iterateBoxes(data []byte, visit func(isobmffBox) error) error {
	pos := 0
	count := 0
	for pos < len(data) {
		count++
		if count > maxNestedBoxCount {
			return fmt.Errorf("%w：嵌套块数量超出限制", ErrNotHEIC)
		}
		if len(data)-pos < 8 {
			return fmt.Errorf("%w：子块头部不完整", ErrNotHEIC)
		}
		boxSize := uint64(binary.BigEndian.Uint32(data[pos : pos+4]))
		boxType := string(data[pos+4 : pos+8])
		hdrLen := uint64(8)
		switch boxSize {
		case 0:
			boxSize = uint64(len(data) - pos)
		case 1:
			if len(data)-pos < 16 {
				return fmt.Errorf("%w：子块扩展长度无效", ErrNotHEIC)
			}
			boxSize = binary.BigEndian.Uint64(data[pos+8 : pos+16])
			hdrLen = 16
		}
		if boxSize < hdrLen || boxSize > uint64(len(data)-pos) {
			return fmt.Errorf("%w：子块长度越界", ErrNotHEIC)
		}
		payload := data[pos+int(hdrLen) : pos+int(boxSize)]
		if err := visit(isobmffBox{typ: boxType, payload: payload}); err != nil {
			return err
		}
		pos += int(boxSize)
	}
	return nil
}

type heicPropKind uint8

const (
	propUnknown heicPropKind = iota
	propISPE
	propIROT
	propCLAP
)

type heicProp struct {
	kind         heicPropKind
	w, h         int
	rotAngle     uint8
	clapW, clapH int
}

func parseMetaBox(meta []byte) (int, int, bool, error) {
	if len(meta) < 4 {
		return 0, 0, false, fmt.Errorf("%w：meta 块过短", ErrNotHEIC)
	}
	// FullBox header: version(1) + flags(3).
	body := meta[4:]

	var (
		hasPictHandler bool
		hasPrimary     bool
		primaryID      uint32
		hasIdat        bool
		itemTypes      map[uint32]string
		props          []heicProp // 1-based index => props[idx-1]
		ipmaEntries    map[uint32][]uint16
	)

	err := iterateBoxes(body, func(b isobmffBox) error {
		switch b.typ {
		case "hdlr":
			// FullBox(4) + pre_defined(4) + handler_type(4)
			if len(b.payload) >= 12 {
				if string(b.payload[8:12]) == "pict" {
					hasPictHandler = true
				}
			}
		case "pitm":
			if len(b.payload) < 6 {
				return fmt.Errorf("%w：pitm 块过短", ErrNotHEIC)
			}
			version := b.payload[0]
			if version == 0 {
				primaryID = uint32(binary.BigEndian.Uint16(b.payload[4:6]))
			} else {
				if len(b.payload) < 8 {
					return fmt.Errorf("%w：pitm v1 块过短", ErrNotHEIC)
				}
				primaryID = binary.BigEndian.Uint32(b.payload[4:8])
			}
			hasPrimary = true
		case "iinf":
			parsed, err := parseIINF(b.payload)
			if err != nil {
				return err
			}
			itemTypes = parsed
		case "iprp":
			p, m, err := parseIPRP(b.payload)
			if err != nil {
				return err
			}
			props = p
			ipmaEntries = m
		case "idat":
			if len(b.payload) > 0 {
				hasIdat = true
			}
		}
		return nil
	})
	if err != nil {
		return 0, 0, false, err
	}
	if !hasPictHandler {
		return 0, 0, false, fmt.Errorf("%w：不是静态图片处理器", ErrNotHEIC)
	}
	if !hasPrimary {
		return 0, 0, false, fmt.Errorf("%w：缺少主图像标识", ErrNotHEIC)
	}
	if itemType, ok := itemTypes[primaryID]; ok {
		if itemType == "av01" {
			return 0, 0, false, ErrAVIFUnsupported
		}
	}

	assoc := ipmaEntries[primaryID]
	if len(assoc) == 0 {
		return 0, 0, false, fmt.Errorf("%w：缺少主图像尺寸属性", ErrNotHEIC)
	}

	var width, height int
	foundISPE := false
	var clapW, clapH int
	var rotTotal uint8
	for _, propIdx := range assoc {
		if propIdx == 0 || int(propIdx) > len(props) {
			continue
		}
		prop := props[propIdx-1]
		switch prop.kind {
		case propISPE:
			width, height = prop.w, prop.h
			foundISPE = true
		case propCLAP:
			if prop.clapW > 0 && prop.clapH > 0 {
				clapW, clapH = prop.clapW, prop.clapH
			}
		case propIROT:
			rotTotal = (rotTotal + prop.rotAngle) & 0x03
		}
	}
	if !foundISPE || width <= 0 || height <= 0 {
		return 0, 0, false, fmt.Errorf("%w：缺少有效尺寸信息", ErrNotHEIC)
	}
	if clapW > 0 && clapH > 0 {
		if clapW <= width {
			width = clapW
		}
		if clapH <= height {
			height = clapH
		}
	}
	if rotTotal == 1 || rotTotal == 3 {
		width, height = height, width
	}
	return width, height, hasIdat, nil
}

func parseIINF(payload []byte) (map[uint32]string, error) {
	if len(payload) < 6 {
		return nil, nil
	}
	version := payload[0]
	off := 6
	if version != 0 {
		if len(payload) < 8 {
			return nil, nil
		}
		off = 8
	}
	types := make(map[uint32]string)
	_ = iterateBoxes(payload[off:], func(b isobmffBox) error {
		if b.typ != "infe" || len(b.payload) < 4 {
			return nil
		}
		v := b.payload[0]
		switch v {
		case 2:
			if len(b.payload) >= 12 {
				id := uint32(binary.BigEndian.Uint16(b.payload[4:6]))
				types[id] = string(b.payload[8:12])
			}
		case 3:
			if len(b.payload) >= 14 {
				id := binary.BigEndian.Uint32(b.payload[4:8])
				types[id] = string(b.payload[10:14])
			}
		}
		return nil
	})
	return types, nil
}

func parseIPRP(payload []byte) ([]heicProp, map[uint32][]uint16, error) {
	var props []heicProp
	assoc := make(map[uint32][]uint16)
	err := iterateBoxes(payload, func(b isobmffBox) error {
		switch b.typ {
		case "ipco":
			return iterateBoxes(b.payload, func(p isobmffBox) error {
				props = append(props, parseIPCOProp(p))
				return nil
			})
		case "ipma":
			return parseIPMA(b.payload, assoc)
		}
		return nil
	})
	return props, assoc, err
}

func parseIPCOProp(b isobmffBox) heicProp {
	switch b.typ {
	case "ispe":
		// FullBox(4) + width(4) + height(4)
		if len(b.payload) >= 12 {
			w := binary.BigEndian.Uint32(b.payload[4:8])
			h := binary.BigEndian.Uint32(b.payload[8:12])
			if w > 0 && h > 0 && w <= 0x7fffffff && h <= 0x7fffffff {
				return heicProp{kind: propISPE, w: int(w), h: int(h)}
			}
		}
	case "irot":
		if len(b.payload) >= 1 {
			return heicProp{kind: propIROT, rotAngle: b.payload[0] & 0x03}
		}
	case "clap":
		// cleanApertureWidthN/D, cleanApertureHeightN/D (uint32 each)
		if len(b.payload) >= 16 {
			wn := binary.BigEndian.Uint32(b.payload[0:4])
			wd := binary.BigEndian.Uint32(b.payload[4:8])
			hn := binary.BigEndian.Uint32(b.payload[8:12])
			hd := binary.BigEndian.Uint32(b.payload[12:16])
			if wn > 0 && wd > 0 && hn > 0 && hd > 0 {
				cw := wn / wd
				ch := hn / hd
				if cw > 0 && ch > 0 && cw <= 0x7fffffff && ch <= 0x7fffffff {
					return heicProp{kind: propCLAP, clapW: int(cw), clapH: int(ch)}
				}
			}
		}
	}
	return heicProp{kind: propUnknown}
}

func parseIPMA(payload []byte, assoc map[uint32][]uint16) error {
	if len(payload) < 8 {
		return fmt.Errorf("%w：ipma 块过短", ErrNotHEIC)
	}
	version := payload[0]
	flags := uint32(payload[1])<<16 | uint32(payload[2])<<8 | uint32(payload[3])
	largePropIdx := flags&1 != 0
	entryCount := binary.BigEndian.Uint32(payload[4:8])
	pos := 8
	for range entryCount {
		var itemID uint32
		if version < 1 {
			if pos+2 > len(payload) {
				return fmt.Errorf("%w：ipma 项截断", ErrNotHEIC)
			}
			itemID = uint32(binary.BigEndian.Uint16(payload[pos : pos+2]))
			pos += 2
		} else {
			if pos+4 > len(payload) {
				return fmt.Errorf("%w：ipma 项截断", ErrNotHEIC)
			}
			itemID = binary.BigEndian.Uint32(payload[pos : pos+4])
			pos += 4
		}
		if pos+1 > len(payload) {
			return fmt.Errorf("%w：ipma 关联计数截断", ErrNotHEIC)
		}
		assocCount := int(payload[pos])
		pos++
		propBytes := 1
		if largePropIdx {
			propBytes = 2
		}
		if pos+assocCount*propBytes > len(payload) {
			return fmt.Errorf("%w：ipma 关联索引截断", ErrNotHEIC)
		}
		list := assoc[itemID]
		for range assocCount {
			var idx uint16
			if largePropIdx {
				idx = binary.BigEndian.Uint16(payload[pos:pos+2]) & 0x7fff
				pos += 2
			} else {
				idx = uint16(payload[pos] & 0x7f)
				pos++
			}
			list = append(list, idx)
		}
		assoc[itemID] = list
	}
	return nil
}
