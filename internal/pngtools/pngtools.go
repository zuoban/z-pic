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

func pngThreadCount() string {
	return strconv.Itoa(min(4, max(2, runtime.GOMAXPROCS(0)/2)))
}

// Optimize losslessly recompresses PNG data with Oxipng 10.2.1.
// It preserves decoded samples (including invisible RGB and 16-bit samples).
// Representation may change losslessly. Metadata is retained; transformations
// are disabled only when sBIT, bKGD, or hIST depends on the representation.
// ICC profiles may be losslessly recompressed.
// Signed/offset-dependent caBX and iDOT chunks preclude changing the file, so
// those inputs are returned untouched. The returned bytes may alias data.
func Optimize(ctx context.Context, data []byte) ([]byte, error) {
	if err := checkInput(ctx, data); err != nil {
		return nil, err
	}
	immutable, representationMetadata, err := chunkConstraints(ctx, data)
	if err != nil {
		return nil, err
	}
	if immutable {
		return data, nil
	}
	args := []string{"-o", "2", "--threads", pngThreadCount()}
	if representationMetadata {
		args = append(args, "--nx")
	}
	return invoke(ctx, "oxipng", append(args, "--stdout", "-"), data)
}

// Quantize runs pngquant 3.0.3 with DefaultQuantizeOptions.
func Quantize(ctx context.Context, data []byte) ([]byte, error) {
	return QuantizeWithOptions(ctx, data, DefaultQuantizeOptions())
}

// QuantizeWithOptions runs pngquant with the specified quality, palette size,
// and speed. All-zero options select defaults. Exit codes 98 and 99
// return the original bytes, discarding any partial stdout. Successful output
// that does not save space also returns the original bytes. This is lossy;
// signed/offset-dependent caBX and iDOT inputs are returned untouched, as in
// Optimize. Callers run Optimize afterwards and validate the final output.
func QuantizeWithOptions(ctx context.Context, data []byte, options QuantizeOptions) ([]byte, error) {
	if err := checkInput(ctx, data); err != nil {
		return nil, err
	}
	if options == (QuantizeOptions{}) {
		options = DefaultQuantizeOptions()
	}
	if err := options.Validate(); err != nil {
		return nil, err
	}
	immutable, _, err := chunkConstraints(ctx, data)
	if err != nil {
		return nil, err
	}
	if immutable {
		return data, nil
	}
	return invoke(ctx, "pngquant", []string{
		fmt.Sprintf("--quality=%d-%d", options.MinQuality, options.MaxQuality),
		"--speed=" + strconv.Itoa(options.Speed),
		"--skip-if-larger", "--output", "-", strconv.Itoa(options.Colors), "-",
	}, data)
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
// direct call cannot panic while looking for byte-dependent metadata.
func chunkConstraints(ctx context.Context, data []byte) (immutable, representationMetadata bool, err error) {
	for offset := 8; offset <= len(data)-12; {
		if err := ctx.Err(); err != nil {
			return false, false, err
		}
		size := uint64(binary.BigEndian.Uint32(data[offset:]))
		if size > uint64(len(data)-offset-12) {
			return false, false, nil
		}
		switch string(data[offset+4 : offset+8]) {
		case "caBX", "iDOT":
			immutable = true
		case "sBIT", "bKGD", "hIST":
			representationMetadata = true
		}
		offset += int(size) + 12
	}
	return immutable, representationMetadata, ctx.Err()
}

func invoke(ctx context.Context, name string, args []string, data []byte) ([]byte, error) {
	// Include extraction in the cancellation/deadline budget.
	ctx, cancel := context.WithTimeout(ctx, toolTimeout)
	defer cancel()
	path, cleanup, err := acquireTool(name)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	return runCommand(ctx, name, path, args, data, maxPNGBytes)
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
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%s 已取消：%w", name, err)
	}
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stdout := &limitedOutput{input: data, limit: limit + 1, cancel: cancel}
	stderr := toolio.NewDiagnostic(maxStderr)
	cmd := exec.CommandContext(childCtx, path, args...)
	cmd.Stdin = bytes.NewReader(data)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	// No PATH lookup or inherited dynamic-library overrides are necessary.
	cmd.Env = []string{"PATH=", "LC_ALL=C", "RAYON_NUM_THREADS=" + pngThreadCount()}
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
	if stdout.size >= len(data) {
		return data, nil
	}
	if stdout.buf.Len() == 0 {
		return data[:stdout.size], nil
	}
	return stdout.buf.Bytes(), nil
}

type limitedOutput struct {
	input    []byte
	buf      bytes.Buffer
	size     int
	limit    int
	overflow bool
	cancel   context.CancelFunc
}

func (w *limitedOutput) Write(p []byte) (int, error) {
	n := min(len(p), w.limit-w.size)
	start := w.size
	w.size += n
	if w.size >= len(w.input) {
		w.buf = bytes.Buffer{}
	} else if w.buf.Len() != 0 || !bytes.Equal(p[:n], w.input[start:w.size]) {
		if w.buf.Len() == 0 {
			w.buf.Grow(w.size)
			_, _ = w.buf.Write(w.input[:start])
		}
		_, _ = w.buf.Write(p[:n])
	}
	if n < len(p) || w.size == w.limit {
		w.overflow = true
		w.cancel()
		return n, errOutputLimit
	}
	return n, nil
}
