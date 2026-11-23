package compress

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"math"
	"reflect"
	"runtime"
	"sync"

	xdraw "golang.org/x/image/draw"

	"zpic/internal/contextio"
)

// QualityMetrics reports objective pixel similarity between an original image
// and its compressed output. When dimensions differ because of resizing, the
// original is resampled onto the output grid before comparison and Resized is
// set to true.
type QualityMetrics struct {
	PSNR           float64 // Peak Signal-to-Noise Ratio in dB (+Inf when Identical).
	SSIM           float64 // Mean Structural Similarity Index in [0, 1].
	ChangedPercent float64 // Percentage of pixels with any RGBA channel delta > 0.
	MaxChannelDiff uint8   // Maximum per-channel absolute difference across RGBA.
	Identical      bool    // True when every compared RGBA pixel matches exactly.
	Resized        bool    // True when compressed dimensions differed from original.
}

// AnalyzeVisualDiff computes objective quality metrics and a visual difference
// heatmap comparing original against compressed.
func AnalyzeVisualDiff(ctx context.Context, original, compressed image.Image) (QualityMetrics, *image.NRGBA, error) {
	return analyzeVisualDiff(ctx, original, compressed, true)
}

// MeasureQualityMetrics computes objective quality metrics without allocating
// a heatmap raster.
func MeasureQualityMetrics(ctx context.Context, original, compressed image.Image) (QualityMetrics, error) {
	metrics, _, err := analyzeVisualDiff(ctx, original, compressed, false)
	return metrics, err
}

// AnalyzeVisualDiffBytes decodes originalData and compressedData into upright
// rasters, then returns objective quality metrics and a visual diff heatmap.
func AnalyzeVisualDiffBytes(ctx context.Context, originalData, compressedData []byte) (QualityMetrics, *image.NRGBA, error) {
	return analyzeVisualDiffBytes(ctx, originalData, compressedData, true)
}

// MeasureQualityMetricsBytes decodes originalData and compressedData into
// upright rasters and returns objective quality metrics without building a
// heatmap image.
func MeasureQualityMetricsBytes(ctx context.Context, originalData, compressedData []byte) (QualityMetrics, error) {
	metrics, _, err := analyzeVisualDiffBytes(ctx, originalData, compressedData, false)
	return metrics, err
}

func analyzeVisualDiffBytes(ctx context.Context, originalData, compressedData []byte, buildHeatmap bool) (QualityMetrics, *image.NRGBA, error) {
	if err := ctx.Err(); err != nil {
		return QualityMetrics{}, nil, err
	}
	if len(originalData) == 0 || len(compressedData) == 0 {
		return QualityMetrics{}, nil, errors.New("缺少可用于画质对比的图片数据")
	}
	if bytes.Equal(originalData, compressedData) {
		if _, err := InspectBytes(ctx, originalData); err != nil {
			return QualityMetrics{}, nil, fmt.Errorf("无法识别原图用于画质对比：%w", err)
		}
		metrics := QualityMetrics{
			PSNR:      math.Inf(1),
			SSIM:      1,
			Identical: true,
		}
		if !buildHeatmap {
			return metrics, nil, nil
		}
		origNRGBA, err := decodeUprightNRGBA(ctx, originalData)
		if err != nil {
			return QualityMetrics{}, nil, fmt.Errorf("无法解码原图用于画质对比：%w", err)
		}
		return analyzeVisualDiffNRGBA(ctx, origNRGBA, origNRGBA, false, true)
	}
	compNRGBA, err := decodeUprightNRGBA(ctx, compressedData)
	if err != nil {
		return QualityMetrics{}, nil, fmt.Errorf("无法解码压缩结果用于画质对比：%w", err)
	}
	origNRGBA, resized, err := decodeAlignedUprightNRGBA(ctx, originalData, compNRGBA.Bounds().Dx(), compNRGBA.Bounds().Dy())
	if err != nil {
		return QualityMetrics{}, nil, fmt.Errorf("无法解码原图用于画质对比：%w", err)
	}
	return analyzeVisualDiffNRGBA(ctx, origNRGBA, compNRGBA, resized, buildHeatmap)
}

