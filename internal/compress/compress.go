// Package compress compresses static JPEG, PNG, WebP, and AVIF images and
// accepts HEIC/HEIF input on macOS. Re-encoding can normalize orientation and
// downscale to a configured long edge, width, height, or percentage.
package compress

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png" // Register the PNG decoder.
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	_ "golang.org/x/image/webp"

	"zpic/internal/aviftools"
	"zpic/internal/contextio"
	"zpic/internal/imageinput"
	"zpic/internal/jpegtools"
	"zpic/internal/pngtools"
	"zpic/internal/webptools"
)

const (
	maxInputSize           = imageinput.MaxSize
	maxIntermediatePNGSize = imageinput.MaxIntermediatePNGSize
	maxPixels              = 32_000_000
)

var jpegtoolsAvailable = jpegtools.Available

type Options struct {
	Quality        int // JPEG, WebP, and AVIF quality in [1, 100].
	PNG            PNGOptions
	Lossless       bool           // PNG: Oxipng only. WebP and AVIF: lossless encoding. JPEG: lossless optimization for an unresized JPEG input when bundled.
	Metadata       MetadataPolicy // "", "icc", or "strip".
	OutputFormat   OutputFormat   // "" (keep), "jpeg", "png", "webp", or "avif".
	JPEGBackground string         // "", "white", "black", or "#RRGGBB" background color for alpha flattening when encoding JPEG.

	// Set at most one resize field. Zero disables that mode. Pixel limits
	// are in [64, 16384] and apply to the image's displayed orientation.
	MaxDimension int // Maximum long edge.
	MaxWidth     int // Maximum width.
	MaxHeight    int // Maximum height.
	ScalePercent int // [1, 100], where 100 keeps the original dimensions.

	TargetSizeBytes          int64 // 0 = disabled; otherwise [MinTargetSizeBytes, MaxTargetSizeBytes].
	TargetResizeMinDimension int   // 0 = disabled; otherwise [64, 16384] minimum long edge for lossy target-size fallback.
	Threads                  int   // Optional native encoder thread cap; <= 0 selects automatic default.
	SkipOverwriteSnapshot    bool  // Skip SHA-256 overwrite snapshot when caller only writes copies.
}

// PNGOptions controls lossy color quantization. The zero value uses the
// balanced defaults. Nonzero values must specify a complete valid setting.
type PNGOptions = pngtools.QuantizeOptions

func DefaultPNGOptions() PNGOptions {
	return pngtools.DefaultQuantizeOptions()
}

// Result describes the bytes selected for export. Width and Height are visual
// upright dimensions, including when Data retains the original EXIF-bearing
// JPEG because recompression offered no saving.
type Result struct {
	SourcePath       string
	Format           string
	Width            int
	Height           int
	OriginalSize     int64
	OutputSize       int64
	Data             []byte
	Unchanged        bool
	TargetSizeBytes  int64
	TargetSizeMissed bool
	TargetResized    bool // The selected output used automatic downscaling for the target.
	AppliedQuality   int  // Lower JPEG/WebP/AVIF quality selected to approach the target.

	sourcePath   string             // Absolute path captured before the caller can change cwd.
	sourceFormat string             // Original decoded format, used to guard Overwrite.
	snapshot     *overwriteSnapshot // Non-nil only for a validated File/Bytes result unless skipped.
}

// File reads an image and compresses it into the selected output format.
// Quality must be in [1, 100]. The zero PNGOptions value uses the balanced
// quantization defaults. Decoding and Go encoding check cancellation on reads/writes.
// Native codec processes are canceled with ctx.
func File(ctx context.Context, path string, options Options) (*Result, error) {
	sourcePath, err := validateRequest(ctx, path, options)
	if err != nil {
		return nil, err
	}
	data, err := readInput(ctx, sourcePath)
	if err != nil {
		return nil, err
	}
	return compressData(ctx, data, path, sourcePath, options, nil)
}

// FileWithAcquire inspects headers on a single open descriptor, calls acquire
// before reading full file bytes into memory, and returns the acquired release
// callback alongside the compression outcome so callers can control lifetime.
func FileWithAcquire(
	ctx context.Context,
	path string,
	options Options,
	acquire func(ImageInfo) (func(), error),
	onStart func(),
) (*Result, func(), error) {
	sourcePath, err := validateRequest(ctx, path, options)
	if err != nil {
		return nil, nil, err
	}
	f, info, err := openAndInspect(ctx, sourcePath)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	info.Path = path
	var release func()
	if acquire != nil {
		release, err = acquire(info)
		if err != nil {
			return nil, nil, err
		}
	}
	if onStart != nil {
		onStart()
	}
	data, err := imageinput.ReadFile(ctx, f, info.Size)
	_ = f.Close()
	if err != nil {
		return nil, release, err
	}
	result, err := compressData(ctx, data, path, sourcePath, options, nil)
	return result, release, err
}

// Bytes compresses an immutable snapshot without reading sourcePath. The path
// must be nonempty and resolvable to an absolute path, but need not still exist.
// The caller must keep data immutable, including while using a Result that
// retains the original bytes. Quality and cancellation follow File's contract.
func Bytes(ctx context.Context, data []byte, sourcePath string, options Options) (*Result, error) {
	absolutePath, err := validateRequest(ctx, sourcePath, options)
	if err != nil {
		return nil, err
	}
	return compressData(ctx, data, sourcePath, absolutePath, options, nil)
}

