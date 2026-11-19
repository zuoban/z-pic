// Package jpegtools runs pinned native MozJPEG tools (cjpeg and jpegtran)
// on image pixel streams and raw JPEG bytes.
package jpegtools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"zpic/internal/toolcache"
	"zpic/internal/toolio"
)

const (
	maxJPEGBytes  = 64 << 20
	maxJPEGPixels = 32_000_000
	maxStderr     = toolio.MaxDiagnosticBytes
	toolTimeout   = 2 * time.Minute
)

var (
	errOutputLimit = errors.New("MozJPEG 工具输出超过 64 MiB 限制")
	// ErrNoSavings reports that encoded output reached or exceeded MaxOutputBytes.
	ErrNoSavings = errors.New("MozJPEG 输出未减小")
	// ErrImperfectTransform reports that jpegtran -perfect rejected a lossless
	// rotation/flip because image dimensions are not MCU-aligned.
	ErrImperfectTransform = errors.New("JPEG 尺寸未对齐 MCU 块，无法无损旋转")
)

var (
	userCacheDir = os.UserCacheDir
	toolSlots    = map[string]*toolcache.Slot{
		"cjpeg":    {},
		"jpegtran": {},
	}
)

// Available reports whether native MozJPEG executables are bundled for this target.
func Available() bool {
	return len(bundledTool("cjpeg")) > 0 && len(bundledTool("jpegtran")) > 0
}

// CopyMode selects which extra markers jpegtran preserves during lossless optimization.
type CopyMode string

const (
	CopyAll  CopyMode = ""
	CopyICC  CopyMode = "icc"
	CopyNone CopyMode = "none"
)

func (m CopyMode) arg() (string, error) {
	switch m {
	case CopyAll:
		return "all", nil
	case CopyICC:
		return "icc", nil
	case CopyNone:
		return "none", nil
	default:
		return "", fmt.Errorf("不支持的 JPEG 元数据复制策略：%q", string(m))
	}
}

// EncodeOptions configures MozJPEG cjpeg lossy encoding.
type EncodeOptions struct {
	Quality        int // Quality in [1, 100].
	MaxOutputBytes int // Optional strict upper bound (< MaxOutputBytes). <= 0 disables early discard.
}

// OptimizeOptions configures MozJPEG jpegtran lossless optimization.
type OptimizeOptions struct {
	Copy        CopyMode
	Orientation int  // Optional EXIF orientation [1..8] to bake into DCT blocks (1 or 0 = no transform).
	Perfect     bool // Require lossless MCU-aligned transform (-perfect) when Orientation > 1.
	SmallerOnly bool // Discard outputs >= len(data) and return data directly.
}

// OptimizeLossless losslessly optimizes JPEG Huffman tables and progressive scans
// via MozJPEG jpegtran without altering decoded DCT coefficients.
func OptimizeLossless(ctx context.Context, data []byte, options OptimizeOptions) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("JPEG 无损优化已取消：%w", err)
	}
	if len(data) > maxJPEGBytes {
		return nil, fmt.Errorf("JPEG 输入超过 64 MiB 限制")
	}
	if len(data) < 2 || data[0] != 0xff || data[1] != 0xd8 {
		return nil, fmt.Errorf("MozJPEG 仅接受 JPEG 输入")
	}
	args, err := buildOptimizeArgs(options)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, toolTimeout)
	defer cancel()
	path, cleanup, err := acquireTool("jpegtran")
	if err != nil {
		return nil, err
	}
	defer cleanup()

	maxKeep := 0
	if options.SmallerOnly {
		maxKeep = len(data)
	}
	out, err := runCommandWithReader(ctx, "jpegtran", path, args, bytes.NewReader(data), maxJPEGBytes, maxKeep)
	if err != nil {
		if errors.Is(err, ErrNoSavings) && options.SmallerOnly {
			return data, nil
		}
		if options.Perfect && options.Orientation > 1 && strings.Contains(strings.ToLower(err.Error()), "not perfect") {
			return nil, fmt.Errorf("%w：%w", ErrImperfectTransform, err)
		}
		return nil, err
	}
	return out, nil
}