func decodeUprightNRGBA(ctx context.Context, data []byte) (*image.NRGBA, error) {
	img, err := decodeUprightImage(ctx, data)
	if err != nil {
		return nil, err
	}
	return toNRGBA(img), nil
}

func decodeAlignedUprightNRGBA(ctx context.Context, data []byte, targetW, targetH int) (*image.NRGBA, bool, error) {
	img, err := decodeUprightImage(ctx, data)
	if err != nil {
		return nil, false, err
	}
	bounds := img.Bounds()
	if bounds.Empty() {
		return nil, false, errors.New("图片尺寸为空，无法对比")
	}
	if bounds.Dx() == targetW && bounds.Dy() == targetH {
		return toNRGBA(img), false, nil
	}
	return scaleToAlignedNRGBA(img, targetW, targetH), true, nil
}

func scaleToAlignedNRGBA(src image.Image, targetW, targetH int) *image.NRGBA {
	dstBounds := image.Rect(0, 0, targetW, targetH)
	// Match resizeImage in transform.go (CatmullRom onto premultiplied RGBA) so
	// lossless downscaling does not introduce spurious filter-mismatch diffs.
	scaled := image.NewRGBA(dstBounds)
	xdraw.CatmullRom.Scale(scaled, dstBounds, src, src.Bounds(), xdraw.Src, nil)
	return toNRGBA(scaled)
}

func decodeUprightImage(ctx context.Context, data []byte) (image.Image, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := InspectBytes(ctx, data)
	if err != nil {
		return nil, err
	}
	if info.Format == "avif" || info.Format == "heic" {
		return DecodePreviewImage(ctx, data)
	}
	img, format, err := image.Decode(contextio.NewReader(ctx, bytes.NewReader(data)))
	if cancelErr := ctx.Err(); cancelErr != nil {
		return nil, cancelErr
	}
	if err != nil {
		return nil, err
	}
	switch format {
	case "jpeg":
		img = normalizeOrientation(img, jpegOrientation(data))
	case "webp":
		img = normalizeOrientation(img, webpOrientation(data))
	case "png":
		img = normalizeOrientation(img, pngOrientation(data))
	}
	return img, nil
}

func analyzeVisualDiff(ctx context.Context, original, compressed image.Image, buildHeatmap bool) (QualityMetrics, *image.NRGBA, error) {
	if err := ctx.Err(); err != nil {
		return QualityMetrics{}, nil, err
	}
	if original == nil || compressed == nil {
		return QualityMetrics{}, nil, errors.New("无法对比空图片")
	}
	origBounds := original.Bounds()
	compBounds := compressed.Bounds()
	if origBounds.Empty() || compBounds.Empty() {
		return QualityMetrics{}, nil, errors.New("图片尺寸为空，无法对比")
	}

	compNRGBA := toNRGBA(compressed)
	w, h := compNRGBA.Bounds().Dx(), compNRGBA.Bounds().Dy()
	resized := origBounds.Dx() != w || origBounds.Dy() != h

	var origNRGBA *image.NRGBA
	if resized {
		origNRGBA = scaleToAlignedNRGBA(original, w, h)
	} else if sameImagePointer(original, compressed) {
		origNRGBA = compNRGBA
	} else {
		origNRGBA = toNRGBA(original)
	}
	return analyzeVisualDiffNRGBA(ctx, origNRGBA, compNRGBA, resized, buildHeatmap)
}

func sameImagePointer(a, b image.Image) bool {
	if a == nil || b == nil {
		return false
	}
	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
	return va.Kind() == reflect.Pointer && va.Type() == vb.Type() && va.Pointer() == vb.Pointer()
}

