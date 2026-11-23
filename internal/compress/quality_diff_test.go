package compress

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math"
	"runtime"
	"testing"
)

func syntheticGradientImage(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8((x * 255) / max(1, w-1)),
				G: uint8((y * 255) / max(1, h-1)),
				B: uint8(((x*3 + y*5) * 7) % 256),
				A: 255,
			})
		}
	}
	return img
}

func TestAnalyzeVisualDiffIdenticalImages(t *testing.T) {
	src := syntheticGradientImage(64, 48)
	metrics, heatmap, err := AnalyzeVisualDiff(context.Background(), src, src)
	if err != nil {
		t.Fatalf("AnalyzeVisualDiff failed: %v", err)
	}
	if !metrics.Identical || metrics.Resized {
		t.Fatalf("expected Identical=true Resized=false, got %+v", metrics)
	}
	if !math.IsInf(metrics.PSNR, 1) || metrics.SSIM != 1 {
		t.Fatalf("expected PSNR=+Inf SSIM=1, got %+v", metrics)
	}
	if metrics.ChangedPercent != 0 || metrics.MaxChannelDiff != 0 {
		t.Fatalf("expected zero diff, got %+v", metrics)
	}
	if heatmap == nil || heatmap.Bounds().Dx() != 64 || heatmap.Bounds().Dy() != 48 {
		t.Fatalf("unexpected heatmap bounds: %v", heatmap)
	}
	// Identical pixels must render as neutral grayscale (R == G == B).
	c := heatmap.NRGBAAt(12, 16)
	if c.R != c.G || c.G != c.B || c.A != 255 {
		t.Fatalf("identical heatmap pixel should be neutral opaque gray, got %+v", c)
	}
}

func TestAnalyzeVisualDiffBytesQualityOrdering(t *testing.T) {
	src := syntheticGradientImage(128, 96)
	var origBuf bytes.Buffer
	if err := png.Encode(&origBuf, src); err != nil {
		t.Fatal(err)
	}
	var highBuf, lowBuf bytes.Buffer
	if err := jpeg.Encode(&highBuf, src, &jpeg.Options{Quality: 92}); err != nil {
		t.Fatal(err)
	}
	if err := jpeg.Encode(&lowBuf, src, &jpeg.Options{Quality: 20}); err != nil {
		t.Fatal(err)
	}

	highMetrics, highMap, err := AnalyzeVisualDiffBytes(context.Background(), origBuf.Bytes(), highBuf.Bytes())
	if err != nil {
		t.Fatalf("high quality diff failed: %v", err)
	}
	lowMetrics, lowMap, err := AnalyzeVisualDiffBytes(context.Background(), origBuf.Bytes(), lowBuf.Bytes())
	if err != nil {
		t.Fatalf("low quality diff failed: %v", err)
	}
	if highMetrics.Identical || lowMetrics.Identical {
		t.Fatalf("lossy JPEG should not be identical: high=%+v low=%+v", highMetrics, lowMetrics)
	}
	if highMetrics.PSNR <= lowMetrics.PSNR {
		t.Fatalf("expected high-quality PSNR > low-quality PSNR: high=%.2f low=%.2f", highMetrics.PSNR, lowMetrics.PSNR)
	}
	if highMetrics.SSIM <= lowMetrics.SSIM {
		t.Fatalf("expected high-quality SSIM > low-quality SSIM: high=%.4f low=%.4f", highMetrics.SSIM, lowMetrics.SSIM)
	}
	if highMap == nil || lowMap == nil {
		t.Fatal("expected non-nil diff heatmaps")
	}

	// Verify identical bytes fast path.
	sameMetrics, err := MeasureQualityMetricsBytes(context.Background(), origBuf.Bytes(), origBuf.Bytes())
	if err != nil {
		t.Fatalf("identical bytes metrics failed: %v", err)
	}
	if !sameMetrics.Identical || !math.IsInf(sameMetrics.PSNR, 1) || sameMetrics.SSIM != 1 {
		t.Fatalf("unexpected identical metrics: %+v", sameMetrics)
	}
}

