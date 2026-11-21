// Package aviftools inspects static AVIF files and runs the bundled libavif tools.
package aviftools

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"zpic/internal/contextio"
	"zpic/internal/imageinput"
	"zpic/internal/toolcache"
	"zpic/internal/toolio"
)

const toolTimeout = 2 * time.Minute

var (
	userCacheDir = os.UserCacheDir
	toolSlots    = map[string]*toolcache.Slot{
		"avifenc": {},
		"avifdec": {},
	}
)

// Available reports whether both native libavif executables are bundled.
func Available() bool {
	return len(bundledTool("avifenc")) > 0 && len(bundledTool("avifdec")) > 0
}

// MetadataMode controls what avifenc preserves from its PNG input.
type MetadataMode string

const (
	MetadataAll  MetadataMode = ""
	MetadataICC  MetadataMode = "icc"
	MetadataNone MetadataMode = "none"
)

type EncodeOptions struct {
	Quality  int // 1–100 in lossy mode.
	Lossless bool
	Speed    int // 1–10, or <= 0 for the default (6).
	Metadata MetadataMode
	ICC      []byte // Optional override for the input PNG's ICC profile.
	Threads  int    // <= 0 selects the current Go parallelism limit.
}

// EncodePNG encodes a PNG into a static AVIF using avifenc.
func EncodePNG(ctx context.Context, pngData []byte, options EncodeOptions) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("AVIF 编码已取消：%w", err)
	}
	if len(pngData) == 0 || len(pngData) > maxAVIFBytes {
		return nil, fmt.Errorf("AVIF 编码输入为空或超过 64 MiB 限制")
	}
	args, err := encodeArgs(options)
	if err != nil {
		return nil, err
	}
	cfg, err := png.DecodeConfig(contextio.NewReader(ctx, bytes.NewReader(pngData)))
	if err != nil {
		return nil, fmt.Errorf("无法读取 AVIF 编码所需的 PNG 输入：%w", err)
	}
	if !validDimensions(cfg.Width, cfg.Height) {
		return nil, fmt.Errorf("AVIF 编码输入超过 3200 万像素限制或尺寸无效")
	}
	if !Available() {
		return nil, ErrUnsupportedPlatform
	}

	dir, err := makeTempDir()
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	if len(options.ICC) > 0 && options.Metadata != MetadataNone {
		iccPath := filepath.Join(dir, "profile.icc")
		if err := os.WriteFile(iccPath, options.ICC, 0o600); err != nil {
			return nil, fmt.Errorf("无法写入 AVIF ICC 临时文件：%w", err)
		}
		args = append(args, "--icc", iccPath)
	}
	output := filepath.Join(dir, "out.avif")
	if err := runTool(ctx, "avifenc", append(args, output), bytes.NewReader(pngData)); err != nil {
		return nil, err
	}
	return readOutput(ctx, "avifenc", output)
}

func encodeArgs(options EncodeOptions) ([]string, error) {
	if !options.Lossless && (options.Quality < 1 || options.Quality > 100) {
		return nil, fmt.Errorf("AVIF 质量必须在 1 到 100 之间")
	}
	speed := options.Speed
	if speed <= 0 {
		speed = 6
	} else if speed > 10 {
		return nil, fmt.Errorf("AVIF 编码速度必须在 0 到 10 之间")
	}
	args := []string{"--stdin", "--input-format", "png", "-s", strconv.Itoa(speed), "-j", jobs(options.Threads)}
	switch options.Metadata {
	case MetadataAll:
	case MetadataICC:
		args = append(args, "--ignore-exif", "--ignore-xmp")
	case MetadataNone:
		args = append(args, "--ignore-exif", "--ignore-xmp", "--ignore-icc")
	default:
		return nil, fmt.Errorf("不支持的 AVIF 元数据策略：%q", options.Metadata)
	}
	if options.Metadata != MetadataNone && len(options.ICC) > maxAVIFBytes {
		return nil, fmt.Errorf("AVIF ICC 配置文件超过 64 MiB 限制")
	}
	if options.Lossless {
		return append(args, "--lossless"), nil
	}
	return append(args, "-q", strconv.Itoa(options.Quality), "--qalpha", "100"), nil
}

// EncodeImage prepares a bounded PNG from img, then encodes it with avifenc.
func EncodeImage(ctx context.Context, img image.Image, options EncodeOptions) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("AVIF 编码已取消：%w", err)
	}
	if img == nil || !validDimensions(img.Bounds().Dx(), img.Bounds().Dy()) {
		return nil, fmt.Errorf("AVIF 编码输入为空、尺寸无效或超过 3200 万像素限制")
	}
	if !Available() {
		return nil, ErrUnsupportedPlatform
	}
	out := pngBuffer{ctx: ctx}
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := encoder.Encode(&out, ConvertYCbCrToNRGBA(img)); err != nil {
		return nil, fmt.Errorf("无法生成 AVIF 编码所需的 PNG：%w", err)
	}
	return EncodePNG(ctx, out.buf.Bytes(), options)
}

