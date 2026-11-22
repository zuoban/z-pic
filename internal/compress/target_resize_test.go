package compress

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/jpeg"
	"slices"
	"testing"
)

func TestTargetResizeJPEGHit(t *testing.T) {
	source := targetSizeNoise(512, 352)
	icc := syntheticRGBICC()
	data := makeJPEGWithICCAndOrient(t, source, icc, 6)
	original := bytes.Clone(data)
	path := writeFixture(t, "oriented.jpg", data)

	floor := compressTargetSizeFixture(t, data, path, Options{
		Quality: 1, MaxDimension: MinResizeDimension, Metadata: MetadataKeepICC,
	})
	options := Options{
		Quality: targetSizeTestQuality, Metadata: MetadataKeepICC,
		TargetSizeBytes: max(MinTargetSizeBytes, floor.OutputSize),
	}
	baseline := compressTargetSizeFixture(t, data, path, options)
	if !baseline.TargetSizeMissed || baseline.TargetResized || baseline.Width != 352 || baseline.Height != 512 {
		t.Fatalf("expected an upright target miss at the original dimensions, got %dx%d, %d bytes (target %d)",
			baseline.Width, baseline.Height, baseline.OutputSize, options.TargetSizeBytes)
	}

	options.TargetResizeMinDimension = MinResizeDimension
	expected, preview, err := BytesWithPreview(t.Context(), data, path, options)
	if err != nil {
		t.Fatal(err)
	}
	if !expected.TargetResized || expected.Unchanged || expected.Height < MinResizeDimension || expected.Height >= baseline.Height {
		t.Fatalf("expected automatic downscale, got %dx%d, resized=%v, unchanged=%v",
			expected.Width, expected.Height, expected.TargetResized, expected.Unchanged)
	}
	width, height, _ := resizedDimensions(baseline.Width, baseline.Height, expected.Height)
	assertTargetSizeResult(t, expected, options.TargetSizeBytes, false, "jpeg", image.Pt(width, height))
	assertPreviewMatchesOutput(t, expected, preview)
	assertConvertedJPEGICC(t, expected.Data, icc)
	if jpegOrientation(expected.Data) != 1 {
		t.Fatal("output retained the source EXIF orientation after rotating pixels")
	}
	for _, method := range inputMethods(data) {
		t.Run(method.name, func(t *testing.T) {
			result, err := method.run(t.Context(), path, options)
			if err != nil {
				t.Fatal(err)
			}
			assertTargetSizeResult(t, result, options.TargetSizeBytes, false, "jpeg", image.Pt(width, height))
			if !result.TargetResized || !bytes.Equal(result.Data, expected.Data) {
				t.Fatalf("entry point selected a different output (%d bytes, resized=%v)", result.OutputSize, result.TargetResized)
			}
			if result.snapshot == nil || *result.snapshot != *captureOverwriteSnapshot(original, result.Data) {
				t.Fatal("overwrite snapshot must use the original input and final output bytes")
			}
		})
	}
	if !bytes.Equal(original, data) {
		t.Fatal("compression mutated the source bytes")
	}
}

func TestTargetResizeJPEGMissAndManualCap(t *testing.T) {
	// The ICC profile alone exceeds the target on both JPEG encoders.
	icc := append(syntheticRGBICC(), make([]byte, MinTargetSizeBytes)...)
	binary.BigEndian.PutUint32(icc, uint32(len(icc)))
	data := makeJPEGWithICCAndOrient(t, targetSizeNoise(512, 352), icc, 1)
	options := Options{Quality: targetSizeTestQuality, MaxDimension: 448, TargetSizeBytes: MinTargetSizeBytes, Metadata: MetadataKeepICC}
	baseline := compressTargetSizeFixture(t, data, "noise.jpg", options)
	assertTargetSizeResult(t, baseline, MinTargetSizeBytes, true, "jpeg", image.Pt(448, 308))
	if baseline.TargetResized {
		t.Fatal("manual cap reported as an automatic downscale")
	}
	options.TargetResizeMinDimension = 384
	result := compressTargetSizeFixture(t, data, "noise.jpg", options)
	assertTargetSizeResult(t, result, MinTargetSizeBytes, true, "jpeg", image.Pt(384, 264))
	if !result.TargetResized || result.Unchanged || result.OutputSize >= baseline.OutputSize {
		t.Fatalf("miss must keep the smallest valid candidate, got %d bytes at the cap and %d output bytes (resized=%v)",
			baseline.OutputSize, result.OutputSize, result.TargetResized)
	}
}

