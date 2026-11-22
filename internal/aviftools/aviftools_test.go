package aviftools

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func bmffBox(typ string, parts ...[]byte) []byte {
	payload := bytes.Join(parts, nil)
	out := binary.BigEndian.AppendUint32(nil, uint32(8+len(payload)))
	out = append(out, typ...)
	return append(out, payload...)
}

func ftypBox(major, compatible string) []byte {
	return bmffBox("ftyp", []byte(major), make([]byte, 4), []byte(compatible))
}

func ispeBox(width, height uint32) []byte {
	payload := binary.BigEndian.AppendUint32(make([]byte, 4), width)
	return bmffBox("ispe", binary.BigEndian.AppendUint32(payload, height))
}

func itemInfoBox(id uint32, typ, nameAndContent string) []byte {
	payload := binary.BigEndian.AppendUint32([]byte{3, 0, 0, 0}, id)
	return bmffBox("infe", payload, []byte{0, 0}, []byte(typ), []byte(nameAndContent))
}

// The primary item is deliberately not the first item and uses wide ipma indices.
func stillAVIFContainer(ftyp []byte, width, height uint32, items [][]byte, extraProps ...[]byte) []byte {
	const primary = 70_000
	hdlr := bmffBox("hdlr", make([]byte, 8), []byte("pict"))
	pitm := bmffBox("pitm", binary.BigEndian.AppendUint32([]byte{1, 0, 0, 0}, primary))
	iinfHeader := binary.BigEndian.AppendUint16(make([]byte, 4), uint16(len(items)+1))
	iinf := bmffBox("iinf", iinfHeader, itemInfoBox(primary, "av01", "primary\x00"), bytes.Join(items, nil))
	props := append([][]byte{ispeBox(2, 3), ispeBox(width, height)}, extraProps...)
	ipco := bmffBox("ipco", props...)
	assoc := []byte{1, 0, 0, 1, 0, 0, 0, 2}       // FullBox version and flags, entry count.
	assoc = append(assoc, 0, 0, 0, 1, 1, 0x80, 1) // Item 1 uses the auxiliary ispe.
	assoc = binary.BigEndian.AppendUint32(assoc, primary)
	assoc = append(assoc, byte(len(props)-1))
	for i := 2; i <= len(props); i++ {
		assoc = binary.BigEndian.AppendUint16(assoc, 0x8000|uint16(i))
	}
	meta := bmffBox("meta", make([]byte, 4), hdlr, pitm, iinf, bmffBox("iprp", ipco, bmffBox("ipma", assoc)))
	// A size of zero extends mdat through EOF. The header probe skips its payload.
	return bytes.Join([][]byte{ftyp, meta, []byte("\x00\x00\x00\x00mdat\xaa")}, nil)
}

func clapBox(width, height uint32) []byte {
	var payload []byte
	for _, n := range []uint32{width, 1, height, 1, 0, 1, 0, 1} {
		payload = binary.BigEndian.AppendUint32(payload, n)
	}
	return bmffBox("clap", payload)
}

func TestInspectAVIFPrimaryItem(t *testing.T) {
	payload := []byte("mif1\x00\x00\x00\x00avif")
	ftyp := binary.BigEndian.AppendUint64([]byte("\x00\x00\x00\x01ftyp"), uint64(16+len(payload)))
	ftyp = append(ftyp, payload...)
	items := [][]byte{
		itemInfoBox(3, "Exif", "camera\x00"),
		itemInfoBox(4, "mime", "other\x00application/octet-stream\x00"),
		itemInfoBox(5, "mime", "xmp\x00application/rdf+xml\x00"),
	}
	data := stillAVIFContainer(ftyp, 96, 64, items, clapBox(80, 40), bmffBox("irot", []byte{1}), bmffBox("colr", []byte("profprofile")))
	if !SniffFTYP(data[:12]) {
		t.Fatal("extended ftyp header not recognized")
	}
	r := &headerOnlyReader{Reader: bytes.NewReader(data), payloadStart: int64(len(data) - 1)}
	info, err := Inspect(t.Context(), r, int64(len(data)))
	want := HeaderInfo{Width: 40, Height: 80, Brand: "mif1", HasEXIF: true, HasXMP: true, HasICC: true}
	if err != nil || info != want {
		t.Fatalf("Inspect = %+v, %v; want %+v", info, err, want)
	}
	data = stillAVIFContainer(ftyp, 96, 64, items[1:2], bmffBox("colr", []byte("rICCprofile")))
	for mode, want := range map[MetadataMode]bool{MetadataAll: false, MetadataICC: true, MetadataNone: true} {
		if got, err := HasStrippableMetadata(t.Context(), data, mode); err != nil || got != want {
			t.Errorf("HasStrippableMetadata(%q) = %v, %v; want %v", mode, got, err, want)
		}
	}
	iccOnly := stillAVIFContainer(ftypBox("miaf", "avif"), 96, 64, nil, bmffBox("colr", []byte("rICCprofile")))
	for mode, want := range map[MetadataMode]bool{MetadataAll: false, MetadataICC: false, MetadataNone: true} {
		if got, err := HasStrippableMetadata(t.Context(), iccOnly, mode); err != nil || got != want {
			t.Errorf("HasStrippableMetadata(%q) for ICC only = %v, %v; want %v", mode, got, err, want)
		}
	}
}