func premultiplyChannel(v, a uint8) uint8 {
	return uint8((uint16(v)*uint16(a) + 127) / 255)
}

const (
	diffBlockSize         = 8
	parallelDiffMinPixels = 65536
)

type pixelDiffStripeStats struct {
	sqErrRGB       uint64
	sqErrAlpha     uint64
	changedPixels  uint64
	maxChannelDiff uint8
	hasAlphaDiff   bool
}

func parallelDiffWorkers(w, h int) int {
	if uint64(w)*uint64(h) < parallelDiffMinPixels {
		return 1
	}
	blockRows := (h + diffBlockSize - 1) / diffBlockSize
	if blockRows <= 1 {
		return 1
	}
	workers := runtime.GOMAXPROCS(0)
	if workers <= 1 {
		return 1
	}
	return min(workers, blockRows)
}

func analyzeVisualDiffStripe(ctx context.Context, origNRGBA, compNRGBA, heatmap *image.NRGBA, yStart, yEnd, w int) (pixelDiffStripeStats, error) {
	var stats pixelDiffStripeStats
	buildHeatmap := heatmap != nil
	for y := yStart; y < yEnd; y++ {
		if (y-yStart)&127 == 0 {
			if err := ctx.Err(); err != nil {
				return stats, err
			}
		}
		oRow := y * origNRGBA.Stride
		cRow := y * compNRGBA.Stride
		hRow := 0
		if buildHeatmap {
			hRow = y * heatmap.Stride
		}
		for x := 0; x < w; x++ {
			oi := oRow + x*4
			ci := cRow + x*4

			r1, g1, b1, a1 := origNRGBA.Pix[oi], origNRGBA.Pix[oi+1], origNRGBA.Pix[oi+2], origNRGBA.Pix[oi+3]
			r2, g2, b2, a2 := compNRGBA.Pix[ci], compNRGBA.Pix[ci+1], compNRGBA.Pix[ci+2], compNRGBA.Pix[ci+3]

			var dr, dg, db, da uint8
			if a1 == 255 && a2 == 255 {
				dr = absDiffUint8(r1, r2)
				dg = absDiffUint8(g1, g2)
				db = absDiffUint8(b1, b2)
			} else if a1 != 0 || a2 != 0 {
				dr = absDiffUint8(premultiplyChannel(r1, a1), premultiplyChannel(r2, a2))
				dg = absDiffUint8(premultiplyChannel(g1, a1), premultiplyChannel(g2, a2))
				db = absDiffUint8(premultiplyChannel(b1, a1), premultiplyChannel(b2, a2))
				da = absDiffUint8(a1, a2)
			}

			stats.sqErrRGB += uint64(dr)*uint64(dr) + uint64(dg)*uint64(dg) + uint64(db)*uint64(db)
			if da > 0 {
				stats.hasAlphaDiff = true
				stats.sqErrAlpha += uint64(da) * uint64(da)
			}

			maxD := max(max(dr, dg), max(db, da))
			if maxD > 0 {
				stats.changedPixels++
				if maxD > stats.maxChannelDiff {
					stats.maxChannelDiff = maxD
				}
			}

			if buildHeatmap {
				hi := hRow + x*4
				hr, hg, hb := diffHeatmapPixel(r1, g1, b1, a1, a2, maxD)
				heatmap.Pix[hi] = hr
				heatmap.Pix[hi+1] = hg
				heatmap.Pix[hi+2] = hb
				heatmap.Pix[hi+3] = 255
			}
		}
	}
	return stats, nil
}

