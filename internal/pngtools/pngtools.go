// Package pngtools runs pinned native PNG tools on raw PNG bytes.
// Callers validate static PNG input (at most 64 MiB and 32 MP) and output.
package pngtools

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
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
	maxPNGBytes = 64 << 20
	maxStderr   = toolio.MaxDiagnosticBytes
	toolTimeout = 2 * time.Minute
)

var errOutputLimit = errors.New("PNG 工具输出超过 64 MiB 限制")

var (
	userCacheDir = os.UserCacheDir
	toolSlots    = map[string]*toolcache.Slot{
		"oxipng":   {},
		"pngquant": {},
	}
)

func effectivePNGThreads(threads int) string {
	if threads > 0 {
		return strconv.Itoa(min(8, threads))
	}
	return strconv.Itoa(min(4, max(1, runtime.GOMAXPROCS(0)/2)))
}

func pngThreadCount() string {
	return effectivePNGThreads(0)
}

// Optimize losslessly recompresses PNG data with Oxipng 10.2.1.
// It preserves decoded samples (including invisible RGB and 16-bit samples).
// Representation may change losslessly. Metadata is retained; transformations
// are disabled only when sBIT, bKGD, or hIST depends on the representation.
// ICC profiles may be losslessly recompressed. Byte-dependent caBX and iDOT
// chunks are automatically stripped by Oxipng when rewriting the file so they
// are not left invalid. The returned bytes may alias data.
func Optimize(ctx context.Context, data []byte) ([]byte, error) {
	return OptimizeWithThreads(ctx, data, 0)
}

// OptimizeWithThreads runs Optimize with an explicit native thread quota when threads > 0.
func OptimizeWithThreads(ctx context.Context, data []byte, threads int) ([]byte, error) {
	return OptimizeWithOptions(ctx, data, OptimizeOptions{Threads: threads})
}

// OptimizeWithOptions runs Oxipng with explicit thread quota and metadata strip mode.
func OptimizeWithOptions(ctx context.Context, data []byte, options OptimizeOptions) ([]byte, error) {
	if err := checkInput(ctx, data); err != nil {
		return nil, err
	}
	if err := options.Strip.Validate(); err != nil {
		return nil, err
	}
	threadStr := effectivePNGThreads(options.Threads)
	args := []string{"-o", "2", "--threads", threadStr}
	switch options.Strip {
	case StripSafe:
		args = append(args, "--strip", "safe")
	case StripAll:
		args = append(args, "--strip", "all")
	default:
		representationMetadata, err := hasRepresentationMetadata(ctx, data)
		if err != nil {
			return nil, err
		}
		if representationMetadata {
			args = append(args, "--nx")
		}
	}
	return invokeWithConfig(ctx, "oxipng", append(args, "--stdout", "-"), data, options.Threads, options.AllowLarger)
}

// HasStrippableMetadata reports whether data contains PNG chunks that would be
// removed by Oxipng under mode.
func HasStrippableMetadata(ctx context.Context, data []byte, mode StripMode) (bool, error) {
	if mode == StripNone {
		return false, ctx.Err()
	}
	for offset := 8; offset <= len(data)-12; {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		size := uint64(binary.BigEndian.Uint32(data[offset:]))
		if size > uint64(len(data)-offset-12) {
			return false, nil
		}
		chunkType := string(data[offset+4 : offset+8])
		if chunkType == "IEND" {
			return false, ctx.Err()
		}
		switch chunkType {
		case "IHDR", "PLTE", "IDAT", "tRNS":
		case "cICP", "iCCP", "sRGB", "pHYs", "acTL", "fcTL", "fdAT":
			if mode == StripAll {
				return true, nil
			}
		default:
			return true, nil
		}
		offset += int(size) + 12
	}
	return false, ctx.Err()
}

// Quantize runs pngquant 3.0.3 with DefaultQuantizeOptions.
func Quantize(ctx context.Context, data []byte) ([]byte, error) {
	return QuantizeWithOptions(ctx, data, DefaultQuantizeOptions())
}