type headerOnlyReader struct {
	*bytes.Reader
	payloadStart int64
}

func (r *headerOnlyReader) Read(p []byte) (int, error) {
	if r.Size()-int64(r.Len())+int64(len(p)) > r.payloadStart {
		return 0, errors.New("inspect attempted to read media data")
	}
	return r.Reader.Read(p)
}

func TestInspectAVIFWithItemData(t *testing.T) {
	ftyp := ftypBox("avif", "mif1avis")
	data := stillAVIFContainer(ftyp, 80, 40, nil)
	metaEnd := len(ftyp) + int(binary.BigEndian.Uint32(data[len(ftyp):]))
	data = append(data[:metaEnd], bmffBox("idat", []byte{0xaa})...)
	binary.BigEndian.PutUint32(data[len(ftyp):], uint32(len(data)-len(ftyp)))

	info, err := InspectBytes(t.Context(), data)
	if want := (HeaderInfo{Width: 80, Height: 40, Brand: "avif"}); err != nil || info != want {
		t.Fatalf("InspectBytes with idat = %+v, %v; want %+v", info, err, want)
	}
}

func TestInspectAVIFRejectsUnsupportedContainers(t *testing.T) {
	still := ftypBox("avif", "mif1")
	invalidLargeSize := binary.BigEndian.AppendUint64([]byte("\x00\x00\x00\x01ftyp"), ^uint64(0))
	for _, tc := range []struct {
		name string
		data []byte
		want error
	}{
		{"HEIC", ftypBox("heic", "mif1"), ErrNotAVIF},
		{"other BMFF", ftypBox("mp42", "isom"), ErrNotAVIF},
		{"sequence brand", ftypBox("avis", "avif"), ErrAnimatedAVIF},
		{"generic sequence brand", ftypBox("msf1", "avif"), ErrAnimatedAVIF},
		{"conflicting HEIF compatibility", ftypBox("mif1", "avifmsf1"), ErrNotAVIF},
		{"conflicting AVIF compatibility", ftypBox("miaf", "avifavis"), ErrNotAVIF},
		{"only animated AVIF compatible", ftypBox("miaf", "avis"), ErrAnimatedAVIF},
		{"non-AVIF primary codec", bytes.Replace(stillAVIFContainer(still, 80, 40, nil), []byte("av01"), []byte("hvc1"), 1), ErrNotAVIF},
		{"movie box", append(bytes.Clone(still), bmffBox("moov")...), ErrAnimatedAVIF},
		{"missing meta", still, errInvalidAVIF},
		{"extended length overflow", invalidLargeSize, errInvalidAVIF},
		{"invalid crop", stillAVIFContainer(still, 80, 40, nil, clapBox(81, 40)), errInvalidAVIF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := InspectBytes(t.Context(), tc.data); !errors.Is(err, tc.want) {
				t.Fatalf("InspectBytes error = %v, want %v", err, tc.want)
			}
		})
	}
	for _, size := range []int64{15, maxAVIFBytes + 1} {
		if _, err := Inspect(t.Context(), bytes.NewReader(still), size); !errors.Is(err, errInvalidAVIF) {
			t.Errorf("accepted invalid file size %d: %v", size, err)
		}
	}
	exactLimit := stillAVIFContainer(still, 8_000, 4_000, nil)
	if _, err := InspectBytes(t.Context(), exactLimit); err != nil {
		t.Fatalf("rejected exactly 32 million pixels: %v", err)
	}
	tooLarge := stillAVIFContainer(still, 8_002, 4_000, nil, clapBox(64, 64))
	if _, err := InspectBytes(t.Context(), tooLarge); err == nil {
		t.Fatal("accepted over-limit coded dimensions with a smaller clean aperture")
	}
}