func analyzeVisualDiffNRGBA(ctx context.Context, origNRGBA, compNRGBA *image.NRGBA, resized, buildHeatmap bool) (QualityMetrics, *image.NRGBA, error) {
	if err := ctx.Err(); err != nil {
		return QualityMetrics{}, nil, err
	}
	w, h := compNRGBA.Bounds().Dx(), compNRGBA.Bounds().Dy()
	if w <= 0 || h <= 0 {
		return QualityMetrics{}, nil, errors.New("图片尺寸为空，无法对比")
	}

	var heatmap *image.NRGBA
	if buildHeatmap {
		heatmap = image.NewNRGBA(image.Rect(0, 0, w, h))
	}

	totalPixels := uint64(w) * uint64(h)
	workers := parallelDiffWorkers(w, h)

	var combined pixelDiffStripeStats
	if workers == 1 {
		stats, err := analyzeVisualDiffStripe(ctx, origNRGBA, compNRGBA, heatmap, 0, h, w)
		if err != nil {
			return QualityMetrics{}, nil, err
		}
		combined = stats
	} else {
		blockRows := (h + diffBlockSize - 1) / diffBlockSize
		stripeStats := make([]pixelDiffStripeStats, workers)
		stripeErrs := make([]error, workers)

		var wg sync.WaitGroup
		for i := 1; i < workers; i++ {
			yStart := ((i * blockRows) / workers) * diffBlockSize
			yEnd := min(h, (((i+1)*blockRows)/workers)*diffBlockSize)
			wg.Add(1)
			go func(idx, start, end int) {
				defer wg.Done()
				stripeStats[idx], stripeErrs[idx] = analyzeVisualDiffStripe(ctx, origNRGBA, compNRGBA, heatmap, start, end, w)
			}(i, yStart, yEnd)
		}
		yEnd0 := min(h, (blockRows/workers)*diffBlockSize)
		stripeStats[0], stripeErrs[0] = analyzeVisualDiffStripe(ctx, origNRGBA, compNRGBA, heatmap, 0, yEnd0, w)
		wg.Wait()

		for _, err := range stripeErrs {
			if err != nil {
				return QualityMetrics{}, nil, err
			}
		}
		if err := ctx.Err(); err != nil {
			return QualityMetrics{}, nil, err
		}
		for _, st := range stripeStats {
			combined.sqErrRGB += st.sqErrRGB
			combined.sqErrAlpha += st.sqErrAlpha
			combined.changedPixels += st.changedPixels
			if st.maxChannelDiff > combined.maxChannelDiff {
				combined.maxChannelDiff = st.maxChannelDiff
			}
			if st.hasAlphaDiff {
				combined.hasAlphaDiff = true
			}
		}
	}

	metrics := QualityMetrics{
		ChangedPercent: float64(combined.changedPixels) * 100 / float64(totalPixels),
		MaxChannelDiff: combined.maxChannelDiff,
		Identical:      combined.changedPixels == 0,
		Resized:        resized,
	}
	if metrics.Identical {
		metrics.PSNR = math.Inf(1)
		metrics.SSIM = 1
		return metrics, heatmap, nil
	}

	var mse float64
	if combined.hasAlphaDiff {
		mse = float64(combined.sqErrRGB+combined.sqErrAlpha) / float64(totalPixels*4)
	} else {
		mse = float64(combined.sqErrRGB) / float64(totalPixels*3)
	}
	if mse <= 0 {
		metrics.PSNR = math.Inf(1)
	} else {
		metrics.PSNR = 10 * math.Log10((255.0*255.0)/mse)
	}

	ssim, err := computeBlockSSIM(ctx, origNRGBA, compNRGBA, combined.hasAlphaDiff)
	if err != nil {
		return QualityMetrics{}, nil, err
	}
	metrics.SSIM = ssim
	return metrics, heatmap, nil
}

func toNRGBA(src image.Image) *image.NRGBA {
	b := src.Bounds()
	if nrgba, ok := src.(*image.NRGBA); ok && b.Min.X == 0 && b.Min.Y == 0 {
		return nrgba
	}
	dst := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Src)
	return dst
}

func absDiffUint8(a, b uint8) uint8 {
	if a >= b {
		return a - b
	}
	return b - a
}

const (
	diffHeatmapBaseMin = 18
	diffHeatmapBaseMax = 62
)