func buildOptimizeArgs(options OptimizeOptions) ([]string, error) {
	copyArg, err := options.Copy.arg()
	if err != nil {
		return nil, err
	}
	args := []string{"-optimize", "-progressive", "-copy", copyArg}
	if options.Orientation != 0 && (options.Orientation < 1 || options.Orientation > 8) {
		return nil, fmt.Errorf("无效的 JPEG 方向值：%d", options.Orientation)
	}
	if options.Orientation > 1 {
		if options.Perfect {
			args = append(args, "-perfect")
		}
		switch options.Orientation {
		case 2:
			args = append(args, "-flip", "horizontal")
		case 3:
			args = append(args, "-rotate", "180")
		case 4:
			args = append(args, "-flip", "vertical")
		case 5:
			args = append(args, "-transpose")
		case 6:
			args = append(args, "-rotate", "90")
		case 7:
			args = append(args, "-transverse")
		case 8:
			args = append(args, "-rotate", "270")
		}
	}
	return args, nil
}

func buildEncodeArgs(options EncodeOptions, grayscale bool) ([]string, error) {
	if options.Quality < 1 || options.Quality > 100 {
		return nil, fmt.Errorf("JPEG 质量必须在 1–100 之间")
	}
	args := []string{"-quality", strconv.Itoa(options.Quality), "-optimize", "-progressive"}
	if grayscale {
		args = append(args, "-grayscale")
	}
	return args, nil
}

func acquireTool(name string) (string, func(), error) {
	asset := bundledTool(name)
	if len(asset) == 0 {
		return "", nil, fmt.Errorf("当前平台 %s/%s 未捆绑 %s；请参照 scripts/prepare-jpegtools.sh 构建并打包原生工具",
			runtime.GOOS, runtime.GOARCH, name)
	}
	slot := toolSlots[name]
	if slot == nil {
		return toolcache.ExtractTemp("zpic-jpegtools-", name, asset)
	}
	return slot.Acquire(name, "zpic-jpegtools-", asset, func() (string, error) {
		return userCacheDir()
	})
}

func extractTool(name string) (string, func(), error) {
	asset := bundledTool(name)
	if len(asset) == 0 {
		return "", nil, fmt.Errorf("当前平台 %s/%s 未捆绑 %s；请参照 scripts/prepare-jpegtools.sh 构建并打包原生工具",
			runtime.GOOS, runtime.GOARCH, name)
	}
	return toolcache.ExtractTemp("zpic-jpegtools-", name, asset)
}

func runCommand(ctx context.Context, name, path string, args []string, data []byte, limit int) ([]byte, error) {
	return runCommandWithReader(ctx, name, path, args, bytes.NewReader(data), limit, 0)
}

func runCommandWithReader(ctx context.Context, name, path string, args []string, stdin io.Reader, limit, discardAt int) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%s 已取消：%w", name, err)
	}
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stdout := &limitedOutput{limit: limit, discardAt: discardAt, cancel: cancel}
	stderr := toolio.NewDiagnostic(maxStderr)
	cmd := exec.CommandContext(childCtx, path, args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = []string{"LC_ALL=C"}
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("%s 已取消或超时：%w", name, ctx.Err())
	}
	if stdout.overflow {
		return nil, fmt.Errorf("%s：%w", name, errOutputLimit)
	}
	if err != nil {
		return nil, fmt.Errorf("%s 执行失败（请检查捆绑工具和系统执行权限）：%w；%s",
			name, err, strings.TrimSpace(stderr.String()))
	}
	if stdout.size == 0 {
		return nil, fmt.Errorf("%s 未生成 JPEG 数据", name)
	}
	if stdout.discarded {
		return nil, ErrNoSavings
	}
	return stdout.buf.Bytes(), nil
}

type limitedOutput struct {
	buf       toolio.BoundedBuffer
	size      int
	limit     int
	discardAt int // Discard candidate bytes once size >= discardAt (when > 0).
	discarded bool
	overflow  bool
	cancel    context.CancelFunc
}

func (w *limitedOutput) Write(data []byte) (int, error) {
	size := min(len(data), w.limit-w.size)
	w.size += size
	if !w.discarded && w.discardAt > 0 && w.size >= w.discardAt {
		w.buf.Reset()
		w.discarded = true
	}
	if !w.discarded && size > 0 {
		maxCap := w.limit
		if w.discardAt > 0 {
			maxCap = min(maxCap, w.discardAt-1)
		}
		w.buf.AppendBounded(nil, data[:size], maxCap)
	}
	if size < len(data) {
		w.overflow = true
		w.cancel()
		return size, errOutputLimit
	}
	return size, nil
}