// BytesWithoutSource compresses an in-memory image snapshot (such as stdin)
// that has no backing source file path. The resulting Result can be saved to
// any new path via Save without colliding with a synthetic source path, and
// cannot overwrite in place.
func BytesWithoutSource(ctx context.Context, data []byte, options Options) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("压缩已取消：%w", err)
	}
	if options.Quality < 1 || options.Quality > 100 {
		return nil, fmt.Errorf("压缩质量必须在 1 到 100 之间")
	}
	if err := validateOptions(options); err != nil {
		return nil, err
	}
	options.SkipOverwriteSnapshot = true
	return compressData(ctx, data, "", "", options, nil)
}

func validateRequest(ctx context.Context, path string, options Options) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("压缩已取消：%w", err)
	}
	if options.Quality < 1 || options.Quality > 100 {
		return "", fmt.Errorf("压缩质量必须在 1 到 100 之间")
	}
	if err := validateOptions(options); err != nil {
		return "", err
	}
	if path == "" {
		return "", fmt.Errorf("图片路径不能为空")
	}
	if strings.IndexByte(path, 0) >= 0 {
		return "", fmt.Errorf("图片路径包含无效字符")
	}
	sourcePath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("无法解析图片路径：%w", err)
	}
	return sourcePath, nil
}

func validateOptions(options Options) error {
	if err := options.PNG.Validate(); err != nil {
		return err
	}
	if err := options.Metadata.Validate(); err != nil {
		return err
	}
	if err := options.OutputFormat.Validate(); err != nil {
		return err
	}
	if err := ValidateJPEGBackground(options.JPEGBackground); err != nil {
		return err
	}
	if err := ValidateResizeOptions(options.MaxDimension, options.MaxWidth, options.MaxHeight, options.ScalePercent); err != nil {
		return err
	}
	if err := ValidateTargetSizeBytes(options.TargetSizeBytes); err != nil {
		return err
	}
	if err := ValidateMaxDimension(options.TargetResizeMinDimension); err != nil {
		return fmt.Errorf("目标体积缩放的最小边长设置无效：%w", err)
	}
	return nil
}

func compressData(ctx context.Context, data []byte, path, sourcePath string, options Options, preview *image.Image) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("压缩已取消：%w", err)
	}
	if len(data) > maxInputSize {
		return nil, fmt.Errorf("图片文件超过 64 MiB 限制")
	}
	var (
		config       image.Config
		format       string
		err          error
		pixelSource  = data
		sourceOrient = 1
	)
	if SniffAVIFHeader(data) {
		pixelSource, format, err = decodeBMFFPNG(ctx, data, options.Threads)
		if err != nil {
			if cancelErr := ctx.Err(); cancelErr != nil {
				return nil, fmt.Errorf("压缩已取消：%w", cancelErr)
			}
			return nil, err
		}
		config, _, err = image.DecodeConfig(contextio.NewReader(ctx, bytes.NewReader(pixelSource)))
		if err != nil {
			return nil, fmt.Errorf("%s 解码输出无效：%w", strings.ToUpper(format), err)
		}
		if format == "heic" {
			sourceOrient = pngOrientation(pixelSource)
		}
	} else {
		config, format, err = image.DecodeConfig(contextio.NewReader(ctx, bytes.NewReader(data)))
		if err != nil {
			return nil, fmt.Errorf("无法识别或读取图片头部，仅支持静态 JPEG/PNG/WebP/HEIC/AVIF：%w", err)
		}
		if format != "jpeg" && format != "png" && format != "webp" {
			return nil, fmt.Errorf("不支持 %s 图片，仅支持静态 JPEG/PNG/WebP/HEIC/AVIF", format)
		}
		// Divide before multiplying so a malicious header cannot overflow.
		if config.Width <= 0 || config.Height <= 0 ||
			uint64(config.Width) > uint64(maxPixels)/uint64(config.Height) {
			return nil, fmt.Errorf("图片像素超过限制（最多 32,000,000 像素）或尺寸无效")
		}
		if format == "png" {
			if err := ValidatePNG(ctx, data); err != nil {
				return nil, err
			}
		}
		if format == "webp" {
			if err := ValidateWebP(ctx, data); err != nil {
				return nil, err
			}
		}
		if format == "jpeg" {
			sourceOrient = jpegOrientation(data)
		} else if format == "webp" {
			sourceOrient = webpOrientation(data)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("压缩已取消：%w", err)
	}
	targetFormat := ResolveOutputFormat(format, options.OutputFormat, options.Lossless)
	// Same-format PNG keeps its legacy raster orientation for the default
	// and long-edge modes. The newer modes use the displayed orientation.
	usePNGOrientation := targetFormat != format || options.MaxWidth > 0 || options.MaxHeight > 0 || options.ScalePercent > 0
	if format == "png" && usePNGOrientation {
		sourceOrient = pngOrientation(data)
	}
	jpegBackground, err := ParseJPEGBackground(options.JPEGBackground)
	if err != nil {
		return nil, err
	}
	source := compressionSource{
		data: data, pixelSource: pixelSource, config: config,
		format: format, targetFormat: targetFormat, orientation: sourceOrient,
		jpegBackground: jpegBackground,
	}
	width, height := source.displayDimensions()
	baselineWidth, baselineHeight, _ := resizeDimensionsForOptions(width, height, options)
	minDimension := options.TargetResizeMinDimension
	canTargetResize := !options.Lossless && options.TargetSizeBytes > 0 && minDimension > 0 && max(baselineWidth, baselineHeight) > minDimension
	baselinePreview := preview
	if canTargetResize {
		// The preview must come from the winning candidate.
		baselinePreview = nil
	}
	baseline, err := source.encodeAtDimension(ctx, options, baselinePreview)
	if err != nil {
		return nil, err
	}
	selected := baseline
	if canTargetResize && int64(len(baseline.data)) > options.TargetSizeBytes {
		if format == "png" && !usePNGOrientation {
			source.orientation = pngOrientation(data)
		}
		probeBase, needsRefine := lowestQualityOptions(options, targetFormat)
		source.cachePrepared = needsRefine
		var refine func(int) (*encodedImage, error)
		if needsRefine {
			refine = func(longEdge int) (*encodedImage, error) {
				refinedOptions := options
				refinedOptions.MaxDimension = longEdge
				refinedOptions.MaxWidth, refinedOptions.MaxHeight, refinedOptions.ScalePercent = 0, 0, 0
				return source.encodeAtDimension(ctx, refinedOptions, nil)
			}
		}
		bestFitEdge := 0
		selected, err = searchTargetResize(ctx, baseline, options.TargetSizeBytes, minDimension, func(longEdge int) (*encodedImage, error) {
			// Probe feasibility once at lowest quality so dimension search does
			// not run a nested target-size quality search at every candidate edge.
			probe := probeBase
			probe.MaxDimension = longEdge
			probe.MaxWidth, probe.MaxHeight, probe.ScalePercent = 0, 0, 0
			candidate, probeErr := source.encodeAtDimension(ctx, probe, nil)
			if probeErr == nil {
				if targetFormat != "png" {
					candidate.appliedQuality = probe.Quality
				}
				if int64(len(candidate.data)) <= options.TargetSizeBytes && longEdge > bestFitEdge {
					bestFitEdge = longEdge
					source.pinBestPrepared(candidate.width, candidate.height)
				}
			}
			return candidate, probeErr
		}, refine)
		source.clearPreparedCache()
		if err != nil {
			return nil, fmt.Errorf("压缩已取消：%w", err)
		}
	}
	result := &Result{
		SourcePath:      path,
		Format:          targetFormat,
		Width:           selected.width,
		Height:          selected.height,
		OriginalSize:    int64(len(data)),
		OutputSize:      int64(len(selected.data)),
		Data:            selected.data,
		Unchanged:       selected.unchanged,
		TargetSizeBytes: options.TargetSizeBytes,
		TargetResized:   selected != baseline,
		sourcePath:      sourcePath,
		sourceFormat:    format,
	}
	result.TargetSizeMissed = result.TargetSizeBytes > 0 && result.OutputSize > result.TargetSizeBytes
	if !result.Unchanged && selected.appliedQuality > 0 && selected.appliedQuality < options.Quality {
		result.AppliedQuality = selected.appliedQuality
	}
	if preview != nil && !result.Unchanged && targetFormat == "avif" {
		pngData, err := aviftools.DecodePNG(ctx, result.Data, options.Threads)
		if err == nil {
			*preview, _, err = image.Decode(contextio.NewReader(ctx, bytes.NewReader(pngData)))
		}
		if err != nil {
			return nil, fmt.Errorf("无法生成预览：%w", err)
		}
	}
	if !options.SkipOverwriteSnapshot {
		result.snapshot = captureOverwriteSnapshot(data, result.Data)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("压缩已取消：%w", err)
	}
	return result, nil
}