var diffHeatmapBlendLUT [diffHeatmapBaseMax - diffHeatmapBaseMin + 1][256][3]uint8

func init() {
	const mix = 0.86
	for diff := 1; diff < 256; diff++ {
		level := math.Pow(float64(diff)/255.0, 0.52)
		if level < 0.08 {
			level = 0.08
		}
		if level > 1 {
			level = 1
		}
		hr, hg, hb := heatmapColor(level)
		// Precompute the exact blended RGB for every possible muted base value
		// [18..62] so per-pixel rendering avoids float/math.Round work while
		// preserving bit-exact output.
		for base := diffHeatmapBaseMin; base <= diffHeatmapBaseMax; base++ {
			baseWeighted := float64(base) * (1 - mix)
			diffHeatmapBlendLUT[base-diffHeatmapBaseMin][diff] = [3]uint8{
				uint8(math.Round(baseWeighted + float64(hr)*mix)),
				uint8(math.Round(baseWeighted + float64(hg)*mix)),
				uint8(math.Round(baseWeighted + float64(hb)*mix)),
			}
		}
	}
}

// diffHeatmapPixel renders identical regions as a dark desaturated reference
// of the original image and maps nonzero pixel deltas onto a high-contrast
// perceptual ramp (deep cyan -> emerald -> gold -> orange -> magenta) so even
// subtle compression artifacts stand out clearly.
func diffHeatmapPixel(r1, g1, b1, a1, a2, maxDiff uint8) (uint8, uint8, uint8) {
	var base uint8
	if a1 == 0 {
		base = 22
	} else {
		luma := (299*int(r1) + 587*int(g1) + 114*int(b1)) / 1000
		if a1 < 255 {
			luma = (luma*int(a1) + 127) / 255
		}
		base = uint8(diffHeatmapBaseMin + (luma*44)/255)
	}
	if maxDiff == 0 {
		return base, base, base
	}
	rgb := diffHeatmapBlendLUT[base-diffHeatmapBaseMin][maxDiff]
	return rgb[0], rgb[1], rgb[2]
}

func heatmapColor(t float64) (uint8, uint8, uint8) {
	type stop struct {
		pos     float64
		r, g, b float64
	}
	stops := [...]stop{
		{0.00, 32, 132, 236}, // vivid blue (tiny 1-2 level diffs)
		{0.25, 34, 211, 238}, // cyan
		{0.50, 250, 204, 21}, // amber-yellow
		{0.78, 249, 115, 22}, // vivid orange
		{1.00, 244, 63, 94},  // crimson-rose (heavy artifacts)
	}
	for i := 1; i < len(stops); i++ {
		if t <= stops[i].pos {
			prev, next := stops[i-1], stops[i]
			span := next.pos - prev.pos
			f := (t - prev.pos) / span
			return uint8(math.Round(prev.r + (next.r-prev.r)*f)),
				uint8(math.Round(prev.g + (next.g-prev.g)*f)),
				uint8(math.Round(prev.b + (next.b-prev.b)*f))
		}
	}
	last := stops[len(stops)-1]
	return uint8(last.r), uint8(last.g), uint8(last.b)
}