// QuantizeWithOptions runs pngquant with the specified quality, palette size,
// and speed. All-zero options select defaults. Exit codes 98 and 99
// return the original bytes, discarding any partial stdout. Successful output
// that does not save space also returns the original bytes. This is lossy;
// unsafe-to-copy chunks such as caBX and iDOT are dropped when rewriting the
// quantized PNG. Callers run Optimize afterwards and validate the final output.
func QuantizeWithOptions(ctx context.Context, data []byte, options QuantizeOptions) ([]byte, error) {
	return QuantizeWithThreads(ctx, data, options, 0)
}

// QuantizeWithThreads runs QuantizeWithOptions with an explicit native thread quota when threads > 0.
func QuantizeWithThreads(ctx context.Context, data []byte, options QuantizeOptions, threads int) ([]byte, error) {
	return QuantizeWithStrip(ctx, data, options, threads, false)
}

// QuantizeWithStrip runs pngquant with optional --strip for full metadata removal.
func QuantizeWithStrip(ctx context.Context, data []byte, options QuantizeOptions, threads int, stripAll bool) ([]byte, error) {
	if err := checkInput(ctx, data); err != nil {
		return nil, err
	}
	if options == (QuantizeOptions{}) {
		options = DefaultQuantizeOptions()
	}
	if err := options.Validate(); err != nil {
		return nil, err
	}
	args := make([]string, 0, 8)
	if stripAll {
		args = append(args, "--strip")
	}
	args = append(args,
		fmt.Sprintf("--quality=%d-%d", options.MinQuality, options.MaxQuality),
		"--speed="+strconv.Itoa(options.Speed),
		"--skip-if-larger", "--output", "-", strconv.Itoa(options.Colors), "-",
	)
	return invokeWithConfig(ctx, "pngquant", args, data, threads, false)
}

func checkInput(ctx context.Context, data []byte) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("PNG 处理已取消：%w", err)
	}
	if len(data) > maxPNGBytes {
		return fmt.Errorf("PNG 输入超过 64 MiB 限制")
	}
	return nil
}

// The caller validates PNG structure. Still bound every slice here so a bad
// direct call cannot panic while looking for representation-dependent metadata.
func hasRepresentationMetadata(ctx context.Context, data []byte) (bool, error) {
	for offset := 8; offset <= len(data)-12; {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		size := uint64(binary.BigEndian.Uint32(data[offset:]))
		if size > uint64(len(data)-offset-12) {
			return false, nil
		}
		switch string(data[offset+4 : offset+8]) {
		case "sBIT", "bKGD", "hIST":
			return true, nil
		case "IEND":
			return false, ctx.Err()
		}
		offset += int(size) + 12
	}
	return false, ctx.Err()
}

func invoke(ctx context.Context, name string, args []string, data []byte) ([]byte, error) {
	return invokeWithThreads(ctx, name, args, data, 0)
}

func invokeWithThreads(ctx context.Context, name string, args []string, data []byte, threads int) ([]byte, error) {
	return invokeWithConfig(ctx, name, args, data, threads, false)
}

func invokeWithConfig(ctx context.Context, name string, args []string, data []byte, threads int, allowLarger bool) ([]byte, error) {
	// Include extraction in the cancellation/deadline budget.
	ctx, cancel := context.WithTimeout(ctx, toolTimeout)
	defer cancel()
	path, cleanup, err := acquireTool(name)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	return runCommandWithConfig(ctx, name, path, args, data, maxPNGBytes, threads, allowLarger)
}

func acquireTool(name string) (string, func(), error) {
	asset := bundledTool(name)
	if len(asset) == 0 {
		return "", nil, fmt.Errorf("当前平台 %s/%s 未捆绑 %s；请参照 scripts/prepare-pngtools.sh 构建并打包原生工具",
			runtime.GOOS, runtime.GOARCH, name)
	}
	slot := toolSlots[name]
	if slot == nil {
		return toolcache.ExtractTemp("zpic-pngtools-", name, asset)
	}
	return slot.Acquire(name, "zpic-pngtools-", asset, func() (string, error) {
		return userCacheDir()
	})
}