// DecodePNG decodes a static AVIF to upright 8-bit PNG, retaining supported
// metadata. avifdec applies the container's clean aperture, rotation and mirror.
func DecodePNG(ctx context.Context, avifData []byte, threads int) ([]byte, error) {
	info, err := InspectBytes(ctx, avifData)
	if err != nil {
		return nil, err
	}
	if !Available() {
		return nil, ErrUnsupportedPlatform
	}

	dir, err := makeTempDir()
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	input, output := filepath.Join(dir, "in.avif"), filepath.Join(dir, "out.png")
	if err := os.WriteFile(input, avifData, 0o600); err != nil {
		return nil, fmt.Errorf("无法写入 AVIF 解码临时文件：%w", err)
	}
	args := []string{"--depth", "8", "--png-compress", "1", "--size-limit", strconv.Itoa(maxAVIFPixels), "-j", jobs(threads), input, output}
	if err := runTool(ctx, "avifdec", args, nil); err != nil {
		return nil, err
	}
	data, err := readOutput(ctx, "avifdec", output)
	if err != nil {
		return nil, err
	}
	cfg, err := png.DecodeConfig(contextio.NewReader(ctx, bytes.NewReader(data)))
	if err != nil {
		return nil, fmt.Errorf("AVIF 解码输出无效：%w", err)
	}
	if cfg.Width != info.Width || cfg.Height != info.Height {
		return nil, fmt.Errorf("AVIF 解码输出尺寸异常（%dx%d != %dx%d）", cfg.Width, cfg.Height, info.Width, info.Height)
	}
	return data, ctx.Err()
}

// ConvertYCbCrToNRGBA keeps 8-bit YCbCr pixels at 8 bits through the PNG bridge.
// The PNG encoder otherwise expands these color models to 16-bit RGB.
func ConvertYCbCrToNRGBA(img image.Image) image.Image {
	switch img.ColorModel() {
	case color.YCbCrModel, color.NYCbCrAModel:
		bounds := img.Bounds()
		rgb := image.NewNRGBA(bounds)
		draw.Draw(rgb, bounds, img, bounds.Min, draw.Src)
		return rgb
	default:
		return img
	}
}

func validDimensions(w, h int) bool {
	return w > 0 && h > 0 && w <= maxAVIFPixels/h
}

func jobs(threads int) string {
	if threads <= 0 {
		threads = runtime.GOMAXPROCS(0)
	}
	return strconv.Itoa(threads)
}

func makeTempDir() (string, error) {
	dir, err := os.MkdirTemp("", "zpic-avif-*")
	if err != nil {
		return "", fmt.Errorf("无法创建 AVIF 临时目录：%w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("无法设置 AVIF 临时目录权限：%w", err)
	}
	return dir, nil
}

func runTool(ctx context.Context, name string, args []string, stdin io.Reader) error {
	ctx, cancel := context.WithTimeout(ctx, toolTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%s 已取消或超时：%w", name, err)
	}
	path, cleanup, err := toolSlots[name].Acquire(name, "zpic-aviftools-", bundledTool(name), userCacheDir)
	if err != nil {
		return fmt.Errorf("无法准备 %s：%w", name, err)
	}
	defer cleanup()
	stderr := toolio.NewDiagnostic(toolio.MaxDiagnosticBytes)
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Stdin = stdin
	cmd.Stderr = stderr
	cmd.Env = []string{"LC_ALL=C"}
	cmd.WaitDelay = time.Second
	err = cmd.Run()
	if cancelErr := ctx.Err(); cancelErr != nil {
		return fmt.Errorf("%s 已取消或超时：%w", name, cancelErr)
	}
	if err != nil {
		if detail := strings.TrimSpace(stderr.String()); detail != "" {
			return fmt.Errorf("%s 执行失败：%w（%s）", name, err, detail)
		}
		return fmt.Errorf("%s 执行失败：%w", name, err)
	}
	return nil
}

func readOutput(ctx context.Context, name, path string) ([]byte, error) {
	data, err := imageinput.Read(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("无法读取 %s 输出：%w", name, err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("%s 输出为空", name)
	}
	return data, nil
}

type pngBuffer struct {
	ctx context.Context
	buf toolio.BoundedBuffer
}

func (w *pngBuffer) Write(data []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	if len(data) > maxAVIFBytes-w.buf.Len() {
		return 0, fmt.Errorf("AVIF 编码所需的 PNG 超过 64 MiB 限制")
	}
	w.buf.AppendBounded(nil, data, maxAVIFBytes)
	return len(data), nil
}
