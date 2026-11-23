package compress

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"reflect"
	"runtime"
	"sync"

	xdraw "golang.org/x/image/draw"

	"zpic/internal/contextio"
	"zpic/internal/ycbcr"
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
	decodeCtx, cancelDecode := context.WithCancel(ctx)
	defer cancelDecode()

	var (
		compImg, origImg image.Image
		compErr, origErr error
		wg               sync.WaitGroup
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		origImg, origErr = decodeUprightImage(decodeCtx, originalData)
		if origErr != nil {
			cancelDecode()
		}
	}()
	compImg, compErr = decodeUprightImage(decodeCtx, compressedData)
	if compErr != nil {
		cancelDecode()
	}
	wg.Wait()

	if err := ctx.Err(); err != nil {
		return QualityMetrics{}, nil, err
	}
	if compErr != nil && (origErr == nil || !errors.Is(compErr, context.Canceled)) {
		return QualityMetrics{}, nil, fmt.Errorf("无法解码压缩结果用于画质对比：%w", compErr)
	}
	if origErr != nil {
		return QualityMetrics{}, nil, fmt.Errorf("无法解码原图用于画质对比：%w", origErr)
	}
	compBounds := compImg.Bounds()
	origBounds := origImg.Bounds()
	if compBounds.Empty() || origBounds.Empty() {
		return QualityMetrics{}, nil, errors.New("图片尺寸为空，无法对比")
	}
	compNRGBA := toNRGBA(compImg)
	w, h := compBounds.Dx(), compBounds.Dy()
	resized := origBounds.Dx() != w || origBounds.Dy() != h
	var origNRGBA *image.NRGBA
	if resized {
		origNRGBA = scaleToAlignedNRGBA(origImg, w, h)
	} else {
		origNRGBA = toNRGBA(origImg)
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
	orient := 1
	switch format {
	case "jpeg":
		orient = jpegOrientation(data)
	case "webp":
		orient = webpOrientation(data)
	case "png":
		orient = pngOrientation(data)
	}
	if orient >= 2 && orient <= 8 {
		return orientImage(ctx, img, orient)
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

const (
	ssimC1 = 6.5025  // (0.01 * 255)^2
	ssimC2 = 58.5225 // (0.03 * 255)^2
)

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

func renderIdenticalHeatmapStripe(ctx context.Context, origNRGBA, heatmap *image.NRGBA, yStart, yEnd, w int) error {
	for y := yStart; y < yEnd; y++ {
		if (y-yStart)&127 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		oRow := y * origNRGBA.Stride
		hRow := y * heatmap.Stride
		for x := 0; x < w; x++ {
			oi := oRow + x*4
			hi := hRow + x*4
			base := diffHeatmapBase(origNRGBA.Pix[oi], origNRGBA.Pix[oi+1], origNRGBA.Pix[oi+2], origNRGBA.Pix[oi+3])
			heatmap.Pix[hi] = base
			heatmap.Pix[hi+1] = base
			heatmap.Pix[hi+2] = base
			heatmap.Pix[hi+3] = 255
		}
	}
	return nil
}

func computeBlockAlphaSSIM(orig, comp *image.NRGBA, bx, by, bw, bh int, n float64) float64 {
	var sumAX, sumAY, sumAXX, sumAYY, sumAXY float64
	for y := by; y < by+bh; y++ {
		oRow := y * orig.Stride
		cRow := y * comp.Stride
		for x := bx; x < bx+bw; x++ {
			ax := float64(orig.Pix[oRow+x*4+3])
			ay := float64(comp.Pix[cRow+x*4+3])
			sumAX += ax
			sumAY += ay
			sumAXX += ax * ax
			sumAYY += ay * ay
			sumAXY += ax * ay
		}
	}
	return ssimFromSums(n, sumAX, sumAY, sumAXX, sumAYY, sumAXY, ssimC1, ssimC2)
}

// analyzeVisualDiffBlockRange fuses pixel diff metrics, optional heatmap
// rendering, and 8x8 block SSIM into a single pass over origNRGBA and compNRGBA
// so image buffers are streamed through CPU cache only once.
func analyzeVisualDiffBlockRange(
	ctx context.Context,
	origNRGBA, compNRGBA, heatmap *image.NRGBA,
	startBlockRow, endBlockRow, w, h int,
	rowLumaSSIM, rowAlphaWeightedSSIM []float64,
) (pixelDiffStripeStats, error) {
	var stats pixelDiffStripeStats
	buildHeatmap := heatmap != nil
	origPix := origNRGBA.Pix
	compPix := compNRGBA.Pix
	origStride := origNRGBA.Stride
	compStride := compNRGBA.Stride
	var heatPix []byte
	var heatStride int
	if buildHeatmap {
		heatPix = heatmap.Pix
		heatStride = heatmap.Stride
	}

	for br := startBlockRow; br < endBlockRow; br++ {
		if (br-startBlockRow)&15 == 0 {
			if err := ctx.Err(); err != nil {
				return stats, err
			}
		}
		by := br * diffBlockSize
		bh := min(diffBlockSize, h-by)
		var blockRowLuma, blockRowAlphaWeighted float64

		for bx := 0; bx < w; bx += diffBlockSize {
			bw := min(diffBlockSize, w-bx)
			n := float64(bw * bh)
			var sumX, sumY, sumXX, sumYY, sumXY float64
			blockHasAlphaDiff := false

			for y := by; y < by+bh; y++ {
				oRow := y * origStride
				cRow := y * compStride
				hRow := 0
				if buildHeatmap {
					hRow = y * heatStride
				}
				for x := bx; x < bx+bw; x++ {
					oi := oRow + x*4
					ci := cRow + x*4

					r1, g1, b1, a1 := origPix[oi], origPix[oi+1], origPix[oi+2], origPix[oi+3]
					r2, g2, b2, a2 := compPix[ci], compPix[ci+1], compPix[ci+2], compPix[ci+3]

					var dr, dg, db, da uint8
					var lx, ly float64
					if a1 == 255 && a2 == 255 {
						dr = absDiffUint8(r1, r2)
						dg = absDiffUint8(g1, g2)
						db = absDiffUint8(b1, b2)
						lx = ssimLumaRLUT[r1] + ssimLumaGLUT[g1] + ssimLumaBLUT[b1]
						ly = ssimLumaRLUT[r2] + ssimLumaGLUT[g2] + ssimLumaBLUT[b2]
					} else {
						if a1 != 0 || a2 != 0 {
							dr = absDiffUint8(premultiplyChannel(r1, a1), premultiplyChannel(r2, a2))
							dg = absDiffUint8(premultiplyChannel(g1, a1), premultiplyChannel(g2, a2))
							db = absDiffUint8(premultiplyChannel(b1, a1), premultiplyChannel(b2, a2))
							da = absDiffUint8(a1, a2)
						}
						lx = (ssimLumaRLUT[r1] + ssimLumaGLUT[g1] + ssimLumaBLUT[b1]) * ssimAlphaNormLUT[a1]
						ly = (ssimLumaRLUT[r2] + ssimLumaGLUT[g2] + ssimLumaBLUT[b2]) * ssimAlphaNormLUT[a2]
					}

					sumX += lx
					sumY += ly
					sumXX += lx * lx
					sumYY += ly * ly
					sumXY += lx * ly

					stats.sqErrRGB += uint64(dr)*uint64(dr) + uint64(dg)*uint64(dg) + uint64(db)*uint64(db)
					if da > 0 {
						blockHasAlphaDiff = true
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
						heatPix[hi] = hr
						heatPix[hi+1] = hg
						heatPix[hi+2] = hb
						heatPix[hi+3] = 255
					}
				}
			}

			lumaSSIM := ssimFromSums(n, sumX, sumY, sumXX, sumYY, sumXY, ssimC1, ssimC2)
			blockRowLuma += lumaSSIM
			if blockHasAlphaDiff {
				alphaSSIM := computeBlockAlphaSSIM(origNRGBA, compNRGBA, bx, by, bw, bh, n)
				blockRowAlphaWeighted += 0.75*lumaSSIM + 0.25*alphaSSIM
			} else {
				blockRowAlphaWeighted += 0.75*lumaSSIM + 0.25
			}
		}

		rowLumaSSIM[br] = blockRowLuma
		rowAlphaWeightedSSIM[br] = blockRowAlphaWeighted
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

	workers := parallelDiffWorkers(w, h)
	blockRows := (h + diffBlockSize - 1) / diffBlockSize

	if origNRGBA == compNRGBA {
		if buildHeatmap {
			if workers == 1 {
				if err := renderIdenticalHeatmapStripe(ctx, origNRGBA, heatmap, 0, h, w); err != nil {
					return QualityMetrics{}, nil, err
				}
			} else {
				stripeErrs := make([]error, workers)
				var wg sync.WaitGroup
				for i := 1; i < workers; i++ {
					yStart := ((i * blockRows) / workers) * diffBlockSize
					yEnd := min(h, (((i+1)*blockRows)/workers)*diffBlockSize)
					wg.Add(1)
					go func(idx, start, end int) {
						defer wg.Done()
						stripeErrs[idx] = renderIdenticalHeatmapStripe(ctx, origNRGBA, heatmap, start, end, w)
					}(i, yStart, yEnd)
				}
				yEnd0 := min(h, (blockRows/workers)*diffBlockSize)
				stripeErrs[0] = renderIdenticalHeatmapStripe(ctx, origNRGBA, heatmap, 0, yEnd0, w)
				wg.Wait()
				for _, err := range stripeErrs {
					if err != nil {
						return QualityMetrics{}, nil, err
					}
				}
			}
			if err := ctx.Err(); err != nil {
				return QualityMetrics{}, nil, err
			}
		}
		return QualityMetrics{
			PSNR:      math.Inf(1),
			SSIM:      1,
			Identical: true,
			Resized:   resized,
		}, heatmap, nil
	}

	totalPixels := uint64(w) * uint64(h)
	rowLumaSSIM := make([]float64, blockRows)
	rowAlphaWeightedSSIM := make([]float64, blockRows)

	var combined pixelDiffStripeStats
	if workers == 1 {
		stats, err := analyzeVisualDiffBlockRange(ctx, origNRGBA, compNRGBA, heatmap, 0, blockRows, w, h, rowLumaSSIM, rowAlphaWeightedSSIM)
		if err != nil {
			return QualityMetrics{}, nil, err
		}
		combined = stats
	} else {
		stripeStats := make([]pixelDiffStripeStats, workers)
		stripeErrs := make([]error, workers)

		var wg sync.WaitGroup
		for i := 1; i < workers; i++ {
			startRow := (i * blockRows) / workers
			endRow := ((i + 1) * blockRows) / workers
			wg.Add(1)
			go func(idx, start, end int) {
				defer wg.Done()
				stripeStats[idx], stripeErrs[idx] = analyzeVisualDiffBlockRange(ctx, origNRGBA, compNRGBA, heatmap, start, end, w, h, rowLumaSSIM, rowAlphaWeightedSSIM)
			}(i, startRow, endRow)
		}
		stripeStats[0], stripeErrs[0] = analyzeVisualDiffBlockRange(ctx, origNRGBA, compNRGBA, heatmap, 0, blockRows/workers, w, h, rowLumaSSIM, rowAlphaWeightedSSIM)
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

	blocksPerRow := (w + diffBlockSize - 1) / diffBlockSize
	totalBlocks := blocksPerRow * blockRows
	if totalBlocks == 0 {
		metrics.SSIM = 1
		return metrics, heatmap, nil
	}
	var sumSSIM float64
	if combined.hasAlphaDiff {
		for _, v := range rowAlphaWeightedSSIM {
			sumSSIM += v
		}
	} else {
		for _, v := range rowLumaSSIM {
			sumSSIM += v
		}
	}
	mean := sumSSIM / float64(totalBlocks)
	if mean > 1 {
		mean = 1
	} else if mean < 0 {
		mean = 0
	}
	metrics.SSIM = mean
	return metrics, heatmap, nil
}

func toNRGBA(src image.Image) *image.NRGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if nrgba, ok := src.(*image.NRGBA); ok {
		if b.Min.X == 0 && b.Min.Y == 0 {
			return nrgba
		}
		dst := image.NewNRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			srcOff := nrgba.PixOffset(b.Min.X, b.Min.Y+y)
			dstOff := y * dst.Stride
			copy(dst.Pix[dstOff:dstOff+w*4], nrgba.Pix[srcOff:srcOff+w*4])
		}
		return dst
	}
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	switch s := src.(type) {
	case *image.RGBA:
		for y := 0; y < h; y++ {
			sOff := s.PixOffset(b.Min.X, b.Min.Y+y)
			dOff := y * dst.Stride
			for x := 0; x < w; x++ {
				r, g, bl, a := s.Pix[sOff], s.Pix[sOff+1], s.Pix[sOff+2], s.Pix[sOff+3]
				sOff += 4
				switch a {
				case 0xff:
					dst.Pix[dOff] = r
					dst.Pix[dOff+1] = g
					dst.Pix[dOff+2] = bl
					dst.Pix[dOff+3] = 0xff
				case 0:
					// dst.Pix is already zero-initialized.
				default:
					aa := uint32(a)
					dst.Pix[dOff] = uint8(((uint32(r) * 0xffff) / aa) >> 8)
					dst.Pix[dOff+1] = uint8(((uint32(g) * 0xffff) / aa) >> 8)
					dst.Pix[dOff+2] = uint8(((uint32(bl) * 0xffff) / aa) >> 8)
					dst.Pix[dOff+3] = a
				}
				dOff += 4
			}
		}
		return dst
	case *image.YCbCr:
		ycbcr.ToNRGBAInto(dst, s, b)
		return dst
	case *image.NYCbCrA:
		ycbcr.NYCbCrAToNRGBAInto(dst, s, b)
		return dst
	case *image.Paletted:
		var lut [256]color.NRGBA
		for i, c := range s.Palette {
			if i >= len(lut) {
				break
			}
			if c == nil {
				continue
			}
			lut[i] = color.NRGBAModel.Convert(c).(color.NRGBA)
		}
		for y := 0; y < h; y++ {
			sOff := s.PixOffset(b.Min.X, b.Min.Y+y)
			dOff := y * dst.Stride
			for x := 0; x < w; x++ {
				c := lut[s.Pix[sOff+x]]
				dst.Pix[dOff] = c.R
				dst.Pix[dOff+1] = c.G
				dst.Pix[dOff+2] = c.B
				dst.Pix[dOff+3] = c.A
				dOff += 4
			}
		}
		return dst
	case *image.Gray:
		for y := 0; y < h; y++ {
			sOff := s.PixOffset(b.Min.X, b.Min.Y+y)
			dOff := y * dst.Stride
			for x := 0; x < w; x++ {
				v := s.Pix[sOff+x]
				dst.Pix[dOff] = v
				dst.Pix[dOff+1] = v
				dst.Pix[dOff+2] = v
				dst.Pix[dOff+3] = 0xff
				dOff += 4
			}
		}
		return dst
	default:
		draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Src)
		return dst
	}
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

var (
	diffHeatmapBlendLUT [diffHeatmapBaseMax - diffHeatmapBaseMin + 1][256][3]uint8
	ssimLumaRLUT        [256]float64
	ssimLumaGLUT        [256]float64
	ssimLumaBLUT        [256]float64
	ssimAlphaNormLUT    [256]float64
)

func init() {
	for i := range 256 {
		f := float64(i)
		ssimLumaRLUT[i] = 0.299 * f
		ssimLumaGLUT[i] = 0.587 * f
		ssimLumaBLUT[i] = 0.114 * f
		ssimAlphaNormLUT[i] = f / 255.0
	}
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

func diffHeatmapBase(r1, g1, b1, a1 uint8) uint8 {
	if a1 == 0 {
		return 22
	}
	luma := (299*int(r1) + 587*int(g1) + 114*int(b1)) / 1000
	if a1 < 255 {
		luma = (luma*int(a1) + 127) / 255
	}
	return uint8(diffHeatmapBaseMin + (luma*44)/255)
}

// diffHeatmapPixel renders identical regions as a dark desaturated reference
// of the original image and maps nonzero pixel deltas onto a high-contrast
// perceptual ramp (deep cyan -> emerald -> gold -> orange -> magenta) so even
// subtle compression artifacts stand out clearly.
func diffHeatmapPixel(r1, g1, b1, a1, a2, maxDiff uint8) (uint8, uint8, uint8) {
	base := diffHeatmapBase(r1, g1, b1, a1)
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
