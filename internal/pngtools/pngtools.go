// Package pngtools runs pinned native PNG tools on raw PNG bytes.
// Callers validate static PNG input (at most 64 MiB and 32 MP) and output.
package pngtools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"zpic/internal/toolcache"
)

const (
	maxPNGBytes = 64 << 20
	maxStderr   = 32 << 10
	toolTimeout = 2 * time.Minute
)

var errOutputLimit = errors.New("PNG 工具输出超过 64 MiB 限制")

type cachedToolEntry struct {
	path string
	info os.FileInfo
	sum  [sha256.Size]byte
	has  bool
}

var (
	userCacheDir = os.UserCacheDir
	toolCacheMu  sync.Mutex
	toolCache    = make(map[string]cachedToolEntry)
)

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
	immutable, representationMetadata := chunkConstraints(data)
	if immutable {
		return data, nil
	}
	args := []string{"-o", "2", "--threads", "2"}
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
// callers run Optimize afterwards and validate the final output.
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
func chunkConstraints(data []byte) (immutable, representationMetadata bool) {
	for offset := 8; offset <= len(data)-12; {
		size := uint64(binary.BigEndian.Uint32(data[offset:]))
		if size > uint64(len(data)-offset-12) {
			return false, false
		}
		switch string(data[offset+4 : offset+8]) {
		case "caBX", "iDOT":
			immutable = true
		case "sBIT", "bKGD", "hIST":
			representationMetadata = true
		}
		offset += int(size) + 12
	}
	return immutable, representationMetadata
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

func executableFilename(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func acquireTool(name string) (string, func(), error) {
	asset := bundledTool(name)
	if len(asset) == 0 {
		return "", nil, fmt.Errorf("当前平台 %s/%s 未捆绑 %s；请参照 scripts/prepare-pngtools.sh 构建并打包原生工具",
			runtime.GOOS, runtime.GOARCH, name)
	}
	if path, err := cachedToolPath(name, asset); err == nil {
		return path, func() {}, nil
	}
	return extractBundledTool(name, asset)
}

func cachedToolPath(name string, asset []byte) (string, error) {
	toolCacheMu.Lock()
	defer toolCacheMu.Unlock()
	cacheRoot, err := userCacheDir()
	if err != nil || cacheRoot == "" {
		return "", errors.New("缓存目录不可用")
	}
	entry := toolCache[name]
	if !entry.has {
		entry.sum = sha256.Sum256(asset)
		entry.has = true
	}
	entryDir := filepath.Join(cacheRoot, "z-pic", "tools", fmt.Sprintf("%s-%x", name, entry.sum[:16]))
	binaryPath := filepath.Join(entryDir, executableFilename(name))
	if entry.path == binaryPath && entry.info != nil && toolcache.MatchesVerified(entryDir, binaryPath, int64(len(asset)), entry.info) {
		return binaryPath, nil
	}
	if err := toolcache.EnsurePrivateDir(filepath.Join(cacheRoot, "z-pic")); err != nil {
		return "", err
	}
	if err := toolcache.EnsurePrivateDir(filepath.Join(cacheRoot, "z-pic", "tools")); err != nil {
		return "", err
	}
	if err := toolcache.EnsurePrivateDir(entryDir); err != nil {
		return "", err
	}
	if info, err := toolcache.Verify(binaryPath, int64(len(asset)), entry.sum); err == nil {
		entry.path, entry.info = binaryPath, info
		toolCache[name] = entry
		return binaryPath, nil
	}
	info, err := toolcache.Stage(entryDir, binaryPath, asset)
	if err != nil {
		return "", err
	}
	entry.path, entry.info = binaryPath, info
	toolCache[name] = entry
	return binaryPath, nil
}

func extractTool(name string) (string, func(), error) {
	asset := bundledTool(name)
	if len(asset) == 0 {
		return "", nil, fmt.Errorf("当前平台 %s/%s 未捆绑 %s；请参照 scripts/prepare-pngtools.sh 构建并打包原生工具",
			runtime.GOOS, runtime.GOARCH, name)
	}
	return extractBundledTool(name, asset)
}

func extractBundledTool(name string, asset []byte) (string, func(), error) {
	dir, err := os.MkdirTemp("", "zpic-pngtools-")
	if err != nil {
		return "", nil, fmt.Errorf("无法创建 PNG 工具临时目录：%w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	path := filepath.Join(dir, executableFilename(name))
	if err := os.WriteFile(path, asset, 0700); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("无法释放 %s 工具：%w", name, err)
	}
	return path, cleanup, nil
}

// runCommand is private so tests can exercise process behavior without a
// production environment override or an exported executable injection hook.
func runCommand(ctx context.Context, name, path string, args []string, data []byte, limit int) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%s 已取消：%w", name, err)
	}
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stdout := &limitedOutput{limit: limit + 1, cancel: cancel}
	stderr := &limitedDiagnostic{limit: maxStderr}
	cmd := exec.CommandContext(childCtx, path, args...)
	cmd.Stdin = bytes.NewReader(data)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	// No PATH lookup or inherited dynamic-library overrides are necessary.
	cmd.Env = []string{"PATH=", "LC_ALL=C", "RAYON_NUM_THREADS=2"}
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("%s 已取消或超时：%w", name, ctx.Err())
	}
	if stdout.overflow || stdout.buf.Len() > limit {
		return nil, fmt.Errorf("%s：%w", name, errOutputLimit)
	}
	if err != nil {
		var exitErr *exec.ExitError
		if name == "pngquant" && errors.As(err, &exitErr) &&
			(exitErr.ExitCode() == 98 || exitErr.ExitCode() == 99) {
			return data, nil
		}
		return nil, fmt.Errorf("%s 执行失败（请检查捆绑工具和系统执行权限）：%w；%s",
			name, err, strings.TrimSpace(stderr.buf.String()))
	}
	if stdout.buf.Len() == 0 {
		return nil, fmt.Errorf("%s 未生成 PNG 数据", name)
	}
	if stdout.buf.Len() >= len(data) {
		return data, nil
	}
	return stdout.buf.Bytes(), nil
}

type limitedOutput struct {
	buf      bytes.Buffer
	limit    int
	overflow bool
	cancel   context.CancelFunc
}

func (w *limitedOutput) Write(p []byte) (int, error) {
	n := min(len(p), w.limit-w.buf.Len())
	_, _ = w.buf.Write(p[:n])
	if n < len(p) || w.buf.Len() == w.limit {
		w.overflow = true
		w.cancel()
		return n, errOutputLimit
	}
	return n, nil
}

// Stderr is drained even after truncation so verbose diagnostics cannot block
// a child process or grow memory without bound.
type limitedDiagnostic struct {
	buf   bytes.Buffer
	limit int
}

func (w *limitedDiagnostic) Write(p []byte) (int, error) {
	_, _ = w.buf.Write(p[:min(len(p), w.limit-w.buf.Len())])
	return len(p), nil
}