type cachedPreparedDim struct {
	width, height int
	orientation   int
	img           image.Image
	pngInput      []byte
	icc           []byte
}

type compressionSource struct {
	data, pixelSource    []byte
	config               image.Config
	format, targetFormat string
	orientation          int
	jpegBackground       color.RGBA
	pixels               image.Image

	// cachePrepared reuses the winning probe's resized raster / intermediate PNG
	// during target-resize refinement so CatmullRom scaling and PNG encoding are
	// not repeated for the exact same dimensions.
	cachePrepared bool
	lastPrepared  cachedPreparedDim
	bestPrepared  cachedPreparedDim
}

func (source *compressionSource) cachedResizedImage(width, height int) image.Image {
	if !source.cachePrepared {
		return nil
	}
	if source.bestPrepared.img != nil &&
		source.bestPrepared.width == width &&
		source.bestPrepared.height == height &&
		source.bestPrepared.orientation == source.orientation {
		return source.bestPrepared.img
	}
	if source.lastPrepared.img != nil &&
		source.lastPrepared.width == width &&
		source.lastPrepared.height == height &&
		source.lastPrepared.orientation == source.orientation {
		return source.lastPrepared.img
	}
	return nil
}

func (source *compressionSource) storeResizedImage(width, height int, img image.Image) {
	if !source.cachePrepared {
		return
	}
	source.lastPrepared = cachedPreparedDim{
		width:       width,
		height:      height,
		orientation: source.orientation,
		img:         img,
	}
}

func (source *compressionSource) cachedPreparedPNG(width, height int) ([]byte, []byte, bool) {
	if !source.cachePrepared {
		return nil, nil, false
	}
	if len(source.bestPrepared.pngInput) > 0 &&
		source.bestPrepared.width == width &&
		source.bestPrepared.height == height &&
		source.bestPrepared.orientation == source.orientation {
		return source.bestPrepared.pngInput, source.bestPrepared.icc, true
	}
	if len(source.lastPrepared.pngInput) > 0 &&
		source.lastPrepared.width == width &&
		source.lastPrepared.height == height &&
		source.lastPrepared.orientation == source.orientation {
		return source.lastPrepared.pngInput, source.lastPrepared.icc, true
	}
	return nil, nil, false
}

func (source *compressionSource) storePreparedPNG(width, height int, pngInput, icc []byte) {
	if !source.cachePrepared {
		return
	}
	source.lastPrepared = cachedPreparedDim{
		width:       width,
		height:      height,
		orientation: source.orientation,
		pngInput:    pngInput,
		icc:         icc,
	}
}