func requireBundledAVIF(t *testing.T) {
	t.Helper()
	if !Available() {
		t.Skip("bundled AVIF tools require macOS on Apple silicon")
	}
}

func avifPixels() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 96, 64))
	for y := range 64 {
		for x := range 96 {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 2), G: uint8(y * 3), B: uint8(x + y), A: uint8(x * 31)})
		}
	}
	return img
}

func decodeAVIFPixels(t *testing.T, data []byte) image.Image {
	t.Helper()
	decoded, err := DecodePNG(t.Context(), data, 1)
	if err != nil {
		t.Fatal(err)
	}
	pixels, err := png.Decode(bytes.NewReader(decoded))
	if err != nil {
		t.Fatal(err)
	}
	return pixels
}

func TestNativeAVIFRoundTrip(t *testing.T) {
	requireBundledAVIF(t)
	img := avifPixels()
	for _, lossless := range []bool{false, true} {
		t.Run(fmt.Sprintf("lossless=%t", lossless), func(t *testing.T) {
			data, err := EncodeImage(t.Context(), img, EncodeOptions{Quality: 72, Lossless: lossless, Speed: 8, Threads: 1})
			if err != nil {
				t.Fatal(err)
			}
			decoded := decodeAVIFPixels(t, data)
			if decoded.Bounds() != img.Bounds() {
				t.Fatalf("decoded bounds = %v, want %v", decoded.Bounds(), img.Bounds())
			}
			for y := range img.Bounds().Dy() {
				for x := range img.Bounds().Dx() {
					got := color.NRGBAModel.Convert(decoded.At(x, y)).(color.NRGBA)
					want := img.NRGBAAt(x, y)
					if got.A != want.A || lossless && got != want {
						t.Fatalf("pixel (%d, %d) = %v, want %v", x, y, got, want)
					}
				}
			}
		})
	}
}

func TestNativeAVIFCleanApertureRotationAndMirror(t *testing.T) {
	requireBundledAVIF(t)
	img := avifPixels()
	var input bytes.Buffer
	if err := png.Encode(&input, img); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "transformed.avif")
	args := []string{"--stdin", "--input-format", "png", "--lossless", "-s", "8", "-j", "1",
		"--crop", "8,4,72,48", "--irot", "1", "--imir", "1", path}
	if err := runTool(t.Context(), "avifenc", args, &input); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decoded := decodeAVIFPixels(t, data)
	if want := image.Rect(0, 0, 48, 72); decoded.Bounds() != want {
		t.Fatalf("transformed bounds = %v, want %v", decoded.Bounds(), want)
	}
	for _, p := range []image.Point{{0, 0}, {47, 71}, {4, 16}, {15, 33}} {
		got := color.NRGBAModel.Convert(decoded.At(p.X, p.Y))
		want := img.NRGBAAt(8+71-p.Y, 4+47-p.X)
		if got != want {
			t.Fatalf("transformed pixel %v = %v, want %v", p, got, want)
		}
	}
}

func TestNativeAVIFMetadata(t *testing.T) {
	requireBundledAVIF(t)
	icc, err := os.ReadFile("/System/Library/ColorSync/Profiles/sRGB Profile.icc")
	if err != nil {
		t.Skipf("system sRGB profile unavailable: %v", err)
	}
	var input bytes.Buffer
	if err := png.Encode(&input, avifPixels()); err != nil {
		t.Fatal(err)
	}
	exif := []byte("II\x2a\x00\x08\x00\x00\x00\x01\x00\x12\x01\x03\x00\x01\x00\x00\x00\x01\x00\x00\x00\x00\x00\x00\x00")
	xmp := []byte("XML:com.adobe.xmp\x00\x00\x00\x00\x00<x:xmpmeta xmlns:x='adobe:ns:meta/'/>")
	data := bytes.Join([][]byte{input.Bytes()[:33], testPNGChunk("eXIf", exif), testPNGChunk("iTXt", xmp), input.Bytes()[33:]}, nil)
	for _, mode := range []MetadataMode{MetadataAll, MetadataICC, MetadataNone} {
		t.Run(fmt.Sprintf("mode=%q", mode), func(t *testing.T) {
			out, err := EncodePNG(t.Context(), data, EncodeOptions{Quality: 70, Metadata: mode, ICC: icc, Threads: 1})
			if err != nil {
				t.Fatal(err)
			}
			info, err := InspectBytes(t.Context(), out)
			if err != nil {
				t.Fatal(err)
			}
			if info.HasEXIF != (mode == MetadataAll) || info.HasXMP != (mode == MetadataAll) || info.HasICC != (mode != MetadataNone) {
				t.Errorf("metadata in %q mode = %+v", mode, info)
			}
			decodeAVIFPixels(t, out)
		})
	}
}