func TestAnalyzeVisualDiffResizedAndAlpha(t *testing.T) {
	orig := syntheticGradientImage(80, 60)
	comp := syntheticGradientImage(40, 30)
	metrics, heatmap, err := AnalyzeVisualDiff(context.Background(), orig, comp)
	if err != nil {
		t.Fatalf("resized diff failed: %v", err)
	}
	if !metrics.Resized {
		t.Fatalf("expected Resized=true, got %+v", metrics)
	}
	if heatmap.Bounds().Dx() != 40 || heatmap.Bounds().Dy() != 30 {
		t.Fatalf("expected heatmap sized to compressed image (40x30), got %v", heatmap.Bounds())
	}
	if metrics.SSIM < 0 || metrics.SSIM > 1 {
		t.Fatalf("SSIM out of range: %f", metrics.SSIM)
	}

	// Alpha-only change must still be detected in metrics and heatmap.
	a1 := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	a2 := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			a1.SetNRGBA(x, y, color.NRGBA{R: 120, G: 120, B: 120, A: 255})
			alpha := uint8(255)
			if x < 8 {
				alpha = 60
			}
			a2.SetNRGBA(x, y, color.NRGBA{R: 120, G: 120, B: 120, A: alpha})
		}
	}
	alphaMetrics, alphaMap, err := AnalyzeVisualDiff(context.Background(), a1, a2)
	if err != nil {
		t.Fatalf("alpha diff failed: %v", err)
	}
	if alphaMetrics.Identical || alphaMetrics.MaxChannelDiff != 195 {
		t.Fatalf("expected alpha difference of 195, got %+v", alphaMetrics)
	}
	if math.Abs(alphaMetrics.ChangedPercent-50) > 0.01 {
		t.Fatalf("expected 50%% changed pixels, got %.2f%%", alphaMetrics.ChangedPercent)
	}
	if alphaMetrics.SSIM >= 1 {
		t.Fatalf("alpha difference should reduce SSIM below 1, got %f", alphaMetrics.SSIM)
	}
	changedPx := alphaMap.NRGBAAt(2, 2)
	unchangedPx := alphaMap.NRGBAAt(12, 2)
	if changedPx.R == changedPx.G && changedPx.G == changedPx.B {
		t.Fatalf("changed alpha pixel should be colored in heatmap, got %+v", changedPx)
	}
	if unchangedPx.R != unchangedPx.G || unchangedPx.G != unchangedPx.B {
		t.Fatalf("unchanged pixel should stay grayscale in heatmap, got %+v", unchangedPx)
	}
}

func TestAnalyzeVisualDiffRespectsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	src := syntheticGradientImage(32, 32)
	if _, _, err := AnalyzeVisualDiff(ctx, src, src); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestAnalyzeVisualDiffTransparentZeroedRGB(t *testing.T) {
	orig := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	comp := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			if x < 8 {
				// Fully transparent pixels with different invisible RGB payloads.
				orig.SetNRGBA(x, y, color.NRGBA{R: 255, G: 120, B: 40, A: 0})
				comp.SetNRGBA(x, y, color.NRGBA{R: 0, G: 0, B: 0, A: 0})
			} else {
				px := color.NRGBA{R: 80, G: 140, B: 200, A: 255}
				orig.SetNRGBA(x, y, px)
				comp.SetNRGBA(x, y, px)
			}
		}
	}

	metrics, heatmap, err := AnalyzeVisualDiff(context.Background(), orig, comp)
	if err != nil {
		t.Fatalf("AnalyzeVisualDiff failed: %v", err)
	}
	if !metrics.Identical || metrics.MaxChannelDiff != 0 || metrics.ChangedPercent != 0 {
		t.Fatalf("fully transparent RGB differences must be treated as identical, got %+v", metrics)
	}
	if !math.IsInf(metrics.PSNR, 1) || metrics.SSIM != 1 {
		t.Fatalf("expected PSNR=+Inf SSIM=1 for identical visible pixels, got %+v", metrics)
	}
	px := heatmap.NRGBAAt(2, 2)
	if px.R != px.G || px.G != px.B || px.A != 255 {
		t.Fatalf("transparent unchanged pixel should stay neutral gray in heatmap, got %+v", px)
	}

	// Introduce a tiny opaque delta and ensure invisible transparent RGB does not
	// distort MaxChannelDiff, SSIM, or light up the transparent region.
	comp.SetNRGBA(10, 10, color.NRGBA{R: 82, G: 140, B: 200, A: 255})
	metrics2, heatmap2, err := AnalyzeVisualDiff(context.Background(), orig, comp)
	if err != nil {
		t.Fatalf("AnalyzeVisualDiff with minor opaque delta failed: %v", err)
	}
	if metrics2.MaxChannelDiff != 2 {
		t.Fatalf("expected MaxChannelDiff=2 (ignoring A==0 RGB), got %d", metrics2.MaxChannelDiff)
	}
	if metrics2.SSIM < 0.999 {
		t.Fatalf("transparent RGB zeroing should not tank SSIM, got %.6f", metrics2.SSIM)
	}
	px2 := heatmap2.NRGBAAt(2, 2)
	if px2.R != px2.G || px2.G != px2.B {
		t.Fatalf("transparent pixel must remain neutral gray in heatmap, got %+v", px2)
	}
}

