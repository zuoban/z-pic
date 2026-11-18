// Package compress compresses static JPEG, PNG and WebP images without scaling.
// JPEG pixels are normalized to their EXIF display orientation.
package compress

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // Register the PNG decoder; encoding is handled by native tools.
	"io"
	"os"
	"path/filepath"
	"strings"

	_ "golang.org/x/image/webp"

	"zpic/internal/contextio"
	"zpic/internal/imageinput"
	"zpic/internal/pngtools"
	"zpic/internal/webptools"
)

const (
	maxInputSize = imageinput.MaxSize
	maxPixels    = 32_000_000
)

type Options struct {
	Quality int // JPEG and WebP quality in [1, 100].
	PNG     PNGOptions
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
	SourcePath   string
	Format       string
	Width        int
	Height       int
	OriginalSize int64
	OutputSize   int64
	Data         []byte
	Unchanged    bool

	sourcePath string             // Absolute path captured before the caller can change cwd.
	snapshot   *overwriteSnapshot // Non-nil only for a validated File/Bytes result.
}

// File reads and compresses an image. Quality must be in [1, 100], including
// for PNG inputs. PNG's zero value uses the balanced quantization defaults.
// Decoding and JPEG encoding check cancellation on reads/writes.
// Native PNG/WebP processes are canceled with ctx.
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

func validateRequest(ctx context.Context, path string, options Options) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("压缩已取消：%w", err)
	}
	if options.Quality < 1 || options.Quality > 100 {
		return "", fmt.Errorf("压缩质量必须在 1 到 100 之间")
	}
	if err := options.PNG.Validate(); err != nil {
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

func compressData(ctx context.Context, data []byte, path, sourcePath string, options Options, preview *image.Image) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("压缩已取消：%w", err)
	}
	if len(data) > maxInputSize {
		return nil, fmt.Errorf("图片文件超过 64 MiB 限制")
	}
	config, format, err := image.DecodeConfig(contextio.NewReader(ctx, bytes.NewReader(data)))
	if err != nil {
		return nil, fmt.Errorf("无法识别或读取图片头部，仅支持静态 JPEG/PNG/WebP：%w", err)
	}
	if format != "jpeg" && format != "png" && format != "webp" {
		return nil, fmt.Errorf("不支持 %s 图片，仅支持静态 JPEG/PNG/WebP", format)
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
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("压缩已取消：%w", err)
	}
	width, height := config.Width, config.Height
	var encoded []byte
	if format == "jpeg" {
		img, _, err := image.Decode(contextio.NewReader(ctx, bytes.NewReader(data)))
		if cancelErr := ctx.Err(); cancelErr != nil {
			return nil, fmt.Errorf("压缩已取消：%w", cancelErr)
		}
		if err != nil {
			return nil, fmt.Errorf("图片数据损坏或无法解码：%w", err)
		}
		img, err = orientForJPEGEncode(ctx, img, jpegOrientation(data))
		if err != nil {
			return nil, fmt.Errorf("调整图片方向已取消：%w", err)
		}
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("压缩已取消：%w", err)
		}
		width, height = img.Bounds().Dx(), img.Bounds().Dy()
		iccSegments, iccBytes := jpegICCSegments(data)
		output := jpegOutput{ctx: ctx, limit: len(data) - iccBytes, stopOnDiscard: true}
		if output.limit <= 2 {
			output.discarded = true
		} else {
			err = jpeg.Encode(&output, img, &jpeg.Options{Quality: options.Quality})
			if errors.Is(err, errJPEGNoSavings) {
				err = nil
			}
		}
		if err == nil {
			if output.discarded {
				encoded = data
			} else {
				encoded = injectJPEGICCSegments(output.buf.Bytes(), iccSegments, iccBytes)
			}
		}
	} else if format == "webp" {
		if config.Width > 16383 || config.Height > 16383 {
			if _, _, decodeErr := image.Decode(contextio.NewReader(ctx, bytes.NewReader(data))); decodeErr != nil {
				return nil, fmt.Errorf("图片数据损坏或无法解码：%w", decodeErr)
			}
			encoded = data
		} else {
			encoded, err = webptools.EncodeSmaller(ctx, data, options.Quality)
			if err == nil {
				if len(encoded) < len(data) {
					var decoded image.Image
					decoded, err = decodeWebPOutput(ctx, encoded, config)
					if err == nil && preview != nil {
						*preview = decoded
					}
				} else if _, _, decodeErr := image.Decode(contextio.NewReader(ctx, bytes.NewReader(data))); decodeErr != nil {
					return nil, fmt.Errorf("图片数据损坏或无法解码：%w", decodeErr)
				}
			}
		}
	} else {
		encoded, err = compressPNG(ctx, data, config, options.PNG, preview)
	}
	if cancelErr := ctx.Err(); cancelErr != nil {
		return nil, fmt.Errorf("压缩已取消：%w", cancelErr)
	}
	if err != nil {
		return nil, fmt.Errorf("图片编码失败：%w", err)
	}
	result := &Result{
		SourcePath: path, Format: format,
		Width: width, Height: height,
		OriginalSize: int64(len(data)),
		Data:         encoded,
		sourcePath:   sourcePath,
	}
	if len(result.Data) >= len(data) {
		result.Data = data
		result.Unchanged = true
	}
	result.OutputSize = int64(len(result.Data))
	result.snapshot = captureOverwriteSnapshot(data, result.Data)
	return result, nil
}

func compressPNG(ctx context.Context, data []byte, config image.Config, options PNGOptions, preview *image.Image) ([]byte, error) {
	candidate := data
	quantized, err := pngtools.QuantizeWithOptions(ctx, data, options)
	if err != nil {
		return nil, err
	}
	if len(quantized) < len(candidate) {
		candidate = quantized
	}
	optimized, err := pngtools.Optimize(ctx, candidate)
	if err != nil {
		if !sameBytesBacking(candidate, data) {
			if validateErr := validatePNGOutput(ctx, candidate, config); validateErr != nil {
				return nil, fmt.Errorf("pngquant 输出无效：%w", validateErr)
			}
		}
		return nil, err
	}
	if len(optimized) < len(candidate) {
		candidate = optimized
	}
	if sameBytesBacking(candidate, data) {
		if _, _, decodeErr := image.Decode(contextio.NewReader(ctx, bytes.NewReader(data))); decodeErr != nil {
			return nil, fmt.Errorf("图片数据损坏或无法解码：%w", decodeErr)
		}
		return candidate, nil
	}
	decoded, err := decodePNGOutput(ctx, candidate, config)
	if err != nil {
		if sameBytesBacking(candidate, quantized) {
			return nil, fmt.Errorf("pngquant 输出无效：%w", err)
		}
		return nil, fmt.Errorf("Oxipng 输出无效：%w", err)
	}
	if preview != nil {
		*preview = decoded
	}
	return candidate, nil
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
func (r *Result) Save(path string) (err error) {
	if r == nil || len(r.Data) == 0 {
		return fmt.Errorf("没有可保存的压缩结果")
	}
	if path == "" {
		return fmt.Errorf("保存路径不能为空")
	}
	destination, err := filepath.Abs(path)
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
	f, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("无法创建输出文件（不能覆盖已有文件）：%w", err)
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("关闭输出文件失败：%w", closeErr)
		}
		if err != nil {
			if removeErr := os.Remove(destination); removeErr != nil {
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