func computeBlockSSIMRow(orig, comp *image.NRGBA, includeAlpha bool, by, w, h int) (float64, int) {
	const (
		c1 = 6.5025  // (0.01 * 255)^2
		c2 = 58.5225 // (0.03 * 255)^2
	)
	bh := min(diffBlockSize, h-by)
	var (
		sumSSIM float64
		blocks  int
	)
	for bx := 0; bx < w; bx += diffBlockSize {
		bw := min(diffBlockSize, w-bx)
		n := float64(bw * bh)
		var (
			sumX, sumY     float64
			sumXX, sumYY   float64
			sumXY          float64
			sumAX, sumAY   float64
			sumAXX, sumAYY float64
			sumAXY         float64
		)
		for y := by; y < by+bh; y++ {
			oRow := y * orig.Stride
			cRow := y * comp.Stride
			for x := bx; x < bx+bw; x++ {
				oi := oRow + x*4
				ci := cRow + x*4
				ax := float64(orig.Pix[oi+3])
				ay := float64(comp.Pix[ci+3])
				lx := (0.299*float64(orig.Pix[oi]) + 0.587*float64(orig.Pix[oi+1]) + 0.114*float64(orig.Pix[oi+2])) * (ax / 255.0)
				ly := (0.299*float64(comp.Pix[ci]) + 0.587*float64(comp.Pix[ci+1]) + 0.114*float64(comp.Pix[ci+2])) * (ay / 255.0)
				sumX += lx
				sumY += ly
				sumXX += lx * lx
				sumYY += ly * ly
				sumXY += lx * ly
				if includeAlpha {
					sumAX += ax
					sumAY += ay
					sumAXX += ax * ax
					sumAYY += ay * ay
					sumAXY += ax * ay
				}
			}
		}
		lumaSSIM := ssimFromSums(n, sumX, sumY, sumXX, sumYY, sumXY, c1, c2)
		if includeAlpha {
			alphaSSIM := ssimFromSums(n, sumAX, sumAY, sumAXX, sumAYY, sumAXY, c1, c2)
			sumSSIM += 0.75*lumaSSIM + 0.25*alphaSSIM
		} else {
			sumSSIM += lumaSSIM
		}
		blocks++
	}
	return sumSSIM, blocks
}

func computeBlockSSIM(ctx context.Context, orig, comp *image.NRGBA, includeAlpha bool) (float64, error) {
	w, h := orig.Bounds().Dx(), orig.Bounds().Dy()
	blockRows := (h + diffBlockSize - 1) / diffBlockSize
	if w <= 0 || blockRows == 0 {
		return 1, nil
	}
	workers := parallelDiffWorkers(w, h)
	rowSums := make([]float64, blockRows)
	rowBlocks := make([]int, blockRows)
	errs := make([]error, workers)

	runRange := func(workerIdx, startRow, endRow int) {
		for br := startRow; br < endRow; br++ {
			if (br-startRow)&31 == 0 {
				if err := ctx.Err(); err != nil {
					errs[workerIdx] = err
					return
				}
			}
			rowSums[br], rowBlocks[br] = computeBlockSSIMRow(orig, comp, includeAlpha, br*diffBlockSize, w, h)
		}
	}

	if workers == 1 {
		runRange(0, 0, blockRows)
	} else {
		var wg sync.WaitGroup
		for i := 1; i < workers; i++ {
			startRow := (i * blockRows) / workers
			endRow := ((i + 1) * blockRows) / workers
			wg.Add(1)
			go func(idx, start, end int) {
				defer wg.Done()
				runRange(idx, start, end)
			}(i, startRow, endRow)
		}
		runRange(0, 0, blockRows/workers)
		wg.Wait()
	}

	for _, err := range errs {
		if err != nil {
			return 0, err
		}
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	var (
		sumSSIM float64
		blocks  int
	)
	for br := range blockRows {
		sumSSIM += rowSums[br]
		blocks += rowBlocks[br]
	}
	if blocks == 0 {
		return 1, nil
	}
	mean := sumSSIM / float64(blocks)
	if mean > 1 {
		mean = 1
	} else if mean < 0 {
		mean = 0
	}
	return mean, nil
}

func ssimFromSums(n, sumX, sumY, sumXX, sumYY, sumXY, c1, c2 float64) float64 {
	muX := sumX / n
	muY := sumY / n
	varX := max(0, sumXX/n-muX*muX)
	varY := max(0, sumYY/n-muY*muY)
	covXY := sumXY/n - muX*muY
	num := (2*muX*muY + c1) * (2*covXY + c2)
	den := (muX*muX + muY*muY + c1) * (varX + varY + c2)
	if den == 0 {
		return 1
	}
	return num / den
}