func (source *compressionSource) pinBestPrepared(width, height int) {
	if !source.cachePrepared {
		return
	}
	if source.lastPrepared.width == width &&
		source.lastPrepared.height == height &&
		source.lastPrepared.orientation == source.orientation {
		source.bestPrepared = source.lastPrepared
	}
}

func (source *compressionSource) clearPreparedCache() {
	source.cachePrepared = false
	source.lastPrepared = cachedPreparedDim{}
	source.bestPrepared = cachedPreparedDim{}
}

// decodePixels reuses the source pixels across all dimension probes. JPEG
// also reuses the orientation and alpha preparation required by its encoder.
func (source *compressionSource) decodePixels(ctx context.Context) (image.Image, error) {
	if source.pixels != nil {
		return source.pixels, nil
	}
	img, _, err := image.Decode(contextio.NewReader(ctx, bytes.NewReader(source.pixelSource)))
	if cancelErr := ctx.Err(); cancelErr != nil {
		return nil, fmt.Errorf("压缩已取消：%w", cancelErr)
	}
	if err != nil {
		return nil, fmt.Errorf("图片数据损坏或无法解码：%w", err)
	}
	if source.targetFormat == "jpeg" {
		img, err = orientForJPEGEncode(ctx, img, source.orientation)
		if err != nil {
			return nil, fmt.Errorf("调整图片方向已取消：%w", err)
		}
		if source.format != source.targetFormat {
			img, err = flattenAlphaForJPEG(ctx, img, source.jpegBackground)
			if err != nil {
				return nil, fmt.Errorf("处理图片透明度已取消：%w", err)
			}
		}
	}
	source.pixels = img
	return img, nil
}

type encodedImage struct {
	data           []byte
	width, height  int
	appliedQuality int
	unchanged      bool
}

func (source *compressionSource) displayDimensions() (int, int) {
	width, height := source.config.Width, source.config.Height
	if source.orientation >= 5 && source.orientation <= 8 {
		return height, width
	}
	return width, height
}

