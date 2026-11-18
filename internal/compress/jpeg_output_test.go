package compress

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/jpeg"
	"strconv"
	"testing"
)

func TestJPEGOutputChunkBoundaries(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		chunks    []string
		discarded bool
		want      string
	}{
		{"empty", []string{"", ""}, false, ""},
		{"smaller", []string{"a", "", "bc"}, false, "abc"},
		{"oneByteBelow", []string{"ab", "cd"}, false, "abcd"},
		{"equal", []string{"ab", "cde", ""}, true, ""},
		{"larger", []string{"abcd", "ef", "more"}, true, ""},
		{"largeFirstChunk", []string{"abcdef", "", "more"}, true, ""},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			output := jpegOutput{limit: 5}
			for _, chunk := range scenario.chunks {
				written, err := output.Write([]byte(chunk))
				if err != nil || written != len(chunk) {
					t.Fatalf("Write(%q) = %d, %v", chunk, written, err)
				}
			}
			if output.discarded != scenario.discarded || output.buf.String() != scenario.want {
				t.Fatalf("discarded=%v data=%q", output.discarded, output.buf.String())
			}
			if output.discarded && output.buf.Cap() != 0 {
				t.Fatal("discarded output retained a candidate buffer")
			}
		})
	}
}

func TestJPEGOutputMatchesFullEncode(t *testing.T) {
	colorSource, err := jpeg.Decode(bytes.NewReader(jpegFixture(t, 100)))
	if err != nil {
		t.Fatal(err)
	}
	gray := image.NewGray(image.Rect(0, 0, 128, 96))
	for index := range gray.Pix {
		gray.Pix[index] = uint8(index * 37)
	}
	for name, source := range map[string]image.Image{
		"Color": colorSource,
		"Gray":  gray,
	} {
		t.Run(name, func(t *testing.T) {
			for _, quality := range []int{35, 100} {
				t.Run(strconv.Itoa(quality), func(t *testing.T) {
					options := &jpeg.Options{Quality: quality}
					var full bytes.Buffer
					if err := jpeg.Encode(&full, source, options); err != nil {
						t.Fatal(err)
					}
					for _, limit := range []int{1, full.Len() - 1, full.Len(), full.Len() + 1} {
						t.Run(strconv.Itoa(limit), func(t *testing.T) {
							output := jpegOutput{limit: limit}
							if err := jpeg.Encode(&output, source, options); err != nil {
								t.Fatal(err)
							}
							if full.Len() < limit {
								if output.discarded || !bytes.Equal(output.buf.Bytes(), full.Bytes()) {
									t.Fatal("smaller JPEG differs from the complete reference encoding")
								}
							} else if !output.discarded || output.buf.Cap() != 0 {
								t.Fatal("equal or larger JPEG was retained")
							}
						})
					}
				})
			}
		})
	}
}

func makeICCAPP2Segment(seq, count byte, payload []byte) []byte {
	body := make([]byte, 0, 14+len(payload))
	body = append(body, jpegICCSignature...)
	body = append(body, seq, count)
	body = append(body, payload...)
	seg := make([]byte, 4+len(body))
	seg[0], seg[1] = 0xff, 0xe2
	binary.BigEndian.PutUint16(seg[2:4], uint16(2+len(body)))
	copy(seg[4:], body)
	return seg
}

func TestJPEGEncodeEarlyExitAndMidStreamCancellation(t *testing.T) {
	source := benchmarkSourceNRGBA(640, 480)
	var high bytes.Buffer
	if err := jpeg.Encode(&high, source, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	output := jpegOutput{limit: 128, stopOnDiscard: true}
	err := jpeg.Encode(&output, source, &jpeg.Options{Quality: 95})
	if !errors.Is(err, errJPEGNoSavings) || !output.discarded || output.buf.Cap() != 0 {
		t.Fatalf("expected early stop on discard, got err=%v discarded=%v cap=%d", err, output.discarded, output.buf.Cap())
	}

	// Trigger cancellation specifically during jpegOutput.Write flushes after decode finishes.
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	writeChecks := 0
	cancelCtx := &cancelOnWriteCheck{Context: base, cancel: cancel, target: &writeChecks, trigger: 2}
	encOut := jpegOutput{ctx: cancelCtx, limit: high.Len() * 2, stopOnDiscard: true}
	err = jpeg.Encode(&encOut, source, &jpeg.Options{Quality: 95})
	if !errors.Is(err, context.Canceled) || encOut.buf.Cap() != 0 {
		t.Fatalf("expected mid-encode cancellation, got err=%v checks=%d cap=%d", err, writeChecks, encOut.buf.Cap())
	}
}

type cancelOnWriteCheck struct {
	context.Context
	cancel  context.CancelFunc
	target  *int
	trigger int
}

func (c *cancelOnWriteCheck) Err() error {
	*c.target++
	if *c.target >= c.trigger {
		c.cancel()
	}
	return c.Context.Err()
}

func TestJPEGPreservesValidICCProfileAndIgnoresBrokenICC(t *testing.T) {
	baseJPEG := jpegFixture(t, 100)
	part1 := bytes.Repeat([]byte("P3-PART1"), 32)
	part2 := bytes.Repeat([]byte("P3-PART2"), 32)
	seg1 := makeICCAPP2Segment(1, 2, part1)
	seg2 := makeICCAPP2Segment(2, 2, part2)

	// Out-of-order ICC chunks in source should be normalized to 1..N in output.
	withICC := append([]byte{}, baseJPEG[:2]...)
	withICC = append(withICC, seg2...)
	withICC = append(withICC, seg1...)
	withICC = append(withICC, baseJPEG[2:]...)

	res, err := Bytes(context.Background(), withICC, "p3.jpg", Options{Quality: 45})
	if err != nil {
		t.Fatal(err)
	}
	if res.Unchanged || res.OutputSize >= res.OriginalSize {
		t.Fatalf("expected compressed output with ICC retained: unchanged=%v out=%d orig=%d",
			res.Unchanged, res.OutputSize, res.OriginalSize)
	}
	gotSegs, gotBytes := jpegICCSegments(res.Data)
	if len(gotSegs) != 2 || gotBytes != len(seg1)+len(seg2) ||
		!bytes.Equal(gotSegs[0], seg1) || !bytes.Equal(gotSegs[1], seg2) {
		t.Fatalf("preserved ICC segments mismatch: count=%d bytes=%d", len(gotSegs), gotBytes)
	}
	if _, err := jpeg.Decode(bytes.NewReader(res.Data)); err != nil {
		t.Fatalf("output with preserved ICC failed decode: %v", err)
	}

	// Incomplete ICC (missing part 2 of 2) must be ignored rather than written broken.
	incomplete := append([]byte{}, baseJPEG[:2]...)
	incomplete = append(incomplete, seg1...)
	incomplete = append(incomplete, baseJPEG[2:]...)
	resIncomplete, err := Bytes(context.Background(), incomplete, "broken-icc.jpg", Options{Quality: 45})
	if err != nil {
		t.Fatal(err)
	}
	if segs, _ := jpegICCSegments(resIncomplete.Data); len(segs) != 0 {
		t.Fatalf("incomplete ICC must not be copied: %d", len(segs))
	}
}