func testPNGChunk(typ string, payload []byte) []byte {
	chunk := binary.BigEndian.AppendUint32(nil, uint32(len(payload)))
	chunk = append(chunk, typ...)
	chunk = append(chunk, payload...)
	return binary.BigEndian.AppendUint32(chunk, crc32.ChecksumIEEE(chunk[4:]))
}

func TestEncodePNGRejectsInvalidInputs(t *testing.T) {
	var input bytes.Buffer
	if err := png.Encode(&input, avifPixels()); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		data    []byte
		options EncodeOptions
	}{
		{"empty input", nil, EncodeOptions{Quality: 70}},
		{"malformed PNG", []byte("not a PNG"), EncodeOptions{Quality: 70}},
		{"quality zero", input.Bytes(), EncodeOptions{}},
		{"quality too high", input.Bytes(), EncodeOptions{Quality: 101}},
		{"speed too high", input.Bytes(), EncodeOptions{Quality: 70, Speed: 11}},
		{"unknown metadata mode", input.Bytes(), EncodeOptions{Quality: 70, Metadata: "unknown"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := EncodePNG(t.Context(), tc.data, tc.options); err == nil || errors.Is(err, ErrUnsupportedPlatform) {
				t.Fatalf("EncodePNG validation error = %v", err)
			}
		})
	}
}

func TestAVIFCancellationAndCorruptPayload(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := EncodeImage(ctx, avifPixels(), EncodeOptions{Quality: 70}); !errors.Is(err, context.Canceled) {
		t.Errorf("EncodeImage cancellation = %v", err)
	}
	if _, err := DecodePNG(ctx, nil, 1); !errors.Is(err, context.Canceled) {
		t.Errorf("DecodePNG cancellation = %v", err)
	}
	requireBundledAVIF(t)
	data, err := EncodeImage(t.Context(), avifPixels(), EncodeOptions{Quality: 80, Threads: 1})
	if err != nil {
		t.Fatal(err)
	}
	r := bytes.NewReader(data)
	reader := boxReader{seeker: r, input: r}
	if err := reader.walk(0, int64(len(data)), func(b box) error {
		if b.typ == "mdat" {
			clear(data[b.offset : b.offset+b.size])
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectBytes(t.Context(), data); err != nil {
		t.Fatalf("zeroed image data invalidated the header: %v", err)
	}
	if _, err := DecodePNG(t.Context(), data, 1); err == nil {
		t.Fatal("decoded a corrupt image payload")
	}
}

func TestIntermediatePNGLimit(t *testing.T) {
	over64 := make([]byte, maxAVIFBytes+1)
	if _, err := EncodePNG(t.Context(), over64, EncodeOptions{Quality: 70}); err == nil || strings.Contains(err.Error(), "限制") {
		t.Fatalf("EncodePNG should accept >64 MiB intermediate PNG past size check, got %v", err)
	}
	over128 := make([]byte, maxIntermediatePNGBytes+1)
	if _, err := EncodePNG(t.Context(), over128, EncodeOptions{Quality: 70}); err == nil || !strings.Contains(err.Error(), "128 MiB") {
		t.Fatalf("EncodePNG should reject >128 MiB intermediate PNG, got %v", err)
	}

	buf := pngBuffer{ctx: t.Context()}
	chunk := make([]byte, 32<<20)
	for range 3 {
		if n, err := buf.Write(chunk); err != nil || n != len(chunk) {
			t.Fatalf("pngBuffer write failed within 128 MiB: n=%d err=%v", n, err)
		}
	}
	if buf.buf.Len() != 96<<20 {
		t.Fatalf("pngBuffer len = %d, want %d", buf.buf.Len(), 96<<20)
	}
	if _, err := buf.Write(make([]byte, (32<<20)+1)); err == nil || !strings.Contains(err.Error(), "128 MiB") {
		t.Fatalf("pngBuffer should reject writes beyond 128 MiB, got %v", err)
	}
}