func (source *compressionSource) encodeAtDimension(ctx context.Context, options Options, preview *image.Image) (*encodedImage, error) {
	data, pixelSource, config := source.data, source.pixelSource, source.config
	sourceFormat, targetFormat, sourceOrient := source.format, source.targetFormat, source.orientation
	converting := targetFormat != sourceFormat
	width, height := source.displayDimensions()
	resizedWidth, resizedHeight, needsResize := resizeDimensionsForOptions(width, height, options)
	transforming := converting || needsResize
	forcedOutput := false

	var (
		encoded        []byte
		appliedQuality int
		err            error
	)
	switch targetFormat {
	case "avif":
		if !aviftools.Available() {
			return nil, aviftools.ErrUnsupportedPlatform
		}
		mode := avifMetadataMode(options.Metadata)
		if !transforming && mode != aviftools.MetadataAll {
			forcedOutput, err = aviftools.HasStrippableMetadata(ctx, data, mode)
			if err != nil {
				break
			}
		}
		pngInput, icc, prepErr := source.prepareAVIFPNG(ctx, needsResize, options)
		if prepErr != nil {
			err = prepErr
			break
		}
		width, height = resizedWidth, resizedHeight
		encoded, appliedQuality, err = encodeWithTargetSize(ctx, options, func(quality int) ([]byte, error) {
			return aviftools.EncodePNG(ctx, pngInput, aviftools.EncodeOptions{
				Quality:  quality,
				Lossless: options.Lossless,
				Metadata: mode,
				ICC:      icc,
				Threads:  options.Threads,
			})
		})
		if err == nil {
			err = validateAVIFOutput(ctx, encoded, width, height, mode)
		}
	case "webp":
		needsWebPReencodeFromPixels := converting || needsResize ||
			(sourceFormat == "webp" && sourceOrient > 1 && options.Metadata != MetadataKeepAll)
		if needsWebPReencodeFromPixels {
			var resized image.Image
			if needsResize {
				resized = source.cachedResizedImage(resizedWidth, resizedHeight)
			}
			if resized == nil {
				decoded, decodeErr := source.decodePixels(ctx)
				if decodeErr != nil {
					return nil, decodeErr
				}
				oriented := normalizeOrientation(decoded, sourceOrient)
				var resizeErr error
				resized, resizeErr = resizeImage(ctx, oriented, options)
				if resizeErr != nil {
					return nil, fmt.Errorf("缩放图片已取消：%w", resizeErr)
				}
				if needsResize {
					source.storeResizedImage(resizedWidth, resizedHeight, resized)
				}
			}
			outBounds := resized.Bounds()
			width, height = outBounds.Dx(), outBounds.Dy()
			var icc []byte
			if options.Metadata != MetadataStrip {
				switch sourceFormat {
				case "jpeg":
					icc = extractJPEGICCProfile(data, 3)
				case "png", "heic", "avif":
					icc = extractPNGICCProfile(pixelSource)
				case "webp":
					icc = extractWebPICCProfile(data)
				}
			}
			encodePreparedWebP := func(quality int) ([]byte, error) {
				out, err := webptools.EncodeImage(ctx, resized, webptools.EncodeOptions{
					Quality:  quality,
					Lossless: options.Lossless,
					Threads:  options.Threads,
				})
				if err == nil && len(icc) > 0 {
					out = injectWebPICC(out, icc, width, height)
				}
				return out, err
			}
			encoded, appliedQuality, err = encodeWithTargetSize(ctx, options, encodePreparedWebP)
			if err == nil {
				outConfig := image.Config{Width: width, Height: height}
				if preview != nil {
					var outDecoded image.Image
					outDecoded, err = decodeWebPOutput(ctx, encoded, outConfig)
					if err == nil {
						*preview = outDecoded
					}
				} else {
					err = validateWebPStructureAndDimensions(ctx, encoded, outConfig)
				}
				forcedOutput = true
			}
		} else {
			mustStrip := options.Metadata != MetadataKeepAll && webpHasStrippableMetadata(data, options.Metadata)
			if config.Width > 16383 || config.Height > 16383 {
				if mustStrip {
					return nil, fmt.Errorf("WebP 图片单边超过 16383 像素，无法移除元数据")
				}
				if _, _, decodeErr := image.Decode(contextio.NewReader(ctx, bytes.NewReader(data))); decodeErr != nil {
					return nil, fmt.Errorf("图片数据损坏或无法解码：%w", decodeErr)
				}
				encoded = data
			} else {
				encodeWebP := func(quality int) ([]byte, error) {
					return webptools.EncodeWithOptions(ctx, data, webptools.EncodeOptions{
						Quality:     quality,
						Lossless:    options.Lossless,
						Metadata:    webpMetadataMode(options.Metadata),
						Threads:     options.Threads,
						SmallerOnly: !mustStrip,
					})
				}
				encoded, appliedQuality, err = encodeWithTargetSize(ctx, options, encodeWebP)
				if err == nil {
					useEncoded := mustStrip || len(encoded) < len(data)
					if useEncoded {
						forcedOutput = mustStrip
						if preview != nil {
							var decoded image.Image
							decoded, err = decodeWebPOutput(ctx, encoded, config)
							if err == nil {
								*preview = normalizeOrientation(decoded, webpOrientation(encoded))
							}
						} else {
							err = validateWebPStructureAndDimensions(ctx, encoded, config)
						}
					} else if _, _, decodeErr := image.Decode(contextio.NewReader(ctx, bytes.NewReader(data))); decodeErr != nil {
						return nil, fmt.Errorf("图片数据损坏或无法解码：%w", decodeErr)
					}
				}
			}
			if err == nil {
				selectedBytes := data
				if forcedOutput || len(encoded) < len(data) {
					selectedBytes = encoded
				}
				width, height = config.Width, config.Height
				if orient := webpOrientation(selectedBytes); orient >= 5 && orient <= 8 {
					width, height = height, width
				}
			}
		}
	case "jpeg":
		if options.Lossless && !transforming {
			if _, _, decodeErr := image.Decode(contextio.NewReader(ctx, bytes.NewReader(data))); decodeErr != nil {
				if cancelErr := ctx.Err(); cancelErr != nil {
					return nil, fmt.Errorf("压缩已取消：%w", cancelErr)
				}
				return nil, fmt.Errorf("图片数据损坏或无法解码：%w", decodeErr)
			}
			mustStrip := options.Metadata != MetadataKeepAll && jpegHasStrippableMetadata(data, options.Metadata)
			if jpegtoolsAvailable() {
				orientTransform := 0
				if sourceOrient > 1 && options.Metadata != MetadataKeepAll {
					orientTransform = sourceOrient
				}
				var optimized []byte
				optimized, err = jpegtools.OptimizeLossless(ctx, data, jpegtools.OptimizeOptions{
					Copy:        jpegCopyMode(options.Metadata),
					Orientation: orientTransform,
					Perfect:     orientTransform > 1,
					SmallerOnly: !mustStrip,
				})
				if err != nil {
					if errors.Is(err, jpegtools.ErrImperfectTransform) {
						return nil, fmt.Errorf("无损模式下该旋转 JPEG 的边长未对齐 MCU 块，无法无损移除 EXIF 方向；请保留全部元数据或关闭无损模式")
					}
				} else if mustStrip || len(optimized) < len(data) {
					outCfg, outFmt, cfgErr := image.DecodeConfig(contextio.NewReader(ctx, bytes.NewReader(optimized)))
					if cfgErr != nil || outFmt != "jpeg" {
						err = fmt.Errorf("MozJPEG 输出无效：%v", cfgErr)
					} else {
						outW, outH := outCfg.Width, outCfg.Height
						if outOrient := jpegOrientation(optimized); outOrient >= 5 && outOrient <= 8 {
							outW, outH = outH, outW
						}
						if outW != width || outH != height {
							err = fmt.Errorf("MozJPEG 输出尺寸异常（%dx%d != %dx%d）", outW, outH, width, height)
						} else {
							encoded = optimized
							forcedOutput = mustStrip
						}
					}
				} else {
					encoded = data
				}
			} else if mustStrip {
				return nil, fmt.Errorf("当前平台未捆绑 MozJPEG，无法在无损模式下移除 JPEG 元数据；请保留全部元数据或关闭无损模式")
			} else {
				encoded = data
			}
		} else {
			var img image.Image
			if needsResize {
				img = source.cachedResizedImage(resizedWidth, resizedHeight)
			}
			if img == nil {
				var decodeErr error
				img, decodeErr = source.decodePixels(ctx)
				if decodeErr != nil {
					return nil, decodeErr
				}
				if needsResize {
					img, err = resizeImage(ctx, img, options)
					if err != nil {
						return nil, fmt.Errorf("缩放图片已取消：%w", err)
					}
					source.storeResizedImage(resizedWidth, resizedHeight, img)
				}
			}
			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("压缩已取消：%w", err)
			}
			width, height = img.Bounds().Dx(), img.Bounds().Dy()
			outputComponents := 3
			if _, isGray := img.(*image.Gray); isGray {
				outputComponents = 1
			}
			var iccSegments [][]byte
			var iccBytes int
			if options.Metadata != MetadataStrip {
				switch sourceFormat {
				case "jpeg":
					iccSegments, iccBytes = jpegCompatibleICCSegments(data, outputComponents)
				case "png", "heic", "avif":
					icc := extractPNGICCProfileForComponents(pixelSource, outputComponents)
					iccSegments, iccBytes = buildJPEGICCSegments(icc, outputComponents)
				case "webp":
					iccSegments, iccBytes = buildJPEGICCSegments(extractWebPICCProfile(data), outputComponents)
				}
			}
			mustStrip := !converting && options.Metadata != MetadataKeepAll && jpegHasStrippableMetadata(data, options.Metadata)
			forcedOutput = transforming || mustStrip
			encoded, appliedQuality, err = encodeWithTargetSize(ctx, options, func(quality int) ([]byte, error) {
				return encodePreparedJPEG(ctx, img, data, iccSegments, iccBytes, quality, forcedOutput)
			})
		}
	default: // png
		pngInput := pixelSource
		pngConfig := config
		// Bake any selected EXIF orientation into the pixels before PNG
		// optimization, which can remove the orientation tag.
		needsPNGReencode := sourceFormat == "jpeg" || sourceFormat == "webp" || needsResize || sourceOrient > 1
		if needsPNGReencode {
			var (
				cachedPNG []byte
				cacheHit  bool
			)
			if needsResize {
				cachedPNG, _, cacheHit = source.cachedPreparedPNG(resizedWidth, resizedHeight)
			}
			if cacheHit {
				pngInput = cachedPNG
				width, height = resizedWidth, resizedHeight
				pngConfig = image.Config{Width: width, Height: height}
				forcedOutput = true
			} else {
				decoded, decodeErr := source.decodePixels(ctx)
				if decodeErr != nil {
					return nil, decodeErr
				}
				oriented := normalizeOrientation(decoded, sourceOrient)
				resized, resizeErr := resizeImage(ctx, oriented, options)
				if resizeErr != nil {
					return nil, fmt.Errorf("缩放图片已取消：%w", resizeErr)
				}
				var colorChunks [][]byte
				if options.Metadata != MetadataStrip {
					switch sourceFormat {
					case "png", "heic", "avif":
						colorChunks = extractPNGColorChunks(pixelSource)
					case "jpeg", "webp":
						components := 3
						switch resized.ColorModel() {
						case color.GrayModel, color.Gray16Model:
							components = 1
						}
						var icc []byte
						if sourceFormat == "jpeg" {
							icc = extractJPEGICCProfile(data, components)
						} else {
							icc = extractWebPICCProfile(data)
						}
						if chunk := buildPNGICCPChunk(icc, components); len(chunk) > 0 {
							colorChunks = [][]byte{chunk}
						}
					}
				}
				pngInput, err = encodeResizedPNG(ctx, resized, colorChunks)
				if err != nil {
					return nil, fmt.Errorf("图片编码失败：%w", err)
				}
				outBounds := resized.Bounds()
				width, height = outBounds.Dx(), outBounds.Dy()
				pngConfig = image.Config{Width: width, Height: height}
				forcedOutput = true
				if needsResize {
					source.storePreparedPNG(width, height, pngInput, nil)
				}
			}
		}
		if converting {
			forcedOutput = true
		}
		var stripped bool
		encoded, stripped, err = compressPNG(ctx, pngInput, pngConfig, options, preview, transforming || needsPNGReencode)
		if err == nil && len(encoded) > maxInputSize {
			err = fmt.Errorf("输出超过 64 MiB 限制")
		}
		if stripped {
			forcedOutput = true
		}
	}
	if cancelErr := ctx.Err(); cancelErr != nil {
		return nil, fmt.Errorf("压缩已取消：%w", cancelErr)
	}
	if err != nil {
		return nil, fmt.Errorf("图片编码失败：%w", err)
	}
	unchanged := !transforming && !forcedOutput && len(encoded) >= len(data)
	if unchanged {
		encoded = data
	}
	return &encodedImage{
		data: encoded, width: width, height: height,
		appliedQuality: appliedQuality, unchanged: unchanged,
	}, nil
}