func TestTargetResizeDormant(t *testing.T) {
	data := targetResizeJPEGFixture(t)
	for _, test := range []struct {
		name    string
		options Options
	}{
		{"no target", Options{Quality: 92, TargetResizeMinDimension: 64}},
		{"lossless", Options{Quality: 92, Lossless: true, TargetSizeBytes: MinTargetSizeBytes, TargetResizeMinDimension: 64}},
		{"lossless with cap", Options{Quality: 92, Lossless: true, MaxDimension: 384, TargetSizeBytes: MinTargetSizeBytes, TargetResizeMinDimension: 64}},
		{"source below floor", Options{Quality: 92, TargetSizeBytes: MinTargetSizeBytes, TargetResizeMinDimension: 513}},
		{"manual cap equals floor", Options{Quality: 92, MaxDimension: 384, TargetSizeBytes: MinTargetSizeBytes, TargetResizeMinDimension: 384}},
		{"manual cap below floor", Options{Quality: 92, MaxDimension: 384, TargetSizeBytes: MinTargetSizeBytes, TargetResizeMinDimension: 448}},
	} {
		t.Run(test.name, func(t *testing.T) {
			baselineOptions := test.options
			baselineOptions.TargetResizeMinDimension = 0
			baseline := compressTargetSizeFixture(t, data, "noise.jpg", baselineOptions)
			result := compressTargetSizeFixture(t, data, "noise.jpg", test.options)
			if result.TargetResized || result.TargetSizeMissed != baseline.TargetSizeMissed ||
				result.Width != baseline.Width || result.Height != baseline.Height ||
				result.AppliedQuality != baseline.AppliedQuality || !bytes.Equal(result.Data, baseline.Data) {
				t.Fatal("dormant automatic downscale changed the baseline result")
			}
		})
	}
}

func TestTargetResizeDimensionValidation(t *testing.T) {
	data := jpegFixture(t, 100)
	path := writeFixture(t, "photo.jpg", data)
	for _, method := range inputMethods(data) {
		t.Run(method.name, func(t *testing.T) {
			for _, dimension := range []int{-1, MinResizeDimension - 1, MaxResizeDimension + 1} {
				result, err := method.run(t.Context(), path, Options{Quality: 80, TargetResizeMinDimension: dimension})
				if result != nil || err == nil {
					t.Errorf("invalid resize minimum %d gave result=%v, error=%v", dimension, result != nil, err)
				}
			}
			if _, err := method.run(t.Context(), path, Options{Quality: 80, TargetResizeMinDimension: MaxResizeDimension}); err != nil {
				t.Fatalf("valid resize minimum rejected: %v", err)
			}
		})
	}
}

func TestTargetResizeNativeAlpha(t *testing.T) {
	source := targetSizeNoise(256, 192)
	for y := range 192 {
		for x := range 256 {
			pixel := source.NRGBAAt(x, y)
			switch {
			case x < 64:
				pixel.A = 0
			case x < 192:
				pixel.A = 64 + (pixel.R+pixel.B)%128
			}
			source.SetNRGBA(x, y, pixel)
		}
	}
	for _, format := range []OutputFormat{OutputFormatPNG, OutputFormatWebP, OutputFormatAVIF} {
		t.Run(string(format), func(t *testing.T) {
			switch format {
			case OutputFormatPNG:
				requireNativePNGTools(t)
			case OutputFormatWebP:
				requireWebPEncoder(t)
			case OutputFormatAVIF:
				requireAVIF(t)
			}
			icc := avifTestProfile(t)
			data := avifTestPNG(t, source, icc, 6)
			result, preview, err := BytesWithPreview(t.Context(), data, "alpha.png", Options{
				Quality: targetSizeTestQuality, Threads: 1, Metadata: MetadataKeepICC, OutputFormat: format,
				TargetSizeBytes: MinTargetSizeBytes, MaxDimension: 224, TargetResizeMinDimension: 128,
			})
			if err != nil {
				t.Fatal(err)
			}
			width, height, resized := resizedDimensions(192, 256, result.Height)
			if !result.TargetResized || !resized || result.Height < 128 || result.Width != width || result.Height != height ||
				result.Format != string(format) || result.OutputSize != int64(len(result.Data)) ||
				result.TargetSizeMissed != (result.OutputSize > MinTargetSizeBytes) {
				t.Fatalf("unexpected output %s %dx%d, %d bytes (resized=%v, missed=%v)",
					result.Format, result.Width, result.Height, result.OutputSize, result.TargetResized, result.TargetSizeMissed)
			}
			if preview == nil || preview.Bounds().Size() != image.Pt(width, height) {
				t.Fatal("preview does not match the upright selected dimensions")
			}
			_, _, _, topAlpha := preview.At(width/2, height/8).RGBA()
			_, _, _, middleAlpha := preview.At(width/2, height/2).RGBA()
			_, _, _, bottomAlpha := preview.At(width/2, height*7/8).RGBA()
			if topAlpha != 0 || middleAlpha == 0 || middleAlpha == 0xffff || bottomAlpha != 0xffff {
				t.Fatalf("upright output lost transparency: alpha=%d, %d, %d", topAlpha, middleAlpha, bottomAlpha)
			}
			switch format {
			case OutputFormatPNG:
				assertPreviewMatchesOutput(t, result, preview)
				assertConvertedPNGICC(t, result.Data, icc)
			case OutputFormatWebP:
				assertPreviewMatchesOutput(t, result, preview)
				if !bytes.Equal(extractWebPICCProfile(result.Data), icc) {
					t.Fatal("WebP output lost ICC profile")
				}
			case OutputFormatAVIF:
				assertAVIFMetadata(t, result.Data, false, false, true)
			}
		})
	}
}

