package heictools

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"testing"
)

func makeBox(typ string, payload []byte) []byte {
	out := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(out[0:4], uint32(len(out)))
	copy(out[4:8], typ)
	copy(out[8:], payload)
	return out
}

func makeFullBox(typ string, version byte, flags uint32, payload []byte) []byte {
	full := make([]byte, 4+len(payload))
	full[0] = version
	full[1] = byte(flags >> 16)
	full[2] = byte(flags >> 8)
	full[3] = byte(flags)
	copy(full[4:], payload)
	return makeBox(typ, full)
}

func makeFTYP(major string, compat ...string) []byte {
	p := make([]byte, 8+4*len(compat))
	copy(p[0:4], major)
	for i, c := range compat {
		copy(p[8+4*i:12+4*i], c)
	}
	return makeBox("ftyp", p)
}

func makeSyntheticHEIC(major string, compat []string, w, h uint32, irot byte, addMoov bool, primaryItemType string) []byte {
	var buf bytes.Buffer
	buf.Write(makeFTYP(major, compat...))

	// hdlr
	hdlr := make([]byte, 12)
	copy(hdlr[4:8], "pict")
	hdlrBox := makeFullBox("hdlr", 0, 0, hdlr)

	// pitm (item ID 3)
	pitm := make([]byte, 2)
	binary.BigEndian.PutUint16(pitm, 3)
	pitmBox := makeFullBox("pitm", 0, 0, pitm)

	// iinf with infe v2 for item 3
	var iinfBox []byte
	if primaryItemType != "" {
		infe := make([]byte, 8)
		binary.BigEndian.PutUint16(infe[0:2], 3)
		copy(infe[4:8], primaryItemType)
		iinfPayload := append([]byte{0, 1}, makeFullBox("infe", 2, 0, infe)...)
		iinfBox = makeFullBox("iinf", 0, 0, iinfPayload)
	}

	// ipco: 1=tile ispe (128x64), 2=primary ispe (w x h), 3=irot
	tileISPE := make([]byte, 8)
	binary.BigEndian.PutUint32(tileISPE[0:4], 128)
	binary.BigEndian.PutUint32(tileISPE[4:8], 64)
	mainISPE := make([]byte, 8)
	binary.BigEndian.PutUint32(mainISPE[0:4], w)
	binary.BigEndian.PutUint32(mainISPE[4:8], h)

	var ipco bytes.Buffer
	ipco.Write(makeFullBox("ispe", 0, 0, tileISPE))
	ipco.Write(makeFullBox("ispe", 0, 0, mainISPE))
	ipco.Write(makeBox("irot", []byte{irot}))

	// ipma: item 1 -> prop 1; item 3 -> prop 2, prop 3
	var ipma bytes.Buffer
	_ = binary.Write(&ipma, binary.BigEndian, uint32(2)) // 2 entries
	_ = binary.Write(&ipma, binary.BigEndian, uint16(1))
	ipma.WriteByte(1)
	ipma.WriteByte(1)
	_ = binary.Write(&ipma, binary.BigEndian, uint16(3))
	ipma.WriteByte(2)
	ipma.WriteByte(2 | 0x80)
	ipma.WriteByte(3)

	iprp := makeBox("iprp", append(makeBox("ipco", ipco.Bytes()), makeFullBox("ipma", 0, 0, ipma.Bytes())...))

	var metaBody bytes.Buffer
	metaBody.Write(hdlrBox)
	metaBody.Write(pitmBox)
	metaBody.Write(iinfBox)
	metaBody.Write(iprp)
	buf.Write(makeFullBox("meta", 0, 0, metaBody.Bytes()))
	if addMoov {
		buf.Write(makeBox("moov", []byte{0, 0, 0, 0}))
	}
	buf.Write(makeBox("mdat", []byte{1, 2, 3, 4}))
	return buf.Bytes()
}

