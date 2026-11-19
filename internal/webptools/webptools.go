package webptools

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
	maxWebPBytes = 64 << 20
	maxStderr    = toolio.MaxDiagnosticBytes
	toolTimeout  = 2 * time.Minute
)

var errOutputLimit = errors.New("WebP 工具输出超过 64 MiB 限制")

var (
	userCacheDir = os.UserCacheDir
	cwebpSlot    toolcache.Slot
)

// MetadataMode selects which metadata chunks cwebp copies from WebP inputs.
// The zero value retains all metadata (-metadata all).
type MetadataMode string

const (
	MetadataAll  MetadataMode = ""
	MetadataICC  MetadataMode = "icc"
	MetadataNone MetadataMode = "none"
)

func (m MetadataMode) arg() (string, error) {
	switch m {
	case MetadataAll:
		return "all", nil
	case MetadataICC:
		return "icc", nil
	case MetadataNone:
		return "none", nil
	default:
		return "", fmt.Errorf("不支持的 WebP 元数据策略：%q", string(m))
	}
}

// EncodeOptions configures cwebp execution.
type EncodeOptions struct {
	Quality     int
	Lossless    bool
	Metadata    MetadataMode
	Threads     int
	SmallerOnly bool // When true and input is WebP, discard outputs >= len(data) and return data.
}

func Encode(ctx context.Context, data []byte, quality int) ([]byte, error) {
	return EncodeWithOptions(ctx, data, EncodeOptions{Quality: quality})
}

// EncodeSmaller returns the encoded output only when it is smaller than data.
// Otherwise it returns data itself, which the caller must keep immutable.
// Output limits, process failures and cancellation still return errors.
func EncodeSmaller(ctx context.Context, data []byte, quality int) ([]byte, error) {
	return EncodeSmallerWithThreads(ctx, data, quality, 0)
}

// EncodeSmallerWithThreads follows EncodeSmaller and bounds multi-threading when threads == 1.
func EncodeSmallerWithThreads(ctx context.Context, data []byte, quality, threads int) ([]byte, error) {
	return EncodeWithOptions(ctx, data, EncodeOptions{
		Quality:     quality,
		Threads:     threads,
		SmallerOnly: true,
	})
}

// EncodeWithOptions recompresses raw WebP bytes using the configured quality,
// lossless mode, metadata policy, and threading cap.
func EncodeWithOptions(ctx context.Context, data []byte, options EncodeOptions) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("WebP 处理已取消：%w", err)
	}
	args, err := buildEncodeArgs(options, true)
	if err != nil {
		return nil, err
	}
	if len(data) > maxWebPBytes {
		return nil, fmt.Errorf("WebP 输入超过 64 MiB 限制")
	}
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return nil, fmt.Errorf("WebP 工具仅接受 WebP 输入")
	}
	ctx, cancel := context.WithTimeout(ctx, toolTimeout)
	defer cancel()
	path, cleanup, err := acquireTool()
	if err != nil {
		return nil, err
	}
	defer cleanup()
	var fallback []byte
	if options.SmallerOnly {
		fallback = data
	}
	return runCommandWithReader(ctx, path, args, bytes.NewReader(data), maxWebPBytes, fallback)
}

func buildEncodeArgs(options EncodeOptions, withMetadata bool) ([]string, error) {
	if !options.Lossless && (options.Quality < 0 || options.Quality > 100) {
		return nil, fmt.Errorf("WebP 质量必须在 0–100 之间")
	}
	metaArg, err := options.Metadata.arg()
	if err != nil {
		return nil, err
	}
	args := make([]string, 0, 14)
	if options.Lossless {
		args = append(args, "-lossless", "-z", "6")
	} else {
		args = append(args, "-q", strconv.Itoa(options.Quality), "-m", "4", "-alpha_q", "100")
	}
	if useMultiThreading(options.Threads) {
		args = append(args, "-mt")
	}
	if withMetadata {
		args = append(args, "-metadata", metaArg)
	} else {
		args = append(args, "-metadata", "none")
	}
	args = append(args, "-quiet", "-o", "-", "--", "-")
	return args, nil
}

func useMultiThreading(threads int) bool {
	if threads > 0 {
		return threads > 1
	}
	return runtime.GOMAXPROCS(0) > 1
}

func acquireTool() (string, func(), error) {
	asset := bundledTool()
	if len(asset) == 0 {
		return "", nil, fmt.Errorf("当前平台 %s/%s 未捆绑 cwebp；请参照 scripts/prepare-webptools.sh 构建并打包原生工具",
			runtime.GOOS, runtime.GOARCH)
	}
	return cwebpSlot.Acquire("cwebp", "zpic-webptools-", asset, func() (string, error) {
		return userCacheDir()
	})
}

func extractTool() (string, func(), error) {
	asset := bundledTool()
	if len(asset) == 0 {
		return "", nil, fmt.Errorf("当前平台 %s/%s 未捆绑 cwebp；请参照 scripts/prepare-webptools.sh 构建并打包原生工具",
			runtime.GOOS, runtime.GOARCH)
	}
	return toolcache.ExtractTemp("zpic-webptools-", "cwebp", asset)
}

func runCommand(ctx context.Context, path string, args []string, data []byte, limit int) ([]byte, error) {
	return runCommandWithFallback(ctx, path, args, data, limit, nil)
}

func runCommandWithFallback(ctx context.Context, path string, args []string, data []byte, limit int, fallback []byte) ([]byte, error) {
	return runCommandWithReader(ctx, path, args, bytes.NewReader(data), limit, fallback)
}

func runCommandWithReader(ctx context.Context, path string, args []string, stdin io.Reader, limit int, fallback []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("cwebp 已取消：%w", err)
	}
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stdout := &limitedOutput{limit: limit, cancel: cancel, fallback: fallback}
	stderr := toolio.NewDiagnostic(maxStderr)
	cmd := exec.CommandContext(childCtx, path, args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = []string{"LC_ALL=C"}
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("cwebp 已取消或超时：%w", ctx.Err())
	}
	if stdout.overflow {
		return nil, fmt.Errorf("cwebp：%w", errOutputLimit)
	}
	if err != nil {
		return nil, fmt.Errorf("cwebp 执行失败（请检查捆绑工具和系统执行权限）：%w；%s",
			err, strings.TrimSpace(stderr.String()))
	}
	if stdout.size == 0 {
		return nil, fmt.Errorf("cwebp 未生成 WebP 数据")
	}
	if stdout.discarded {
		return fallback, nil
	}
	return stdout.buf.Bytes(), nil
}

type limitedOutput struct {
	buf       toolio.BoundedBuffer
	fallback  []byte
	size      int
	limit     int
	discarded bool
	overflow  bool
	cancel    context.CancelFunc
}

func (w *limitedOutput) Write(data []byte) (int, error) {
	size := min(len(data), w.limit-w.size)
	w.size += size
	if !w.discarded && w.fallback != nil && w.size >= len(w.fallback) {
		w.buf.Reset()
		w.discarded = true
	}
	if !w.discarded && size > 0 {
		maxCap := w.limit
		if w.fallback != nil {
			maxCap = min(maxCap, len(w.fallback)-1)
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