func TestSearchTargetResize(t *testing.T) {
	baseline := &encodedImage{width: 512, height: 352, data: make([]byte, 220)}

	t.Run("binary search finds larger fit than fixed 4/5 steps", func(t *testing.T) {
		// Fixed 4/5 steps from 512 probe 409 (miss) then 327 (fit).
		// Binary search converges close to the true 400px threshold.
		var edges []int
		selected, err := searchTargetResize(t.Context(), baseline, 100, 64, func(longEdge int) (*encodedImage, error) {
			edges = append(edges, longEdge)
			size := 150
			if longEdge <= 400 {
				size = 90
			}
			return &encodedImage{width: longEdge, data: make([]byte, size)}, nil
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if selected.width <= 327 || selected.width > 400 {
			t.Fatalf("expected binary search width in (327, 400], got %d (edges=%v)", selected.width, edges)
		}
		if len(edges) > maxTargetResizeAttempts {
			t.Fatalf("exceeded attempt budget: %d > %d (edges=%v)", len(edges), maxTargetResizeAttempts, edges)
		}
		wantEdges := []int{287, 399, 455, 427, 413}
		if !slices.Equal(edges, wantEdges) || selected.width != 399 {
			t.Fatalf("selected=%d, edges=%v, want width=399 edges=%v", selected.width, edges, wantEdges)
		}
	})

	t.Run("miss probes minDimension as final attempt on large range", func(t *testing.T) {
		hugeBaseline := &encodedImage{width: 4096, height: 3072, data: make([]byte, 500)}
		var edges []int
		selected, err := searchTargetResize(t.Context(), hugeBaseline, 100, 64, func(longEdge int) (*encodedImage, error) {
			edges = append(edges, longEdge)
			return &encodedImage{width: longEdge, data: make([]byte, 120+longEdge/10)}, nil
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(edges) != maxTargetResizeAttempts {
			t.Fatalf("expected %d attempts, got %d (edges=%v)", maxTargetResizeAttempts, len(edges), edges)
		}
		if edges[len(edges)-1] != 64 {
			t.Fatalf("final attempt must probe minDimension=64, got edges=%v", edges)
		}
		if selected.width != 64 {
			t.Fatalf("expected smallest candidate at minDimension=64, got width=%d", selected.width)
		}
	})

	t.Run("fit only at minDimension succeeds on final attempt", func(t *testing.T) {
		hugeBaseline := &encodedImage{width: 4096, height: 3072, data: make([]byte, 500)}
		var edges []int
		selected, err := searchTargetResize(t.Context(), hugeBaseline, 100, 64, func(longEdge int) (*encodedImage, error) {
			edges = append(edges, longEdge)
			size := 150
			if longEdge == 64 {
				size = 95
			}
			return &encodedImage{width: longEdge, data: make([]byte, size)}, nil
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if selected.width != 64 || len(selected.data) != 95 {
			t.Fatalf("expected fit at minDimension=64 (95 bytes), got width=%d (%d bytes), edges=%v",
				selected.width, len(selected.data), edges)
		}
	})

	t.Run("miss chooses fewest bytes and ties keep larger dimension", func(t *testing.T) {
		sizesByCall := []int{180, 140, 140, 160, 190, 140}
		var edges []int
		selected, err := searchTargetResize(t.Context(), baseline, 100, 64, func(longEdge int) (*encodedImage, error) {
			size := sizesByCall[len(edges)]
			edges = append(edges, longEdge)
			return &encodedImage{width: longEdge, data: make([]byte, size)}, nil
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		wantEdges := []int{287, 175, 119, 91, 77, 64}
		if !slices.Equal(edges, wantEdges) {
			t.Fatalf("edges=%v, want %v", edges, wantEdges)
		}
		// 175, 119, and 64 all tied at 140 bytes; larger dimension 175 must win.
		if selected.width != 175 || len(selected.data) != 140 {
			t.Fatalf("expected width=175 (140 bytes), got width=%d (%d bytes)", selected.width, len(selected.data))
		}
	})

	t.Run("failed probes and ties preserve baseline", func(t *testing.T) {
		sizesByCall := []int{0, 240, 220, 0, 250, 220}
		var edges []int
		selected, err := searchTargetResize(t.Context(), baseline, 100, 64, func(longEdge int) (*encodedImage, error) {
			size := sizesByCall[len(edges)]
			edges = append(edges, longEdge)
			if size == 0 {
				return nil, errors.New("failed encode")
			}
			return &encodedImage{width: longEdge, data: make([]byte, size)}, nil
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if selected != baseline {
			t.Fatalf("expected baseline to be preserved, got width=%d (%d bytes), edges=%v",
				selected.width, len(selected.data), edges)
		}
	})

	t.Run("failed upper probe narrows safely while keeping valid fit", func(t *testing.T) {
		var edges []int
		selected, err := searchTargetResize(t.Context(), baseline, 100, 64, func(longEdge int) (*encodedImage, error) {
			edges = append(edges, longEdge)
			if longEdge == 399 {
				return nil, errors.New("transient encoder failure")
			}
			if longEdge <= 360 {
				return &encodedImage{width: longEdge, data: make([]byte, 90)}, nil
			}
			return &encodedImage{width: longEdge, data: make([]byte, 140)}, nil
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if selected.width <= 287 || selected.width > 360 {
			t.Fatalf("expected valid fit in (287, 360], got width=%d (edges=%v)", selected.width, edges)
		}
	})
}

func TestSearchTargetResizeCancellation(t *testing.T) {
	baseline := &encodedImage{width: 512, height: 352, data: make([]byte, 220)}

	t.Run("canceled during probe", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		calls := 0
		selected, err := searchTargetResize(ctx, baseline, 100, 64, func(longEdge int) (*encodedImage, error) {
			calls++
			cancel()
			return &encodedImage{width: longEdge, data: make([]byte, 99)}, nil
		})
		if selected != nil || !errors.Is(err, context.Canceled) || calls != 1 {
			t.Fatalf("canceled search returned result=%v, error=%v after %d probes", selected != nil, err, calls)
		}
	})

	t.Run("pre-canceled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		calls := 0
		selected, err := searchTargetResize(ctx, baseline, 100, 64, func(longEdge int) (*encodedImage, error) {
			calls++
			return &encodedImage{width: longEdge, data: make([]byte, 99)}, nil
		})
		if selected != nil || !errors.Is(err, context.Canceled) || calls != 0 {
			t.Fatalf("pre-canceled search returned result=%v, error=%v after %d probes", selected != nil, err, calls)
		}
	})

	t.Run("deadline exceeded from probe", func(t *testing.T) {
		calls := 0
		selected, err := searchTargetResize(t.Context(), baseline, 100, 64, func(longEdge int) (*encodedImage, error) {
			calls++
			return nil, context.DeadlineExceeded
		})
		if selected != nil || !errors.Is(err, context.DeadlineExceeded) || calls != 1 {
			t.Fatalf("deadline-exceeded search returned result=%v, error=%v after %d probes", selected != nil, err, calls)
		}
	})
}

func targetResizeJPEGFixture(t *testing.T) []byte {
	t.Helper()
	var data bytes.Buffer
	if err := jpeg.Encode(&data, targetSizeNoise(512, 352), &jpeg.Options{Quality: 100}); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}