func pngStripMode(policy MetadataPolicy) pngtools.StripMode {
	switch policy {
	case MetadataKeepICC:
		return pngtools.StripSafe
	case MetadataStrip:
		return pngtools.StripAll
	default:
		return pngtools.StripNone
	}
}

func webpMetadataMode(policy MetadataPolicy) webptools.MetadataMode {
	switch policy {
	case MetadataKeepICC:
		return webptools.MetadataICC
	case MetadataStrip:
		return webptools.MetadataNone
	default:
		return webptools.MetadataAll
	}
}

func jpegCopyMode(policy MetadataPolicy) jpegtools.CopyMode {
	switch policy {
	case MetadataKeepICC:
		return jpegtools.CopyICC
	case MetadataStrip:
		return jpegtools.CopyNone
	default:
		return jpegtools.CopyAll
	}
}

func encodePreparedJPEG(ctx context.Context, img image.Image, data []byte, iccSegments [][]byte, iccBytes, quality int, forcedOutput bool) ([]byte, error) {
	limit := len(data) - iccBytes
	if forcedOutput {
		limit = maxInputSize - iccBytes + 1
	}
	if limit <= 2 {
		return data, nil
	}
	var raw []byte
	if jpegtools.Available() {
		maxOut := 0
		if !forcedOutput {
			maxOut = limit
		}
		encoded, err := jpegtools.EncodeImage(ctx, img, jpegtools.EncodeOptions{
			Quality:        quality,
			MaxOutputBytes: maxOut,
		})
		if !forcedOutput && errors.Is(err, jpegtools.ErrNoSavings) {
			return data, nil
		}
		if err != nil {
			return nil, err
		}
		if len(encoded)+iccBytes > maxInputSize {
			return nil, fmt.Errorf("输出超过 64 MiB 限制")
		}
		outCfg, outFmt, err := image.DecodeConfig(contextio.NewReader(ctx, bytes.NewReader(encoded)))
		if err != nil || outFmt != "jpeg" {
			return nil, fmt.Errorf("MozJPEG 输出无效：%v", err)
		}
		bounds := img.Bounds()
		if outCfg.Width != bounds.Dx() || outCfg.Height != bounds.Dy() {
			return nil, fmt.Errorf("MozJPEG 输出尺寸异常（%dx%d != %dx%d）", outCfg.Width, outCfg.Height, bounds.Dx(), bounds.Dy())
		}
		raw = encoded
	} else {
		output := jpegOutput{ctx: ctx, limit: limit, stopOnDiscard: !forcedOutput}
		err := jpeg.Encode(&output, img, &jpeg.Options{Quality: quality})
		if errors.Is(err, errJPEGNoSavings) {
			return data, nil
		}
		if err != nil {
			return nil, err
		}
		if output.discarded {
			return nil, fmt.Errorf("输出超过 64 MiB 限制")
		}
		raw = output.buf.Bytes()
	}
	return injectJPEGICCSegments(raw, iccSegments, iccBytes), nil
}

