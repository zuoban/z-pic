package webptools

import (
	"bytes"
	"context"
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
	maxWebPBytes = 64 << 20
	maxStderr    = toolio.MaxDiagnosticBytes
	toolTimeout  = 2 * time.Minute
)

var errOutputLimit = errors.New("WebP 工具输出超过 64 MiB 限制")

var (
	userCacheDir = os.UserCacheDir
	cwebpSlot    toolcache.Slot
)

func Encode(ctx context.Context, data []byte, quality int) ([]byte, error) {
	return encode(ctx, data, quality, false)
}

// EncodeSmaller returns the encoded output only when it is smaller than data.
// Otherwise it returns data itself, which the caller must keep immutable.
// Output limits, process failures and cancellation still return errors.
func EncodeSmaller(ctx context.Context, data []byte, quality int) ([]byte, error) {
	return encode(ctx, data, quality, true)
}

func encode(ctx context.Context, data []byte, quality int, smallerOnly bool) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("WebP 处理已取消：%w", err)
	}
	if quality < 0 || quality > 100 {
		return nil, fmt.Errorf("WebP 质量必须在 0–100 之间")
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
	if smallerOnly {
		fallback = data
	}
	return runCommandWithFallback(ctx, path, []string{
		"-q", strconv.Itoa(quality), "-m", "4", "-mt", "-alpha_q", "100",
		"-metadata", "all", "-quiet", "-o", "-", "--", "-",
	}, data, maxWebPBytes, fallback)
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
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("cwebp 已取消：%w", err)
	}
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stdout := &limitedOutput{limit: limit, cancel: cancel, fallback: fallback}
	stderr := toolio.NewDiagnostic(maxStderr)
	cmd := exec.CommandContext(childCtx, path, args...)
	cmd.Stdin = bytes.NewReader(data)
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
	buf       bytes.Buffer
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
		w.buf = bytes.Buffer{}
		w.discarded = true
	}
	if !w.discarded {
		_, _ = w.buf.Write(data[:size])
	}
	if size < len(data) {
		w.overflow = true
		w.cancel()
		return size, errOutputLimit
	}
	return size, nil
}