func TestInspectSyntheticHEIC(t *testing.T) {
	data := makeSyntheticHEIC("heic", []string{"mif1", "heic"}, 1200, 800, 0, false, "hvc1")
	if !SniffFTYP(data) {
		t.Fatal("SniffFTYP returned false for valid HEIC")
	}
	info, err := InspectBytes(context.Background(), data)
	if err != nil {
		t.Fatalf("InspectBytes failed: %v", err)
	}
	if info.Width != 1200 || info.Height != 800 || info.Brand != "heic" {
		t.Fatalf("unexpected HeaderInfo: %+v", info)
	}
}

func TestInspectRotationSwapsDimensions(t *testing.T) {
	for _, angle := range []byte{1, 3} {
		data := makeSyntheticHEIC("heic", []string{"mif1", "heic"}, 1200, 800, angle, false, "hvc1")
		info, err := InspectBytes(context.Background(), data)
		if err != nil {
			t.Fatalf("angle=%d InspectBytes failed: %v", angle, err)
		}
		if info.Width != 800 || info.Height != 1200 {
			t.Fatalf("angle=%d got %dx%d, want 800x1200", angle, info.Width, info.Height)
		}
	}
	for _, angle := range []byte{0, 2} {
		data := makeSyntheticHEIC("heic", []string{"mif1", "heic"}, 1200, 800, angle, false, "hvc1")
		info, err := InspectBytes(context.Background(), data)
		if err != nil {
			t.Fatalf("angle=%d InspectBytes failed: %v", angle, err)
		}
		if info.Width != 1200 || info.Height != 800 {
			t.Fatalf("angle=%d got %dx%d, want 1200x800", angle, info.Width, info.Height)
		}
	}
}

func TestInspectRejectsAVIFAndSequencesAndOversized(t *testing.T) {
	avif := makeSyntheticHEIC("avif", []string{"mif1", "avif"}, 100, 100, 0, false, "av01")
	if _, err := InspectBytes(context.Background(), avif); !errors.Is(err, ErrAVIFUnsupported) {
		t.Fatalf("expected ErrAVIFUnsupported for avif major brand, got %v", err)
	}

	mifAVIF := makeSyntheticHEIC("mif1", []string{"mif1", "avif"}, 100, 100, 0, false, "av01")
	if _, err := InspectBytes(context.Background(), mifAVIF); !errors.Is(err, ErrAVIFUnsupported) {
		t.Fatalf("expected ErrAVIFUnsupported for mif1+avif, got %v", err)
	}

	disguisedAVIF := makeSyntheticHEIC("heic", []string{"mif1", "heic"}, 100, 100, 0, false, "av01")
	if _, err := InspectBytes(context.Background(), disguisedAVIF); !errors.Is(err, ErrAVIFUnsupported) {
		t.Fatalf("expected ErrAVIFUnsupported for av01 primary item, got %v", err)
	}

	seqBrand := makeSyntheticHEIC("msf1", []string{"msf1", "hevc"}, 100, 100, 0, false, "hvc1")
	if _, err := InspectBytes(context.Background(), seqBrand); !errors.Is(err, ErrAnimatedHEIC) {
		t.Fatalf("expected ErrAnimatedHEIC for msf1 brand, got %v", err)
	}

	withMoov := makeSyntheticHEIC("heic", []string{"mif1", "heic"}, 100, 100, 0, true, "hvc1")
	if _, err := InspectBytes(context.Background(), withMoov); !errors.Is(err, ErrAnimatedHEIC) {
		t.Fatalf("expected ErrAnimatedHEIC for moov box, got %v", err)
	}

	tooManyPixels := makeSyntheticHEIC("heic", []string{"mif1", "heic"}, 10000, 4000, 0, false, "hvc1")
	if _, err := InspectBytes(context.Background(), tooManyPixels); err == nil {
		t.Fatal("expected error for >32MP HEIC")
	}

	mp4 := makeSyntheticHEIC("isom", []string{"isom", "mp41"}, 100, 100, 0, false, "")
	if _, err := InspectBytes(context.Background(), mp4); !errors.Is(err, ErrNotHEIC) {
		t.Fatalf("expected ErrNotHEIC for mp4, got %v", err)
	}
}