func TestAnalyzeVisualDiffLosslessResizeMatchesEncoderKernel(t *testing.T) {
	src := syntheticGradientImage(80, 60)
	var origBuf bytes.Buffer
	if err := png.Encode(&origBuf, src); err != nil {
		t.Fatal(err)
	}
	res, err := BytesWithoutSource(context.Background(), origBuf.Bytes(), Options{
		Quality:      80,
		Lossless:     true,
		ScalePercent: 50,
		Threads:      1,
	})
	if err != nil {
		t.Fatalf("lossless resize failed: %v", err)
	}
	metrics, _, err := AnalyzeVisualDiffBytes(context.Background(), origBuf.Bytes(), res.Data)
	if err != nil {
		t.Fatalf("AnalyzeVisualDiffBytes failed: %v", err)
	}
	if !metrics.Resized {
		t.Fatalf("expected Resized=true, got %+v", metrics)
	}
	if !metrics.Identical || metrics.MaxChannelDiff != 0 || !math.IsInf(metrics.PSNR, 1) || metrics.SSIM != 1 {
		t.Fatalf("lossless resize should match CatmullRom alignment without spurious error, got %+v", metrics)
	}
}

func TestAnalyzeVisualDiffBytesEnforcesInspectLimits(t *testing.T) {
	gifHeader := []byte("GIF89a\x01\x00\x01\x00\x80\x00\x00\x00\x00\x00\xff\xff\xff!\xf9\x04\x01\x00\x00\x00\x00,\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x02D\x01\x00;")
	if _, err := MeasureQualityMetricsBytes(context.Background(), gifHeader, gifHeader); err == nil {
		t.Fatal("MeasureQualityMetricsBytes must reject unsupported GIF input even on equal-bytes fast path")
	}
	if _, _, err := AnalyzeVisualDiffBytes(context.Background(), gifHeader, gifHeader); err == nil {
		t.Fatal("AnalyzeVisualDiffBytes must reject unsupported GIF input")
	}
}

func TestDiffHeatmapLUTMatchesExactFormula(t *testing.T) {
	samples := []struct {
		r1, g1, b1, a1, a2 uint8
	}{
		{0, 0, 0, 255, 255},
		{255, 255, 255, 255, 255},
		{123, 45, 200, 255, 255},
		{255, 128, 64, 0, 0},
		{255, 128, 64, 0, 128},
	}
	for _, sample := range samples {
		luma := (299*int(sample.r1) + 587*int(sample.g1) + 114*int(sample.b1)) / 1000
		base := uint8(18 + (luma*44)/255)
		if sample.a1 == 0 && sample.a2 == 0 {
			base = 22
		}
		for diff := 0; diff <= 255; diff++ {
			gotR, gotG, gotB := diffHeatmapPixel(sample.r1, sample.g1, sample.b1, sample.a1, sample.a2, uint8(diff))
			var wantR, wantG, wantB uint8
			if diff == 0 {
				wantR, wantG, wantB = base, base, base
			} else {
				level := math.Pow(float64(diff)/255.0, 0.52)
				if level < 0.08 {
					level = 0.08
				}
				if level > 1 {
					level = 1
				}
				hr, hg, hb := heatmapColor(level)
				const mix = 0.86
				wantR = uint8(math.Round(float64(base)*(1-mix) + float64(hr)*mix))
				wantG = uint8(math.Round(float64(base)*(1-mix) + float64(hg)*mix))
				wantB = uint8(math.Round(float64(base)*(1-mix) + float64(hb)*mix))
			}
			if gotR != wantR || gotG != wantG || gotB != wantB {
				t.Fatalf("sample=%+v diff=%d: got (%d,%d,%d), want (%d,%d,%d)", sample, diff, gotR, gotG, gotB, wantR, wantG, wantB)
			}
		}
	}
}