var pngTargetSizeTiers = [...]PNGOptions{
	{MinQuality: 0, MaxQuality: 85, Colors: 256, Speed: 3},
	{MinQuality: 0, MaxQuality: 70, Colors: 192, Speed: 3},
	{MinQuality: 0, MaxQuality: 55, Colors: 128, Speed: 4},
	{MinQuality: 0, MaxQuality: 40, Colors: 64, Speed: 4},
	{MinQuality: 0, MaxQuality: 25, Colors: 32, Speed: 5},
	{MinQuality: 0, MaxQuality: 15, Colors: 16, Speed: 5},
	{MinQuality: 0, MaxQuality: 5, Colors: 8, Speed: 5},
	{MinQuality: 0, MaxQuality: 1, Colors: 4, Speed: 6},
}

func pngTargetSizeTiersFor(options PNGOptions) []PNGOptions {
	baseline := options
	if baseline == (PNGOptions{}) {
		baseline = DefaultPNGOptions()
	}
	tiers := make([]PNGOptions, 0, len(pngTargetSizeTiers))
	for _, tier := range pngTargetSizeTiers {
		if tier == baseline || tier.MaxQuality > baseline.MaxQuality || tier.Colors > baseline.Colors {
			continue
		}
		tiers = append(tiers, tier)
	}
	return tiers
}

func lowestQualityOptions(options Options, targetFormat string) (Options, bool) {
	probe := options
	probe.TargetSizeBytes = 0
	if targetFormat == "png" {
		tiers := pngTargetSizeTiersFor(options.PNG)
		if len(tiers) == 0 {
			return probe, false
		}
		probe.PNG = tiers[len(tiers)-1]
		return probe, true
	}
	probe.Quality = 1
	return probe, options.Quality > 1
}

type pngCandidate struct {
	data      []byte
	stripped  bool
	quantized bool
	oversized bool
}

func compressPNG(ctx context.Context, data []byte, config image.Config, options Options, preview *image.Image, transformed bool) ([]byte, bool, error) {
	return compressPNGWithEncoder(ctx, data, config, options, preview, transformed, encodePNGCandidate)
}

func compressPNGWithEncoder(ctx context.Context, data []byte, config image.Config, options Options, preview *image.Image, transformed bool, encode func(context.Context, []byte, image.Config, Options, [][]byte) (pngCandidate, error)) ([]byte, bool, error) {
	var colorChunks [][]byte
	if transformed && options.Metadata != MetadataStrip {
		colorChunks = extractPNGColorChunks(data)
	}
	target := options.TargetSizeBytes
	targetSearchEligible := target > 0 && !options.Lossless
	candidate, err := encode(ctx, data, config, options, colorChunks)
	var limitErr error
	if err != nil {
		if !targetSearchEligible || !errors.Is(err, pngtools.ErrOutputLimit) {
			return nil, false, err
		}
		limitErr = err
		candidate = pngCandidate{oversized: true}
	}
	if targetSearchEligible && (candidate.oversized || int64(len(candidate.data)) > target) &&
		(transformed || candidate.stripped || candidate.oversized || int64(len(data)) > target) {
		tiers := pngTargetSizeTiersFor(options.PNG)
		candidate, _, err = searchTargetSize(ctx, candidate, len(tiers)+1, target, func(out pngCandidate) int {
			if out.oversized {
				return maxInputSize + 1
			}
			return len(out.data)
		}, func(rank int) (pngCandidate, error) {
			probe := options
			probe.PNG = tiers[len(tiers)-rank]
			out, probeErr := encode(ctx, data, config, probe, colorChunks)
			if errors.Is(probeErr, pngtools.ErrOutputLimit) {
				limitErr = probeErr
				return pngCandidate{oversized: true}, nil
			}
			return out, probeErr
		})
		if err != nil {
			return nil, false, err
		}
	}
	if candidate.oversized {
		return nil, false, limitErr
	}
	if !transformed && !candidate.stripped && sameBytesBacking(candidate.data, data) {
		if _, _, err := image.Decode(contextio.NewReader(ctx, bytes.NewReader(data))); err != nil {
			return nil, false, fmt.Errorf("图片数据损坏或无法解码：%w", err)
		}
		return data, false, nil
	}
	if preview != nil {
		*preview, err = decodePNGOutput(ctx, candidate.data, config)
	} else {
		err = validatePNGStructureAndCRCs(ctx, candidate.data, config)
	}
	if err != nil {
		if candidate.quantized {
			return nil, false, fmt.Errorf("pngquant 输出无效：%w", err)
		}
		return nil, false, fmt.Errorf("Oxipng 输出无效：%w", err)
	}
	return candidate.data, candidate.stripped, nil
}