func extractTool(name string) (string, func(), error) {
	asset := bundledTool(name)
	if len(asset) == 0 {
		return "", nil, fmt.Errorf("当前平台 %s/%s 未捆绑 %s；请参照 scripts/prepare-pngtools.sh 构建并打包原生工具",
			runtime.GOOS, runtime.GOARCH, name)
	}
	return toolcache.ExtractTemp("zpic-pngtools-", name, asset)
}

// runCommand is private so tests can exercise process behavior without a
// production environment override or an exported executable injection hook.
func runCommand(ctx context.Context, name, path string, args []string, data []byte, limit int) ([]byte, error) {
	return runCommandWithThreads(ctx, name, path, args, data, limit, 0)
}

func runCommandWithThreads(ctx context.Context, name, path string, args []string, data []byte, limit, threads int) ([]byte, error) {
	return runCommandWithConfig(ctx, name, path, args, data, limit, threads, false)
}

func runCommandWithConfig(ctx context.Context, name, path string, args []string, data []byte, limit, threads int, allowLarger bool) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%s 已取消：%w", name, err)
	}
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stdout := &limitedOutput{input: data, limit: limit + 1, allowLarger: allowLarger, cancel: cancel}
	stderr := toolio.NewDiagnostic(maxStderr)
	cmd := exec.CommandContext(childCtx, path, args...)
	cmd.Stdin = bytes.NewReader(data)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	threadStr := effectivePNGThreads(threads)
	// No PATH lookup or inherited dynamic-library overrides are necessary.
	cmd.Env = []string{"PATH=", "LC_ALL=C", "RAYON_NUM_THREADS=" + threadStr, "OMP_NUM_THREADS=" + threadStr}
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("%s 已取消或超时：%w", name, ctx.Err())
	}
	if stdout.overflow || stdout.size > limit {
		return nil, fmt.Errorf("%s：%w", name, errOutputLimit)
	}
	if err != nil {
		var exitErr *exec.ExitError
		if name == "pngquant" && errors.As(err, &exitErr) &&
			(exitErr.ExitCode() == 98 || exitErr.ExitCode() == 99) {
			return data, nil
		}
		return nil, fmt.Errorf("%s 执行失败（请检查捆绑工具和系统执行权限）：%w；%s",
			name, err, strings.TrimSpace(stderr.String()))
	}
	if stdout.size == 0 {
		return nil, fmt.Errorf("%s 未生成 PNG 数据", name)
	}
	if !allowLarger && stdout.size >= len(data) {
		return data, nil
	}
	if stdout.buf.Len() == 0 {
		return data[:stdout.size], nil
	}
	return stdout.buf.Bytes(), nil
}

type limitedOutput struct {
	input       []byte
	buf         toolio.BoundedBuffer
	size        int
	limit       int
	allowLarger bool
	overflow    bool
	cancel      context.CancelFunc
}

func (w *limitedOutput) Write(p []byte) (int, error) {
	n := min(len(p), w.limit-w.size)
	start := w.size
	w.size += n
	if w.allowLarger {
		if n > 0 {
			w.buf.AppendBounded(nil, p[:n], w.limit)
		}
	} else if w.size >= len(w.input) {
		w.buf.Reset()
	} else if w.buf.Len() != 0 || !bytes.Equal(p[:n], w.input[start:w.size]) {
		var prefix []byte
		if w.buf.Len() == 0 {
			prefix = w.input[:start]
		}
		w.buf.AppendBounded(prefix, p[:n], min(w.limit, len(w.input)-1))
	}
	if n < len(p) || w.size == w.limit {
		w.overflow = true
		w.cancel()
		return n, errOutputLimit
	}
	return n, nil
}