func TestAnalyzeVisualDiffParallelMatchesSerial(t *testing.T) {
	// Use non-multiple-of-8 dimensions above the 65536-pixel parallel threshold
	// to exercise partial trailing blocks, multi-stripe partitioning, and alpha.
	cases := []struct {
		name      string
		w, h      int
		withAlpha bool
	}{
		{"opaque 301x253", 301, 253, false},
		{"alpha 275x261", 275, 261, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			orig := syntheticGradientImage(tc.w, tc.h)
			comp := image.NewNRGBA(orig.Bounds())
			copy(comp.Pix, orig.Pix)
			for y := 0; y < tc.h; y++ {
				for x := 0; x < tc.w; x++ {
					i := y*comp.Stride + x*4
					if (x+y)%3 == 0 {
						comp.Pix[i] ^= 0x0b
						comp.Pix[i+1] ^= 0x05
						comp.Pix[i+2] ^= 0x03
					}
					if tc.withAlpha {
						if x < 24 && y < 24 {
							orig.Pix[i+3] = 0
							comp.Pix[i+3] = 0
							comp.Pix[i] ^= 0x55 // invisible RGB difference
						} else if (x*5+y*7)%17 == 0 {
							comp.Pix[i+3] = uint8(180 + (x+y)%70)
						}
					}
				}
			}

			prevProcs := runtime.GOMAXPROCS(1)
			defer runtime.GOMAXPROCS(prevProcs)

			if got := parallelDiffWorkers(tc.w, tc.h); got != 1 {
				t.Fatalf("expected 1 worker when GOMAXPROCS=1, got %d", got)
			}
			serialMetrics, serialHeatmap, err := AnalyzeVisualDiff(context.Background(), orig, comp)
			if err != nil {
				t.Fatalf("serial AnalyzeVisualDiff failed: %v", err)
			}
			serialOnlyMetrics, err := MeasureQualityMetrics(context.Background(), orig, comp)
			if err != nil {
				t.Fatalf("serial MeasureQualityMetrics failed: %v", err)
			}
			if serialOnlyMetrics != serialMetrics {
				t.Fatalf("MeasureQualityMetrics != AnalyzeVisualDiff: %+v vs %+v", serialOnlyMetrics, serialMetrics)
			}

			for _, procs := range []int{2, 4, 7} {
				runtime.GOMAXPROCS(procs)
				if got := parallelDiffWorkers(tc.w, tc.h); got != procs {
					t.Fatalf("expected %d workers for %dx%d, got %d", procs, tc.w, tc.h, got)
				}
				parMetrics, parHeatmap, err := AnalyzeVisualDiff(context.Background(), orig, comp)
				if err != nil {
					t.Fatalf("parallel(%d) AnalyzeVisualDiff failed: %v", procs, err)
				}
				if parMetrics != serialMetrics {
					t.Fatalf("parallel(%d) metrics mismatch:\n got: %+v\nwant: %+v", procs, parMetrics, serialMetrics)
				}
				if !bytes.Equal(parHeatmap.Pix, serialHeatmap.Pix) {
					t.Fatalf("parallel(%d) heatmap pixels diverged from serial output", procs)
				}
				parOnlyMetrics, err := MeasureQualityMetrics(context.Background(), orig, comp)
				if err != nil {
					t.Fatalf("parallel(%d) MeasureQualityMetrics failed: %v", procs, err)
				}
				if parOnlyMetrics != serialMetrics {
					t.Fatalf("parallel(%d) MeasureQualityMetrics mismatch: %+v vs %+v", procs, parOnlyMetrics, serialMetrics)
				}
			}
		})
	}
}

func BenchmarkAnalyzeVisualDiff(b *testing.B) {
	orig := syntheticGradientImage(1024, 768)
	comp := image.NewNRGBA(orig.Bounds())
	copy(comp.Pix, orig.Pix)
	for idx := 0; idx < len(comp.Pix); idx += 4 {
		comp.Pix[idx] ^= 0x07
		comp.Pix[idx+1] ^= 0x03
	}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, _, err := AnalyzeVisualDiff(ctx, orig, comp); err != nil {
			b.Fatal(err)
		}
	}
}