func encodePNGCandidate(ctx context.Context, data []byte, config image.Config, options Options, colorChunks [][]byte) (pngCandidate, error) {
	candidate := data
	var quantized []byte
	if !options.Lossless {
		var err error
		quantized, err = pngtools.QuantizeWithStrip(ctx, data, options.PNG, options.Threads, options.Metadata == MetadataStrip)
		if err != nil {
			return pngCandidate{}, err
		}
		// pngquant can convert profiled pixels to sRGB and discard their ICC.
		if len(quantized) < len(candidate) &&
			(len(colorChunks) == 0 || slices.EqualFunc(colorChunks, extractPNGColorChunks(quantized), bytes.Equal)) {
			candidate = quantized
		}
	}
	stripMode := pngStripMode(options.Metadata)
	mustStrip, err := pngtools.HasStrippableMetadata(ctx, candidate, stripMode)
	if err != nil {
		return pngCandidate{}, err
	}
	optimized, err := pngtools.OptimizeWithOptions(ctx, candidate, pngtools.OptimizeOptions{
		Threads:     options.Threads,
		Strip:       stripMode,
		AllowLarger: mustStrip,
	})
	if err != nil {
		if !sameBytesBacking(candidate, data) {
			if validateErr := validatePNGOutput(ctx, candidate, config); validateErr != nil {
				return pngCandidate{}, fmt.Errorf("pngquant 输出无效：%w", validateErr)
			}
		}
		return pngCandidate{}, err
	}
	if mustStrip || len(optimized) < len(candidate) {
		candidate = optimized
	}
	return pngCandidate{
		data:      candidate,
		stripped:  mustStrip,
		quantized: sameBytesBacking(candidate, quantized),
	}, nil
}

func sameBytesBacking(a, b []byte) bool {
	return len(a) == len(b) && (len(a) == 0 || &a[0] == &b[0])
}

func validatePNGOutput(ctx context.Context, data []byte, input image.Config) error {
	_, err := decodePNGOutput(ctx, data, input)
	return err
}

func decodePNGOutput(ctx context.Context, data []byte, input image.Config) (image.Image, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data) > maxInputSize {
		return nil, fmt.Errorf("输出为空或超过 64 MiB 限制")
	}
	config, format, err := image.DecodeConfig(contextio.NewReader(ctx, bytes.NewReader(data)))
	if err != nil {
		return nil, fmt.Errorf("无法读取 PNG 头部：%w", err)
	}
	if format != "png" || config.Width != input.Width || config.Height != input.Height {
		return nil, fmt.Errorf("输出格式或图片尺寸发生了变化")
	}
	if err := ValidatePNG(ctx, data); err != nil {
		return nil, err
	}
	decoded, _, err := image.Decode(contextio.NewReader(ctx, bytes.NewReader(data)))
	if err != nil {
		return nil, fmt.Errorf("PNG 数据损坏：%w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return decoded, nil
}

// readInput enforces the limit on bytes actually read, even if the file grows
// after Stat. It also closes the input on every exit path.
func readInput(ctx context.Context, path string) ([]byte, error) {
	return imageinput.Read(ctx, path)
}

// Save creates a new file and never overwrites an existing path. Failed writes
// or closes remove the file created by this call.
func (r *Result) Save(path string) error {
	return r.save(path, nil)
}

// SaveInRoot creates a new file relative to an open directory. It keeps the
// write and failure cleanup in that directory if the directory's path changes.
func (r *Result) SaveInRoot(root *os.Root, name string) error {
	return r.save(name, root)
}

func (r *Result) save(path string, root *os.Root) (err error) {
	if r == nil || len(r.Data) == 0 {
		return fmt.Errorf("没有可保存的压缩结果")
	}
	if path == "" {
		return fmt.Errorf("保存路径不能为空")
	}
	openFile, remove := os.OpenFile, os.Remove
	absolutePath := path
	if root != nil {
		openFile, remove = root.OpenFile, root.Remove
		absolutePath = filepath.Join(root.Name(), path)
	}
	destination, err := filepath.Abs(absolutePath)
	if err != nil {
		return fmt.Errorf("无法解析保存路径：%w", err)
	}
	source := r.sourcePath
	if source == "" && r.SourcePath != "" {
		source, err = filepath.Abs(r.SourcePath)
		if err != nil {
			return fmt.Errorf("无法解析源文件路径：%w", err)
		}
	}
	if destination == source {
		return fmt.Errorf("不能覆盖源图片，请选择新的保存路径")
	}
	if root == nil {
		path = destination
	}
	f, err := openFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("无法创建输出文件（不能覆盖已有文件）：%w", err)
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("关闭输出文件失败：%w", closeErr)
		}
		if err != nil {
			if removeErr := remove(path); removeErr != nil {
				err = fmt.Errorf("%w；清理未完成的输出文件失败：%v", err, removeErr)
			}
		}
	}()
	n, err := f.Write(r.Data)
	if err != nil {
		return fmt.Errorf("写入输出文件失败：%w", err)
	}
	if n != len(r.Data) {
		return fmt.Errorf("写入输出文件失败：%w", io.ErrShortWrite)
	}
	return nil
}
