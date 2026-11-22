package compress

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"math"

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
	} else if original == compressed {
		origNRGBA = compNRGBA
	} else {
		origNRGBA = toNRGBA(original)
	}
	return analyzeVisualDiffNRGBA(ctx, origNRGBA, compNRGBA, resized, buildHeatmap)
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

	var (
		sqErrRGB       uint64
		sqErrAlpha     uint64
		changedPixels  uint64
		maxChannelDiff uint8
		hasAlphaDiff   bool
	)

	totalPixels := uint64(w) * uint64(h)
	for y := 0; y < h; y++ {
		if y&127 == 0 {
			if err := ctx.Err(); err != nil {
				return QualityMetrics{}, nil, err
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
			if a1 != 0 || a2 != 0 {
				dr = absDiffUint8(r1, r2)
				dg = absDiffUint8(g1, g2)
				db = absDiffUint8(b1, b2)
				da = absDiffUint8(a1, a2)
			}

			sqErrRGB += uint64(dr)*uint64(dr) + uint64(dg)*uint64(dg) + uint64(db)*uint64(db)
			if da > 0 {
				hasAlphaDiff = true
				sqErrAlpha += uint64(da) * uint64(da)
			}

			maxD := max(max(dr, dg), max(db, da))
			if maxD > 0 {
				changedPixels++
				if maxD > maxChannelDiff {
					maxChannelDiff = maxD
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

	metrics := QualityMetrics{
		ChangedPercent: float64(changedPixels) * 100 / float64(totalPixels),
		MaxChannelDiff: maxChannelDiff,
		Identical:      changedPixels == 0,
		Resized:        resized,
	}
	if metrics.Identical {
		metrics.PSNR = math.Inf(1)
		metrics.SSIM = 1
		return metrics, heatmap, nil
	}

	var mse float64
	if hasAlphaDiff {
		mse = float64(sqErrRGB+sqErrAlpha) / float64(totalPixels*4)
	} else {
		mse = float64(sqErrRGB) / float64(totalPixels*3)
	}
	if mse <= 0 {
		metrics.PSNR = math.Inf(1)
	} else {
		metrics.PSNR = 10 * math.Log10((255.0*255.0)/mse)
	}

	ssim, err := computeBlockSSIM(ctx, origNRGBA, compNRGBA, hasAlphaDiff)
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

var diffHeatmapLUT [256][3]uint8

func init() {
	for diff := 1; diff < len(diffHeatmapLUT); diff++ {
		level := math.Pow(float64(diff)/255.0, 0.52)
		if level < 0.08 {
			level = 0.08
		}
		if level > 1 {
			level = 1
		}
		hr, hg, hb := heatmapColor(level)
		diffHeatmapLUT[diff] = [3]uint8{hr, hg, hb}
	}
}

// diffHeatmapPixel renders identical regions as a dark desaturated reference
// of the original image and maps nonzero pixel deltas onto a high-contrast
// perceptual ramp (deep cyan -> emerald -> gold -> orange -> magenta) so even
// subtle compression artifacts stand out clearly.
func diffHeatmapPixel(r1, g1, b1, a1, a2, maxDiff uint8) (uint8, uint8, uint8) {
	luma := (299*int(r1) + 587*int(g1) + 114*int(b1)) / 1000
	base := uint8(18 + (luma*44)/255)
	if a1 == 0 && a2 == 0 {
		base = 22
	}
	if maxDiff == 0 {
		return base, base, base
	}
	heat := diffHeatmapLUT[maxDiff]
	// Blend strongly over the muted base so structure stays subtly anchored.
	const mix = 0.86
	baseWeighted := float64(base) * (1 - mix)
	r := uint8(math.Round(baseWeighted + float64(heat[0])*mix))
	g := uint8(math.Round(baseWeighted + float64(heat[1])*mix))
	b := uint8(math.Round(baseWeighted + float64(heat[2])*mix))
	return r, g, b
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

func computeBlockSSIM(ctx context.Context, orig, comp *image.NRGBA, includeAlpha bool) (float64, error) {
	const (
		blockSize = 8
		c1        = 6.5025  // (0.01 * 255)^2
		c2        = 58.5225 // (0.03 * 255)^2
	)
	w, h := orig.Bounds().Dx(), orig.Bounds().Dy()
	var (
		sumSSIM float64
		blocks  int
	)
	for by := 0; by < h; by += blockSize {
		if (by/blockSize)&31 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
		}
		bh := min(blockSize, h-by)
		for bx := 0; bx < w; bx += blockSize {
			bw := min(blockSize, w-bx)
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
