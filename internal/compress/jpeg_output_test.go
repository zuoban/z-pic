package compress

import (
	"bytes"
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
